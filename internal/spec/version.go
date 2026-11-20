package spec

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/zero4573/tix/internal/fsx"
	"github.com/zero4573/tix/internal/note"
)

const VersionType = "spec-version"

const TimeLayout = "2006-01-02T15:04:05"

type Version struct {
	Type     string   `yaml:"type" json:"-"`
	Slug     string   `yaml:"slug" json:"slug"`
	Ticket   string   `yaml:"ticket" json:"ticket"`
	Project  string   `yaml:"project" json:"project"`
	Promoted string   `yaml:"promoted" json:"promoted"`
	Files    []string `yaml:"files,flow" json:"files"`
	Branch   string   `yaml:"branch" json:"branch"`
	Commit   string   `yaml:"commit" json:"commit"`
	Pinned   string   `yaml:"pinned" json:"pinned"`
	Merged   string   `yaml:"merged" json:"merged"`
	Tags     []string `yaml:"tags,flow" json:"tags"`
}

func (v Version) ID() string {
	return strings.TrimSuffix(strings.TrimPrefix(v.Ticket, "[["), "]]")
}

func (v *Version) ClearPin() {
	v.Commit, v.Pinned, v.Merged, v.Tags = "", "", "", nil
}

func (v Version) HasFile(file string) bool {
	for _, f := range v.Files {
		if f == file {
			return true
		}
	}
	return false
}

func VersionsDir(vault, slug string) string {
	return filepath.Join(vault, "projects", slug, "spec-versions")
}

func VersionPath(vault, slug, id string) string {
	return filepath.Join(VersionsDir(vault, slug), slug+"-spec-"+id+".md")
}

func CopyDir(vault, id, slug string) string {
	return filepath.Join(note.TicketDir(vault, id), "specs", slug)
}

func BaseDir(vault, id, slug string) string {
	return filepath.Join(note.TicketDir(vault, id), "specs-base", slug)
}

func AfterDir(vault, id, slug string) string {
	return filepath.Join(note.TicketDir(vault, id), "specs-after", slug)
}

func RebaseMarker(vault, id, slug string) string {
	return filepath.Join(note.TicketDir(vault, id), "specs-base", "."+slug+".rebase")
}

func KeptList(vault, id, slug string) string {
	return filepath.Join(note.TicketDir(vault, id), "specs-after", "."+slug+".kept")
}

func ReadKept(path string) ([]string, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []string
	for _, l := range strings.Split(string(b), "\n") {
		if l = strings.TrimSpace(l); l != "" {
			out = append(out, l)
		}
	}
	return MergeFiles(out, nil), nil
}

func WriteKept(path string, files []string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return fsx.WriteFileAtomic(path, []byte(strings.Join(MergeFiles(files, nil), "\n")+"\n"), 0o644)
}

func PendingRebase(vault, id, slug string) error {
	_, err := os.Lstat(RebaseMarker(vault, id, slug))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	return &pendingRebaseError{fmt.Sprintf("%s: the snapshots of %s are part way rebased (a tix spec start stopped while it rebased them): run tix spec start %s %s again to finish them", slug, id, id, slug)}
}

var ErrRebasePending = errors.New("snapshots part way rebased")

type pendingRebaseError struct{ msg string }

func (e *pendingRebaseError) Error() string        { return e.msg }
func (e *pendingRebaseError) Is(target error) bool { return target == ErrRebasePending }

func RefreshPending(id, slug string) string {
	return fmt.Sprintf("refresh pending: run tix spec start %s %s", id, slug)
}

func ReadSnapshot(dir string) (Tree, error) {
	if st, err := os.Lstat(dir); err != nil || !st.IsDir() {
		return nil, nil
	}
	return ReadTree(dir)
}

func ReadVersion(path string) (Version, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Version{}, err
	}
	var v Version
	fm, ok := frontmatterBlock(b)
	if !ok {
		return Version{}, fmt.Errorf("%s: no frontmatter", path)
	}
	if err := yaml.Unmarshal(fm, &v); err != nil {
		return Version{}, fmt.Errorf("%s: %w", path, err)
	}
	if v.Type != VersionType {
		return Version{}, fmt.Errorf("%s: type is %q, not %s", path, v.Type, VersionType)
	}
	return v, nil
}

func frontmatterBlock(b []byte) ([]byte, bool) {
	b = bytes.ReplaceAll(b, []byte("\r\n"), []byte("\n"))
	if !bytes.HasPrefix(b, []byte("---\n")) {
		return nil, false
	}
	rest := b[4:]
	if i := bytes.Index(rest, []byte("\n---\n")); i >= 0 {
		return rest[:i+1], true
	}
	if bytes.HasSuffix(rest, []byte("\n---")) {
		return rest[:len(rest)-3], true
	}
	return nil, false
}

type VersionFile struct {
	Path    string  `json:"path"`
	Version Version `json:"version"`
}

