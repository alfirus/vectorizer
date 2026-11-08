#!/usr/bin/env bash
# vectorizer-backup-daily.sh — nightly backup of the vectorizer ChromaDB data + config.
# Installed as:  0 3 * * *  (03:00 host time, host TZ = UTC) in the prod host crontab.
# Log: /var/log/vectorizer/vectorizer-backup-daily.log  (cron appends stdout/stderr)
#
# What a backup consists of (all under ${BACKUP_ROOT:-$HOME/backups}/vectorizer/, 700, 7-day retention):
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
# Resilience (t_1c36ece4): step 0b pre-flight waits out apt/dpkg activity and recent
# dockerd restarts; step 1 retries each docker snapshot invocation 3x60s before
# failing loudly (2026-10-16: dockerd restart mid-backup killed attempt 1 outright).
# Restore: see ops-cron/README.md (live values in the private runbook).
set -uo pipefail
PATH=/usr/bin:/bin:/usr/sbin:/sbin

# Self-locating: no hardcoded server checkout path in the repo.
OPS="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
APP_DIR="$(dirname "$OPS")"
BK="${BACKUP_ROOT:-$HOME/backups}/vectorizer"
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

# --- 0b. pre-flight guard: package-manager / dockerd stability (t_1c36ece4) --------
# 2026-10-16: an apt upgrade (docker-ce + containerd.io) restarted dockerd
# mid-backup and killed the snapshot helper. If an upgrade is in flight or
# dockerd restarted recently, wait for things to settle instead of snapshotting
# into a restart. Advisory only: after PREFLIGHT_MAX_WAIT_S we proceed hot with
# a WARN — the snapshot retry loop in step 1 is the real safety net.
PREFLIGHT_STABLE_S=600
PREFLIGHT_MAX_WAIT_S=600
PREFLIGHT_POLL_S=30
apt_in_flight() {
  # prints a one-line reason and returns 0 while a package manager is active.
  # Uses fuser on the apt/dpkg locks (the real signal — held only while working)
  # plus a process check that ignores the always-present
  # unattended-upgrade-shutdown --wait-for-signal stub.
  for L in /var/lib/dpkg/lock-frontend /var/lib/dpkg/lock \
           /var/lib/apt/lists/lock /var/cache/apt/archives/lock; do
    if [ -e "$L" ] && fuser "$L" >/dev/null 2>&1; then echo "apt/dpkg lock held: $L"; return 0; fi
  done
  while IFS= read -r pid; do
    [ -n "$pid" ] || continue
    cmd=$(ps -o args= -p "$pid" 2>/dev/null) || continue
    case "$cmd" in *unattended-upgrade-shutdown*--wait-for-signal*) continue;; *) echo "package-manager active: $cmd"; return 0;; esac
  done <<PIDS
$(pgrep -f 'apt-get|aptitude|dpkg|unattended-upgr' 2>/dev/null)
PIDS
  return 1
}
dockerd_fresh() {
  # prints a one-line reason and returns 0 while dockerd restarted recently.
  age=$(ps -o etimes= -C dockerd 2>/dev/null | tr -d ' ' | head -1)
  case "$age" in ''|*[!0-9]*) echo "dockerd not visible via ps";; *)
    if [ "$age" -lt "$PREFLIGHT_STABLE_S" ]; then echo "dockerd restarted ${age}s ago (<${PREFLIGHT_STABLE_S}s stable)"; return 0; fi;; esac
  return 1
}
preflight_wait() {
  waited=0
  while [ "$waited" -lt "$PREFLIGHT_MAX_WAIT_S" ]; do
    reason=""
    r=$(apt_in_flight) && reason="$r"
    r=$(dockerd_fresh) && reason="${reason}${reason:+; }$r"
    if [ -z "$reason" ]; then
      [ "$waited" -gt 0 ] && log "pre-flight: settled after ${waited}s"
      return 0
    fi
    log "pre-flight: waiting ${PREFLIGHT_POLL_S}s ($reason) [${waited}s/${PREFLIGHT_MAX_WAIT_S}s]"
    sleep "$PREFLIGHT_POLL_S"
    waited=$(( waited + PREFLIGHT_POLL_S ))
  done
  log "WARN pre-flight: still unsettled after ${PREFLIGHT_MAX_WAIT_S}s — proceeding hot (snapshot retry is the safety net)"
  return 0
}
preflight_wait

# --- 1. consistent sqlite snapshot + write-activity check -------------------------
# If ingestion is running right now, wait (up to 2 more minutes) for a quiet window so
# the tarball can't catch a half-written .bin file. Proceeds hot with a WARN otherwise.
QUIET=0
SNAP=""
FILES=0; BYTES=0; COLS=0; DOCS=0
# t_1c36ece4: a transient docker failure (e.g. dockerd restart mid-backup ->
# `error waiting for container: unexpected EOF`) must NOT kill the nightly run
# on attempt 1. Every docker invocation gets SNAP_RETRIES tries with
# SNAP_RETRY_WAIT_S backoff, on ANY failure mode: non-zero exit, empty output,
# or non-JSON output. Only after exhausting retries do we fail loudly.
SNAP_RETRIES=3
SNAP_RETRY_WAIT_S=60
SNAP_LAST_ERR=""
run_snapshot() {
  # $1 = file to write the JSON snapshot line to; returns 0 on success, 1
  # after exhausting retries. Runs in the CURRENT shell (no command
  # substitution) so SNAP_LAST_ERR propagates to the caller's fail() line and
  # diagnostics stream straight into the cron log in real time.
  _try=1
  while [ "$_try" -le "$SNAP_RETRIES" ]; do
    _out=$(docker run --rm -v "$VOL":/data -v "$OPS":/scripts:ro -v "$BK":/backup \
             node:24-alpine node /scripts/chroma_backup.js --date "$DATE" 2>&1)
    _rc=$?
    if [ "$_rc" -eq 0 ] && [ -n "$_out" ] && echo "$_out" | jq -e . >/dev/null 2>&1; then
      echo "$_out" >"$1"
      return 0
    fi
    if [ "$_try" -lt "$SNAP_RETRIES" ]; then
      log "WARN snapshot docker run failed (quiet-attempt $attempt try $_try/$SNAP_RETRIES, rc=$_rc): $(echo "$_out" | tr '\n' '; ' | cut -c1-300) — retrying in ${SNAP_RETRY_WAIT_S}s"
      sleep "$SNAP_RETRY_WAIT_S"
    else
      SNAP_LAST_ERR="rc=$_rc out=$(echo "$_out" | tr '\n' '; ' | cut -c1-500)"
    fi
    _try=$((_try + 1))
  done
  return 1
}
for attempt in 1 2 3; do
  SNAP_JSON="$STATE/snap-$DATE.json"
  run_snapshot "$SNAP_JSON" || fail "sqlite snapshot attempt $attempt failed after $SNAP_RETRIES tries: $SNAP_LAST_ERR"
  SNAP=$(cat "$SNAP_JSON"); rm -f "$SNAP_JSON"
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
  [ -e "$APP_DIR/$f" ] && CFG_FILES="$CFG_FILES $f"
done
( cd "$APP_DIR" && tar czf "$BK/vectorizer-config-$DATE.tar.gz" $CFG_FILES ) \
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
