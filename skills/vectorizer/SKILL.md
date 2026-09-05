# Vectorizer Memory Skill

Teach agents to use Vectorizer (self-hosted, 768d `nomic-embed-text-v2` via LM Studio, workspace-isolated ChromaDB `ws_<id>`).

## When to use MCP tools (25 total: 8 messages + 5 brain + 3 provenance + 3 code + 3 peers + 6 workspace)
- **After every user/assistant turn:** `vectorizer_add_message {workspace_id, session_id, role, content}` (auto-chunks 4000, embeds 768d)
- **Before answering any question:** `vectorizer_search {query, workspace_id, n_results:5}` — inject top results into prompt. Scoped searches ALSO see the shared `_global` workspace (identity facts live there).
- **Long context / recap:** `vectorizer_summarize {workspace_id, session_id}` or `vectorizer_list_messages {workspace_id, session_id}`
- **RAG Q&A:** `vectorizer_ask {question, workspace_id}` (auto-searches if `context` omitted; 404 abstains when nothing relevant — do NOT confabulate, say you don't know)
- **Cross-workspace:** `vectorizer_search_all {query}` (parallel fan-out, score-sorted)

## Forget / correct (new — wrong facts are NOT immortal)
- `vectorizer_get_message {id, workspace_id?}` — fetch all chunks for a message
- `vectorizer_delete_message {id, workspace_id?}` — forget: deletes ALL chunks
- `vectorizer_update_message {id, content, workspace_id?, session_id?, role?}` — correct in place (same ID, fresh embedding; role/session recovered from stored chunks when omitted)
- `vectorizer_update_message {id, sections: {"Heading": "new body"}, workspace_id?}` — SPLICE: rewrite only named `##` sections, re-embeds only changed chunks, ID stable (trace edges survive). Prefer splice over full rewrite for long docs.
- Conclusions: `DELETE /api/v1/conclusions/:id?workspace_id=...`

## Codebase Q&A (new 2026-09 — structural, no grep)
- `vectorizer_index_repo {path, workspace_id?}` — index a server-local repo path (container sees `/data/...` mounts; `/data/repo` = vectorizer source). Per-symbol chunks + DEFINES/CALLS/IMPORTS reasoning edges.
- `vectorizer_symbols {workspace_id, file?}` — symbols in a file (name, kind, signature).
- `vectorizer_callers {workspace_id, symbol}` — "what calls X?" from CALLS edges. Use before refactoring.
- Pitfall: reasoning-chain responses nest fields under `metadata` (`e["metadata"]["premise_ids"]`, NOT `e["premise_ids"]`).

## "index this project" (2026-09: magic phrase → code index)
User saying "index this project" means: stage the project onto the server, index it, confirm searchable. Steps:
1. Workspace name = `code_<folder>` (lowercase, dashes→underscores).
2. Copy code to server: `tar` the project (exclude `node_modules .next .git dist build`) → `/opt/code-stage/<name>/` via SSH (`ubuntu@100.90.123.105`, key `~/.ssh/personal`). Server path = `/data/code-stage/<name>`.
3. `vectorizer_index_repo {path: "/data/code-stage/<name>", workspace_id: "code_<name>"}` — budget ~15s/file embed time (LM Studio); if client times out, the server keeps working — poll with `vectorizer_symbols`.
4. Verify: `vectorizer_symbols {workspace_id}` junk-check (no `vault-data/`/`exports/` dumps), then `vectorizer_callers` on a known symbol. Report files/symbols/edges.
5. Re-index later = just repeat 2–4 (unchanged files skip by hash, only new/changed embed — fast).
- Pitfall: the server ONLY sees container mounts (`/data/repo`, `/data/code-stage`) — laptop paths never work directly. Always stage first.

## "scan this project" (2026-09: briefing + issue hunt)
After indexing (or on an already-indexed `code_<name>` workspace):
1. Briefing from the index — structure + entry points + key modules via `vectorizer_symbols`, imports via CALLS/IMPORTS edges (`vectorizer_callers` on entry symbols), README/docs via `vectorizer_search {workspace_id: "code_<name>"}`.
2. Issue hunt — TODO/FIXME/XXX + secrets (`sk-`, `AKIA`, `BEGIN PRIVATE KEY`) via `vectorizer_search` on the code workspace; dead code via `vectorizer_callers` = empty on exported symbols; risks = error-swallows (`_ =`) + missing-timeout HTTP clients + unguarded deletes.
3. Deliver: briefing first, then findings ranked by risk. Offer to file fixes, don't just list.

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

## Slow-model backpressure (2026-09)
Embed + brain hit LM Studio over LAN — a slow model queues requests server-side. The server now defends itself: per-attempt timeouts (`EMBED_TIMEOUT_SECS` 300 / `LLM_TIMEOUT_SECS` 600), 429/5xx retry with backoff + `Retry-After`, singleflight dedup of identical embeds, concurrency caps (`EMBED_MAX_INFLIGHT` 4 / `LLM_MAX_INFLIGHT` 2). If ingests feel slow, raise the timeouts — don't hammer retries; bursts queue in-process, not in LM Studio.
