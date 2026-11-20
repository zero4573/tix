package assets

import (
	"strings"
	"testing"
)

func TestLeadPicksModelPerTaskKeepsItInTaskFileAndPassesItInSpawn(t *testing.T) {
	retroLeadWant(t, "plugin/skills/lead/SKILL.md",
		"**Model per task** (`models.md`)",
		"write it to the task file's `model:`",
		"copy it into the `Spawn:` entry as `model:`",
		"with the entry's `model:` and `effort:` lines when given")
	retroLeadNot(t, "plugin/skills/lead/SKILL.md", "`effort: low` for `tix:mechanical` work")
	retroLeadWant(t, "plugin/skills/lead/models.md",
		"# Model per task",
		"the task file keeps it for the next lead after a rotation",
		"`haiku`: `tix:mechanical` only",
		"`fable`: critical tasks",
		"(empty → `opus`; `opus` → `fable`)",
		"A `fable` task that fails, or a second failure, goes through the gate.",
		"`tix:reviewer-deep`: fable)",
		"Never `haiku` for `tix:task` or a reviewer",
		"In kiro the subagent runs its agent's own model")
	retroLeadWant(t, "plugin/skills/work-ticket/SKILL.md", "`model` and `effort` if given")

	b, err := Scaffold.ReadFile("vault-scaffold/templates/task.md")
	if err != nil {
		t.Fatal(err)
	}
	tmpl := strings.Join(strings.Fields(string(b)), " ")
	for _, s := range []string{"agent: task model: depends_on", "model: empty (the agent's own) | haiku | opus | fable"} {
		if !strings.Contains(tmpl, s) {
			t.Errorf("templates/task.md: missing %q", s)
		}
	}
}
