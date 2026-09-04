# Vectorizer MCP Server (1536d (Qwen3-Embedding-4B MRL))

Stdio MCP proxy for Vectorizer REST (`/api/v1/*`, `X-API-Key`).

## Tools
`vectorizer_list_workspaces`, `vectorizer_create_workspace`, `vectorizer_get_workspace_stats`, `vectorizer_health`, `vectorizer_add_message`, `vectorizer_add_messages_batch`, `vectorizer_list_messages`, `vectorizer_get_message`, `vectorizer_delete_message`, `vectorizer_update_message`, `vectorizer_search` (cosine/HNSW, scoped + shared `_global` scope), `vectorizer_search_all`, `vectorizer_summarize`, `vectorizer_ask` (RAG auto-search, abstains 404 when nothing relevant — do NOT confabulate), `vectorizer_trace` (provenance: forward why-believe / reverse blast-radius), `vectorizer_stale` (dead-knowledge review proposals), `vectorizer_brief` (one-shot session overview).

## Env
`VECTORIZER_URL` (default `http://localhost:8091`), `VECTORIZER_API_KEY`, `VECTORIZER_WORKSPACE_ID` (optional default).

## Run
```bash
cd mcp && npm install && npm run build
VECTORIZER_URL=http://localhost:8091 node dist/index.js  # stdio
```

## Clients
```json
// opencode.json / claude.json
{"mcpServers":{"vectorizer":{"command":"node","args":["/path/vectorizer/mcp/dist/index.js"],"env":{"VECTORIZER_URL":"http://localhost:8091","VECTORIZER_API_KEY":"..."}}}}
```
Hosted: `npx mcp-remote https://mcp.vectorizer.dev --header "X-API-Key:$KEY"` (requires Worker deploy).
