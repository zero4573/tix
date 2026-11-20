package harness

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/zero4573/tix/internal/session"
)

type claude struct{}

func (claude) Name() string                 { return Claude }
func (claude) SkillRef(skill string) string { return "/tix:" + skill }

func (claude) Prepare(s Session) error {
	if s.Settings {
		var deny []string
		if s.ReposRoot != "" {
			deny = append(deny, session.ReposReadOnly(s.ReposRoot)...)
		}
		for _, c := range s.DenyShell {
			deny = append(deny, "Bash("+c+":*)")
		}
		if err := session.WriteSettings(s.Dir, deny...); err != nil {
			return err
		}
		if err := session.Trust(s.Dir); err != nil {
			fmt.Fprintf(os.Stderr, "tix: couldn't mark %s as trusted in %s: %v\n", s.Dir, session.ClaudeConfig(), err)
		}
		if err := renderReviewAgents(s); err != nil {
			fmt.Fprintf(os.Stderr, "tix: couldn't write the kiro review agents to %s: %v (deep reviews stay on Claude models)\n", s.Dir, err)
		}
	}
	if s.Instructions != "" {
		return os.WriteFile(filepath.Join(s.Dir, "CLAUDE.md"), []byte(s.Instructions), 0o644)
	}
	return nil
}

func (h claude) Argv(s Session) ([]string, error) {
	plugin := s.Plugin
	if plugin == "" {
		var err error
		if plugin, err = session.PluginDir(); err != nil {
			return nil, err
		}
	}
	argv := session.CommandPlugin(plugin, claudeDirs(s)...)
	if s.SystemPrompt != "" {
		argv = append(argv, "--append-system-prompt", s.SystemPrompt)
	}
	if s.Settings {
		argv = append(argv, "--permission-mode", "auto")
	}
	if s.Continue {
		argv = append(argv, "--continue")
	}
	argv = append(argv, s.Extra...)
	if p := FirstPrompt(h, s); p != "" {
		if s.Print {
			argv = append(argv, "-p", p, "--output-format", "stream-json", "--verbose")
		} else {
			argv = append(argv, p)
		}
	}
	return argv, nil
}

func claudeDirs(s Session) []string {
	dirs := append([]string{}, s.Dirs...)
	if s.ReposRoot != "" {
		dirs = append(dirs, s.ReposRoot)
	}
	return append(dirs, s.ReadOnly...)
}

func (claude) Env(s Session) map[string]string {
	return sandboxEnv(Session{Dirs: existing(s.Dirs), ReadOnly: existing(s.ReadOnly), ReposRoot: s.ReposRoot})
}

func renderReviewAgents(s Session) error {
	plugin := s.Plugin
	if plugin == "" {
		var err error
		if plugin, err = session.PluginDir(); err != nil {
			return err
		}
	}
	_, err := RenderKiro(plugin, filepath.Join(s.Dir, ".kiro"))
	return err
}
