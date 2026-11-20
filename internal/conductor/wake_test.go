package conductor

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeMain(t *testing.T, path, tickets string) {
	t.Helper()
	body := "---\nmax-running: 3\n---\n## Queue\n- PROJ-9 · - · later\n## Tickets\n" + tickets + "## Log\n- 2026-11-18 PROJ-3 · merged\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func setStatus(t *testing.T, s *Server, id, status string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(s.Vault, "tickets", "jira", id, id+".md"), []byte("---\nstatus: "+status+"\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestWakerEvents(t *testing.T) {
	s, f := setup(t)
	f.agents = []agentRow{
		{"claude", "working", filepath.Join(f.work, "PROJ-1"), "w1:p1", "w1"},
		{"claude", "working", filepath.Join(f.work, "PROJ-2"), "w2:p1", "w2"},
	}
	f.main = "idle"
	mainFile := filepath.Join(t.TempDir(), "main.md")
	writeMain(t, mainFile, "- PROJ-1 · working · claude · since 2026-11-18\n- PROJ-2 · shipping · claude · since 2026-11-18\n")
	w := &Waker{S: s, Pane: "w0:p1", Command: "/tix:main watch", MainFile: mainFile,
		Debounce: time.Minute, IdleAfter: 30 * time.Minute}
	t0 := time.Now()
	w.lastWake = t0
	ctx := context.Background()
	step := func(d time.Duration) string { return w.Step(ctx, t0.Add(d)) }

	if got := step(2 * time.Minute); got != "" {
		t.Errorf("first poll woke main: %q", got)
	}
	f.agents[0].Status = "idle"
	if got := step(3 * time.Minute); got != "" {
		t.Errorf("a working ticket's turn woke main: %q", got)
	}
	f.agents[0].Status = "working"
	step(4 * time.Minute)
	f.agents[1].Status = "idle"
	f.main = "working"
	if got := step(5 * time.Minute); got != "" {
		t.Errorf("woke a working main: %q", got)
	}
	f.main = "idle"
	if got := step(6 * time.Minute); got != "/tix:main watch PROJ-2 finished a turn" {
		t.Errorf("shipping turn: %q", got)
	}
	f.agents[0].Status = "idle"
	if got := step(10 * time.Minute); got != "" {
		t.Errorf("idle 4m woke main: %q", got)
	}
	if got := step(41 * time.Minute); got != "/tix:main watch PROJ-1 idle 30m0s" {
		t.Errorf("idle spell: %q", got)
	}
	if got := step(80 * time.Minute); got != "" {
		t.Errorf("same idle spell told twice: %q", got)
	}
	setStatus(t, s, "PROJ-1", "review")
	f.agents = append(f.agents, agentRow{"claude", "working", filepath.Join(f.work, "PROJ-3"), "w3:p1", "w3"})
	if got := step(82 * time.Minute); got != "/tix:main watch PROJ-1 status review" {
		t.Errorf("status change: %q", got)
	}
	f.agents = f.agents[:2]
	if got := step(84 * time.Minute); got != "" {
		t.Errorf("the user's session exiting woke main: %q", got)
	}
	s.stopped = map[string]bool{"PROJ-2": true}
	f.agents = f.agents[:1]
	if got := step(86 * time.Minute); got != "" {
		t.Errorf("a session main stopped woke main: %q", got)
	}
	f.agents = nil
	if got := step(88 * time.Minute); got != "/tix:main watch PROJ-1 exited" {
		t.Errorf("exit: %q", got)
	}
	if got := step(10 * time.Hour); got != "" {
		t.Errorf("woke with no event: %q", got)
	}
	w.Tick = time.Hour
	if got := step(11 * time.Hour); got != "/tix:main watch" {
		t.Errorf("tick: %q", got)
	}
}

func TestWakerSyncAndPRs(t *testing.T) {
	s, f := setup(t)
	f.agents = nil
	f.main = "idle"
	ci := "#4 COMPLETED/SUCCESSFUL"
	s.PRs = func(_ context.Context, r PRRef) (PRState, error) {
		return PRState{State: "OPEN", Updated: "t1", CI: ci}, nil
	}
	if r := do(t, s, Request{Op: "watch", ID: "PROJ-2", PRs: []string{"https://bitbucket.org/acme/api/pull-requests/12"}}); r.Status != "ok" {
		t.Fatalf("watch: %+v", r)
	}
	w := &Waker{S: s, Pane: "w0:p1", Command: "/tix:main watch", MainFile: filepath.Join(t.TempDir(), "none.md"),
		Debounce: time.Minute, PRPoll: 10 * time.Minute, SyncEach: time.Hour}
	t0 := time.Now()
	w.lastWake, w.lastPR, w.lastSync = t0, t0, t0
	ctx := context.Background()
	w.Step(ctx, t0.Add(time.Minute))
	if got := w.Step(ctx, t0.Add(11*time.Minute)); got != "" {
		t.Errorf("an unchanged PR woke main: %q", got)
	}
	ci = "#5 COMPLETED/FAILED"
	if got := w.Step(ctx, t0.Add(22*time.Minute)); got != "/tix:main watch PROJ-2 PR api/#12 CI #5 COMPLETED/FAILED" {
		t.Errorf("CI change: %q", got)
	}
	if got := w.Step(ctx, t0.Add(33*time.Minute)); got != "" {
		t.Errorf("the same change told twice: %q", got)
	}
	f.onTix = func(args []string) {
		if args[0] == "sync" {
			d := filepath.Join(s.Vault, "tickets", "jira", "PROJ-7")
			_ = os.MkdirAll(d, 0o755)
			_ = os.WriteFile(filepath.Join(d, "PROJ-7.md"), []byte("---\nstatus: todo\n---\n"), 0o644)
		}
	}
	if got := w.Step(ctx, t0.Add(61*time.Minute)); got != "/tix:main watch sync new tickets PROJ-7" {
		t.Errorf("sync: %q", got)
	}
	if r := do(t, s, Request{Op: "unwatch", ID: "PROJ-2"}); r.Status != "ok" || len(s.watched()) != 0 {
		t.Errorf("unwatch: %+v %v", r, s.watched())
	}
}

func TestWatchRefusals(t *testing.T) {
	s, _ := setup(t)
	s.PRs = func(context.Context, PRRef) (PRState, error) { return PRState{State: "OPEN"}, nil }
	for _, c := range []struct {
		prs  []string
		want string
	}{
		{nil, "give 1 to"},
		{[]string{"https://github.com/a/b/pull/1"}, "not a Bitbucket pull request"},
		{[]string{"acme/api#0"}, "bad pull request number"},
	} {
		if r := do(t, s, Request{Op: "watch", ID: "PROJ-1", PRs: c.prs}); r.Status != "refused" || !strings.Contains(r.Detail, c.want) {
			t.Errorf("%v: %+v", c.prs, r)
		}
	}
	s.PRs = nil
	if r := do(t, s, Request{Op: "watch", ID: "PROJ-1", PRs: []string{"acme/api#3"}}); r.Status != "failed" {
		t.Errorf("no Bitbucket: %+v", r)
	}
}

func TestChangesAndChecks(t *testing.T) {
	old := PRState{State: "OPEN", Updated: "a", CI: "#1 COMPLETED/SUCCESSFUL"}
	for cur, want := range map[PRState]string{
		old: "",
		{State: "OPEN", Updated: "b", CI: old.CI}:                        "updated (comments, commits or approvals)",
		{State: "MERGED", Updated: "b"}:                                  "state MERGED",
		{State: "OPEN", Updated: "b", CI: old.CI, Checks: "git(behind)"}: "merge checks git(behind)",
	} {
		if got := changes(old, cur); got != want {
			t.Errorf("changes(%+v) = %q, want %q", cur, got, want)
		}
	}
	var cs []mergeCheck
	for _, k := range []string{"minimum_approvals", "failed_builds", ""} {
		c := mergeCheck{Type: "standard_merge_check", Status: "FAILED"}
		c.Check.Kind = k
		if k == "" {
			c.Type, c.Reason = "git_mergeability_check", "conflicts"
		}
		cs = append(cs, c)
	}
	if got := failingChecks(cs); got != "git_mergeability_check(conflicts)" {
		t.Errorf("failingChecks: %q", got)
	}
}

func TestStopCleansTasks(t *testing.T) {
	s, f := setup(t)
	f.agents = nil
	if r := do(t, s, Request{Op: "start", ID: "PROJ-1"}); r.Status != "ok" {
		t.Fatalf("start: %+v", r)
	}
	f.agents[0].Status = "idle"
	r := do(t, s, Request{Op: "stop", ID: "PROJ-1"})
	if r.Status != "ok" || !strings.Contains(r.Output, "tix ws task-clean: ran ws task-clean PROJ-1") ||
		!strings.Contains(r.Output, "tix ws tidy-clean: ran ws tidy-clean PROJ-1") {
		t.Errorf("stop: %+v", r)
	}
	if !s.wasStopped("PROJ-1") || s.wasStopped("PROJ-1") {
		t.Errorf("wasStopped should be true once")
	}
}
