package conductor

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/zero4573/tix/internal/note"
)

type Request struct {
	Op       string   `json:"op"`
	ID       string   `json:"id,omitempty"`
	Agent    string   `json:"agent,omitempty"`
	Feedback bool     `json:"feedback,omitempty"`
	Continue bool     `json:"continue,omitempty"`
	Text     string   `json:"text,omitempty"`
	Lines    int      `json:"lines,omitempty"`
	Timeout  int      `json:"timeout,omitempty"`
	PRs      []string `json:"prs,omitempty"`
	Force    bool     `json:"force,omitempty"`
}

type Reply struct {
	Status   string    `json:"status"`
	Detail   string    `json:"detail,omitempty"`
	Sessions []Session `json:"sessions,omitempty"`
	Output   string    `json:"output,omitempty"`
}

type Session struct {
	ID        string `json:"id"`
	Agent     string `json:"agent"`
	State     string `json:"state"`
	Pane      string `json:"pane"`
	Workspace string `json:"workspace"`
	Title     string `json:"title,omitempty"`
	Ticket    string `json:"ticket"`
	Main      bool   `json:"main"`
}

const (
	MaxText      = 8000
	MaxLines     = 400
	DefaultLines = 80
	MaxWait      = 30 * 60
	DefaultWait  = 10 * 60
)

type Runner func(ctx context.Context, env []string, name string, args ...string) ([]byte, error)

type Server struct {
	Vault, WorkRoot string
	Tix             string
	Herdr           string
	StateFile       string
	MaxSessions     int
	StartWait       time.Duration
	Run             Runner
	PRs             PRSource

	mu       sync.Mutex
	lastSync time.Time
	stopped  map[string]bool
}

func ExecRunner(ctx context.Context, env []string, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = append(os.Environ(), env...)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		var e struct {
			Error struct{ Code, Message string } `json:"error"`
		}
		if json.Unmarshal([]byte(msg), &e) == nil && e.Error.Message != "" {
			msg = e.Error.Code + ": " + e.Error.Message
		}
		if msg == "" {
			msg = err.Error()
		}
		return out, errors.New(tail(msg, 20))
	}
	return out, nil
}

func (s *Server) Serve(l net.Listener) error {
	for {
		c, err := l.Accept()
		if err != nil {
			return err
		}
		go s.conn(c)
	}
}

func (s *Server) conn(c net.Conn) {
	defer c.Close()
	sc := bufio.NewScanner(c)
	sc.Buffer(make([]byte, 64<<10), 64<<10)
	var r Reply
	if !sc.Scan() {
		return
	}
	var req Request
	dec := json.NewDecoder(strings.NewReader(sc.Text()))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		r = refuse("not a request: %v", err)
	} else {
		r = s.Handle(context.Background(), req)
	}
	b, _ := json.Marshal(r)
	_, _ = c.Write(append(b, '\n'))
}

func refuse(f string, a ...any) Reply { return Reply{Status: "refused", Detail: fmt.Sprintf(f, a...)} }
func fail(f string, a ...any) Reply   { return Reply{Status: "failed", Detail: fmt.Sprintf(f, a...)} }

func (s *Server) Handle(ctx context.Context, q Request) Reply {
	if q.Op == "sync" {
		return s.sync(ctx)
	}
	if q.Op == "unwatch" {
		if err := s.ticket(q.ID); err != nil {
			return refuse("%v", err)
		}
		return s.unwatch(q.ID)
	}
	if q.Op == "list" {
		ss, err := s.sessions(ctx)
		if err != nil {
			return fail("herdr: %v", err)
		}
		return Reply{Status: "ok", Sessions: ss}
	}
	if err := s.ticket(q.ID); err != nil {
		return refuse("%v", err)
	}
	switch q.Op {
	case "start":
		return s.start(ctx, q)
	case "prompt", "read", "wait", "stop":
		ss, err := s.sessions(ctx)
		if err != nil {
			return fail("herdr: %v", err)
		}
		cur, ok := find(ss, q.ID)
		if !ok {
			return refuse("%s has no session in herdr (tix main start %s)", q.ID, q.ID)
		}
		switch q.Op {
		case "prompt":
			return s.prompt(ctx, cur, q.Text)
		case "read":
			return s.read(ctx, cur, q.Lines)
		case "wait":
			return s.wait(ctx, cur, q.Timeout)
		default:
			return s.stop(ctx, cur)
		}
	case "clean":
		return s.clean(ctx, q.ID)
	case "watch":
		return s.watch(ctx, q.ID, q.PRs)
	}
	return refuse("unknown op %q (list, sync, start, prompt, read, wait, stop, clean, watch, unwatch)", q.Op)
}

