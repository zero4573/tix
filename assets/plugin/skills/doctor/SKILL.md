---
name: doctor
description: Grade finished ticket sessions from their Claude Code transcripts (/tix:doctor [ID...]). tix doctor collect condenses each ticket's sessions and subagents, one tix:judge per ticket scores them on efficiency, code quality, workflow compliance and communication plus tix's own checks, and the failures that recur across tickets become evidence-backed tasks in the vault's tix-improvements/. Never edits tix, a repo or a skill.
disable-model-invocation: true
argument-hint: "[ticket IDs; default: the 6 most recent unscored]"
---

# /tix:doctor [ID...]

A retro looks at one ticket; you look across several for the mistakes that keep coming back.
Every change you propose traces to a failure you saw in two or more tickets, never to general
advice. Transcripts stay on this machine: quote no secret, token or customer data into the vault.

## 1. Collect
`tix doctor collect [ID...]` (no IDs: the 6 most recent tickets no doctor note scored yet).
Its lines: `out <dir>`, `ticket <ID> <transcripts> <tool errors> <KB> <index.md>`, `scored <ID>
<note>`, `none <ID>`. Exit 2: no ticket has Claude Code transcripts (kiro sessions leave none);
say so and stop. An unknown command: this `tix` is older than the skill; say so and stop.

## 2. Judge
Spawn one `tix:judge` per `ticket` line, up to 4 at a time, each with the prompt:
`Score ticket <ID> with tix:grade. Bundle: <dir>/<ID> (start at index.md). Ticket folder: the
index's first line. Write <dir>/<ID>/score.md.`
Wait for all of them; a judge that wrote no `score.md` is left out and named in your summary.

## 3. Group
Read every `score.md` and the `tickets:` and `## Watch` of earlier doctor notes
(`rg -l '^tickets:' <vault>/tix-improvements/*-doctor.md`). Group the failure lines by cause
and owner, in your words: two lines are one cause when one fix would prevent both.
- **Recurring:** the cause shows in 2 or more tickets, counting earlier `## Watch` lines.
- **Watch:** seen in one ticket only.
- Drop a cause an open task (`- [ ]`) in `tix-improvements/` already covers; name the note
  instead.
For each recurring cause owned by tix, read the skill or agent it names (the plugin's skills are
this skill folder's siblings) and place the change on the retro's fix ladder (design, types,
lint or check, test, docs or agent rules last); a rule that exists and was broken again moves up
a level. Draft the edit as a unified diff against the file's path in the tix repo
(`assets/plugin/skills/<name>/SKILL.md`, `assets/plugin/agents/<name>.md`, a `tix` command's
`internal/` package by name only).

## 4. Write
One new note, asking nothing: `tix-improvements/<yyyy-MM-dd>-<HHmm>-doctor.md` (`-2`… when
taken), the frontmatter of a retro's note there with `doctor` added to its tags,
`tickets: [<every ID judged>]`, `overall: <mean of the tickets' overall, 2 decimals>`; then:
- `## Scores`: one row per ticket (`| ID | efficiency | quality | compliance | communication |
  overall | failed checks |`), and one line comparing the mean with the last doctor note's.
- `## Tasks`: per recurring tix cause, `- [ ] <category> · <change> · <where in tix> ·
  <evidence: ID and transcript:line, for each ticket> ➕ <date>`, most costly first, at most 6.
- `## Repos`: recurring causes a repo owns (a missing check, a doc an agent needed), one line
  each with the repo and evidence; never filed anywhere.
- `## Prompting`: recurring moments where the user's input caused rework, each with a habit
  to try (the `/tix:retro` prompting category, `/tix:coach sharpen` for practice).
- `## Watch`: the one-ticket causes, one line each, for the next doctor run.
- `## Drafts`: the diff of each task, under a `### <task's change>` heading.
No recurring cause: write the note with Scores and Watch only.

## 5. Report
At most 12 lines: the note's name, the mean and its trend, the top 3 tasks, and the bundle
folder (`out`), which holds transcript excerpts: the user deletes it when done. Never edit the
plugin, a repo, a skill, a backlog or a ticket, and never file anything.
