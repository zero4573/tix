package diffview

import (
	"fmt"

	"github.com/zero4573/tix/internal/gitx"
)

type Git struct{}

func (Git) Interactive() bool { return false }

func (Git) Available(func(string) (string, error)) bool { return true }

func (Git) Show(repos []Repo, o Options) error {
	for _, r := range repos {
		fmt.Fprintf(o.Stdout, "=== %s (%s vs %s) ===\n", r.Name, r.Branch, r.Base)
		if r.Missing() {
			fmt.Fprintf(o.Stdout, "(worktree missing: %s)\n", r.Dir)
			continue
		}
		args := []string{"--no-pager", "diff", "--no-ext-diff"}
		if o.Stat {
			args = append(args, "--stat")
		}
		cmd := gitx.CommandIn(r.Dir, append(args, r.MergeBase)...)
		cmd.Stdout, cmd.Stderr = o.Stdout, o.Stderr
		_ = cmd.Run()
		if files := untracked(r.Dir); len(files) > 0 {
			fmt.Fprintln(o.Stdout, "--- untracked files:")
			for _, f := range files {
				fmt.Fprintln(o.Stdout, f)
			}
		}
		fmt.Fprintln(o.Stdout)
	}
	return nil
}
