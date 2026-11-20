# pstack adoption plan

Adapting [pstack](https://github.com/cursor/plugins/tree/main/pstack) (MIT) into the tix ticket
workflow. Goal: `tix start <ID>` takes a ticket as context and ends in high-quality, verified
code, while coaching the user toward better prompting and planning habits.

tix keeps its orchestration (PO, lead, tasks, bounded vault state, `owns`, contracts, merge
chain, `diff-check`, dispositions gate, tidy, signing). pstack adds three things tix lacks:
engineering judgment (principles, design before plan), proof (real-surface verification,
findings proven by a run), and prompter coaching.

## Settled decisions

| Decision | Choice |
|---|---|
| Autopilot default | `autopilot: stack`: verified and ready, the user merges. `off` and `full` are opt-in per ticket or vault. |
| Tidy | Stays before the first push. Fix rounds after the PR opens append commits, never re-tidy. |
| Verification skills | Per repo, but stored in the vault (`projects/<slug>/<slug>-verify.md`; vault note names must be unique), not committed to repos. One generator and shared driver recipes in the plugin. |
| Design review gate | On for `dev` and `epic`, and re-triggered on detected scope creep. |
| Arena | Only for expensive-to-reverse designs (persisted formats, another team's interface, security boundaries). |
| Comments | pstack's no-comments method, enforced by `diff-check` and a per-wave Comment Sicko pass. |
| Blocks | Relaxed to pstack's rule: reversible work proceeds; pause only for irreversible or outward-facing actions. Table below. |
| Force pushes | Allowed only as `--force-with-lease` to the ticket's own feature branch (conflict rebases after the PR opens). Needs andy-nixos changes: `git-ssh-broker` (`modules/apps/git-ssh-broker/src/transport.go`) allows a non-fast-forward only to a branch `workspace.json` names as the ticket's, only with a lease; `sandboxDenyRules` (`modules/apps/claude/claude-sandbox.nix`) narrow to match. Branch and tag deletes, tag moves and every other force push stay refused. |
| Jira follow-up tickets | Batched for one confirmation at hand-over. Vault and manual tickets are filed automatically. |

## Flow

```
tix start <ID>
 ├─ Kickoff: repos, branches, versions (asks only when ambiguous)
 ├─ Restate + ticket scorecard                          coaching
 ├─ Clarify (design tree)
 ├─ Design: data shape, two shapes, arena if costly to reverse,
 │          prototypes settle empirical forks → design review gate
 ├─ Plan: acceptance table with pass predicates, principles per task
 ├─ Waves: tasks → merge chain → Comment Sicko → proof-based review
 │         (scope creep → design delta → design review gate)
 ├─ Verify: fresh tix:verifier runs every predicate; blast radius
 ├─ Hand-over: Attention review by another model; retro with fix ladder + prompting
 └─ Autopilot (stack): tidy → resign → push → PR → babysit → verifier at PR head → stop
```

## Changes by area

### Principles and practices
- `skills/principles/SKILL.md`: one-screen index. Full text in `principles/<name>.md` (files, not
  skills, to keep the session skill list small).
- Task files gain `principles: [...]`, chosen by the lead. The task report gains a
  `Principles:` line naming the choice each one changed. The reviewer checks the task's
  principles plus a core set (subtract before you add, minimize reader load, boundary
  discipline, test behaviour not implementation).
- `practices/{typescript,react,go}.md` in the plugin, loaded by file pattern. One rule per line,
  each paired with what enforces it (an eslint or golangci-lint rule, a type, a test, or "review
  judgment"). TypeScript is seeded from pstack's `typescript-best-practices`; React and Go start
  from rules the user approves. pstack has no React or Go guidance.
- Repo-specific practices go in the vault (`projects/<slug>/<slug>-practices.md`), not in repos.
- Growth: a retro finding seen on two tickets becomes a rule, at the highest enforceable level,
  with the check shown failing on the real past mistake.

### Design (lead, new section 2b)
- For `dev`/`epic` changes that cross a function boundary or add an interface: trace, name the
  data shape, sketch two shapes, pick by one owner per state, one way per task, least caller
  knowledge. Output `design.md` (≤60 lines). Contracts follow from it.
- Arena, only when expensive to reverse: `Spawn:` three `tix:designer` agents (fable, opus,
  sonnet) on one brief; a `tix:reviewer-deep` scores them on a rubric; the lead picks a base and
  grafts.
- Prototype tasks (throwaway branch, never merged) settle questions an experiment can answer.
  Only product and preference choices go to the gate.
- Design review gate: the PO shows a ≤10-line summary before wave 1.
- Scope-creep triggers (each re-opens the gate with a design delta: accept, split into a new
  ticket (recommended when independent), or drop): a new relay request or feature, a task's
  "Outside owns", a new repo, a diff that adds a public interface or contract `design.md` doesn't
  name, a new acceptance criterion, planned tasks growing more than ~50%.

### Plan and tasks
- `plan.md ## Acceptance criteria` becomes a table: criterion · surface · scenario · pass
  predicate · evidence path. Tests alone don't satisfy a row.
- Task "done" = repo checks plus its acceptance check on the matching surface. Inconclusive or
  the wrong surface is `partial`.
- Tests assert observable behaviour. Prefer no new test over a bad one (mostly mocks, mirrors the
  implementation, timing-dependent).
- No comments outside the kept kinds (below).
- Ambiguity: make the reversible choice and log it under `Assumed:` in the report. Block only for
  a contract change.
- Template changes land in `assets/vault-scaffold/` (run `go generate ./assets`). A ticket that
  changes these formats keeps the old ones for itself (`work-ticket/workflow.md`).

### No comments
- Kept kinds only: license headers; non-obvious behaviour forced by something outside our
  control; `prettier-ignore` and suppressions of faulty or style-only rules; doc comments on a
  public API (Go: only packages other repos import); issue or RFC links for constraints code
  can't express. Go directives (`//go:build`, `//go:generate`) are not comments; `//nolint` gets
  the same scrutiny as `eslint-disable`.
- A surprise in our own code: delete the comment, mark the symbol `MUST KILL` (rename, extract,
  type, redesign). Correctness suppressions (e.g. `react-hooks/exhaustive-deps`) are removed and
  fixed. "Do not remove" claims are traced; a real constraint becomes a type, test or lint, then
  the comment goes.
- Enforcement: a `comment-added` note in `tix ws diff-check` (the task rewords its own branch,
  like `commit-style`), and a read-only `tix:comment-sicko` on each wave's merged range before
  review; the lead plans its `MUST KILL` items as fix tasks.

### Review
- A must-fix needs a run that proves it (failing test, repro, output). Unconfirmed suspicions
  become should-fix with a "how to confirm" line. The review file records a `Dropped` count.
- Must-fix only for a failing run at the head or a bug a user would hit.
- Deep waves: a panel of two reviewers on different models (fable, opus) with the brief "break
  this change"; findings with one root cause are merged.

### Bugs (`tix:diagnose`)
- The failing regression test is committed before the fix; tidy keeps the order.
- Revert what a refuted hypothesis motivated.
- Seed hypotheses from the area's git history.

### Verification
- `/tix:verify-init <repo>` (adapted from `create-verification-skill`): reads the repo, writes
  `projects/<slug>/<slug>-verify.md` in the vault (launch, health check, drive, evidence, cleanup, feature
  map, sandbox limits). It must run once end to end before it counts.
- Shared driver recipes in the plugin: Go service over HTTP, React via Playwright, CLI via PTY.
- `/tix:verify-maintain` fixes drift.
- `tix:verifier` (never an author) runs every acceptance predicate at the ticket head before
  hand-over, and again at the PR head: PASS / FAIL / INCONCLUSIVE, evidence in
  `tickets/<src>/<ID>/evidence/`. It also proves the blast-radius fact (the one fact the change
  is safe because of) with a script.

### Hand-over and retro
- Attention review: a model other than the lead's reads `plan.md ## Decisions`, the board and the
  reports, and flags weak evidence, skipped verification and risky choices.
- Retro gains the fix ladder (architecture, types, lint/CI, test, docs last; a new check shown
  failing on the real past mistake) and a Prompting category (below).
- Follow-ups: the lead decides (fix now if cheap, else defer) and reports; the user overrides
  after. Jira tickets to file are batched into one confirmation at hand-over.

### Autopilot (`stack` by default)
- After hand-over: `/tix:tidy` runs automatically, then `tix ws resign` (waits for the user only
  if 1Password asks to approve the signature), push the feature branch, open the PR with the
  `sdlc` template, babysit (conflicts, then threads via `pr-feedback`, then CI), `tix:verifier`
  at the PR head, then stop. `full` also merges, only when the verdict covers the current head
  (`git patch-id`), never on green CI alone, using Bitbucket's "close source branch".
- `save` loses its push questions in autopilot (marks gate, mid-ticket warning, signing) since
  the order is fixed.
- First step of this phase: confirm via the Atlassian MCP `discover` which Bitbucket operations
  exist (create PR, pipeline status, merge). pstack's watcher is GitHub-only.
- andy-nixos: drop or condition the `Bash(tix ws rm:*)` deny (`modules/apps/tix.nix`) so
  worktrees can be removed after merge; the broker and deny-rule change for force-with-lease
  (Settled decisions). A conflict rebase pushes with `--force-with-lease=<branch>:<last pushed sha>`.

### Blocks, re-evaluated

| Block | Verdict |
|---|---|
| Task stops on any major-tradeoff ambiguity | Relax: decide, log `Assumed:`; stop only for contract changes. |
| Lead gate on every major tradeoff | Narrow: prototype empirical forks; gate only product, preference, irreversible. |
| Fix loops go to the user | Replace: attack the premise first; ask only if it doesn't settle it. |
| Follow-up A–D question, never file tickets | Relax: lead decides; Jira tickets batched for one confirmation at hand-over. |
| Kickoff always asks repos, branch, version | Relax: ask only when ambiguous. |
| Never push / never force | Relax in autopilot: push the feature branch; force only with a lease to the ticket's own branch. |
| `tix ws rm` denied | Relax after merge. |
| `branch -D`, `reset --hard`, `git clean` | Keep denied. |
| Clarify confirm; one lead; `owns`; frozen contracts | Keep (structure). |
| Note deletion, paid Rovo calls, tags | Keep (data, cost, releases). |
| Environment failure stops the task | Keep (fix the gap, don't hide it). |

### Coaching
- Kickoff restatement: goal, smallest change that meets it, done check, confirmed before clarify.
- Ticket scorecard: goal, a done check that can fail, the proof to show, known context, real
  constraints; flags a theory of the cause up front, "how" instead of "what", a vague "done".
  Clarify tags each question with the gap it fills; the sharpened ticket goes in
  `plan.md ## Scope`.
- Gate recommendations name the principle behind them, building a one-line steering vocabulary.
- `/tix:coach <question>` (which skill, what to type) and `/tix:coach sharpen <draft>` (rewrite
  deeper, not bigger, with each change explained). Never does the task.
- Retro Prompting category: moments where input caused rework, one habit to try each, kept in
  the vault's `learning/prompting/` (`tix:teach` format). The next kickoff shows one line: "last
  time: X; try: Y".
- `coach: on | quiet` in the vault config.

### Not adopted
`poteto-mode` router and playbooks (ticket types route), Cursor-only pieces (`setup-pstack`,
`/loop`, Bugbot, automations), cross-vendor panels (Claude-only here), `statechart` and formal
verification (revisit for concurrency-heavy repos), the `show-me-your-work` TSV (the vault files
are the trail), `ketchup`, `bro`, `make-bot-ui`.

## Status (2026-11-18)

- Phases 1, 2 and 4: done on `feature/pstack-phase-1` (skills, agents, templates, `diff-check`'s
  `comment-added` note, kiro rendering); not yet run on a real ticket.
- Phase 3: done in tix (`tix:ship`, `autopilot: stack` in the board template). Bitbucket writes
  come from a token with `write:bitbucket:agent-interface`; andy-nixos's `mcp-guard` (branch
  `feature/sandbox-proxy-mcp-guard`) refuses every write operation off its allowlist (merges,
  declines, approvals, API commits and branches, pipeline runs), and the git-ssh-broker change
  lets a session rewrite only its own commits on `feature/*` branches.

- Phase 5 (from Warp Factories and skill-doctor): done. `tix:reviewer` runs on opus, apart from
  the tasks' sonnet; `/tix:doctor` (`tix doctor collect`, `tix:judge` with `tix:grade`) grades
  finished tickets and turns causes that recur into `tix-improvements/` tasks; `evals/` holds
  behavioural evals of the plugin (`make eval`, `claude plugin eval`); with the autopilot a
  ticket is `done` only when its PRs are merged.
- Cross-family deep reviews: done. andy-nixos's `review-broker` (branch
  `feature/kiro-review-broker`) runs `tix-reviewer-deep` on kiro models of other families
  (`gpt-5.6-sol`, `glm-5`) for a Claude session's `tix review kiro`; a skipped model never holds
  a wave. Not yet run against real kiro.
- Main session: `tix main` and `/tix:main` (a host service over herdr, `internal/conductor`, which
  also wakes main when a ticket session finishes a turn or exits): sync, queue, restarts, the
  hand-over check, `/tix:ship`, PR watching with the ticket session stopped, closing merged
  tickets (asked once each by default). Tested with a fake herdr; not yet run on real sessions.

## Phases

1. **Prompt-only:** principles and practices files, task `principles:`, review proof rule, no
   comments rule and `diff-check` note, restatement and scorecard, acceptance table, bug commit
   order, relaxed blocks in tix. Done when one `dev` and one `bug` ticket run through it and their
   retros show fewer fix waves and unconfirmed must-fixes than recent tickets.
2. **New agents:** design step (`tix:designer`, prototypes, gate, scope-creep triggers),
   `verify-init` and `tix:verifier`, `tix:comment-sicko`, Attention review. Done when a verifier
   FAIL has blocked a hand-over and `verify-init` has run end to end in the sandbox for one repo.
3. **Autopilot `stack`:** Bitbucket operations confirmed, save changes, babysit loop, andy-nixos
   changes. Done when one ticket goes from `tix start` to a verified open PR with no manual step
   except signature approval.
4. **Habits loop:** `/tix:coach`, retro Prompting category and `learning/prompting/`, fix ladder.
   Done when a kickoff shows a "last time / try" line from an earlier retro.

5. **Measure and improve:** the reviewer on its own model, `/tix:doctor`, plugin evals, done on
   merge. Done when a doctor run over at least four tickets has produced a task that was applied
   and the next run no longer shows its cause, and `make eval` passes with a positive delta
   against the no-plugin baseline on every case. Evals cover single skills on small fixtures:
   `claude plugin eval` caps a case at 200 turns and an hour, has no scripted user replies, and
   doesn't document background agents, so a whole ticket can't be one case.

pstack is MIT: add a section to `assets/plugin/THIRD_PARTY_NOTICES.md` and an "Adapted from"
line on each adapted skill.
