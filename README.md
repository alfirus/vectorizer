# Vectorizer — Semantic Memory Server

A lightweight, self-hosted memory server for AI agents. Stores messages as embeddings in ChromaDB with optional LLM-powered summarization and Q&A. Each agent gets isolated memory via workspace namespaces.

Now with **markdown vault + file-graph + workflow librarian** — truth is markdown, vector is a fingerprint beside it, 768d is enough when you have good metadata.

## Documentation

- **[Architecture Blueprint](docs/BLUEPRINT.md)** — full system design, data flows, API contract, config schema, deployment topology
- **This README** — quick start guide and usage reference

## Features

- **Workspace isolation** — `ws_<id>` collections, no cross-talk
- **Markdown vault** — structured folders `10-memory/{daily,longterm}/20-knowledge/{00-inbox,10-topics,20-howto,30-reference}/30-work/{projects,decisions,incidents}/90-archive` + `_shared/knowledge` — markdown is truth (git-backed), vector is index
- **768d Nomic local** — `EMBED_PROVIDER=openai-compatible` via LM Studio `text-embedding-nomic-embed-text-v2` 768d, space cosine, HNSW `M:16 efConstruction:200 efSearch:128` — <100ms, no cloud 429, 2x RAM win over 1536d
- **11-field flat metadata** — `source_type, source_path, header_path, chunk_type, created_at, tags, importance, agent, language, parent_doc_id, doc_title` — auto-filled by markdown parser + workflow, no hallucination
- **File-graph** — `vault/00-index/GRAPH.json` + in-memory BFS — file-based, no Postgres — nodes `doc/chunk/entity/folder`, edges `belongs_to/next_chunk/mentions/links_to`
- **Workflow librarian** — tagging + rerank are code, not LLM: `header_path+folder → tags/entities/summary_1line` and `vector 0.55 + BM25 0.25 + importance 0.08 + entityOverlap 0.07 + recency 0.05` — ~150ms vs 1.5s AI rerank; `LIBRARIAN_MODE=workflow|hybrid`
- **Semantic + hybrid search** — vector cosine (HNSW) + BM25 RRF, `?hybrid=true`, temporal `after`/`before`, `grep` + `temporal` endpoints, workflow rerank built-in
- **Peers + peer cards** — `POST /workspaces/:id/peers`, `PUT /peers/:peer_id/card`
- **Agentic dialectic chat** — `POST /workspaces/:id/chat` (observer/observed, `reasoning_level` none/low/medium/high/max, 5 tools: `search_memory`, `search_messages`, `grep_messages`, `get_reasoning_chain`, `get_observation_context`, SSE streaming)
- **Reasoning graph + deriver** — `ws_<id>_reasoning` (premise edges, BFS `GetReasoningChain`), async deriver `2s/5msg` batch → `summarize→CreateConclusion+AddReasoningEdge`
- **Conclusions + representations + surprisal dreamer** — offline `summarize→embed 768d→ws_<id>_conclusions` every `3h` with surprisal gate (`distance <0.15` skip)
- **Optional LLM brain** — SSE streaming, auto-fetch, summarization & RAG Q&A via `/chat/completions` with **10-minute timeout** (for large models like Qwen3.6-35B-A3B) — reserved for Deriver/Dreamer/Chat synthesis, not tagging; requires LM Studio API token
- **Embedder interface** — pluggable `embedding.Embedder` abstraction; swap providers without changing callers
- **Auth** — `X-API-Key` or JWT `w/p/ad` (`AUTH_USE_AUTH`, `scripts/generate_jwt/main.go`)
- **Layered config** — `env > .env > config.toml > defaults` (`config.toml.example`, `BurntSushi/toml`)
- **Docker-ready** — one `docker compose up` (Chroma `1.0.0`, healthchecks)
- **MCP + Skills + SDKs** — `mcp-remote` (13 tools + `vectorizer_chat`), `skills/vectorizer`, `@vectorizer/sdk` / `vectorizer-ai`
- **Evals** — `go run ./evals/run.go -file evals/data/sample.jsonl` (LongMemEval-style `recall` + `reasoning-grounded` via chat)

