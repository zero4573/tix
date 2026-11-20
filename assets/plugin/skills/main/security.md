# The security sweep (main)

The host service runs the sweep itself (`tix cve update`): when main starts and whenever the
last sweep recorded in `security/.state.json` is 6 hours old, never twice within that
(`tix cve due` prints the last and next times). It writes `security/security.md` (Needs
action, Resolvable, Unresolvable, Watch, Resolved, Unscanned, Log) and
`security/advisories/<ID>.md`, and wakes you only when something needs you:
`security sweep <n> P1 <n> P2[ (partial)], assess <IDs>, ticket <IDs>, now resolvable <IDs>,
failed <sources>`, or `security sweep failed: <why>`; each item comes once, after it reaches you
(the rest of a long list is in `## Needs action`). Never edit `security/` by hand and never sweep yourself
(`tix cve update --force` only when the user asks for one now). Read only what an ID needs.

## On the event, per ID
- **assess** (a major CVE in a runtime an image runs): `tix cve show <ID>`, then the vendor's
  advisory from its references against each image tag. Record it:
  `tix cve set <ID> --assess "affected"`, `"unknown"` or `"not affected: <why>"`. Affected or
  unknown turns it into a ticket action below; not affected closes it.
- **ticket** (major, no ticket yet): `tix cve show <ID> --jira` prints the project, epic,
  labels, summary and markdown description. No epic set: ask the user once which epic, then
  `tix cve set --epic <KEY>` (`--label` too). Search the epic first:
  `searchJiraIssuesUsingJql` with `parent = <epic> AND text ~ "<ID>"` (and each alias the
  advisory lists). Found: `tix cve set <ID> --ticket <KEY>`. Else `createJiraIssue`: cloudId
  as `ship` finds it, `issueType: Task`, `parent` the epic, `assignee` your accountId
  (`atlassianUserInfo`, so `tix sync` brings it into the vault), the labels, summary and
  description as printed. A required field the create names: take the epic's value; one the
  epic doesn't have: ask. Then `tix cve set <ID> --ticket <KEY>`.
- **now resolvable** with a ticket (its row's ticket column): one `addOrEditJiraIssueComment`
  naming the fix from `tix cve show <ID>`'s `## Fix`.
- **failed <sources>** or **sweep failed**: tell the user once. A sweep without OSV doesn't
  count, so the service retries it after 30 minutes; the register keeps its rows meanwhile.

## Ask
Once per event, every ticket it created in one question (AskUserQuestion, one option per
ticket plus "none now"): "New security tickets under <epic>: <key> <summary> (<P>, <why
major>) … Start one now?". A start: `tix main sync` (refused for the 10-minute limit: queue it
and start it on the `sync new tickets` event), then put it first in `## Queue` with
`· security <ID>` and `start` it, `--force` when `tix start --list` marks it covered by the
epic (the user just chose it on its own). Not now: `## Proposed` with `· security`. Log it.

## Report
One line per ticket created, per assessment and per now-resolvable ID with a ticket; nothing
else (the register has the rest).
