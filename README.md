# Vectorizer — Semantic Memory Server

A lightweight, self-hosted memory server for AI agents. Stores messages as embeddings in ChromaDB with optional LLM-powered summarization and Q&A. Each agent gets isolated memory via workspace namespaces.

Now with **markdown vault + file-graph + workflow librarian** — truth is markdown on SynologyDrive, vector is a fingerprint beside it, 768d is enough when you have good metadata.

## Documentation

- **[Architecture Blueprint](docs/BLUEPRINT.md)** — full system design, data flows, API contract, config schema, deployment topology
- **This README** — quick start guide and usage reference

## Features

- **Workspace isolation** — `ws_<id>` collections, no cross-talk
- **Markdown vault (new)** — structured folders `10-memory/{daily,longterm}/20-knowledge/{00-inbox,10-topics,20-howto,30-reference}/30-work/{projects,decisions,incidents}/90-archive` + `_shared/knowledge` — markdown is truth (git-backed), vector is index
- **768d Nomic local (new)** — `EMBED_PROVIDER=openai-compatible` via LM Studio `text-embedding-nomic-embed-text-v2` 768d, space cosine, HNSW `M:16 efConstruction:200 efSearch:128` — <100ms, no cloud 429, 2x RAM win over 1536d
- **11-field flat metadata** — `source_type, source_path, header_path, chunk_type, created_at, tags, importance, agent, language, parent_doc_id, doc_title` — auto-filled by markdown parser + workflow, no hallucination
- **File-graph (new)** — `vault/00-index/GRAPH.json` (1419 nodes, 7479 edges at 68 files) + in-memory BFS — file-based, no Postgres — nodes `doc/chunk/entity/folder`, edges `belongs_to/next_chunk/mentions/links_to`
- **Workflow librarian (new)** — tagging + rerank are code, not LLM: `header_path+folder → tags/entities/summary_1line` and `vector 0.55 + BM25 0.25 + importance 0.08 + entityOverlap 0.07 + recency 0.05` — ~150ms vs 1.5s AI rerank; `LIBRARIAN_MODE=workflow|hybrid`
- **Semantic + hybrid search** — vector cosine (HNSW) + BM25 RRF, `?hybrid=true`, temporal `after`/`before`, `grep` + `temporal` endpoints, workflow rerank built-in
- **Peers + peer cards** — `POST /workspaces/:id/peers`, `PUT /peers/:peer_id/card`
- **Agentic dialectic chat** — `POST /workspaces/:id/chat` (observer/observed, `reasoning_level` none/low/medium/high/max, 5 tools: `search_memory`, `search_messages`, `grep_messages`, `get_reasoning_chain`, `get_observation_context`, SSE streaming)
- **Reasoning graph + deriver** — `ws_<id>_reasoning` (premise edges, BFS `GetReasoningChain`), async deriver `2s/5msg` batch → `summarize→CreateConclusion+AddReasoningEdge`
- **Conclusions + representations + surprisal dreamer** — offline `summarize→embed 768d→ws_<id>_conclusions` every `3h` with surprisal gate (`distance <0.15` skip)
- **Optional LLM brain** — SSE streaming, auto-fetch, summarization & RAG Q&A via `/chat/completions` — now reserved for Deriver/Dreamer/Chat synthesis, not tagging
- **Embedder interface** — pluggable `embedding.Embedder` abstraction; swap providers without changing callers
- **Auth** — `X-API-Key` or JWT `w/p/ad` (`AUTH_USE_AUTH`, `scripts/generate_jwt/main.go`, Vectorizer parity)
- **Layered config** — `env > .env > config.toml > defaults` (`config.toml.example`, `BurntSushi/toml`)
- **Docker-ready** — one `docker compose up` (Chroma `1.0.0`, `qwen-embed` vLLM `1536d` optional profile, healthchecks, `qwen_cache`)
- **MCP + Skills + SDKs** — `mcp-remote` (13 tools + `vectorizer_chat`), `skills/vectorizer`, `@vectorizer/sdk` / `vectorizer-ai`
- **Evals** — `go run ./evals/run.go -file evals/data/sample.jsonl` (LongMemEval-style `recall` + `reasoning-grounded` via chat)

## Architecture