func (s *Server) ticket(id string) error {
	if !note.ValidKey(id) {
		return fmt.Errorf("not a ticket ID: %q", id)
	}
	if _, err := os.Stat(note.TicketPath(s.Vault, id)); err != nil {
		return fmt.Errorf("%s isn't a ticket of the vault", id)
	}
	return nil
}

func (s *Server) status(id string) string { return note.Get(note.TicketPath(s.Vault, id), "status") }

type herdrAgent struct {
	Agent         *string `json:"agent"`
	Status        string  `json:"agent_status"`
	ForegroundCwd string  `json:"foreground_cwd"`
	Pane          string  `json:"pane_id"`
	Workspace     string  `json:"workspace_id"`
	Title         string  `json:"terminal_title_stripped"`
}

func (s *Server) sessions(ctx context.Context) ([]Session, error) {
	out, err := s.herdr(ctx, "agent", "list")
	if err != nil {
		return nil, err
	}
	var r struct {
		Result struct {
			Agents []herdrAgent `json:"agents"`
		} `json:"result"`
	}
	if err := json.Unmarshal(out, &r); err != nil {
		return nil, fmt.Errorf("agent list: %w", err)
	}
	state := s.load()
	var ss []Session
	for _, a := range r.Result.Agents {
		if a.Agent == nil || *a.Agent == "" {
			continue
		}
		rel, err := filepath.Rel(s.WorkRoot, a.ForegroundCwd)
		if err != nil || rel == "." || strings.HasPrefix(rel, "..") {
			continue
		}
		id := strings.Split(rel, string(filepath.Separator))[0]
		if s.ticket(id) != nil {
			continue
		}
		ss = append(ss, Session{ID: id, Agent: *a.Agent, State: a.Status, Pane: a.Pane,
			Workspace: a.Workspace, Title: a.Title, Ticket: s.status(id), Main: state[id] == a.Workspace})
	}
	sort.Slice(ss, func(i, j int) bool { return ss[i].ID < ss[j].ID })
	return ss, nil
}

func find(ss []Session, id string) (Session, bool) {
	for _, x := range ss {
		if x.ID == id {
			return x, true
		}
	}
	return Session{}, false
}

