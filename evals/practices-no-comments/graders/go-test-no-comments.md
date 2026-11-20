---
type: regex
target: { source: file, path: "retry/retry_test.go" }
pattern: "^\\s*//(?!go:|nolint:\\w)|[\\w;)}\\]]\\s+//"
flags: m
match: not_contains
---