```
Agent → Vectorizer API → ChromaDB (vectors) + Embedding Service
  │                        │
  ├─ POST /messages       ├─ text-embedding-nomic-embed-text-v2 768d (local, primary)
  ├─ GET  /search         ├─ Qwen/Qwen3-Embedding-4B 1536d MRL (vLLM, optional)
  └─ POST /brain/ask      ├─ text-embedding-004 / 005 (Google, 768d)
                          └─ qwen3.6-35b-a3b (LM Studio, LLM brain — Deriver/Dreamer/Chat only)

Vault pipeline (new):
  SynologyDrive/ai/*.md → vault_index.py (markdown-aware chunker 3600-4800+200, header_path, never inside ```)
                        → tags/entities via workflow (keyword + known list [Maisarah,Alfirus,Bukku,DJI...])
                        → POST /messages/batch {metadata: 11 fields + entities}
                        → Chroma ws_family 768d cosine + GRAPH.json (chunk→doc→entity→folder)
                        → POST /messages/search → vector top-k → graph 1-hop → WorkflowRerankScore → top3
```

## Quick Start

### Option A: Docker Compose (recommended)

```bash
# Clone and configure
git clone https://github.com/alfirus/vectorizer.git
cd vectorizer
cp .env.example .env
# Edit .env with your settings (see Configuration)

# Start everything
docker compose up -d

# Verify it's running (should show embedding_model nomic-embed-text-v2 768d, space cosine)
curl http://localhost:8091/api/v1/health
# {"chromadb":"ok","embedding_model":"text-embedding-nomic-embed-text-v2","llm_enabled":true,"status":"ok","version":"0.3.0"}

# Populate vault (markdown truth → vector index)
python "C:/Users/alfir/SynologyDrive/ai/maisarah/vault/00-index/vault_index.py"
python "C:/Users/alfir/SynologyDrive/ai/maisarah/vault/00-index/graph_build.py"  # optional: rebuild GRAPH.json

# Search with lineage
curl -H "X-API-Key: vectorizer-local-key" -H "Content-Type: application/json" \
  -d '{"query":"Bukku automation","where":{"workspace_id":"family"},"n_results":3}' \
  http://localhost:8091/api/v1/messages/search
# Returns results[].metadata {source_path, header_path, tags, entities, importance, chunk_id, file_hash}
```

### Option B: Local Development

```bash
# Ensure ChromaDB is running (or use docker-compose)
# Then:
make build && ./vectorizer.exe
```

## Vault — Markdown as Source of Truth

All memory lives as markdown, not vectors. Vault structure:

```
aisynology/ai/
├── maisarah/vault/
│   ├── 00-index/        # MEMORY_INDEX.json, GRAPH.json, vault_index.py, graph_build.py
│   ├── 10-memory/
│   │   ├── longterm/    # AGENTS.md, SOUL.md, IDENTITY.md, MEMORY.md, USER.md (importance 5)
│   │   ├── daily/       # YYYY-MM-DD.md (importance 2)
│   │   └── sessions/
│   ├── 20-knowledge/
│   │   ├── 00-inbox/    # importance 2
│   │   ├── 10-topics/   # importance 3
│   │   ├── 20-howto/    # importance 4
│   │   └── 30-reference/# importance 3
│   ├── 30-work/
│   │   ├── projects/
│   │   ├── decisions/
│   │   └── incidents/
│   └── 90-archive/
└── _shared/knowledge/   # cross-agent shared (importance 4)
    ├── balqis-ag/       # Bukku automation, tasks, reports
    ├── ain/             # agent profiles, memory logs
    ├── kifli-emergency/ # emergency manuals
    └── adviksai-dji/    # DJI T25P/T10/T70P specs
```

**Indexer:** `vault/00-index/vault_index.py`

- Markdown-aware: splits by H1/H2/H3, never inside ```
- Chunks: 3600-4800 chars (~900-1200 tokens) + 200 overlap, prefix `header_path`
- Metadata: 11-field flat (`source_type, source_path, header_path, chunk_type, created_at, tags, importance, agent, language, parent_doc_id, doc_title`) + `entities, summary_1line, chunk_id, file_hash`
- Tags/entities: workflow code (`keyword_tags` from folder+header) — `vault/00-index/graph_build.py` adds entities from known list + TitleCase header tokens, Brain `VaultTagSystem` enriches when `LIBRARIAN_MODE=hybrid`
- Registry: `MEMORY_INDEX.json` hash dedupe — `To index: 0, skipped 55` on unchanged run
- Graph: `GRAPH.json` 68 docs → 1201 chunks → 1419 nodes, 7479 edges (chunk→doc belongs_to, chunk→chunk next, chunk→entity mentions, doc→folder, [[wikilink]])

