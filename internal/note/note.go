package note

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

var keyRe = regexp.MustCompile(`^[A-Z][A-Z0-9_]*(-[A-Z0-9]+)+$`)

func ValidKey(s string) bool { return keyRe.MatchString(s) }

func IsTicketNote(file string) bool {
	id := strings.TrimSuffix(filepath.Base(file), ".md")
	return ValidKey(id) && filepath.Base(file) == id+".md" && filepath.Base(filepath.Dir(file)) == id
}

func Synced(fm map[string]string) bool {
	s := fm["source"]
	return s != "" && s != "manual"
}

var SyncOwnedFields = []string{"parent", "children", "blocked-by", "blocks", "related", "blocked", "covers", "covered-by"}

func SyncOwned(fm map[string]string, field string, sourceBlock bool) bool {
	if !Synced(fm) {
		return false
	}
	if sourceBlock || strings.HasPrefix(field, "source-") {
		return true
	}
	for _, f := range SyncOwnedFields {
		if f == field {
			return true
		}
	}
	return false
}

func Frontmatter(file string) map[string]string {
	f, err := os.Open(file)
	if err != nil {
		return map[string]string{}
	}
	defer f.Close()
	return FrontmatterFrom(f)
}

func FrontmatterFrom(r io.Reader) map[string]string {
	out := map[string]string{}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 1024*1024), 1024*1024)
	first := true
	for sc.Scan() {
		line := strings.TrimSuffix(sc.Text(), "\r")
		if first {
			line = strings.TrimPrefix(line, "\ufeff")
			if line != "---" {
				return out
			}
			first = false
			continue
		}
		if line == "---" {
			break
		}
		key, val, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		if _, seen := out[key]; seen {
			continue
		}
		out[key] = unquote(strings.TrimLeft(val, " \t"))
	}
	return out
}

func Get(file, field string) string { return Frontmatter(file)[field] }

func unquote(v string) string {
	v = strings.TrimRight(v, " \t")
	if n := len(v); n >= 2 && (v[0] == '"' && v[n-1] == '"' || v[0] == '\'' && v[n-1] == '\'') {
		var s string
		if yaml.Unmarshal([]byte(v), &s) == nil {
			return s
		}
	}
	if len(v) > 0 && (v[0] == '"' || v[0] == '\'') {
		v = v[1:]
	}
	if n := len(v); n > 0 && (v[n-1] == '"' || v[n-1] == '\'') {
		v = v[:n-1]
	}
	return v
}

func Ignored(file string) bool {
	fm := Frontmatter(file)
	if fm["ignore"] != "true" {
		return false
	}
	until := fm["ignore-until"]
	return until == "" || until >= time.Now().Format("2006-01-02")
}

type Ticket struct {
	ID, Status, Summary string
}

func List(vault string, all bool) []Ticket {
	var out []Ticket
	for _, l := range TicketNotes(vault) {
		id, file := l.ID, l.Note()
		fm := Frontmatter(file)
		status := fm["status"]
		if !all && (status == "done" || status == "closed") {
			continue
		}
		if fm["ignore"] == "true" {
			status += ", ignored"
		}
		if _, lead := StartGuard(vault, id); lead != "" {
			status += ", covered by " + lead
		}
		if status == "" {
			status = "-"
		}
		out = append(out, Ticket{id, status, strings.ReplaceAll(fm["summary"], "\t", " ")})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func StartGuard(vault, id string) (why, lead string) {
	file := TicketPath(vault, id)
	fm := Frontmatter(file)
	if Ignored(file) {
		why = "marked ignore: true"
		if r := fm["ignore-reason"]; r != "" {
			why += " (" + r + ")"
		}
		return why, ""
	}
	lead = strings.NewReplacer("[", "", "]", "", `"`, "").Replace(fm["covered-by"])
	if lead == "" {
		return "", ""
	}
	st := Get(TicketPath(vault, lead), "status")
	if st == "done" || st == "closed" {
		return "", ""
	}
	lt := Get(TicketPath(vault, lead), "ticket-type")
	if lt == "" {
		lt = "lead"
	}
	return fmt.Sprintf("covered by %s (%s, %s)", lead, lt, orDash(st)), lead
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
