package cli

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/zero4573/tix/internal/gitx"
	"github.com/zero4573/tix/internal/vault"
	"github.com/zero4573/tix/internal/workspace"
)

func wsResignCmd() *cobra.Command {
	var dry bool
	cmd := &cobra.Command{
		Use:   "resign <ID> [--dry-run] [<slug>...]",
		Short: "Sign a ticket's unpushed commits (before each push, and after the tidy)",
		Long: `Ticket work is committed unsigned; the lead signs it before each push
(after a merged wave) and after the tidy, and the user may ask. For each named repo
of the workspace (default: all) this re-creates the unpushed commits of the
ticket branch (those on HEAD and on no remote-tracking branch) that have no
signature, and their descendants, with git commit-tree -S: the same tree,
author, committer, dates and message, byte for byte, merges included. Then it
moves the branch to the new tip (only from the tip it read) and checks that
the tree is unchanged. Commits that are already signed and whose parents
stay as they are keep their ids.

It signs with the key git is set up with (user.signingkey and gpg.format of
the user's config). A repo with uncommitted changes, a detached HEAD or a
checkout that is not on the workspace's branch is skipped with a warning, and
so is one where an open task branch (checked out in a worktree, not merged)
forks from an unpushed commit: re-creating that commit would bring its
unsigned copy back with the task's merge.

--dry-run lists the unsigned commits instead, one line each:

  <slug>  <short sha>  <subject>

Exits 0 when every repo is signed (or, with --dry-run, has nothing
unsigned), 3 when --dry-run lists a commit, 1 when a repo was skipped or
signing failed (nothing of that repo is moved then).`,
		Args:              cobra.MinimumNArgs(1),
		ValidArgsFunction: completeWorkspaceID,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, err := vault.Require()
			if err != nil {
				return err
			}
			return wsResign(ctx, args[0], args[1:], dry)
		},
	}
	cmd.Flags().BoolVar(&dry, "dry-run", false, "list the unsigned unpushed commits, sign nothing")
	return cmd
}

type rawCommit struct {
	sha, tree         string
	parents           []string
	author, committer string
	message           []byte
	signed            bool
}

func readRawCommit(dir, sha string) (rawCommit, error) {
	cmd := gitx.CommandIn(dir, "cat-file", "commit", sha)
	raw, err := cmd.Output()
	if err != nil {
		return rawCommit{}, fmt.Errorf("can't read commit %s: %w", shortSHA(sha), err)
	}
	c := rawCommit{sha: sha}
	header, msg, _ := bytes.Cut(raw, []byte("\n\n"))
	c.message = msg
	for _, l := range strings.Split(string(header), "\n") {
		k, v, _ := strings.Cut(l, " ")
		switch k {
		case "tree":
			c.tree = v
		case "parent":
			c.parents = append(c.parents, v)
		case "author":
			c.author = v
		case "committer":
			c.committer = v
		case "gpgsig", "gpgsig-sha256":
			c.signed = true
		case "encoding", "mergetag":
			return rawCommit{}, fmt.Errorf("commit %s has a %s header, which resign doesn't carry over", shortSHA(sha), k)
		}
	}
	if c.tree == "" || c.author == "" || c.committer == "" {
		return rawCommit{}, fmt.Errorf("commit %s: unexpected object", shortSHA(sha))
	}
	return c, nil
}

func identEnv(prefix, ident string) ([]string, error) {
	lt, gt := strings.LastIndex(ident, " <"), strings.LastIndex(ident, "> ")
	if lt < 0 || gt < lt {
		return nil, fmt.Errorf("unexpected identity %q", ident)
	}
	return []string{
		prefix + "_NAME=" + ident[:lt],
		prefix + "_EMAIL=" + ident[lt+2:gt],
		prefix + "_DATE=" + ident[gt+2:],
	}, nil
}

func unpushedCommits(dir string) ([]string, error) {
	out, err := gitx.OutIn(dir, nil, "rev-list", "--reverse", "--topo-order", "HEAD", "--not", "--remotes")
	if err != nil {
		return nil, err
	}
	return strings.Fields(out), nil
}

func openTasksForkedFrom(dir, key string, unpushed []string) []string {
	wts, err := listWorktrees(dir)
	if err != nil {
		return nil
	}
	isUnpushed := map[string]bool{}
	for _, sha := range unpushed {
		isUnpushed[sha] = true
	}
	var open []string
	for _, wt := range wts {
		if !strings.HasPrefix(wt.branch, "task/"+key+"-") {
			continue
		}
		ref := "refs/heads/" + wt.branch
		if gitx.Ok(dir, "merge-base", "--is-ancestor", ref, "HEAD") {
			continue
		}
		if fork, err := gitx.Out(dir, "merge-base", ref, "HEAD"); err == nil && isUnpushed[fork] {
			open = append(open, wt.branch)
		}
	}
	return open
}

