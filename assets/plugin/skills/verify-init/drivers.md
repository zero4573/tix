# Driver recipes

Use the repo's own harness when it has one; these are the fallbacks. Every recipe starts the
app as the note's Launch says and stops it by pid.

## HTTP service (Go, Node)
- Start it on a free port (`PORT=0` or a port from `conventions.md`), wait for the health route
  (`curl -fsS --retry 30 --retry-connrefused --retry-delay 1 http://127.0.0.1:<port>/<health>`).
- Drive with `curl -sS -o <evidence>.json -w '%{http_code}\n'` per request; save the request and
  the response body; check stored effects with the repo's own CLI or a read-only DB query.
- With a database: the ticket's `conventions.md ## Test DB` stack, never a shared one.

## Web UI (React)
- Start the dev server (`npm run dev -- --port <port>` or the repo's script) and wait for it.
- Drive with the sandbox's `pw-node <script.js>` (node with `require('playwright')` and its own
  headless Chromium; `playwright screenshot <url> <file>` for one page), locating elements by
  role, label or test id, never by position. Save a screenshot before and after each action
  (`page.screenshot({ path })`). A repo's own Playwright suite needs browsers the sandbox image
  can't run: use it only when `npx playwright test` launches.
- No `pw-node` on PATH (an older sandbox): the rows are INCONCLUSIVE; the note's `## Limits`
  says so and names the closest check (component tests with the real DOM, the API rows the UI calls).

## CLI or TUI
- Build once, then run each scenario in a fresh scratch dir with its own `HOME`.
- Plain commands: capture stdout, stderr and the exit code to evidence files.
- Interactive: `tmux new-session -d -s v<pid> '<cmd>'`, `tmux send-keys`, `tmux capture-pane -p`;
  kill the session in cleanup.

## Library
- Run the README's or the docs' examples as written against the built package; a doc example
  that doesn't run is a FAIL.
