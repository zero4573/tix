package clean

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/zero4573/tix/internal/followup"
	"github.com/zero4573/tix/internal/fsx"
	"github.com/zero4573/tix/internal/links"
	"github.com/zero4573/tix/internal/note"
	"github.com/zero4573/tix/internal/vaultlock"
	"github.com/zero4573/tix/internal/vindex"
)

const lockOwner = "tix-clean"

var refreshIndex = vindex.Refresh

type Env struct {
	Out             io.Writer
	Save            func(id string) (log string, err error)
	RemoveWorkspace func(id string) error
}

type Result struct {
	Cleaned  int
	Failed   bool
	FollowUp string
}

func rel(vault, p string) string {
	if r, err := filepath.Rel(vault, p); err == nil {
		return filepath.ToSlash(r)
	}
	return p
}

func Describe(o Options, items []Item, it Item) string {
	head := it.ID
	if it.Status != "" {
		head += " (" + it.Status + ")"
	}
	what := ""
	switch it.Action {
	case Skip:
		return head + ": skip: " + it.Reason
	case Keep:
		return head + ": keep (manual)"
	case Archive:
		what = "archive to " + rel(o.Vault, note.ArchiveDir(o.Vault, it.ID))
	case Trash, Purge:
		dir := note.TicketDir(o.Vault, it.ID)
		kept := links.Referenced(o.Vault, dir, gone(o, items, it.ID))
		n, notes, _ := links.RewriteToURL(o.Vault, note.TicketPath(o.Vault, it.ID), it.ID, it.URL, []string{dir},
			links.RewriteOpts{DryRun: true, Skip: SyncOwned()})
		what = "move to .trash/" + it.ID
		if it.Action == Purge {
			what = "delete"
		}
		what += fmt.Sprintf("; rewrite %d link(s) in %d note(s) to %s", n, notes, it.URL)
		if len(kept) > 0 {
			what += fmt.Sprintf("; keep %d file(s) in %s", len(kept), rel(o.Vault, note.ArchiveDir(o.Vault, it.ID)))
		}
	}
	if it.Save {
		what = "run /tix:save (in " + it.SaveIn + "), then " + what
	}
	if ws := o.wsDir(it.ID); hasWorkspace(ws) {
		what = "remove the workspace; " + what
	} else if fsx.IsDir(ws) {
		what = "leave " + ws + " (no workspace.json); " + what
	}
	return head + ": " + what
}

func gone(o Options, items []Item, except string) []string {
	var out []string
	for _, it := range items {
		if it.ID != except && (it.Action == Trash || it.Action == Purge) {
			out = append(out, note.TicketDir(o.Vault, it.ID))
		}
	}
	return out
}

