---
name: ship
description: The ticket's autopilot (autopilot stack, the default) after hand-over, run by the PO or as /tix:ship <ID>. Tidies and signs the ticket branches, pushes them with /tix:save, opens or updates one pull request per repo, then drives each to merge-ready, conflicts first, then review threads (tix:pr-feedback, replies posted), then CI, with a fresh tix:verifier at every new head. It never merges; it stops when only your approval and merge are left. Adapted from pstack's babysit, shipping and autopilot-stack.
argument-hint: "<ticket ID>"
---

# /tix:ship <ID>

You own the road to merge-ready, never the merge. Every pass is safe to rerun: read the state,
do the next step, report. Your cwd is the ticket workspace; `CLAUDE.md` names the vault.

## 0. Preconditions
- `board.md`'s `autopilot:` is `stack` (unset counts as `stack`); `off`: say so and stop.
- `status: done`: the PRs are merged; say so and stop.
- The lead's hand-over is done (`status: review`, `lead: none`) and `verification.md` has every
  acceptance row `PASS` or a gap accepted at the gate. Otherwise stop and say what's missing.
- Bitbucket goes through the `atlassian` MCP server: `discover` once per operation, then
  `executeRead` / `executeWrite` (no `cloudId` for Bitbucket). The sandbox's proxy refuses merges, declines,
  approvals, API commits and branches, and pipeline runs: never try them. No paid Rovo calls.

## 1. Tidy, sign, push (first pass)
1. `tix ws marks <ID>`: a repo not `ok` for `tidy` gets `/tix:tidy` (the `tix:tidy` skill, run as
   it says), with no question: autopilot means yes.
2. `tix ws resign <ID>` (it signs the same trees; 1Password may ask the user to approve).
3. The tidy keeps every tree byte for byte, so `verification.md` still holds: check it with
   `git rev-parse <old head>^{tree} <new head>^{tree}` per repo (the old heads are in its
   frontmatter); a differing tree needs a new `tix:verifier` run before the push.
4. `/tix:save` (the `tix:save` skill): it writes the vault and pushes the ticket branches; its
   own questions (Jira transitions) stay the user's.

## 2. Open or update the pull requests
Per repo of `workspace.json`: find an open PR from the ticket branch
(`listBitbucketRepoPullRequests`, `q: source.branch.name = "<branch>" AND state = "OPEN"`).
- None: `createBitbucketRepoPullRequest` from `<branch>` to the repo's `base`, ready for review
  (not draft), `closeSourceBranch: true`, the reviewers of `getBitbucketRepoDefaultReviewers`.
