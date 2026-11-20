package diffcheck

import (
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/zero4573/tix/internal/gitx"
	"github.com/zero4573/tix/internal/spec"
)

type SpecHit struct {
	ID     string
	Commit string
	Path   string
	Reason string
}

func splitRange(dir, revRange string) (start, base, end string, ok bool, err error) {
	var l, r string
	if i := strings.Index(revRange, "..."); i >= 0 {
		l, r = revRange[:i], revRange[i+3:]
	} else if i := strings.Index(revRange, ".."); i >= 0 {
		l, r = revRange[:i], revRange[i+2:]
	} else {
		return "", "", "", false, nil
	}
	if l == "" {
		l = "HEAD"
	}
	if r == "" {
		r = "HEAD"
	}
	rev := func(s string) (string, error) {
		out, err := gitx.Out(dir, "rev-parse", "--verify", "--end-of-options", s+"^{commit}")
		return strings.TrimSpace(out), err
	}
	if start, err = rev(l); err != nil {
		return
	}
	if end, err = rev(r); err != nil {
		return
	}
	mb, err := gitx.Out(dir, "merge-base", start, end)
	if err != nil {
		return start, "", end, true, nil
	}
	base = strings.TrimSpace(mb)
	return start, base, end, true, nil
}

func specRefs(dir, rev string) ([]spec.IDRef, error) {
	if rev == "" {
		return nil, nil
	}
	out, err := gitx.Out(dir, "ls-tree", "-r", "--name-only", "-z", rev, "--", "specs")
	if err != nil {
		return nil, err
	}
	var refs []spec.IDRef
	for _, p := range strings.Split(out, "\x00") {
		if p == "" || path.Base(p) != "spec.md" || path.Dir(path.Dir(p)) != "specs" {
			continue
		}
		src, err := gitx.Out(dir, "cat-file", "blob", rev+":"+p)
		if err != nil {
			return nil, err
		}
		refs = append(refs, spec.FindIDs(p, []byte(src))...)
	}
	return refs, nil
}

func CheckSpecIDs(dir, revRange string) ([]SpecHit, error) {
	start, base, end, ok, err := splitRange(dir, revRange)
	if err != nil || !ok {
		return nil, err
	}
	endRefs, err := specRefs(dir, end)
	if err != nil || len(endRefs) == 0 {
		return nil, err
	}
	baseRefs, err := specRefs(dir, base)
	if err != nil {
		return nil, err
	}
	startRefs := baseRefs
	if start != base {
		if startRefs, err = specRefs(dir, start); err != nil {
			return nil, err
		}
	}
	inBase := map[string]bool{}
	for _, r := range baseRefs {
		inBase[r.ID] = true
	}
	inStart := map[string]bool{}
	for _, r := range startRefs {
		inStart[r.ID] = true
	}
	short := ShortSHA(end)
	seen := map[string]bool{}
	var hits []SpecHit
	add := func(r spec.IDRef, reason string) {
		k := reason + "\x00" + r.ID + "\x00" + r.File
		if !seen[k] {
			seen[k] = true
			hits = append(hits, SpecHit{ID: r.ID, Commit: short, Path: r.File, Reason: reason})
		}
	}
	for _, r := range endRefs {
		if !inBase[r.ID] && inStart[r.ID] {
			add(r, "clash")
		}
	}
	dups := spec.Duplicates(endRefs)
	for _, id := range spec.SortedIDs(dups) {
		for _, r := range dups[id] {
			add(r, "duplicate")
		}
	}
	sort.SliceStable(hits, func(i, j int) bool { return hits[i].ID < hits[j].ID })
	return hits, nil
}

func (h SpecHit) String() string {
	return fmt.Sprintf("%s\t%s\t%s", h.ID, h.Commit, h.Path)
}
