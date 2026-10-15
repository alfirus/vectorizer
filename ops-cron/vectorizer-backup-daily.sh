#!/usr/bin/env bash
# vectorizer-backup-daily.sh — nightly backup of the vectorizer ChromaDB data + config.
# Installed as:  0 3 * * *  (03:00 host time, host TZ = UTC) in ubuntu's crontab on ns539881.
# Log: /var/log/vectorizer/vectorizer-backup-daily.log  (cron appends stdout/stderr)
#
# What a backup consists of (all under /home/ubuntu/backups/vectorizer/, 700, 7-day retention):
#   chroma-volume-<date>.tar.gz      the WHOLE chroma_data docker volume — segment .bin
#                                    files hold the actual embedding vectors (sqlite does
#                                    NOT), so this is the only complete copy of the data
#   chroma-sqlite-<date>.db          consistent online copy of chroma.sqlite3 (VACUUM INTO)
#                                    — metadata/FTS; the live db inside the tarball may be
#                                    torn if a write landed during the copy
#   vectorizer-config-<date>.tar.gz  .env + compose files + up.sh (secrets -> 600)
#
# On any failure: fires VectorizerBackupFailed (critical -> owner Telegram + email) and
# exits 1; the hourly health job independently watches backup age (VectorizerBackupStale).
# Step 5b (t_d7130e95): off-host sync via vectorizer-offhost-sync.sh (restic-over-ssh
# to the office Synology) runs inside this same 03:00 slot after step 5 — no second
# schedule. Until the repo is provisioned it logs SKIP and stays green.
# Restore: see /opt/vectorizer/ops-cron/README.md.
set -uo pipefail
PATH=/usr/bin:/bin:/usr/sbin:/sbin

OPS=/opt/vectorizer/ops-cron
BK=/home/ubuntu/backups/vectorizer
STATE=$HOME/.local/state/vectorizer-ops
VOL=vectorizer_chroma_data
DATE=$(date +%F)
LOCK="$BK/.backup.lock"

mkdir -p "$BK" "$STATE"
chmod 700 "$BK"

log() { echo "$(date -u +%Y-%m-%dT%H:%M:%SZ) $*"; }

fail() {
  log "FAIL $*"
  mkdir -p "$STATE"
  touch "$STATE/backup_failed"
  "$OPS/vz-alert.sh" fire VectorizerBackupFailed critical \
    "vectorizer nightly backup ($DATE) FAILED: $* — see /var/log/vectorizer/vectorizer-backup-daily.log" || true
  exit 1
}

# one instance only (health job watches this lock to know a backup is in flight;
# it also grabs it for a millisecond to probe liveness, hence the retry)
exec 9>"$LOCK"
HELD=0
for _ in 1 2 3 4 5 6 7 8 9 10; do
  flock -n 9 && { HELD=1; break; }
  sleep 3
done
[ "$HELD" = "1" ] || { log "SKIP another backup instance still holds $LOCK after 30s"; exit 0; }

# ~15 GB of I/O on md3 reliably trips DiskIOSaturated/DStateProcesses (measured on the
# first run, t_551df9d9) -> mute exactly those two for the window, self-expiring in 75m
"$OPS/vz-silence.sh" start 75 || log "WARN could not create the backup I/O silence"

# --- 0. chroma must be reachable -------------------------------------------------
curl -sf -m 10 http://127.0.0.1:8102/api/v2/version >/dev/null \
  || fail "chromadb not reachable on 127.0.0.1:8102"

# --- 1. consistent sqlite snapshot + write-activity check -------------------------
# If ingestion is running right now, wait (up to 2 more minutes) for a quiet window so
# the tarball can't catch a half-written .bin file. Proceeds hot with a WARN otherwise.
QUIET=0
SNAP=""
FILES=0; BYTES=0; COLS=0; DOCS=0
for attempt in 1 2 3; do
  SNAP=$(docker run --rm -v "$VOL":/data -v "$OPS":/scripts:ro -v "$BK":/backup \
           node:24-alpine node /scripts/chroma_backup.js --date "$DATE" 2>&1) \
    || fail "sqlite snapshot attempt $attempt failed: $SNAP"
  echo "$SNAP" | jq -e . >/dev/null 2>&1 || fail "snapshot returned non-JSON: $SNAP"
  FILES=$(echo "$SNAP" | jq -r .files)
  BYTES=$(echo "$SNAP" | jq -r .bytes)
  COLS=$(echo "$SNAP" | jq -r .collections)
  DOCS=$(echo "$SNAP" | jq -r .embeddings)
  INTEG=$(echo "$SNAP" | jq -r .integrity)
  NEWEST=$(echo "$SNAP" | jq -r .newest_mtime_epoch)
  [ "$INTEG" = "ok" ] || fail "sqlite copy integrity_check = $INTEG"
  [ "$COLS" -gt 0 ] || fail "snapshot reports 0 collections"
  AGE=$(( $(date +%s) - NEWEST ))
  if [ "$AGE" -ge 120 ]; then QUIET=1; break; fi
  log "attempt $attempt: last chroma write ${AGE}s ago (<120s) — waiting 60s for a quiet window"
  sleep 60
