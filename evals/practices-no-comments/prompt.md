---
description: New Go and TypeScript code follows tix:practices, the no-comments rule first
tags: [practices, smoke]
runs: 2
max_turns: 30
timeout_seconds: 600
---
Add `Backoff(attempt int, base, max time.Duration) time.Duration` to retry/retry.go: attempt 0
returns base, each further attempt doubles it, never above max, and a negative attempt counts as
0. Add a table test next to it. Then add `formatBytes(n: number): string` to web/format.ts
(1023 → "1023 B", 1536 → "1.5 KiB", 1048576 → "1 MiB"). Follow our coding practices.
