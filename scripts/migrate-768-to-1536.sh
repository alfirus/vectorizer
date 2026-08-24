#!/bin/bash
# Migrate ws_* collections from 768d to 1536d (Qwen3). Run after switching EMBED_DIMENSIONS.
# Usage: ./scripts/migrate-768-to-1536.sh  # requires vectorizer + chromadb + qwen-embed up
set -e
echo "WARNING: This drops and re-embeds ws_* collections at 1536d. Ensure EMBED_MODEL=Qwen/Qwen3-Embedding-4B, EMBED_DIMENSIONS=1536"
read -p "Continue? y/N " ans; [[ $ans == y* ]] || exit 1
BASE=${VECTORIZER_URL:-http://localhost:8091}
echo "Listing workspaces..."
curl -s $BASE/api/v1/workspaces | python3 -m json.tool
echo "For each workspace, delete collection via Chroma API or restart with empty volume: docker compose down -v && docker compose up -d"
echo "Then re-ingest via: curl -X POST $BASE/api/v1/messages --data @-"
echo "No auto re-embed to avoid mixing 768/1536 cosine spaces."
