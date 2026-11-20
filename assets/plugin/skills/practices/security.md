# Security checks

`tix ws sec-check -C <checkout> [--all] <since>` runs the scanners that apply (gitleaks for
secrets; gosec and govulncheck per Go module; npm audit per `package-lock.json`) and prints
one line per finding, `<new|legacy>  <block|warn>  <tool>  <severity>  <where>  <rule>  <message>`:
`new` came in with the commits `<since>..HEAD`, `legacy` was already there (only with `--all`).
It exits 3 when a `new` line says `block`. Every line the range adds with `#nosec`,
`gitleaks:allow` or `nolint:gosec`, and any change to `.gitleaks.toml`/`.gitleaksignore`, is a
`new  warn  suppression` line: the review judges each one. govulncheck's `new` is a changed
`go.mod` line or an added call on the way to the vulnerable function; the standard library's only
counts as new with a changed `go`/`toolchain` line (its fix is a toolchain bump: the legacy report). A `skip  <tool>  <reason>` line (not installed, no
network for the vulnerability database or the registry) never blocks: name it once in your
report or on the board line. The repo's own lint carries the language rules (`go.md`,
`typescript.md`: gosec's rules through golangci-lint, `eslint-plugin-security`).

## Task agents
Before you report, with your task's fork point as `<since>`
(`git merge-base HEAD <ticket-branch>`): `tix ws sec-check -C <worktree> <since>`. Every `new`
line, `warn` too, is yours: fix it (the vulnerable dependency bumped to its fixed version, the
secret removed from the branch, which an unpushed task branch allows, and named in the report
for rotation; the code made safe). A finding that is wrong for this code gets the narrowest
suppression with its reason, which the review judges: gosec `// #nosec G204 -- <why>` on the
line, gitleaks `gitleaks:allow` on the line or its fingerprint in `.gitleaksignore`, never a
blanket exclusion. Put the run under `Checks:` (`sec-check: exit <rc>`, with any skip lines).

## The lead's merge chain
Right after `tix ws diff-check`, `tix ws sec-check -C <ws>/tasks/<slug>-NN $(git merge-base HEAD
task/<ID>-NN-<task-slug>)`: exit 3 → don't merge, mark the line `partial` with the blocking
lines and ask a follow-up that fixes them (or suppresses one with its reason); `warn` lines go to
the next review's prompt. At verify, once for the whole ticket: `tix ws sec-check -C <ws>/<slug>
<base>` (it catches your own commits); a blocking line is a fix task like a `FAIL` row.

## Legacy report
When you write the first `plan.md`, per repo: `tix ws sec-check -C <ws>/<slug> --all HEAD` (every
finding is legacy then). Write `tickets/<source>/<ID>/security.md`: one row per finding or group
(all of a toolchain's or a module's advisories are one row: "Go 1.26.0 → 1.26.11, 32 stdlib
advisories"), with `| Finding | Where | Risk | Difficulty | Proposal |`:
- **Risk** in this repo's context: `high` (reachable from untrusted input, a live secret, a
  called vulnerable function on a served path), `medium`, `low` (test-only, unreachable, a CLI
  that runs its own user's input).
- **Difficulty:** `easy` (a patch or minor bump whose checks pass unchanged, a one-place code fix
  under ~20 lines with no behaviour or API change), `moderate`, `hard` (a major bump, a design
  change, a secret that needs rotating by its owner).
- `easy`: fix it in this ticket, a task of wave 1 (`fixes:` naming the row), no question.
- Otherwise: one gate question for all of them, each with its risk, difficulty and what the
  patch involves; options: fix it in this ticket, a follow-up ticket, or accept the risk (left
  as is); recommend by risk. The answer goes into the row's `Proposal`.
A repo whose lint doesn't wire the language's security rules (gosec, `eslint-plugin-security`) is
a row too: `moderate`, it adds a dev dependency or lint config.
