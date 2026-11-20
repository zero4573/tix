package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/zero4573/tix/internal/note"
	"github.com/zero4573/tix/internal/vault"
)

func boardCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "board",
		Short: "Read and update a ticket's board.md task lines without lost updates",
	}
	cmd.AddCommand(boardSetCmd(), boardWaveCmd())
	return cmd
}

var (
	boardTaskRe = regexp.MustCompile(`^- \[(.)\] ([0-9A-Za-z]+) · ([^·]*) · (.*)$`)
	boardDoneRe = regexp.MustCompile(` (✅|❌) [0-9]{4}-[0-9]{2}-[0-9]{2}$`)
	boardNNRe   = regexp.MustCompile(`^[0-9]{1,4}[a-z]?$`)
	boardShaRe  = regexp.MustCompile(`^merged ([0-9a-f]{7,40}|-)$`)
	boardTaskW  = regexp.MustCompile(` · w([0-9]+) · `)
)

func boardBox(status string) (box, mark string, err error) {
	switch {
	case status == "planned":
		return " ", "", nil
	case status == "running", status == "orphaned", status == "partial", status == "blocked", status == "pending — user":
		return "/", "", nil
	case boardShaRe.MatchString(status):
		return "x", "✅", nil
	case status == "dropped":
		return "-", "❌", nil
	}
	return "", "", fmt.Errorf("board: unknown status %q (planned, running, orphaned, partial, blocked, 'pending — user', 'merged <sha>' or 'merged -', dropped)", status)
}

func boardSetCmd() *cobra.Command {
	var noteText, from string
	cmd := &cobra.Command{
		Use:   "set <ID> <NN> <status> [--note <text>] [--from <status>]",
		Short: "Set one task line's status under the board's lock",
		Long: `Sets the status of task NN on the ticket's board.md: the checkbox, the
status word and the date mark follow from it ([ ] planned; [/] running,
orphaned, partial, blocked, 'pending — user'; [x] 'merged <sha>' with ✅ and
today's date; [-] dropped with ❌ and today's date). The rest of the line
stays, or its last field (the note) is replaced with --note. The file is
read and written under board.md.lock, so the PO and the lead never
overwrite each other's line; a hand edit can.

--from <status> makes the change conditional: it is refused (exit 3, the
line printed) when the line's status is another one by now, e.g. the PO's
'orphaned' mark only on a line still 'running'.

Prints the new line. Exit 2 when the board has no task NN.`,
		Args: cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, err := vault.Require()
			if err != nil {
				return err
			}
			path, err := boardPath(ctx, args[0])
			if err != nil {
				return err
			}
			line, err := boardSet(path, args[1], args[2], noteText, cmd.Flags().Changed("note"), from, time.Now(), 10*time.Second)
			if line != "" {
				fmt.Fprintln(cmd.OutOrStdout(), line)
			}
			return err
		},
	}
	cmd.SilenceUsage = true
	cmd.Flags().StringVar(&noteText, "note", "", "replace the line's note (its last field, one line without ' · ')")
	cmd.Flags().StringVar(&from, "from", "", "only when the line's status is this one now")
	return cmd
}

func boardPath(ctx vault.Context, key string) (string, error) {
	if !note.ValidKey(key) {
		return "", fmt.Errorf("board: not a ticket ID: '%s'", key)
	}
	p := filepath.Join(note.TicketDir(ctx.Vault, key), "board.md")
	if _, err := os.Stat(p); err != nil {
		return "", fmt.Errorf("board: %s has no board.md", key)
	}
	return p, nil
}

