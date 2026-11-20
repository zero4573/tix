#!/usr/bin/env bash
set -euo pipefail
mkdir -p tests
cat > clamp.sh <<'S'
clamp() {
	local v=$1 lo=$2 hi=$3
	if [ "$v" -lt "$lo" ]; then
		echo "$lo"
	elif [ "$v" -gt "$hi" ]; then
		echo "$lo"
	else
		echo "$v"
	fi
}
S
cat > tests/test_clamp_inside.sh <<'S'
. ./clamp.sh
[ "$(clamp 5 0 10)" = 5 ] || { echo "clamp 5 0 10: got $(clamp 5 0 10)"; exit 1; }
[ "$(clamp -3 0 10)" = 0 ] || { echo "clamp -3 0 10: got $(clamp -3 0 10)"; exit 1; }
S
cat > run-tests.sh <<'S'
#!/usr/bin/env bash
fail=0
for t in tests/test_*.sh; do
	if bash "$t"; then echo "ok   $t"; else echo "FAIL $t"; fail=1; fi
done
exit $fail
S
chmod +x run-tests.sh
cat > BUG.md <<'S'
# Values above the range come back as the minimum

`clamp 15 0 10` prints 0. It should print 10: a value above the range is clamped to its top.
Reported by the billing job, which clamps retry counts with it.
S
