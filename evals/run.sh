#!/usr/bin/env bash
set -euo pipefail
root=$(cd "$(dirname "$0")/.." && pwd)
work=$(mktemp -d "${TMPDIR:-/tmp}/tix-evals.XXXXXX")
trap 'rm -rf "$work"' EXIT
cp -R "$root/assets/plugin" "$work/tix"
mkdir -p "$work/tix/evals"
for c in "$root"/evals/*/; do
	case $(basename "$c") in results) continue ;; esac
	cp -R "$c" "$work/tix/evals/"
done
status=0
claude plugin eval "$work/tix" --trust-plugin --scaffold --no-publish \
	--allow-tools Bash Write Edit \
	--judge-model "${TIX_EVAL_JUDGE:-sonnet}" \
	--max-cost-usd "${TIX_EVAL_MAX_USD:-10}" "$@" || status=$?
if [ -d "$work/tix/evals/results" ]; then
	mkdir -p "$root/evals/results"
	cp -R "$work/tix/evals/results/." "$root/evals/results/"
	echo "results: $root/evals/results/$(ls -t "$work/tix/evals/results" | head -n 1)"
fi
exit $status
