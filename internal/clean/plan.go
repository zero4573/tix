package clean

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/zero4573/tix/internal/links"
	"github.com/zero4573/tix/internal/note"
	"github.com/zero4573/tix/internal/workspace"
)

type Action int

const (
	Skip Action = iota
	Trash
	Purge
	Archive
	Keep
)

type Options struct {
	Vault, WorkRoot         string
	IDs                     []string
	IncludeDone, KeepManual bool
	Purge, Save             bool
	Days                    int
	Now                     time.Time
	Running                 func(dir string) bool
}

type Item struct {
	ID, Status string
	Action     Action
	Reason     string
	Task       string
	Explicit   bool
	Save       bool
	SaveIn     string
	URL        string
	Lead       string
	Covers     []string
}

func (it Item) Acts() bool { return it.Save || it.Action != Skip && it.Action != Keep }

func (o Options) running(dir string) bool {
	if o.Running != nil {
		return o.Running(dir)
	}
	return workspace.Running(dir)
}

func (o Options) wsDir(id string) string { return filepath.Join(o.WorkRoot, id) }

func hasWorkspace(dir string) bool {
	_, err := os.Stat(workspace.File(dir))
	return err == nil
}

func finished(status string) bool { return status == "done" || status == "closed" }

func linkTarget(s string) string {
	return strings.TrimSpace(strings.NewReplacer("[", "", "]", "", `"`, "", "'", "").Replace(s))
}

func coversOf(file string) []string {
	f := note.Fields(file)
	if f["ticket-type"] != "epic" {
		return nil
	}
	var out []string
	for _, item := range strings.Split(f["covers"], ",") {
		if id := linkTarget(item); note.ValidKey(id) {
			out = append(out, id)
		}
	}
	return out
}

func usableURL(s string) bool {
	u, err := url.Parse(s)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != ""
}

func Plan(o Options) []Item {
	if o.Now.IsZero() {
		o.Now = time.Now()
	}
	ids := o.IDs
	explicit := len(ids) > 0
	if !explicit {
		for _, t := range note.List(o.Vault, true) {
			ids = append(ids, t.ID)
		}
	}
	var items []Item
	seen := map[string]bool{}
	for _, id := range ids {
		if seen[id] {
			continue
		}
		seen[id] = true
		if it, ok := planOne(o, id, explicit); ok {
			items = append(items, it)
		}
	}
	for changed := true; changed; {
		changed = false
		planned := map[string]bool{}
		for _, it := range items {
			if it.Action != Skip {
				planned[it.ID] = true
			}
		}
		for i, it := range items {
			if it.Action == Skip || len(it.Covers) == 0 {
				continue
			}
			var waiting []string
			for _, c := range it.Covers {
				if _, err := os.Stat(note.TicketPath(o.Vault, c)); err == nil && !planned[c] {
					waiting = append(waiting, c)
				}
			}
			if len(waiting) > 0 {
				items[i] = skip(it, "waiting on covered tickets: "+strings.Join(waiting, ", "), "")
				changed = true
			}
		}
	}
	return items
}

func skip(it Item, reason, task string) Item {
	it.Action, it.Reason, it.Task, it.Save = Skip, reason, task, false
	return it
}

