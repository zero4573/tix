package vault

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/zero4573/tix/internal/config"
	"github.com/zero4573/tix/internal/fsx"
	"github.com/zero4573/tix/internal/gitx"
)

func List() []string {
	root := config.ObsidianRoot()
	entries, _ := os.ReadDir(root)
	var out []string
	for _, e := range entries {
		if st, err := os.Stat(filepath.Join(root, e.Name(), ".obsidian")); err == nil && st.IsDir() {
			out = append(out, e.Name())
		}
	}
	return out
}

func isVault(dir string) bool {
	st, err := os.Stat(filepath.Join(dir, ".obsidian"))
	return err == nil && st.IsDir()
}

func Resolve(name string) (string, error) {
	if isVault(name) {
		return filepath.Abs(name)
	}
	p := filepath.Join(config.ObsidianRoot(), name)
	if isVault(p) {
		return p, nil
	}
	return "", fmt.Errorf("no vault named '%s' under %s", name, config.ObsidianRoot())
}

func workspaceVault(dir string) string {
	for d := dir; d != "" && d != "." && filepath.Dir(d) != d; d = filepath.Dir(d) {
		data, err := os.ReadFile(filepath.Join(d, "workspace.json"))
		if err != nil {
			continue
		}
		var ws struct {
			Vault string `json:"vault"`
		}
		if json.Unmarshal(data, &ws) == nil && ws.Vault != "" {
			return ws.Vault
		}
	}
	return ""
}

func Default() (path string, ok bool, err error) {
	name := config.LaunchVault
	if name == "" {
		if wd, e := os.Getwd(); e == nil {
			name = workspaceVault(wd)
		}
	}
	if name == "" {
		name = SavedDefault()
	}
	if name == "" {
		return "", false, nil
	}
	p, err := Resolve(name)
	if err != nil {
		return "", false, fmt.Errorf("default vault '%s' isn't a vault (tix vault default to change it), ignoring it", name)
	}
	return p, true, nil
}

func SavedDefault() string {
	data, _ := os.ReadFile(config.DefaultVaultFile())
	return strings.TrimSpace(string(data))
}

func Current() (string, error) {
	p, ok, err := Default()
	if ok {
		return p, nil
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "tix: "+err.Error())
	}
	vaults := List()
	switch {
	case len(vaults) == 1:
		return filepath.Join(config.ObsidianRoot(), vaults[0]), nil
	case len(vaults) == 0:
		if err != nil {
			return "", err
		}
		return "", fmt.Errorf("no Obsidian vaults under %s (set one up with tix vault init)", config.ObsidianRoot())
	default:
		return "", fmt.Errorf("no default vault, and several under %s (%s): pick one with tix vault default <name>",
			config.ObsidianRoot(), strings.Join(vaults, " "))
	}
}

func SetDefault(vault string) (string, error) {
	_ = os.Remove(config.LegacyDefaultVaultFile())
	saved := vault
	if filepath.Dir(vault) == filepath.Clean(config.ObsidianRoot()) {
		saved = filepath.Base(vault)
	}
	file := filepath.Join(config.Dir(), "default-vault")
	if err := os.MkdirAll(config.Dir(), 0o755); err != nil {
		return "", err
	}
	return saved, os.WriteFile(file, []byte(saved+"\n"), 0o644)
}

