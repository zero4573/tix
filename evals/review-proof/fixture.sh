#!/usr/bin/env bash
set -euo pipefail
mkdir -p vault/tickets/manual/MAN-1/reviews vault/templates repo/tests
cat > vault/tickets/manual/MAN-1/MAN-1.md <<'S'
---
summary: Bounded job queue
status: in-progress
source: manual
---
# Bounded job queue

The job runner needs a file-backed queue that holds at most N jobs.

| ID | Criterion | Surface | Scenario | Pass | Evidence |
|---|---|---|---|---|---|
| AC-1 | push beyond the capacity is refused | cli | `q_new 2; q_push a; q_push b; q_push c` | the third push exits 1 and the queue still holds 2 jobs | |
| AC-2 | pop returns jobs in push order | cli | `q_push a; q_push b; q_pop; q_pop` | prints a then b | |
S
cat > vault/templates/review.md <<'S'
# Review

Each finding ≤6 lines: `W<N>-<repo>-<M|S|C><nn> · file:line · problem · fix · test · confirmed|unconfirmed`;
a must-fix is `confirmed` by a run (the command and the output it printed).

## Summary
## Must-fix
## Should-fix
## Consider
## Keep as pattern
## Dropped
## Previous findings
## Dispositions
S
cd repo
git init -q -b main .
git config user.name "Eval User"
git config user.email "eval@example.com"
git config commit.gpgsign false
cat > run-tests.sh <<'S'
#!/usr/bin/env bash
fail=0
for t in tests/test_*.sh; do
	if bash "$t"; then echo "ok   $t"; else echo "FAIL $t"; fail=1; fi
done
exit $fail
S
chmod +x run-tests.sh
git add -A
git commit -q -m init
git checkout -q -b feature/MAN-1
cat > queue.sh <<'S'
Q=${Q:-./queue.txt}
QMAX=${QMAX:-10}

q_new() {
	QMAX=$1
	: > "$Q"
}

q_len() {
	wc -l < "$Q" | tr -d ' '
}

q_push() {
	if [ "$(q_len)" -gt "$QMAX" ]; then
		return 1
	fi
	printf '%s\n' "$1" >> "$Q"
}

q_pop() {
	[ -s "$Q" ] || return 1
	head -n 1 "$Q"
	tail -n +2 "$Q" > "$Q.tmp" && mv "$Q.tmp" "$Q"
}
S
cat > tests/test_queue_order.sh <<'S'
cd "$(mktemp -d)"
. "$OLDPWD/queue.sh"
q_new 5
q_push a
q_push b
[ "$(q_pop)" = a ] || { echo "first pop"; exit 1; }
[ "$(q_pop)" = b ] || { echo "second pop"; exit 1; }
S
git add -A
git commit -q -m "Add a bounded file-backed job queue"
