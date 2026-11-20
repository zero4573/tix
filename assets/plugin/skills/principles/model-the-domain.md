# Model the domain

Encode the domain in a structure instead of scattered conditionals; the right structure makes
invalid states unrepresentable and deletes branches.

- A state machine instead of booleans and phase checks; a typed model instead of loose
  parameters; a map, registry or tagged union instead of branching spread across files; a
  reducer instead of ad hoc mutation.
- Don't force it: keep plain code when the shape is clear, local and unlikely to grow.

The sign you skipped this: a feature that adds one more branch to an if/else chain, or a second
boolean that must stay in sync with the first.