```bash
python vault/00-index/vault_index.py --dry-run          # preview: X files, Y chunks, skipped Z
python vault/00-index/vault_index.py                     # index only changed (hash diff)
python vault/00-index/vault_index.py --reindex           # force rebuild all
python vault/00-index/graph_build.py                     # rebuild GRAPH.json
```

## Cron Jobs — Regarding Vectorizer

| Job | Schedule | Where | What |
|-----|----------|-------|------|
| `vectorizer-reindex-1h` | `0 * * * *` (hourly) | Hermes `origin` | `vectorizer_reindex.py` → `vault_index.py` diff embed 768d; failures alert via Telegram DM + email |
| `vectorizer-backup-daily` | `0 3 * * *` (03:00) | Hermes `origin` | `vectorizer_backup.py` → `SynologyDrive/ai/backups/vectorizer/chroma-YYYY-MM-DD.tar.gz` + `GRAPH.json/MEMORY_INDEX.json`, prune `+7d` (keep 7 days) |
| `vectorizer-health-5m` | `*/5 * * * *` | Hermes `origin` | `vectorizer_healthcheck.py` probes `8091/health + 8100/heartbeat + 8092/ + 1234/v1/models`; auto `docker restart`, re-applies `tailscale serve --https=443 http://localhost:8092` |
| `deriver` | `2s/5msg` ticker | Inside `vectorizer` | `Summarize("Extract 1-3 facts" + text[:8000]) → CreateConclusion + AddReasoningEdge` on `POST /messages` |
| `dreamer` | `3h` ticker | Inside `vectorizer` | `ListWorkspaces→ListSessions→GetMessages(20)` surprisal `<0.15` skip → `CreateConclusion(source:dreamer)` |

Alerts (server-only) live in `C:/Users/alfir/vectorizer/.env`:

```
ALERT_TELEGRAM_BOT_TOKEN=   # @BotFather token, Telegram DM alert
ALERT_TELEGRAM_CHAT_ID=     # use @userinfobot / getUpdates
ALERT_EMAIL_TO=alfirus@gmail.com
ALERT_EMAIL_FROM=vectorizer@alfirus.my
SMTP_HOST=smtp.gmail.com  SMTP_PORT=587  SMTP_USER=  SMTP_PASS= # Gmail App Password (16-char)
BACKUP_RETENTION_DAYS=7
```

Configure without touching `.env` by hand: **Vectorizer Dashboard → Settings** (`/dashboard/settings`) edits all of the above server-side (secrets masked in UI; schedules persisted to `cron_schedules.json`; cron ownership stays on host `hermes cron list`).

## Configuration

All settings via `.env` file or environment variables. Key options:

| Variable | Default | Description |
|----------|---------|-------------|
| `PORT` | `8091` | Server port |
| `CHROMA_HOST` | `localhost` | ChromaDB hostname |
| `CHROMA_PORT` | `8100` | ChromaDB port |
| `DEFAULT_API_KEY` | *(empty)* | API key for auth (set to disable) |
| `EMBED_PROVIDER` | `openai-compatible` | Embedding provider: `google`, `openai-compatible`, `lm-studio` |
| `LM_STUDIO_URL` | `http://host.docker.internal:1234/v1` | LM Studio endpoint |
| `OAI_COMPATIBLE_URL` | `http://host.docker.internal:1234/v1` | OpenAI-compatible embedding URL |
| `EMBED_MODEL` | `text-embedding-nomic-embed-text-v2` | Embedding model (768d) |
| `EMBED_DIMENSIONS` | `768` | Embedding dimensions — 768 (nomic) or 1536 (Qwen3) |
| `LLM_ENABLED` | `false` | Enable LLM brain (Deriver/Dreamer/Chat synthesis) |
| `LLM_PROVIDER` | `lm-studio` | LLM provider: `lm-studio` or `openai-compatible` |
| `LIBRARIAN_MODE` | `workflow` | Librarian: `workflow` (code tagging+rerank <10ms, default) or `hybrid` (workflow + AI 1.5s cap) |
| `LLM_MODEL` | `qwen3.6-35b-...` | LLM model for brain |
| `VAULT_ROOT` | `/data/ai` | Vault mount inside Docker (`C:/Users/alfir/SynologyDrive/ai:/data/ai:ro`) |

