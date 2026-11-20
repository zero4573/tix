package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/zero4573/tix/internal/cvewatch"
	"github.com/zero4573/tix/internal/repo"
	"github.com/zero4573/tix/internal/vault"
)

type cveDeps struct {
	now     func() time.Time
	sources func() cvewatch.Sources
}

func hostSources() cvewatch.Sources {
	src := cvewatch.DefaultSources()
	src.NVDKey = os.Getenv("NVD_API_KEY")
	if src.NVDKey != "" {
		src.NVDPause = 700 * time.Millisecond
	}
	return src
}

func cveCmd() *cobra.Command { return cveCmdWith(cveDeps{now: time.Now, sources: hostSources}) }

func cveCmdWith(d cveDeps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "cve",
		Short: "Known vulnerabilities in the projects' dependencies and runtimes",
	}
	cmd.AddCommand(cveSweepCmd(d), cveUpdateCmd(d), cveDueCmd(d), cveSetCmd(d), cveShowCmd(), cveAckCmd())
	return cmd
}

func cveSweepCmd(d cveDeps) *cobra.Command {
	var days int
	var asJSON bool
	var only []string
	cmd := &cobra.Command{
		Use:   "sweep [--json] [--days N] [--repo <slug|clone>]...",
		Short: "Check every main clone against OSV, CISA KEV, EPSS and NVD",
		Long: `Reads the dependency lockfiles and manifests of every main clone under the
vault's projectsRoot (--repo: only those), and checks them against the
public vulnerability databases:

  OSV.dev      the packages (Go modules and the Go toolchain, npm, Maven,
               PyPI, crates.io, RubyGems, Pub, Packagist): which advisories
               affect the pinned version, and the version that fixes each
  CISA KEV     the known exploited vulnerabilities catalog
  FIRST EPSS   the probability each CVE is exploited in the next 30 days
  NVD          critical and high CVEs published in the last --days (default
               30, at most 120) for the runtimes the repos' images run
               (nginx, Java, Tomcat, Node.js, PostgreSQL, ...), and KEV
               entries added for them in that window; these are "watch"
               items, since the image tag alone doesn't say whether the
               version is affected

Advisories that are the same issue under several IDs (CVE, GHSA, GO-...)
are merged under the CVE. Each gets a priority:

  P1  in CISA KEV, or remote code execution over the network with a
      critical or high severity: "major"
  P2  critical, or high with an EPSS of 0.1 or more
  P3  high or medium
  P4  the rest

A finding is resolvable when every affected package has a fixed version.
Manifests without pinned versions are listed as unscanned. A source that
can't be reached is a skip line, never a silent pass. NVD_API_KEY, when
set, raises NVD's rate limit.

Text output, highest priority first:

  <P> <id> <severity> [KEV] [RCE] epss=<n> <fix|no fix> <repo> <file> <package>@<version>[ -> <fix>] ...
  watch <P> <id> <severity> [KEV] [RCE] <product> <repo>:<image> ...
  unscanned <repo> <file> <reason>
  skip <source> <reason>

Exits 0 when there's no P1 finding or watch item, 3 when there is one, and
1 on errors. It writes nothing; tix cve update keeps the vault's register.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, err := vault.Require()
			if err != nil {
				return err
			}
			rep, err := runSweep(cmd.Context(), ctx, only, days, d)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if asJSON {
				enc := json.NewEncoder(out)
				enc.SetIndent("", "  ")
				if err := enc.Encode(rep); err != nil {
					return err
				}
			} else {
				printSweep(out, rep)
			}
			for _, f := range rep.Findings {
				if f.Major {
					return exitError(3)
				}
			}
			for _, w := range rep.Watch {
				if w.Major {
					return exitError(3)
				}
			}
			return nil
		},
	}
	cmd.Flags().IntVar(&days, "days", 30, "how far back NVD and KEV are read for the runtimes (at most 120)")
	cmd.Flags().BoolVar(&asJSON, "json", false, "the full report as JSON")
	cmd.Flags().StringArrayVar(&only, "repo", nil, "only this main clone (its slug or provider/owner/repo); repeatable")
	return cmd
}

func runSweep(c context.Context, ctx vault.Context, only []string, days int, d cveDeps) (cvewatch.Report, error) {
	clones := repo.MainClones(ctx.ProjectsRoot)
	if len(only) > 0 {
		var picked []string
		for _, o := range only {
			cl, ok := repo.Resolve(ctx.ProjectsRoot, o)
			if !ok {
				return cvewatch.Report{}, fmt.Errorf("cve: %s isn't a main clone under %s", o, ctx.ProjectsRoot)
			}
			picked = append(picked, cl)
		}
		clones = picked
	}
	if len(clones) == 0 {
		return cvewatch.Report{}, fmt.Errorf("cve: no main clones under %s", ctx.ProjectsRoot)
	}
	inv := cvewatch.Collect(ctx.ProjectsRoot, clones)
	return cvewatch.Sweep(c, inv, cvewatch.Options{Days: days, Sources: d.sources(), Now: d.now}), nil
}

func printSweep(out io.Writer, rep cvewatch.Report) {
	fmt.Fprintf(out, "swept %s: %d repos, %d packages, %d findings, %d watch\n",
		rep.Swept, len(rep.Repos), rep.Packages, len(rep.Findings), len(rep.Watch))
	for _, f := range rep.Findings {
		fix := "no fix"
		if f.Resolvable {
			fix = "fix"
		}
		var pk []string
		for _, p := range f.Packages {
			s := fmt.Sprintf("%s %s %s@%s", p.Repo, p.File, p.Name, p.Version)
			if p.Fix != "" {
				s += " -> " + p.Fix
			}
			pk = append(pk, s)
		}
		fmt.Fprintf(out, "%s %s %s%s epss=%.3f %s %s\n", f.Priority, f.ID, f.Severity, flags(f.KEV != nil, f.RCE), f.EPSS, fix, strings.Join(pk, "; "))
	}
	for _, w := range rep.Watch {
		var where []string
		for _, c := range w.Where {
			img := c.Image
			if img == "" {
				img = c.Product + " " + c.Version
			}
			where = append(where, c.Repo+":"+img)
		}
		fmt.Fprintf(out, "watch %s %s %s%s %s %s\n", w.Priority, w.ID, w.Severity, flags(w.KEV != nil, w.RCE), w.Product, strings.Join(where, " "))
	}
	for _, u := range rep.Unscanned {
		fmt.Fprintf(out, "unscanned %s %s %s\n", u.Repo, u.File, u.Reason)
	}
	for _, s := range rep.Skips {
		fmt.Fprintf(out, "skip %s %s\n", s.Source, s.Reason)
	}
}

func flags(kev, rce bool) string {
	s := ""
	if kev {
		s += " KEV"
	}
	if rce {
		s += " RCE"
	}
	return s
}
