---
title: {{title}}
created: {{date}}
updated: {{date}}
status: active
type: review
ticket: "[[TICKET-ID]]"
wave: 1
repo: provider-owner-repo
range:
model:
tags: [review]
---
# {{title}}

Wave review of [[provider-owner-repo]] for [[TICKET-ID]]; its tasks: `[[<ID>-NN-<slug>]]`.
Each finding ≤6 lines: `W<N>-<repo>-<M|S|C><nn> · file:line · problem · fix · test · confirmed|unconfirmed`;
a must-fix is `confirmed` by a run (the command and the output it printed).
Findings are plain bullets; only the `## Dispositions` lines are checklist lines.

## Summary
≤5 lines.

## Must-fix
<!-- ≤3, each confirmed by a run: data loss, security, a broken contract, a bug a user would hit, or a blocker for the next wave -->

## Should-fix
<!-- ≤6 -->

## Consider
<!-- ≤5 -->

## Keep as pattern
<!-- ≤5 bullets -->

## Dropped
<!-- One line: how many suspicions you dropped because no run confirmed them, and why (e.g. "2: the race needs a second writer the code never starts") -->

## Previous findings
<!-- One line: resolved and still-open IDs, checked against the previous review's Dispositions (a line whose word is `open` or `fixing` is still open) -->

## Dispositions
<!-- The reviewer writes one line per finding ID of Must-fix, Should-fix and Consider: `- [ ] <ID> · open`. The lead keeps exactly one line per ID and sets box, word and date: `- [/] <ID> · fixing · task NN`, `- [x] <ID> · resolved · task NN (<sha>) ✅ <date>`, `- [-] <ID> · kept: <reason ≤150 chars> ❌ <date>`, `- [x] <ID> · deferred → [[<ticket>]] · <why> #deferred ✅ <date>` -->
