#!/usr/bin/env bash
# vz-alert.sh — post or resolve one alert in the local Alertmanager (127.0.0.1:9093).
#
# usage: vz-alert.sh {fire|resolve} <AlertName> <severity> "<summary text>"
#
# Labels are kept IDENTICAL between fire and resolve so the fingerprint matches
# (alertmanager resolves the active alert when a copy arrives with endsAt in the past).
# severity=critical routes to telegram-owner + email-me; anything else only email-me.
# endsAt on fire = now+90m: an unfed alert self-expires ~1.5h after the last post,
# so a dead cron can never leave a stuck alert, and a live hourly job refreshes it.
set -u
PATH=/usr/bin:/bin:/usr/sbin:/sbin
AM_URL="http://127.0.0.1:9093/api/v2/alerts"

MODE="${1:?usage: vz-alert.sh fire|resolve NAME SEVERITY SUMMARY}"
NAME="${2:?missing alert name}"
SEV="${3:?missing severity}"
SUMMARY="${4:-$NAME}"

case "$MODE" in
  fire)    STARTS=$(date -u -d "-1 minute" +%Y-%m-%dT%H:%M:%SZ); ENDS=$(date -u -d "+90 minutes" +%Y-%m-%dT%H:%M:%SZ) ;;
  resolve) STARTS=$(date -u -d "-5 minutes" +%Y-%m-%dT%H:%M:%SZ); ENDS=$(date -u -d "-1 minute" +%Y-%m-%dT%H:%M:%SZ) ;;
  *) echo "vz-alert: bad mode '$MODE'" >&2; exit 2 ;;
esac

PAYLOAD=$(jq -n --arg name "$NAME" --arg sev "$SEV" --arg sum "$SUMMARY" \
                      --arg starts "$STARTS" --arg ends "$ENDS" \
  '[{ labels:      { alertname:$name, severity:$sev, component:"vectorizer", host:"ns539881" },
     annotations: { summary:$sum, runbook:"/opt/vectorizer/ops-cron/README.md" },
     startsAt:$starts, endsAt:$ends }]')

TMP=$(mktemp)
CODE=$(curl -s -o "$TMP" -w '%{http_code}' --max-time 10 -X POST "$AM_URL" \
        -H 'Content-Type: application/json' -d "$PAYLOAD" 2>/dev/null || echo 000)
if [ "$CODE" = "200" ]; then
  echo "$(date -u +%Y-%m-%dT%H:%M:%SZ) vz-alert $MODE name=$NAME severity=$SEV http=200"
  rm -f "$TMP"; exit 0
fi
echo "$(date -u +%Y-%m-%dT%H:%M:%SZ) vz-alert $MODE name=$NAME http=$CODE body=$(head -c 300 "$TMP")" >&2
rm -f "$TMP"; exit 1
