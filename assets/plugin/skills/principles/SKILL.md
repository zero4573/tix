---
name: principles
description: Engineering principles for ticket work, adapted from pstack. An index of 24 rules (core, architecture, verification, delegation, meta), each with when it applies; the full rule is the file <name>.md next to this one. Load it when a task file lists principles, when planning or reviewing code, or to name the principle behind a recommendation.
---

# Principles

One file per principle sits next to this one (`<name>.md`, under 15 lines each). Read the file
of every principle you apply; this index only says when each applies.

- **Lead:** each task file's `principles:` lists the 1-4 that shape its design choices (always
  `prove-it-works` for code; `test-behavior-not-implementation` when it adds tests). A gate
  recommendation names the principle behind it ("Recommended: B. Subtract before you add: …"),
  so the user learns the words and can steer with one line ("apply prove-it-works").
- **Task agent:** read the listed files before writing code. The report's `Principles:` line
  names each one with the choice it changed, or `none changed`.
- **Reviewer:** judge the diff against the task's principles and the core set
  (`subtract-before-you-add`, `minimize-reader-load`, `boundary-discipline`,
  `test-behavior-not-implementation`). A finding names the principle it rests on.

| Principle | Group | Applies when |
|---|---|---|
| `laziness-protocol` | Core | Sizing a diff, refactoring, or tempted to add an abstraction, layer or flag |
| `foundational-thinking` | Core | Before writing logic: core types, data structures, the order of scaffold and feature work, what concurrent actors share |
| `redesign-from-first-principles` | Core | A new requirement has to fit into an existing design |
| `attack-the-premise` | Core | Two or more fixes that share one assumption have failed the same check |
| `subtract-before-you-add` | Core | Ordering an addition, refactor or rewrite |
| `minimize-reader-load` | Core | Shaping or reviewing code that is hard to follow |
| `outcome-oriented-execution` | Core | A planned rewrite or migration with explicit phase boundaries |
| `experience-first` | Core | Product, UX or feature-scope tradeoffs |
| `exhaust-the-design-space` | Core | A new interaction or architectural decision with no precedent in the code |
| `build-the-lever` | Core | Any non-trivial work: edits, migrations, analyses, checks |
| `model-the-domain` | Architecture | Stateful logic, or code that branches a lot or repeats a shape assumption across files |
| `boundary-discipline` | Architecture | Wiring validation, error handling or framework adapters |
| `type-system-discipline` | Architecture | Designing types or a signature in a typed language |
| `make-operations-idempotent` | Architecture | Commands, lifecycle steps or loops that run amid crashes, restarts and retries |
| `migrate-callers-then-delete-legacy-apis` | Architecture | A new internal API replaces one that still has callers |
| `separate-before-serializing-shared-state` | Architecture | Concurrent actors might write the same file, branch, key or object |
| `prove-it-works` | Verification | After a task, before calling it done |
| `fix-root-causes` | Verification | Debugging |
| `sequence-verifiable-units` | Verification | Multi-step work (sweeps, migrations, runs of similar edits) and the order of commits |
| `test-behavior-not-implementation` | Verification | Writing, changing or keeping a test |
| `explain-the-number` | Verification | Measuring, reporting or acting on a number (a speedup, a latency, a count, an eval result) |
| `guard-the-context-window` | Delegation | Large outputs, long files, repeated reads, wide fan-out |
| `never-block-on-the-human` | Delegation | Tempted to ask "should I do X?" about reversible work |
| `encode-lessons-in-structure` | Meta | The same instruction is written a second time, or a correction recurs |

Adapted from cursor/plugins pstack (MIT); see THIRD_PARTY_NOTICES.md.