## Architecture

```
Agent → Vectorizer API → ChromaDB (vectors) + Embedding Service
  │                        │
  ├─ POST /messages       ├─ text-embedding-nomic-embed-text-v2 768d (local, primary)
  ├─ GET  /search         ├─ Qwen/Qwen3-Embedding-4B 1536d MRL (vLLM, optional)
  └─ POST /brain/ask      ├─ text-embedding-004 / 005 (Google, 768d)
                          └─ qwen3.6-35b-a3b (LM Studio, LLM brain — 10min timeout)

Vault pipeline:
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
python vault/00-index/vault_index.py
python vault/00-index/graph_build.py  # optional: rebuild GRAPH.json

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

### Option C: Server Deployment (Live)

```bash
# On server (139.99.131.127)
git clone https://github.com/alfirus/vectorizer.git /opt/vectorizer
cd /opt/vectorizer

# Create .env with Tailscale LM Studio URL and API token
cat > .env << 'EOF'
PORT=8091
DEFAULT_API_KEY=vectorizer-local-key
CHROMA_HOST=chromadb
CHROMA_PORT=8000
EMBED_PROVIDER=openai-compatible
EMBED_MODEL=text-embedding-nomic-embed-text-v2
EMBED_DIMENSIONS=768
LM_STUDIO_URL=http://100.121.188.113:1234/v1
LM_STUDIO_API_KEY=sk-lm-YOUR_TOKEN_HERE
OAI_COMPATIBLE_URL=http://100.121.188.113:1234/v1
LLM_ENABLED=true
LLM_PROVIDER=lm-studio
LLM_MODEL=qwen3.6-35b-a3b-uncensored-hauhaucs-aggressive
LLM_API_KEY=sk-lm-YOUR_TOKEN_HERE
LIBRARIAN_MODE=workflow
VAULT_ROOT=/data/ai
EOF

# Start with ChromaDB on port 8102 (separate from aict's 8100)
docker compose -f docker-compose.server.yml up -d

# Import vault data
python3 scripts/vault_index.py --workspace maisarah
python3 scripts/graph_build.py
```

## Vault — Markdown as Source of Truth

All memory lives as markdown, not vectors. Vault structure:

```
ai/
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

**Indexer:** `scripts/vault_index.py`