func planOne(o Options, id string, explicit bool) (Item, bool) {
	it := Item{ID: id, Explicit: explicit}
	file := note.TicketPath(o.Vault, id)
	if _, err := os.Stat(file); !note.ValidKey(id) || err != nil {
		return skip(it, "not a ticket in the vault", ""), explicit
	}
	fm := note.Frontmatter(file)
	it.Status = fm["status"]
	synced := note.Synced(fm)
	switch {
	case it.Status == "closed", it.Status == "done" && o.IncludeDone:
	case it.Status == "done":
		return skip(it, "done, not closed (--include-done to clean it)", ""), explicit
	default:
		st := it.Status
		if st == "" {
			st = "none"
		}
		return skip(it, "not finished (status "+st+")", ""), explicit
	}
	if it.Status == "done" && synced {
		return skip(it, "done synced ticket (tix sync would re-create it)", ""), true
	}
	if o.Days > 0 {
		if age := ageDays(file, fm["updated"], o.Now); age < o.Days {
			return skip(it, fmt.Sprintf("updated %d day(s) ago (--days %d)", age, o.Days), ""), true
		}
	}
	it.URL = fm["source-url"]
	remote := synced && usableURL(it.URL)
	if !remote && o.KeepManual {
		it.Action, it.Covers = Keep, coversOf(file)
		return it, true
	}
	dirs := []string{o.wsDir(id)}
	if lead := linkTarget(fm["covered-by"]); lead != "" {
		it.Lead = lead
		if st := note.Get(note.TicketPath(o.Vault, lead), "status"); st != "" && !finished(st) {
			return skip(it, "lead "+lead+" still open", ""), true
		}
		dirs = append(dirs, o.wsDir(lead))
	}
	it.Covers = coversOf(file)
	if reason, task := safety(o, id, it.Status, dirs); reason != "" {
		return skip(it, reason, task), true
	}

	saveIn := ""
	for _, d := range dirs {
		if hasWorkspace(d) {
			saveIn = d
			break
		}
	}
	if state, why := note.SaveStateOf(o.Vault, saveIn, id); state == note.Unsaved {
		if !o.Save || saveIn == "" {
			return skip(it, "needs /tix:save ("+why+")", fmt.Sprintf(
				"[[%s]] (%s) wasn't saved (%s): tix start %s, run /tix:save, then tix clean %s (or tix clean --save %s)",
				id, it.Status, why, filepath.Base(firstNonEmpty(saveIn, o.wsDir(id))), id, id)), true
		}
		if slug := nullVersion(saveIn); slug != "" {
			return skip(it, "needs /tix:save, and "+slug+" has no target version (a headless save can't ask)", fmt.Sprintf(
				"[[%s]] (%s) wasn't saved, and its workspace has no target version for %s: tix start %s, run /tix:save, then tix clean %s",
				id, it.Status, slug, filepath.Base(saveIn), id)), true
		}
		it.Save, it.SaveIn = true, filepath.Base(saveIn)
	}

	switch {
	case remote && o.Purge:
		it.Action = Purge
	case remote:
		it.Action = Trash
	default:
		it.Action = Archive
	}
	return it, true
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func nullVersion(dir string) string {
	info, _ := workspace.Read(dir)
	for _, r := range info.Repos {
		if r.TargetVersion == nil || *r.TargetVersion == "" {
			return r.Slug
		}
	}
	return ""
}

func ageDays(file, updated string, now time.Time) int {
	t, err := time.ParseInLocation("2006-01-02", strings.TrimSpace(updated), time.Local)
	if err != nil {
		st, serr := os.Stat(file)
		if serr != nil {
			return 0
		}
		t = st.ModTime()
	}
	y, m, d := now.Date()
	today := time.Date(y, m, d, 0, 0, 0, 0, time.Local)
	y, m, d = t.Date()
	return max(0, int(today.Sub(time.Date(y, m, d, 0, 0, 0, 0, time.Local)).Hours()/24))
}

func Order(items []Item) []Item {
	out := append([]Item(nil), items...)
	sort.SliceStable(out, func(i, j int) bool {
		if (out[i].Lead != "") != (out[j].Lead != "") {
			return out[i].Lead != ""
		}
		return out[i].ID < out[j].ID
	})
	return out
}

func SyncOwned() func(links.Place) bool {
	cache := map[string]map[string]string{}
	return func(p links.Place) bool {
		if !note.IsTicketNote(p.File) {
			return false
		}
		fm, ok := cache[p.File]
		if !ok {
			fm = note.Frontmatter(p.File)
			cache[p.File] = fm
		}
		field := ""
		if p.Frontmatter {
			field = p.Field
		}
		return note.SyncOwned(fm, field, p.SourceBlock)
	}
}
