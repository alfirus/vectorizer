# Vectorizer MCP Server (768d)

Stdio MCP proxy for Vectorizer REST (`/api/v1/*`, `X-API-Key`).

## Tools
`vectorizer_list_workspaces`, `vectorizer_create_workspace`, `vectorizer_get_workspace_stats`, `vectorizer_health`, `vectorizer_add_message`, `vectorizer_add_messages_batch`, `vectorizer_list_messages`, `vectorizer_search` (cosine/HNSW), `vectorizer_summarize`, `vectorizer_ask` (RAG auto-search).

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
