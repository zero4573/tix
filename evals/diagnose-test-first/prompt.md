---
description: A bug fix starts from a regression test that fails before the fix (tix:diagnose)
tags: [diagnose, bug]
runs: 2
max_turns: 40
timeout_seconds: 900
---
BUG.md has a bug report against this repo. Find the cause and fix it; `./run-tests.sh` runs the
tests in tests/. Don't commit: the sandbox's .git is read-only.
