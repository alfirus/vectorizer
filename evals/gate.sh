#!/bin/sh
# evals/gate.sh — release gate: fail below EVAL_MIN_RECALL on the smoke set.
# Usage: EVAL_MIN_RECALL=0.8 BASE=http://localhost:8091 evals/gate.sh
set -eu
BASE="${BASE:-http://localhost:8091}"
FILE="${FILE:-evals/data/sample.jsonl}"
MIN="${EVAL_MIN_RECALL:-0.8}"

OUT="$(go run evals/run.go -base "$BASE" -file "$FILE" 2>&1)"
echo "$OUT"
RECALL="$(printf '%s\n' "$OUT" | sed -n 's/.*recall \([0-9]*\)\/\([0-9]*\) = \([0-9.]*\).*/\3/p' | tail -1)"
if [ -z "$RECALL" ]; then
  echo "gate: could not parse recall from harness output" >&2
  exit 1
fi
# shellcheck disable=SC1102
awk -v r="$RECALL" -v m="$MIN" 'BEGIN { if (r + 0 < m + 0) { printf "gate: recall %s < min %s\n", r, m; exit 1 } printf "gate: recall %s >= min %s — PASS\n", r, m }'
