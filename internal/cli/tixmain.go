package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/zero4573/tix/internal/conductor"
	"github.com/zero4573/tix/internal/config"
	"github.com/zero4573/tix/internal/harness"
	"github.com/zero4573/tix/internal/launcher"
	"github.com/zero4573/tix/internal/modelcache"
	"github.com/zero4573/tix/internal/vault"
	"github.com/zero4573/tix/internal/workspace"
)

const mainSocketEnv = "TIX_MAIN_SOCKET"

func mainCmd() *cobra.Command {
	var cont bool
	var agent string
	cmd := &cobra.Command{
		Use:   "main [--continue] [--agent claude|kiro] | main <list|sync|start|prompt|read|wait|stop|clean|watch|unwatch> ...",
		Short: "The main session: one session that runs the vault's ticket sessions in herdr",
		Long: `tix main, in a herdr pane on the host, opens the main session of the
current vault (tix vault default): a session running /tix:main in
<workRoot>/.main-<vault>, in this terminal, with the vault read-write and
the repos read-only. It runs the tickets' lifecycle around their own
sessions: it syncs the vault, keeps a queue of tickets to work, starts and
restarts ticket sessions (claude or kiro), checks a handed-over ticket and
runs /tix:ship, stops the session at merge-ready and watches the pull
requests itself, restarting it for review threads, CI or a rebase, and
closes merged tickets. It keeps its state in main.md there, so a new main
session picks up where the last one left off; ticket sessions keep running
while main is stopped.

It reaches herdr only through a service tix main runs on the host for as
long as the session lasts, on a socket mounted into the session
($` + mainSocketEnv + `). The service acts on the vault's ticket sessions
only (a herdr agent whose directory is <workRoot>/<ID>), never types into an
approval or question dialog, stops only sessions it started, and cleans
only done or closed tickets. Stopping a session, it also removes the
ticket's merged task worktrees and tidy clones (tix ws task-clean and
tidy-clean), which a sandboxed session can't. It wakes main with its watch
pass (/tix:main watch <events>) only when something main acts on happened,
since each pass is a turn herdr notifies you of: a ticket session of main's
exited, finished a turn while checking or shipping, or sat idle 30 minutes
while working; a ticket note's status changed; a watched pull request
changed (state, comments or commits, CI, merge checks; read every 10
minutes); an hourly tix sync brought new tickets; its security sweep found
something main acts on. The service runs that sweep itself (tix cve update,
on the host) whenever the last one recorded in the vault's
security/.state.json is 6 hours old, so a restart of main doesn't sweep
again, and retries a failed one after 30 minutes. Its subcommands are how
the session calls it:

  tix main list                       ticket sessions: ID, agent, state, pane, ticket status, main|user
  tix main sync                       tix sync on the host now (at most every 10 minutes)
  tix main start <ID> [--agent A] [--feedback] [--continue] [--force]
  tix main prompt <ID> <text>|--file <path>
  tix main read <ID> [--lines N]      the session's recent terminal text
  tix main wait <ID> [--timeout S]    until it's idle, done or blocked
  tix main stop <ID>                  close a session main started (not while it works)
  tix main clean <ID>                 tix clean --yes on the host, for a done or closed ticket
  tix main watch <ID> <PR>...         watch pull requests (links or workspace/repo#id)
  tix main unwatch <ID>               stop watching the ticket's pull requests

They exit 0 on success, 1 when the service refused or failed, and 4 when
the session has no service ($` + mainSocketEnv + ` unset).`,

		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, err := vault.Require()
			if err != nil {
				return err
			}
			return runMain(ctx, cont, agent)
		},
	}
	cmd.Flags().BoolVarP(&cont, "continue", "c", false, continueHelp)
	cmd.Flags().StringVar(&agent, "agent", "", agentHelp)
	cmd.AddCommand(mainServeCmd(), mainListCmd(), mainStartCmd(), mainPromptCmd(),
		mainSimpleCmd("sync", "sync", "Run tix sync on the host (at most every 10 minutes)"),
		mainSimpleCmd("read", "read <ID> [--lines N]", "Print the session's recent terminal text"),
		mainSimpleCmd("wait", "wait <ID> [--timeout S]", "Wait until the session is idle, done or blocked"),
		mainSimpleCmd("stop", "stop <ID>", "Close a ticket session main started"),
		mainSimpleCmd("clean", "clean <ID>", "Run tix clean --yes on the host for a done or closed ticket"),
		mainSimpleCmd("unwatch", "unwatch <ID>", "Stop watching the ticket's pull requests"), mainWatchCmd())
	return cmd
}

