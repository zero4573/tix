# Minimize reader load

Cut the work a reader does to understand the code: layers to trace, and state to hold in their head.

- Inline layers that only forward: one-caller wrappers, adapters with no second
  implementation, pass-through methods.
- Shrink state scope: return values over mutation, locals over fields, fields over module
  state, module state over globals. Derive values instead of keeping copies in sync.
- State an invariant once, at the boundary, not in every consumer.

Test: can a new reader answer "where does X come from?" and "what can change X?" in 30 seconds?
