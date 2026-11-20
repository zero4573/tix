package spec

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/zero4573/tix/internal/gitx"
	"github.com/zero4573/tix/internal/textdiff"
)

type Tree map[string][]byte

const (
	Added   = "added"
	Removed = "removed"
	Changed = "changed"
	Flipped = "status"
)

type ReqChange struct {
	Kind string      `json:"kind"`
	Req  Requirement `json:"requirement"`
	From string      `json:"from,omitempty"`
	To   string      `json:"to,omitempty"`
}

type CapChange struct {
	Slug       string      `json:"slug"`
	Capability string      `json:"capability"`
	File       string      `json:"file"`
	Changes    []ReqChange `json:"changes"`
	Interfaces string      `json:"interfaces,omitempty"`
}

type FileDiff struct {
	Path    string `json:"path"`
	Added   int    `json:"added"`
	Removed int    `json:"removed"`
	Diff    string `json:"diff"`
}

type Diff struct {
	Slug  string      `json:"slug"`
	Caps  []CapChange `json:"capabilities"`
	Files []FileDiff  `json:"files"`
}

func (d Diff) Empty() bool { return len(d.Files) == 0 && len(d.Caps) == 0 }

func ReadTree(root string) (Tree, error) {
	t := Tree{}
	if st, err := os.Lstat(root); err != nil || !st.IsDir() {
		return t, nil
	}
	err := filepath.WalkDir(root, func(p string, e os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if e.IsDir() {
			if p != root && strings.HasPrefix(e.Name(), ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if !e.Type().IsRegular() {
			return nil
		}
		b, ok, err := readRegular(p)
		if err != nil {
			return err
		}
		if !ok {
			return nil
		}
		rel, _ := filepath.Rel(root, p)
		t[filepath.ToSlash(rel)] = b
		return nil
	})
	return t, err
}

func readRegular(path string) (b []byte, ok bool, err error) {
	f, err := openNoFollow(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) || isSymlinkRefusal(err) {
			return nil, false, nil
		}
		return nil, false, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, false, err
	}
	if !st.Mode().IsRegular() || st.Size() > MaxSpecSize {
		return nil, false, nil
	}
	b, err = io.ReadAll(io.LimitReader(f, MaxSpecSize+1))
	if err != nil {
		return nil, false, err
	}
	if len(b) > MaxSpecSize {
		return nil, false, nil
	}
	return b, true, nil
}

func GitTree(dir, rev, folder string) (Tree, error) {
	t := Tree{}
	if err := gitx.Check(dir); err != nil {
		return nil, err
	}
	out, err := gitx.CommandIn(dir, "ls-tree", "-r", "-z", "--full-name", rev, "--", folder+"/").Output()
	if err != nil {
		return nil, fmt.Errorf("read %s of %s in %s: %w", folder, rev, dir, err)
	}
	var oids, rels []string
	for _, ent := range strings.Split(string(out), "\x00") {
		meta, path, ok := strings.Cut(ent, "\t")
		f := strings.Fields(meta)
		if !ok || len(f) < 3 || (f[0] != "100644" && f[0] != "100755") {
			continue
		}
		rel := strings.TrimPrefix(path, folder+"/")
		if strings.HasPrefix(rel, ".") || strings.Contains(rel, "/.") {
			continue
		}
		oids = append(oids, f[2])
		rels = append(rels, rel)
	}
	if len(oids) == 0 {
		return t, nil
	}
	cmd := gitx.CommandIn(dir, "cat-file", "--batch")
	cmd.Stdin = strings.NewReader(strings.Join(oids, "\n") + "\n")
	pipe, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	r := bufio.NewReader(pipe)
	var readErr error
	for _, rel := range rels {
		hdr, err := r.ReadString('\n')
		if err != nil {
			readErr = err
			break
		}
		f := strings.Fields(hdr)
		if len(f) != 3 || f[1] != "blob" {
			continue
		}
		size, err := strconv.ParseInt(f[2], 10, 64)
		if err != nil || size < 0 {
			readErr = fmt.Errorf("cat-file: bad header %q", strings.TrimSpace(hdr))
			break
		}
		if size > MaxSpecSize {
			_, readErr = io.CopyN(io.Discard, r, size+1)
		} else {
			b := make([]byte, size+1)
			if _, readErr = io.ReadFull(r, b); readErr == nil {
				t[rel] = b[:size]
			}
		}
		if readErr != nil {
			break
		}
	}
	io.Copy(io.Discard, r)
	werr := cmd.Wait()
	if readErr != nil {
		return nil, fmt.Errorf("cat-file: %w", readErr)
	}
	if werr != nil {
		return nil, fmt.Errorf("cat-file: %w", werr)
	}
	return t, nil
}

func MergeBase(dir, base string) (string, error) {
	ref := gitx.BaseRef(dir, base)
	if !gitx.HasRef(dir, ref+"^{commit}") {
		return "", fmt.Errorf("base %q does not resolve in %s", base, dir)
	}
	out, err := gitx.CommandIn(dir, "merge-base", ref, "HEAD").Output()
	if err != nil {
		return ref, nil
	}
	if s := strings.TrimSpace(string(out)); s != "" {
		return s, nil
	}
	return ref, nil
}

func Overlay(before, over Tree) Tree {
	out := Tree{}
	for p, b := range before {
		out[p] = b
	}
	for p, b := range over {
		out[p] = b
	}
	return out
}

func (t Tree) paths() []string {
	var out []string
	for p := range t {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

func (t Tree) specFiles() []string {
	var out []string
	for _, p := range t.paths() {
		if d, f := filepath.Split(p); f == "spec.md" && d != "" && strings.Count(d, "/") == 1 {
			out = append(out, p)
		}
	}
	return out
}

func DiffTrees(slug string, before, after Tree) Diff {
	d := Diff{Slug: slug}
	seen := map[string]bool{}
	var all []string
	for _, p := range append(before.paths(), after.paths()...) {
		if !seen[p] {
			seen[p] = true
			all = append(all, p)
		}
	}
	sort.Strings(all)
	for _, p := range all {
		a, b := before[p], after[p]
		u := textdiff.Unified("a/"+p, "b/"+p, a, b, 3)
		if u == "" {
			continue
		}
		fd := FileDiff{Path: p, Diff: u}
		for _, ln := range strings.Split(u, "\n") {
			switch {
			case strings.HasPrefix(ln, "+++") || strings.HasPrefix(ln, "---"):
			case strings.HasPrefix(ln, "+"):
				fd.Added++
			case strings.HasPrefix(ln, "-"):
				fd.Removed++
			}
		}
		d.Files = append(d.Files, fd)
	}
	capSeen := map[string]bool{}
	for _, p := range append(after.specFiles(), before.specFiles()...) {
		if capSeen[p] {
			continue
		}
		capSeen[p] = true
		cc := diffCap(slug, p, before[p], after[p])
		if len(cc.Changes) > 0 || cc.Interfaces != "" {
			d.Caps = append(d.Caps, cc)
		}
	}
	sort.SliceStable(d.Caps, func(i, j int) bool { return d.Caps[i].Capability < d.Caps[j].Capability })
	return d
}

func diffCap(slug, file string, before, after []byte) CapChange {
	old, cur := Parse(file, before), Parse(file, after)
	cc := CapChange{Slug: slug, File: file, Capability: cur.Capability}
	if after == nil {
		cc.Capability = old.Capability
	}
	if cc.Capability == "" {
		cc.Capability = filepath.Dir(file)
	}
	prev := map[string]Requirement{}
	for _, r := range old.Requirements {
		prev[r.ID] = r
	}
	now := map[string]bool{}
	for _, r := range cur.Requirements {
		now[r.ID] = true
		o, had := prev[r.ID]
		switch {
		case !had:
			cc.Changes = append(cc.Changes, ReqChange{Kind: Added, Req: r, To: r.Status})
		case !sameBody(o, r):
			cc.Changes = append(cc.Changes, ReqChange{Kind: Changed, Req: r, From: o.Status, To: r.Status})
		case o.Status != r.Status:
			cc.Changes = append(cc.Changes, ReqChange{Kind: Flipped, Req: r, From: o.Status, To: r.Status})
		}
	}
	for _, r := range old.Requirements {
		if !now[r.ID] {
			cc.Changes = append(cc.Changes, ReqChange{Kind: Removed, Req: r, From: r.Status})
		}
	}
	if oi, ni := interfacesText(before), interfacesText(after); oi != ni {
		cc.Interfaces = ni
	}
	return cc
}

func sameBody(a, b Requirement) bool {
	if a.Title != b.Title || a.Statement != b.Statement ||
		strings.Join(a.Evidence, "\n") != strings.Join(b.Evidence, "\n") ||
		strings.Join(a.Tests, "\n") != strings.Join(b.Tests, "\n") ||
		len(a.Markers) != len(b.Markers) || len(a.Scenarios) != len(b.Scenarios) {
		return false
	}
	for i := range a.Markers {
		if a.Markers[i].Question != b.Markers[i].Question {
			return false
		}
	}
	for i := range a.Scenarios {
		x, y := a.Scenarios[i], b.Scenarios[i]
		if x.Name != y.Name || len(x.Steps) != len(y.Steps) {
			return false
		}
		for j := range x.Steps {
			if x.Steps[j].Kind != y.Steps[j].Kind || x.Steps[j].Text != y.Steps[j].Text {
				return false
			}
		}
	}
	return true
}

func interfacesText(src []byte) string {
	var out []string
	in, fence := false, false
	for _, ln := range splitLines(src) {
		if isFence(ln) {
			fence = !fence
		} else if !fence {
			if lvl, h := heading(ln); lvl == 1 || lvl == 2 {
				in = lvl == 2 && h == "Interfaces"
				continue
			}
		}
		if in {
			out = append(out, ln)
		}
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}

func (d Diff) Counts() map[string]int {
	n := map[string]int{}
	for _, c := range d.Caps {
		for _, ch := range c.Changes {
			n[ch.Kind]++
		}
	}
	return n
}

func (d Diff) Summary() []string {
	var out []string
	for _, c := range d.Caps {
		for _, ch := range c.Changes {
			var s string
			switch ch.Kind {
			case Added:
				s = fmt.Sprintf("added %s · %s (%s, %s)", ch.Req.ID, ch.Req.Title, c.Capability, ch.To)
			case Removed:
				s = fmt.Sprintf("removed %s · %s (%s)", ch.Req.ID, ch.Req.Title, c.Capability)
			case Flipped:
				s = fmt.Sprintf("status %s · %s (%s, %s -> %s)", ch.Req.ID, ch.Req.Title, c.Capability, ch.From, ch.To)
			default:
				s = fmt.Sprintf("changed %s · %s (%s", ch.Req.ID, ch.Req.Title, c.Capability)
				if ch.From != ch.To {
					s += fmt.Sprintf(", %s -> %s", ch.From, ch.To)
				}
				s += ")"
			}
			out = append(out, s)
		}
		if c.Interfaces != "" {
			out = append(out, fmt.Sprintf("changed interfaces (%s)", c.Capability))
		}
	}
	return out
}

func RenderRequirement(r Requirement) string {
	var b strings.Builder
	fmt.Fprintf(&b, "### %s · %s\nstatus: %s\n", r.ID, r.Title, r.Status)
	if len(r.Evidence) > 0 {
		fmt.Fprintf(&b, "evidence: %s\n", strings.Join(r.Evidence, ", "))
	}
	if len(r.Tests) > 0 {
		fmt.Fprintf(&b, "tests: %s\n", strings.Join(r.Tests, ", "))
	}
	if r.Statement != "" {
		b.WriteString(r.Statement + "\n")
	}
	for _, m := range r.Markers {
		fmt.Fprintf(&b, "[NEEDS CLARIFICATION: %s]\n", m.Question)
	}
	for _, sc := range r.Scenarios {
		fmt.Fprintf(&b, "\n#### Scenario: %s\n", sc.Name)
		for _, st := range sc.Steps {
			fmt.Fprintf(&b, "- %s %s\n", st.Kind, st.Text)
		}
	}
	return b.String()
}

func RenderChange(diffs ...Diff) string {
	var parts []string
	for _, d := range diffs {
		for _, c := range d.Caps {
			var b strings.Builder
			fmt.Fprintf(&b, "### %s · %s\n", c.Slug, c.Capability)
			var gone []string
			for _, ch := range c.Changes {
				if ch.Kind == Removed {
					gone = append(gone, ch.Req.ID)
				}
			}
			if len(gone) > 0 {
				fmt.Fprintf(&b, "\nRemoved: %s\n", strings.Join(gone, ", "))
			}
			for _, ch := range c.Changes {
				if ch.Kind != Removed {
					b.WriteString("\n" + RenderRequirement(ch.Req))
				}
			}
			if c.Interfaces != "" {
				b.WriteString("\n#### Interfaces\n\n" + c.Interfaces + "\n")
			}
			parts = append(parts, b.String())
		}
	}
	return strings.Join(parts, "\n")
}