## API Reference

### Health Check

```bash
GET /api/v1/health
```

Returns service status and configuration. No auth required. Should show `embedding_model: text-embedding-nomic-embed-text-v2, space: cosine, llm_enabled: true`.

### Create Workspace

```bash
POST /api/v1/workspaces
Content-Type: application/json

{
  "name": "agent-sarah"
}
```

Creates a new workspace (namespace) for an agent's isolated memory. Returns the workspace object with generated ID.

### List Workspaces

```bash
GET /api/v1/workspaces
```

Returns all workspaces.

### Get Workspace Stats

```bash
GET /api/v1/workspaces/:id/stats
```

Returns document count and metadata for a workspace. Example: `{"workspace_id":"family","document_count":1204}` with `vault/00-index/MEMORY_INDEX.json:68 files` + `GRAPH.json:1419 nodes`.

### Store Message

```bash
POST /api/v1/messages
Content-Type: application/json

{
  "workspace_id": "ws-abc123",
  "session_id": "sess-def456",
  "role": "user",
  "content": "Hello, I need help with my account",
  "metadata": {
    "source_type": "file", "source_path": "vault/20-knowledge/10-topics/foo.md",
    "header_path": "Foo > Bar", "tags": "foo,bar", "entities": "Alfirus,Bukku",
    "importance": 3, "agent": "maisarah"
  }
}
```

Stores a message, chunks it if too long (6000 max, vault uses 3600-4800), generates embeddings 768d, and upserts to ChromaDB. Returns the stored message ID. Metadata passthrough for vault 11-field + `entities/summary_1line` is fully supported (both `AddMessage` and `AddBatchMessages`).

### Batch Store Messages

```bash
POST /api/v1/messages/batch
Content-Type: application/json

{
  "workspace_id": "family",
  "messages": [
    {"session_id": "maisarah-vault-...", "role": "user", "content": "[vault path]\n# header\nbody...", "metadata": {"source_path":"vault/...","header_path":"A > B","tags":"x,y","entities":"Alfirus","importance":5}},
    {"session_id": "sess-def456", "role": "assistant", "content": "..."}
  ]
}
```

Store multiple messages in a single request. Used by `vault_index.py` (batches of 10).

### Semantic Search (JSON)

```bash
POST /api/v1/messages/search
Content-Type: application/json

{
  "query": "Bukku automation Serdang",
  "n_results": 5,
  "where": {
    "workspace_id": "family",
    "role": "user"
  }
}
```

Search across messages using semantic similarity. Pipeline: `embed query 768d → HNSW cosine → graph 1-hop (GRAPH.json, 5 neighbors) → WorkflowRerankScore (vector 0.55 + BM25 0.25 + importance 0.08 + entityOverlap 0.07) → top-k`. Returns `results[] {id, document, metadata{source_path,header_path,tags,entities,importance,chunk_id,file_hash}, distance}`. If `LIBRARIAN_MODE=hybrid`, adds AI `RerankVaultResults` with 1.5s cap for ≥3 hits.

### Semantic Search (Query Params)

```bash
GET /api/v1/messages/search?q=account+billing&workspace_id=family&n_results=5
```

Simpler search endpoint using query parameters instead of JSON body. Same workflow rerank path as JSON.

### LLM Brain — Summarize

Requires `LLM_ENABLED=true`.

```bash
POST /api/v1/brain/summarize
Content-Type: application/json

{
  "text": "Long conversation transcript to summarize...",
  "max_chars": 500,
  "workspace_id": "ws-abc123"
}
```

Summarizes text using the configured LLM (Qwen 35b). Can optionally fetch workspace/session messages.

### LLM Brain — Ask Question

Requires `LLM_ENABLED=true`.

```bash
POST /api/v1/brain/ask
Content-Type: application/json

{
  "question": "What did the user ask about?",
  "workspace_id": "ws-abc123",
  "session_id": "sess-def456"
}
```

