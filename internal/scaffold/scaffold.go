package scaffold

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/zero4573/tix/assets"
)

const Skipped = "obsidian-types.json"

type File struct {
	Rel  string
	Data []byte
	Hash string
}

type Source struct {
	Files   []File
	History map[string][]string
}

func Embedded() (Source, error) {
	files, err := FromFS(assets.Scaffold, "vault-scaffold")
	if err != nil {
		return Source{}, err
	}
	hist, err := ParseHistory(assets.ScaffoldHistory)
	if err != nil {
		return Source{}, fmt.Errorf("the embedded scaffold history: %w", err)
	}
	return Source{Files: files, History: hist}, nil
}

func FromFS(fsys fs.FS, root string) ([]File, error) {
	var files []File
	err := fs.WalkDir(fsys, root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel := strings.TrimPrefix(p, root+"/")
		if rel == Skipped {
			return nil
		}
		data, err := fs.ReadFile(fsys, p)
		if err != nil {
			return err
		}
		files = append(files, File{Rel: rel, Data: data, Hash: Key(rel, data)})
		return nil
	})
	sort.Slice(files, func(i, j int) bool { return files[i].Rel < files[j].Rel })
	return files, err
}

func Norm(data []byte) []byte {
	out := bytes.ReplaceAll(data, []byte("\r\n"), []byte("\n"))
	out = bytes.TrimRight(out, "\n")
	if len(out) == 0 {
		return []byte{}
	}
	return append(out, '\n')
}

func Hash(data []byte) string {
	sum := sha256.Sum256(Norm(data))
	return hex.EncodeToString(sum[:])
}

func Key(rel string, data []byte) string {
	if !strings.HasSuffix(rel, ".base") {
		return Hash(data)
	}
	var v any
	dec := yaml.NewDecoder(bytes.NewReader(data))
	if err := dec.Decode(&v); err != nil {
		return Hash(data)
	}
	var extra any
	if err := dec.Decode(&extra); err == nil {
		return Hash(data)
	}
	j, err := json.Marshal(v)
	if err != nil {
		return Hash(data)
	}
	sum := sha256.Sum256(j)
	return hex.EncodeToString(sum[:])
}

type State int

const (
	Missing State = iota
	UpToDate
	Stale
	Edited
	RetiredClean
	RetiredEdited
	Newer
)

type Entry struct {
	Rel    string
	State  State
	Have   string
	Ship   *File
	Reason string
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func look(dest, rel string) (exists bool, hash, reason string, err error) {
	st, err := os.Lstat(dest)
	if errors.Is(err, fs.ErrNotExist) {
		return false, "", "", nil
	}
	if err != nil {
		return false, "", "", err
	}
	if !st.Mode().IsRegular() {
		return true, "", "not a regular file", nil
	}
	data, err := os.ReadFile(dest)
	if err != nil {
		return false, "", "", err
	}
	return true, Key(rel, data), "", nil
}

func Classify(vault string, src Source, rec Record) ([]Entry, error) {
	var entries []Entry
	shipped := map[string]bool{}
	for i := range src.Files {
		f := &src.Files[i]
		shipped[f.Rel] = true
		exists, have, reason, err := look(filepath.Join(vault, filepath.FromSlash(f.Rel)), f.Rel)
		if err != nil {
			return nil, err
		}
		e := Entry{Rel: f.Rel, Have: have, Ship: f, Reason: reason}
		r, recorded := rec.Files[f.Rel]
		switch {
		case !exists:
			e.State = Missing
		case reason != "":
			e.State = Edited
		case have == f.Hash:
			e.State = UpToDate
		case recorded && r.SHA256 == have && !contains(src.History[f.Rel], have):
			e.State = Newer
		case contains(src.History[f.Rel], have):
			e.State = Stale
		default:
			e.State = Edited
		}
		entries = append(entries, e)
	}

	retired := map[string]bool{}
	for rel := range src.History {
		retired[rel] = !shipped[rel]
	}
	for rel := range rec.Files {
		retired[rel] = !shipped[rel]
	}
	for rel, ok := range retired {
		if !ok || !filepath.IsLocal(filepath.FromSlash(rel)) {
			continue
		}
		exists, have, reason, err := look(filepath.Join(vault, filepath.FromSlash(rel)), rel)
		if err != nil {
			return nil, err
		}
		if !exists {
			continue
		}
		e := Entry{Rel: rel, Have: have, Reason: reason, State: RetiredEdited}
		r, recorded := rec.Files[rel]
		switch {
		case reason != "":
		case contains(src.History[rel], have):
			e.State = RetiredClean
		case recorded && r.SHA256 == have:
			e.State = Newer
		}
		entries = append(entries, e)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Rel < entries[j].Rel })
	return entries, nil
}

var Removed = []string{"done.md", "follow-ups.md", "pending.md"}

func IsRemoved(rel string) bool { return contains(Removed, rel) }
