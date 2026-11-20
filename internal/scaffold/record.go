package scaffold

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/zero4573/tix/internal/config"
)

const RecordName = ".scaffold.json"

const RecordFormat = 1

const recordComment = "What tix has shipped into this vault (tix vault init / tix vault update): for each file, the sha256 of the shipped content (LF line endings, one trailing newline; .base files are hashed over their parsed YAML) and the tix version that wrote or confirmed it. A file whose content still has this hash counts as unedited: tix vault update updates it, unless the hash is from a newer tix, which it keeps. Don't edit it by hand."

type Shipped struct {
	SHA256  string `json:"sha256"`
	Version string `json:"version"`
}

type Record struct {
	Comment string             `json:"_comment"`
	Format  int                `json:"format"`
	Files   map[string]Shipped `json:"files"`
}

type recordState int

const (
	recordAbsent recordState = iota
	recordOK
	recordNewer
	recordInvalid
)

func loadRecord(vault string) (Record, recordState, string) {
	file := filepath.Join(vault, RecordName)
	empty := Record{Files: map[string]Shipped{}}
	data, err := os.ReadFile(file)
	if errors.Is(err, fs.ErrNotExist) {
		return empty, recordAbsent, ""
	}
	if err != nil {
		return empty, recordInvalid, fmt.Sprintf("%s: %v; using the built-in history only", RecordName, err)
	}
	var rec Record
	if err := json.Unmarshal(data, &rec); err != nil {
		return empty, recordInvalid, fmt.Sprintf("%s isn't valid JSON (%v); using the built-in history only", RecordName, err)
	}
	if rec.Format > RecordFormat {
		return empty, recordNewer, fmt.Sprintf("%s is from a newer tix; using the built-in history only", RecordName)
	}
	if rec.Files == nil {
		rec.Files = map[string]Shipped{}
	}
	return rec, recordOK, ""
}

func saveRecord(vault string, files map[string]Shipped) error {
	return config.WriteJSON(filepath.Join(vault, RecordName), Record{Comment: recordComment, Format: RecordFormat, Files: files})
}

func writeAtomic(dest string, data []byte, mode fs.FileMode, keep bool) error {
	dir := filepath.Dir(dest)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	perm := fs.FileMode(0o644)
	if keep {
		perm = mode.Perm()
	}
	var tmp string
	var f *os.File
	for {
		tmp = filepath.Join(dir, "."+filepath.Base(dest)+"."+randomID()+".tix-tmp")
		var err error
		f, err = os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, perm)
		if err == nil {
			break
		}
		if !errors.Is(err, fs.ErrExist) {
			return err
		}
	}
	_, err := f.Write(data)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil && keep {
		err = os.Chmod(tmp, perm)
	}
	if err == nil {
		err = os.Rename(tmp, dest)
	}
	if err != nil {
		_ = os.Remove(tmp)
	}
	return err
}

func randomID() string {
	var b [4]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

func UniqueOwner(base string) string {
	return fmt.Sprintf("%s-%d-%s", base, os.Getpid(), randomID())
}