func boardSet(path, nn, status, noteText string, setNote bool, from string, now time.Time, wait time.Duration) (string, error) {
	if !boardNNRe.MatchString(nn) {
		return "", fmt.Errorf("board: not a task number: '%s'", nn)
	}
	box, mark, err := boardBox(status)
	if err != nil {
		return "", err
	}
	if setNote && (strings.ContainsAny(noteText, "\r\n\t") || strings.Contains(noteText, " · ") || strings.TrimSpace(noteText) == "") {
		return "", fmt.Errorf("board: the note is one non-empty line without ' · '")
	}
	var result string
	var refused error
	err = relayWriteLocked(path, wait, func(s string) (string, error) {
		lines := strings.Split(s, "\n")
		at := -1
		for i, l := range lines {
			if m := boardTaskRe.FindStringSubmatch(strings.TrimRight(l, "\r")); m != nil && m[2] == nn {
				if at >= 0 {
					return "", fmt.Errorf("board: task %s has more than one line", nn)
				}
				at = i
			}
		}
		if at < 0 {
			return "", exitWith(2, fmt.Errorf("board: no task %s on the board", nn))
		}
		cr := strings.HasSuffix(lines[at], "\r")
		m := boardTaskRe.FindStringSubmatch(strings.TrimRight(lines[at], "\r"))
		if cur := strings.TrimSpace(m[3]); from != "" && cur != from {
			result = strings.TrimRight(lines[at], "\r")
			refused = fmt.Errorf("board: task %s is '%s', not '%s': line left as it is", nn, cur, from)
			return s, nil
		}
		rest := boardDoneRe.ReplaceAllString(m[4], "")
		if setNote {
			created := ""
			if i := strings.LastIndex(rest, " ➕ "); i >= 0 {
				rest, created = rest[:i], rest[i:]
			}
			if i := strings.LastIndex(rest, " · "); i >= 0 {
				rest = rest[:i+len(" · ")] + noteText
			} else {
				rest = noteText
			}
			rest += created
		}
		line := "- [" + box + "] " + nn + " · " + status + " · " + rest
		if mark != "" {
			line += " " + mark + " " + now.Format("2006-01-02")
		}
		result = line
		if cr {
			line += "\r"
		}
		lines[at] = line
		return strings.Join(lines, "\n"), nil
	})
	if err != nil {
		return "", err
	}
	if refused != nil {
		fmt.Fprintln(os.Stderr, "tix: "+refused.Error())
		return result, exitError(3)
	}
	return result, nil
}

func boardWaveCmd() *cobra.Command {
	var wave int
	cmd := &cobra.Command{
		Use:   "wave <ID> [--wave N]",
		Short: "Print a wave's task lines as the board has them",
		Long: `Prints the board's current wave (frontmatter wave:, or --wave N) and its
tasks with their status, read from board.md, one line:

  wave <N>: <NN> <status>, <NN> <status>, …

then the task numbers that are finished (merged or dropped) and those that
are not. The PO builds the wave line it sends the lead from this, never
from memory. Exit 3 when the wave has no task line.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, err := vault.Require()
			if err != nil {
				return err
			}
			path, err := boardPath(ctx, args[0])
			if err != nil {
				return err
			}
			b, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			w := wave
			if !cmd.Flags().Changed("wave") {
				if _, err := fmt.Sscanf(note.Frontmatter(path)["wave"], "%d", &w); err != nil {
					return fmt.Errorf("board: %s has no wave: number in its frontmatter (use --wave N)", args[0])
				}
			}
			var all, done, open []string
			for _, l := range strings.Split(string(b), "\n") {
				m := boardTaskRe.FindStringSubmatch(strings.TrimRight(l, "\r"))
				if m == nil {
					continue
				}
				wm := boardTaskW.FindStringSubmatch(l)
				if wm == nil || wm[1] != fmt.Sprint(w) {
					continue
				}
				st := strings.TrimSpace(m[3])
				all = append(all, m[2]+" "+st)
				if strings.HasPrefix(st, "merged") || st == "dropped" {
					done = append(done, m[2])
				} else {
					open = append(open, m[2])
				}
			}
			out := cmd.OutOrStdout()
			if len(all) == 0 {
				fmt.Fprintf(out, "wave %d: no tasks\n", w)
				return exitError(3)
			}
			fmt.Fprintf(out, "wave %d: %s\n", w, strings.Join(all, ", "))
			fmt.Fprintf(out, "finished: %s\n", orDash(done))
			fmt.Fprintf(out, "open: %s\n", orDash(open))
			return nil
		},
	}
	cmd.SilenceUsage = true
	cmd.Flags().IntVar(&wave, "wave", 0, "the wave to print (default: the board's wave:)")
	return cmd
}

func orDash(s []string) string {
	if len(s) == 0 {
		return "-"
	}
	return strings.Join(s, ", ")
}