func Run(o Options, items []Item, env Env) Result {
	out := env.Out
	var res Result
	saveRan := false
	var touched pathSet
	whole := false
	defer func() {
		if res.FollowUp != "" {
			touched.add(rel(o.Vault, res.FollowUp))
		}
		switch {
		case whole || saveRan:
			refreshIndex(o.Vault)
		case len(touched.list) > 0:
			refreshIndex(o.Vault, touched.list...)
		}
	}()
	var tasks []string
	say := func(format string, a ...any) { fmt.Fprintf(out, "tix clean: "+format+"\n", a...) }

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(stop)
	stopped := func() bool {
		select {
		case <-stop:
			say("interrupted: stopping before the next ticket (run tix clean again to finish)")
			res.Failed = true
			return true
		default:
			return false
		}
	}

	failedSave := map[string]string{}
	saved := map[string]bool{}
	start := time.Now().Truncate(time.Second)
	for _, it := range items {
		if !it.Save || saved[it.SaveIn] || failedSave[it.SaveIn] != "" {
			continue
		}
		if stopped() {
			return res
		}
		saveRan = true
		log, err := env.Save(it.SaveIn)
		if err != nil {
			failedSave[it.SaveIn] = fmt.Sprintf("/tix:save failed (%v); see %s", err, log)
		} else {
			saved[it.SaveIn] = true
		}
	}
	if len(saved)+len(failedSave) > 0 {
		plain := o
		plain.Save = false
		again := map[string]Item{}
		for _, it := range Plan(plain) {
			again[it.ID] = it
		}
		for i, it := range items {
			if !it.Save {
				continue
			}
			nit, ok := again[it.ID]
			switch {
			case failedSave[it.SaveIn] != "":
				nit = skip(it, failedSave[it.SaveIn], fmt.Sprintf("[[%s]] (%s): tix clean --save couldn't save it: %s. Run /tix:save in tix start %s, then tix clean %s",
					it.ID, it.Status, failedSave[it.SaveIn], it.SaveIn, it.ID))
			case !savedSince(o.Vault, it.ID, start):
				nit = skip(it, "/tix:save ran but didn't mark it saved", fmt.Sprintf(
					"[[%s]] (%s): tix clean --save ran /tix:save, but it didn't set saved:. Check it in tix start %s, then tix clean %s",
					it.ID, it.Status, it.SaveIn, it.ID))
			case !ok:
				nit = skip(it, "no longer a candidate after its save", "")
			}
			nit.Explicit = it.Explicit
			if nit.Action == Skip {
				say("%s: skipped: %s", it.ID, nit.Reason)
			}
			items[i] = nit
		}
		for i, it := range items {
			if it.Action == Skip || len(it.Covers) == 0 {
				continue
			}
			for _, c := range it.Covers {
				for _, other := range items {
					if other.ID == c && other.Action == Skip {
						items[i] = skip(it, "waiting on covered tickets: "+c, "")
					}
				}
			}
		}
	}

	removed := map[string]bool{}
	var changed []string
	for _, it := range Order(items) {
		if it.Action == Skip {
			if it.Task != "" {
				tasks = append(tasks, it.Task)
			}
			res.Failed = res.Failed || it.Explicit
			continue
		}
		if !it.Acts() {
			continue
		}
		if stopped() {
			break
		}
		wrote := false
		msg, more, err := one(o, env, items, it, &changed, &touched, &wrote)
		tasks = append(tasks, more...)
		if err != nil {
			whole = whole || wrote
			say("%s: %v", it.ID, err)
			res.Failed = true
			continue
		}
		removed[strings.ToLower(it.ID)] = true
		res.Cleaned++
		say("%s: %s", it.ID, msg)
	}

	var still []string
	for _, f := range changed {
		if _, err := os.Stat(f); err != nil {
			continue
		}
		still = append(still, f)
	}
	if len(still) > 0 {
		owned := SyncOwned()
		for _, p := range links.Problems(o.Vault, still) {
			base := strings.ToLower(p.Target[strings.LastIndex(p.Target, "/")+1:])
			if len(p.Found) == 0 && removed[base] && !owned(p.Place) {
				say("warning: %s:%d still links [[%s]]", rel(o.Vault, p.File), p.Line, p.Target)
			}
		}
	}

	if len(tasks) > 0 {
		n, err := followup.Write(o.Vault, "tix-clean", "tix clean", tasks)
		if err != nil {
			say("couldn't write the follow-up note: %v", err)
		} else if n != "" {
			res.FollowUp = n
			say("follow-ups in %s", n)
		}
	}
	return res
}

func savedSince(vault, id string, start time.Time) bool {
	t, err := time.ParseInLocation("2006-01-02T15:04:05", note.Get(note.TicketPath(vault, id), "saved"), time.Local)
	return err == nil && !t.Before(start)
}

