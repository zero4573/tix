---
name: verify-init
description: Write a repo's verification note, the scripted way to launch the app, check it is healthy, drive it the way a user does and capture evidence, so any agent can prove acceptance criteria on the real surface. Reads the repo rather than asking, proves the note once end to end, and drafts it for the vault (projects/<slug>/<slug>-verify.md, promoted by /tix:save). Use as /tix:verify-init <repo>, or when a task file lists skills [tix:verify-init]. Adapted from pstack's create-verification-skill.
argument-hint: "<repo slug>"
---

# /tix:verify-init <repo>

The note is read cold, mid-task, by an agent that has never seen the app: exact commands, real
selectors and routes from this repo, no placeholders. One note per repo; the method is the same
for every repo, the driver recipes are in `drivers.md` next to this file.

## 1. Read the repo, not the user
Answer from the code; ask only what the code can't show.
- **Surface:** what a user touches (an HTTP API, a web UI, a CLI, a library); the main one first.
- **Run:** the repo's own dev command (`AGENTS.md`, Makefile, package scripts, README), ports,
  env vars, seed data, auth. It must reach a usable state from an empty data dir.
- **Drive:** an existing harness first (Playwright specs, HTTP test clients, scripts); else the
  recipe in `drivers.md` for the surface.
- **Observe:** what proves behaviour: response bodies, screenshots, terminal output, rows, logs.
- **Isolate:** can two instances run side by side (ports, data dirs)? If not, say so.
- **Sandbox:** what the session can't run (a browser, a container runtime, a service): probe it.
If the checkout doesn't build or start, fix that inside your task's `owns` or report it.

## 2. Write the draft
In a ticket: `tickets/<source>/<ID>/kb-drafts/<slug>-verify.md`; in a repo or spec session:
`inbox/<slug>-verify.md` with `session:`. Frontmatter: `type: reference`, `project: <slug>`,
`target: projects/<slug>` (or `update-of: <slug>-verify` when one exists), `tags: [verify]`.
Sections, each grounded in what step 1 found:
- `## Launch`: the command, how to tell it's ready (a log line, a health URL), teardown.
- `## Doctor`: one read-only check that the instance is worth driving (up, right build, our port).
- `## Drive`: the recipe with this repo's real routes, selectors or commands.
- `## Evidence`: what to capture and where (`tickets/<source>/<ID>/evidence/`); drive the real
  user path, never test-only endpoints; check side effects (rows, files, messages) too.
- `## Cleanup`: stop what you started by pid; evidence survives cleanup.
- `## Features`: the 3-5 main user-facing features, each with how to reach it, how to drive it,
  and the observable end state that proves it works.
- `## Limits`: what can't run in the sandbox, and the closest check that can.

## 3. Prove it once
Follow the draft end to end: launch, doctor, drive one feature, capture evidence, clean up, and
confirm the evidence survived. Fix what fails and rerun cleanup after each failed attempt. A
note never run is a draft, not a deliverable. Report the run (commands, exit codes, evidence
path) under `Results:` (a task) or in your reply.

Adapted from cursor/plugins pstack (MIT); see THIRD_PARTY_NOTICES.md.
