package ticketconv

import (
	"os"
	"path/filepath"
	"strings"
)

func RewriteWorkspace(doc map[string]any, id, oldDir, newDir string) bool {
	changed := false
	if cur, _ := doc["id"].(string); cur != id {
		doc["id"], changed = id, true
	}
	repos, _ := doc["repos"].([]any)
	for _, r := range repos {
		m, ok := r.(map[string]any)
		if !ok {
			continue
		}
		p, _ := m["path"].(string)
		if np, ok := Reparent(p, oldDir, newDir); ok {
			m["path"], changed = np, true
		}
	}
	return changed
}

func Reparent(path, oldDir, newDir string) (string, bool) {
	if path == "" {
		return path, false
	}
	rel, err := filepath.Rel(filepath.Clean(oldDir), filepath.Clean(path))
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return path, false
	}
	return filepath.Join(newDir, rel), true
}

func MainGitDir(dir string) string {
	data, err := os.ReadFile(filepath.Join(dir, ".git"))
	if err != nil {
		return ""
	}
	g, ok := strings.CutPrefix(strings.TrimSpace(string(data)), "gitdir:")
	if !ok {
		return ""
	}
	g = filepath.Clean(strings.TrimSpace(g))
	if !filepath.IsAbs(g) {
		g = filepath.Join(dir, g)
	}
	if filepath.Base(filepath.Dir(g)) != "worktrees" {
		return ""
	}
	return filepath.Dir(filepath.Dir(g))
}
