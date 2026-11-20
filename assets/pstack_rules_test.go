package assets

import (
	"io/fs"
	"regexp"
	"strings"
	"testing"
)

func TestPrinciplesSkillIndexesOneFilePerPrincipleAndEveryCitedNameHasOne(t *testing.T) {
	index := retroLeadRead(t, "plugin/skills/principles/SKILL.md")
	names := regexp.MustCompile("\\| `([a-z-]+)` \\| (Core|Architecture|Verification|Delegation|Meta) \\|").FindAllStringSubmatch(index, -1)
	if len(names) != 24 {
		t.Fatalf("principles index lists %d principles, want 24", len(names))
	}
	for _, m := range names {
		b, err := fs.ReadFile(Files, "plugin/skills/principles/"+m[1]+".md")
		if err != nil {
			t.Errorf("principle %s has no file: %v", m[1], err)
			continue
		}
		if n := strings.Count(string(b), "\n"); n > 15 {
			t.Errorf("principle %s is %d lines, cap 15", m[1], n)
		}
	}
	for _, s := range []string{"`principles:`", "`Principles:` line", "Adapted from cursor/plugins pstack (MIT)"} {
		if !strings.Contains(index, s) {
			t.Errorf("principles: missing %q", s)
		}
	}
	for _, p := range []string{"plugin/skills/lead/SKILL.md", "plugin/skills/task-agent/SKILL.md", "plugin/skills/lead/reference.md"} {
		text := retroLeadRead(t, p)
		for _, m := range regexp.MustCompile("`([a-z]+(?:-[a-z]+)+)`").FindAllStringSubmatch(text, -1) {
			if strings.HasPrefix(m[1], "test-behavior") || strings.HasPrefix(m[1], "prove-it") || strings.HasPrefix(m[1], "attack-the") || strings.HasPrefix(m[1], "never-block") {
				if _, err := fs.ReadFile(Files, "plugin/skills/principles/"+m[1]+".md"); err != nil {
					t.Errorf("%s cites principle %s, which has no file", p, m[1])
				}
			}
		}
	}
}

func TestPracticesStatesNoCommentsRuleWithKeptKindsAndEnforcers(t *testing.T) {
	retroLeadWant(t, "plugin/skills/practices/SKILL.md",
		"## No comments", "license or legal headers", "an app's or service's own", "`comment-check` hook",
		"`react-hooks/exhaustive-deps`", "`note comment-added`", "<slug>-practices.md")
	for _, f := range []string{"typescript.md", "react.md", "go.md", "java.md"} {
		text := retroLeadRead(t, "plugin/skills/practices/"+f)
		if !strings.Contains(text, "| Rule | Enforced by |") {
			t.Errorf("practices/%s: no Rule / Enforced by table", f)
		}
	}
	retroLeadWant(t, "plugin/skills/task-agent/SKILL.md", "No comments: only the kinds `tix:practices` keeps", "No `note comment-added` line")
	retroLeadWant(t, "plugin/skills/lead/SKILL.md", "`note comment-added` means the task added comments: `partial`")
	retroLeadWant(t, "plugin/skills/wave-review/SKILL.md", "trailing ones included")
}

func TestMustFixNeedsAProvingRunAndReviewRecordsWhatItDropped(t *testing.T) {
	retroLeadWant(t, "plugin/skills/wave-review/SKILL.md",
		"**A must-fix needs a run that proves it**", "never a must-fix", "`## Dropped`", "confirmed by a run")
	tpl, err := fs.ReadFile(Scaffold, "vault-scaffold/templates/review.md")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(tpl), "## Dropped") {
		t.Error("review template: no ## Dropped section")
	}
}

func TestAcceptanceRowsHaveSurfaceScenarioPredicateAndTasksReportEach(t *testing.T) {
	plan, err := fs.ReadFile(Scaffold, "vault-scaffold/templates/plan.md")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(plan), "| ID | Criterion | Surface | Scenario | Pass | Evidence |") {
		t.Error("plan template: no acceptance table")
	}
	task, err := fs.ReadFile(Scaffold, "vault-scaffold/templates/task.md")
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{"\nacceptance: []\n", "\nprinciples: []\n"} {
		if !strings.Contains(string(task), s) {
			t.Errorf("task template: missing %q", s)
		}
	}
	retroLeadWant(t, "plugin/skills/lead/SKILL.md", "one `AC-n` row per criterion", "a passing test suite proves no row", "`acceptance: [AC-n]`")
	retroLeadWant(t, "plugin/skills/task-agent/SKILL.md", "Acceptance: <AC-n: pass|fail|inconclusive", "`inconclusive` and the task `partial`, never `done`")
}

