# Boundary discipline

Validate, narrow types and handle errors where data crosses a boundary (CLI args, config,
network, external APIs, the database). Inside, trust the types: no re-validation, no redundant
nil checks.

- Parse raw data into domain types at the boundary; keep transport and framework types out of
  the core.
- Business logic lives in pure functions; the shell that calls them stays thin.
