package conductor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeNote(t *testing.T, s *Server, id, fm string) {
	t.Helper()
	d := filepath.Join(s.Vault, "tickets", "jira", id)
	if err := os.MkdirAll(d, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(d, id+".md"), []byte("---\n"+fm+"---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestStartGuard(t *testing.T) {
	s, f := setup(t)
	f.agents = nil
	writeNote(t, s, "PROJ-5", "status: in-progress\nticket-type: epic\n")
	writeNote(t, s, "PROJ-6", "status: todo\ncovered-by: \"[[PROJ-5]]\"\n")
	writeNote(t, s, "PROJ-7", "status: todo\nignore: true\nignore-reason: not ours\n")
	r := do(t, s, Request{Op: "start", ID: "PROJ-6"})
	if r.Status != "refused" || !strings.Contains(r.Detail, "covered by PROJ-5 (epic, in-progress)") ||
		!strings.Contains(r.Detail, "start PROJ-6 --force") {
		t.Errorf("covered: %+v", r)
	}
	if r := do(t, s, Request{Op: "start", ID: "PROJ-7"}); r.Status != "refused" || !strings.Contains(r.Detail, "ignore: true (not ours)") {
		t.Errorf("ignored: %+v", r)
	}
	for _, c := range f.calls {
		if strings.Contains(strings.Join(c, " "), "workspace create") {
			t.Errorf("a refused start opened a herdr workspace: %v", c)
		}
	}
	if r := do(t, s, Request{Op: "start", ID: "PROJ-6", Force: true}); r.Status != "ok" {
		t.Fatalf("forced: %+v", r)
	}
	ran := false
	for _, c := range f.calls {
		if l := strings.Join(c, " "); strings.Contains(l, "pane run") && strings.HasSuffix(l, " start --force PROJ-6") {
			ran = true
		}
	}
	if !ran {
		t.Errorf("tix start without --force: %v", f.calls)
	}
	writeNote(t, s, "PROJ-5", "status: done\n")
	writeNote(t, s, "PROJ-8", "status: todo\ncovered-by: \"[[PROJ-5]]\"\n")
	if r := do(t, s, Request{Op: "start", ID: "PROJ-8"}); r.Status != "ok" {
		t.Errorf("covered by a done epic: %+v", r)
	}
}
