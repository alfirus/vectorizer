#!/usr/bin/env bash
# vectorizer-health-1h.sh — hourly probe of the vectorizer stack on the prod host.
# Installed as:  0 * * * *  in the prod host crontab.
# Log: /var/log/vectorizer/vectorizer-health-1h.log
#
# Checks (one "[ts] OK|FAIL check=... detail=..." line each, then a SUMMARY line):
#   vectorizer-api   REST health on 127.0.0.1:8091
#   chromadb         version endpoint on 127.0.0.1:8102 + collections count > 0
#   chromadb-read    sampled readability: resolve 3 fixed collections to UUIDs,
#                    GET /count each (expect numeric) + one POST /get limit=1
#                    (expect ids). Catches the compactor/backfill fault class
#                    (t_d1b3b72e) that stays green on version+count>0 alone.
#                    Sample: ws_code_ags + probe-content-only + ws_global (the
#                    _global workspace's canonical collection since 807760b;
#                    the pre-canonical raw name ws__global is tolerated as a
#                    read-only legacy alias, never required).
#                    Recurrence runbook: cd <server-app-dir> && ./up.sh recreate
#                    vectorizer-chromadb (never bare -f; see up.sh header).
#   dashboard        HTTP on 127.0.0.1:8092 (200/307)
#   mcp              HTTP answer on 127.0.0.1:8093 (any code = alive; 000 = down)
#   containers       vectorizer / vectorizer-mcp / vectorizer-dashboard / vectorizer-chromadb
#                    running, and healthy when the container defines a healthcheck
#   backup-freshness newest chroma-volume-*.tar.gz is <= 26h old (skipped while the
#                    backup lock is held, i.e. a run is in flight) — this is the
#                    watchdog that makes "silently missing backup" impossible
#
# Alerting: failures POST to Alertmanager 127.0.0.1:9093 (vz-alert.sh) as
#   VectorizerHealth        severity=critical -> owner Telegram + email
#   VectorizerBackupStale   severity=critical -> owner Telegram + email
# A firing alert is re-posted every hour (refreshing its 90m endsAt) until checks pass,
# then resolved explicitly. Alertmanager itself is monitored by the Prometheus
# ContainerDown rules from t_4a12ed0f, so "probe down" is covered there.
set -uo pipefail
PATH=/usr/bin:/bin:/usr/sbin:/sbin

OPS="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
BK="${BACKUP_ROOT:-$HOME/backups}/vectorizer"
STATE=$HOME/.local/state/vectorizer-ops
LOCK="$BK/.backup.lock"
MAX_BACKUP_AGE_S=$(( 26 * 3600 ))

mkdir -p "$STATE"
log() { echo "$(date -u +%Y-%m-%dT%H:%M:%SZ) $*"; }

SVC_FAILS=()      # -> VectorizerHealth
BK_FAIL=""        # -> VectorizerBackupStale

emit() { # emit OK|FAIL name detail
  log "$1 check=$2 detail=$3"
}

# --- HTTP checks -------------------------------------------------------------------
http_check() { # name url expect(codes, comma) 
  local name="$1" url="$2" expect="$3" code
  code=$(curl -s -o /dev/null -w '%{http_code}' --max-time 8 "$url" || echo 000)
  case ",$expect," in
    *",$code,"*) emit OK "$name" "http=$code $url"; return 0 ;;
    *) emit FAIL "$name" "http=$code $url (want $expect)"; SVC_FAILS+=("$name http=$code"); return 1 ;;
  esac
}
http_check vectorizer-api http://127.0.0.1:8091/api/v1/health 200
http_check dashboard      http://127.0.0.1:8092/              200,307

