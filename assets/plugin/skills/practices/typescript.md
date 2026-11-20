# TypeScript

Apply `type-system-discipline` and `boundary-discipline` (`tix:principles`) first.

| Rule | Enforced by |
|---|---|
| Model variants as a discriminated union with a literal `kind`, not a bag of optional fields | review |
| Brand primitives that mean different things (`string & { readonly __brand: "UserId" }`); a brand comes from a verifying type guard, never from `as` | review |
| Build shapes so illegal values can't be constructed (`[T, ...T[]]` for non-empty, `start` + `duration` for a range); strengthen a type only where the loose one forces `!`, a cast or a "can't happen" throw | review |
| External data is `unknown`, never `any` | `@typescript-eslint/no-explicit-any` |
| Parse external data with the repo's schema library and infer the type from the schema (`z.infer<typeof Schema>`) instead of hand-written guards | review |
| No `as` casts except right after validation; prefer `satisfies` to check a value without widening it | `@typescript-eslint/consistent-type-assertions` |
| Narrow in this order: discriminant switch, `in`, `typeof`/`instanceof`, a type guard, `as` last | review |
| A type guard (`isX`, `hasX`) verifies everything it claims | review, tests |
| Every switch over a union is exhaustive (`const _exhaustive: never = x` in the default arm) | `@typescript-eslint/switch-exhaustiveness-check` |
| Derive types from existing ones (`Pick`, `Omit`, `Parameters`, `ReturnType`, `Awaited`, `typeof`) before declaring a new interface | review |
| Functions with several parameters take one object, so order is self-describing (not on hot paths) | review |
| Don't mock what you can run: real framework test primitives, mocks only for what can't run locally | review |
| Structured logging with an id to debug from; no `console.log` in shipped code | `no-console` |
| No `@ts-ignore`; `@ts-expect-error` only with a reason a reviewer can check | `@typescript-eslint/ban-ts-comment` |
| No injection sinks with untrusted input (`eval`, `new Function`, `child_process` with a built string, non-literal `RegExp`, `fs` paths from input) | `eslint-plugin-security` recommended config |
| No production dependency with a high or critical advisory: bump it, or an `overrides` entry to the fixed version | `npm audit` (`tix ws sec-check`) |

Adapted from cursor/plugins pstack (MIT), `typescript-best-practices`.
