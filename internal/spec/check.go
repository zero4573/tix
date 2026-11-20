package spec

import (
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

const (
	KindDuplicate = "duplicate-id"
	KindMalformed = "malformed"
	KindMarker    = "marker"
	KindUntested  = "untested"
	KindInterface = "interface"
	KindStaleRef  = "stale-ref"
)

type Finding struct {
	Repo    string `json:"repo"`
	Kind    string `json:"kind"`
	File    string `json:"file"`
	Line    int    `json:"line"`
	ID      string `json:"id,omitempty"`
	Message string `json:"message"`
}

func (f Finding) String() string {
	return fmt.Sprintf("%s:%d: %s: %s", f.File, f.Line, f.Kind, f.Message)
}

type Target struct {
	Slug  string
	Dir   string
	Specs string
}

type Row struct {
	Dir                          string
	Interface, Kind, Where, With string
	File                         string
	Line                         int
}

var (
	anyIDRe    = regexp.MustCompile(`REQ-[A-Z][A-Z0-9]{1,7}-[0-9]+`)
	testNameRe = regexp.MustCompile(`(^test_|_test\.|\.test\.|\.spec\.|Tests?\.[A-Za-z]+$|^test\.)`)
)

var (
	maxTestFiles = 50000
	maxWalk      = 500000
)

func Check(targets []Target, only []string) []Finding {
	want := map[string]bool{}
	for _, s := range only {
		want[s] = true
	}
	type loaded struct {
		t     Target
		rows  []Row
		specs []*Spec
		fs    []Finding
	}
	var all []*loaded
	for _, t := range targets {
		l := &loaded{t: t}
		l.specs, l.rows, l.fs = loadTarget(t)
		all = append(all, l)
	}
	rowsBy := map[string][]Row{}
	for _, l := range all {
		if len(l.specs) > 0 {
			rowsBy[l.t.Slug] = l.rows
		}
	}
	var out []Finding
	for _, l := range all {
		if len(want) > 0 && !want[l.t.Slug] {
			continue
		}
		out = append(out, l.fs...)
		for _, r := range l.rows {
			if msg := unmatched(r, l.t.Slug, rowsBy); msg != "" {
				out = append(out, Finding{Repo: l.t.Slug, Kind: KindInterface, File: r.File, Line: r.Line, Message: msg})
			}
		}
		out = append(out, untested(l.t, l.specs)...)
		out = append(out, staleRefs(l.t, l.specs)...)
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Repo != b.Repo {
			return a.Repo < b.Repo
		}
		if a.File != b.File {
			return a.File < b.File
		}
		return a.Line < b.Line
	})
	return out
}

func loadTarget(t Target) ([]*Spec, []Row, []Finding) {
	var fs []Finding
	add := func(kind, file string, line int, id, f string, a ...any) {
		fs = append(fs, Finding{Repo: t.Slug, Kind: kind, File: file, Line: line, ID: id, Message: fmt.Sprintf(f, a...)})
	}
	caps, err := Capabilities(t.Specs)
	if err != nil {
		add(KindMalformed, t.Specs, 1, "", "%v", err)
		return nil, nil, fs
	}
	var specs []*Spec
	var rows []Row
	var refs []IDRef
	for _, c := range caps {
		b, err := readSpec(c.File)
		if err != nil {
			add(KindMalformed, c.File, 1, "", "%v", err)
			continue
		}
		s := Parse(c.File, b)
		specs = append(specs, s)
		refs = append(refs, FindIDs(c.File, b)...)
		rows = append(rows, interfaceRows(c.File, b)...)
		for _, p := range s.Problems {
			if !strings.HasPrefix(p.Msg, "duplicate ID ") {
				add(KindMalformed, p.File, p.Line, "", "%s", p.Msg)
			}
		}
		for _, r := range s.Requirements {
			for _, m := range r.Markers {
				add(KindMarker, c.File, m.Line, r.ID, "%s has an open clarification: %s", r.ID, m.Question)
			}
		}
	}
	dups := Duplicates(refs)
	for _, id := range SortedIDs(dups) {
		d := dups[id]
		for _, r := range d[1:] {
			add(KindDuplicate, r.File, r.Line, id, "duplicate ID %s (first at %s:%d)", id, d[0].File, d[0].Line)
		}
	}
	return specs, rows, fs
}

func interfaceRows(file string, src []byte) []Row {
	var out []Row
	in, dir, fence := false, "", false
	for i, ln := range splitLines(src) {
		if isFence(ln) {
			fence = !fence
			continue
		}
		if fence {
			continue
		}
		if lvl, h := heading(ln); lvl > 0 {
			switch {
			case lvl == 2:
				in, dir = h == "Interfaces", ""
			case lvl == 3 && in:
				dir = strings.ToLower(h)
				if dir != "provides" && dir != "consumes" {
					dir = ""
				}
			}
			continue
		}
		t := strings.TrimSpace(ln)
		if dir == "" || !strings.HasPrefix(t, "|") {
			continue
		}
		cells := strings.Split(strings.Trim(t, "|"), "|")
		if len(cells) != 4 {
			continue
		}
		for j := range cells {
			cells[j] = strings.Join(strings.Fields(cells[j]), " ")
		}
		if cells[0] == "Interface" || strings.Trim(cells[0], "-: ") == "" {
			continue
		}
		out = append(out, Row{Interface: cells[0], Kind: cells[1], Where: cells[2], With: cells[3], File: file, Line: i + 1, Dir: dir})
	}
	return out
}

