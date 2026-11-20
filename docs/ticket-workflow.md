# The ticket workflow

`tix start <ID>` opens a session that runs `/tix:work-ticket <ID>`. That session is the
product owner (PO). It talks to you, runs one lead agent, and spawns every agent the lead
asks for, because subagents cannot spawn agents. The lead plans in waves of small tasks.
Each task runs in its own worktree under `tasks/` on its own branch, and the lead merges
the finished branches into the ticket branch.

```mermaid
sequenceDiagram
  participant U as you
  participant P as PO (work-ticket)
  participant L as lead
  participant T as task agents
  participant R as reviewers
  U->>P: tix start ID
  P->>P: resume from board.md, or kickoff: repos, base, version, tix ws add, tix ws refresh
  P->>U: clarify (tix:clarify): rounds of questions with a recommended answer
  P->>L: spawn (plan.md, contracts/, tasks/, board.md)
  L-->>P: Spawn: wave of tasks
  P->>T: one agent per task, in parallel (up to 4)
  T-->>P: done, with a Report in the task file
  P->>L: wake once the wave is done
  L->>L: merge task branches (--no-ff), run lint and tests
  L-->>P: Spawn: reviewers
  P->>R: one review per repo
  R-->>P: reviews/wave-N-slug.md
  P->>L: wake: triage findings
  L-->>P: fix tasks or the next wave
  L-->>P: hand-over: retro, follow-ups, status review
  P-->>U: ready for your review
```

## Who writes what

- The PO owns `board.md`'s `lead:` and `po:` fields and `relay.md`, the channel between you,
  the PO and the lead. It answers status from `board.md`. While a wave runs it holds
  non-urgent requests and feedback and hands them to the lead with the wave-finished
  message; only urgent ones wake the lead mid-wave.
- The lead owns `plan.md`, `contracts/`, `tasks/` and the board rows, merges branches, and
  ends each run with a block the PO acts on (`Spawn:`, `Ask user:`, `Status:`, `Next:`).
- A task agent writes only the paths its task file `owns`, commits in its task worktree and
  fills the task's `## Report` (at most 40 lines).
- Reviewers write one file per repo under `reviews/`, with capped must-fix, should-fix and
  consider lists. A small wave (under about 150 changed lines, no contract or
  security-sensitive code) gets no reviewer: the lead checks the diff itself or leaves it
  for the next wave's review; the last wave before hand-over is always reviewed.

## Parallel tasks and the lead skill

At most 4 agents run at once. Tasks whose `owns` overlap never share a wave. Concurrent
tasks in one Go package get distinct name prefixes for their helpers and `Test` functions
(set in each task file), so the names cannot clash when the lead merges the branches.

The lead's `SKILL.md` keeps the playbook short. Detail read at one moment sits in
`reference.md` next to it, under headings `SKILL.md` points to: the board-line recipe, the
review disposition formats, the gate script that checks every review finding has a
disposition, and how each ticket type shapes the plan.

## Rules the skills enforce

- **Reports.** A task's `## Report` lists `Commits:` (one line per commit) and a `Checks:`
  line with the repo's full check command, its exit code and the short sha it ran at, after
  the last commit. A skipped check makes the task `partial`, never `done`. The PO checks that
  each finished task's report has content and flags the empty ones `NO REPORT` to the lead,
  who marks them `partial` and asks again instead of doing the work.
- **Acceptance rows.** `plan.md ## Acceptance criteria` is a table: each `AC-n` row has a
  surface, a scenario and a pass predicate that can fail, and a passing test suite proves no
  row. A task's `acceptance:` names the rows it proves; its report's `Acceptance:` line gives
  each `pass`, `fail` or `inconclusive`, and anything but `pass` makes it `partial`.
- **Principles and practices.** A task's `principles:` names 1-4 rules of `tix:principles`; its
  report's `Principles:` line says what each changed. `tix:practices` holds the TypeScript,
  React and Go rules, each with what enforces it, and the no-comments rule; `diff-check`'s
  `note comment-added` sends added comments back to the task.
- **Reversible choices.** A task decides what is reversible inside its `owns` and records it on
  an `Assumed:` line; the lead's gate stops only for product, preference and hard-to-reverse
  choices, settles empirical questions by experiment, and tests a failed fixes' shared premise
  before asking. Follow-ups are the lead's call (manual tickets filed with `tix new`); the user
  only picks which also go to the ticket's source, and decides a must-fix's deferral.
- **Design first.** A change across function boundaries gets `design.md` (`tix:design`) and a
  design review gate before any contract or task file; an expensive-to-reverse design runs an
  arena of three `tix:designer` agents and a judge; `prototype: true` tasks answer
  experiment-shaped questions and are never merged. Scope creep (a new request, "Outside owns",
  a new repo or interface or acceptance row, tasks ~50% over plan) re-opens the gate.
- **Verified before hand-over.** A `tix:verifier` runs every acceptance row on its surface in a
  detached verification worktree, using the repo's verify note (`/tix:verify-init`), proves the
  blast-radius fact and reviews the decision trail into `verification.md`; FAIL rows become fix
  tasks and hand-over waits for every row to pass or its gap to be accepted at the gate.
