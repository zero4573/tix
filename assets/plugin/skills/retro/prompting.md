# Retro: prompting habits and the fix ladder

## Prompting (a category of its own)
Moments where the user's input, not the agents, caused rework. They teach the user, so they never
become backlog items, tickets or `tix-improvements/` tasks. Look for:
- scope added mid-wave (a `request` or `feature` relay entry after wave 1 that the ticket or
  clarify could have carried);
- a vague or missing done check (an acceptance row the lead couldn't give a predicate, a
  verifier row `INCONCLUSIVE` for want of one);
- a clarify question answered "skip" whose assumed answer turned out wrong;
- a theory of the cause in the ticket that sent the diagnosis the wrong way;
- a design adjusted or rethought at its gate for something the ticket already knew;
- a decision reversed after the work it shaped was merged.

Each moment gets one habit to try next time, in the user's words, small and concrete ("put the
command that proves it works in the ticket", not "be clearer"). At most 3, most costly first.

Write them, asking nothing (like `tix-improvements/`), as one new note per retro:
`learning/prompting/<yyyy-MM-dd>-<HHmm>-<ID>-prompting.md` (outside a ticket the repo slug or
`session` for `<ID>`; `-2`… when taken), standard frontmatter with `type: learning-record`,
`tags: [learning, prompting]`, in a ticket `ticket: "[[<ID>]]"`, then one line each:
`- <the moment, with its evidence: R-nn, task NN, AC-n> · try: <the habit>`.
No moments: write no note. Name the note to the user. Coaching is quiet when
`learning/prompting/prompting.md` has `coach: quiet` in its frontmatter: still write the note,
but don't name it.

## The fix ladder (for every candidate but Prompting)
Put each change at the highest level that works, and say which:
1. **Design:** make the mistake impossible (one owner per piece of state, one way to do a task,
   hide what shouldn't be imported, delete the old way an agent would copy).
2. **Types:** the bad state can't be written.
3. **Lint or CI check:** its error names the fix; when the pattern is already common, fail only
   on new occurrences. Show it failing on the real past mistake before calling it done.
4. **Test** of the behaviour.
5. **Docs or agent rules**, last, only for judgement calls: nothing fails when an agent skips them.
A rule that already existed and was broken again moves up the ladder in the same change.
