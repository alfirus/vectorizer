#!/usr/bin/env bash
# seed.sh — drive the demo stack end to end (t_6d2db280).
# Usage (from the repo root):
#   docker compose -f demo/docker-compose.demo.yml up -d --build
#   ./demo/seed.sh
#
# Everything that needs embeddings is gated on /health reporting
# embedding "ok" (i.e. LM Studio reachable). Without it the script still
# runs the health + workspace steps and the offline chunk preiew, then
# exits 0 with a pointer instead of failing.
set -uo pipefail

BASE="${VECTORIZER_URL:-http://127.0.0.1:8091}/api/v1"
KEY="${VECTORIZER_API_KEY:-demo-key-change-me}"
WS=acme

say() { printf '\n==> %s\n' "$*"; }
have_jq=1; command -v jq >/dev/null || have_jq=0
pp() { if [ "$have_jq" = 1 ]; then jq .; else cat; fi; }
auth=(-H "Authorization: Bearer $KEY")

say "1/4 health (waits up to ~90s for the container)"
for i in $(seq 1 45); do
  if curl -sf "$BASE/health" -o /tmp/vz-demo-health.json 2>/dev/null; then break; fi
  sleep 2
done
curl -sf "$BASE/health" | pp || { echo "FATAL: vectorizer not up at $BASE"; exit 1; }
EMBED=$(jq -r '.embedding // "unknown"' /tmp/vz-demo-health.json 2>/dev/null || echo unknown)
echo "embedding status: $EMBED"

say "2/4 create workspace '$WS' (idempotent)"
curl -sf -X POST "$BASE/workspaces" "${auth[@]}" \
  -H 'Content-Type: application/json' -d '{"name":"'"$WS"'"}' | pp

if [ "$EMBED" != "ok" ]; then
  say "3/4 SKIPPED store+search (no embedding endpoint)."
  echo "Start LM Studio (text-embedding-nomic-embed-text-v2 on :1234),"
  echo "'docker compose -f demo/docker-compose.demo.yml restart vectorizer',"
  echo "then re-run ./demo/seed.sh for the full loop."
  say "offline preview: markdown chunker over the sample vault (no server needed)"
  VAULT_ROOT=demo/vault python3 scripts/vault_index.py --workspace "$WS" --dry-run
  say "demo partial-OK (health + workspace + dry-run)"
  exit 0
fi

say "3/4 store three sample memories"
curl -sf -X POST "$BASE/messages/batch" "${auth[@]}" \
  -H 'Content-Type: application/json' -d '{
  "messages": [
    {"workspace_id":"'"$WS"'","session_id":"demo-seed-01","role":"user",
     "content":"Acme 150T press brake on line 3 needs blade replacement every 4000 cycles",
     "metadata":{"source_path":"acme/vault/press-brake-blades.md"}},
    {"workspace_id":"'"$WS"'","session_id":"demo-seed-01","role":"user",
     "content":"Line 3 safety: confirm the press-brake light curtain shows green LEDs before every shift",
     "metadata":{"source_path":"acme/vault/safety-checklist.md"}},
    {"workspace_id":"'"$WS"'","session_id":"demo-seed-02","role":"user",
     "content":"Decision 2026-09-18: retrofit the line-3 conveyor, revisit replace option in Q2 if vibration climbs",
     "metadata":{"source_path":"acme/vault/line3-upgrade-decision.md"}}
  ]}' | pp

say "4/4 hybrid search"
curl -sf -X POST "$BASE/workspaces/$WS/search" "${auth[@]}" \
  -H 'Content-Type: application/json' \
  -d '{"query":"press brake maintenance interval","n_results":3}' \
  | ( [ "$have_jq" = 1 ] && jq '.results[] | {score, source, snippet: .document[0:80]}' || cat )

say "demo OK — try: vault dry-run: VAULT_ROOT=demo/vault python3 scripts/vault_index.py --workspace acme --dry-run"