- **Comment Sicko.** Code waves get a `tix:comment-sicko` beside the reviewer; its findings
  (numbered from 51) sit in `reviews/wave-N-<slug>-comments.md` and go through the same
  dispositions gate.
- **Reviews need proof.** A must-fix is confirmed by a run (the command and its output); an
  unconfirmed suspicion is a should-fix with a way to confirm it, and `## Dropped` counts the rest.
- **Task front matter.** `db: none|heavy`: a `heavy` task runs DB suites on a shared stack,
  so at most one runs per repo stack at a time. `fixes:` lists the backlog items and review
  findings a task closes; at merge the lead moves each backlog line to closed and marks the
  finding `resolved`.
- **Test DB.** The ticket's `conventions.md` keeps the DB environment under `## Test DB`. The
  lead passes it to reviewers, who run the DB suites with it; a skipped DB test is a
  must-fix finding when the ticket has that section or a wave task is `db: heavy`.
- **Task agents.** They install dependencies before checks (for example `npm ci` when there
  is a lockfile and no `node_modules`) and run helper scripts from their own worktree with
  worktree-relative paths, never from a main clone.
- **Commit style.** `board.md`'s `commit-style:` (`conventional`, `repo` or `ticket`) wins
  over the workspace `CLAUDE.md`, which is written before the kickoff chooses.
- **After a sandbox or container wipe.** If task worktrees or the test stack are gone, the
  PO tells the lead, and every `running` task is marked `orphaned`; the lead checks their
  branches before replanning.

## Checking a range for binary and large files

`tix ws diff-check [-C <dir>] [--max-size 1M] [--ticket <ID>] <rev-range>` walks every non-merge commit of a
range and flags each file the commit adds or modifies that git treats as binary or whose
blob is over the size limit (bytes, or a number with K, M or G; default 1M). Deletions are
ignored, so a file added and removed within the range is still reported. Each flagged file is
one stdout line, tab separated: `binary`, `large` or `binary,large`, the size in bytes, the
short sha and the path. Exit 0 means clean, 3 means something was flagged (with a count on
stderr), 1 means a usage error, a directory that is not a git checkout or a bad range.

