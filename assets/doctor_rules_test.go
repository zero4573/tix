package assets

import (
	"io/fs"
	"strings"
	"testing"
)

func TestDoctorProposesOnlyWhatRecursWithEvidenceAndNeverEdits(t *testing.T) {
	doctor := retroLeadRead(t, "plugin/skills/doctor/SKILL.md")
	for _, s := range []string{
		"`tix doctor collect [ID...]`", "Exit 2", "`tix:judge`", "with tix:grade",
		"**Recurring:** the cause shows in 2 or more tickets", "**Watch:** seen in one ticket only",
		"already covers", "fix ladder", "unified diff",
		"`tix-improvements/<yyyy-MM-dd>-<HHmm>-doctor.md`", "`tickets: [<every ID judged>]`",
		"## Scores", "## Tasks", "## Repos", "## Prompting", "## Watch", "## Drafts",
		"quote no secret", "the user deletes it when done",
		"Never edit the plugin, a repo, a skill, a backlog or a ticket, and never file anything.",
	} {
		if !strings.Contains(doctor, s) {
			t.Errorf("doctor: missing %q", s)
		}
	}
	if n := strings.Count(raw(t, "plugin/skills/doctor/SKILL.md"), "\n"); n > 75 {
		t.Errorf("doctor SKILL.md is %d lines, cap 75", n)
	}
	grade := retroLeadRead(t, "plugin/skills/grade/SKILL.md")
	for _, s := range []string{
		"No evidence, no failure.", "Transcripts by search, not whole", "A safety breach sets 0",
		"`lean` 1", "`ship` 1", "`followed` 1", "`clear` 1",
		"0.3 efficiency + 0.3 quality + 0.25 compliance + 0.15 communication",
		"**K1 acceptance:**", "**K8 ship:**", "`prompting`", "## score.md (exact format)",
	} {
		if !strings.Contains(grade, s) {
			t.Errorf("grade: missing %q", s)
		}
	}
	judge := raw(t, "plugin/agents/judge.md")
	for _, s := range []string{"\nmodel: opus\n", "\nskills:\n  - tix:grade\n", "Write only the\n`score.md`"} {
		if !strings.Contains(judge, s) {
			t.Errorf("judge: missing %q", s)
		}
	}
	data, err := fs.ReadFile(Scaffold, "vault-scaffold/AGENTS.md")
	if err != nil {
		t.Fatal(err)
	}
	vault := string(data)
	if !strings.Contains(vault, "`/tix:doctor` writes `<yyyy-MM-dd>-<HHmm>-doctor.md`") {
		t.Error("vault AGENTS.md: doctor notes not allowed in tix-improvements/")
	}
}

func TestRoutineReviewerRunsOnAnotherModelThanTasks(t *testing.T) {
	task := raw(t, "plugin/agents/task.md")
	rev := raw(t, "plugin/agents/reviewer.md")
	model := func(s string) string {
		for _, l := range strings.Split(s, "\n") {
			if strings.HasPrefix(l, "model: ") {
				return strings.TrimPrefix(l, "model: ")
			}
		}
		return ""
	}
	if model(task) == "" || model(task) == model(rev) {
		t.Errorf("tix:task runs %q and tix:reviewer %q: a review must not share the tasks' model", model(task), model(rev))
	}
}

