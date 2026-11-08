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

echo "== sanity: shell syntax =="
for f in "$OPS"/*.sh; do bash -n "$f" && echo "OK $f"; done

echo "== install done =="
