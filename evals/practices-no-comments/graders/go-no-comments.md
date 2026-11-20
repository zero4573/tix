---
type: regex
target: { source: file, path: "retry/retry.go" }
pattern: "^\\s*//(?!go:|nolint:\\w)|[\\w;)}\\]]\\s+//"
flags: m
match: not_contains
weight: 2
---
