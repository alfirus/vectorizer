#!/usr/bin/env bash
#
# up.sh — THE canonical live entry point for the vectorizer stack (ns539881).
#
#   ./up.sh up [args...]        build+start (passes args to `compose up -d`)
#   ./up.sh recreate <svc...>   force-recreate services (picks up .env changes)
#   ./up.sh build <svc...>      rebuild images (passes args to `compose build`)
#   ./up.sh verify              live-config check only (ports + mounts + env + health)
#
# It ALWAYS pins BOTH compose files:
#   -f docker-compose.server.yml -f docker-compose.override.yml
#
# WHY: any explicit `-f` disables compose's auto-load of docker-compose.override.yml.
# A bare `-f docker-compose.server.yml` recreate SILENTLY drops the untracked
# live-only override, which carries:
#   - vectorizer-mcp tailnet binding 100.90.123.105:8093 (fleet's ONLY remote MCP path)
#   - vectorizer mounts /opt/vectorizer->/data/repo:ro + /opt/code-stage->/data/code-stage:ro
#   - VAULT_WRITEBACK=true
# That exact mistake caused the fleet-wide Vectorizer memory outage on 2026-10-03
# 03:44 UTC. NEVER recreate with a bare -f set — always go through this script.
#
# The override stays UNTRACKED on purpose (live-only paths/bindings; pulls never
# conflict). This script contains no secrets and is safe to commit.
#
set -euo pipefail
cd "$(dirname "$0")"

BASE="docker-compose.server.yml"
OVERRIDE="docker-compose.override.yml"

if [[ ! -f "$BASE" ]]; then
  echo "FATAL: $BASE missing in $(pwd)" >&2
  exit 1
fi
if [[ ! -f "$OVERRIDE" ]]; then
  echo "FATAL: $OVERRIDE missing in $(pwd) — refusing to run without live config." >&2
  echo "A recreate without it would silently drop the tailnet MCP binding, live mounts," >&2
  echo "and VAULT_WRITEBACK (2026-10-03 outage). Restore the file first." >&2
  exit 1
fi

COMPOSE=(docker compose -f "$BASE" -f "$OVERRIDE")

usage() {
  echo "usage: ./up.sh {up|recreate|build|verify} [service...] [extra compose args]" >&2
  echo "  up        compose up -d (default when services given: up -d --no-deps)" >&2
  echo "  recreate  compose up -d --force-recreate (for .env changes)" >&2
  echo "  build     compose build" >&2
  echo "  verify    live-config check only" >&2
  exit 1
}

wait_healthy() {
  # After a recreate, containers sit inside their healthcheck start_period
  # (vectorizer-mcp: 20s) — an immediate /health curl fails even though the
  # config is correct. Wait until every recreated container reports healthy
  # (or Running without a healthcheck) before verify runs its checks.
  local deadline=$((SECONDS + 120))
  while [[ $SECONDS -lt $deadline ]]; do
    local bad=0
    while IFS= read -r line; do
      case "$line" in
        *"(healthy)"*|*"Up "*"(health: starting)"*) : ;;
        *"Up "*) : ;;  # Running, no healthcheck (e.g. chromadb shows bare Up)
        *) bad=1; break ;;
      esac
    done < <(docker ps --format '{{.Names}} {{.Status}}' --filter "label=com.docker.compose.project=vectorizer" 2>/dev/null)
    [[ "$bad" -eq 0 ]] && return 0
    sleep 5
  done
  echo "WARN: containers not all healthy within 120s — running verify anyway:" >&2
  docker ps --format '{{.Names}} {{.Status}}' --filter "label=com.docker.compose.project=vectorizer" >&2
}

verify() {
  local fail=0
  echo "--- verify: vectorizer-mcp ports (expect 127.0.0.1:8093 AND 100.90.123.105:8093) ---"
  docker port vectorizer-mcp || fail=1
  docker port vectorizer-mcp 2>/dev/null | grep -q "100.90.123.105:8093" \
    || { echo "VERIFY-FAIL: tailnet 100.90.123.105:8093 binding missing on vectorizer-mcp" >&2; fail=1; }
  echo "--- verify: vectorizer mounts (expect vault-data, code-stage, repo) ---"
  docker inspect vectorizer --format '{{range .Mounts}}{{.Source}} -> {{.Destination}} ({{.Mode}}){{println}}{{end}}' || fail=1
  for want in "/opt/vectorizer/vault-data -> /data/ai" "/opt/code-stage -> /data/code-stage" "/opt/vectorizer -> /data/repo"; do
    docker inspect vectorizer --format '{{range .Mounts}}{{.Source}} -> {{.Destination}}{{println}}{{end}}' 2>/dev/null | grep -qF "$want" \
      || { echo "VERIFY-FAIL: mount missing: $want" >&2; fail=1; }
  done
  echo "--- verify: vectorizer env (expect VAULT_WRITEBACK=true) ---"
  docker inspect vectorizer --format '{{range .Config.Env}}{{println .}}{{end}}' | grep -E '^(VAULT_WRITEBACK|VAULT_ROOT)=' || fail=1
  docker inspect vectorizer --format '{{range .Config.Env}}{{println .}}{{end}}' | grep -q '^VAULT_WRITEBACK=true$' \
    || { echo "VERIFY-FAIL: VAULT_WRITEBACK=true missing on vectorizer" >&2; fail=1; }
  echo "--- verify: on-host REST health ---"
  curl -s -m 10 http://127.0.0.1:8091/api/v1/health | head -c 400; echo
  echo "--- verify: tailnet MCP health (fleet path, 60s retry for post-recreate start) ---"
  local i
  for i in 1 2 3 4 5 6; do
    curl -s -m 10 http://100.90.123.105:8093/health && { mcp_ok=1; break; }
    sleep 10
  done
  [[ "${mcp_ok:-0}" -eq 1 ]] || { echo "VERIFY-FAIL: tailnet MCP /health unreachable after 60s" >&2; fail=1; }
  echo
  if [[ "$fail" -ne 0 ]]; then
    echo "VERIFY: FAILED — live config drifted. Do not leave the stack like this." >&2
    return 1
  fi
  echo "VERIFY: OK — live config intact."
}

ACTION="${1:-up}"
case "$ACTION" in
  up)
    shift || true
    "${COMPOSE[@]}" up -d "$@"
    wait_healthy
    verify
    ;;
  recreate)
    shift || true
    [[ $# -ge 1 ]] || usage
    "${COMPOSE[@]}" up -d --force-recreate "$@"
    wait_healthy
    verify
    ;;
  build)
    shift || true
    [[ $# -ge 1 ]] || usage
    "${COMPOSE[@]}" build "$@"
    ;;
  verify)
    verify
    ;;
  -h|--help|help)
    usage
    ;;
  *)
    echo "unknown action: $ACTION" >&2
    usage
    ;;
esac
