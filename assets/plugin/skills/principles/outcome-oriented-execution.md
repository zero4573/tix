# Outcome oriented execution

Optimize for the verified end state, not for smooth intermediate states.

- Converge on the target design; don't write compatibility code that only keeps a transitional
  state alive, it outlives the migration.
- Breakage between phases is fine when it is planned, scoped and reversible: say where.
- Keep strong checks on the areas you touch, and run the full checks at the end.
