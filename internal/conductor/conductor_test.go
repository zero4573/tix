package conductor

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type agentRow struct {
	Agent, Status, Cwd, Pane, Workspace string
}

type fakeHerdr struct {
	t      *testing.T
	work   string
	agents []agentRow
	calls  [][]string
	main   string
	onTix  func(args []string)
	tixOut func(args []string) ([]byte, error)
	n      int
	mu     sync.Mutex
}

func (f *fakeHerdr) run(_ context.Context, env []string, name string, args ...string) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, append([]string{name}, args...))
	cmd := strings.Join(args[:min(2, len(args))], " ")
	switch {
	case name == "tix":
		if f.onTix != nil {
			f.onTix(args)
		}
		if f.tixOut != nil {
			return f.tixOut(args)
		}
		return []byte("ran " + strings.Join(args, " ") + "\n"), nil
	case cmd == "agent list":
		var rows []map[string]any
		for _, a := range f.agents {
			rows = append(rows, map[string]any{"agent": a.Agent, "agent_status": a.Status,
				"foreground_cwd": a.Cwd, "pane_id": a.Pane, "workspace_id": a.Workspace})
		}
		rows = append(rows, map[string]any{"agent": nil, "foreground_cwd": f.work, "pane_id": "w9:p1", "workspace_id": "w9"})
		return json.Marshal(map[string]any{"result": map[string]any{"agents": rows}})
	case cmd == "workspace create":
		f.n++
		ws := "wn" + string(rune('0'+f.n))
		return json.Marshal(map[string]any{"result": map[string]any{
			"workspace": map[string]any{"workspace_id": ws}, "root_pane": map[string]any{"pane_id": ws + ":p1"}}})
	case cmd == "pane run":
		id := args[len(args)-1][strings.LastIndex(args[len(args)-1], " ")+1:]
		pane := args[2]
		f.agents = append(f.agents, agentRow{"claude", "working", filepath.Join(f.work, id), pane, strings.Split(pane, ":")[0]})
		return nil, nil
	case cmd == "workspace close":
		var keep []agentRow
		for _, a := range f.agents {
			if a.Workspace != args[2] {
				keep = append(keep, a)
			}
		}
		f.agents = keep
		return []byte(`{"result":{"type":"ok"}}`), nil
	case cmd == "agent get":
		return json.Marshal(map[string]any{"result": map[string]any{"agent": map[string]any{"agent_status": f.main}}})
	case cmd == "agent prompt", cmd == "agent rename":
		return nil, nil
	case cmd == "agent read":
		return []byte("❯ hello\n"), nil
	case cmd == "agent wait":
		return []byte(`{"result":{"agent":{"agent_status":"idle"}}}`), nil
	}
	return nil, errors.New("unexpected herdr call: " + strings.Join(args, " "))
}

