package harness

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	Claude = "claude"
	Kiro   = "kiro"
)

type Session struct {
	Dir                 string
	Dirs                []string
	ReadOnly            []string
	ReposRoot           string
	DenyShell           []string
	Instructions        string
	SystemPrompt        string
	Skill, Args, Prompt string
	Continue            bool
	Plugin              string
	Hooks               string
	Settings            bool
	Print               bool
	Extra               []string
}

type Harness interface {
	Name() string
	Prepare(s Session) error
	Argv(s Session) ([]string, error)
	Env(s Session) map[string]string
	SkillRef(skill string) string
}

func Get(name string) (Harness, error) {
	switch name {
	case "", Claude:
		return claude{}, nil
	case Kiro:
		return kiro{}, nil
	}
	return nil, fmt.Errorf("unknown agent %q (claude or kiro)", name)
}

func Select(flag, vaultAgent string) (Harness, error) {
	for _, n := range []string{flag, os.Getenv("TIX_AGENT"), vaultAgent} {
		if n = strings.TrimSpace(n); n != "" {
			return Get(n)
		}
	}
	return claude{}, nil
}

func FirstPrompt(h Harness, s Session) string {
	if s.Prompt != "" {
		return s.Prompt
	}
	if s.Skill == "" {
		return ""
	}
	p := h.SkillRef(s.Skill)
	if s.Args != "" {
		p += " " + s.Args
	}
	return p
}

func sandboxEnv(s Session, readOnly ...string) map[string]string {
	rw := append([]string{}, s.Dirs...)
	ro := append(append([]string{}, s.ReadOnly...), readOnly...)
	if s.ReposRoot != "" {
		ro = append(ro, s.ReposRoot)
	}
	return map[string]string{
		"SANDBOX_ADD_DIRS": joinList(os.Getenv("SANDBOX_ADD_DIRS"), rw),
		"SANDBOX_RO_PATHS": joinList(os.Getenv("SANDBOX_RO_PATHS"), ro),
	}
}

func joinList(have string, add []string) string {
	seen := map[string]bool{}
	var out []string
	for _, p := range append(strings.Split(have, string(os.PathListSeparator)), add...) {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if c := filepath.Clean(p); !seen[c] {
			seen[c] = true
			out = append(out, p)
		}
	}
	return strings.Join(out, string(os.PathListSeparator))
}

func existing(ds []string) []string {
	var out []string
	for _, d := range ds {
		if st, err := os.Stat(d); err == nil && st.IsDir() {
			out = append(out, d)
		}
	}
	return out
}
