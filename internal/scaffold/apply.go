package scaffold

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/zero4573/tix/internal/vaultlock"
)

var beforeRemove func(abs string)

type Options struct {
	DryRun  bool
	Take    []string
	Version string
	Owner   string
	AddOnly bool
	Locked  bool
}

const (
	Added   = "added"
	Updated = "updated"
	Taken   = "taken"
	Deleted = "removed"
)

type Result struct {
	Entries  []Entry
	Done     map[string]string
	Warnings []string
}

func (r Result) Count(s State) int {
	n := 0
	for _, e := range r.Entries {
		if e.State == s {
			n++
		}
	}
	return n
}

func Apply(vault string, src Source, o Options) (Result, error) {
	ship := map[string]bool{}
	for _, f := range src.Files {
		ship[f.Rel] = true
	}
	take := map[string]bool{}
	for _, t := range o.Take {
		rel, err := takeRel(vault, t, func(rel string) bool { return ship[rel] || len(src.History[rel]) > 0 })
		if err != nil {
			return Result{}, err
		}
		switch {
		case ship[rel]:
			take[rel] = true
		case len(src.History[rel]) > 0:
			return Result{}, fmt.Errorf("%s isn't shipped any more", rel)
		default:
			return Result{}, fmt.Errorf("not a file tix ships: %s", rel)
		}
	}

	if !o.DryRun && !o.Locked {
		owner := o.Owner
		if owner == "" {
			owner = "tix-vault-update"
		}
		owner = UniqueOwner(owner)
		if err := vaultlock.Acquire(vault, owner); err != nil {
			return Result{}, err
		}
		defer func() { _ = vaultlock.Release(vault, owner) }()
	}

	rec, state, warning := loadRecord(vault)
	res := Result{Done: map[string]string{}}
	if warning != "" {
		res.Warnings = append(res.Warnings, warning)
	}
	entries, err := Classify(vault, src, rec)
	if err != nil {
		return Result{}, err
	}
	res.Entries = entries

	for _, e := range entries {
		if !take[e.Rel] || e.State != Edited && e.State != Newer {
			continue
		}
		if e.Reason != "" {
			return Result{}, fmt.Errorf("%s: %s; move it away first", e.Rel, e.Reason)
		}
		bak := filepath.Join(vault, filepath.FromSlash(e.Rel)) + ".bak"
		if _, err := os.Lstat(bak); err == nil {
			return Result{}, fmt.Errorf("%s.bak exists; move it away first", e.Rel)
		} else if !errors.Is(err, fs.ErrNotExist) {
			return Result{}, err
		}
	}

	for _, e := range entries {
		switch {
		case e.State == Missing:
			res.Done[e.Rel] = Added
		case e.State == Stale && !o.AddOnly:
			res.Done[e.Rel] = Updated
		case (e.State == Edited || e.State == Newer) && take[e.Rel]:
			res.Done[e.Rel] = Taken
		case e.State == RetiredClean && IsRemoved(e.Rel) && !o.AddOnly:
			res.Done[e.Rel] = Deleted
		default:
			continue
		}
		if o.DryRun {
			continue
		}
		dest := filepath.Join(vault, filepath.FromSlash(e.Rel))
		if res.Done[e.Rel] == Deleted {
			if beforeRemove != nil {
				beforeRemove(dest)
			}
			if _, now, reason, err := look(dest, e.Rel); err == nil && (reason != "" || now != e.Have) {
				delete(res.Done, e.Rel)
				res.Warnings = append(res.Warnings, fmt.Sprintf("%s: changed during the update; kept", e.Rel))
				continue
			}
			if err := os.Remove(dest); err != nil && !errors.Is(err, fs.ErrNotExist) {
				return res, err
			}
			continue
		}
		var mode fs.FileMode
		keep := false
		if st, err := os.Lstat(dest); err == nil {
			mode, keep = st.Mode(), true
		}
		if res.Done[e.Rel] == Taken {
			if err := os.Rename(dest, dest+".bak"); err != nil {
				return res, err
			}
		}
		if err := writeAtomic(dest, e.Ship.Data, mode, keep); err != nil {
			return res, err
		}
	}
	if o.DryRun {
		return res, nil
	}

	files := map[string]Shipped{}
	set := func(rel, hash string) {
		if old, ok := rec.Files[rel]; ok && old.SHA256 == hash {
			files[rel] = old
		} else {
			files[rel] = Shipped{SHA256: hash, Version: o.Version}
		}
	}
	for _, e := range entries {
		switch {
		case res.Done[e.Rel] == Deleted:
		case res.Done[e.Rel] != "":
			set(e.Rel, e.Ship.Hash)
		case e.State == UpToDate, e.State == Stale:
			set(e.Rel, e.Have)
		default:
			if old, ok := rec.Files[e.Rel]; ok {
				files[e.Rel] = old
			}
		}
	}
	write := false
	switch state {
	case recordAbsent:
		write = len(files) > 0
	case recordOK:
		write = !sameRecord(rec.Files, files)
	case recordInvalid:
		write = len(res.Done) > 0
	case recordNewer:
	}
	if write {
		if err := saveRecord(vault, files); err != nil {
			return res, err
		}
	}
	return res, nil
}

func sameRecord(a, b map[string]Shipped) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if w, ok := b[k]; !ok || w != v {
			return false
		}
	}
	return true
}

func takeRel(vault, t string, known func(rel string) bool) (string, error) {
	clean := filepath.Clean(t)
	local := !filepath.IsAbs(clean) && filepath.IsLocal(clean)
	if local && known(filepath.ToSlash(clean)) {
		return filepath.ToSlash(clean), nil
	}
	abs, err := filepath.Abs(clean)
	if err != nil {
		return "", err
	}
	if rel, ok := inVault(vault, abs); ok {
		return rel, nil
	}
	if !local {
		return "", fmt.Errorf("%s is outside the vault", t)
	}
	return filepath.ToSlash(clean), nil
}

func inVault(vault, abs string) (string, bool) {
	root, err := filepath.Abs(vault)
	if err != nil {
		return "", false
	}
	rel := func(root, p string) (string, bool) {
		r, err := filepath.Rel(root, p)
		if err != nil || !filepath.IsLocal(r) {
			return "", false
		}
		return filepath.ToSlash(r), true
	}
	if r, ok := rel(root, abs); ok {
		return r, true
	}
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", false
	}
	dir, err := filepath.EvalSymlinks(filepath.Dir(abs))
	if err != nil {
		return "", false
	}
	return rel(realRoot, filepath.Join(dir, filepath.Base(abs)))
}
