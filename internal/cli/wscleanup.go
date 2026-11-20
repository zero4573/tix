package cli

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/zero4573/tix/internal/fsx"
	"github.com/zero4573/tix/internal/gitx"
	"github.com/zero4573/tix/internal/vault"
	"github.com/zero4573/tix/internal/workspace"
)

func wsCleanupCmd() *cobra.Command {
	var dry bool
	cmd := &cobra.Command{
		Use:   "cleanup <ID> [--dry-run]",
		Short: "Remove what tix made for a ticket and no longer needs",
		Long: `Removes the leftovers of a ticket's work that are spent, checking each one
first and listing what it removed and what it kept (and why):

  - the tidy skill's scratch clones whose result is on the ticket branch
    (tix ws tidy-clean without --force);
  - refs/tidy-backup/<branch> of a repo whose ticket branch has nothing
    unpushed and a remote-tracking branch at its tip, or is merged into its
    base: the history the backup kept is on the remote;
  - merged, clean task worktrees and their task/<ID>-* branches (tix ws
    task-clean without --force). In the sandbox the mounts keep worktrees
    that existed at session start busy, so there this part only names the
    host command; tix start runs it before the next session anyway.

It never forces: an unmerged clone, an unpushed backup or a dirty worktree
stays, and the ticket worktrees themselves are tix ws rm's (yours, once the
PR is merged). Safe to run at any time; /tix:save offers it after a push.`,
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: completeWorkspaceID,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, err := vault.Require()
			if err != nil {
				return err
			}
			return wsCleanup(ctx, args[0], dry)
		},
	}
	cmd.Flags().BoolVar(&dry, "dry-run", false, "show what would be removed")
	return cmd
}

func wsCleanup(ctx vault.Context, key string, dry bool) error {
	if err := requireKey("cleanup", key); err != nil {
		return err
	}
	dir := filepath.Join(ctx.WorkRoot, key)
	if !fsx.IsDir(dir) {
		return fmt.Errorf("cleanup: no workspace at %s", dir)
	}
	info, err := workspace.Read(dir)
	if err != nil {
		return fmt.Errorf("cleanup: %w", err)
	}
	_ = wsTidyClean(ctx, key, false, dry)
	for _, r := range info.Repos {
		cleanupBackupRefs(r, dry)
	}
	if gitx.InContainer() {
		fmt.Printf("tix ws: task worktrees: run tix ws task-clean %s on the host (tix start does it before the next session)\n", key)
		return nil
	}
	_ = wsTaskClean(ctx, key, false, dry, false)
	return nil
}

func cleanupBackupRefs(r workspace.Repo, dry bool) {
	out, err := gitx.OutIn(r.Path, nil, "for-each-ref", "--format=%(refname) %(objectname)", "refs/tidy-backup/")
	if err != nil || strings.TrimSpace(out) == "" {
		return
	}
	tip, err := gitx.Out(r.Path, "rev-parse", "-q", "--verify", "refs/heads/"+r.Branch)
	pushed := false
	if err == nil && tip != "" {
		remote, rerr := gitx.Out(r.Path, "rev-parse", "-q", "--verify", "refs/remotes/origin/"+r.Branch)
		pushed = rerr == nil && remote == tip
		if !pushed {
			base := gitx.BaseRef(r.Path, r.Base)
			pushed = strings.HasPrefix(base, "origin/") && gitx.Ok(r.Path, "merge-base", "--is-ancestor", tip, base)
		}
	}
	for _, l := range strings.Split(strings.TrimSpace(out), "\n") {
		ref, sha, ok := strings.Cut(l, " ")
		if !ok || !strings.HasPrefix(ref, "refs/tidy-backup/") {
			continue
		}
		switch {
		case !pushed:
			wsWarn("%s: %s kept: %s isn't pushed (or merged into %s) yet", r.Slug, ref, r.Branch, r.Base)
		case dry:
			fmt.Printf("tix ws: would delete %s of %s (%s)\n", ref, r.Slug, shortSHA(sha))
		case gitx.Run(r.Path, "update-ref", "-d", ref, sha) != nil:
			wsWarn("%s: couldn't delete %s", r.Slug, ref)
		default:
			fmt.Printf("tix ws: deleted %s of %s (%s)\n", ref, r.Slug, shortSHA(sha))
		}
	}
}
