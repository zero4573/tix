package session

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/zero4573/tix/assets"
	"github.com/zero4573/tix/internal/config"
	"github.com/zero4573/tix/internal/fsx"
	"github.com/zero4573/tix/internal/lock"
)

func PluginDir() (string, error) {
	d := os.Getenv("TIX_PLUGIN")
	if c := config.Get("pluginDir"); d == "" && c != "" && fsx.IsDir(c) {
		d = c
	}
	if d == "" {
		return assets.Materialize("plugin")
	}
	if _, err := os.Stat(filepath.Join(d, ".claude-plugin", "plugin.json")); err != nil {
		return "", fmt.Errorf("no tix plugin at %s (set TIX_PLUGIN, or reinstall)", d)
	}
	return d, nil
}

func Command(dirs ...string) ([]string, error) {
	plugin, err := PluginDir()
	if err != nil {
		return nil, err
	}
	return CommandPlugin(plugin, dirs...), nil
}

func CommandPlugin(plugin string, dirs ...string) []string {
	argv := strings.Fields(os.Getenv("TIX_CLAUDE"))
	if len(argv) == 0 {
		argv = []string{"claude"}
	}
	argv = append(argv, "--plugin-dir", plugin)
	for _, d := range dirs {
		if st, err := os.Stat(d); err == nil && st.IsDir() {
			argv = append(argv, "--add-dir="+d)
		}
	}
	return argv
}

func WriteSettings(dir string, extraDeny ...string) error {
	var deny []any
	for _, tool := range []string{"Edit", "Write", "NotebookEdit"} {
		deny = append(deny, fmt.Sprintf("%s(/%s/**)", tool, config.CacheDir()))
	}
	for _, r := range extraDeny {
		deny = append(deny, r)
	}
	env := map[string]any{}
	for k, v := range GoEnv(dir) {
		env[k] = v
	}
	var settings any = map[string]any{"permissions": map[string]any{"deny": deny}, "env": env}
	if f := os.Getenv("TIX_SESSION_SETTINGS"); f != "" {
		var extra any
		data, err := os.ReadFile(f)
		if err == nil {
			err = json.Unmarshal(data, &extra)
		}
		if err != nil {
			return fmt.Errorf("TIX_SESSION_SETTINGS (%s) isn't a readable JSON file", f)
		}
		settings = merge(settings, extra)
	}
	if err := os.MkdirAll(filepath.Join(dir, ".claude"), 0o755); err != nil {
		return err
	}
	if err := WriteIgnore(dir); err != nil {
		return err
	}
	if err := os.Remove(filepath.Join(dir, ".claude", "graph-mcp.json")); err != nil && !os.IsNotExist(err) {
		return err
	}
	return writeJSON(filepath.Join(dir, ".claude", "settings.json"), settings)
}

func ReposReadOnly(projectsRoot string) []string {
	var deny []string
	for _, tool := range []string{"Edit", "Write", "NotebookEdit"} {
		deny = append(deny, fmt.Sprintf("%s(/%s/**)", tool, filepath.Join(projectsRoot, "*", "*", "*")))
	}
	return deny
}

func CacheDir(dir string) string { return filepath.Join(dir, ".cache") }

func DevDir(dir string) string { return filepath.Join(CacheDir(dir), "dev") }

func GoEnv(dir string) map[string]string {
	goDir := filepath.Join(CacheDir(dir), "go")
	return map[string]string{
		"GOPATH":     goDir,
		"GOMODCACHE": filepath.Join(goDir, "pkg", "mod"),
		"GOCACHE":    filepath.Join(goDir, "build"),
	}
}

const ignoreFile = "# Written by tix; regenerated on every start, so don't edit it.\n" +
	"# rg (Claude's Grep) skips these unless a search names them.\n" +
	"/.cache/\n"

func WriteIgnore(dir string) error {
	return os.WriteFile(filepath.Join(dir, ".ignore"), []byte(ignoreFile), 0o644)
}

func merge(a, b any) any {
	switch av := a.(type) {
	case map[string]any:
		if bv, ok := b.(map[string]any); ok {
			out := map[string]any{}
			for k, v := range av {
				out[k] = v
			}
			for k, v := range bv {
				out[k] = merge(av[k], v)
			}
			return out
		}
	case []any:
		if bv, ok := b.([]any); ok {
			return union(av, bv)
		}
	}
	if b == nil {
		return a
	}
	return b
}

func union(a, b []any) []any {
	seen := map[string]bool{}
	type item struct {
		key string
		v   any
	}
	var items []item
	for _, v := range append(append([]any{}, a...), b...) {
		k, _ := json.Marshal(v)
		if !seen[string(k)] {
			seen[string(k)] = true
			items = append(items, item{string(k), v})
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].key < items[j].key })
	out := make([]any, len(items))
	for i, it := range items {
		out[i] = it.v
	}
	return out
}

func marshal(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	err := enc.Encode(v)
	return buf.Bytes(), err
}

func writeJSON(file string, v any) error {
	data, err := marshal(v)
	if err != nil {
		return err
	}
	return os.WriteFile(file, data, 0o644)
}

func ClaudeConfig() string {
	dir := os.Getenv("CLAUDE_CONFIG_DIR")
	if dir == "" {
		dir = config.Home()
	}
	return filepath.Join(dir, ".claude.json")
}

func Trust(dir string) error {
	if abs, err := filepath.Abs(dir); err == nil {
		dir = abs
	}
	if r, err := filepath.EvalSymlinks(dir); err == nil {
		dir = r
	}
	file := ClaudeConfig()
	if trusted(file, dir) {
		return nil
	}
	return lock.With(file+".tickets-lock", func() error {
		cfg := map[string]any{}
		if data, err := os.ReadFile(file); err == nil && len(bytes.TrimSpace(data)) > 0 {
			dec := json.NewDecoder(bytes.NewReader(data))
			dec.UseNumber()
			if err := dec.Decode(&cfg); err != nil {
				return fmt.Errorf("%s isn't valid JSON: %w", file, err)
			}
		}
		projects, _ := cfg["projects"].(map[string]any)
		if projects == nil {
			projects = map[string]any{}
			cfg["projects"] = projects
		}
		p, _ := projects[dir].(map[string]any)
		if p == nil {
			p = map[string]any{}
			projects[dir] = p
		}
		p["hasTrustDialogAccepted"] = true
		data, err := marshal(cfg)
		if err != nil {
			return err
		}
		// not a rename: running sandboxes bind-mount this file and would keep the old inode
		return os.WriteFile(file, data, 0o600)
	})
}

func trusted(file, dir string) bool {
	data, err := os.ReadFile(file)
	if err != nil {
		return false
	}
	var cfg struct {
		Projects map[string]struct {
			HasTrustDialogAccepted bool `json:"hasTrustDialogAccepted"`
		} `json:"projects"`
	}
	return json.Unmarshal(data, &cfg) == nil && cfg.Projects[dir].HasTrustDialogAccepted
}