func (s *Server) start(ctx context.Context, q Request) Reply {
	if q.Agent != "" && q.Agent != "claude" && q.Agent != "kiro" {
		return refuse("agent %q: claude or kiro", q.Agent)
	}
	if st := s.status(q.ID); st == "done" || st == "closed" {
		return refuse("%s is %s", q.ID, st)
	}
	if why, lead := note.StartGuard(s.Vault, q.ID); why != "" && !q.Force {
		if lead != "" {
			return refuse("%s: %s, whose session works it; ask the user: work it in %s's session (start %s), or on its own (start %s --force)", q.ID, why, lead, lead, q.ID)
		}
		return refuse("%s: %s; ask the user before starting it with --force", q.ID, why)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	ss, err := s.sessions(ctx)
	if err != nil {
		return fail("herdr: %v", err)
	}
	if cur, ok := find(ss, q.ID); ok {
		return refuse("%s already has a session (%s, %s, pane %s)", q.ID, cur.Agent, cur.State, cur.Pane)
	}
	n := 0
	for _, x := range ss {
		if x.Main {
			n++
		}
	}
	if s.MaxSessions > 0 && n >= s.MaxSessions {
		return refuse("%d sessions started from main are running (at most %d): stop one first", n, s.MaxSessions)
	}
	out, err := s.herdr(ctx, "workspace", "create", "--cwd", s.WorkRoot, "--label", q.ID, "--no-focus",
		"--env", "TIX_VAULT="+s.Vault)
	if err != nil {
		return fail("herdr workspace create: %v", err)
	}
	var w struct {
		Result struct {
			Workspace struct {
				ID string `json:"workspace_id"`
			} `json:"workspace"`
			Root struct {
				ID string `json:"pane_id"`
			} `json:"root_pane"`
		} `json:"result"`
	}
	if err := json.Unmarshal(out, &w); err != nil || w.Result.Root.ID == "" {
		return fail("herdr workspace create: no pane in %q", tail(string(out), 3))
	}
	ws, pane := w.Result.Workspace.ID, w.Result.Root.ID
	st := s.load()
	st[q.ID] = ws
	s.save(st)
	line := shellQuote(s.Tix) + " start"
	if q.Agent != "" {
		line += " --agent " + q.Agent
	}
	if q.Feedback {
		line += " --feedback"
	}
	if q.Continue {
		line += " --continue"
	}
	if q.Force {
		line += " --force"
	}
	line += " " + q.ID
	if _, err := s.herdr(ctx, "pane", "run", pane, line); err != nil {
		return fail("herdr pane run: %v", err)
	}
	deadline := time.Now().Add(s.StartWait)
	for time.Now().Before(deadline) {
		time.Sleep(2 * time.Second)
		if ss, err := s.sessions(ctx); err == nil {
			if cur, ok := find(ss, q.ID); ok && cur.Pane == pane {
				_, _ = s.herdr(ctx, "agent", "rename", pane, agentName(q.ID))
				return Reply{Status: "ok", Detail: fmt.Sprintf("%s started (%s, pane %s, herdr workspace %s)", q.ID, cur.Agent, pane, ws), Sessions: []Session{cur}}
			}
		}
	}
	text, _ := s.herdr(ctx, "pane", "read", pane, "--source", "recent-unwrapped", "--lines", "20")
	return Reply{Status: "failed", Detail: fmt.Sprintf("no agent in pane %s after %s; its terminal is left open for the user", pane, s.StartWait), Output: tail(string(text), 20)}
}

func agentName(id string) string {
	n := strings.ToLower(id)
	if len(n) > 32 {
		n = n[:32]
	}
	return n
}

func (s *Server) prompt(ctx context.Context, cur Session, text string) Reply {
	if cur.State == "blocked" {
		return refuse("%s is waiting on an approval or a question: that's the user's to answer, in pane %s", cur.ID, cur.Pane)
	}
	if err := checkText(text); err != nil {
		return refuse("%v", err)
	}
	if _, err := s.herdr(ctx, "agent", "prompt", cur.Pane, text); err != nil {
		return fail("herdr agent prompt: %v", err)
	}
	return Reply{Status: "ok", Detail: fmt.Sprintf("sent to %s (pane %s)", cur.ID, cur.Pane)}
}

func checkText(t string) error {
	switch {
	case strings.TrimSpace(t) == "":
		return errors.New("empty prompt")
	case len(t) > MaxText:
		return fmt.Errorf("prompt is %d bytes (at most %d)", len(t), MaxText)
	case !utf8.ValidString(t):
		return errors.New("prompt isn't UTF-8")
	}
	for _, r := range t {
		if unicode.IsControl(r) && r != '\n' && r != '\t' {
			return fmt.Errorf("prompt has a control character (%U)", r)
		}
	}
	return nil
}

func (s *Server) read(ctx context.Context, cur Session, lines int) Reply {
	if lines <= 0 {
		lines = DefaultLines
	}
	if lines > MaxLines {
		lines = MaxLines
	}
	out, err := s.herdr(ctx, "agent", "read", cur.Pane, "--source", "recent-unwrapped", "--lines", fmt.Sprint(lines))
	if err != nil {
		return fail("herdr agent read: %v", err)
	}
	return Reply{Status: "ok", Output: string(out), Sessions: []Session{cur}}
}

func (s *Server) wait(ctx context.Context, cur Session, secs int) Reply {
	if secs <= 0 {
		secs = DefaultWait
	}
	if secs > MaxWait {
		secs = MaxWait
	}
	out, err := s.herdr(ctx, "agent", "wait", cur.Pane, "--timeout", fmt.Sprint(secs*1000))
	if err != nil {
		if strings.Contains(err.Error(), "timeout") {
			return Reply{Status: "ok", Detail: fmt.Sprintf("%s still %s after %ds", cur.ID, cur.State, secs), Sessions: []Session{cur}}
		}
		return fail("herdr agent wait: %v", err)
	}
	var r struct {
		Result struct {
			Agent herdrAgent `json:"agent"`
		} `json:"result"`
	}
	if json.Unmarshal(out, &r) == nil && r.Result.Agent.Status != "" {
		cur.State = r.Result.Agent.Status
	}
	return Reply{Status: "ok", Detail: fmt.Sprintf("%s is %s", cur.ID, cur.State), Sessions: []Session{cur}}
}

func (s *Server) stop(ctx context.Context, cur Session) Reply {
	if !cur.Main {
		return refuse("%s's session wasn't started from main (herdr workspace %s): the user ends it", cur.ID, cur.Workspace)
	}
	if cur.State == "working" || cur.State == "blocked" {
		return refuse("%s is %s: wait for it to settle first", cur.ID, cur.State)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.herdr(ctx, "workspace", "close", cur.Workspace); err != nil {
		return fail("herdr workspace close: %v", err)
	}
	st := s.load()
	delete(st, cur.ID)
	s.save(st)
	if s.stopped == nil {
		s.stopped = map[string]bool{}
	}
	s.stopped[cur.ID] = true
	return Reply{Status: "ok", Detail: fmt.Sprintf("%s's session stopped (herdr workspace %s closed)", cur.ID, cur.Workspace),
		Output: s.cleanTasks(ctx, cur.ID)}
}

func (s *Server) cleanTasks(ctx context.Context, id string) string {
	cctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	var out []string
	for _, sub := range []string{"task-clean", "tidy-clean"} {
		b, err := s.Run(cctx, []string{"TIX_VAULT=" + s.Vault}, s.Tix, "ws", sub, id)
		text := strings.TrimSpace(tail(string(b), 5))
		if err != nil {
			text = strings.TrimSpace(text + "\n" + err.Error())
		}
		if text != "" {
			out = append(out, "tix ws "+sub+": "+text)
		}
	}
	return strings.Join(out, "\n")
}

func (s *Server) wasStopped(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	ok := s.stopped[id]
	delete(s.stopped, id)
	return ok
}

func (s *Server) clean(ctx context.Context, id string) Reply {
	if st := s.status(id); st != "done" && st != "closed" {
		return refuse("%s is %q: only a done or closed ticket is cleaned", id, st)
	}
	ss, err := s.sessions(ctx)
	if err != nil {
		return fail("herdr: %v", err)
	}
	if cur, ok := find(ss, id); ok {
		return refuse("%s's session is still open (pane %s): stop it first", id, cur.Pane)
	}
	cctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	out, err := s.Run(cctx, []string{"TIX_VAULT=" + s.Vault}, s.Tix, "clean", "--yes", id)
	if err != nil {
		return Reply{Status: "failed", Detail: "tix clean: " + err.Error(), Output: tail(string(out), 40)}
	}
	return Reply{Status: "ok", Detail: id + " cleaned", Output: tail(string(out), 40)}
}

func (s *Server) herdr(ctx context.Context, args ...string) ([]byte, error) {
	cctx, cancel := context.WithTimeout(ctx, time.Duration(MaxWait+60)*time.Second)
	defer cancel()
	return s.Run(cctx, nil, s.Herdr, args...)
}

func (s *Server) load() map[string]string {
	m := map[string]string{}
	if b, err := os.ReadFile(s.StateFile); err == nil {
		_ = json.Unmarshal(b, &m)
	}
	return m
}

func (s *Server) save(m map[string]string) {
	b, _ := json.MarshalIndent(m, "", "  ")
	_ = os.MkdirAll(filepath.Dir(s.StateFile), 0o700)
	tmp := s.StateFile + ".tmp"
	if os.WriteFile(tmp, append(b, '\n'), 0o600) == nil {
		_ = os.Rename(tmp, s.StateFile)
	}
}

func shellQuote(s string) string {
	if s != "" && strings.IndexFunc(s, func(r rune) bool {
		return !(r == '/' || r == '-' || r == '_' || r == '.' || unicode.IsLetter(r) || unicode.IsDigit(r))
	}) < 0 {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func tail(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

const SyncEvery = 10 * time.Minute

func (s *Server) sync(ctx context.Context) Reply {
	s.mu.Lock()
	if since := time.Since(s.lastSync); !s.lastSync.IsZero() && since < SyncEvery {
		s.mu.Unlock()
		return refuse("synced %s ago; at most once every %s", since.Round(time.Second), SyncEvery)
	}
	s.lastSync = time.Now()
	s.mu.Unlock()
	cctx, cancel := context.WithTimeout(ctx, 15*time.Minute)
	defer cancel()
	out, err := s.Run(cctx, []string{"TIX_VAULT=" + s.Vault}, s.Tix, "sync")
	if err != nil {
		return Reply{Status: "failed", Detail: "tix sync: " + err.Error(), Output: tail(string(out), 30)}
	}
	return Reply{Status: "ok", Detail: "synced", Output: tail(string(out), 30)}
}