func mainDir(ctx vault.Context) string {
	return filepath.Join(ctx.WorkRoot, ".main-"+filepath.Base(ctx.Vault))
}

func runMain(ctx vault.Context, cont bool, agent string) error {
	if !isTerminal() {
		return errors.New("the session runs in this terminal, and this isn't one (stdin/stdout); run it from a terminal")
	}
	if os.Getenv("HERDR_ENV") != "1" {
		return errors.New("run tix main in a herdr pane: the main session runs ticket sessions in herdr")
	}
	herdr, err := exec.LookPath("herdr")
	if err != nil {
		return err
	}
	dir := mainDir(ctx)
	_, err = os.Stat(filepath.Join(dir, ".agent-state"))
	resume := err == nil && cont
	if err := workspace.Ensure(dir, "main", ctx.Vault); err != nil {
		return err
	}
	h, err := harness.Select(agent, vault.AgentOf(ctx.Vault))
	if err != nil {
		return err
	}
	if prev := workspace.Agent(dir); resume && prev != h.Name() {
		warnf("main: its last session ran %s, not %s, so it can't be continued: starting a new one", prev, h.Name())
		resume = false
	}
	if err := workspace.ClaimSession(dir, "main"); err != nil {
		var running *workspace.RunningError
		if errors.As(err, &running) {
			return fmt.Errorf("the main session of this vault is running (pid %d) in another terminal", running.Record.PID)
		}
		warnf("couldn't record the session in %s: %v", workspace.SessionsFile(dir), err)
	}
	_ = workspace.SetAgent(dir, h.Name())
	writeToolVersions(ctx, dir)

	sock, err := startConductor(ctx, herdr, dir, h.SkillRef("main")+" watch")
	if err != nil {
		return err
	}
	s := harness.Session{
		Dir:          dir,
		Dirs:         []string{ctx.Vault, filepath.Dir(sock)},
		ReposRoot:    ctx.ProjectsRoot,
		ReadOnly:     []string{modelcache.Dir()},
		DenyShell:    []string{"git commit", "git push"},
		Instructions: mainClaudeMD(ctx),
		Settings:     true,
		Skill:        "main",
		Continue:     resume,
	}
	if err := h.Prepare(s); err != nil {
		return err
	}
	argv, err := h.Argv(s)
	if err != nil {
		return err
	}
	env := h.Env(s)
	env["TIX_VAULT"] = ctx.Vault
	env[mainSocketEnv] = sock
	fmt.Printf("tix main: the main session of %s runs in this terminal\n", filepath.Base(ctx.Vault))
	return passExit(launcher.Run(launcher.Session{Dir: dir, Argv: argv, Env: env}))
}