func setup(t *testing.T) (*Server, *fakeHerdr) {
	t.Helper()
	root := t.TempDir()
	vault, work := filepath.Join(root, "vault"), filepath.Join(root, "work")
	for id, status := range map[string]string{"PROJ-1": "in-progress", "PROJ-2": "review", "PROJ-3": "done"} {
		d := filepath.Join(vault, "tickets", "jira", id)
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(d, id+".md"), []byte("---\nstatus: "+status+"\n---\n# "+id+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	f := &fakeHerdr{t: t, work: work, agents: []agentRow{
		{"claude", "idle", filepath.Join(work, "PROJ-2"), "w1:p1", "w1"},
		{"claude", "working", filepath.Join(root, "elsewhere"), "w2:p1", "w2"},
		{"kiro", "idle", filepath.Join(work, "NOPE-9"), "w3:p1", "w3"},
		{"claude", "blocked", filepath.Join(work, "PROJ-1", "tasks"), "w4:p1", "w4"},
	}}
	s := &Server{Vault: vault, WorkRoot: work, Tix: "tix", Herdr: "herdr", MaxSessions: 2,
		StateFile: filepath.Join(root, "state", "main.json"), StartWait: 3 * time.Second, Run: f.run}
	return s, f
}

func do(t *testing.T, s *Server, q Request) Reply {
	t.Helper()
	return s.Handle(context.Background(), q)
}

func TestList(t *testing.T) {
	s, _ := setup(t)
	r := do(t, s, Request{Op: "list"})
	if r.Status != "ok" || len(r.Sessions) != 2 {
		t.Fatalf("list: %+v", r)
	}
	if got := r.Sessions[0]; got.ID != "PROJ-1" || got.State != "blocked" || got.Ticket != "in-progress" || got.Main {
		t.Errorf("PROJ-1: %+v", got)
	}
	if got := r.Sessions[1]; got.ID != "PROJ-2" || got.Pane != "w1:p1" {
		t.Errorf("PROJ-2: %+v", got)
	}
}

func TestRefusals(t *testing.T) {
	s, f := setup(t)
	for _, c := range []struct {
		q    Request
		want string
	}{
		{Request{Op: "start", ID: "proj-1"}, "not a ticket ID"},
		{Request{Op: "start", ID: "NOPE-9"}, "isn't a ticket of the vault"},
		{Request{Op: "start", ID: "PROJ-3"}, "is done"},
		{Request{Op: "start", ID: "PROJ-2"}, "already has a session"},
		{Request{Op: "start", ID: "PROJ-1", Agent: "codex"}, "claude or kiro"},
		{Request{Op: "prompt", ID: "PROJ-1", Text: "go on"}, "the user's to answer"},
		{Request{Op: "prompt", ID: "PROJ-2", Text: "go\x1b[A on"}, "control character"},
		{Request{Op: "prompt", ID: "PROJ-2", Text: strings.Repeat("x", MaxText+1)}, "at most"},
		{Request{Op: "prompt", ID: "PROJ-2", Text: "  "}, "empty prompt"},
		{Request{Op: "read", ID: "PROJ-3"}, "has no session"},
		{Request{Op: "stop", ID: "PROJ-2"}, "wasn't started from main"},
		{Request{Op: "clean", ID: "PROJ-2"}, `is "review"`},
		{Request{Op: "rm", ID: "PROJ-2"}, "unknown op"},
	} {
		r := do(t, s, c.q)
		if r.Status != "refused" || !strings.Contains(r.Detail, c.want) {
			t.Errorf("%+v: got %+v, want refused with %q", c.q, r, c.want)
		}
	}
	for _, c := range f.calls {
		if c[1] != "agent" || c[2] != "list" {
			t.Errorf("a refusal ran %v", c)
		}
	}
}

func TestStartPromptStopClean(t *testing.T) {
	s, f := setup(t)
	f.agents = f.agents[:1]
	r := do(t, s, Request{Op: "start", ID: "PROJ-1", Agent: "kiro"})
	if r.Status != "ok" || len(r.Sessions) != 1 || !r.Sessions[0].Main {
		t.Fatalf("start: %+v", r)
	}
	var run []string
	for _, c := range f.calls {
		if c[1] == "pane" {
			run = c
		}
		if c[1] == "workspace" && c[2] == "create" && !strings.Contains(strings.Join(c, " "), "TIX_VAULT="+s.Vault) {
			t.Errorf("workspace create without the vault: %v", c)
		}
	}
	if strings.Join(run[2:], " ") != "run wn1:p1 tix start --agent kiro PROJ-1" {
		t.Errorf("pane run: %v", run)
	}
	if r := do(t, s, Request{Op: "prompt", ID: "PROJ-1", Text: "/tix:ship PROJ-1"}); r.Status != "ok" {
		t.Errorf("prompt: %+v", r)
	}
	if r := do(t, s, Request{Op: "stop", ID: "PROJ-1"}); r.Status != "refused" || !strings.Contains(r.Detail, "working") {
		t.Errorf("stop while working: %+v", r)
	}
	if r := do(t, s, Request{Op: "wait", ID: "PROJ-1", Timeout: 5}); r.Status != "ok" || !strings.Contains(r.Detail, "idle") {
		t.Errorf("wait: %+v", r)
	}
	f.agents[1].Status = "idle"
	if r := do(t, s, Request{Op: "read", ID: "PROJ-1", Lines: 9999}); r.Status != "ok" || r.Output != "❯ hello\n" {
		t.Errorf("read: %+v", r)
	}
	if last := f.calls[len(f.calls)-1]; last[len(last)-1] != "400" {
		t.Errorf("read lines not capped: %v", last)
	}
	if r := do(t, s, Request{Op: "stop", ID: "PROJ-1"}); r.Status != "ok" {
		t.Errorf("stop: %+v", r)
	}
	if len(s.load()) != 0 {
		t.Errorf("state after stop: %v", s.load())
	}
	if r := do(t, s, Request{Op: "clean", ID: "PROJ-3"}); r.Status != "ok" {
		t.Errorf("clean: %+v", r)
	}
	last := f.calls[len(f.calls)-1]
	if strings.Join(last, " ") != "tix clean --yes PROJ-3" {
		t.Errorf("clean ran %v", last)
	}
}

func TestMaxSessions(t *testing.T) {
	s, f := setup(t)
	f.agents = nil
	s.MaxSessions = 1
	if r := do(t, s, Request{Op: "start", ID: "PROJ-1"}); r.Status != "ok" {
		t.Fatalf("start: %+v", r)
	}
	if r := do(t, s, Request{Op: "start", ID: "PROJ-2"}); r.Status != "refused" || !strings.Contains(r.Detail, "at most 1") {
		t.Errorf("second start: %+v", r)
	}
}

func TestShellQuote(t *testing.T) {
	for in, want := range map[string]string{"/nix/store/x-tix/bin/tix": "/nix/store/x-tix/bin/tix", "/a b/tix": "'/a b/tix'", "/a'b": `'/a'\''b'`} {
		if got := shellQuote(in); got != want {
			t.Errorf("shellQuote(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSyncAndContinue(t *testing.T) {
	s, f := setup(t)
	f.agents = nil
	if r := do(t, s, Request{Op: "sync"}); r.Status != "ok" || r.Output != "ran sync" {
		t.Fatalf("sync: %+v", r)
	}
	if r := do(t, s, Request{Op: "sync"}); r.Status != "refused" || !strings.Contains(r.Detail, "at most once every") {
		t.Errorf("second sync: %+v", r)
	}
	if r := do(t, s, Request{Op: "start", ID: "PROJ-1", Continue: true}); r.Status != "ok" {
		t.Fatalf("start: %+v", r)
	}
	for _, c := range f.calls {
		if c[1] == "pane" && c[len(c)-1] != "tix start --continue PROJ-1" {
			t.Errorf("pane run: %v", c)
		}
	}
}