func one(o Options, env Env, items []Item, it Item, changed *[]string, touched *pathSet, wrote *bool) (msg string, tasks []string, err error) {
	if err := vaultlock.Acquire(o.Vault, lockOwner); err != nil {
		return "", nil, err
	}
	defer func() {
		if rerr := vaultlock.Release(o.Vault, lockOwner); rerr != nil && err == nil {
			err = rerr
		}
	}()
	now, _ := planOne(withoutSave(o), it.ID, true)
	if now.Action != it.Action {
		reason := now.Reason
		if reason == "" {
			reason = "its plan changed"
		}
		return "", nil, errors.New("skipped: " + reason)
	}

	var parts []string
	switch ws := o.wsDir(it.ID); {
	case hasWorkspace(ws):
		if err := env.RemoveWorkspace(it.ID); err != nil {
			return "", nil, fmt.Errorf("workspace not removed (%v); nothing else changed", err)
		}
		if fsx.IsDir(ws) {
			parts = append(parts, "removed its workspace, but left "+ws+" in place: it still has other files")
		} else {
			parts = append(parts, "removed its workspace")
		}
	case fsx.IsDir(ws):
		parts = append(parts, "left "+ws+" in place (no workspace.json)")
	}

	dir := note.TicketDir(o.Vault, it.ID)
	archive := note.ArchiveDir(o.Vault, it.ID)
	if it.Action == Archive {
		*wrote = true
		mv, err := links.MoveTree(o.Vault, dir, archive, nil, it.ID+".md")
		touched.addMove(mv)
		if err != nil {
			return "", nil, err
		}
		return strings.Join(append(parts, fmt.Sprintf("archived %d file(s) to %s", len(mv.Moves), rel(o.Vault, archive))), "; "), nil, nil
	}

	kept := links.Referenced(o.Vault, dir, gone(o, items, it.ID))
	if len(kept) > 0 {
		*wrote = true
		mv, err := links.MoveTree(o.Vault, dir, archive, kept, "")
		touched.addMove(mv)
		if err != nil {
			return "", nil, err
		}
	}
	*wrote = true
	n, notes, err := links.RewriteToURL(o.Vault, note.TicketPath(o.Vault, it.ID), it.ID, it.URL, []string{dir}, links.RewriteOpts{
		Skip: SyncOwned(),
		NotRewritten: func(p links.Place) {
			tasks = append(tasks, fmt.Sprintf("%s:%d still links %s (removed by tix clean) in a frontmatter form it can't rewrite: make it \"[%s](%s)\"",
				rel(o.Vault, p.File), p.Line, it.ID, it.ID, it.URL))
		},
		Changed: func(f string) { *changed = append(*changed, f); touched.add(rel(o.Vault, f)) },
	})
	if err != nil {
		return "", tasks, err
	}
	touched.addTree(o.Vault, dir)
	where := ""
	if it.Action == Purge {
		*wrote = true
		if err := fsx.RemoveAll(dir); err != nil {
			return "", tasks, err
		}
		where = "deleted"
	} else {
		trash := filepath.Join(o.Vault, ".trash", it.ID)
		if _, err := os.Lstat(trash); err == nil {
			trash += "-" + time.Now().Format("20060102150405")
		}
		if err := os.MkdirAll(filepath.Dir(trash), 0o755); err != nil {
			return "", tasks, err
		}
		*wrote = true
		if err := os.Rename(dir, trash); err != nil {
			return "", tasks, err
		}
		where = "moved to " + rel(o.Vault, trash)
	}
	parts = append(parts, where, fmt.Sprintf("rewrote %d link(s) in %d note(s)", n, notes))
	if len(kept) > 0 {
		parts = append(parts, fmt.Sprintf("kept %d file(s) in %s", len(kept), rel(o.Vault, archive)))
	}
	return strings.Join(parts, "; "), tasks, nil
}

func withoutSave(o Options) Options {
	o.Save = false
	return o
}

type pathSet struct {
	list []string
	seen map[string]bool
}

func (s *pathSet) add(paths ...string) {
	if s.seen == nil {
		s.seen = map[string]bool{}
	}
	for _, p := range paths {
		if !s.seen[p] {
			s.seen[p] = true
			s.list = append(s.list, p)
		}
	}
}

func (s *pathSet) addMove(r links.MoveResult) {
	for _, m := range r.Moves {
		s.add(m.From, m.To)
	}
	s.add(r.Changed...)
}

func (s *pathSet) addTree(vault, dir string) {
	_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			s.add(rel(vault, p))
		}
		return nil
	})
}