func startConductor(ctx vault.Context, herdr, dir, wake string) (string, error) {
	base := os.Getenv("XDG_RUNTIME_DIR")
	if base == "" {
		base = os.TempDir()
	}
	sockDir, err := os.MkdirTemp(base, "tix-main-")
	if err != nil {
		return "", err
	}
	sock := filepath.Join(sockDir, "main.sock")
	self, err := os.Executable()
	if err != nil {
		return "", err
	}
	logf, err := os.OpenFile(filepath.Join(dir, "conductor.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return "", err
	}
	defer logf.Close()
	c := exec.Command(self, "main", "serve", "--socket", sock, "--herdr", herdr, "--tix", self,
		"--main-pane", os.Getenv("HERDR_PANE_ID"), "--wake", wake, "--main-file", filepath.Join(dir, "main.md"))
	c.Env = append(os.Environ(), "TIX_VAULT="+ctx.Vault)
	c.Stdout, c.Stderr = logf, logf
	c.SysProcAttr = ownGroup()
	if err := c.Start(); err != nil {
		return "", err
	}
	for range 50 {
		if fi, err := os.Stat(sock); err == nil && fi.Mode()&os.ModeSocket != 0 {
			return sock, nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	_ = c.Process.Kill()
	return "", fmt.Errorf("the main service didn't start (see %s)", filepath.Join(dir, "conductor.log"))
}

func mainClaudeMD(ctx vault.Context) string {
	return fmt.Sprintf("# Main session\n\n"+
		"Written by tix main; regenerated on every start, so don't edit it.\n\n"+
		"- Vault: `%[1]s`. Read its `AGENTS.md` for note rules.\n"+
		"- Ticket workspaces: `%[2]s/<ID>`. Each ticket's session works there; you\n"+
		"  don't edit its repos, commit or push.\n"+
		"- Repos: main clones at `%[3]s/<provider>/<owner>/<repo>`, read-only.\n"+
		"- Ticket sessions: only through `tix main <list|start|prompt|read|wait|stop|clean>`.\n\n"+
		"Follow the `main` skill (`/tix:main`). Your own notes go in this folder's\n"+
		"`main.md`.\n",
		ctx.Vault, ctx.WorkRoot, ctx.ProjectsRoot)
}

func mainServeCmd() *cobra.Command {
	var sock, herdr, tix, pane, wake, mainFile string
	var max int
	var tick, idle, prPoll, syncEach, sweepEach time.Duration
	cmd := &cobra.Command{
		Use:    "serve --socket <path> --herdr <bin> --tix <bin>",
		Short:  "The main session's host service (started by tix main)",
		Hidden: true,
		Args:   cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, err := vault.Require()
			if err != nil {
				return err
			}
			srv := &conductor.Server{Vault: ctx.Vault, WorkRoot: ctx.WorkRoot, Tix: tix, Herdr: herdr,
				StateFile:   filepath.Join(config.StateDir(), "main-"+filepath.Base(ctx.Vault)+".json"),
				MaxSessions: max, StartWait: 3 * time.Minute, Run: conductor.ExecRunner}
			if os.Getenv("TIX_MCP_CONFIG") != "" {
				srv.PRs = conductor.BitbucketPRs
			}
			_ = os.Remove(sock)
			l, err := net.Listen("unix", sock)
			if err != nil {
				return err
			}
			_ = os.Chmod(sock, 0o600)
			if pane != "" && wake != "" {
				w := &conductor.Waker{S: srv, Pane: pane, Command: wake, MainFile: mainFile, Debounce: time.Minute,
					IdleAfter: idle, PRPoll: prPoll, SyncEach: syncEach, SweepEach: sweepEach, Tick: tick}
				go w.Run(context.Background(), 20*time.Second)
			}
			parent := os.Getppid()
			go func() {
				for os.Getppid() == parent {
					time.Sleep(2 * time.Second)
				}
				l.Close()
			}()
			fmt.Fprintf(os.Stderr, "%s tix main serve: %s, vault %s\n", time.Now().Format(time.RFC3339), sock, ctx.Vault)
			_ = srv.Serve(l)
			_ = os.RemoveAll(filepath.Dir(sock))
			return nil
		},
	}
	cmd.Flags().StringVar(&sock, "socket", "", "the socket")
	cmd.Flags().StringVar(&herdr, "herdr", "herdr", "the herdr binary")
	cmd.Flags().StringVar(&tix, "tix", "tix", "the tix binary started sessions and clean run")
	cmd.Flags().IntVar(&max, "max-sessions", 6, "live ticket sessions started from main, at most")
	cmd.Flags().StringVar(&pane, "main-pane", "", "main's herdr pane, prompted with --wake")
	cmd.Flags().StringVar(&wake, "wake", "", "main's watch command (/tix:main watch)")
	cmd.Flags().StringVar(&mainFile, "main-file", "", "main.md (its ## Tickets give each ticket's stage)")
	cmd.Flags().DurationVar(&idle, "idle-after", 30*time.Minute, "wake main when a working ticket's session sits idle this long")
	cmd.Flags().DurationVar(&prPoll, "pr-poll", 10*time.Minute, "how often the watched pull requests are read")
	cmd.Flags().DurationVar(&syncEach, "sync-each", time.Hour, "how often tix sync runs (main is woken only for new tickets)")
	cmd.Flags().DurationVar(&sweepEach, "cve-sweep-each", 6*time.Hour, "how old the last security sweep may get before the service runs tix cve update (0: never)")
	cmd.Flags().DurationVar(&tick, "tick", 0, "also wake main this often with no event (0: never)")
	return cmd
}

func mainCall(req conductor.Request) (conductor.Reply, error) {
	sock := os.Getenv(mainSocketEnv)
	if sock == "" {
		return conductor.Reply{}, errors.New("no main service in this session ($" + mainSocketEnv + " unset): tix main starts one")
	}
	conn, err := net.Dial("unix", sock)
	if err != nil {
		return conductor.Reply{}, err
	}
	defer conn.Close()
	b, err := json.Marshal(req)
	if err != nil {
		return conductor.Reply{}, err
	}
	if _, err := conn.Write(append(b, '\n')); err != nil {
		return conductor.Reply{}, err
	}
	sc := bufio.NewScanner(conn)
	sc.Buffer(make([]byte, 64<<10), 8<<20)
	if !sc.Scan() {
		if err := sc.Err(); err != nil {
			return conductor.Reply{}, err
		}
		return conductor.Reply{}, errors.New("the main service closed the connection")
	}
	var r conductor.Reply
	if err := json.Unmarshal(sc.Bytes(), &r); err != nil {
		return conductor.Reply{}, fmt.Errorf("bad reply from the main service: %w", err)
	}
	return r, nil
}

func mainDo(out, errw io.Writer, req conductor.Request) error {
	r, err := mainCall(req)
	if err != nil {
		fmt.Fprintln(errw, "tix main:", err)
		return exitError(4)
	}
	if r.Status != "ok" {
		fmt.Fprintf(errw, "tix main %s: %s: %s\n", req.Op, r.Status, r.Detail)
		if r.Output != "" {
			fmt.Fprintln(errw, r.Output)
		}
		return exitError(1)
	}
	switch req.Op {
	case "list":
		for _, s := range r.Sessions {
			who := "user"
			if s.Main {
				who = "main"
			}
			status := s.Ticket
			if status == "" {
				status = "-"
			}
			fmt.Fprintf(out, "%s\t%s\t%s\t%s\t%s\t%s\n", s.ID, s.Agent, s.State, s.Pane, status, who)
		}
	case "read":
		fmt.Fprint(out, r.Output)
		if !strings.HasSuffix(r.Output, "\n") {
			fmt.Fprintln(out)
		}
	default:
		fmt.Fprintln(out, r.Detail)
		if r.Output != "" {
			fmt.Fprintln(out, r.Output)
		}
	}
	return nil
}

func mainListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List the vault's ticket sessions in herdr",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return mainDo(cmd.OutOrStdout(), cmd.ErrOrStderr(), conductor.Request{Op: "list"})
		},
	}
}

