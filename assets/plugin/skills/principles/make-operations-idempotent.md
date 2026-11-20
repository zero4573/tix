# Make operations idempotent

Every state-changing operation converges to the right state however often it runs and wherever
the last run stopped.

- Derive each step from the state that exists, not from what the last run meant to do: read,
  compare with the goal, apply the difference.
- Whatever a run leaves behind (a lock, a temp file, a half-written record) must be
  recognizable as stale by the next run. Write to a temp path and rename it into place.

Test: what if this runs twice, or crashed at every possible point?
