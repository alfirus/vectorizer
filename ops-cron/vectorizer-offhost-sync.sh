#!/usr/bin/env bash
# vectorizer-offhost-sync.sh — off-host copy of the nightly vectorizer backup (t_d7130e95).
# Target (per decision t_df104a39): office Synology NAS via restic-over-ssh
#   repo: /volume1/backups/ns539881/vectorizer/ (adapt if DSM volume differs; record actual)
# Runs as ROOT via a narrow sudoers rule (it reads root-owned 0600 credentials).
# Called by vectorizer-backup-daily.sh AFTER local artifacts are written + verified:
#   sudo -n /opt/vectorizer/ops-cron/vectorizer-offhost-sync.sh <YYYY-MM-DD>
#
# Behaviour:
#   - repo/creds not provisioned yet -> log SKIP, exit 0 (nightly stays green; the
#     freshness alert on the last-success marker is what pages when stale).
#   - provisioned -> restic backup of exactly the three dated artifacts, then
#     `forget --keep-daily 7 --prune` ONLY after a successful backup.
#   - any failure after provisioning -> non-zero exit (daily job fires
#     VectorizerBackupFailed). Never prints secrets: the repo password is passed
#     by file reference only and this script runs with tracing off.
# Out of scope (never included): /opt/vectorizer/vault-data (11 GB).
set -uo pipefail
PATH=/usr/bin:/bin:/usr/sbin:/sbin

BK=/home/ubuntu/backups/vectorizer
ENV_FILE=/root/.restic/vectorizer-offhost.env   # root-owned 0600, sets RESTIC_REPOSITORY (+ optional RESTIC_PASSWORD_FILE override)
PASS_FILE=/root/.restic/vectorizer-repo-pass   # root-owned 0600, the restic repo password
SYNC_LOG=/var/log/vectorizer/vectorizer-backup-sync.log
MARKER=/var/log/vectorizer/offhost-last-success
DATE="${1:-$(date +%F)}"

log() { echo "$(date -u +%Y-%m-%dT%H:%M:%SZ) $*" >>"$SYNC_LOG"; }

# --- gate: nothing provisioned yet -> SKIP green ------------------------------------
if [ ! -f "$ENV_FILE" ]; then
  log "SKIP date=$DATE reason=repo-not-provisioned (owner action: DSM share + svc user + repo passphrase, due 2026-10-10)"
  echo "offhost SKIP (repo not provisioned)"
  exit 0
fi

# shellcheck disable=SC1090
set -a; . "$ENV_FILE"; set +a
: "${RESTIC_REPOSITORY:?RESTIC_REPOSITORY not set in $ENV_FILE}"
RESTIC_PASSWORD_FILE="${RESTIC_PASSWORD_FILE:-$PASS_FILE}"
export RESTIC_PASSWORD_FILE
[ -f "$RESTIC_PASSWORD_FILE" ] || { log "FAIL date=$DATE reason=password-file-missing($RESTIC_PASSWORD_FILE)"; echo "offhost FAIL (password file missing)"; exit 1; }
command -v restic >/dev/null || { log "FAIL date=$DATE reason=restic-not-installed"; echo "offhost FAIL (restic missing)"; exit 1; }

VOL_TAR="$BK/chroma-volume-$DATE.tar.gz"
SQLITE_DB="$BK/chroma-sqlite-$DATE.db"
CFG_TAR="$BK/vectorizer-config-$DATE.tar.gz"
for f in "$VOL_TAR" "$SQLITE_DB" "$CFG_TAR"; do
  [ -s "$f" ] || { log "FAIL date=$DATE reason=artifact-missing($f)"; echo "offhost FAIL (artifact missing)"; exit 1; }
done
gzip -t "$VOL_TAR" || { log "FAIL date=$DATE reason=vol-tarball-corrupt"; echo "offhost FAIL (tarball corrupt)"; exit 1; }

# --- backup (content-addressed: re-runs upload only missing blobs -> resumable) -----
OUT=$(restic -r "$RESTIC_REPOSITORY" backup --tag "vectorizer-$DATE" \
  "$VOL_TAR" "$SQLITE_DB" "$CFG_TAR" 2>&1) || {
  log "FAIL date=$DATE reason=restic-backup-exit-nonzero out=$(echo "$OUT" | tail -n 3 | tr '\n' ';')"
  echo "offhost FAIL (restic backup)"
  exit 1
}
SNAP=$(echo "$OUT" | grep -oE 'snapshot [0-9a-f]+ saved' | awk '{print $2}' || true)
ADDED=$(echo "$OUT" | grep -oE 'Added to the repository: [^,]+' | head -n 1 || true)

# --- retention AFTER success only (7 days, matching the local job) ------------------
restic -r "$RESTIC_REPOSITORY" forget --keep-daily 7 --prune --tag vectorizer >/dev/null 2>&1 \
  || log "WARN date=$DATE reason=forget-prune-failed (backup itself succeeded)"

# --- marker (atomic; freshness alert in vectorizer-health-1h depends on this) ---------
date -u +%Y-%m-%dT%H:%M:%SZ >"$MARKER.tmp" && mv "$MARKER.tmp" "$MARKER"
chmod 644 "$MARKER" "$SYNC_LOG" 2>/dev/null || true

log "OK date=$DATE snapshot=${SNAP:-unknown} ${ADDED:-bytes-unknown} marker=$MARKER"
echo "offhost OK snapshot=${SNAP:-unknown}"
exit 0
