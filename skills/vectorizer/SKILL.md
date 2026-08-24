# Vectorizer Memory Skill

Teach agents to use Vectorizer (self-hosted, 1536d (Qwen3-Embedding-4B MRL) `Qwen/Qwen3-Embedding-4B (1536d MRL, nomic-embed-text 768d fallback)`, workspace-isolated ChromaDB `ws_<id>`).

## When to use MCP tools
- **After every user/assistant turn:** `vectorizer_add_message {workspace_id, session_id, role, content}` (auto-chunks 4000, embeds 1536d (Qwen3-Embedding-4B MRL))
- **Before answering any question:** `vectorizer_search {query, workspace_id, n_results:5}` — inject top results into prompt
- **Long context / recap:** `vectorizer_summarize {workspace_id, session_id}` or `vectorizer_list_messages {workspace_id, session_id}`
- **RAG Q&A:** `vectorizer_ask {question, workspace_id}` (auto-searches if `context` omitted)

## Rules
- `workspace_id = agent identity` (e.g. `hermes`, `opencode`), isolation via `ws_<workspace_id>` collection.
- Always set `session_id` (conversation/thread id). Same workspace, different session = separate history but searchable together.
- Chunking 4000 chars is automatic — pass full content.
- If no MCP: fallback to REST `POST /api/v1/messages`, `POST /api/v1/messages/search`, `GET /api/v1/messages?workspace_id=…` with `X-API-Key`.

## Setup
Requires `mcp.vectorizer` configured (`VECTORIZER_URL=http://localhost:8091`). If missing, use REST directly.
