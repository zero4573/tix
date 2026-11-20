# Fix root causes

Fix the fault, not the place where it surfaced: the first place the system did something it
should not, or failed to handle a state it should.

- Reproduce first. Ask why until the answer is a wrong decision in the code; when stuck,
  instrument and read the real state.
- Ask whether the triggering state is legitimate. If it is, handle it where the code broke. If
  it should never exist, a guard there hides the bug: find what produced it. Guards, retries,
  swallowed errors and clamped values are the usual ways to hide one.
- Fix the pattern, not the instance: find every place with the same fault.
