---
name: design
description: The design step of a ticket, between the plan's scope and its first task file. The lead traces the code, names the data shape, compares two shapes (or runs an arena of tix:designer agents when the design is expensive to reverse), settles experiment-shaped questions with prototype tasks, writes design.md and stops at the design review gate; it re-opens the gate when scope creeps. Also the brief of a tix:designer (a candidate or the judge). Adapted from pstack's architect and arena.
---

# design

Settle the shape before code: one attempt locks in the first shape a model thinks of. Applies
`foundational-thinking`, `model-the-domain` and `redesign-from-first-principles` (`tix:principles`).

## When (the lead)
After `plan.md`'s `## Scope`, `## Acceptance criteria` and `## Decisions` are written, before any
contract or task file, for a `dev` or `epic` ticket (or a bug fix that changes an interface) whose
change crosses a function boundary, adds or changes an interface, a schema or a persisted format,
or spans repos. Otherwise write `design: skipped, <reason>` in `plan.md ## Decisions` and go on.

## Steps (the lead)
1. **Trace** every system the change touches: Grep the ticket's terms, then the callers and
   callees of what they find; note who owns each piece of state today.
2. **Name the data shape first**: the types, states and the structure that organizes them (a
   state machine, a typed model, a registry or tagged union, the right collection).
3. **Two shapes.** Sketch two different designs (types, signatures, module boundaries, which file
   owns what) and pick the one where each piece of state has one owner, each task has one way to
   do it, and callers need to know least. One line says why the other lost.
4. **Expensive to reverse** (a persisted format, another team's interface, a security boundary,
   a public API): run the arena below instead of step 3 alone.
5. **Open questions:** one an experiment can settle (a layout, a timing, whether a library does X)
   gets a prototype task (below), never a question; a product or preference choice goes to the
   gate with the design.
6. Write `design.md` (template `design`, ≤60 lines) in the ticket folder.
7. **Design review gate:** `tix relay add … --kind question` with a summary of ≤10 lines (the data
   shape, the chosen shape and the rejected one, what each repo gets, open product questions),
   options A (recommended): accept; B: adjust (say what); C: rethink, then `Ask user:` and
   `Next: wait-user`. Contracts and task files come after the answer; record it in
   `plan.md ## Decisions`.

## Arena (expensive to reverse)
- Write the brief under `design.md ## Brief` (the artifact, the constraints, the files to read)
  and a rubric of 3-6 gradeable criteria under `## Rubric` (candidates are told not to read it).
- `Spawn:` three `tix:designer` agents in one block, the same prompt with `role: candidate <n>`,
  and `model:` fable, opus and sonnet. Each writes `designs/candidate-<n>.md`. In kiro a spawn
  takes no model: the seats differ only in their separate contexts; say so in `design.md`.
- When all three finished, `Spawn:` one `tix:designer` with `role: judge` (`model: sonnet`): it
  scores each candidate against the rubric and recommends a base in `designs/judge.md`.
- Read every candidate end to end and score it yourself, criterion by criterion. Pick the base a
  maintainer can extend most easily; graft the one or two best ideas of the others by hand.
  Candidates that converge: note it and take that shape. Candidates that diverge wildly: the
  brief was under-specified; sharpen it and run again. `design.md ## Chosen` names the base, each
  graft and its source, and the judge's verdict.

## Prototype tasks
A task file with `prototype: true`, a `## Goal` that states the question and the observation
that answers it, `owns` in its own worktree, and `## Done when` naming the evidence (output,
timing, screenshot). Quick and throwaway: no review, never merged. From its report write the
answer to `plan.md ## Decisions`, set its board line `dropped` with the note `prototype: <answer>`,
and remove its worktree with a plain `git worktree remove` (the unmerged branch stays: one
`pending — user` board line gives the user its `git branch -D`).

## Scope creep (the lead, every run)
Re-open the design review gate when any of these happens: a `request` or `feature` relay entry
that adds scope; a task's "Outside owns"; a repo not in `## Workspace`; a wave's diff adds a
public interface or contract `design.md` doesn't name; a new acceptance row; planned tasks more
than ~50% above the first plan. Append `## Delta <date>` to `design.md` (what changed, the shape
it needs, redesigned as if it had been there from the start), and ask: A: accept into this
ticket; B: split into a new ticket (recommended when the work stands alone; you file it as a
manual ticket); C: drop it. Running tasks the delta doesn't touch keep running.

## Designer (tix:designer)
Your prompt names the ticket, the workspace, your role and the brief (`design.md ## Brief`).
Read-only on code: Grep, Glob and Read by line range; never edit, commit or switch branches.
- **Candidate n:** never read `## Rubric` or another candidate. Write only
  `designs/candidate-<n>.md` (≤80 lines): the data shape, types and signatures, module boundaries
  with an ownership table (state → owner), how a caller uses it (a short example), the
  alternatives you considered and why they lost. Plain bullets, no checkboxes.
- **Judge:** read the rubric and every candidate end to end. Write only `designs/judge.md`
  (≤40 lines): a score per candidate per criterion with one line of evidence each, the base you
  recommend, and the one or two ideas worth grafting from the others.
Reply in ≤5 lines: the file path and the shape in one sentence.

Adapted from cursor/plugins pstack (MIT); see THIRD_PARTY_NOTICES.md.
