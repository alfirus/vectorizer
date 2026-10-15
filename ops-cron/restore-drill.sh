#!/usr/bin/env bash
# restore-drill.sh — one-time verification that the nightly vectorizer backup actually
# restores into a working Chroma. Compares restored data against live production.
set -uo pipefail
PATH=/usr/bin:/bin:/usr/sbin:/sbin

BK=/home/ubuntu/backups/vectorizer
DATE="${1:-2026-10-04}"
WORK="$BK/restore-test"
NAME=vz-restore-test
PORT=8199
LIVE=http://127.0.0.1:8102/api/v2/tenants/default_tenant/databases/default_database
TEST=http://127.0.0.1:$PORT/api/v2/tenants/default_tenant/databases/default_database

step() { echo "[$(date -u +%H:%M:%S)] $*"; }

step "extract $(basename "$BK/chroma-volume-$DATE.tar.gz")"
rm -rf "$WORK"; mkdir -p "$WORK"
pigz -dc "$BK/chroma-volume-$DATE.tar.gz" | tar -xf - -C "$WORK" || { echo EXTRACT_FAILED; exit 1; }
du -sh "$WORK"
step "swap in the consistent sqlite copy"
cp "$BK/chroma-sqlite-$DATE.db" "$WORK/chroma.sqlite3" || exit 1
ls -la "$WORK/chroma.sqlite3"

step "start restored chroma on 127.0.0.1:$PORT"
docker rm "$NAME" >/dev/null 2>&1 || true
docker run -d --name "$NAME" -p "127.0.0.1:$PORT:8000" -v "$WORK":/data chromadb/chroma:1.0.0 >/dev/null || exit 1
CODE=000
for _ in $(seq 1 45); do
  CODE=$(curl -s -o /dev/null -w '%{http_code}' --max-time 3 http://127.0.0.1:$PORT/api/v2/version || echo 000)
  [ "$CODE" = "200" ] && break
  sleep 2
done
echo "restored-chroma-http=$CODE version=$(curl -s --max-time 3 http://127.0.0.1:$PORT/api/v2/version)"
if [ "$CODE" != "200" ]; then
  echo "--- container logs:"; docker logs --tail 40 "$NAME" 2>&1
  docker rm "$NAME" >/dev/null 2>&1 || true; rm -rf "$WORK"; exit 1
fi

step "collection counts live vs restored"
LIVE_N=$(curl -s --max-time 10 "$LIVE/collections" | jq length)
REST_N=$(curl -s --max-time 10 "$TEST/collections" | jq length)
echo "collections: live=$LIVE_N restored=$REST_N $([ "$LIVE_N" = "$REST_N" ] && echo MATCH || echo MISMATCH)"

MISMATCH=0
for coll in ws_code_aict_codeedges ws_code_ags ws_code_vectorizer probe-content-only ws_alya_conclusions; do
  L=$(curl -s --max-time 10 "$LIVE/collections/$coll/count")
  R=$(curl -s --max-time 10 "$TEST/collections/$coll/count")
  if [ "$L" = "$R" ]; then S=MATCH; else S=MISMATCH; MISMATCH=1; fi
  echo "count $coll: live=$L restored=$R $S"
done

step "read-path proof on restored data (filtered get, the check that failed in t_4375d9e7)"
curl -s --max-time 15 -X POST "$TEST/collections/ws_code_aict_codeedges/get" \
  -H 'Content-Type: application/json' \
  -d '{"where":{"workspace_id":"code_aict"},"limit":3,"include":["metadatas"]}' \
  | jq -c '{rows:(.ids|length), sample_ws:.metadatas[0].workspace_id}'

step "summary"
if [ "$MISMATCH" -eq 0 ] && [ "$LIVE_N" = "$REST_N" ] && [ "$CODE" = "200" ]; then
  echo "DRILL_RESULT=PASS (restored copy matches live on all spot checks)"
else
  echo "DRILL_RESULT=FAIL"
fi

step "cleanup"
docker stop "$NAME" >/dev/null 2>&1
docker rm "$NAME" >/dev/null 2>&1
rm -rf "$WORK"
echo "DRILL_DONE"