func raw(t *testing.T, path string) string {
	t.Helper()
	b, err := Files.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestWithAutopilotTicketIsDoneWhenPRsAreMerged(t *testing.T) {
	save := retroLeadRead(t, "plugin/skills/save/SKILL.md")
	if !strings.Contains(save, "With `board.md`'s `autopilot: stack` (unset counts as `stack`) keep `review` instead: `tix:ship` sets `done` when every pull request is merged") {
		t.Error("save: sets done under the autopilot before the merge")
	}
	ship := retroLeadRead(t, "plugin/skills/ship/SKILL.md")
	for _, s := range []string{"**Merged or declined:**", "`DECLINED`: the user's call", "When every PR is `MERGED`, set the ticket note's `status: done`", "`status: done`: the PRs are merged; say so and stop."} {
		if !strings.Contains(ship, s) {
			t.Errorf("ship: missing %q", s)
		}
	}
}

func TestDeepReviewRunsOnOtherModelFamiliesAndNeverWaitsForOne(t *testing.T) {
	po := retroLeadRead(t, "plugin/skills/work-ticket/SKILL.md")
	for _, s := range []string{"`tix review kiro <ID> --wave <N> --repo <slug> --range <sha>..<sha> --model all --brief-file <file>`",
		"never hold the wave", "`tix review models` exiting 4 (no broker): skip it silently."} {
		if !strings.Contains(po, s) {
			t.Errorf("work-ticket: missing %q", s)
		}
	}
	lead := retroLeadRead(t, "plugin/skills/lead/SKILL.md")
	for _, s := range []string{"`reviews/wave-N-<slug>-<model>.md`, findings from 101, 201…", "without its command and output counts as a should-fix", "never a reason to wait"} {
		if !strings.Contains(lead, s) {
			t.Errorf("lead: missing %q", s)
		}
	}
	if !strings.Contains(retroLeadRead(t, "plugin/skills/grade/SKILL.md"), "**K9 families:**") {
		t.Error("grade: no K9 families check")
	}
}

func TestHandOverWorkSummaryIsBuiltFromWhatRanAndShippedToPRAndJira(t *testing.T) {
	sum := retroLeadRead(t, "plugin/skills/summary/SKILL.md")
	for _, s := range []string{"Everything you show ran", "`pw-node <script.js>`", "`agg <cast> <gif>`",
		"at most 90 s and 20 MB", "Nothing secret on screen or in text", "`summary/scripts/`",
		"## What changed", "## Try it", "## Screenshots", "## Walkthrough", "## Verification", "## Not shown"} {
		if !strings.Contains(sum, s) {
			t.Errorf("summary: missing %q", s)
		}
	}
	if !strings.Contains(raw(t, "plugin/agents/summarizer.md"), "\nskills:\n  - tix:summary\n") {
		t.Error("summarizer: doesn't preload tix:summary")
	}
	lead := retroLeadRead(t, "plugin/skills/lead/SKILL.md")
	if !strings.Contains(lead, "one `Spawn:` of `tix:summarizer`") || !strings.Contains(lead, "**Hand over** once the summary is written") {
		t.Error("lead: no summary step before the hand-over")
	}
	ship := retroLeadRead(t, "plugin/skills/ship/SKILL.md")
	for _, s := range []string{"## 2b. The work summary on the Jira ticket", "`uploadAttachmentToJiraIssue` (`executeWrite`) with `filePath`", "never print it or write it to a note", "No phase 2",
		"never retry in a loop", "Another source (manual): nothing to post", "Then `## Summary` from `summary/summary.md`"} {
		if !strings.Contains(ship, s) {
			t.Errorf("ship: missing %q", s)
		}
	}
	if !strings.Contains(retroLeadRead(t, "plugin/skills/verify-init/drivers.md"), "Drive with the sandbox's `pw-node <script.js>`") {
		t.Error("drivers: the web UI recipe doesn't use the sandbox's pw-node")
	}
}

func TestMainSessionUsesHostServiceOnlyAndLeavesApprovalsAndMergesToUser(t *testing.T) {
	s := raw(t, "plugin/skills/main/SKILL.md")
	for _, w := range []string{"disable-model-invocation: true", "`tix main list`", "`clean <ID>` (done or closed, session stopped)",
		"waits on the user: leave it alone", "Never ticket work of your own", "Never merge, approve or decline a PR",
		"ask once", "never asked again", "kiro\n  `/tix-ship <ID>`", "a new main resumes from it",
		"`start <ID> --continue` (at most 3\n     restarts a day", "it\n     stopped early", "no must-fix open in `reviews/`",
		"Merge-ready: `stop`", "watches the pull requests itself", "`tix main sync`", "`## Proposed`", "**Finished too early:**"} {
		if !strings.Contains(s, w) {
			t.Errorf("main: missing %q", w)
		}
	}
	if n := strings.Count(s, "\n"); n > 110 {
		t.Errorf("main is %d lines, cap 110", n)
	}
}
