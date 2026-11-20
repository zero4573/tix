package vindex

import (
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type FileInfo struct {
	MTime int64
	Size  int64
}

type Source interface {
	List() ([]string, error)
	Normalize(path string) (rel string, ok bool)
	Stat(rel string) (FileInfo, error)
	Read(rel string) ([]byte, error)
}

var skippedTop = map[string]bool{"raw": true, "archive": true, "templates": true}

func indexable(rel string) bool {
	if !strings.HasSuffix(rel, ".md") {
		return false
	}
	parts := strings.Split(rel, "/")
	for _, p := range parts {
		if strings.HasPrefix(p, ".") {
			return false
		}
	}
	if len(parts) == 1 {
		return true
	}
	if skippedTop[parts[0]] {
		return false
	}
	if len(parts) > 4 && parts[0] == "tickets" && (parts[3] == "specs-base" || parts[3] == "specs-after" || parts[3] == "specs-stage") {
		return false
	}
	return true
}

type vaultSource struct {
	root, realRoot string
	dirOK          map[string]bool
}

func VaultSource(vault string) Source {
	real, _ := filepath.EvalSymlinks(vault)
	return &vaultSource{root: vault, realRoot: real, dirOK: map[string]bool{}}
}

func (v *vaultSource) List() ([]string, error) {
	var out []string
	_ = filepath.WalkDir(v.root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		rel, _ := filepath.Rel(v.root, p)
		if rel == "." {
			return nil
		}
		if d.IsDir() {
			if strings.HasPrefix(d.Name(), ".") || (filepath.Dir(rel) == "." && skippedTop[d.Name()]) {
				return filepath.SkipDir
			}
			return nil
		}
		if r := filepath.ToSlash(rel); d.Type().IsRegular() && indexable(r) {
			out = append(out, r)
		}
		return nil
	})
	sort.Strings(out)
	return out, nil
}

func (v *vaultSource) Normalize(f string) (string, bool) {
	f = filepath.ToSlash(f)
	if filepath.IsAbs(f) {
		if r, err := filepath.Rel(v.root, filepath.FromSlash(f)); err == nil {
			f = filepath.ToSlash(r)
		}
	}
	return f, indexable(f)
}

func (v *vaultSource) Stat(rel string) (FileInfo, error) {
	d := filepath.Dir(rel)
	ok, seen := v.dirOK[d]
	if !seen {
		r, err := filepath.EvalSymlinks(filepath.Join(v.root, filepath.FromSlash(d)))
		ok = err == nil && r == filepath.Join(v.realRoot, filepath.FromSlash(d))
		v.dirOK[d] = ok
	}
	if !ok {
		return FileInfo{}, fs.ErrNotExist
	}
	fi, err := os.Lstat(filepath.Join(v.root, filepath.FromSlash(rel)))
	if err != nil {
		return FileInfo{}, err
	}
	if !fi.Mode().IsRegular() {
		return FileInfo{}, fs.ErrInvalid
	}
	return FileInfo{MTime: fi.ModTime().UnixNano(), Size: fi.Size()}, nil
}

func (v *vaultSource) Read(rel string) ([]byte, error) {
	return os.ReadFile(filepath.Join(v.root, filepath.FromSlash(rel)))
}
