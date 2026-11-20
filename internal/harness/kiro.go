package harness

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/zero4573/tix/assets"
	"github.com/zero4573/tix/internal/session"
)

type kiro struct{}

func (kiro) Name() string                 { return Kiro }
func (kiro) SkillRef(skill string) string { return KiroSkill(skill) }

const AgentName = "tix"

func (k kiro) Prepare(s Session) error {
	plugin := s.Plugin
	if plugin == "" {
		var err error
		if plugin, err = session.PluginDir(); err != nil {
			return err
		}
	}
	dotKiro := filepath.Join(s.Dir, ".kiro")
	agents, err := RenderKiro(plugin, dotKiro)
	if err != nil {
		return err
	}
	if s.Instructions != "" {
		if err := os.WriteFile(filepath.Join(s.Dir, "AGENTS.md"), []byte(KiroText(s.Instructions)), 0o644); err != nil {
			return err
		}
	}
	if s.Settings {
		if err := session.WriteIgnore(s.Dir); err != nil {
			return err
		}
	}
	exe := s.Hooks
	if exe == "" {
		if exe, err = assets.Executable(); err != nil {
			return err
		}
	}
	return os.WriteFile(filepath.Join(dotKiro, "agents", AgentName+".json"), kiroAgent(s, exe, agents), 0o644)
}

func kiroAgent(s Session, exe string, subagents []string) []byte {
	var p strings.Builder
	p.WriteString("You are a tix session (the ticket workflow, with an Obsidian vault as long-term memory).\n")
	if s.Instructions != "" {
		p.WriteString("This workspace's instructions are in `AGENTS.md`: read them first and follow them.\n")
	}
	if dirs := append(append([]string{}, s.Dirs...), s.ReadOnly...); len(dirs) > 0 || s.ReposRoot != "" {
		p.WriteString("\nFolders this session works in besides the workspace:\n")
		for _, d := range s.Dirs {
			fmt.Fprintf(&p, "- `%s` (read-write)\n", d)
		}
		if s.ReposRoot != "" {
			fmt.Fprintf(&p, "- `%s` (the main clones: read-only, never edit, commit or push there)\n", s.ReposRoot)
		}
		for _, d := range s.ReadOnly {
			fmt.Fprintf(&p, "- `%s` (read-only)\n", d)
		}
	}
	if len(s.DenyShell) > 0 {
		p.WriteString("\nNever run these commands: `" + strings.Join(s.DenyShell, "`, `") + "`.\n")
	}
	if len(subagents) > 0 {
		p.WriteString("\nThe workflow's agents are your subagent tools: subagent_" + strings.Join(subagents, ", subagent_") + ".\n")
	}
	if s.SystemPrompt != "" {
		p.WriteString("\n" + KiroText(s.SystemPrompt) + "\n")
	}
	hook := func(args string) []map[string]any {
		return []map[string]any{{"command": fmt.Sprintf("%q hook --agent kiro %s", exe, args)}}
	}
	cfg := map[string]any{
		"name":           AgentName,
		"description":    "tix session",
		"prompt":         p.String(),
		"model":          KiroSessionModel,
		"tools":          []string{"*"},
		"includeMcpJson": true,
		"hooks": map[string]any{
			"userPromptSubmit": hook("agent-state working"),
			"postToolUse":      []map[string]any{{"matcher": "*", "command": hook("agent-state working")[0]["command"]}},
			"stop":             hook("agent-state idle"),
		},
	}
	if s.Instructions != "" {
		cfg["resources"] = []string{"file://AGENTS.md"}
	}
	out, _ := marshalIndent(cfg)
	return out
}

func (h kiro) Argv(s Session) ([]string, error) {
	argv := strings.Fields(os.Getenv("TIX_KIRO"))
	if len(argv) == 0 {
		argv = []string{"kiro-cli"}
	}
	argv = append(argv, "chat", "--agent", AgentName)
	if s.Continue {
		argv = append(argv, "--resume")
	}
	if s.Print {
		argv = append(argv, "--no-interactive")
	}
	argv = append(argv, s.Extra...)
	if p := FirstPrompt(h, s); p != "" {
		argv = append(argv, p)
	}
	return argv, nil
}

func (kiro) Env(s Session) map[string]string {
	env := sandboxEnv(Session{Dirs: existing(s.Dirs), ReadOnly: existing(s.ReadOnly), ReposRoot: s.ReposRoot})
	if s.Settings {
		for k, v := range session.GoEnv(s.Dir) {
			env[k] = v
		}
	}
	return env
}
