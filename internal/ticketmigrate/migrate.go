package ticketmigrate

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/zero4573/tix/internal/fsx"
	tnote "github.com/zero4573/tix/internal/note"
)

type FileChange struct {
	Rel      string
	Old, New []byte
}

type TicketChange struct {
	ID          string
	Dir         string
	StatusAdded bool
	StatusRel   string
	Files       []FileChange
}

func (t TicketChange) Rels() []string {
	var r []string
	if t.StatusAdded {
		r = append(r, t.StatusRel)
	}
	for _, f := range t.Files {
		r = append(r, f.Rel)
	}
	return r
}

type Result struct {
	Tickets  []TicketChange
	Warnings []string
	Migrated int
	Current  int
	Closed   int
}

var writeStatus = fsx.WriteFileAtomic

var beforeWrite func(abs string)

func Migrate(vault string, statusTpl []byte, today string, dryRun bool) (Result, error) {
	var res Result
	for _, l := range tnote.TicketDirs(vault) {
		id := l.ID
		relDir := filepath.ToSlash(mustRel(vault, l.Dir))
		note, err := os.ReadFile(l.Note())
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			res.Warnings = append(res.Warnings, fmt.Sprintf("%s/%s.md: %v; skipped", relDir, id, err))
			continue
		}
		if status(note) == "closed" {
			res.Closed++
			continue
		}
		tc, err := migrateTicket(vault, l.Dir, relDir, id, statusTpl, today, dryRun, &res)
		if err != nil {
			return res, err
		}
		if tc.StatusAdded || len(tc.Files) > 0 {
			res.Migrated++
			res.Tickets = append(res.Tickets, tc)
		} else {
			res.Current++
		}
	}
	return res, nil
}

func mustRel(base, p string) string {
	r, err := filepath.Rel(base, p)
	if err != nil {
		return p
	}
	return r
}

func status(note []byte) string {
	s := strings.TrimPrefix(string(note), "\xef\xbb\xbf")
	lines := strings.Split(s, "\n")
	if len(lines) == 0 || strings.TrimRight(lines[0], " \t\r") != "---" {
		return ""
	}
	for _, l := range lines[1:] {
		l = strings.TrimRight(l, " \t\r")
		if l == "---" {
			break
		}
		if v, ok := strings.CutPrefix(l, "status:"); ok {
			return strings.ToLower(strings.Trim(strings.TrimSpace(v), `"'`))
		}
	}
	return ""
}

func migrateTicket(vault, dir, relDir, id string, statusTpl []byte, today string, dryRun bool, res *Result) (TicketChange, error) {
	tc := TicketChange{ID: id, Dir: relDir, StatusRel: path.Join(relDir, id+"-status.md")}
	warn := func(format string, a ...any) { res.Warnings = append(res.Warnings, fmt.Sprintf(format, a...)) }

	statusFailed := false
	sp := filepath.Join(dir, id+"-status.md")
	if _, err := os.Lstat(sp); errors.Is(err, fs.ErrNotExist) {
		data := strings.NewReplacer("TICKET-ID", id, "{{date}}", today).Replace(string(statusTpl))
		if !dryRun {
			if err := writeStatus(sp, []byte(data), 0o644); err != nil {
				warn("%s: %v; not written (the ticket's other files are converted; run it again to add it)", tc.StatusRel, err)
				statusFailed = true
			}
		}
		tc.StatusAdded = !statusFailed
	} else if err != nil {
		return tc, err
	}

	convert := func(rel string, conv func([]byte) Converted) {
		abs := filepath.Join(dir, filepath.FromSlash(rel))
		vrel := path.Join(relDir, rel)
		data, err := os.ReadFile(abs)
		if err != nil {
			if !errors.Is(err, fs.ErrNotExist) {
				warn("%s: %v; skipped", vrel, err)
			}
			return
		}
		c := conv(data)
		for _, p := range c.Problems {
			warn("%s:%d: not converted (%s); convert it by hand", vrel, p.Line, p.Why)
		}
		for _, p := range c.Warnings {
			warn("%s:%d: %s", vrel, p.Line, p.Why)
		}
		if !c.Changed {
			return
		}
		if !dryRun {
			if beforeWrite != nil {
				beforeWrite(abs)
			}
			if now, err := os.ReadFile(abs); err != nil || !bytes.Equal(now, data) {
				warn("%s: changed during the update; run it again", vrel)
				return
			}
			if err := fsx.WriteNote(abs, c.Data); err != nil {
				warn("%s: %v; not converted", vrel, err)
				return
			}
		}
		tc.Files = append(tc.Files, FileChange{Rel: vrel, Old: data, New: c.Data})
	}
	convert("board.md", ConvertBoard)
	convert("backlog.md", ConvertBacklog)
	revs, _ := filepath.Glob(filepath.Join(dir, "reviews", "*.md"))
	sort.Strings(revs)
	for _, r := range revs {
		if st, err := os.Stat(r); err == nil && st.Mode().IsRegular() {
			convert("reviews/"+filepath.Base(r), ConvertReview)
		}
	}
	return tc, nil
}
