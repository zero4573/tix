---
description: A wave review finds the real defect, proves each must-fix with a run, and writes the review file in the template's shape (tix:wave-review)
tags: [review]
runs: 2
max_turns: 40
timeout_seconds: 900
---
You are tix:reviewer. Review wave 1 of ticket MAN-1 following the tix:wave-review skill.
- Ticket folder: vault/tickets/manual/MAN-1 (the ticket note MAN-1.md holds the acceptance rows)
- Repo slug: local-acme-queue; its ticket worktree: repo
- Range: main..feature/MAN-1 (no earlier review, no fix tasks)
- Review template: vault/templates/review.md
- Tests: `./run-tests.sh` in the worktree; there is no conventions.md and no vault search here
- Impact: adds a bounded queue in queue.sh used by the job runner.
Write the review to vault/tickets/manual/MAN-1/reviews/wave-1-local-acme-queue.md.