done
[ "$QUIET" = "1" ] || log "WARN hot backup: chroma was still being written to, tarball may contain a file mid-write (sqlite copy is consistent regardless)"
log "snapshot ok: files=$FILES bytes=$BYTES collections=$COLS docs=$DOCS integrity=$INTEG"

# --- 2. full volume tarball -------------------------------------------------------
TAR="$BK/chroma-volume-$DATE.tar"
log "tar start -> $TAR"
docker run --rm -v "$VOL":/data:ro alpine tar -C /data -cf - . > "$TAR" \
  || { rm -f "$TAR"; fail "tar of $VOL failed"; }

TAR_ENTRIES=$(tar -tf "$TAR" | grep -vc '/$' || true)
[ "$TAR_ENTRIES" -ge "$FILES" ] || { rm -f "$TAR"; fail "archive has $TAR_ENTRIES files, live volume has $FILES"; }
log "tar ok: entries=$TAR_ENTRIES (live files=$FILES) size=$(stat -c%s "$TAR")"

# --- 3. compress + verify ---------------------------------------------------------
log "pigz start"
pigz -6 -p 4 -f "$TAR" || fail "pigz failed on $TAR"
GZ="$TAR.gz"
gzip -t "$GZ" || fail "gzip integrity test failed on $GZ"
log "gz ok: $GZ size=$(stat -c%s "$GZ")"

# --- 4. config bundle -------------------------------------------------------------
CFG_FILES=""
for f in .env docker-compose.yml docker-compose.server.yml docker-compose.override.yml up.sh; do
  [ -e "/opt/vectorizer/$f" ] && CFG_FILES="$CFG_FILES $f"
done
( cd /opt/vectorizer && tar czf "$BK/vectorizer-config-$DATE.tar.gz" $CFG_FILES ) \
  || fail "config bundle failed"
chmod 600 "$BK/vectorizer-config-$DATE.tar.gz"
log "config ok: vectorizer-config-$DATE.tar.gz size=$(stat -c%s "$BK/vectorizer-config-$DATE.tar.gz")"

# --- 5. retention (7 days) --------------------------------------------------------
for pat in 'chroma-volume-*.tar.gz' 'chroma-sqlite-*.db' 'vectorizer-config-*.tar.gz'; do
  find "$BK" -maxdepth 1 -name "$pat" -mtime +7 -delete 2>/dev/null
done

# --- 5b. off-host sync (restic-over-ssh to office Synology, t_d7130e95) --------------
# Runs AFTER local artifacts are written + verified (extends this job; no second
# schedule). vectorizer-offhost-sync.sh runs as root via a narrow sudoers rule
# (root-owned 0600 creds), exits 0 with SKIP until the owner provisions the repo
# (due 2026-10-10), and fails loudly once provisioned -> surfaces as
# VectorizerBackupFailed via fail() below.
if ! sudo -n "$OPS/vectorizer-offhost-sync.sh" "$DATE"; then
  fail "off-host sync failed — see /var/log/vectorizer/vectorizer-backup-sync.log"
fi

# --- 6. summary + clear any previous failure alert -------------------------------
log "SUMMARY ok date=$DATE quiet=$QUIET files=$FILES bytes=$BYTES collections=$COLS docs=$DOCS gz_bytes=$(stat -c%s "$GZ") kept=$(find "$BK" -maxdepth 1 -name 'chroma-volume-*.tar.gz' | wc -l)"
if [ -f "$STATE/backup_failed" ]; then
  "$OPS/vz-alert.sh" resolve VectorizerBackupFailed critical "vectorizer nightly backup is succeeding again" && rm -f "$STATE/backup_failed"
fi
exit 0
