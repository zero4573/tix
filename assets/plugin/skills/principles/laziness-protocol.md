# Laziness protocol

Bias toward deletion and the smallest change that solves the problem.

- Make the smallest diff that meets the goal. Fewer lines beat tidy boilerplate.
- Make a choice once, behind one source of truth, and pass the result on as a plain value.
- An abstraction earns its place by removing branches or invalid states now, not by a future
  caller that may never come.