Answers questions using LLM + retrieved context from semantic search. Pass `context` directly or let it fetch from workspace/session. Uses `AgentSystemPrompt` perspective.

## Authentication

Set `DEFAULT_API_KEY` in `.env` to enable API key authentication:

```bash
curl -H "X-API-Key: your-s...ere" \
  http://localhost:8091/api/v1/messages \
  -d '{"workspace_id":"ws-abc","session_id":"sess-def","role":"user","content":"test"}'
```

Health check (`/api/v1/health`) is always public.

## Workspace Isolation

Each workspace maps to a separate ChromaDB collection: `ws_<workspace_id>`. This ensures agents never see each other's memories. Search can be scoped to:

- A specific workspace (recommended) — `family` holds all vault + _shared (68 files) at 768d cosine
- A specific session within a workspace — `maisarah-vault-10-memory-longterm-IDENTITY-md` etc.
- All workspaces (cross-agent search)

## Embedding Models

| Model | Provider | Dimensions | Notes |
|-------|----------|------------|-------|
| `text-embedding-nomic-embed-text-v2` | LM Studio (openai-compatible) | 768 | **Current primary** — local, <100ms, no 429, cosine |
| `Qwen/Qwen3-Embedding-4B` | vLLM | 1536 | MRL (optional gpu profile) — 2x RAM/latency vs 768 |
| `Qwen/Qwen3-Embedding-0.6B` | LM Studio | 1024 | Mid option |
| `nomic-embed-text` | LM Studio | 768 | CPU fallback |
| `text-embedding-004` | Google AI Studio | 768 | Cloud, batch+single |
| `text-embedding-3-small` | OpenAI | 1536 | Cloud |

768d is sufficient — recall comes from markdown-aware chunking (3600-4800+200, header_path prefix) + rich metadata (11-field + entities) + workflow rerank, not dimensions. 768*4 bytes ~3KB vs 6KB per vector.

## Librarian — Workflow vs AI

| Mode | How it works | Latency | When to use |
|------|--------------|---------|-------------|
| `workflow` (default, `LIBRARIAN_MODE=workflow`) | Tagging: `keyword_tags` + known entities + `header_path`; Rerank: `WorkflowRerankScore` formula | ~150ms total (embed 100ms + vector 30ms + rerank 5ms + graph 10ms) | Always — deterministic, fast, private, git-diffable |
| `hybrid` (`LIBRARIAN_MODE=hybrid`) | Same workflow + async Brain enrichment: `VaultTagSystem` tags+entities async, `VaultRerankSystem` order with 1.5s cap (≥3 hits) | +0-1.5s on eligible searches | Complex ambiguous queries ("that drone thing") — AI rescues synonyms |

**Recommendation:** `workflow` at 68 files / 1204 chunks. AI helps most at 5k-50k dense cross-linked chunks. Brain now reserved for **Deriver (2s/5msg → conclusions) + Dreamer (3h, surprisal 0.15) + Chat synthesis**.

## LLM Brain Modes

Toggle via `LLM_ENABLED` (`config/config.go:25`, `.env` `LLM_ENABLED=true|false`). `POST /brain/*` + `POST /workspaces/:id/chat` gate on this.

### `LLM_ENABLED=false` — Brain off

Use Vectorizer as pure vector+graph DB + workflow librarian. No `POST /chat/completions` calls, smallest footprint, deterministic. Good for `agent brings own LLM (Claude/GPT-4o)` and does `search → inject → generate`.

### `LLM_ENABLED=true` — Brain on (current)

Full maturity: agentic chat, async deriver/dreamer building `ws_*_conclusions/reasoning`, streaming, continuity via auto-stored `assistant` conclusions. Requires `LM_STUDIO_URL=http://host.docker.internal:1234/v1` reachable.

## Reasoning Maturity