func TestReversibleChoicesAreMadeAndOnlyProductPreferenceAndIrreversibleReachUser(t *testing.T) {
	retroLeadWant(t, "plugin/skills/task-agent/SKILL.md", "`Assumed:` line", "Assumed: <choices you made")
	retroLeadWant(t, "plugin/skills/lead/SKILL.md",
		"**Decision gate:** only a product or preference choice, or one that is hard to reverse",
		"A question an experiment can settle gets the experiment", "names its principle")
	retroLeadWant(t, "plugin/skills/lead/reference.md", "the premise they all assumed", "`attack-the-premise`")
	retroLeadWant(t, "plugin/skills/work-ticket/kickoff.md", "is taken without a question", "Anything with two or more plausible values is asked")
}

func TestKickoffRestatesTicketAndScoresItAsPromptBeforeClarify(t *testing.T) {
	retroLeadWant(t, "plugin/skills/work-ticket/kickoff.md",
		"## 3c. Restate and score", "**Restatement**, 3 lines", "**Scorecard**",
		"a done check that can fail", "a theory of the cause stated as fact", "`[done check]`")
}

func TestBugRegressionTestLandsBeforeFixAndRefutedHypothesesLeaveNothing(t *testing.T) {
	retroLeadWant(t, "plugin/skills/diagnose/SKILL.md",
		"Commit that failing test on its own", "revert everything it motivated", "`git log -L`")
	retroLeadWant(t, "plugin/skills/lead/reference.md", "the regression test is its own commit, before the fix's")
	retroLeadWant(t, "plugin/skills/task-agent/SKILL.md", "committed before the fix")
}

func TestPstackNotices(t *testing.T) {
	retroLeadWant(t, "plugin/THIRD_PARTY_NOTICES.md", "## cursor/plugins pstack", "Copyright (c) 2026 Lauren Tan", "`skills/principles`", "`skills/practices`")
}

func TestDesignStepGateArenaPrototypesAndScopeCreepRunBeforeContracts(t *testing.T) {
	retroLeadWant(t, "plugin/skills/design/SKILL.md",
		"**Two shapes.**", "each piece of state has one owner", "**Design review gate:**",
		"`tix:designer`", "`model:` fable, opus and sonnet", "In kiro a spawn takes no model",
		"`role: judge`", "`prototype: true`", "never merged", "## Scope creep",
		"B: split into a new ticket (recommended when the work stands alone",
		"Adapted from cursor/plugins pstack (MIT)")
	retroLeadWant(t, "plugin/skills/lead/SKILL.md", "3b. **Design** (`tix:design`",
		"re-open the design review gate first (`tix:design`, \"Scope creep\")", "`skills: [tix:verify-init]`")
	for _, f := range []string{"vault-scaffold/templates/design.md", "vault-scaffold/templates/task.md"} {
		b, err := fs.ReadFile(Scaffold, f)
		if err != nil {
			t.Fatal(err)
		}
		if f == "vault-scaffold/templates/design.md" && !strings.Contains(string(b), "type: design\n") {
			t.Error("design template: no type: design")
		}
		if f == "vault-scaffold/templates/task.md" && !strings.Contains(string(b), "\nprototype: false\n") {
			t.Error("task template: no prototype field")
		}
	}
}

