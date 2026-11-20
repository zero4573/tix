package workspace

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/zero4573/tix/internal/fsx"
	"github.com/zero4573/tix/internal/lock"
)

type Repo struct {
	Provider      string          `json:"provider"`
	Owner         string          `json:"owner"`
	Repo          string          `json:"repo"`
	Slug          string          `json:"slug"`
	Path          string          `json:"path"`
	Base          string          `json:"base"`
	Branch        string          `json:"branch"`
	Mode          string          `json:"mode"`
	TargetVersion *string         `json:"targetVersion"`
	Marks         map[string]Mark `json:"marks,omitempty"`
}

func (r Repo) Clone() string { return r.Provider + "/" + r.Owner + "/" + r.Repo }

var Warnings io.Writer = os.Stderr

func File(dir string) string { return filepath.Join(dir, "workspace.json") }

func Agent(dir string) string {
	var doc struct {
		Agent string `json:"agent"`
	}
	if data, err := os.ReadFile(File(dir)); err == nil {
		_ = json.Unmarshal(data, &doc)
	}
	if doc.Agent == "" {
		return "claude"
	}
	return doc.Agent
}

func SetAgent(dir, agent string) error {
	return Update(File(dir), func(doc map[string]any) error {
		doc["agent"] = agent
		return nil
	})
}

func Update(file string, fn func(doc map[string]any) error) error {
	return lock.With(file+".lock", func() error {
		doc := map[string]any{}
		if data, err := os.ReadFile(file); err == nil && len(data) > 0 {
			if err := json.Unmarshal(data, &doc); err != nil {
				return err
			}
		}
		if err := fn(doc); err != nil {
			return err
		}
		data, err := json.MarshalIndent(doc, "", "  ")
		if err != nil {
			return err
		}
		tmp := file + ".tmp"
		if err := os.WriteFile(tmp, append(data, '\n'), 0o644); err != nil {
			return err
		}
		return os.Rename(tmp, file)
	})
}

func Ensure(dir, id, vault string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return Update(File(dir), func(doc map[string]any) error {
		if _, ok := doc["id"]; !ok {
			doc["id"] = id
		}
		if _, ok := doc["repos"]; !ok {
			doc["repos"] = []any{}
		}
		if vault != "" {
			doc["vault"] = vault
		}
		return nil
	})
}

func Migrate(oldDir, newDir string) (bool, error) {
	if _, err := os.Stat(oldDir); err != nil {
		return false, nil
	}
	if _, err := os.Stat(newDir); err == nil {
		fmt.Fprintf(warnOut, "warning: %s is left over next to %s and tix no longer lists it; move what you need out of it and remove it\n", oldDir, newDir)
		return false, nil
	}
	if Running(oldDir) {
		return false, fmt.Errorf("a session is running in %s: close it first, then run tix again", oldDir)
	}
	if err := os.Rename(oldDir, newDir); err != nil {
		return false, err
	}
	oldReal := evalLoose(oldDir)
	err := Update(File(newDir), func(doc map[string]any) error {
		repos, _ := doc["repos"].([]any)
		for _, r := range repos {
			m, _ := r.(map[string]any)
			p, _ := m["path"].(string)
			if rel, ok := under(oldReal, evalLoose(p)); ok {
				m["path"] = filepath.Join(newDir, rel)
			}
		}
		return nil
	})
	return true, err
}

var warnOut io.Writer = os.Stderr

func under(dir, p string) (string, bool) {
	rel, err := filepath.Rel(dir, p)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false
	}
	return rel, true
}

func evalLoose(p string) string {
	p = filepath.Clean(p)
	rest := ""
	for cur := p; ; {
		if r, err := filepath.EvalSymlinks(cur); err == nil {
			return filepath.Join(r, rest)
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return p
		}
		rest = filepath.Join(filepath.Base(cur), rest)
		cur = parent
	}
}

type Info struct {
	ID    string `json:"id"`
	Vault string `json:"vault"`
	Repos []Repo `json:"repos"`
	Dev   *Dev   `json:"dev,omitempty"`
}

type Dev struct {
	Slug string `json:"slug"`
}

