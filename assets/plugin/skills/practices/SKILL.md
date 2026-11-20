---
name: practices
description: Language best practices for ticket code (TypeScript, React, Go), the no-comments rule and the security checks (tix ws sec-check - gitleaks, gosec, govulncheck, npm audit - with the legacy report), one rule per line, each paired with what enforces it (a lint rule, a scanner, a type, a test, or review judgment). Load it when a task writes or a review reads code in any language (the no-comments rule covers all of them, Dart, shell, Python and Nix included), and read the language files next to this one for .go, .ts, .tsx, .js or .jsx.
---

# Practices

Read the file for each language your diff touches: `typescript.md` (also for `.js`), `react.md`
for components and hooks (with `typescript.md`), `go.md`. Every rule names what enforces it. A
rule whose enforcer the repo hasn't wired (a lint rule missing from its config) is still a rule:
follow it, and the retro proposes wiring it.

A repo's own practices are in the vault, `projects/<slug>/<slug>-practices.md` when it exists
(read it by section: `rg -n '^## '`, then `sed -n a,bp`); they win over these files where they
differ, and the repo's `AGENTS.md` wins over both.

## No comments (every language)
Write no comments. Make the code say it: names, types, smaller functions, a structure that
models the domain (`tix:principles`, `model-the-domain`). Only these kinds stay:
- license or legal headers;
- non-obvious behaviour forced by something outside the code's control (a vendor, platform or
  protocol quirk), with what forces it;
- `prettier-ignore`, and suppressions of lint rules that are faulty or style-only;
- doc comments of a library other repos import, on its exported API; an app's or service's own
  classes, functions and methods get none (Go `//`, Dart and Rust `///`, JSDoc, docstrings);
- a link to an issue or RFC for a constraint the code can't express;
- tool directives, which are not comments (`//go:build`, `//go:generate`, `/// <reference>`).

A surprise in our own code is not explained, it is fixed: rename, extract, add a type, or
redesign until the comment is unnecessary. A suppression of a rule that protects correctness
(`react-hooks/exhaustive-deps`, `@ts-ignore`, `//nolint:errcheck`) is removed and the code fixed.
A "do not remove" or "IMPORTANT" claim is checked against the code; a real constraint becomes a
type, a test or a lint rule, then the comment goes. Commented-out code is deleted.
Enforced by: the `comment-check` hook after every edit in a ticket or spec session, `tix ws
diff-check` (`note comment-added`), the comment review and the wave review.

## Security (every language)
Every task runs `tix ws sec-check` before it reports, the lead's merge chain refuses a branch
that adds a blocking finding, and the first plan writes a legacy report of what was already
there, rated by risk and difficulty: easy fixes are made in the ticket, the rest go to the user.
How: `security.md` (next to this file).

## Adding a rule
A rule comes from a mistake that happened twice (`/tix:retro`). It goes in at the highest level
that works (a design that makes the mistake impossible, then a type, then a lint rule shown
failing on the real past mistake, then a test, and prose last), with its enforcer named.
Rules for one repo go to the vault's `projects/<slug>/<slug>-practices.md` (drafted in the ticket's
`kb-drafts/`, promoted by `/tix:save`), not to these files.

Adapted in part from cursor/plugins pstack (MIT); see THIRD_PARTY_NOTICES.md.
