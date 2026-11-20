package clean

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/zero4573/tix/internal/gitx"
	"github.com/zero4573/tix/internal/workspace"
)

func safety(o Options, id, status string, dirs []string) (reason, task string) {
	for _, dir := range dirs {
		if !hasWorkspace(dir) {
			continue
		}
		ws := filepath.Base(dir)
		if o.running(dir) {
			return "session running (" + ws + ")", ""
		}
		info, _ := workspace.Read(dir)
		for _, r := range info.Repos {
			_, err := os.Stat(filepath.Join(r.Path, ".git"))
			if err != nil {
				continue
			}
			if gitx.Dirty(r.Path, "--ignore-submodules") {
				return "uncommitted changes in " + r.Slug, fmt.Sprintf(
					"[[%s]] (%s): %s has uncommitted changes in %s. Commit or drop them (tix ws diff %s), push, then tix clean %s",
					id, status, r.Path, ws, ws, id)
			}
			if sub := gitx.PopulatedSubmodule(r.Path); sub != "" {
				return "submodule checked out in " + r.Slug, fmt.Sprintf(
					"[[%s]] (%s): %s has a submodule checked out at %s, whose work tix doesn't check. Check it by hand (commit and push its work, or deinit it), then tix clean %s (tix ws rm has no override for this stop)",
					id, status, r.Path, sub, id)
			}
			if lost, err := gitx.Lost(r.Path, r.Base); err != nil || lost {
				return "commits on no remote in " + r.Slug, fmt.Sprintf(
					"[[%s]] (%s): %s has commits that aren't on any remote (or a stash). Push them (/tix:save in tix start %s, or git push on the host), then tix clean %s",
					id, status, r.Path, id, id)
			}
		}
	}
	return "", ""
}
