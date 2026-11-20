package note

import (
	"encoding/json"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

var sourceRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]*$`)

func ValidSource(s string) bool { return sourceRe.MatchString(s) && !ValidKey(s) }

func TicketsDir(vault string) string { return filepath.Join(vault, "tickets") }

func SourceDir(vault, source string) string { return filepath.Join(TicketsDir(vault), source) }

func SourcePath(vault, source, id string) string {
	return filepath.Join(SourceDir(vault, source), id, id+".md")
}

type Loc struct {
	ID     string
	Source string
	Dir    string
}

func (l Loc) Note() string { return filepath.Join(l.Dir, l.ID+".md") }

func sourceDirs(root string) []string {
	entries, _ := os.ReadDir(root)
	var out []string
	for _, e := range entries {
		if e.IsDir() && ValidSource(e.Name()) {
			out = append(out, e.Name())
		}
	}
	return out
}

func DirsUnder(root string) []Loc {
	var out []Loc
	entries, _ := os.ReadDir(root)
	for _, e := range entries {
		if e.IsDir() && ValidKey(e.Name()) {
			out = append(out, Loc{ID: e.Name(), Dir: filepath.Join(root, e.Name())})
		}
	}
	for _, src := range sourceDirs(root) {
		sub, _ := os.ReadDir(filepath.Join(root, src))
		for _, e := range sub {
			if e.IsDir() && ValidKey(e.Name()) {
				out = append(out, Loc{ID: e.Name(), Source: src, Dir: filepath.Join(root, src, e.Name())})
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].ID != out[j].ID {
			return out[i].ID < out[j].ID
		}
		return out[i].Source < out[j].Source
	})
	return out
}

func TicketDirs(vault string) []Loc { return DirsUnder(TicketsDir(vault)) }

func TicketNotes(vault string) []Loc {
	var out []Loc
	seen := map[string]bool{}
	for _, l := range TicketDirs(vault) {
		if seen[l.ID] {
			continue
		}
		if p := Find(vault, l.ID); p != "" {
			seen[l.ID] = true
			out = append(out, Loc{ID: l.ID, Source: sourceOfDir(vault, filepath.Dir(p)), Dir: filepath.Dir(p)})
		}
	}
	return out
}

func sourceOfDir(vault, dir string) string {
	parent := filepath.Dir(dir)
	if parent == TicketsDir(vault) {
		return ""
	}
	return filepath.Base(parent)
}

func isFile(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.Mode().IsRegular()
}

func Find(vault, id string) string {
	if !ValidKey(id) {
		return ""
	}
	for _, src := range sourceDirs(TicketsDir(vault)) {
		if p := SourcePath(vault, src, id); isFile(p) {
			return p
		}
	}
	if p := filepath.Join(TicketsDir(vault), id, id+".md"); isFile(p) {
		return p
	}
	return ""
}

func TicketPath(vault, id string) string {
	if p := Find(vault, id); p != "" {
		return p
	}
	return SourcePath(vault, GuessSource(vault, id), id)
}

func TicketDir(vault, id string) string { return filepath.Dir(TicketPath(vault, id)) }

func RelDir(vault, id string) string {
	r, err := filepath.Rel(vault, TicketDir(vault, id))
	if err != nil {
		return path.Join("tickets", GuessSource(vault, id), id)
	}
	return filepath.ToSlash(r)
}

func SourceOf(vault, id string) string {
	if p := Find(vault, id); p != "" {
		if s := sourceOfDir(vault, filepath.Dir(p)); s != "" {
			return s
		}
		if s := strings.TrimSpace(Get(p, "source")); ValidSource(s) {
			return s
		}
	}
	return GuessSource(vault, id)
}

func ArchiveRoot(vault string) string { return filepath.Join(vault, "archive", "tickets") }

func ArchiveDir(vault, id string) string {
	for _, l := range DirsUnder(ArchiveRoot(vault)) {
		if l.ID == id {
			return l.Dir
		}
	}
	return filepath.Join(ArchiveRoot(vault), SourceOf(vault, id), id)
}

func ArchivedIDs(vault string) []string {
	var out []string
	seen := map[string]bool{}
	for _, l := range DirsUnder(ArchiveRoot(vault)) {
		if !seen[l.ID] {
			seen[l.ID] = true
			out = append(out, l.ID)
		}
	}
	return out
}

func Sources(vault string) map[string]SourceConf {
	data, err := os.ReadFile(filepath.Join(TicketsDir(vault), ".sources.json"))
	if err != nil {
		return nil
	}
	var cfg struct {
		Sources map[string]SourceConf `json:"sources"`
	}
	if json.Unmarshal(data, &cfg) != nil {
		return nil
	}
	return cfg.Sources
}

type SourceConf struct {
	Enabled  *bool  `json:"enabled"`
	IDPrefix string `json:"idPrefix"`
}

func GuessSource(vault, id string) string {
	srcs := Sources(vault)
	keys := make([]string, 0, len(srcs))
	for k := range srcs {
		if ValidSource(k) {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	prefix := func(k string) string {
		if p := srcs[k].IDPrefix; p != "" || k != "manual" {
			return p
		}
		return "MAN"
	}
	for _, k := range keys {
		if p := prefix(k); p != "" && strings.HasPrefix(id, p+"-") {
			return k
		}
	}
	if _, ok := srcs["manual"]; !ok && strings.HasPrefix(id, "MAN-") {
		return "manual"
	}
	for _, enabledOnly := range []bool{true, false} {
		for _, k := range keys {
			if c := srcs[k]; prefix(k) == "" && (!enabledOnly || c.Enabled == nil || *c.Enabled) {
				return k
			}
		}
	}
	return "manual"
}

func RelTicket(rel string) (id, dir, rest string, ok bool) {
	p := strings.Split(rel, "/")
	if len(p) < 2 || p[0] != "tickets" {
		return "", "", "", false
	}
	if ValidKey(p[1]) {
		return p[1], strings.Join(p[:2], "/"), strings.Join(p[2:], "/"), true
	}
	if len(p) >= 3 && ValidSource(p[1]) && ValidKey(p[2]) {
		return p[2], strings.Join(p[:3], "/"), strings.Join(p[3:], "/"), true
	}
	return "", "", "", false
}

func RelNote(rel string) (string, bool) {
	id, _, rest, ok := RelTicket(rel)
	if ok && rest == id+".md" {
		return id, true
	}
	return "", false
}
