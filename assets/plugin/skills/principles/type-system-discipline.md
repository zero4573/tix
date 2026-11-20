# Type system discipline

Make the compiler reject impossible states, mixed-up primitives and unhandled variants.

- Give a primitive its own type when two values share one but mean different things; validate
  once where it is created.
- External data (JSON, RPC, CLI args, config, env, database rows) is untyped until a parse
  function at the boundary returns the typed model.
- Derive types from the schema that owns the shape instead of writing a parallel one.
- Handle every variant exhaustively, so a new one is a compile error.

Language syntax: the `tix:practices` files.
