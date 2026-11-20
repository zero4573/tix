package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/zero4573/tix/internal/fsx"
	"github.com/zero4573/tix/internal/gitx"
	"github.com/zero4573/tix/internal/vault"
	"github.com/zero4573/tix/internal/workspace"
)

func wsTidyCleanCmd() *cobra.Command {
	var force, dry bool
	cmd := &cobra.Command{
		Use:   "tidy-clean <ID> [--force] [--dry-run]",
		Short: "Remove the tidy skill's scratch clones under <ws>/tasks/",
		Long: `Removes the scratch clones the tidy skill leaves in <workRoot>/<ID>/tasks/
(tidy-<slug>, or tidy-<slug>-<suffix> for a second tidy of the same repo;
the sandbox denies rm -rf, so nobody there can). A clone goes when its
bookmark (the ticket branch) is spent: a commit already on the ticket
worktree's branch, on its refs/tidy-backup/<branch> or on its
remote-tracking branch, or one whose tree is the ticket branch's tree (the
tidied history was moved to the branch and then signed or rewritten again,
so nothing but that history would be lost). Any other clone is kept with a
warning unless --force. Nothing else under tasks/ is touched, task worktrees
included.`,
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: completeWorkspaceID,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, err := vault.Require()
			if err != nil {
				return err
			}
			return wsTidyClean(ctx, args[0], force, dry)
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "remove unmerged clones too")
	cmd.Flags().BoolVar(&dry, "dry-run", false, "show what would be removed")
	return cmd
}

func wsTidyClean(ctx vault.Context, key string, force, dry bool) error {
	if err := requireKey("tidy-clean", key); err != nil {
		return err
	}
	dir := filepath.Join(ctx.WorkRoot, key)
	if !fsx.IsDir(dir) {
		return fmt.Errorf("tidy-clean: no workspace at %s", dir)
	}
	tasks := filepath.Join(dir, "tasks")
	entries, _ := os.ReadDir(tasks)
	var names []string
	for _, e := range entries {
		if e.IsDir() && strings.HasPrefix(e.Name(), "tidy-") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	if len(names) == 0 {
		fmt.Printf("tix ws: no tidy clones in %s\n", tasks)
		return nil
	}
	info, err := workspace.Read(dir)
	if err != nil {
		return fmt.Errorf("tidy-clean: %w", err)
	}
	repos := map[string]workspace.Repo{}
	for _, r := range info.Repos {
		repos[r.Slug] = r
	}
	kept := 0
	for _, name := range names {
		path := filepath.Join(tasks, name)
		slug := tidySlug(repos, name)
		reason := tidyUnmerged(repos, slug, path)
		if reason != "" && !force {
			warnf("%s: %s, not removing (use --force)", path, reason)
			kept++
			continue
		}
		if dry {
			fmt.Printf("tix ws: would remove %s\n", path)
			continue
		}
		if err := fsx.RemoveAll(path); err != nil {
			return fmt.Errorf("tidy-clean: %w", err)
		}
		fmt.Printf("tix ws: removed %s\n", path)
	}
	if kept > 0 {
		return fmt.Errorf("tidy-clean: kept %d clone(s)", kept)
	}
	return nil
}

func tidySlug(repos map[string]workspace.Repo, name string) string {
	rest := strings.TrimPrefix(name, "tidy-")
	best := ""
	for slug := range repos {
		if (rest == slug || strings.HasPrefix(rest, slug+"-")) && len(slug) > len(best) {
			best = slug
		}
	}
	if best == "" {
		return rest
	}
	return best
}

func tidySpent(r workspace.Repo, sha string) bool {
	for _, ref := range []string{"refs/heads/" + r.Branch, "refs/tidy-backup/" + r.Branch, "refs/remotes/origin/" + r.Branch} {
		if gitx.HasRef(r.Path, ref) && gitx.Ok(r.Path, "merge-base", "--is-ancestor", sha, ref) {
			return true
		}
	}
	tree, err1 := gitx.Out(r.Path, "rev-parse", "-q", "--verify", sha+"^{tree}")
	head, err2 := gitx.Out(r.Path, "rev-parse", "-q", "--verify", "refs/heads/"+r.Branch+"^{tree}")
	return err1 == nil && err2 == nil && tree != "" && tree == head
}

func tidyUnmerged(repos map[string]workspace.Repo, slug, path string) string {
	r, ok := repos[slug]
	if !ok {
		return "no repo " + slug + " in the workspace"
	}
	sha := readBranchRef(filepath.Join(path, ".git"), r.Branch)
	if sha == "" {
		return "no bookmark " + r.Branch
	}
	if !tidySpent(r, sha) {
		short := sha
		if len(short) > 7 {
			short = short[:7]
		}
		return fmt.Sprintf("bookmark %s isn't on %s", short, r.Branch)
	}
	return ""
}

func readBranchRef(gitDir, branch string) string {
	if fi, err := os.Lstat(gitDir); err != nil || !fi.IsDir() {
		return ""
	}
	ref := "refs/heads/" + branch
	if b, ok := readRefFile(filepath.Join(gitDir, filepath.FromSlash(ref)), 1<<10); ok {
		if sha := strings.TrimSpace(string(b)); isHexSHA(sha) {
			return sha
		}
	}
	b, ok := readRefFile(filepath.Join(gitDir, "packed-refs"), 64<<20)
	if !ok {
		return ""
	}
	for _, line := range strings.Split(string(b), "\n") {
		if sha, name, ok := strings.Cut(strings.TrimSpace(line), " "); ok && name == ref && isHexSHA(sha) {
			return sha
		}
	}
	return ""
}

func readRefFile(path string, max int64) ([]byte, bool) {
	fi, err := os.Lstat(path)
	if err != nil || !fi.Mode().IsRegular() || fi.Size() > max {
		return nil, false
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, false
	}
	defer f.Close()
	if st, err := f.Stat(); err != nil || !os.SameFile(fi, st) {
		return nil, false
	}
	b, err := io.ReadAll(io.LimitReader(f, max+1))
	if err != nil || int64(len(b)) > max {
		return nil, false
	}
	return b, true
}

func isHexSHA(s string) bool {
	if len(s) != 40 && len(s) != 64 {
		return false
	}
	for _, c := range s {
		if !strings.ContainsRune("0123456789abcdef", c) {
			return false
		}
	}
	return true
}
