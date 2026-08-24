#!/bin/bash
set -e
BASE=${VECTORIZER_URL:-http://localhost:8091}
WORKSPACE=${WORKSPACE:-shared-proj}
ADMIN_TOKEN=${ADMIN_TOKEN:-}

auth_header() {
  if [ -n "$ADMIN_TOKEN" ]; then echo "Authorization: Bearer $ADMIN_TOKEN"; else echo "X-API-Key: ${VECTORIZER_API_KEY:-}"; fi
}

echo "=== Provision Option C: $WORKSPACE with alpha/beta/gamma, strict peer JWT ==="
echo "Base: $BASE"

# Workspace
echo "Creating workspace $WORKSPACE..."
curl -s -X POST "$BASE/api/v1/workspaces" -H "$(auth_header)" -H "Content-Type: application/json" -d "{\"name\":\"$WORKSPACE\"}" | cat; echo

# Peers + peer cards
for peer in alpha beta gamma; do
  echo "Creating peer $peer..."
  curl -s -X POST "$BASE/api/v1/workspaces/$WORKSPACE/peers" -H "$(auth_header)" -H "Content-Type: application/json" -d "{\"id\":\"$peer\"}" | cat; echo
  echo "Setting peer card $peer..."
  curl -s -X PUT "$BASE/api/v1/workspaces/$WORKSPACE/peers/$peer/card" -H "$(auth_header)" -H "Content-Type: application/json" -d "{\"lines\":[\"Role: $peer agent\",\"Workspace: $WORKSPACE\",\"Scope default: private-$peer\"]}" | cat; echo
done

# Sessions (cover all combos)
echo "Creating sessions..."
for sess in "pair-alpha-beta:alpha,beta:proj-frontend" "pair-beta-gamma:beta,gamma:proj-backend" "pair-alpha-gamma:alpha,gamma:proj-research" "sess-alpha-private:alpha:private-alpha" "sess-beta-private:beta:private-beta" "sess-gamma-private:gamma:private-gamma" "pair-all:alpha,beta,gamma:shared-all"; do
  IFS=: read sid peers scope <<< "$sess"
  peer_json=$(echo "$peers" | sed 's/,/","/g; s/^/"/; s/$/"/')
  curl -s -X POST "$BASE/api/v1/sessions" -H "$(auth_header)" -H "Content-Type: application/json" -d "{\"workspace_id\":\"$WORKSPACE\",\"session_id\":\"$sid\",\"peer_ids\":[$peer_json],\"scope\":\"$scope\"}" | cat; echo
done

# Scopes (explicit, cover as much as you can)
echo "Creating scopes..."
curl -s -X POST "$BASE/api/v1/workspaces/$WORKSPACE/scopes" -H "Content-Type: application/json" -H "$(auth_header)" -d '{"name":"proj-frontend","sessions":["pair-alpha-beta"]}' | cat; echo
curl -s -X POST "$BASE/api/v1/workspaces/$WORKSPACE/scopes" -H "Content-Type: application/json" -H "$(auth_header)" -d '{"name":"proj-backend","sessions":["pair-beta-gamma"]}' | cat; echo
curl -s -X POST "$BASE/api/v1/workspaces/$WORKSPACE/scopes" -H "Content-Type: application/json" -H "$(auth_header)" -d '{"name":"proj-research","sessions":["pair-alpha-gamma"]}' | cat; echo
curl -s -X POST "$BASE/api/v1/workspaces/$WORKSPACE/scopes" -H "Content-Type: application/json" -H "$(auth_header)" -d '{"name":"shared-all","sessions":["pair-all","pair-alpha-beta","pair-beta-gamma","pair-alpha-gamma"]}' | cat; echo
for peer in alpha beta gamma; do
  curl -s -X POST "$BASE/api/v1/workspaces/$WORKSPACE/scopes" -H "Content-Type: application/json" -H "$(auth_header)" -d "{\"name\":\"private-$peer\",\"sessions\":[\"sess-$peer-private\"]}" | cat; echo
done

echo "=== Done. Generate per-agent JWTs with: ==="
echo "export AUTH_JWT_SECRET=\$(cat .env | grep AUTH_JWT_SECRET | cut -d= -f2)"
echo "go run ./scripts/generate_jwt --workspace $WORKSPACE --peer alpha --expires 30d"
echo "go run ./scripts/generate_jwt --workspace $WORKSPACE --peer beta --expires 30d"
echo "go run ./scripts/generate_jwt --workspace $WORKSPACE --peer gamma --expires 30d"
echo "=== Strict peer check enabled: peer_id must match JWT p ==="
echo "All agents share LM Studio GGUF Qwen3 at 1536d via http://host.docker.internal:1234/v1"
