package note

import (
	"os"
	"path/filepath"
	"testing"
)

func TestStartGuardAndList(t *testing.T) {
	v := t.TempDir()
	for id, fm := range map[string]string{
		"PROJ-1": "status: in-progress\nticket-type: epic\n",
		"PROJ-2": "status: todo\ncovered-by: \"[[PROJ-1]]\"\n",
		"PROJ-3": "status: todo\nignore: true\n",
		"PROJ-4": "status: todo\n",
		"PROJ-5": "status: todo\ncovered-by: \"[[PROJ-9]]\"\n",
	} {
		d := filepath.Join(v, "tickets", "jira", id)
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(d, id+".md"), []byte("---\n"+fm+"---\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for id, want := range map[string][2]string{
		"PROJ-2": {"covered by PROJ-1 (epic, in-progress)", "PROJ-1"},
		"PROJ-3": {"marked ignore: true", ""},
		"PROJ-4": {"", ""},
		"PROJ-5": {"covered by PROJ-9 (lead, -)", "PROJ-9"},
	} {
		if why, lead := StartGuard(v, id); why != want[0] || lead != want[1] {
			t.Errorf("%s: %q %q, want %q %q", id, why, lead, want[0], want[1])
		}
	}
	got := map[string]string{}
	for _, tk := range List(v, false) {
		got[tk.ID] = tk.Status
	}
	if got["PROJ-2"] != "todo, covered by PROJ-1" || got["PROJ-3"] != "todo, ignored" || got["PROJ-4"] != "todo" {
		t.Errorf("List: %v", got)
	}
}
