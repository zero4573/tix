# lead: checks

What the lead checks at four moments: when it merges, when it sets up test databases, when it
plans work the session may not be able to run, and before a contract goes to the tasks.
`SKILL.md` points here by heading. Each rule is here because a ticket lost a round to it.

## Merge chain
The chain of `SKILL.md` 5, with the parts that went wrong before:
- Logs: the chain's first link is `log=$(mktemp -d) &&`, and every check writes `"$log/<name>.log"`. Never build a log path from a variable set in an earlier call (shell state doesn't carry over: an unset one writes to `/` or into the worktree as `-lint.log`).
- Go fails with "read-only file system" on the module cache: the shell lacks the workspace's Go env. Put `eval "$(tix ws env)" &&` before the checks (it exports `GOPATH`, `GOMODCACHE`, `GOCACHE`).
- Format checks (gofmt, prettier, a linter's format mode) run on the files the merged range touches, never the whole tree: `git diff --name-only --diff-filter=d <pre-merge sha>..HEAD -- '*.go' | xargs -r gofmt -l`. A file that was unformatted on the base is not this merge's finding; never revert a merge for it.
- A chain that takes longer than a foreground call allows (about 3 minutes: merge, checks, integration runs) is run in the background (`run_in_background`) with its exit code written to `"$log/rc"`; wait with a bounded loop on that file, in the same run: never end your run (the final block) while it is still going, or you wake up again later beside the next lead. A wait that times out says nothing about the job: read `"$log/rc"` and the logs before you call it failed.
- `timeout <n> <cmd>` only with the cleanup after it in the same call (`; podman rm -f <the containers the command starts>`): a run cut short leaves a container that fails the next run.
- When the merged range touches a file that a spec cites (`evidence:`, `tests:`), run `tix spec check --ws <ID>` in the chain: a `stale-ref` finding is a citation that now points elsewhere, fixed in the same wave (by the task that moved the code, or your own commit).
- A consumer repo's fixture check that fails only on goldens an API task of the same wave changed, and passes against the API's pre-wave commit, is expected drift, not a failed merge: note it on the board line and plan the sync task.
- Before hand-over, per repo: `tix ws diff-check -C <ws>/<slug> --ticket <ID> $(git -C <ws>/<slug> merge-base HEAD <base ref>)..HEAD`. It scans your own and the PO's commits too (merge subjects included), which the per-task check never saw: a `vault-id` line there (a wave or task named in a subject) is reworded by the tidy.

## Test DB
`conventions.md ## Test DB` names, per repo stack: the DSN or env the suites need, a **health check** (one command that exits 0 when the stack is usable), a **rebuild** command, and which task may run it. Write all three at kickoff, not when the first suite fails.
- A `db: heavy` task runs the health check first; on failure it runs the rebuild, then the health check again; still failing, it reports `partial` with both outputs. It never reports "DB suites not run" without having tried the rebuild.
- When a review server (or anything else the PO or user runs) uses the stack's default database, give the tests a scratch database of their own in `## Test DB` at once: a suite that shares a database with a running server fails in packages no task touched.
- No container runtime in the session (below, Sandbox probes): the section says how the databases are started instead (a script under the workspace, the commands, the ports), written once.

## Sandbox probes
What the session can't do is found at planning, not mid-task. When a task of the plan needs one of these, probe it in your kickoff run and write the result into `conventions.md` (`## Environment`):
- container builds: `printf 'FROM scratch\n' > "$d/Containerfile" && podman build -q "$d"` (`d=$(mktemp -d)`). Fails (e.g. overlay `userxattr`): every step that builds an image is a host step, planned as one `pending — user` line with the exact commands, asked once through the gate.
- a C compiler for cgo (`command -v gcc cc`), PowerShell for `.ps1` tests (`command -v pwsh`), any other tool a check names: missing → the check is listed in `conventions.md` as "not runnable here", the task's `## Checks` says so, and the user gets it as one manual check, not as a surprise in every report.

## Contract checks
Before a contract (or a spec's `## Interfaces`) goes to the tasks:
- Every "X passes <check>" or "Y is accepted" claim is checked against the validation code: grep the rules that apply (the finding codes, the validator's error paths) and cite them next to the claim. A claim the code contradicts is a decision for the gate, not a line to write down.
- A new shape (a citation form, a caption, a field) is tested against **every** validator that reads it, not only the obvious one; list those validators in the contract.
- A helper that redacts, sanitises or filters output fails closed: the contract says what happens to every input it doesn't recognise, and that is never "returned as is". Adding or changing such a helper mid-ticket is a contract amendment with its own `tix:reviewer-deep` pass before the task starts.
- Files that hold secrets: ask for confinement by design (a directory handle the code can't leave: Go `os.Root`, `openat`) in the contract, not for path checks that each review round finds another way around (a leaf symlink, a parent symlink, a race in the walk).
- A task whose `## Do` lists several kinds of work gets one `## Done when` line and one report metric per kind (counts before and after, the functions named), so a part that was skipped shows.

## Task questions and refinements
- A task agent's question whose answer would change something a user decision fixed (a `user→lead · decision` relay entry, a `plan.md ## Decisions` line the user confirmed) goes through the gate, however minor it looks. You answer directly only what the files and decisions already settle.
- A reviewer's proposal that narrows a fix just made in the same direction (a stop condition that is too broad, a flag that is too global) is a refinement: plan it without a question. The fix-loop question (`reference.md`, "Fix loops") is for fixes that undo each other or break what an earlier round secured.

