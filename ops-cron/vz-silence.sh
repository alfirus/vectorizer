#!/usr/bin/env bash
# vz-silence.sh — start/end an Alertmanager silence for the scheduled vectorizer backup window.
#
# usage: vz-silence.sh {start|end} [minutes]
#
# Why: a full backup streams ~15 GB across the single md3 array (read the chroma volume,
# write the tarball, re-read it for pigz). Measured live on t_551df9d9 (4 Oct): avg I/O
# queue >50 for 10+ min tripped DiskIOSaturated warning+critical on run 1, and iowait
# (counted as CPU by the `100 - idle` rule) pushed HighCPU >85% on run 2 — three
# guaranteed nightly false pages if left unmuted. The silence is created when the backup
# starts with a hard 75-minute expiry, so it always self-destructs even if the job dies;
# nothing in the Prometheus rules is modified. Anything else (SiteDown, ContainerDown,
# LowMemory, DiskFull, the vectorizer health/backup alerts) stays fully audible.
set -u
PATH=/usr/bin:/bin:/usr/sbin:/sbin
AM=http://127.0.0.1:9093/api/v2
MODE="${1:?usage: vz-silence.sh start|end [minutes]}"
MINS="${2:-75}"

case "$MODE" in
  start)
    STARTS=$(date -u +%Y-%m-%dT%H:%M:%SZ)
    ENDS=$(date -u -d "+$MINS minutes" +%Y-%m-%dT%H:%M:%SZ)
    PAYLOAD=$(jq -n --arg s "$STARTS" --arg e "$ENDS" --arg c "$(date -u +%F) vectorizer-backup-daily scheduled I/O window (t_551df9d9)" '
      { startsAt:$s, endsAt:$e, createdBy:"vectorizer-backup-daily", comment:$c,
        matchers:[
          {name:"alertname", value:"DiskIOSaturated|DStateProcesses|HighCPU", isRegex:true, isEqual:true}
        ]}')
    ID=$(curl -s --max-time 10 -X POST "$AM/silences" -H 'Content-Type: application/json' -d "$PAYLOAD" | jq -r '.silenceID // .id // empty')
    case "$ID" in
      ""|null) echo "vz-silence start FAILED (empty id)"; exit 1 ;;
    esac
    echo "$(date -u +%Y-%m-%dT%H:%M:%SZ) vz-silence start id=$ID ttl=${MINS}m matchers=alertname=~DiskIOSaturated|DStateProcesses|HighCPU"
    ;;
  end)
    # expire every silence this script created (defensive; expiry alone would suffice)
    N=0
    for SID in $(curl -s --max-time 10 "$AM/silences" \
                 | jq -r '.[] | select(.createdBy=="vectorizer-backup-daily") | .id'); do
      curl -s --max-time 10 -X PUT "$AM/silence/$SID" -H 'Content-Type: application/json' \
           -d "{\"endsAt\":\"$(date -u -d '-1 minute' +%Y-%m-%dT%H:%M:%SZ)\"}" >/dev/null && N=$((N+1))
    done
    echo "$(date -u +%Y-%m-%dT%H:%M:%SZ) vz-silence end expired=$N"
    ;;
  *) echo "vz-silence: bad mode '$MODE'"; exit 2 ;;
esac
