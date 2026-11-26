#!/usr/bin/env bash
# install.sh — idempotent installer for the vectorizer backup + health cron jobs (<prod-host>).
# Safe to re-run: it only adds what is missing and never touches existing crontab lines.
set -euo pipefail
PATH=/usr/bin:/bin:/usr/sbin:/sbin

# Self-locating: the ops dir is wherever this checkout lives (no hardcoded
# server path — live values stay out of the repo).
OPS="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
LOGDIR=/var/log/vectorizer
BK="${BACKUP_ROOT:-$HOME/backups}/vectorizer"
MARKER="vectorizer-backup-daily"

echo "== log dir =="
if [ ! -d "$LOGDIR" ]; then
  sudo mkdir -p "$LOGDIR"
  sudo chown ubuntu:ubuntu "$LOGDIR"
  sudo chmod 750 "$LOGDIR"
  echo "created $LOGDIR (owner ubuntu)"
else
  echo "$LOGDIR already exists"
fi

echo "== script perms =="
chmod 755 "$OPS"/*.sh
chmod 644 "$OPS"/*.js "$OPS"/README.md

echo "== dirs =="
mkdir -p "$BK" "$HOME/.local/state/vectorizer-ops"
chmod 700 "$BK"

echo "== crontab =="
if crontab -l 2>/dev/null | grep -q "$MARKER"; then
  echo "cron entries already present:"
else
  { crontab -l 2>/dev/null; cat <<EOF
# >>> vectorizer-ops (t_551df9d9) >>>
# vectorizer-backup-daily: nightly ChromaDB volume + sqlite snapshot + config bundle, 03:00 UTC
0 3 * * * $OPS/vectorizer-backup-daily.sh >> /var/log/vectorizer/vectorizer-backup-daily.log 2>&1
# vectorizer-health-1h: hourly stack probe + backup freshness watchdog, alerts via alertmanager
0 * * * * $OPS/vectorizer-health-1h.sh >> /var/log/vectorizer/vectorizer-health-1h.log 2>&1
# <<< vectorizer-ops (t_551df9d9) <<<
EOF
  } | crontab -
  echo "installed"
fi
crontab -l | grep -A2 -B1 "vectorizer-ops" || true

echo "== off-host replication (t_d7130e95 + t_6d2db280) =="
if command -v restic >/dev/null; then
  echo "restic present: $(restic version | head -n 1)"
else
  echo "restic NOT installed — off-host sync stays SKIP-green until it is"
  echo " (owner action: sudo apt install restic, then provision /root/.restic/)"
fi
SUDOERS_DROP=/etc/sudoers.d/vectorizer-offhost
if [ -f "$SUDOERS_DROP" ]; then
  echo "sudoers drop-in already present ($SUDOERS_DROP) — untouched"
else
  echo "installing sudoers drop-in for the nightly off-host call (backup-daily runs sudo -n as ubuntu)"
  printf 'ubuntu ALL=(root) NOPASSWD: %s/vectorizer-offhost-sync.sh *\n' "$OPS" | sudo tee "$SUDOERS_DROP" >/dev/null
  sudo chmod 0440 "$SUDOERS_DROP"
  if sudo visudo -c -f "$SUDOERS_DROP" >/dev/null; then
    echo "sudoers drop-in installed + validated"
  else
    echo "sudoers validation FAILED — removing drop-in, fix manually"
    sudo rm -f "$SUDOERS_DROP"
    exit 1
  fi
fi
if sudo test -f /root/.restic/vectorizer-offhost.env 2>/dev/null; then
  echo "off-host repo provisioned (env file present)"
else
  echo "off-host repo NOT provisioned — sync logs SKIP (green) until it is"
  echo " (owner action, private runbook: DSM share + svc user, repo passphrase in"
  echo "  /root/.restic/vectorizer-offhost.env, VAULT_DATA_DIR for the ~11G vault)"
fi

echo "== sanity: shell syntax =="
for f in "$OPS"/*.sh; do bash -n "$f" && echo "OK $f"; done

echo "== install done =="