func ListVersions(vault, slug string) ([]VersionFile, error) {
	entries, err := os.ReadDir(VersionsDir(vault, slug))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []VersionFile
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		p := filepath.Join(VersionsDir(vault, slug), e.Name())
		v, err := ReadVersion(p)
		if err != nil || v.Slug != slug {
			continue
		}
		out = append(out, VersionFile{Path: p, Version: v})
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i].Version, out[j].Version
		if a.Promoted != b.Promoted {
			return a.Promoted > b.Promoted
		}
		return a.ID() < b.ID()
	})
	return out, nil
}

func (t Tree) Keys() []string { return t.paths() }

func Pick(t Tree, files []string) Tree {
	out := Tree{}
	for _, f := range files {
		if b, ok := t[f]; ok {
			out[f] = b
		}
	}
	return out
}

func MergeFiles(a, b []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, f := range append(append([]string{}, a...), b...) {
		if !seen[f] {
			seen[f] = true
			out = append(out, f)
		}
	}
	sort.Strings(out)
	return out
}

func PinStale(old, cur Tree, files []string) bool {
	for _, f := range files {
		a, aok := old[f]
		b, bok := cur[f]
		if aok != bok || !bytes.Equal(a, b) {
			return true
		}
	}
	return false
}

func TicketAfter(prev, projects Tree, promoted []string, now Tree) Tree {
	if prev == nil {
		prev = projects
	}
	return Overlay(Overlay(Pick(projects, Unfilled(prev, projects, promoted, now)), Pick(prev, promoted)), now)
}

func Unfilled(prev, projects Tree, promoted []string, now Tree) []string {
	if prev == nil {
		return nil
	}
	var out []string
	for _, f := range promoted {
		_, inPrev := prev[f]
		_, inNow := now[f]
		_, inProj := projects[f]
		if !inPrev && !inNow && inProj {
			out = append(out, f)
		}
	}
	return out
}

func PromotedSince(versions []VersionFile, id, file, since string) (VersionFile, bool) {
	for _, vf := range versions {
		if vf.Version.ID() != id && vf.Version.Promoted > since && vf.Version.HasFile(file) {
			return vf, true
		}
	}
	return VersionFile{}, false
}

func ChangedBy(vault, slug string, versions []VersionFile, id, file string, cur []byte, ok bool) ([]VersionFile, error) {
	var out []VersionFile
	for _, vf := range versions {
		o := vf.Version.ID()
		if o == id || !vf.Version.HasFile(file) {
			continue
		}
		b, err := os.ReadFile(filepath.Join(AfterDir(vault, o, slug), filepath.FromSlash(file)))
		if errors.Is(err, os.ErrNotExist) {
			if !ok {
				if _, err := os.Stat(AfterDir(vault, o, slug)); err == nil {
					out = append(out, vf)
				} else if !errors.Is(err, os.ErrNotExist) {
					return nil, err
				}
			}
			continue
		} else if err != nil {
			return nil, err
		}
		if ok && bytes.Equal(b, cur) {
			out = append(out, vf)
		}
	}
	return out, nil
}

func Sides(projects, base, after, copy Tree, promoted []string) (before, aft Tree) {
	switch {
	case base == nil:
		return projects, Overlay(projects, copy)
	case after != nil:
		return base, Overlay(base, Overlay(Pick(after, promoted), copy))
	case len(promoted) == 0:
		return Overlay(projects, Pick(base, copy.paths())), Overlay(projects, copy)
	}
	return base, Overlay(projects, copy)
}

func Conflicts(projects, base, after, copy Tree) []string {
	if base == nil {
		return nil
	}
	var out []string
	for _, f := range copy.paths() {
		if _, ok := after[f]; SameFile(base, projects, f) || (ok && SameFile(after, projects, f)) {
			continue
		}
		out = append(out, f)
	}
	return out
}

func SameFile(a, b Tree, f string) bool {
	x, xok := a[f]
	y, yok := b[f]
	return xok == yok && bytes.Equal(x, y)
}

func Rebase(projects, base, after Tree, files []string) (newBase, newAfter Tree, changed []string) {
	newBase, newAfter = Overlay(base, nil), after
	if after != nil {
		newAfter = Overlay(after, nil)
	}
	seen := map[string]bool{}
	for _, f := range files {
		b, ok := projects[f]
		if !ok || seen[f] {
			continue
		}
		seen[f] = true
		moved := !SameFile(base, projects, f)
		newBase[f] = b
		if _, ok := after[f]; ok {
			moved = moved || !SameFile(after, projects, f)
			newAfter[f] = b
		}
		if moved {
			changed = append(changed, f)
		}
	}
	sort.Strings(changed)
	return newBase, newAfter, changed
}

