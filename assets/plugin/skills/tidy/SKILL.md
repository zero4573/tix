---
name: tidy
description: Rewrite a ticket branch's own commits, back to its branching point and pushed ones included, into coherent ones with Jujutsu (/tix:tidy), the tree unchanged byte for byte, in a scratch clone, then move the branch with a backup ref, sign it and push it with a lease. Use when the user runs /tix:tidy, asks to clean up or squash the ticket's history, or when a task agent has skills [tix:tidy].
argument-hint: "[repo slug ...; default: every repo with commits above its branching point] [since <rev>|unpushed]"
---

# /tix:tidy [repo ...] [since <rev>|unpushed]

Order: tidy → sign → push (the lead's "Push", a `--force-with-lease` when pushed commits were rewritten) → `/tix:save` (which pushes what is left and pins specs). Tidy only rewrites history, never content.
Its reach is the ticket branch's own commits, down to its **branching point**: `git rev-list HEAD --not --exclude=<remote>/<branch> --remotes` (`<remote>`: `branch.<branch>.remote`, else `origin`), pushed to the ticket branch or not, never one the base or another remote branch has (a branch it is stacked on, a merged one). A commit in reach whose author or committer is not the worktree's `user.email` (`git log --format='%h %ae %ce' <reach>`) is someone else's: stop and ask through the gate before planning. Only when the user says so explicitly does it reach elsewhere: `since <rev>` (a later boundary), `unpushed` (the old reach, `--not --remotes`), or a mode of `modes.md`.
Tidy before the save that pins: save pins each vault spec version to the pushed SHA, so the pinned one is the final one.

## Started by the PO

The user runs `/tix:tidy` in the PO session. Outside a ticket workspace (no `board.md`
next to `CLAUDE.md`), say so and stop. Otherwise add a relay entry with
`tix relay add <ID> --from user --to lead --kind request "tidy history: <repos or all>"` (never
edit `relay.md` by hand; it prints `R-nn`) and wake the
lead (SendMessage "new request R-nn"; no live lead: planned when the next one is spawned).

## The lead's part

1. One task per repo with commits in reach (the branching point's rev-list above, or the user's
   `since`/`unpushed` boundary; none: skip the repo). The scratch clone dir is
   `<ws>/tasks/tidy-<slug>`, or `tidy-<slug>-2`, `-3`… when an earlier tidy's clone is still there (never reuse or rename one). Give the task the immutable boundary, from the ticket worktree: the
   full ids on the `-` lines of `git rev-list --boundary HEAD --not --exclude=<remote>/<branch> --remotes`
   (`--not <rev>`, or `--not --remotes` for `unpushed`), plus the base tip when `git merge-base --is-ancestor <base tip> HEAD` holds.
2. After the task, in the ticket worktree: `git fetch <scratch clone> <bookmark>` (the new
   tip is `FETCH_HEAD`), then check all of these, else stop with nothing moved:
   - `git rev-parse FETCH_HEAD^{tree}` equals `<old>^{tree}`;
   - every boundary id: `git merge-base --is-ancestor <id> FETCH_HEAD`;
   - `git rev-list FETCH_HEAD --not --exclude=<remote>/<branch> --remotes` lists only new commits: nothing another branch has was rewritten.

   Then `git update-ref refs/tidy-backup/<branch> <old>` and move the branch with
   `git update-ref refs/heads/<branch> <new> <old>` (`<old>` is the report's `Old tip:`; the old value guards a race: when the branch moved since, the tidy is stale and the task runs again on the new tip, nothing is moved). The
   tree is the same, so `git status --short` must stay empty: check.
3. Run the repo's checks, then sign: `tix ws resign <ID> <slug>` re-creates the unpushed
   commits signed (the same trees, authors, dates and messages; only the ids change), as the
   lead's every push does. It fails without moving the
   branch when the signing agent refuses: ask the user to unlock it (the gate) and run it
   again; never push or mark an unsigned result as done. Then `tix ws mark <ID> tidy`, and push
   (`lead/SKILL.md` 5, "Push"): when pushed commits were rewritten, `origin/<branch>` is an ancestor
   of `refs/tidy-backup/<branch>` and the push is its `--force-with-lease` to that sha; the host's
   broker may refuse it (then the user runs it). A tidy after the PRs are open rewrites what reviewers
   saw: only when the user asked for it.
4. Record the old→new mapping (old range, new commit list) in the ticket note's
   `## Review`, and delete the scratch clones with `tix ws cleanup <ID>` (the sandbox denies `rm -rf`; it removes a clone whose result is on the ticket branch, its backup ref or the remote, or has the branch's tree, and the backup refs of pushed branches; a clone it keeps holds work that is nowhere else: `tix ws tidy-clean <ID> --force` only after the user agrees).

Never touch commits below the boundary, never `reset --hard`. Other shapes of rewrite (a rewrite from the root, one script applied to every commit, a rebuild by capability): `modes.md`, read it before planning one.

## The task agent's part

jj is Jujutsu (0.46 syntax; `jj help <cmd>` when unsure). Never colocate it in a ticket
worktree (it would write the main clone's `.git`): work only in the scratch clone.

1. `<old>` = the ticket branch tip **as you read it when you start** (`git -C <ticket worktree> rev-parse <branch>`), never a tip written into the task file earlier: the PO's and the lead's commits move it while a tidy waits. Put it in your report as `Old tip: <sha>`; the lead's branch move checks the branch is still there. The boundary ids come from the lead.
2. `jj git clone --colocate <ticket worktree> <scratch dir>` (the whole repo, so every
   boundary id exists in it; the clone already tracks the bookmark). Before any commit set,
   with `jj config set --repo`: `user.name`/`user.email` from the worktree's `git config`,
   and `ui.editor '["true"]'` (a list: a bare `true` is a TOML boolean jj rejects). Then set `revset-aliases."immutable_heads()"` to the boundary ids (a
   union by `|`, full ids) so jj refuses to rewrite anything not made for the ticket.
   Check `jj log -r '(<boundary ids>) & ~immutable()'` is empty (every boundary id
   immutable) and `jj log -r '<boundary ids>::'` shows only ticket commits as mutable.
3. Fold each fix-up commit into the commit it changes: `jj absorb --from <rev>` (a bare
   `jj absorb` reads the empty `@`), then `jj abandon <rev>` when it is left empty. Then
   `jj squash`, `jj split`, `jj describe` into one commit per coherent change; `jj rebase`
   to linearize (no merge commits); lines added then reverted disappear. Messages in the
   ticket's commit style with a why-body and no vault-only IDs (task numbers, waves, `R-nn`,
   `B-nn`, findings, a manual ticket's key). A rewrite gives descendants new commit ids:
   address commits by change id or relative revset (`@-`), never a stale commit id (it
   makes divergent copies). When the history is too tangled to fold, rebuild by concern
   instead: `jj new <start>`, `jj restore --from <old> <paths>`, describe, repeat.
   `jj op log` / `jj op restore` undo a mistake. Don't `git checkout` in the clone.
4. Verify the final tree equals `<old>^{tree}` byte for byte: `git rev-parse <new>^{tree}`
   in the clone (colocated, so git sees jj's commits). Different: `jj op restore`, fix,
   repeat.
5. Run the repo's checks on the final commit, move the bookmark to it:
   `jj bookmark set <ticket branch> -r <rev> --allow-backwards`. Report the commit list
   and the bookmark. Don't sign in the clone: the lead signs the moved branch (`tix ws resign`).
