---
type: llm
focus: { source: file, path: "vault/tickets/manual/MAN-1/reviews/wave-1-local-acme-queue.md" }
weight: 2
---
The Must-fix section has a finding that q_push accepts one job more than the capacity (its
`-gt` comparison should be `-ge`, so `q_new 2` lets a third push through), and that finding
shows a command that was run and the output it printed demonstrating the overflow.