func UnsetDefault() error {
	_ = os.Remove(config.LegacyDefaultVaultFile())
	err := os.Remove(filepath.Join(config.Dir(), "default-vault"))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

type Locations struct {
	WorkRoot     string
	ProjectsRoot string
}

func LocationsOf(vault string) Locations {
	name := filepath.Base(vault)
	var wf struct {
		WorkRoot     string `json:"workRoot"`
		ProjectsRoot string `json:"projectsRoot"`
	}
	if data, err := os.ReadFile(filepath.Join(vault, ".workflow.json")); err == nil {
		_ = json.Unmarshal(data, &wf)
	}
	l := Locations{
		WorkRoot:     config.ExpandHome(wf.WorkRoot),
		ProjectsRoot: config.ExpandHome(wf.ProjectsRoot),
	}
	if wf.WorkRoot == "" {
		l.WorkRoot = filepath.Join(config.Home(), "Projects", "work-"+name)
	}
	if wf.ProjectsRoot == "" {
		l.ProjectsRoot = filepath.Join(config.Home(), "Projects", "repo-"+name)
	}
	return l
}

type Context struct {
	Vault string
	Locations
}

func Require() (Context, error) {
	v, err := Current()
	if err != nil {
		return Context{}, err
	}
	return use(v)
}

func use(v string) (Context, error) {
	l := LocationsOf(v)
	if err := l.Validate(); err != nil {
		return Context{}, err
	}
	os.Setenv("TIX_VAULT", v)
	gitx.TrustRoots(l.ProjectsRoot, l.WorkRoot)
	return Context{v, l}, nil
}

func (l Locations) Validate() error {
	if fsx.Within(canonical(l.WorkRoot), canonical(l.ProjectsRoot)) {
		return fmt.Errorf("workRoot %s is inside projectsRoot %s: set them to separate folders in .workflow.json", l.WorkRoot, l.ProjectsRoot)
	}
	return nil
}

func PathsOverlap(a, b string) bool {
	a, b = canonical(a), canonical(b)
	return fsx.Within(a, b) || fsx.Within(b, a)
}

func canonical(p string) string {
	p = filepath.Clean(config.ExpandHome(p))
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	dir, rest := p, ""
	for dir != "." && filepath.Dir(dir) != dir {
		if r, err := filepath.EvalSymlinks(dir); err == nil {
			return filepath.Join(r, rest)
		}
		rest = filepath.Join(filepath.Base(dir), rest)
		dir = filepath.Dir(dir)
	}
	return p
}

func Optional() (Context, error) {
	v, ok, _ := Default()
	if !ok {
		if vs := List(); len(vs) == 1 {
			v, ok = filepath.Join(config.ObsidianRoot(), vs[0]), true
		}
	}
	if ok {
		return use(v)
	}
	home := config.Home()
	return Context{Locations: Locations{
		WorkRoot:     filepath.Join(home, "Projects", "work"),
		ProjectsRoot: filepath.Join(home, "Projects"),
	}}, nil
}

func PathsSame(a, b string) bool {
	return a != "" && b != "" && canonical(a) == canonical(b)
}

func AgentOf(vault string) string {
	var wf struct {
		Agent string `json:"agent"`
	}
	if data, err := os.ReadFile(filepath.Join(vault, ".workflow.json")); err == nil {
		_ = json.Unmarshal(data, &wf)
	}
	return wf.Agent
}

func ToolVersionsOf(vault string) (map[string]string, []string, string) {
	out := map[string]string{"golang": "latest", "python": "latest"}
	file := filepath.Join(vault, ".workflow.json")
	data, err := os.ReadFile(file)
	if err != nil {
		return out, nil, ""
	}
	var wf struct {
		ToolVersions map[string]any `json:"toolVersions"`
	}
	if err := json.Unmarshal(data, &wf); err != nil {
		return out, nil, fmt.Sprintf("tix: %s: %v; using the default tool versions", file, err)
	}
	set := map[string]string{}
	for k, v := range wf.ToolVersions {
		str, ok := v.(string)
		if !ok {
			return out, nil, fmt.Sprintf("tix: %s: toolVersions.%s isn't a string; using the default tool versions", file, k)
		}
		set[k] = str
	}
	var opted []string
	for k, v := range set {
		if v == "" || v == "none" {
			delete(out, k)
			opted = append(opted, k)
		} else {
			out[k] = v
		}
	}
	sort.Strings(opted)
	return out, opted, ""
}
