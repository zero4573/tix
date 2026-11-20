package cli

import (
	"fmt"
	"os/exec"

	"github.com/spf13/cobra"

	"github.com/zero4573/tix/internal/gitx"
	"github.com/zero4573/tix/internal/seccheck"
	"github.com/zero4573/tix/internal/vault"
)

func wsSecCheckCmd() *cobra.Command {
	var dir string
	var all bool
	cmd := &cobra.Command{
		Use:   "sec-check [-C <dir>] [--all] <since>",
		Short: "Run the security scanners on a checkout and flag what the commits since <since> introduced",
		Long: `Runs the security scanners that apply to the checkout at -C (default .) and
tells the findings the commits <since>..HEAD introduced (new) from the ones
already there (legacy). Run it in the worktree whose HEAD is the range's
end: a task worktree with the ticket branch tip as <since> before a merge,
the ticket worktree with its base for the whole ticket.

  gitleaks     secrets in the range's commits (--all: the whole history);
               every one blocks, the output is redacted
  gosec        each Go module whose .go files the range touched (--all: every
               one); new = a line of the finding was added by the range; HIGH
               severity with MEDIUM or HIGH confidence, and SQL injection
               (G201, G202) block, the rest warn
  govulncheck  each Go module whose .go files, go.mod or go.sum the range
               touched (--all: every one); vulnerabilities in code the module
               calls; new = the range changed the dependency's go.mod line, or
               added a call on the way to it (the standard library's: only
               a changed go or toolchain line, as the fix is a toolchain
               bump); every one blocks
  npm audit    each package-lock.json the range touched (--all: every one),
               production dependencies; new = a package the range added or
               whose locked version it changed; high and critical block
  osv maven    when the range touched a pom.xml, build.gradle(.kts) or
               gradle.lockfile (--all: when there is one), the versions they
               pin, against OSV.dev (network); new = a version the range
               added to a manifest; high and critical block

  suppression  every line the range adds with #nosec, gitleaks:allow,
               nolint:gosec or @SuppressFBWarnings, and any change to .gitleaks.toml or
               .gitleaksignore: a new warn line, for the review to judge

Each scanner runs at most 10 minutes. One line per finding, new ones first
(legacy ones only with --all):

  <new|legacy>  <block|warn>  <tool>  <severity>  <where>  <rule>  <message>

and one per scanner that didn't run (not installed, timed out, or it failed,
e.g. no network for the vulnerability database or the registry, or packages
that don't load):

  skip  <tool>  <reason>

Exits 0 when no new finding blocks (warnings, legacy findings and skips don't
count), 3 when one does, 1 on a usage error, a directory that is not a git
checkout, or a bad <since>.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			_, _ = vault.Optional()
			if dir == "" {
				dir = "."
			}
			if _, err := exec.LookPath("git"); err != nil {
				return fmt.Errorf("sec-check: git not found")
			}
			if _, err := gitx.Out(dir, "rev-parse", "-q", "--verify", args[0]+"^{commit}"); err != nil {
				return fmt.Errorf("sec-check: %s is not a commit in %s", args[0], dir)
			}
			rep, err := seccheck.Run(cmd.Context(), dir, seccheck.Options{RangeStart: args[0], IncludeLegacy: all})
			if err != nil {
				return fmt.Errorf("sec-check: %w", err)
			}
			out := cmd.OutOrStdout()
			for _, f := range rep.Findings {
				fmt.Fprintln(out, f.Line())
			}
			for _, s := range rep.Skips {
				fmt.Fprintf(out, "skip  %s  %s\n", s.Tool, s.Reason)
			}
			if rep.Blocking() {
				return exitError(3)
			}
			return nil
		},
	}
	cmd.Flags().StringVarP(&dir, "dir", "C", "", "the checkout to scan (default .)")
	cmd.Flags().BoolVar(&all, "all", false, "also list the legacy findings (and scan every module and the whole history)")
	return cmd
}