func SetDev(file string, d *Dev) error {
	return Update(file, func(doc map[string]any) error {
		if d == nil {
			delete(doc, "dev")
		} else {
			doc["dev"] = map[string]any{"slug": d.Slug}
		}
		return nil
	})
}

func Read(dir string) (Info, error) {
	var info Info
	data, err := os.ReadFile(File(dir))
	if err != nil {
		return info, err
	}
	if err := json.Unmarshal(data, &info); err != nil {
		return info, err
	}
	root := resolved(dir)
	kept := info.Repos[:0]
	for _, r := range info.Repos {
		if r.Path != "" && filepath.IsAbs(r.Path) && fsx.Inside(resolved(r.Path), root) {
			kept = append(kept, r)
		} else {
			fmt.Fprintf(Warnings, "tix: %s: ignoring %s at %q: not inside the workspace\n", File(dir), r.Slug, r.Path)
		}
	}
	info.Repos = kept
	return info, nil
}

func resolved(p string) string {
	p, _ = filepath.Abs(p)
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	if r, err := filepath.EvalSymlinks(filepath.Dir(p)); err == nil {
		return filepath.Join(r, filepath.Base(p))
	}
	return p
}

type By int

const (
	ByNone By = iota
	ByPID
	ByAgentState
)

type Status struct {
	Running bool
	By      By
	PID     int
	Started string
	State   State
}

func Session(dir string) Status {
	st := Status{State: ReadState(dir)}
	switch live, rec := sessionLiveness(dir); live {
	case LivenessAlive:
		st.Running, st.By, st.PID, st.Started = true, ByPID, rec.PID, rec.Started
		return st
	case LivenessDead:
		return st
	}
	fi, err := os.Stat(filepath.Join(dir, ".agent-state"))
	if err != nil || time.Since(fi.ModTime()) > 24*time.Hour {
		return st
	}
	if st.State.State != "" && st.State.State != "exited" {
		st.Running, st.By = true, ByAgentState
	}
	return st
}

func Running(dir string) bool {
	return Session(dir).Running
}

type State struct {
	State  string `json:"state"`
	Reason string `json:"reason"`
	TS     string `json:"ts"`
}

func ReadState(dir string) State {
	var s State
	if data, err := os.ReadFile(filepath.Join(dir, ".agent-state")); err == nil {
		_ = json.Unmarshal(data, &s)
	}
	return s
}

func List(workRoot string, withSpec bool) []string {
	all, _ := filepath.Glob(filepath.Join(workRoot, "*", "workspace.json"))
	var matches []string
	for _, m := range all {
		if b := filepath.Base(filepath.Dir(m)); b[0] != '.' {
			matches = append(matches, m)
		}
	}
	if withSpec {
		spec, _ := filepath.Glob(filepath.Join(workRoot, ".spec-*", "workspace.json"))
		matches = append(matches, spec...)
	}
	var dirs []string
	for _, m := range matches {
		dirs = append(dirs, filepath.Dir(m))
	}
	return dirs
}

func Put(file string, r Repo) error {
	return Update(file, func(doc map[string]any) error {
		old, _ := doc["repos"].([]any)
		var repos []any
		var version, marks any
		for _, e := range old {
			if m, ok := e.(map[string]any); ok && m["slug"] == r.Slug {
				version = m["targetVersion"]
				marks = m["marks"]
				continue
			}
			repos = append(repos, e)
		}
		if r.TargetVersion != nil && *r.TargetVersion != "" {
			version = *r.TargetVersion
		}
		entry := map[string]any{
			"provider": r.Provider, "owner": r.Owner, "repo": r.Repo, "slug": r.Slug,
			"path": r.Path, "base": r.Base, "branch": r.Branch, "mode": r.Mode,
			"targetVersion": version,
		}
		if len(r.Marks) > 0 {
			marks = r.Marks
		}
		if marks != nil {
			entry["marks"] = marks
		}
		repos = append(repos, entry)
		doc["repos"] = repos
		return nil
	})
}

func Drop(file, slug string) error {
	return Update(file, func(doc map[string]any) error {
		old, _ := doc["repos"].([]any)
		repos := []any{}
		for _, e := range old {
			if m, ok := e.(map[string]any); ok && m["slug"] == slug {
				continue
			}
			repos = append(repos, e)
		}
		doc["repos"] = repos
		return nil
	})
}