func TestHandOverWaitsForVerifierOfRowsBlastRadiusAndDecisionTrail(t *testing.T) {
	retroLeadWant(t, "plugin/skills/verify/SKILL.md",
		"`PASS`", "`FAIL`", "`INCONCLUSIVE`", "Never a pass", "## 2. Blast radius", "## 3. Attention",
		"<slug>-verify.md`", "reviewed by <model>")
	retroLeadWant(t, "plugin/skills/verify-init/SKILL.md",
		"## 3. Prove it once", "`target: projects/<slug>`", "`drivers.md`", "Adapted from cursor/plugins pstack (MIT)")
	retroLeadWant(t, "plugin/skills/verify-init/drivers.md", "## HTTP service", "## Web UI (React)", "## CLI or TUI")
	retroLeadWant(t, "plugin/skills/lead/SKILL.md",
		"- **Verify** (`tix:verify`)", "--detach <ws>/tasks/<slug>-verify HEAD", "every row is `PASS` or its gap was accepted at the gate")
	retroLeadWant(t, "plugin/skills/work-ticket/SKILL.md", "\"verification written\"")
}

func TestCommentSickoFindingsLiveInOwnNumberedReviewFileAndPassDispositionGate(t *testing.T) {
	retroLeadWant(t, "plugin/agents/comment-sicko.md",
		"`tix:practices`", "numbered from 51", "reviews/wave-<N>-<slug>-comments.md", "## Dispositions", "You never write code")
	retroLeadWant(t, "plugin/skills/lead/SKILL.md", "one `tix:comment-sicko` per repo")
}

func TestCoachRetroPromptingHabitsFixLadderAndKickoffLastTimeLine(t *testing.T) {
	coach := retroLeadRead(t, "plugin/skills/coach/SKILL.md")
	for _, s := range []string{"disable-model-invocation: true", "## Sharpen: `/tix:coach sharpen <draft>`",
		"Never does the task itself", "a done check that can fail", "Adapted from cursor/plugins pstack (MIT)"} {
		if !strings.Contains(coach, s) {
			t.Errorf("coach: missing %q", s)
		}
	}
	retroLeadWant(t, "plugin/skills/retro/prompting.md",
		"## Prompting (a category of its own)", "· try: <the habit>", "`learning/prompting/<yyyy-MM-dd>-<HHmm>-<ID>-prompting.md`",
		"## The fix ladder", "Show it failing on the real past mistake", "`coach: quiet`")
	retroLeadWant(t, "plugin/skills/retro/SKILL.md", "<ladder level>", "`prompting.md`")
	retroLeadWant(t, "plugin/skills/lead/handover.md", "each placed on the fix ladder", "Prompting note of that file")
	retroLeadWant(t, "plugin/skills/work-ticket/kickoff.md", "**Last time:**", "\"last time: <moment>; try: <habit>\"")
	b, err := fs.ReadFile(Scaffold, "vault-scaffold/AGENTS.md")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "at most one in `learning/prompting/`") {
		t.Error("vault AGENTS.md: retros may not write learning/prompting/")
	}
}

func TestAutopilotShipsAfterHandOverAndNeverMerges(t *testing.T) {
	retroLeadWant(t, "plugin/skills/ship/SKILL.md",
		"never the merge", "`tix ws resign <ID>`", "`createBitbucketRepoPullRequest`", "ready for review (not draft)",
		"conflicts first", "**Review threads:**", "`parentCommentId`", "**CI:**", "`getBitbucketRepoPipelineStepLog`",
		"a fresh `tix:verifier`", "--force-with-lease=<branch>:<sha you last pushed>", "a conflict is theirs",
		"No vault-only IDs, ever", "Adapted from cursor/plugins pstack (MIT)")
	ship := retroLeadRead(t, "plugin/skills/ship/SKILL.md")
	for _, banned := range []string{"`mergeBitbucketRepoPullRequest`", "`runBitbucketRepoPipeline`", "`createBitbucketRepoCommit`"} {
		if strings.Contains(ship, banned) {
			t.Errorf("ship names %s, which it must never call", banned)
		}
	}
	retroLeadWant(t, "plugin/skills/work-ticket/handover.md", "load `tix:ship` and run it now")
	retroLeadWant(t, "plugin/skills/lead/handover.md", "the PO's `tix:ship` tidies, with no question")
	retroLeadWant(t, "plugin/skills/pr-feedback/SKILL.md", "ship pushes your commits and posts the drafted replies itself")
	b, err := fs.ReadFile(Scaffold, "vault-scaffold/templates/board.md")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "\nautopilot: stack\n") {
		t.Error("board template: autopilot isn't stack by default")
	}
}
