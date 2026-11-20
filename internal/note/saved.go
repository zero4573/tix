package note

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/zero4573/tix/internal/gitx"
	"github.com/zero4573/tix/internal/workspace"
)

type SaveState int

const (
	Unsaved SaveState = iota
	Saved
	NothingToSave
)

func (s SaveState) String() string {
	return [...]string{"unsaved", "saved", "nothing to save"}[s]
}

var summaryRe = regexp.MustCompile(`(?m)^---\ncreated: \*\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d\*\n`)

func SaveStateOf(vault, wsDir, id string) (SaveState, string) {
	dir := TicketDir(vault, id)
	if d := pendingDraft(filepath.Join(dir, "kb-drafts")); d != "" {
		return Unsaved, "kb-drafts/" + d + " isn't promoted"
	}
	file := TicketPath(vault, id)
	if s := Frontmatter(file)["saved"]; s != "" {
		return Saved, "saved " + s
	}
	if data, err := os.ReadFile(file); err == nil && summaryRe.MatchString(body(Normalize(string(data)))) {
		return Saved, "summary block"
	}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if e.Name() != id+".md" && !strings.HasPrefix(e.Name(), ".") {
			return Unsaved, "worked (" + e.Name() + "), not saved"
		}
	}
	if worked, why := committed(wsDir); worked {
		return Unsaved, why
	}
	return NothingToSave, "never worked"
}

func body(s string) string {
	if !strings.HasPrefix(s, "---\n") {
		return s
	}
	if i := strings.Index(s[3:], "\n---\n"); i >= 0 {
		return s[3+i+5:]
	}
	return ""
}

func pendingDraft(dir string) string {
	found := ""
	_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || found != "" {
			return nil
		}
		if !d.IsDir() && strings.HasSuffix(p, ".md") && Frontmatter(p)["status"] != "merged" {
			found, _ = filepath.Rel(dir, p)
			found = filepath.ToSlash(found)
		}
		return nil
	})
	return found
}

func committed(wsDir string) (bool, string) {
	if wsDir == "" {
		return false, ""
	}
	info, err := workspace.Read(wsDir)
	if err != nil {
		return false, ""
	}
	for _, r := range info.Repos {
		if _, err := os.Stat(filepath.Join(r.Path, ".git")); err != nil {
			continue
		}
		n, err := gitx.Out(r.Path, "rev-list", "--count", gitx.BaseRef(r.Path, r.Base)+"..HEAD")
		if err != nil || n != "0" {
			return true, "commits in " + r.Slug + ", not saved"
		}
	}
	return false, ""
}