func Ancestor(base, after Tree, files []string) Tree {
	out := Tree{}
	for _, f := range files {
		if b, ok := after[f]; ok {
			out[f] = b
		} else if b, ok := base[f]; ok {
			out[f] = b
		}
	}
	return out
}

type Clash struct {
	Capability   string   `json:"capability"`
	File         string   `json:"file"`
	Requirements []string `json:"requirements"`
	Interfaces   bool     `json:"interfaces"`
}

func Clashes(theirs, ours Diff) []Clash {
	caps := map[string]CapChange{}
	for _, c := range theirs.Caps {
		caps[c.File] = c
	}
	theirFiles := map[string]bool{}
	for _, f := range theirs.Files {
		theirFiles[f.Path] = true
	}
	var out []Clash
	for _, f := range ours.Files {
		if !theirFiles[f.Path] {
			continue
		}
		cl := Clash{File: f.Path, Capability: strings.SplitN(f.Path, "/", 2)[0], Requirements: []string{}}
		var oc CapChange
		for _, c := range ours.Caps {
			if c.File == f.Path {
				oc = c
				if c.Capability != "" {
					cl.Capability = c.Capability
				}
			}
		}
		tc := caps[f.Path]
		ids := map[string]bool{}
		for _, ch := range tc.Changes {
			ids[ch.Req.ID] = true
		}
		for _, ch := range oc.Changes {
			if ids[ch.Req.ID] {
				cl.Requirements = append(cl.Requirements, ch.Req.ID)
			}
		}
		cl.Interfaces = tc.Interfaces != "" && oc.Interfaces != ""
		out = append(out, cl)
	}
	return out
}

func VersionState(v Version) string {
	if v.Commit == "" {
		return "Unpinned: promoted, not pushed yet"
	}
	s := "Pinned to " + short(v.Commit)
	if v.Branch != "" {
		s += " on " + v.Branch
	}
	if v.Merged != "" {
		s += ", merged as " + short(v.Merged)
	}
	if len(v.Tags) > 0 {
		s += ", in " + v.Tags[0]
	}
	return s
}

func yamlScalar(s string) string {
	if s != "" && !strings.ContainsAny(s, ",[]{}#&*!|>'\"%@`") && !strings.Contains(s, ": ") && !strings.HasSuffix(s, ":") && strings.TrimSpace(s) == s {
		var back string
		if out, err := yaml.Marshal(s); err == nil && strings.TrimSpace(string(out)) == s && yaml.Unmarshal(out, &back) == nil && back == s {
			return s
		}
	}
	q, _ := json.Marshal(s)
	return string(q)
}

func short(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}

func RenderVersion(v Version, ticketRel string, before Tree, d Diff) ([]byte, error) {
	v.Type = VersionType
	if v.Files == nil {
		v.Files = []string{}
	}
	if v.Tags == nil {
		v.Tags = []string{}
	}
	var b bytes.Buffer
	b.WriteString("---\n")
	for _, kv := range []struct {
		k string
		v any
	}{
		{"type", v.Type}, {"slug", v.Slug}, {"ticket", v.Ticket}, {"project", v.Project},
		{"promoted", v.Promoted}, {"files", v.Files}, {"branch", v.Branch}, {"commit", v.Commit},
		{"pinned", v.Pinned}, {"merged", v.Merged}, {"tags", v.Tags},
	} {
		switch x := kv.v.(type) {
		case string:
			fmt.Fprintf(&b, "%s: %s\n", kv.k, yamlScalar(x))
		case []string:
			items := make([]string, len(x))
			for i, it := range x {
				items[i] = yamlScalar(it)
			}
			fmt.Fprintf(&b, "%s: [%s]\n", kv.k, strings.Join(items, ", "))
		}
	}
	b.WriteString("---\n")
	fmt.Fprintf(&b, "# %s spec version %s\n\n", v.Slug, v.ID())
	b.WriteString("Generated by tix spec promote; edits are overwritten.\n\n")
	b.WriteString(VersionState(v) + "\n")
	if len(d.Caps) > 0 {
		b.WriteString("\n")
	}
	for _, c := range d.Caps {
		link := func(snap, label string) string {
			return "[[" + ticketRel + "/" + snap + "/" + v.Slug + "/" + strings.TrimSuffix(c.File, ".md") + "|" + label + "]]"
		}
		var parts []string
		if _, ok := before[c.File]; ok {
			parts = append(parts, link("specs-base", "before"))
		}
		parts = append(parts, link("specs-after", "after"))
		fmt.Fprintf(&b, "- %s: %s\n", c.Capability, strings.Join(parts, " · "))
	}
	if ch := RenderChange(d); ch != "" {
		b.WriteString("\n## Change\n\n" + ch)
		if !strings.HasSuffix(ch, "\n") {
			b.WriteString("\n")
		}
	}
	return b.Bytes(), nil
}
