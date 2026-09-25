---
name: vectorizer
description: Use when you need to remember, recall, or correct facts across sessions — Vectorizer semantic memory on the live server, your workspace {{workspace}}. Also for indexing/searching programming projects (code_* workspaces).
---
# Vectorizer — your semantic memory

Vectorizer is the team's self-hosted semantic memory server (live production): ChromaDB + 768d `nomic-embed-text-v2` embeddings, markdown-truth vault, workflow librarian rerank. API base: `http://100.90.123.105:8091/api/v1` (Tailscale; `GET /api/v1/health` is public).

**Your identity on this server:**
- Your workspace: `{{workspace}}` — everything you store goes here. `workspace_id` = agent identity (Chroma collection `ws_{{workspace}}`, fully isolated).
- Your agent header: `X-Agent: {{agent_name}}` on EVERY call (the usage dashboard attributes activity to you; names are normalized lowercase).
- Auth: `X-API-Key: vectorizer-local-key`.

## REST quick reference (curl — always works)

```
# store one message (chunking + 768d embedding are automatic)
curl -s -X POST http://100.90.123.105:8091/api/v1/messages \
  -H "X-API-Key: vectorizer-local-key" -H "X-Agent: {{agent_name}}" -H "Content-Type: application/json" \
  -d '{"workspace_id":"{{workspace}}","session_id":"<session-id>","role":"assistant","content":"<fact or record>",\
       "metadata":{"source_type":"conversation","tags":"decisions","importance":3,"agent":"{{agent_name}}"}}'

# search your workspace (hybrid = vector + identifier-aware BM25)
curl -s -X POST http://100.90.123.105:8091/api/v1/messages/search \
  -H "X-API-Key: vectorizer-local-key" -H "X-Agent: {{agent_name}}" -H "Content-Type: application/json" \
  -d '{"query":"<what you need>","n_results":5,"where":{"workspace_id":"{{workspace}}","hybrid":true}}'

# cross-workspace recall (all agents, RRF-fused)
curl -s -X POST http://100.90.123.105:8091/api/v1/messages/search/all ... -d '{"query":"...","n_results":10}'
```

Batch store: `POST /api/v1/messages/batch` `{"workspace_id":"{{workspace}}","messages":[...]}` (batches of ~10).

Your profile is wired to the shared MCP bridge (`http://100.90.123.105:8093/mcp`, `X-Agent: {{agent_name}}` header). Prefer the MCP tools over raw curl where they're loaded — they register as `mcp_vectorizer_vectorizer_*` (e.g. `mcp_vectorizer_vectorizer_search`, `mcp_vectorizer_vectorizer_add_message`; 25 tools: messages, brain, provenance, code, peers, workspace) with the same semantics as the REST calls below. If you don't see them in this session (server changes need an agent restart), fall back to the REST calls below.

## Recall/record loop
1. **Session start (recap or long task):** `GET /api/v1/conclusions/brief?workspace_id={{workspace}}` — stats + representation + recent + top entities in ONE call.
2. **Before answering a factual question:** search first (`n_results:5`), inject hits into your context. Empty result = "nothing relevant" — say so, NEVER confabulate (the relevance floor `RAG_MIN_SCORE=0.22` / `RAG_MAX_DISTANCE=0.78` already dropped junk).
3. **After each meaningful turn:** store the durable fact/decision (not chat noise). Always set `session_id` (conversation/thread id) — same workspace + different sessions = separate history, searchable together.
4. **End of task:** store the outcome/decision with `importance` 3-4.

## What to store (metadata)
`role`: `user`/`assistant`/`system` — `system` + `importance >= 4` = timeless identity facts (skip time-decay).
`importance`: 1-2 ephemeral, 3 normal, 4 durable, 5 identity. `tags`: comma-separated. Long content (>4000 chars) auto-chunks.

