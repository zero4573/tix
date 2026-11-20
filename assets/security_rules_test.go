package assets

import "testing"

func TestSecurityScannersRunBeforeReportAndMergeChainRefusesBlockingFinding(t *testing.T) {
	retroLeadWant(t, "plugin/skills/practices/security.md",
		"tix ws sec-check", "`new`", "`legacy`", "exits 3", "`skip <tool> <reason>`",
		"## Task agents", "## The lead's merge chain", "## Legacy report",
		"**Risk**", "**Difficulty:**", "`easy`: fix it in this ticket", "accept the risk", "// #nosec G204 -- <why>")
	retroLeadWant(t, "plugin/skills/practices/SKILL.md", "## Security (every language)", "`security.md`")
	retroLeadWant(t, "plugin/skills/practices/go.md", "gosec", "govulncheck")
	retroLeadWant(t, "plugin/skills/practices/typescript.md", "eslint-plugin-security", "npm audit")
	retroLeadWant(t, "plugin/skills/practices/java.md", "FindSecBugs", "`osv maven`", "@SuppressFBWarnings")
	retroLeadWant(t, "plugin/skills/practices/security.md", "`osv maven`", "@SuppressFBWarnings", "FindSecBugs")
	retroLeadWant(t, "plugin/skills/task-agent/SKILL.md", "tix ws sec-check -C <worktree>")
	retroLeadWant(t, "plugin/skills/lead/SKILL.md",
		"tix ws sec-check -C <ws>/tasks/<slug>-NN", "the security legacy report", "tix ws sec-check -C <ws>/<slug> <base>")
}