func unmatched(r Row, slug string, rowsBy map[string][]Row) string {
	if r.With == "" || r.With == "-" {
		return fmt.Sprintf("%s %s names no repo in With", r.Dir, r.Interface)
	}
	other, ok := rowsBy[r.With]
	if !ok {
		return fmt.Sprintf("%s %s: %s has no spec to match it", r.Dir, r.Interface, r.With)
	}
	want := "consumes"
	if r.Dir == "consumes" {
		want = "provides"
	}
	for _, o := range other {
		if o.Dir == want && o.Interface == r.Interface && o.Kind == r.Kind && o.With == slug {
			return ""
		}
	}
	return fmt.Sprintf("%s %s (%s) has no matching %s row in %s", r.Dir, r.Interface, r.Kind, want, r.With)
}

var fileLineRe = regexp.MustCompile(`^(.+?):([0-9]+)(?:-([0-9]+))?$`)

func staleRefs(t Target, specs []*Spec) []Finding {
	if t.Dir == "" {
		return nil
	}
	type fileInfo struct {
		lines []string
		ok    bool
	}
	files := map[string]*fileInfo{}
	load := func(rel string) *fileInfo {
		if f, ok := files[rel]; ok {
			return f
		}
		f := &fileInfo{}
		files[rel] = f
		p := filepath.Join(t.Dir, filepath.FromSlash(rel))
		st, err := os.Lstat(p)
		if err != nil || !st.Mode().IsRegular() || st.Size() > 8<<20 {
			return f
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return f
		}
		f.lines, f.ok = strings.Split(string(b), "\n"), true
		return f
	}
	var out []Finding
	for _, s := range specs {
		for _, r := range s.Requirements {
			for _, group := range []struct {
				what string
				refs []string
			}{{"evidence", r.Evidence}, {"tests", r.Tests}} {
				for _, ref := range group.refs {
					ref = strings.Trim(strings.TrimSpace(ref), "`")
					ref = strings.TrimPrefix(ref, t.Slug+":")
					m := fileLineRe.FindStringSubmatch(ref)
					if m == nil {
						continue
					}
					rel := path.Clean(filepath.ToSlash(m[1]))
					if path.IsAbs(rel) || rel == ".." || strings.HasPrefix(rel, "../") || strings.ContainsAny(rel, " \t") {
						continue
					}
					msg := ""
					f := load(rel)
					first, _ := strconv.Atoi(m[2])
					last := first
					if m[3] != "" {
						last, _ = strconv.Atoi(m[3])
					}
					n := len(f.lines)
					if n > 0 && f.lines[n-1] == "" {
						n--
					}
					switch {
					case !f.ok:
						msg = "no such file in the checkout"
					case first < 1 || last < first || last > n:
						msg = fmt.Sprintf("the file has %d lines", n)
					case strings.TrimSpace(f.lines[first-1]) == "":
						msg = "that line is blank"
					}
					if msg != "" {
						out = append(out, Finding{Repo: t.Slug, Kind: KindStaleRef, File: s.File, Line: r.Line, ID: r.ID,
							Message: fmt.Sprintf("%s %s of %s: %s", group.what, ref, r.ID, msg)})
					}
				}
			}
		}
	}
	return out
}

func untested(t Target, specs []*Spec) []Finding {
	var impl []Requirement
	var files []string
	for _, s := range specs {
		for _, r := range s.Requirements {
			if r.Status == Implemented {
				impl = append(impl, r)
				files = append(files, s.File)
			}
		}
	}
	if len(impl) == 0 || t.Dir == "" {
		return nil
	}
	cited, capped := citedIDs(t.Dir, t.Specs)
	if capped != "" {
		return []Finding{{Repo: t.Slug, Kind: KindMalformed, File: t.Dir,
			Message: "the test-citation search stopped at " + capped + ", so untested requirements aren't reported"}}
	}
	var out []Finding
	for i, r := range impl {
		if !cited[r.ID] {
			out = append(out, Finding{Repo: t.Slug, Kind: KindUntested, File: files[i], Line: r.Line, ID: r.ID,
				Message: r.ID + " is implemented but no test file cites it"})
		}
	}
	return out
}

func isTestFile(rel string) bool {
	if testNameRe.MatchString(filepath.Base(rel)) {
		return true
	}
	for _, c := range strings.Split(filepath.ToSlash(filepath.Dir(rel)), "/") {
		switch c {
		case "test", "tests", "__tests__", "e2e", "testdata":
			return true
		}
	}
	return false
}

func citedIDs(dir, specs string) (cited map[string]bool, capped string) {
	out := map[string]bool{}
	seen, read := 0, 0
	_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if seen++; seen > maxWalk {
			capped = fmt.Sprintf("%d entries", maxWalk)
			return filepath.SkipAll
		}
		if read >= maxTestFiles {
			capped = fmt.Sprintf("%d test files", maxTestFiles)
			return filepath.SkipAll
		}
		if d.Type()&fs.ModeSymlink != 0 {
			return nil
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "node_modules", "vendor":
				return filepath.SkipDir
			}
			if p == filepath.Clean(specs) {
				return filepath.SkipDir
			}
			return nil
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil || !d.Type().IsRegular() || !isTestFile(rel) {
			return nil
		}
		if info, err := d.Info(); err != nil || info.Size() > MaxSpecSize {
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return nil
		}
		read++
		for _, id := range anyIDRe.FindAllString(string(b), -1) {
			out[id] = true
		}
		return nil
	})
	return out, capped
}