# chromadb: version + real data present + sampled readability (compactor-fault class)
CH_BASE=http://127.0.0.1:8102/api/v2/tenants/default_tenant/databases/default_database/collections
CH_CODE=$(curl -s -o /dev/null -w '%{http_code}' --max-time 8 http://127.0.0.1:8102/api/v2/version || echo 000)
CH_OK=no
if [ "$CH_CODE" = "200" ]; then
  COL_JSON=$(curl -s --max-time 8 "$CH_BASE" 2>/dev/null || true)
  NCOL=$(printf '%s' "$COL_JSON" | jq 'if type=="array" then length else (.collections|length) end' 2>/dev/null || echo 0)
  if [ "${NCOL:-0}" -gt 0 ] 2>/dev/null; then
    emit OK chromadb "http=200 collections=$NCOL"
    CH_OK=yes
  else
    emit FAIL chromadb "reachable but collections=$NCOL"
    SVC_FAILS+=("chromadb collections=$NCOL")
  fi
else
  emit FAIL chromadb "http=$CH_CODE"
  SVC_FAILS+=("chromadb http=$CH_CODE")
fi

# sampled per-collection count/get probe (t_4a2e438e; fault class t_d1b3b72e).
# Fixed 3-collection sample keeps the hourly run cheap; read-only, never wipes.
# The _global workspace's canonical collection is ws_global: ResolveWorkspaceID
# canonicalizes "_global" -> "global" (807760b), so GetCollectionName("_global")
# is "ws_global" (same for the ws_global_conclusions/_reasoning sidecars live
# on prod). The pre-canonical raw concatenation ws__global is tolerated as a
# read-only legacy alias — a leftover legacy collection must never false-page,
# but neither name existing IS a failure (t_89fd9d50: false critical 01:00 UTC
# 2026-11-08 when the probe required ws__global while prod only had ws_global).
if [ "$CH_OK" = "yes" ]; then
  READ_FAILS=()
  READ_OK=()
  GET_ID=""
  resolve_id() { # <collection-name> -> id or empty
    printf '%s' "$COL_JSON" | jq -r --arg n "$1" '.[] | select(.name==$n) | .id // empty' 2>/dev/null || true
  }
  probe_one() { # <label> <collection-id>
    local _label="$1" _pid="$2" _cnt
    [ -z "$GET_ID" ] && GET_ID="$_pid:$_label"
    _cnt=$(curl -s --max-time 8 "$CH_BASE/$_pid/count" 2>/dev/null || true)
    case "$_cnt" in
      ''|*[!0-9]*) READ_FAILS+=("$_label:count=$(printf '%.120s' "$_cnt")") ;;
      *) READ_OK+=("$_label=$_cnt") ;;
    esac
  }
  for _name in ws_code_ags probe-content-only; do
    _id=$(resolve_id "$_name")
    if [ -z "$_id" ]; then
      READ_FAILS+=("$_name:missing")
      continue
    fi
    probe_one "$_name" "$_id"
  done
  _global_id=$(resolve_id ws_global)
  if [ -n "$_global_id" ]; then
    probe_one ws_global "$_global_id"
  else
    _legacy_id=$(resolve_id ws__global)
    if [ -n "$_legacy_id" ]; then
      probe_one "ws__global[legacy-alias-of-ws_global]" "$_legacy_id"
    else
      READ_FAILS+=("ws_global:missing (legacy ws__global also absent)")
    fi
  fi
  if [ "${#READ_OK[@]}" -gt 0 ]; then
    _gid=${GET_ID%%:*}; _gname=${GET_ID#*:}
    _get=$(curl -s --max-time 8 -X POST "$CH_BASE/$_gid/get" \
      -H 'Content-Type: application/json' -d '{"limit":1,"include":["documents"]}' 2>/dev/null || true)
    _nids=$(printf '%s' "$_get" | jq '.ids | length' 2>/dev/null || echo 0)
    if [ "${_nids:-0}" -gt 0 ] 2>/dev/null; then
      READ_OK+=("get($_gname)=ok")
    else
      READ_FAILS+=("get($_gname)=$(printf '%.120s' "$_get")")
    fi
  fi
  if [ "${#READ_FAILS[@]}" -eq 0 ]; then
    emit OK chromadb-read "${READ_OK[*]}"
  else
    emit FAIL chromadb-read "${READ_FAILS[*]}"
    SVC_FAILS+=("chromadb-read ${READ_FAILS[*]}")
  fi
fi

# mcp: any HTTP status proves the process answers (404 on / is normal)
M_CODE=$(curl -s -o /dev/null -w '%{http_code}' --max-time 8 http://127.0.0.1:8093/ || echo 000)
if [ "$M_CODE" != "000" ]; then emit OK mcp "http=$M_CODE"; else emit FAIL mcp "no answer on 127.0.0.1:8093"; SVC_FAILS+=("mcp down"); fi

# --- container checks ---------------------------------------------------------------
for c in vectorizer vectorizer-mcp vectorizer-dashboard vectorizer-chromadb; do
  INFO=$(docker inspect -f '{{.State.Status}} {{if .State.Health}}{{.State.Health.Status}}{{else}}no-healthcheck{{end}}' "$c" 2>/dev/null) \
    || INFO="missing"
  ST=${INFO%% *}; HC=${INFO#* }
  if [ "$ST" = "running" ] && { [ "$HC" = "healthy" ] || [ "$HC" = "no-healthcheck" ]; }; then
    emit OK "container:$c" "status=$ST health=$HC"
  else
    emit FAIL "container:$c" "status=$ST health=$HC"
    SVC_FAILS+=("$c $ST/$HC")
  fi
done

# --- backup freshness ---------------------------------------------------------------
# the lock file itself persists after a run — probe whether it is HELD right now
if ! flock -n "$LOCK" -c true 2>/dev/null; then
  emit OK backup-freshness "backup in flight (lock held) — age check skipped"
else
  NEWEST=$(find "$BK" -maxdepth 1 -name 'chroma-volume-*.tar.gz' -printf '%T@\n' 2>/dev/null | sort -n | tail -1)
  if [ -z "$NEWEST" ]; then
    emit FAIL backup-freshness "no chroma-volume-*.tar.gz in $BK"
    BK_FAIL="no backup archive exists yet in $BK"
  else
    AGE=$(( $(date +%s) - ${NEWEST%.*} ))
    HUMAN=$(date -u -d "@${NEWEST%.*}" +%Y-%m-%dT%H:%M:%SZ)
    if [ "$AGE" -le "$MAX_BACKUP_AGE_S" ]; then
      emit OK backup-freshness "age=$((AGE/3600))h newest=$HUMAN"
    else
      emit FAIL backup-freshness "age=$((AGE/3600))h (>26h) newest=$HUMAN"
      BK_FAIL="newest backup is $((AGE/3600))h old ($HUMAN)"
    fi
  fi
fi

# --- alert state machine ------------------------------------------------------------
NF=${#SVC_FAILS[@]}
BK_STALE=no; [ -n "$BK_FAIL" ] && BK_STALE=yes
log "SUMMARY status=$([ "$NF" -eq 0 ] && [ "$BK_STALE" = no ] && echo ok || echo FAIL) service_fails=$NF backup_stale=$BK_STALE"

HOST="$(hostname)"
if [ "$NF" -gt 0 ]; then
  if [ ! -f "$STATE/health_failed" ]; then
    "$OPS/vz-alert.sh" fire VectorizerHealth critical \
      "vectorizer stack unhealthy on $HOST: ${SVC_FAILS[*]}" \
      && touch "$STATE/health_failed" || log "ALERT_DELIVERY_FAILED VectorizerHealth"
  else
    "$OPS/vz-alert.sh" fire VectorizerHealth critical \
      "vectorizer stack still unhealthy on $HOST: ${SVC_FAILS[*]}" \
      || log "ALERT_DELIVERY_FAILED VectorizerHealth"
  fi
elif [ -f "$STATE/health_failed" ]; then
  if "$OPS/vz-alert.sh" resolve VectorizerHealth critical "vectorizer stack healthy again"; then
    rm -f "$STATE/health_failed"
  fi
fi

if [ -n "$BK_FAIL" ]; then
  "$OPS/vz-alert.sh" fire VectorizerBackupStale critical \
    "vectorizer backup stale/missing on $HOST: $BK_FAIL" \
    && touch "$STATE/backup_stale" || log "ALERT_DELIVERY_FAILED VectorizerBackupStale"
elif [ -f "$STATE/backup_stale" ]; then
  if "$OPS/vz-alert.sh" resolve VectorizerBackupStale critical "vectorizer backups fresh again"; then
    rm -f "$STATE/backup_stale"
  fi
fi

exit 0
