package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/zero4573/tix/internal/cvewatch"
	"github.com/zero4573/tix/internal/lock"
	"github.com/zero4573/tix/internal/vault"
	"github.com/zero4573/tix/internal/vaultlock"
)

const (
	cveLockOwner     = "tix-cve"
	defaultCVEEvery  = 12 * time.Hour
	securityDirName  = "security"
	cveTimeLayout    = time.RFC3339
	cveNotDuePrefix  = "not due"
	cveDueWordDue    = "due"
	cveDueWordNotDue = "not-due"
)

func securityDir(v string) string { return filepath.Join(v, securityDirName) }

func withState(v string, every time.Duration, fn func(st *cvewatch.State) error) error {
	if err := vaultlock.Acquire(v, cveLockOwner); err != nil {
		return err
	}
	defer func() { _ = vaultlock.Release(v, cveLockOwner) }()
	dir := securityDir(v)
	st, err := cvewatch.LoadState(dir)
	if err != nil {
		return err
	}
	if err := fn(st); err != nil {
		return err
	}
	return cvewatch.WriteVault(dir, st, every)
}

func cveUpdateCmd(d cveDeps) *cobra.Command {
	var every time.Duration
	var force, asJSON bool
	var days int
	cmd := &cobra.Command{
		Use:   "update [--every 12h] [--force] [--days N] [--json]",
		Short: "Sweep when due and write the vault's security register",
		Long: `Runs the sweep (tix cve sweep) when the last one is at least --every old
(--force: now), merges it into <vault>/security/.state.json and writes
security/security.md and security/advisories/<ID>.md from it. One update runs
at a time (security/.sweep.lock); a second one waits, then finds the sweep
not due. The state keeps every row's status (resolvable, unresolvable,
watch, not-affected, resolved), its first-seen and since dates, its ticket
and assessment, and the last sweep's time.

The merge rules: a row the sweep reports again is updated in place (an
unresolvable one that now has a fix becomes resolvable, dated); one it no
longer reports is resolved only when its source ran (osv for packages, nvd
for runtimes) and none of its files is unscanned; when CISA KEV or EPSS
failed, a row keeps its earlier priority and KEV entry.

Output, one line each:

  swept <time> complete|partial: <open> open, <n> P1, <n> P2; next <time>
  change <P> <ID> <what> <summary>       (new, now resolvable, resolved, ...)
  action <assess|ticket> <P> <ID> <summary>  (a major row without a ticket)
  skip <source> <reason>

or "not due: last <time>, next <time>". --json prints the same as one object.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, err := vault.Require()
			if err != nil {
				return err
			}
			dir := securityDir(ctx.Vault)
			root, err := cvewatch.OpenDir(dir)
			if err != nil {
				return err
			}
			lf, err := root.OpenFile(cvewatch.SweepLock, os.O_CREATE|os.O_RDWR, 0o644)
			root.Close()
			if err != nil {
				return err
			}
			defer lf.Close()
			var res cvewatch.UpdateResult
			err = lock.WithFile(lf, func() error {
				st, err := cvewatch.LoadState(dir)
				if err != nil {
					return err
				}
				now := d.now()
				if !force && !st.Due(now, every) {
					res = cvewatch.UpdateResult{Last: st.LastSweep, Next: st.Next(every).UTC().Format(cveTimeLayout)}
					return nil
				}
				rep, err := runSweep(cmd.Context(), ctx, nil, days, d)
				if err != nil {
					return err
				}
				return withState(ctx.Vault, every, func(st *cvewatch.State) error {
					res = cvewatch.UpdateResult{Delta: st.Merge(rep, d.now(), every), Due: true, Last: st.LastSweep}
					res.Next = st.Next(every).UTC().Format(cveTimeLayout)
					return nil
				})
			})
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if asJSON {
				return json.NewEncoder(out).Encode(res)
			}
			printUpdate(out, res)
			return nil
		},
	}
	cmd.Flags().DurationVar(&every, "every", defaultCVEEvery, "how old the last sweep must be before another runs")
	cmd.Flags().BoolVar(&force, "force", false, "sweep now, however recent the last one is")
	cmd.Flags().IntVar(&days, "days", 30, "how far back NVD and KEV are read for the runtimes (at most 120)")
	cmd.Flags().BoolVar(&asJSON, "json", false, "the result as one JSON object")
	return cmd
}

func printUpdate(out io.Writer, r cvewatch.UpdateResult) {
	if !r.Due {
		fmt.Fprintf(out, "%s: last %s, next %s\n", cveNotDuePrefix, r.Last, r.Next)
		return
	}
	state := "partial"
	if r.Complete {
		state = "complete"
	}
	fmt.Fprintf(out, "swept %s %s: %d open, %d P1, %d P2; next %s\n", r.Swept, state, r.Open, r.P1, r.P2, r.Next)
	for _, c := range r.Changes {
		fmt.Fprintf(out, "change %s %s %s %s\n", c.Priority, c.ID, c.What, c.Summary)
	}
	for _, a := range r.Actions {
		fmt.Fprintf(out, "action %s %s %s %s\n", a.Need, a.Priority, a.ID, a.Summary)
	}
	for _, s := range r.Skips {
		fmt.Fprintf(out, "skip %s %s\n", s.Source, s.Reason)
	}
}

func cveDueCmd(d cveDeps) *cobra.Command {
	var every time.Duration
	cmd := &cobra.Command{
		Use:   "due [--every 12h]",
		Short: "When the last sweep ran and when the next is due",
		Long: `Prints "<due|not-due> last <time> next <time>" from security/.state.json
(never swept: "due last - next -"). Reads only the state; no network.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, err := vault.Require()
			if err != nil {
				return err
			}
			st, err := cvewatch.LoadState(securityDir(ctx.Vault))
			if err != nil {
				return err
			}
			if !cmd.Flags().Changed("every") {
				every = st.Interval(every)
			}
			word, last, next := cveDueWordNotDue, st.LastSweep, "-"
			if st.Due(d.now(), every) {
				word = cveDueWordDue
			}
			if last == "" {
				last = "-"
			} else {
				next = st.Next(every).UTC().Format(cveTimeLayout)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s last %s next %s\n", word, last, next)
			return nil
		},
	}
	cmd.Flags().DurationVar(&every, "every", defaultCVEEvery, "the sweep interval")
	return cmd
}

