# Vectorizer Memory Skill

Teach agents to use Vectorizer (self-hosted, 768d `nomic-embed-text-v2` via LM Studio, workspace-isolated ChromaDB `ws_<id>`).

## When to use MCP tools (22 total: 8 messages + 5 brain + 3 peers + 3 workspace + 3 provenance)
- **After every user/assistant turn:** `vectorizer_add_message {workspace_id, session_id, role, content}` (auto-chunks 4000, embeds 768d)
- **Before answering any question:** `vectorizer_search {query, workspace_id, n_results:5}` — inject top results into prompt. Scoped searches ALSO see the shared `_global` workspace (identity facts live there).
- **Long context / recap:** `vectorizer_summarize {workspace_id, session_id}` or `vectorizer_list_messages {workspace_id, session_id}`
- **RAG Q&A:** `vectorizer_ask {question, workspace_id}` (auto-searches if `context` omitted; 404 abstains when nothing relevant — do NOT confabulate, say you don't know)
- **Cross-workspace:** `vectorizer_search_all {query}` (parallel fan-out, score-sorted)

## Forget / correct (new — wrong facts are NOT immortal)
- `vectorizer_get_message {id, workspace_id?}` — fetch all chunks for a message
- `vectorizer_delete_message {id, workspace_id?}` — forget: deletes ALL chunks
- `vectorizer_update_message {id, content, workspace_id?, session_id?, role?}` — correct in place (same ID, fresh embedding; role/session recovered from stored chunks when omitted)
- Conclusions: `DELETE /api/v1/conclusions/:id?workspace_id=...`

## Provenance + hygiene (new 2026-09)
- `vectorizer_trace {workspace_id, id, direction: forward|reverse, depth?}` — forward = "why do I believe X?" (conclusion → premises → messages); reverse = "what breaks if I forget X?" (blast radius). Check reverse BEFORE deleting anything.
- `vectorizer_stale {workspace_id, max_age_days?, limit?}` — dead-knowledge proposals (old, never-reinforced, non-timeless). Review, then confirm via delete/TTL. Nothing auto-deletes.
- `vectorizer_brief {workspace_id, peer_id?, max_conclusions?, include_stale?}` — one-shot session-start overview (stats + repr + recent + entities). Use this instead of 3 separate calls.

## Relevance rules (2026-09: floors everywhere)
- `Ask` / chat seed / `search_memory` / `search_messages` all enforce `RAG_MIN_SCORE` (default 0.22, score = 1 − distance). Below floor = abstain, never summarize junk.
- Empty tool result = "nothing relevant" signal, NOT an error. Tell the user you couldn't find it.
- Timeless facts (`role=system`, `importance>=4`) skip time-decay; everything else decays after 7d.
- Chat answers only auto-persist as conclusions when the seed had a relevant hit.

## Rules
- `workspace_id = agent identity` (e.g. `hermes`, `opencode`), isolation via `ws_<workspace_id>` collection.
- Shared identity facts (family, user profile) go to workspace `_global` — visible to every scoped search.
- Always set `session_id` (conversation/thread id). Same workspace, different session = separate history but searchable together.
- Chunking 4000 chars is automatic — pass full content.
- If no MCP: fallback to REST `POST /api/v1/messages`, `POST /api/v1/messages/search`, `GET /api/v1/messages?workspace_id=…` with `X-API-Key`.

## Setup
Requires `mcp.vectorizer` configured (`VECTORIZER_URL=http://100.90.123.105:8091` live, `http://localhost:8091` local dev). If missing, use REST directly.
