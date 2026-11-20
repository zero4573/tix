# lead: hand-over

Read by the lead (the `tix:lead` agent) when the review-disposition gate (`SKILL.md` 7)
prints nothing, the board has no `planned`, `running`, `partial` or `orphaned` line, and every
item of `plan.md ## Waves` is merged or dropped with the user's answer (unstarted scope is asked
about through the gate, never handed over silently).
Ends with the ticket at `status: review` and `Next: handover-done`. Never run `/tix:save`; push only as `SKILL.md` 5, "Push" says (a commit of the hand-over is pushed in this run).

## 0. Disposition gate (precondition)
Run the gate of `SKILL.md` section 7 (script: `reference.md`, "Gate script") from the vault root (including its `no Dispositions`
line and the open-line and `#deferred` checks): its output must be empty. If it prints anything, go back to planning fix waves
(or `kept:`/deferral dispositions); never hand over with an `open` or `fixing` finding. With a `board.md ## Specs` list, every line must be `merged` and the PO must have flipped the passed requirement IDs (`specs.md`, "Review").

## 1. Workspace clean
In one chain per repo:
`git -C <ws>/<slug> worktree list` (only the ticket worktree is left),
`git -C <ws>/<slug> branch --list 'task/<ID>-*'` (empty), and
`git -C <ws>/<slug> status --short` (empty; commit leftovers with a subject in the ticket's commit style). Run the whole-range diff-check of `checks.md`, "Merge chain" (your and the PO's commits, merge subjects included), then `tix ws cleanup <ID>`: it removes the spent tidy clones and tidy backup refs and says what it kept. Merged task worktrees the sandbox keeps busy are one line in `## Review` for the whole ticket (`tix ws task-clean <ID>`, run on the host; `tix start` also runs it before the next session), never a board line each; only an unmerged task worktree or branch stays on the board as `pending — user` with its command.

## 2. Retro (short; the method of the user-invoked `/tix:retro`)
Over the board lines, the reviews' `## Summary` and finding lines, and the task reports
(`rg -n -A12 '^## Report' tickets/<source>/<ID>/tasks/`), find what would have made this ticket's agents
faster or safer: navigation pointers, missing or unwired automated checks, reviewer rules
(mechanical violations get a check instead), `AGENTS.md` no-ops and bloat, tool economy,
information access, each placed on the fix ladder (`prompting.md` in the retro skill's folder, a sibling of this skill's). Add **at most 8**, most severe first. Also write the Prompting note of that file (moments where the user's input caused rework, each with a habit to try), asking nothing. Each one whose change is to tix itself
(its skills, agents, hooks or a `tix` command) is a task in one new note in the vault's
`tix-improvements/` (name and format: the vault's `AGENTS.md`, "Vault Structure"),
written now with no question and named in `## Review`: never a backlog item or a follow-up
ticket. Add the rest to `backlog.md ## Open` as
`- [ ] B-nn · w<N> · <repo|vault> · retro: <change> (<evidence: task NN, finding id>) ➕ <date>`. They go
into your follow-up decisions (3) with the rest of the backlog.

## 3. Backlog → follow-up tickets
1. Decide each open item of the backlog's `## Open` yourself (`SKILL.md` 7, "Follow-ups are your
   call"): A, this ticket; B, a new ticket; C, an existing ticket (name it); D, leave it in the backlog (backlog items only).
   Items you pick A become a fix wave (`SKILL.md` 4-7) merged before the hand-over ends. For each
   B: `tix new --type <dev|bug|chore|investigation> "<summary>"` (items that belong together share one). It prints
   the new note's path. Write each new note's initial content yourself, in that note only:
   `## Description` (the items and why they matter), `## Acceptance criteria`,
   `## Context / links` (`[[<ID>]]` and the `B-nn`/finding IDs), plus `projects:` and the tag
   `from-ticket`. From then on the ticket is the user's. For C, add the item to that ticket's note.
   List every item with its choice in `## Review`: the user reverses any later.
2. A synced ticket's follow-ups may also belong at its source (e.g. Jira), which other people
   see: ask once, through the gate, in one `Ask user:` question listing the new tickets (B) with a
   recommendation per ticket (which to file at the source, which to keep in the vault). End the
   run with `Next: wait-user`. The PO files the chosen ones with `/tix:new-ticket`; never you.
3. Check that each `deferred` disposition line's ticket exists and lists the item; fix any that doesn't.
4. Close the items sent to tickets: change each line in `backlog.md` to `- [x] B-nn · … · deferred → [[<ticket>]] #deferred ✅ <date>` and move it under `## Closed` (`### w<N>`); a dropped one is `- [-] … · dropped: <reason> ❌ <date>`; the rest stays `[ ]` in `## Open`.

## 4. kb-drafts
Make sure `tickets/<source>/<ID>/kb-drafts/` is complete, each note with a `target:` field:
- feature notes with the projects touched and target versions;
- sequence notes with a mermaid `sequenceDiagram` for each data flow the ticket adds or
  changes;
- a decision note (template `templates/decision.md`) for each major choice in
  `plan.md ## Decisions`;
- relationship changes (`depends-on`, group membership, any proposed group) as listed in
  `plan.md ## Relationships`, so `/tix:save` can apply them.

## 5. `## Review` in the ticket note (≤40 lines)
- what changed per repo (`tix ws diff <ID> --stat`, one line per repo);
- for a lead, one line per covered ticket: what was done for it, and anything left;
- the waves and their reviews: counts, and any must-fix left open with its reason;
- tests added and run, with results;
- open `## Follow-ups` (blockers still waiting on someone), each follow-up item with your choice (A-D) and the new tickets filed (B), and the `tix-improvements/` note of the retro (2), if any;
- remaining risks, and the board's `pending — user` lines: what the user checks by hand.
  To try running apps, give the command for the host while the session runs: `tix ws browse
  <ID> http://localhost:<port>` for a web app, `tix ws run <ID> -- <client>` for anything else
  (the apps keep their usual ports; no port publishing).

## 5b. Acceptance criteria (manual ticket)
If the ticket note has `source: manual`: tick the `## Acceptance criteria` items the merged work
meets: read the section with `tix vault note get` first, then write it back with
`tix vault note set-section` changing only `[ ]` to `[x]` (text unchanged). Unmet ones stay open and
are named in `## Review`. Never edit a covered ticket's criteria or a synced ticket's source block.

## 5c. Docs and tidy marks
Run `tix ws marks <ID>`. Exit 0 (every repo `ok` for `tidy`), or `board.md`'s `autopilot:` is `stack` or unset (the PO's `tix:ship` tidies, with no question): go on. Otherwise ask
through the gate (`SKILL.md`): run tidy now? A (recommended): tidy;
B: neither (`/tix:save` will warn and ask before pushing). End with `Next: wait-user`,
and on the answer run what was chosen per `tix:tidy` (the lead marks it
with `tix ws mark`) before Finish.

## 6. Finish
1. Set `status: review` (and on each covered ticket whose part is done). Mark the
   hand-over line in `## Tasks` `[x]` with `✅ <today>`.
2. End with the final block: `Spawn: none`, `Ask user: none`, `Pushed:`, `Status:` the ticket is ready
   for review (`tix ws diff <ID>` shows the changes) plus any `pending — user` command, and
   `Next: handover-done`. The PO tells the user the rest (changes in this session, then
   `/tix:save`, which pushes the ticket branches).
