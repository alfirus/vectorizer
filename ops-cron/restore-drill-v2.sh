#!/usr/bin/env bash
# restore-drill-v2.sh — corrected restore verification (drill v1 passed vacuously:
# Chroma 1.0 /count needs a UUID, not the collection name, so every count compared
# two identical error payloads). This version resolves name -> uuid first, then:
#   1. collection sets live vs restored (id + name)
#   2. per-collection counts by UUID for a sample incl. the big sidecar
#   3. a filtered read (where workspace_id=code_aict) with documents returned
# Cleans up after itself.
set -uo pipefail
PATH=/usr/bin:/bin:/usr/sbin:/sbin

BK=/home/ubuntu/backups/vectorizer
DATE="${1:-2026-10-04}"
WORK="$BK/restore-test"
NAME=vz-restore-test
PORT=8199
TEN="http://127.0.0.1:$PORT/api/v2/tenants/default_tenant/databases/default_database"
LIVE="http://127.0.0.1:8102/api/v2/tenants/default_tenant/databases/default_database"

step() { echo "[$(date -u +%H:%M:%S)] $*"; }
cleanup() {
  docker stop "$NAME" >/dev/null 2>&1
  docker rm "$NAME" >/dev/null 2>&1
  rm -rf "$WORK"
}

step "extract chroma-volume-$DATE.tar.gz"
rm -rf "$WORK"; mkdir -p "$WORK"
pigz -dc "$BK/chroma-volume-$DATE.tar.gz" | tar -xf - -C "$WORK" || { echo EXTRACT_FAILED; exit 1; }
cp "$BK/chroma-sqlite-$DATE.db" "$WORK/chroma.sqlite3" || { echo SQLITE_SWAP_FAILED; exit 1; }
du -sh "$WORK"

step "start restored chroma on 127.0.0.1:$PORT"
docker rm "$NAME" >/dev/null 2>&1 || true
docker run -d --name "$NAME" -p "127.0.0.1:$PORT:8000" -v "$WORK":/data chromadb/chroma:1.0.0 >/dev/null || exit 1
CODE=000
for _ in $(seq 1 45); do
  CODE=$(curl -s -o /dev/null -w '%{http_code}' --max-time 3 "$TEN/collections" || echo 000)
  [ "$CODE" = "200" ] && break
  sleep 2
done
echo "restored-list-http=$CODE"
if [ "$CODE" != "200" ]; then docker logs --tail 40 "$NAME" 2>&1; cleanup; exit 1; fi

step "1) collection sets live vs restored"
curl -s --max-time 20 "$LIVE/collections"  | jq -S -c '[.[]|{id,name}]|sort_by(.name)' > /tmp/live_colls.json
curl -s --max-time 20 "$TEN/collections" | jq -S -c '[.[]|{id,name}]|sort_by(.name)' > /tmp/rest_colls.json
LIVE_N=$(jq length /tmp/live_colls.json); REST_N=$(jq length /tmp/rest_colls.json)
SET_SAME=$(cmp -s /tmp/live_colls.json /tmp/rest_colls.json && echo yes || echo no)
echo "collections: live=$LIVE_N restored=$REST_N identical_set=$SET_SAME"

step "2) per-collection counts by UUID (sample)"
FAILS=0
for COLL in ws_code_aict_codeedges ws_code_ags ws_code_vectorizer probe-content-only ws_alya_conclusions ws_code_aict; do
  UUID=$(jq -r --arg n "$COLL" '.[]|select(.name==$n)|.id' /tmp/rest_colls.json)
  L=$(curl -s --max-time 20 "$LIVE/collections/$UUID/count")
  R=$(curl -s --max-time 20 "$TEN/collections/$UUID/count")
  if [ "$L" = "$R" ] && [[ "$L" =~ ^[0-9]+$ ]]; then S=MATCH; else S=BAD; FAILS=$((FAILS+1)); fi
  echo "count $COLL uuid=${UUID:0:8}… live=$L restored=$R $S"
done

step "3) filtered read with documents (the t_4375d9e7 class of check)"
EDGE_UUID=$(jq -r '.[]|select(.name=="ws_code_aict_codeedges")|.id' /tmp/rest_colls.json)
curl -s --max-time 30 -X POST "$TEN/collections/$EDGE_UUID/get" \
  -H 'Content-Type: application/json' \
  -d '{"where":{"workspace_id":"code_aict"},"limit":3,"include":["metadatas","documents"]}' \
  > /tmp/rest_get.json
jq -c '{rows:(.ids|length), ws:.metadatas[0].workspace_id, doc_prefix:(.documents[0][0:60])}' /tmp/rest_get.json
ROWS=$(jq -r '.ids|length' /tmp/rest_get.json 2>/dev/null || echo 0)
WS=$(jq -r '.metadatas[0].workspace_id' /tmp/rest_get.json 2>/dev/null || echo none)
if [ "${ROWS:-0}" -lt 3 ] || [ "$WS" != "code_aict" ]; then FAILS=$((FAILS+1)); echo "READ_CHECK=BAD"; else echo "READ_CHECK=OK"; fi

step "4) total embedding rows restored"
python3 - <<'EOF' 2>/dev/null || echo "host sqlite count skipped"
import sqlite3
c = sqlite3.connect("/home/ubuntu/backups/vectorizer/restore-test/chroma.sqlite3")
print("restored sqlite embeddings rows:", c.execute("select count(*) from embeddings").fetchone()[0])
EOF

step "summary"
if [ "$FAILS" -eq 0 ] && [ "$SET_SAME" = "yes" ] && [ "$CODE" = "200" ]; then
  echo "DRILL2_RESULT=PASS collections=$REST_N counts=all-match read=ok"
else
  echo "DRILL2_RESULT=FAIL fails=$FAILS set_same=$SET_SAME"
fi
cleanup
echo "DRILL2_DONE"