- **Reasoning graph:** `ws_<id>_reasoning` stores `conclusion_id → premise_ids + supporting_message_ids`; `GetReasoningChain` BFS, `GetObservationContext` windows `±2` chunks.
- **Deriver:** `2s/5msg` flush → `CreateConclusion + AddReasoningEdge`.
- **Dreamer:** `3h`, `8000` tokens `FitContextWithinTokens`, `distance<0.15` surprisal skip.
- **File-graph:** `GRAPH.json` `chunk↔doc↔entity↔folder` + `vault_index.py` hash dedupe — markdown truth stays synced.
- **Workflow rerank:** `vector 0.55 + BM25 0.25 + importance 0.08 + entityOverlap 0.07` — replaces AI rerank for vault.
- **Agentic chat:** `POST /workspaces/:id/chat` loops up to `maxTools` per `reasoning_level` (`none=0, low=2, medium=4, high=6, max=8`, `temp 0.1→0.9`).

## End-to-End Workflow — User Prompt → AI → Vectorizer → Response

```
User Prompt
   │
   ▼
AI Agent (Hermes/Maisarah/Claude) receives prompt
   │ 1. Agent calls Vectorizer FIRST (recall)
   ├─► vectorizer_search / POST /messages/search {query, workspace_id: family} ─┐
   │   or vectorizer_chat / POST /workspaces/:id/chat {query, observer}     │
   │   Vectorizer: embed query 768d cosine → HNSW + graph 1-hop (5)          │
   │   → WorkflowRerankScore (vector+BM25+importance+entity) → top-k +        │
   │   peer cards + conclusions                                              │
   │ ◄─ returns {results, distances} + metadata{source_path,header_path,tags,entities,importance} │
   │                                                                         │
   ├─► 2. Agent injects results + source_path markdown into LLM prompt       │
   │      (Markdown is truth: open vault/10-memory/longterm/MEMORY.md#header)│
   │                                                                         │
   ├─► 3. LLM generates answer (external or brain Ask/Chat)                  │
   │                                                                         │
   ├─► 4. Agent calls Vectorizer AGAIN (record)                               │
   ├─► POST /messages {workspace_id:family, session_id, role:"user", content: prompt}
   ├─► POST /messages {role:"assistant", content: answer}                     │
   │   Vectorizer: SanitizeString → chunkText 6000 (vault 3600-4800) → Embed 768d │
   │   → ws_family collection, metadata {message_id, role, source_type, header_path, tags, entities}
   │   → 3× retry backoff; 100k char cap; deriver 2s/5msg → conclusions       │
   │                                                                         │
   └─► 5. Async side-effects (no user wait)                                  │
       ├─ deriver 2s/5msg → ws_conclusions + reasoning edges                  │
       ├─ dreamer 3h → ws_conclusions (surprisal 0.15)                        │
       ├─ vault_index.py 6h (Hermes cron 943f6f432dee) → hash dedupe → 768d  │
       └─ webhooks Fire("message.created") if registered                     │
            │
            ▼
       Next turn reuses vault markdown + vector lineage → long-term persona.
```

**Step-by-step:**

1. **User sends prompt** → Maisarah/Hermes intercepts.
2. **Recall:** `vectorizer_search {query, where:{workspace_id:"family"}, n_results:3}` → 768d embed → HNSW cosine → graph 1-hop → WorkflowRerankScore → `source_path#header_path + tags/entities + importance`.
3. **Inject:** Agent opens `SynologyDrive/ai/maisarah/vault/...` at that header_path as grounding context.
4. **Generate:** LLM produces answer grounded in vault markdown.
5. **Record:** Both prompt + answer stored via `POST /messages` → chunked 6000 (vault 3600-4800) → 768d → `ws_family`.
6. **Background:** Deriver/Dreamer build conclusions, `vault_reindex-6h` keeps `MEMORY_INDEX.json → GRAPH.json` in sync.
7. **Fallback:** If Vectorizer down, agent proceeds without memory (graceful degradation).

This mirrors `Store → Reason (deriver) → Query (chat/representation) → Inject` loop, single-binary Go/Chroma + vault, no Postgres.

## Integrations

MCP (Claude Desktop, OpenCode, OpenClaw, Hermes via `mcp-remote`):

```json
{"mcpServers":{"vectorizer":{"command":"node","args":["./mcp/dist/index.js"],"env":{"VECTORIZER_URL":"http://localhost:8091","VECTORIZER_API_KEY":"Bearer <jwt>","VECTORIZER_WORKSPACE_ID":"family"}}}}
```

Tools: `vectorizer_search`, `vectorizer_add_message` (supports `metadata{source_path,header_path,tags,entities,importance}` passthrough), `vectorizer_chat` (dialectic), etc. (see `mcp/README.md:1`).

