package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/zero4573/tix/internal/config"
	"github.com/zero4573/tix/internal/doctor"
	"github.com/zero4573/tix/internal/note"
	"github.com/zero4573/tix/internal/vault"
)

func doctorCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Material for /tix:doctor, which grades finished ticket sessions",
	}
	cmd.AddCommand(doctorCollectCmd())
	return cmd
}

func doctorCollectCmd() *cobra.Command {
	var out, projects string
	var max int
	var all bool
	cmd := &cobra.Command{
		Use:   "collect [--out <dir>] [--projects-dir <dir>] [--max <n>] [--all] [<ID>...]",
		Short: "Condense the Claude Code transcripts of ticket sessions for grading",
		Long: `Finds the Claude Code transcripts of the current vault's ticket
workspaces (<workRoot>/<ID>, a cleaned one too) in Claude Code's projects
folder (--projects-dir; default $CLAUDE_CONFIG_DIR/projects, else
~/.claude/projects): every session whose cwd is the workspace or under it,
and each session's subagents. Kiro sessions leave no such transcripts and
are not found.

Without IDs it takes the --max (default 6) tickets with the most recent
sessions, leaving out those a doctor note already scored (the tickets:
list of tix-improvements/*-doctor.md in the vault) unless --all.

Writes one folder per ticket under --out (default
<workRoot>/.doctor/<yyyyMMdd-HHmm>): index.md (the ticket folder's
evidence files, one row per transcript: agent, models, times, prompts,
tool calls and errors, interrupts, compactions) and transcripts/NNN-<agent>-<id>.md,
one line per prompt, message, tool call and result, clipped, in time
order. Nothing leaves the machine.

stdout, one line each:

  out  <dir>
  ticket  <ID>  <transcripts>  <tool errors>  <KB>  <index.md>
  scored  <ID>  <doctor note>
  none  <ID>

Exit 0, 2 when no ticket had transcripts.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, err := vault.Require()
			if err != nil {
				return err
			}
			if projects == "" {
				projects = doctor.ProjectsDir(os.Getenv, config.Home())
			}
			found, err := doctor.Tickets(projects, ctx.WorkRoot)
			if err != nil {
				return fmt.Errorf("doctor: %w", err)
			}
			byID := map[string]doctor.Ticket{}
			for _, t := range found {
				byID[t.ID] = t
			}
			found = slices.DeleteFunc(found, func(t doctor.Ticket) bool { return !note.ValidKey(t.ID) })
			w := cmd.OutOrStdout()
			var pick []doctor.Ticket
			if len(args) > 0 {
				for _, id := range args {
					if t, ok := byID[id]; ok {
						pick = append(pick, t)
					} else {
						fmt.Fprintf(w, "none\t%s\n", id)
					}
				}
			} else {
				scored := doctorScored(ctx.Vault)
				for _, t := range found {
					if n, ok := scored[t.ID]; ok && !all {
						fmt.Fprintf(w, "scored\t%s\t%s\n", t.ID, n)
						continue
					}
					if len(pick) < max {
						pick = append(pick, t)
					}
				}
			}
			if len(pick) == 0 {
				return exitError(2)
			}
			if out == "" {
				out = filepath.Join(ctx.WorkRoot, ".doctor", time.Now().Format("20060102-1504"))
			}
			fmt.Fprintf(w, "out\t%s\n", out)
			for _, t := range pick {
				s, err := doctor.Bundle(out, t.ID, doctorTicketDir(ctx.Vault, t.ID), t.Sessions, doctor.DefaultLimits)
				if err != nil {
					return fmt.Errorf("doctor: %s: %w", t.ID, err)
				}
				fmt.Fprintf(w, "ticket\t%s\t%d\t%d\t%d\t%s\n", t.ID, s.Transcripts, s.ToolErrors, (s.Bytes+1023)/1024, filepath.Join(s.Dir, "index.md"))
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&out, "out", "", "folder for the bundles (default <workRoot>/.doctor/<yyyyMMdd-HHmm>)")
	cmd.Flags().StringVar(&projects, "projects-dir", "", "Claude Code's projects folder")
	cmd.Flags().IntVar(&max, "max", 6, "tickets to take when none are named")
	cmd.Flags().BoolVar(&all, "all", false, "also take tickets a doctor note already scored")
	return cmd
}

func doctorTicketDir(v, id string) string {
	if p := note.Find(v, id); p != "" {
		return filepath.Dir(p)
	}
	if d := note.ArchiveDir(v, id); d != "" {
		if st, err := os.Stat(d); err == nil && st.IsDir() {
			return d
		}
	}
	return ""
}

func doctorScored(v string) map[string]string {
	files, _ := filepath.Glob(filepath.Join(v, "tix-improvements", "*-doctor.md"))
	sort.Strings(files)
	out := map[string]string{}
	for _, f := range files {
		for _, id := range note.ListField(f, "tickets") {
			out[strings.Trim(id, "[]\"' ")] = filepath.Base(f)
		}
	}
	return out
}