func cveSetCmd(d cveDeps) *cobra.Command {
	var ticket, assess, epic, project string
	var labels []string
	var every time.Duration
	cmd := &cobra.Command{
		Use:   "set <ID> [--ticket <KEY>] [--assess <text>] | set --epic <KEY> [--project <KEY>] [--label <l>]...",
		Short: "Record a row's ticket or assessment, or where major issues are filed",
		Long: `Records what the main session decided, then rewrites the register:
  --ticket <KEY>    the ticket filed for the row (it leaves "Needs action")
  --assess <text>   a runtime row's assessment: "affected", "unknown", or
                    "not affected: <why>" (that one moves it out of the open rows)
With no ID: --epic, --project and --label set where major issues are filed
(the epic the main session files them under; unset: it files nothing).`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, err := vault.Require()
			if err != nil {
				return err
			}
			if len(args) == 0 && epic == "" && project == "" && len(labels) == 0 {
				return fmt.Errorf("cve set: give an ID with --ticket or --assess, or --epic/--project/--label")
			}
			if len(args) == 1 && ticket == "" && assess == "" {
				return fmt.Errorf("cve set %s: give --ticket or --assess", args[0])
			}
			if len(args) == 1 && (epic != "" || project != "" || len(labels) > 0) {
				return fmt.Errorf("cve set %s: --epic, --project and --label take no ID", args[0])
			}
			return withState(ctx.Vault, every, func(st *cvewatch.State) error {
				if len(args) == 1 {
					return st.Set(args[0], ticket, assess, d.now())
				}
				if epic != "" {
					st.Config.Epic = epic
				}
				if project != "" {
					st.Config.Project = project
				}
				if len(labels) > 0 {
					st.Config.Labels = labels
				}
				return nil
			})
		},
	}
	cmd.Flags().StringVar(&ticket, "ticket", "", "the ticket key filed for the row")
	cmd.Flags().StringVar(&assess, "assess", "", `"affected", "unknown" or "not affected: <why>"`)
	cmd.Flags().StringVar(&epic, "epic", "", "the epic major issues are filed under")
	cmd.Flags().StringVar(&project, "project", "", "the project of that epic (default: the epic key's prefix)")
	cmd.Flags().StringArrayVar(&labels, "label", nil, "a label for the filed issues; repeatable")
	cmd.Flags().DurationVar(&every, "every", defaultCVEEvery, "the sweep interval (for the register's next-sweep)")
	return cmd
}

func cveShowCmd() *cobra.Command {
	var jira bool
	cmd := &cobra.Command{
		Use:   "show <ID> [--jira]",
		Short: "Print a register row, or the issue to file for it",
		Long: `Prints the row's advisory note. --jira prints, as JSON, what to file for
it: {"project","epic","labels","summary","description"} (description in
markdown), from the register's config (tix cve set --epic).`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, err := vault.Require()
			if err != nil {
				return err
			}
			st, err := cvewatch.LoadState(securityDir(ctx.Vault))
			if err != nil {
				return err
			}
			r, ok := st.Rows[args[0]]
			if !ok {
				return fmt.Errorf("cve show: %s isn't in the register", args[0])
			}
			out := cmd.OutOrStdout()
			if !jira {
				fmt.Fprint(out, r.Advisory())
				return nil
			}
			if st.Config.Epic == "" {
				return fmt.Errorf("cve show --jira: no epic set (tix cve set --epic <KEY>)")
			}
			return json.NewEncoder(out).Encode(map[string]any{
				"project": st.Config.ProjectKey(), "epic": st.Config.Epic, "labels": st.Config.Labels,
				"summary": r.JiraSummary(), "description": r.JiraDescription(st.LastSweep),
			})
		},
	}
	cmd.Flags().BoolVar(&jira, "jira", false, "the issue to file, as JSON")
	return cmd
}

func cveAckCmd() *cobra.Command {
	return &cobra.Command{
		Use:    "ack <item>...",
		Short:  "Record that main was told these items of an update (its host service runs it)",
		Hidden: true,
		Args:   cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, err := vault.Require()
			if err != nil {
				return err
			}
			return withState(ctx.Vault, defaultCVEEvery, func(st *cvewatch.State) error {
				st.Ack(args)
				return nil
			})
		},
	}
}
