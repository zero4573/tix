package notesec

import (
	"path"
	"path/filepath"
	"reflect"
	"strings"

	"github.com/zero4573/tix/internal/mdsec"
)

func checkWrite(c Ctx, rel string, src, out []byte, t Target) error {
	covered, err := check(c, rel, src, t)
	if err != nil {
		return err
	}
	if _, ok := ticketOf(path.Clean(filepath.ToSlash(rel))); !ok {
		return nil
	}
	manual := isManual(src) && !covered
	if err := checkContent(rel, t, manual); err != nil {
		return err
	}
	if manual {
		seen := map[string]bool{}
		for _, s := range mdsec.Parse(out).Sections {
			if k := strings.ToLower(strings.TrimSpace(s.Title)); s.Level > 0 && contentSections[k] {
				if seen[k] {
					return refuse("refusing to write %s: the result has more than one section titled %q (a ticket note has each content section once)", rel, s.Title)
				}
				seen[k] = true
			}
		}
	}
	if !reflect.DeepEqual(protectedView(src, covered, manual), protectedView(out, covered, manual)) {
		what := "the source block of a synced ticket (tix sync overwrites it), its Description, Acceptance criteria, Context / links or identity fields"
		if manual {
			what = "the identity or source fields or a source block"
		}
		if covered {
			what = "anything but the work sections and status"
		}
		if t.Level == 1 {
			return refuse("refusing to rewrite the H1 of %s with its subsections: that reaches %s of a ticket note, which agents never write (AGENTS.md, \"Who writes what\")", rel, what)
		}
		return refuse("refusing to write %s: the change reaches %s of a ticket note, which agents never write (AGENTS.md, \"Who writes what\")", rel, what)
	}
	return nil
}

func checkContent(rel string, t Target, manual bool) error {
	if len(t.Content) == 0 || len(t.Section) == 0 {
		return nil
	}
	for _, l := range strings.Split(string(t.Content), "\n") {
		if l = strings.TrimSpace(l); l == sourceStart || l == sourceEnd {
			return refuse("refusing to write section %q of %s: the content has a source block marker line %q, and the source block of a ticket note is never written by agents (tix sync overwrites it)", strings.Join(t.Section, " > "), rel, l)
		}
	}
	seen := map[string]bool{}
	for _, s := range mdsec.Parse(append([]byte("\n"), t.Content...)).Sections {
		if s.Level == 0 {
			continue
		}
		if t.Level > 0 && s.Level <= t.Level {
			if t.Level == 1 && !manual {
				return refuse("refusing to rewrite the H1 of %s: the content has a level-%d heading %q, and a rewrite of the H1 with its subsections would have to carry the note's protected sections (Description, Acceptance criteria, Context / links), which agents never write", rel, s.Level, s.Title)
			}
			return refuse("refusing to write section %q of %s: the content has a level-%d heading %q, at or above the section's level %d", strings.Join(t.Section, " > "), rel, s.Level, s.Title, t.Level)
		}
		if k := strings.ToLower(strings.TrimSpace(s.Title)); contentSections[k] {
			if manual && t.Level == 1 && !seen[k] {
				seen[k] = true
				continue
			}
			return refuse("refusing to write section %q of %s: the content has a heading titled %q, like a content section that the note already has or may not get here (a manual ticket's own write may carry each once, in an H1 rewrite)", strings.Join(t.Section, " > "), rel, s.Title)
		}
	}
	return nil
}

const (
	sourceStart = "<!-- source:start -->"
	sourceEnd   = "<!-- source:end -->"
)

func sourceBlocks(src []byte) []string {
	var out []string
	var cur []string
	in := false
	for _, l := range strings.SplitAfter(string(src), "\n") {
		t := strings.TrimSpace(l)
		if !in && t == sourceStart {
			in, cur = true, nil
		}
		if in {
			cur = append(cur, l)
			if t == sourceEnd {
				out, in = append(out, strings.Join(cur, "")), false
			}
		}
	}
	if in {
		out = append(out, strings.Join(cur, ""))
	}
	return out
}

func protectedView(src []byte, covered, manual bool) []string {
	d := mdsec.Parse(src)
	var view []string
	for _, kv := range Fields(src) {
		if covered {
			if strings.ToLower(kv.Key) != "status" {
				view = append(view, "F:"+kv.Key+"="+kv.Raw)
			}
		} else if protectedField(kv.Key) {
			view = append(view, "F:"+kv.Key+"="+kv.Raw)
		}
	}
	for _, b := range sourceBlocks(src) {
		view = append(view, "X:"+b)
	}
	if !covered {
		if manual {
			return view
		}
		for _, s := range d.Sections {
			if s.Level > 0 && contentSections[strings.ToLower(strings.TrimSpace(s.Title))] {
				view = append(view, "S:"+string(src[s.Whole.Start:s.Whole.End]))
			}
		}
		return view
	}
	pos := 0
	if d.Frontmatter != nil {
		pos = d.Frontmatter.End
	}
	var rest strings.Builder
	for _, s := range d.Sections {
		if s.Level == 0 || !workSections[strings.ToLower(strings.TrimSpace(s.Title))] || s.Whole.Start < pos {
			continue
		}
		rest.Write(src[pos:s.Whole.Start])
		pos = s.Whole.End
	}
	rest.Write(src[pos:])
	return append(view, "B:"+rest.String())
}