- Markdown-aware: splits by H1/H2/H3, never inside ```
- Chunks: 3600-4800 chars (~900-1200 tokens) + 200 overlap, prefix `header_path`
- Metadata: 11-field flat (`source_type, source_path, header_path, chunk_type, created_at, tags, importance, agent, language, parent_doc_id, doc_title`) + `entities, summary_1line, chunk_id, file_hash`
- Tags/entities: workflow code (`keyword_tags` from folder+header) — `scripts/graph_build.py` adds entities from known list + TitleCase header tokens, Brain `VaultTagSystem` enriches when `LIBRARIAN_MODE=hybrid`
- Registry: `MEMORY_INDEX.json` hash dedupe — `To index: 0, skipped 55` on unchanged run
- Graph: `GRAPH.json` 68 docs → 1201 chunks → 1419 nodes, 7479 edges (chunk→doc belongs_to, chunk→chunk next, chunk→entity mentions, doc→folder, [[wikilink]])

```bash
python scripts/vault_index.py --dry-run          # preview: X files, Y chunks, skipped Z
python scripts/vault_index.py                     # index only changed (hash diff)
python scripts/vault_index.py --reindex           # force rebuild all
python scripts/graph_build.py                     # rebuild GRAPH.json
```

**Environment variables** (for Docker/server deployment):

```bash
VAULT_ROOT=/data/ai                    # vault base path inside container
VECTORIZER_URL=http://vectorizer:8091  # Vectorizer API endpoint (NO /api/v1 — code appends it)
VECTORIZER_API_KEY=vectorizer-local-key # API key
REINDEX_PYTHON=python3                  # Python binary to use
```

## Deployment

### Local (Docker Compose)

```bash
docker compose up -d  # Vectorizer :8091 + ChromaDB :8100 + Dashboard :8092
```

### Server (Production)

Uses `docker-compose.server.yml` with Tailscale mesh for LM Studio. Vectorizer binds to `0.0.0.0:8091` for Tailscale access from the desktop:

```yaml
services:
  vectorizer:
    build: .
    container_name: vectorizer
    ports:
      - "0.0.0.0:8091:8091"       # Tailscale access from desktop
      - "127.0.0.1:50051:50051"   # gRPC (local only)
    env_file: .env
    environment:
      CHROMA_HOST: chromadb
      CHROMA_PORT: 8000
      EMBED_PROVIDER: openai-compatible
      EMBED_MODEL: text-embedding-nomic-embed-text-v2
      EMBED_DIMENSIONS: 768
      LM_STUDIO_URL: http://100.121.188.113:1234/v1
      LIBRARIAN_MODE: workflow
      VAULT_ROOT: /data/ai
    volumes:
      - /opt/vectorizer/vault-data:/data/ai  # writable for MEMORY_INDEX.json
    extra_hosts:
      - "host.docker.internal:host-gateway"
    depends_on:
      chromadb:
        condition: service_started
    restart: unless-stopped
    healthcheck:
      test: ["CMD-SHELL", "wget -qO- http://127.0.0.1:8091/api/v1/health || exit 1"]
      interval: 30s
      timeout: 5s

  chromadb:
    image: chromadb/chroma:1.0.0
    container_name: vectorizer-chromadb
    ports: ["127.0.0.1:8102:8000"]  # 8102 to avoid conflict with aict's 8100
    volumes: [chroma_data:/chroma/chroma]
    restart: unless-stopped

  dashboard:
    build: ../vectorizer-dashboard
    container_name: vectorizer-dashboard
    ports: ["127.0.0.1:8092:8092"]
    environment:
      PORT: 8092
      VECTORIZER_URL: http://vectorizer:8091   # NO /api/v1 — code appends it
      VECTORIZER_API_KEY: vectorizer-local-key
      LM_STUDIO_KEY: ${LM_STUDIO_API_KEY}
      LLM_MODEL: qwen3.6-35b-a3b-uncensored-hauhaucs-aggressive
      VAULT_ROOT: /data/ai
    volumes:
      - /opt/vectorizer/vault-data:/data/ai  # writable for graph_build.py
    extra_hosts:
      - "host.docker.internal:host-gateway"
    depends_on:
      vectorizer:
        condition: service_healthy
    restart: unless-stopped
