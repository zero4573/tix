# Model per task

Read when planning a wave (lead skill, 4. Implement).

The agent type sets the default (`tix:task`, `tix:mechanical`: sonnet; `tix:reviewer`: opus, a
model other than the tasks' default so the review doesn't share their blind spots; `tix:reviewer-deep`: fable). Choose per task while planning, write it to the task file's `model:`
(empty: the default) and copy it into the `Spawn:` entry as `model:`; the task file keeps it for
the next lead after a rotation.
- `haiku`: `tix:mechanical` only, when the task file gives every edit or command exactly (doc
  rows, a rename list, a golden regen and its command) and no logic changes.
- empty: feature and fix work inside the existing design.
- `fable`: critical tasks, where a mistake is costly or hard to undo: auth, secrets,
  permissions, data migrations, persisted formats, crash-window steps, and what runs or is
  read on the host outside the sandbox.
- `opus`: engine, contract, transaction or concurrency design, and a change along a call path
  shared by several packages.
- A task that came back `partial` or failed on difficulty (not on missing information or a
  decision) gets one rerun or follow-up a step up (empty → `opus`; `opus` → `fable`). A
  `fable` task that fails, or a second failure, goes through the gate.
- `effort: high` (no model change) for a `tix:task` routine in design but fiddly in detail (many
  edge cases, parsing, ordering); `tix:mechanical` already runs at `effort: low`.

Never `haiku` for `tix:task` or a reviewer, nor anything below `fable` for a critical task; a
review's model is its agent type (`tix:reviewer` or `tix:reviewer-deep`), never a `model:`. In kiro the subagent runs its agent's own model and these
fields don't apply.
