# Foundational thinking

Get the data shape right before the logic; the right shape makes the code that follows obvious.

- Define the core types first, trace every way the data is read and written, and pick
  structures that fit the dominant paths. Deduplicate structure, not every line.
- Before two actors share state, ask what happens when the other one changes it. Unless the
  answer is "nothing", isolate it.
- Scaffold first: what every later step needs (shared types, tests, checks) lands first, after
  dead code is removed. Small commits, one purpose each.