```

**Nginx proxy** (HTTPS via Cloudflare):

```nginx
server {
    listen 443 ssl;
    server_name vectorizer.alfirus.my;

    location / {
        proxy_pass http://127.0.0.1:8092;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;

        # SSE support
        proxy_buffering off;
        proxy_cache off;
        proxy_set_header Connection '';
        proxy_http_version 1.1;
        chunked_transfer_encoding off;
    }
}
```

**⚠️ .env changes require container recreation, not restart:**

```bash
# .env is cached at container creation time — restart does NOT re-read it
docker compose -f docker-compose.server.yml up -d --force-recreate vectorizer
```

**⚠️ Vault permissions:** Files inside `/opt/vectorizer/vault-data/` must be writable by the container (uid 1001 `nextjs`). If you see `PermissionError: [Errno 13] Permission denied` on `MEMORY_INDEX.json`:

```bash
chmod 666 /opt/vectorizer/vault-data/maisarah/vault/00-index/*.json
chmod 777 /opt/vectorizer/vault-data/maisarah/vault/00-index/
```

**⚠️ Dashboard PORT leak:** If dashboard service inherits Vectorizer's `.env` with `PORT=8091`, dashboard will conflict. Always set `PORT: 8092` explicitly in the dashboard service environment.

## Cron Jobs

| Job | Schedule | Where | What |
|-----|----------|-------|------|
| `vectorizer-reindex-1h` | `0 * * * *` (hourly) | Hermes `origin` | `vectorizer_reindex.py` → `vault_index.py` diff embed 768d; failures alert via Telegram DM + email |
| `vectorizer-backup-daily` | `0 3 * * *` (03:00) | Hermes `origin` | `vectorizer_backup.py` → `SynologyDrive/ai/backups/vectorizer/chroma-YYYY-MM-DD.tar.gz` + `GRAPH.json/MEMORY_INDEX.json`, prune `+7d` (keep 7 days) |
| `vectorizer-health-5m` | `*/5 * * * *` | Hermes `origin` | `vectorizer_healthcheck.py` probes `8091/health + 8102/heartbeat + 8092/ + 1234/v1/models`; auto `docker restart` |
| `deriver` | `2s/5msg` ticker | Inside `vectorizer` | `Summarize("Extract 1-3 facts" + text[:8000]) → CreateConclusion + AddReasoningEdge` on `POST /messages` |
| `dreamer` | `3h` ticker | Inside `vectorizer` | `ListWorkspaces→ListSessions→GetMessages(20)` surprisal `<0.15` skip → `CreateConclusion(source:dreamer)` |

## Configuration

All settings via `.env` file or environment variables. Key options:

| Variable | Default | Description |
|----------|---------|-------------|
| `PORT` | `8091` | Server port |
| `CHROMA_HOST` | `localhost` | ChromaDB hostname |
| `CHROMA_PORT` | `8100` | ChromaDB port |
| `DEFAULT_API_KEY` | *(empty)* | API key for auth (set to enable) |
| `EMBED_PROVIDER` | `openai-compatible` | Embedding provider: `google`, `openai-compatible`, `lm-studio` |
| `LM_STUDIO_URL` | `http://host.docker.internal:1234/v1` | LM Studio endpoint |
| `LM_STUDIO_API_KEY` | *(empty)* | LM Studio API token (`sk-lm-...`) — required when LM Studio auth is enabled |
| `OAI_COMPATIBLE_URL` | `http://host.docker.internal:1234/v1` | OpenAI-compatible embedding URL |
| `EMBED_MODEL` | `text-embedding-nomic-embed-text-v2` | Embedding model (768d) |
| `EMBED_DIMENSIONS` | `768` | Embedding dimensions — 768 (nomic) or 1536 (Qwen3) |
| `LLM_ENABLED` | `false` | Enable LLM brain (Deriver/Dreamer/Chat synthesis) |
| `LLM_PROVIDER` | `lm-studio` | LLM provider: `lm-studio` or `openai-compatible` |
| `LLM_MODEL` | `qwen3.6-35b-a3b-uncensored-hauhaucs-aggressive` | LLM model for brain |
| `LLM_API_KEY` | *(empty)* | LM Studio API token (same as `LM_STUDIO_API_KEY`) |
| `LIBRARIAN_MODE` | `workflow` | Librarian: `workflow` (code tagging+rerank <10ms, default) or `hybrid` (workflow + AI 1.5s cap) |
| `VAULT_ROOT` | `/data/ai` | Vault mount inside Docker |

## API Reference

### Health Check

```bash
GET /api/v1/health
```

Returns service status and configuration. No auth required.

### Create Workspace

```bash
POST /api/v1/workspaces
Content-Type: application/json

{
  "name": "agent-sarah"
}
```

### List Workspaces

```bash
GET /api/v1/workspaces
```

### Get Workspace Stats

```bash
GET /api/v1/workspaces/:id/stats
```

Returns document count and metadata for a workspace.

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
    "source_type": "file",
    "source_path": "vault/20-knowledge/10-topics/foo.md",
    "header_path": "Foo > Bar",
    "tags": "foo,bar",
    "entities": "Alfirus,Bukku",
    "importance": 3,
    "agent": "maisarah"
  }
}
```

Stores a message, chunks it if too long (6000 max, vault uses 3600-4800), generates embeddings 768d, and upserts to ChromaDB.

### Batch Store Messages

```bash
POST /api/v1/messages/batch
Content-Type: application/json

