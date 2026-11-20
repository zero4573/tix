package cli

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"sync"

	"github.com/spf13/cobra"

	"github.com/zero4573/tix/internal/note"
)

const reviewSocketEnv = "TIX_REVIEW_BROKER_SOCKET"

func reviewCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "review",
		Short: "Deep reviews on kiro models of other families, through the sandbox's review broker",
		Long: `A Claude session in a ticket workspace, started by claude-sandbox with
its review-broker, has $` + reviewSocketEnv + `: the broker on the host
runs the kiro sandbox on this workspace with tix-reviewer-deep on a kiro
model of another family. Kiro's login never enters the session.`,
	}
	cmd.AddCommand(reviewModelsCmd(), reviewKiroCmd())
	return cmd
}

type reviewReply struct {
	Log        string   `json:"log"`
	Status     string   `json:"status"`
	Detail     string   `json:"detail"`
	ReviewFile string   `json:"reviewFile"`
	Models     []string `json:"models"`
}

var errNoBroker = errors.New("no review broker in this session ($" + reviewSocketEnv + " unset)")

func reviewDial() (net.Conn, error) {
	sock := os.Getenv(reviewSocketEnv)
	if sock == "" {
		return nil, errNoBroker
	}
	return net.Dial("unix", sock)
}

func reviewCall(req map[string]any, logw io.Writer) (reviewReply, error) {
	conn, err := reviewDial()
	if err != nil {
		return reviewReply{}, err
	}
	defer conn.Close()
	line, err := json.Marshal(req)
	if err != nil {
		return reviewReply{}, err
	}
	if _, err := conn.Write(append(line, '\n')); err != nil {
		return reviewReply{}, err
	}
	sc := bufio.NewScanner(conn)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		var r reviewReply
		if err := json.Unmarshal(sc.Bytes(), &r); err != nil {
			return reviewReply{}, fmt.Errorf("bad reply from the broker: %q", sc.Text())
		}
		if r.Status != "" {
			return r, nil
		}
		if logw != nil {
			fmt.Fprintln(logw, r.Log)
		}
	}
	if err := sc.Err(); err != nil {
		return reviewReply{}, err
	}
	return reviewReply{}, errors.New("the broker closed the connection without a status")
}

func reviewModels() ([]string, error) {
	r, err := reviewCall(map[string]any{"op": "models"}, nil)
	if err != nil {
		return nil, err
	}
	if r.Status != "ok" {
		return nil, fmt.Errorf("%s: %s", r.Status, r.Detail)
	}
	return r.Models, nil
}

func reviewModelsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "models",
		Short: "List the kiro models the review broker allows",
		Long: `Prints the broker's models, one per line. Exit 4 when the session has
no broker (a kiro session, a sandbox without it, or no sandbox).`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			models, err := reviewModels()
			if err != nil {
				fmt.Fprintln(cmd.ErrOrStderr(), "tix review models:", err)
				return exitError(4)
			}
			for _, m := range models {
				fmt.Fprintln(cmd.OutOrStdout(), m)
			}
			return nil
		},
	}
}

func reviewKiroCmd() *cobra.Command {
	var wave int
	var repo, rng, model, brief, briefFile string
	cmd := &cobra.Command{
		Use:   "kiro <ID> --wave <N> --repo <slug> --range <sha>..<sha> --model <model>|all [--brief <text>|--brief-file <path>]",
		Short: "Run one deep review per kiro model through the review broker",
		Long: `Asks the review broker for a deep review of the wave's range on a kiro
model (--model all: every model it allows, at once). The kiro reviewer
writes tickets/<source>/<ID>/reviews/wave-<N>-<slug>-<model>.md, its
findings numbered from 101 for the broker's first model, 201 for the
second, ... The range is two commit shas (git rev-parse them first). The
brief (at most 8000 bytes) is what the lead's prompt would tell a reviewer:
impact, previous findings, conventions.

The kiro run's output goes to stderr. stdout, one line per model:

  ok  <model>  <review file>
  skipped  <model>  <usage-limit|timeout|failed|unavailable>  <detail>
  refused  <model>  <detail>

A skipped review never blocks the wave: the reviews that finished stand,
and the board notes the skip. Exit 0 when every model wrote its review, 4
when one was skipped (no broker included), 1 when the broker refused a
request (a bad argument: fix it, don't retry as is).`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id := args[0]
			if !note.ValidKey(id) {
				return fmt.Errorf("review kiro: not a ticket ID: %s", id)
			}
			if briefFile != "" {
				data, err := os.ReadFile(briefFile)
				if err != nil {
					return fmt.Errorf("review kiro: %w", err)
				}
				brief = string(data)
			}
			if wave < 1 || repo == "" || rng == "" || model == "" {
				return errors.New("review kiro: --wave, --repo, --range and --model are required")
			}
			out := cmd.OutOrStdout()
			models := []string{model}
			if model == "all" {
				var err error
				if models, err = reviewModels(); err != nil {
					fmt.Fprintf(out, "skipped\tall\tunavailable\t%v\n", err)
					return exitError(4)
				}
			}
			var mu sync.Mutex
			var wg sync.WaitGroup
			code := 0
			for _, m := range models {
				wg.Add(1)
				go func(m string) {
					defer wg.Done()
					req := map[string]any{"op": "review", "ticket": id, "wave": wave, "repo": repo,
						"range": rng, "model": m, "brief": brief}
					r, err := reviewCall(req, &prefixWriter{w: cmd.ErrOrStderr(), prefix: m + ": ", mu: &mu})
					mu.Lock()
					defer mu.Unlock()
					switch {
					case err != nil:
						fmt.Fprintf(out, "skipped\t%s\tunavailable\t%v\n", m, err)
						code = max(code, 4)
					case r.Status == "ok":
						fmt.Fprintf(out, "ok\t%s\t%s\n", m, r.ReviewFile)
					case r.Status == "refused":
						fmt.Fprintf(out, "refused\t%s\t%s\n", m, oneLine(r.Detail))
						code = 1
					default:
						fmt.Fprintf(out, "skipped\t%s\t%s\t%s\n", m, r.Status, oneLine(r.Detail))
						if code != 1 {
							code = 4
						}
					}
				}(m)
			}
			wg.Wait()
			if code != 0 {
				return exitError(code)
			}
			return nil
		},
	}
	cmd.Flags().IntVar(&wave, "wave", 0, "the wave")
	cmd.Flags().StringVar(&repo, "repo", "", "the repo's slug")
	cmd.Flags().StringVar(&rng, "range", "", "<sha>..<sha> of the wave")
	cmd.Flags().StringVar(&model, "model", "", "a kiro model the broker allows, or all")
	cmd.Flags().StringVar(&brief, "brief", "", "the lead's brief")
	cmd.Flags().StringVar(&briefFile, "brief-file", "", "a file holding the brief")
	return cmd
}

type prefixWriter struct {
	w      io.Writer
	prefix string
	mu     *sync.Mutex
}

func (p *prefixWriter) Write(b []byte) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, l := range strings.SplitAfter(string(b), "\n") {
		if l != "" {
			fmt.Fprint(p.w, p.prefix+l)
		}
	}
	return len(b), nil
}

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }
