package spec

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/zero4573/tix/internal/note"
)

const (
	InRepo  = "repo"
	InVault = "vault"
	Unset   = ""
)

type Location struct {
	Kind  string `json:"kind"`
	Dir   string `json:"dir"`
	Field string `json:"field"`
}

func ProjectNote(vault, slug string) string {
	return filepath.Join(vault, "projects", slug, slug+".md")
}

func VaultDir(vault, slug string) string {
	return filepath.Join(vault, "projects", slug, "specs")
}

func Resolve(vault, slug, repoDir string) (Location, error) {
	field := note.Get(ProjectNote(vault, slug), "specs")
	switch field {
	case InRepo:
		return Location{Kind: InRepo, Dir: filepath.Join(repoDir, "specs"), Field: field}, nil
	case InVault:
		return Location{Kind: InVault, Dir: VaultDir(vault, slug), Field: field}, nil
	case "":
		if st, err := os.Stat(filepath.Join(repoDir, "specs")); err == nil && st.IsDir() {
			return Location{Kind: InRepo, Dir: filepath.Join(repoDir, "specs")}, nil
		}
		return Location{}, nil
	}
	return Location{}, fmt.Errorf("%s: specs must be repo or vault, got %q", ProjectNote(vault, slug), field)
}