For `A..B` ranges it also flags `spec-id` lines for duplicate requirement IDs (see [the tix ws commands](workspace-commands.md#diff-check)).

It also prints three informational line kinds that never change the exit code: `note test-changed <name> <short sha> <path>` when a commit changes or removes a Go `Test*`, `Benchmark*`, `Fuzz*` or `Example*` func that its first parent had (or, with name `-`, an existing `testdata/**/*.txtar` file), `note comment-added <lines> <short sha> <path>` when a commit adds whole-line comments that the no-comments rule doesn't keep, in any `//` or `#` language, and `note commit-style <style> <short sha> :commit-message` (with `--ticket`) for a subject that misses the ticket's commit style. With `commit-style: ticket` the ticket's own ID in a subject is not a `vault-id` hit.

With `--ticket <ID>` it also flags the ticket's own vault-only IDs that a commit introduces. The
set is the ID itself, the `R-nn` of the relay's entry lines (`R-nn · …`), the `B-nn` of the
backlog's item lines (`- [ ] B-nn · …`) and the finding IDs of the finding and disposition lines
of `reviews/*.md`; an ID mentioned only in prose is not in it. A commit message is flagged for
any whole-word occurrence. A file is flagged for an ID that occurs more often in the commit's
added lines of it than in its removed ones (binary files skipped), so a line kept from the base,
even reworded, passes, and a second copy of it doesn't. Each hit is a `vault-id`, the ID, the
short sha and the path (`:commit-message` for a message) line, and exit 3 applies. Paths are
raw, whatever characters they hold. Lines come per commit in `rev-list` order, a commit's size
lines before its ID lines. A generic `R-9090` that the ticket's relay doesn't have passes. A
ticket that is not in the vault is a usage error (exit 1). The skills treat a hit in a fixture
that models a ticket as a note, not a must-fix.

The same `--ticket` run flags test names built from the ticket's waves, whose numbers come from
the board's task lines (`· w<N> ·`, in `board.md` and `board-archive.md`) and the review files'
names (`reviews/wave-<N>-*.md`): an identifier like `TestW116DeleteConflict`, `test_w3_retries`
or `testW3` that a commit adds to a test file (`*_test.go`, `test_*.py`, `*.test.ts`,
`*.spec.ts`, `*_spec.rb`, `*_test.exs`, `*_test.dart`, or a file under `test/`, `tests/`,
`__tests__/` or `spec/`) more often than it removes it. Each is a `wave-test` line after the
commit's `vault-id` lines, and exit 3 applies. Only the ticket's own wave numbers count, so
`TestW2Form` passes in a ticket without a wave 2.

```mermaid
sequenceDiagram
  participant T as task agent
  participant C as tix ws diff-check
  participant L as lead
  T->>C: ticket branch..HEAD, before reporting
  C-->>T: exit 0, or flagged files to drop
  L->>C: HEAD..task branch, before each merge
  C-->>L: exit 3: do not merge
  L->>T: ask a follow-up to drop the files, task is partial
```

A task agent runs it over its own branch before reporting, with `--ticket <ID>` for a ticket that has a vault folder. The lead runs it before each merge and the wave reviewer over the diff it reviews;
on exit 3 it does not merge and asks for a follow-up that drops the files from the unpushed
task branch, unless one is expected (a lockfile) and the board line says so. On exit 1 or an
older `tix` without the command, the lead reads the commit log instead.

## Checks that run in the sandbox

Sessions often run sandboxed, as root, with no network, a signing-only key at most, and
pushes only through a host broker.

- `.tool-versions` in the workspace root pins `go`, `gofmt` and `python3` through asdf, so
  the tools work without `ASDF_*_VERSION` exports.
- Go caches live under the workspace (`.cache/go`), not in the home.
- Each repo's own `AGENTS.md` names its check command (here: `make check`, which bounds
  test parallelism with `TESTFLAGS`). Tests never rely on file permissions, since root
  ignores them.
- Sessions commit unsigned and sign once, after the tidy (`tix ws resign <ID>`, which needs
  the sandbox's signing-only agent), or when you ask; `/tix:save` pushes only signed commits, see [finishing a ticket](finishing-a-ticket.md).

## Manual and synced tickets

| | Synced ticket | Manual ticket |
|---|---|---|
| Source | `tix sync` writes the note from the source | `tix new` writes it; never synced |
| Branch | `feature/<ID>[-<description>]` | `feature/<description>` (no ID) |
| `tix ws add` | `--desc` optional | needs `--desc` or `--branch` |
| Commit subject | `<ID> <summary>` by default | the ticket's chosen style, never an ID |
| Where its key may appear | code, commits, branches, PRs | only in the vault |

A manual ticket's key and the other vault-only IDs (task numbers, waves, relay and backlog
entries, review findings) stay out of code, docs, commit messages and pushed branch names.
For a manual ticket the kickoff asks for a commit style, recorded in `board.md`:
Conventional Commits (`type(scope): summary`) or the repo's own convention without an ID.
A synced ticket uses `<ID> <summary>` unless the repo's `AGENTS.md` says otherwise.
`tix ws add` refuses a manual ticket's branch that another workspace already has recorded.

Task lists cross the Jira boundary in both directions without becoming vault tasks.
`/tix:new-ticket` files a description that has a task list as HTML (`contentFormat:
"html"`, Jira's `task-list`/`task-item` markup), because Jira turns markdown `- [ ]` items
into plain bullets. `tix sync` (and the ticket-sync skill, for HTML bodies) writes Jira task
items back as plain bullets with the state as text, `- \[ \] item` and `- \[x\] item`, so
they never show up as checkboxes in the vault's task views.

Ticket types shape the plan: a bug starts from a root cause and a failing regression test,
an investigation ends in a written finding, a chore may be one task, an epic covers other
tickets.

## Sync and conversion

`tix sync` (`internal/jira`) plans from cheap listings and applies only what the changelog says
moved, with no model in the loop; the README's Sync section has the table. What keeps it safe:

- Keys become file names, so anything the server sends that isn't a ticket key is dropped.
- It never writes over an existing note the index can't read (it holds your work), a failed
  create leaves no half-written note, and the close is written last: a note closed before its
  other parts would never be retried, since the planner skips closed notes.
- Failed tickets become follow-ups that the next run retries, and a failed epic keeps no
  baseline. When tix planned the run (the skill then gets `--plan`), tix keeps the sync state;
  otherwise (`--full`, other sources) the ticket-sync skill does.
- A create that failed without the server saying so may still have created the issue
  (`jira.UnsureError`): filing again could make a second one, so callers never retry blindly.
- The agents' report and section writes go through the note write rules, see
  [the knowledge base](knowledge-base.md#note-writes).
- A headless sync session runs with `--strict-mcp-config`: only the MCP servers given on the
  command line (by `$TIX_CLAUDE` or whatever wraps claude), never the claude.ai connectors of the
  login, which need interactive auth and would stall a run with no one to answer.

`tix ticket convert` (`internal/ticketconv`) changes nothing on a refusal. It files the issue
before anything local changes, writes its resume record while the note is still in its old
folder, and moves the note last, so a stop anywhere leaves a state a re-run finishes without
filing a second issue. It reads `workspace.json` raw, because `workspace.Read` drops the paths
outside the workspace that this step must report, and compares the work root resolved, since it
may be spelled through a symlink. An ID is replaced only as a whole word (no letter, digit, `_`
or `-` before it, no letter, digit or `_` after it); task branch names and the `branch:` and
`converted-from:` lines keep the old ID. Task items become `- \[ \] item`, because the source's
markdown shows a real checkbox as a bare `[]`. When `internal/jira` reads task items, a box
without a list marker counts only in that bare `[]` form, so prose such as "[x] marks the spot"
is left alone.

A ticket's save state: unsaved while a `kb-drafts/` note isn't promoted; saved by its `saved:`
marker (or, in older notes, a summary block after the frontmatter); nothing to save when the
folder holds only the note and the workspace has no commits beyond its bases.