{
  "workspace_id": "family",
  "messages": [
    {"session_id": "maisarah-vault-...", "role": "user", "content": "...", "metadata": {...}},
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

Search across messages using semantic similarity. Pipeline: `embed query 768d → HNSW cosine → graph 1-hop (GRAPH.json, 5 neighbors) → WorkflowRerankScore (vector 0.55 + BM25 0.25 + importance 0.08 + entityOverlap 0.07) → top-k`.

### Semantic Search (Query Params)

```bash
GET /api/v1/messages/search?q=account+billing&workspace_id=family&n_results=5
```

### Search All Workspaces

```bash
POST /api/v1/messages/search/all
Content-Type: application/json

{
  "query": "deployment process",
  "n_results": 10
}
```

Searches all workspaces in parallel, merges via RRF fusion, returns results sorted by score.

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

## Authentication

Set `DEFAULT_API_KEY` in `.env` to enable API key authentication:

```bash
curl -H "X-API-Key: vectorizer-local-key" \
  http://localhost:8091/api/v1/messages \
  -d '{"workspace_id":"ws-abc","session_id":"sess-def","role":"user","content":"test"}'
```

Health check (`/api/v1/health`) is always public.

## Workspace Isolation

Each workspace maps to a separate ChromaDB collection: `ws_<workspace_id>`. This ensures agents never see each other's memories. Search can be scoped to:

- A specific workspace (recommended) — `family` holds all vault + _shared
- A specific session within a workspace
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

768d is sufficient — recall comes from markdown-aware chunking (3600-4800+200, header_path prefix) + rich metadata (11-field + entities) + workflow rerank, not dimensions.

## Librarian — Workflow vs AI

| Mode | How it works | Latency | When to use |
|------|--------------|---------|-------------|
| `workflow` (default) | Tagging: `keyword_tags` + known entities + `header_path`; Rerank: `WorkflowRerankScore` formula | ~150ms total | Always — deterministic, fast, private, git-diffable |
| `hybrid` | Same workflow + async Brain enrichment: `VaultTagSystem` tags+entities async, `VaultRerankSystem` order with 1.5s cap (≥3 hits) | +0-1.5s | Complex ambiguous queries ("that drone thing") — AI rescues synonyms |

## Integrations

MCP (Claude Desktop, OpenCode, OpenClaw, Hermes via `mcp-remote`):

```json
{"mcpServers":{"vectorizer":{"command":"node","args":["./mcp/dist/index.js"],"env":{"VECTORIZER_URL":"http://localhost:8091","VECTORIZER_API_KEY":"vectorizer-local-key","VECTORIZER_WORKSPACE_ID":"family"}}}}
```

SDKs: `sdks/typescript` (`@vectorizer/sdk`) + `sdks/python` (`vectorizer-ai`) — `new Vectorizer({baseUrl, apiKey}).search("...")`.

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
│   ├── chromadb/     # ChromaDB v2 API client
│   ├── embedding/    # Embedder interface (openai-compatible LM Studio nomic-768, google, vLLM)
│   ├── llmbrain/     # LLM brain (10-minute timeout) + prompts.go
│   ├── store/
│   │   ├── store.go  # Store + Vault librarian
│   │   ├── graph.go  # File-graph (GRAPH.json, Expand 1-hop)
│   │   ├── workflow_rerank.go
│   │   ├── reasoning.go, scopes.go, keys.go, conclusions.go, peers.go
│   ├── deriver/      # Async deriver 2s/5msg
│   ├── dreamer/      # Surprisal dreamer 3h
│   ├── handlers/     # Workspaces, messages, peers, chat
│   ├── models/       # Workspace, Session, Message, Peer, PeerCard, SearchRequest
│   ├── grpc/         # gRPC server
│   ├── security/     # JWT
│   └── webhooks/     # Webhook manager
├── mcp/              # MCP proxy (13 tools)
├── skills/           # Skills
├── sdks/             # TS + Python SDKs
├── evals/            # Eval harness
├── scripts/          # Vault indexer, backup, healthcheck, migration
├── main.go           # Entry point
├── Dockerfile        # Container build
├── docker-compose.yml       # Full stack (local)
├── docker-compose.server.yml # Production server
├── Makefile          # Build/run commands
└── .env.example      # Configuration template
```

## License

MIT
