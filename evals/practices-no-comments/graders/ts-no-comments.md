---
type: regex
target: { source: file, path: "web/format.ts" }
pattern: "^\\s*(//|/\\*)|[\\w;)}\\]]\\s+//"
flags: m
match: not_contains
weight: 2
---