## Correct / forget — wrong facts are NOT immortal
- `PUT /api/v1/messages/:id` `{"workspace_id":"{{workspace}}","content":"..."}` — full replace, same id, fresh embedding.
- `PUT /api/v1/messages/:id` `{"workspace_id":"{{workspace}}","sections":{"Heading":"new body"}}` — SPLICE: rewrites only named `##` sections, re-embeds only changed chunks, id stable (reasoning edges survive). Idempotent (`{"noop":true}` on identical retry). Prefer splice for long records.
- `DELETE /api/v1/messages/:id?workspace_id={{workspace}}` — forget ALL chunks of a message.
- Before deleting: `GET /api/v1/conclusions/trace?workspace_id={{workspace}}&id=<cid>&direction=reverse` — blast radius of what breaks.

## Workspace conventions
- `{{workspace}}` — YOUR working memory. Never write into another agent's workspace.
- `code_<project>` — a PROGRAMMING PROJECT index (per-symbol chunks + DEFINES/CALLS/IMPORTS edges). `code_vectorizer` = github.com/alfirus/vectorizer. Prose memory does NOT go in `code_*`.
- `_global` — shared identity facts (family, user profile); every scoped search also sees it.
- `family` — the indexed family vault (markdown truth under `vault/{10-memory,20-knowledge,30-work,90-archive}`).

## Programming projects (`code_*` workspaces)
User says **"index this project"** → stage on the server, index, verify:
1. Workspace name `code_<folder>` (lowercase, `-`→`_`). ONE canonical workspace per project — never invent `code_x2`/`_full`/`_v2`; re-index into the SAME workspace (unchanged files hash-skip). Stale look-alike collections should be deleted, not reused.
2. Stage: `tar` the project (exclude `node_modules .next .git dist build`) → `scp` to `ubuntu@100.90.123.105:/opt/code-stage/<name>/` (key `~/.ssh/personal`). Container path = `/data/code-stage/<name>` (laptop paths never work — the server only sees its mounts; `/opt/vectorizer` is mounted read-only at `/data/repo`).
3. Index: `POST /api/v1/code/index` `{"path":"/data/code-stage/<name>","workspace_id":"code_<name>"}` — budget ~15s/file (LM Studio embeds); a client timeout is fine, the server keeps working — poll `GET /api/v1/code/symbols`.
4. Verify: `GET /api/v1/code/symbols?workspace_id=code_<name>` (junk-check) + `GET /api/v1/code/callers?workspace_id=code_<name>&symbol=<known>`.

**"scan this project"** → after indexing: briefing from symbols + CALLS/IMPORTS edges + docs via search, then issue hunt (TODO/FIXME/XXX, secrets `sk-`/`AKIA`/`BEGIN PRIVATE KEY`, dead code = empty callers on exported symbols). Deliver briefing first, findings ranked by risk.

## Provenance & hygiene
- `GET /api/v1/conclusions/trace?workspace_id={{workspace}}&id=<cid>&direction=forward` — "why do I believe X?" (forward) / `reverse` — "what breaks if X goes?"
- `GET /api/v1/conclusions/stale?workspace_id={{workspace}}` — dead-knowledge proposals (old, never-reinforced). Review, then delete explicitly; nothing is auto-deleted.
- The deriver (every ~5 messages) and the dreamer (every 3h) distill your stored messages into conclusions automatically — that's what `brief`/`trace` run over.

## Your workflow as {{role_title}}
{{role_bullets}}

## Rules
- `workspace_id` = your identity (`{{workspace}}`); `X-Agent: {{agent_name}}` on every call. Never touch another agent's workspace.
- Store decisions + rationale, not transcripts. One fact per message recalls better than one long brief.
- Empty search result is a real answer: "I don't have that in memory."
- Embed/LLM calls queue at LM Studio under load (server retries with backoff) — don't hammer retries, raise nothing.
- Server: `ns539881` / Tailscale `100.90.123.105` / SSH `ubuntu@100.90.123.105` key `~/.ssh/personal`. Docs: https://github.com/alfirus/vectorizer
