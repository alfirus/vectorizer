---
name: vectorizer-integration
description: Setup Vectorizer SDK/MCP in a codebase (env, docker, health check)
---
# Vectorizer Integration

1. `cp .env.example .env` → set `EMBED_PROVIDER`, `EMBED_MODEL=nomic-embed-text` (768d), `CHROMA_HOST`, `DEFAULT_API_KEY`.
2. `docker compose up -d` → verify `curl http://localhost:8091/api/v1/health` returns `chromadb: ok`.
3. MCP: add to `opencode.json` / `claude.json`: `{"mcpServers":{"vectorizer":{"command":"node","args":["./mcp/dist/index.js"],"env":{"VECTORIZER_URL":"http://localhost:8091"}}}}`
4. SDK: `npm add @vectorizer/sdk` or `pip install vectorizer-ai` (see `sdks/`).
5. Verify: `vectorizer_search {query:"hello", workspace_id:"test"}` returns 200.

Run `npx skills add vectorizer` + `/vectorizer-integration`.
