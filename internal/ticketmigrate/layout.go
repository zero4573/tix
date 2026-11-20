package ticketmigrate

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/zero4573/tix/internal/links"
	tnote "github.com/zero4573/tix/internal/note"
)

type Move struct {
	ID, Source string
	From, To   string
}

type LayoutResult struct {
	Moves     []Move
	Files     []links.Moved
	Rewritten int
	Changed   []string
	Warnings  []string
}

func (r LayoutResult) Rels() []string {
	var out []string
	for _, f := range r.Files {
		out = append(out, f.From, f.To)
	}
	return append(out, r.Changed...)
}

var moveTrees = links.MoveTrees

func MigrateLayout(vault string, dryRun bool) (LayoutResult, error) {
	var res LayoutResult
	var trees []links.Tree
	plan := func(root, rootRel string, l tnote.Loc) {
		src := strings.TrimSpace(tnote.Get(l.Note(), "source"))
		from := rootRel + "/" + l.ID
		if !tnote.ValidSource(src) {
			if src != "" {
				res.Warnings = append(res.Warnings, fmt.Sprintf("%s/%s.md: source %q can't name a folder; placed by its ID", from, l.ID, src))
			}
			src = tnote.GuessSource(vault, l.ID)
		}
		to := filepath.Join(root, src, l.ID)
		res.Moves = append(res.Moves, Move{ID: l.ID, Source: src, From: from, To: rootRel + "/" + src + "/" + l.ID})
		trees = append(trees, links.Tree{Src: l.Dir, Dst: to, Last: l.ID + ".md"})
	}
	for _, r := range []struct{ root, rel string }{
		{tnote.TicketsDir(vault), "tickets"},
		{tnote.ArchiveRoot(vault), "archive/tickets"},
	} {
		for _, l := range dirsIn(r.root) {
			plan(r.root, r.rel, l)
		}
	}
	var ok []links.Tree
	var moves []Move
	for i, t := range trees {
		if taken := takenIn(t); taken != "" {
			r, _ := filepath.Rel(vault, taken)
			res.Warnings = append(res.Warnings, fmt.Sprintf("%s: not moved: %s exists already; merge the two folders by hand", res.Moves[i].From, filepath.ToSlash(r)))
			continue
		}
		ok = append(ok, t)
		moves = append(moves, res.Moves[i])
	}
	res.Moves = moves
	if dryRun || len(ok) == 0 {
		return res, nil
	}
	mv, err := moveTrees(vault, ok)
	res.Files, res.Rewritten, res.Changed = mv.Moves, mv.Rewritten, mv.Changed
	return res, err
}

func dirsIn(root string) []tnote.Loc {
	var out []tnote.Loc
	for _, l := range tnote.DirsUnder(root) {
		if l.Source == "" {
			out = append(out, l)
		}
	}
	return out
}

func takenIn(t links.Tree) string {
	var taken string
	_ = filepath.WalkDir(t.Src, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || taken != "" {
			return nil
		}
		r, _ := filepath.Rel(t.Src, p)
		if dst := filepath.Join(t.Dst, r); isFile(dst) {
			taken = dst
		}
		return nil
	})
	return taken
}

func isFile(p string) bool {
	_, err := os.Lstat(p)
	return err == nil
}