func mainStartCmd() *cobra.Command {
	var agent string
	var feedback, cont, force bool
	cmd := &cobra.Command{
		Use:   "start <ID> [--agent claude|kiro] [--feedback] [--continue] [--force]",
		Short: "Start a ticket's session in a new herdr workspace (tix start <ID>)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return mainDo(cmd.OutOrStdout(), cmd.ErrOrStderr(), conductor.Request{Op: "start", ID: args[0], Agent: agent, Feedback: feedback, Continue: cont, Force: force})
		},
	}
	cmd.Flags().StringVar(&agent, "agent", "", "claude or kiro (default: tix start's)")
	cmd.Flags().BoolVar(&feedback, "feedback", false, "tix start --feedback: /tix:pr-feedback")
	cmd.Flags().BoolVar(&force, "force", false, "tix start --force: start an ignored ticket, or one an open epic or lead covers, on its own (the user's call)")
	cmd.Flags().BoolVarP(&cont, "continue", "c", false, "tix start --continue: the workspace's last conversation")
	return cmd
}

func mainPromptCmd() *cobra.Command {
	var file string
	cmd := &cobra.Command{
		Use:   "prompt <ID> <text> | prompt <ID> --file <path>",
		Short: "Send a prompt to a ticket's session (refused while it waits on an approval)",
		Args:  cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			text := ""
			switch {
			case file != "" && len(args) == 1:
				b, err := os.ReadFile(file)
				if err != nil {
					return err
				}
				text = string(b)
			case file == "" && len(args) == 2:
				text = args[1]
			default:
				return errors.New("give the prompt as an argument or with --file, not both")
			}
			return mainDo(cmd.OutOrStdout(), cmd.ErrOrStderr(), conductor.Request{Op: "prompt", ID: args[0], Text: text})
		},
	}
	cmd.Flags().StringVar(&file, "file", "", "a file holding the prompt")
	return cmd
}

func mainSimpleCmd(op, use, short string) *cobra.Command {
	var lines, timeout int
	cmd := &cobra.Command{
		Use:   use,
		Short: short,
		Args:  cobra.RangeArgs(0, 1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if (op == "sync") != (len(args) == 0) {
				return fmt.Errorf("usage: tix main %s", use)
			}
			id := ""
			if len(args) == 1 {
				id = args[0]
			}
			return mainDo(cmd.OutOrStdout(), cmd.ErrOrStderr(), conductor.Request{Op: op, ID: id, Lines: lines, Timeout: timeout})
		},
	}
	switch op {
	case "read":
		cmd.Flags().IntVar(&lines, "lines", conductor.DefaultLines, fmt.Sprintf("lines (at most %d)", conductor.MaxLines))
	case "wait":
		cmd.Flags().IntVar(&timeout, "timeout", conductor.DefaultWait, fmt.Sprintf("seconds (at most %d)", conductor.MaxWait))
	}
	return cmd
}

func mainWatchCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "watch <ID> <PR>...",
		Short: "Watch a ticket's pull requests: a change wakes main",
		Args:  cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return mainDo(cmd.OutOrStdout(), cmd.ErrOrStderr(), conductor.Request{Op: "watch", ID: args[0], PRs: args[1:]})
		},
	}
}
