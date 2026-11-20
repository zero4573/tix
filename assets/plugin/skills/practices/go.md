# Go

Apply `boundary-discipline` and `type-system-discipline` (`tix:principles`) first.

| Rule | Enforced by |
|---|---|
| A `//nolint` names the linter and the reason (`//nolint:gosec // <reason>`); one that silences a correctness linter (errcheck, staticcheck, govet, gosec) is removed and the code fixed | golangci-lint `nolintlint` (`require-explanation`, `require-specific`) |
| A function that does I/O or can block takes `ctx context.Context` as its first parameter and passes it on; never store a context in a struct | revive `context-as-argument`, `containedctx` |
| Wrap errors with `%w` and context (`fmt.Errorf("load config: %w", err)`); compare with `errors.Is`/`errors.As`, never by message | `errorlint` |
| No mutable package-level state: pass dependencies in (a struct's fields, function arguments); constants and `sync.Once`-built read-only tables are fine | `gochecknoglobals` |
| Accept interfaces, return structs: define an interface where it is consumed, with only the methods used, and return concrete types | `ireturn`, review |
| Doc comments only on packages other repos import; internal packages get none, so turn off the exported-symbol doc rule for them | review, revive `exported` scoped to public packages |
| No secrets, injection or unchecked input in code: gosec's rules; a wrong finding gets `// #nosec G<nnn> -- <why>` on its line, never a blanket exclusion | gosec (golangci-lint `gosec`, `tix ws sec-check`) |
| No dependency or toolchain with a known vulnerability in code we call: bump to its fixed version | govulncheck (`tix ws sec-check`) |

More rules are added from retros (`SKILL.md`, "Adding a rule").