Skill: `skills/vectorizer/SKILL.md` + `skills/vectorizer-memory/` — recall/record loop (search before answering, store after with `source_path`).

SDKs: `sdks/typescript` (`@vectorizer/sdk`) + `sdks/python` (`vectorizer-ai`) — `new Vectorizer({baseUrl, apiKey}).search("...")`.

## 3-Agent Setup (Option C — Recommended)

Shared `shared-proj`, peers `alpha/beta/gamma`, strict JWT (`peer_id` must match `p`), all share vault `ws_family` 768d:

```bash
# 1. Generate JWTs (requires AUTH_JWT_SECRET)
export AUTH_JWT_SECRET=$(openssl rand -hex 32); echo "AUTH_JWT_SECRET=$AUTH_JWT_SECRET" >> .env
go run ./scripts/generate_jwt --workspace shared-proj --admin --expires 30d  # → ADMIN_TOKEN
go run ./scripts/generate_jwt --workspace shared-proj --peer alpha --expires 30d  # → ALPHA_JWT

# 2. Provision workspace/peers/scopes/sessions
VECTORIZER_URL=http://localhost:8091 ADMIN_TOKEN=$ADMIN_TOKEN ./scripts/provision_option_c.sh

# 3. Per-agent MCP: Bearer $ALPHA_JWT, peer_id=alpha — rejected if mismatched (403)
# 4. Docker (LM Studio Nomic 768 on host — no qwen-embed GPU needed for vault)
docker compose up -d  # vectorizer + chromadb; healthcheck shows nomic 768d cosine
curl http://localhost:8091/api/v1/health
```

## Development

```bash
# Build locally
make build

# Run with local ChromaDB
make run

# Run tests + eval
make test
go run ./evals/run.go -file evals/data/sample.jsonl -base http://localhost:8091

# Clean build artifacts
make clean
```

### Project Structure

```
vectorizer/
├── config/           # Layered config (env>.env>config.toml)
├── internal/
│   ├── chromadb/     # ChromaDB v2 API client (Count raw-int fix, ensureCollection)
│   ├── embedding/    # Embedder interface (openai-compatible LM Studio nomic-768, google, vLLM)
│   ├── llmbrain/     # LLM brain + prompts.go (AgentSystemPrompt, VaultTagSystem+VaultRerankSystem)
│   ├── store/
│   │   ├── store.go  # Store + Vault librarian (SetBrain, TagVaultChunk[tags+summary+entities], SearchWithScopeAndRerank + WorkflowRerankScore + graph expand 1-hop)
│   │   ├── graph.go  # File-graph (GRAPH.json, Expand 1-hop) — no Postgres
│   │   ├── workflow_rerank.go # WorkflowRerankScore formula
│   │   ├── reasoning.go, scopes.go, keys.go, conclusions.go, peers.go
│   ├── deriver/      # Async deriver 2s/5msg
│   ├── dreamer/      # Surprisal dreamer 3h
│   ├── handlers/     # Workspaces, messages (metadata passthrough 11-field + entities), peers, chat (agentic)
│   ├── models/       # Workspace, Session, Message, Peer, PeerCard, SearchRequest
│   ├── grpc/         # gRPC server (VectorizerService)
│   ├── security/     # JWT w/p/ad
│   └── webhooks/     # Webhook manager
├── vault/            # (mounted) SynologyDrive/ai:/data/ai:ro — truth is here, not in repo
│   └── maisarah/vault/00-index/ # MEMORY_INDEX.json, GRAPH.json, vault_index.py, graph_build.py
├── docs/             # BLUEPRINT.md
├── proto/            # vectorizer.proto (gRPC)
├── vectorizerpb/     # Generated protobuf
├── mcp/              # MCP proxy (13 tools)
├── skills/           # Skills
├── sdks/             # TS + Python SDKs
├── evals/            # Eval harness
├── main.go           # Entry point, wiring (SetBrain, Graph), auth, rate limit, gRPC
├── Dockerfile        # Container build (Go 1.25, exposes 8091+50051)
├── docker-compose.yml# Full stack (Vectorizer + ChromaDB + optional qwen-embed vLLM)
├── Makefile          # Build/run commands
└── .env.example      # Configuration template
```

## License

MIT