func wsResign(ctx vault.Context, key string, slugs []string, dry bool) error {
	if err := requireKey("resign", key); err != nil {
		return err
	}
	dir := filepath.Join(ctx.WorkRoot, key)
	if _, err := os.Stat(workspace.File(dir)); err != nil {
		return fmt.Errorf("resign: %s has no workspace (%s)", key, dir)
	}
	repos := wsRepos(ctx, key)
	if len(slugs) > 0 {
		var pick []workspace.Repo
		for _, s := range slugs {
			found := false
			for _, r := range repos {
				if r.Slug == s {
					pick, found = append(pick, r), true
				}
			}
			if !found {
				return fmt.Errorf("resign: %s has no repo '%s' in its workspace", key, s)
			}
		}
		repos = pick
	}
	failed, listed := false, 0
	for _, r := range repos {
		n, err := resignRepo(key, r, dry)
		if err != nil {
			wsWarn("%s: %v", r.Slug, err)
			failed = true
			continue
		}
		listed += n
	}
	switch {
	case failed:
		return exitError(1)
	case dry && listed > 0:
		return exitError(3)
	}
	return nil
}

func resignRepo(key string, r workspace.Repo, dry bool) (int, error) {
	if _, err := os.Stat(filepath.Join(r.Path, ".git")); err != nil {
		return 0, fmt.Errorf("worktree missing (%s)", r.Path)
	}
	shas, err := unpushedCommits(r.Path)
	if err != nil {
		return 0, err
	}
	commits := make([]rawCommit, 0, len(shas))
	unsigned := 0
	for _, sha := range shas {
		c, err := readRawCommit(r.Path, sha)
		if err != nil {
			return 0, err
		}
		if !c.signed {
			unsigned++
			if dry {
				subj, _, _ := bytes.Cut(c.message, []byte("\n"))
				fmt.Printf("%s\t%s\t%s\n", r.Slug, shortSHA(sha), subj)
			}
		}
		commits = append(commits, c)
	}
	if dry {
		return unsigned, nil
	}
	if unsigned == 0 {
		fmt.Printf("tix ws: %s: nothing to sign (%d unpushed commit(s))\n", r.Slug, len(commits))
		return 0, nil
	}
	branch, err := gitx.Out(r.Path, "symbolic-ref", "-q", "--short", "HEAD")
	if err != nil || branch == "" {
		return 0, fmt.Errorf("detached HEAD, not signed")
	}
	if branch != r.Branch {
		return 0, fmt.Errorf("on %s, not the ticket branch %s: not signed", branch, r.Branch)
	}
	if gitx.Dirty(r.Path) {
		return 0, fmt.Errorf("uncommitted changes, not signed")
	}
	if open := openTasksForkedFrom(r.Path, key, shas); len(open) > 0 {
		return 0, fmt.Errorf("not signed: open task branch(es) %s fork from unpushed commits that signing would re-create (merging them would bring the unsigned copies back); sign once they are merged", strings.Join(open, ", "))
	}
	old := shas[len(shas)-1]
	renamed := map[string]string{}
	signedNow := 0
	for _, c := range commits {
		moved := false
		parents := make([]string, len(c.parents))
		for i, p := range c.parents {
			parents[i] = p
			if n, ok := renamed[p]; ok {
				parents[i], moved = n, true
			}
		}
		if c.signed && !moved {
			continue
		}
		args := []string{"commit-tree", "-S", c.tree}
		for _, p := range parents {
			args = append(args, "-p", p)
		}
		cmd := gitx.CommandIn(r.Path, args...)
		a, err1 := identEnv("GIT_AUTHOR", c.author)
		cm, err2 := identEnv("GIT_COMMITTER", c.committer)
		if err1 != nil || err2 != nil {
			return 0, fmt.Errorf("commit %s: unexpected author or committer", shortSHA(c.sha))
		}
		cmd.Env = append(append(cmd.Env, a...), cm...)
		cmd.Stdin = bytes.NewReader(c.message)
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		out, err := cmd.Output()
		if err != nil {
			return 0, fmt.Errorf("signing %s failed, branch not moved: %s", shortSHA(c.sha), firstLine(stderr.String(), err))
		}
		n := strings.TrimSpace(string(out))
		nc, err := readRawCommit(r.Path, n)
		if err != nil {
			return 0, err
		}
		if !nc.signed || nc.tree != c.tree || nc.author != c.author || nc.committer != c.committer || !bytes.Equal(nc.message, c.message) {
			return 0, fmt.Errorf("the signed copy of %s differs from it, branch not moved", shortSHA(c.sha))
		}
		renamed[c.sha] = n
		signedNow++
	}
	tip := renamed[old]
	if tip == "" {
		return 0, fmt.Errorf("the branch tip wasn't rewritten, branch not moved")
	}
	if err := gitx.Run(r.Path, "update-ref", "-m", "tix ws resign", "refs/heads/"+branch, tip, old); err != nil {
		return 0, fmt.Errorf("the branch moved while its commits were signed, nothing changed")
	}
	if gitx.Dirty(r.Path) {
		return 0, fmt.Errorf("moved %s to %s but the checkout now shows changes: go back with git update-ref refs/heads/%s %s %s", branch, shortSHA(tip), branch, old, tip)
	}
	fmt.Printf("tix ws: %s: signed %d commit(s), %s %s → %s\n", r.Slug, signedNow, branch, shortSHA(old), shortSHA(tip))
	return signedNow, nil
}

func firstLine(s string, err error) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if s == "" {
		return err.Error()
	}
	return s
}