- Title in the ticket's commit style (a synced ticket: `<ID> <summary>`; else `type(scope):
  summary` naming the main symbol). Body ≤40 lines: the repo's own PR template when it has one
  (`.bitbucket/pull_request_template.md`, `PULL_REQUEST_TEMPLATE.md`, or the one the session's
  SDLC instructions name), else `## Why`, `## What changed`, `## Scope`, `## Tradeoffs`,
  `## Blast radius`, `## Verification`, from the ticket note's `## Review`, `design.md` and
  `verification.md` (each acceptance row's verdict and how it ran). No vault-only IDs, ever.
  Then `## Summary` from `summary/summary.md` (its `What changed` and `Try it`, ≤40 lines) and
  one line saying where its screenshots and video are (the Jira ticket, or "kept with the ticket").
- One existing: update its description the same way (`updateBitbucketRepoPullRequest`) when the
  head moved. Record each PR's link in the ticket note's `## Review` (one line per repo).

## 2b. The work summary on the Jira ticket
For a Jira ticket, when `summary/summary.md`'s `heads:` match the PR heads and its `posted:` is
empty or older (a summary from an older head: spawn a fresh `tix:summarizer` first):
Jira calls take a `cloudId`: the jira source's `site` from `tickets/.sources.json`, else the one
jira resource of `getAccessibleAtlassianResources` (see `ticket-sync`'s `sources/jira.md`).
1. Each file of `summary/media/` up to 20 MB (larger ones stay in the vault):
   `uploadAttachmentToJiraIssue` (`executeWrite`) with `filePath` relative to `summary/media/`
   (phase 1). Its `uploadCommand` carries a short-lived token: run it from `summary/media/` as
   `bash -c "$(jq -r .data.uploadCommand <reply.json>) -sS"`, never print it or write it to a note,
   and keep the `data.id` it returns, with phase 1's `collection` and `fileName`. No phase 2: the
   comment's inline embed attaches the file, and phase 2 as well attaches it twice.
2. One comment (`addOrEditJiraIssueComment`, `contentFormat: "html"`): the summary's
   `What changed`, `Try it` and `Verification`, the PR links, and the media by
   `inlineFileId` / `inlineFileCollection` / `inlineFileName` (parallel lists, in the summary's
   order), which shows them under the text and attaches them.
3. Set `posted: <date> <slug>@<sha>…` in `summary.md`.
An upload refused or failing: the media stays in the vault and the comment says so; never retry
in a loop. Another source (manual): nothing to post, the summary stays with the ticket.

## 3. Drive to merge-ready (every pass, per PR, in this order)
0. **Merged or declined:** `getBitbucketRepoPullRequest`'s `state`. `MERGED`: nothing left for
   this PR. `DECLINED`: the user's call; report it and leave the PR alone. When every PR is
   `MERGED`, set the ticket note's `status: done` (a lead's covered tickets too), and report
   that `tix clean` can remove the workspace now; the ticket is done only then.
1. **Conflicts:** `getBitbucketRepoPullRequestMergeability`. Behind its base without conflicts:
   `git -C <path> fetch` then `git -c commit.gpgsign=false rebase origin/<base>` in the ticket worktree, the repo's
   checks, `tix ws resign <ID> <slug>`, a fresh `tix:verifier`, then
   `git push --force-with-lease=<branch>:<sha you last pushed> <remote> <branch>` (the host's
   broker lets you rewrite only your own commits on a `feature/` branch). A real conflict: stop
   for this PR and report the files and the command to the user; a conflict is theirs.
2. **Review threads:** `tix:pr-feedback` for this ticket (it applies and commits feedback and
   drafts a reply per thread). Then resign, a fresh `tix:verifier`, push (fast-forward), and post
   each drafted reply under its thread (`addBitbucketRepoPullRequestComment` with
   `parentCommentId`), naming the commit that answers it; resolve a thread only when the change
   fully answers it. Judge every comment on its merits, bots' too: a wrong one gets a reply with
   the evidence, not a change.
3. **CI:** `listBitbucketRepoPipelines` (`targetBranch: <branch>`, `sort: -created_on`) at the
   head. Running: end the pass. Failed: read the failing step's log
   (`getBitbucketRepoPipelineStepLog`, filtered), save an excerpt to
   `tickets/<source>/<ID>/ci/<date>-<slug>.md`, and decide: a failure of this diff is a
   `request` relay entry for a fresh lead ("CI failed in <step>: see ci/…"); the lead's fix
   round ends in a new hand-over and the next pass pushes it. A flake (it passes locally, the
   log shows infrastructure) is reported for the user to rerun once; you can't run pipelines.
4. **Merge-ready:** no blocker but approvals and the merge itself (mergeability), CI green at the
   head, every thread answered, `verification.md` at this head: report it and stop.

## 4. Report (every pass, ≤8 lines)
Per PR: its link, the head sha, CI, threads (answered/open), the verifier's verdict at the
head, and what's next: "ready for your review and merge (run `/tix:ship <ID>` after merging
to close the ticket)", "CI running: run `/tix:ship <ID>`
again (Claude Code: `/loop 10m /tix:ship <ID>`)", or the blocker that is the user's. Under a
main session (`tix main`), end with the state alone (`merge-ready`, `CI running`, or the
blocker): main reruns you, stops this session at merge-ready and watches the PRs.

Adapted from cursor/plugins pstack (MIT); see THIRD_PARTY_NOTICES.md.
