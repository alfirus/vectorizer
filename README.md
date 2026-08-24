# Vectorizer — Semantic Memory Server

A lightweight, self-hosted memory server for AI agents. Stores messages as embeddings in ChromaDB with optional LLM-powered summarization and Q&A. Each agent gets isolated memory via workspace namespaces.

## Documentation

- **[Architecture Blueprint](docs/BLUEPRINT.md)** — full system design, data flows, API contract, config schema, deployment topology
- **This README** — quick start guide and usage reference

## Features

- **Workspace isolation** — `ws_<id>` collections, no cross-talk
- **Semantic + hybrid search** — vector cosine (HNSW) + BM25 RRF, `?hybrid=true`, temporal `after`/`before`
- **Peers + peer cards** — `POST /workspaces/:id/peers`, `PUT /peers/:peer_id/card` (Honcho peer parity)
- **Dialectic chat** — `POST /workspaces/:id/chat` (observer/observed, `reasoning_level` none/low/medium/high/max)
- **Conclusions + representations + dreamer** — offline `summarize→embed 768d→ws_<id>_conclusions` (10m cron)
- **Optional LLM brain** — SSE streaming, auto-fetch, summarization & RAG Q&A
- **Auth** — `X-API-Key` or JWT `w/p/ad` (`AUTH_USE_AUTH`, `scripts/generate_jwt.go`, Honcho parity)
- **Layered config** — `env > .env > config.toml > defaults` (`config.toml.example`)
- **Docker-ready** — one `docker compose up` (Chroma `1.0.0`, healthchecks, persistent volume)
- **MCP + Skills + SDKs** — Honcho-style `mcp-remote`, `skills/`, `@vectorizer/sdk` / `vectorizer-ai`
- **Evals** — `go run evals/run.go -file evals/data/sample.jsonl` (LongMemEval-style recall)

## Architecture

```
Agent → Vectorizer API → ChromaDB (vectors) + Embedding Service
  │                        │
  ├─ POST /messages       ├─ nomic-embed-text (local)
  ├─ GET  /search         └─ text-embedding-3-small (OpenAI)
  └─ POST /brain/ask      └─ qwen3:8b, gpt-4o-mini, etc.
```

## Quick Start

### Option A: Docker Compose (recommended)

```bash
# Clone and configure
git clone https://github.com/alfirus/vectorizer.git
cd vectorizer
cp .env.example .env
# Edit .env with your settings

# Start everything
docker compose up -d

# Verify it's running
curl http://localhost:8091/api/v1/health
```

### Option B: Local Development

```bash
# Ensure ChromaDB is running (or use docker-compose)
# Then:
make build && ./vectorizer.exe
```

## Configuration

All settings via `.env` file or environment variables. Key options:

| Variable | Default | Description |
|----------|---------|-------------|
| `PORT` | `8091` | Server port |
| `CHROMA_HOST` | `localhost` | ChromaDB hostname |
| `CHROMA_PORT` | `8100` | ChromaDB port |
| `DEFAULT_API_KEY` | *(empty)* | API key for auth (set to disable) |
| `EMBED_PROVIDER` | `lm-studio` | Embedding provider: `lm-studio` or `openai-compatible` |
| `LM_STUDIO_URL` | `http://localhost:1234/v1` | LM Studio endpoint |
| `EMBED_MODEL` | `nomic-embed-text` | Embedding model name |
| `LLM_ENABLED` | `false` | Enable LLM brain (summarize/ask) |
| `LLM_PROVIDER` | `lm-studio` | LLM provider: `lm-studio` or `openai-compatible` |
| `LLM_MODEL` | `qwen3:8b` | LLM model name |

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

Returns document count and metadata for a workspace.

### Store Message

```bash
POST /api/v1/messages
Content-Type: application/json

{
  "workspace_id": "ws-abc123",
  "session_id": "sess-def456",
  "role": "user",
  "content": "Hello, I need help with my account"
}
```

Stores a message, chunks it if too long, generates embeddings, and upserts to ChromaDB. Returns the stored message ID.

### Batch Store Messages

```bash
POST /api/v1/messages/batch
Content-Type: application/json

{
  "messages": [
    {"session_id": "sess-def456", "role": "user", "content": "..."},
    {"session_id": "sess-def456", "role": "assistant", "content": "..."}
  ]
}
```

Store multiple messages in a single request.

### Semantic Search (JSON)

```bash
POST /api/v1/messages/search
Content-Type: application/json

{
  "query": "account billing issue",
  "n_results": 5,
  "where": {
    "workspace_id": "ws-abc123",
    "role": "user"
  }
}
```

Search across messages using semantic similarity. Returns matching chunks with metadata and distance scores.

### Semantic Search (Query Params)

```bash
GET /api/v1/messages/search?q=account+billing&workspace_id=ws-abc123&n_results=5
```

Simpler search endpoint using query parameters instead of JSON body.

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

Summarizes text using the configured LLM. Can optionally fetch workspace/session messages for summarization (future enhancement).

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

Answers questions using LLM + retrieved context from semantic search. Pass `context` directly or let it fetch from workspace/session.

## Authentication

Set `DEFAULT_API_KEY` in `.env` to enable API key authentication:

```bash
curl -H "X-API-Key: your-secret-api-key-here" \
  http://localhost:8091/api/v1/messages \
  -d '{"workspace_id":"ws-abc","session_id":"sess-def","role":"user","content":"test"}'
```

Health check (`/api/v1/health`) is always public.

## Workspace Isolation

Each workspace maps to a separate ChromaDB collection: `ws_<workspace_id>`. This ensures agents never see each other's memories. Search can be scoped to:

- A specific workspace (recommended)
- A specific session within a workspace
- All workspaces (cross-agent search)

## Embedding Models

| Model | Provider | Dimensions | Notes |
|-------|----------|------------|-------|
| `nomic-embed-text` | LM Studio | 768 | Best local option, fast |
| `text-embedding-3-small` | OpenAI | 1536 | Good quality, cloud |
| `text-embedding-3-large` | OpenAI | 3072 | Highest quality, slower |

The embedding model is configured via `EMBED_MODEL`. ChromaDB handles vector storage and similarity search automatically.

## LLM Brain Modes

### Disabled (default)
Pure storage + search. Your agent brings its own LLM for synthesis/Q&A. Zero hidden costs.

### Enabled
Vectorizer calls an external LLM for:
- **Summarization** — condense long conversations into key facts
- **Q&A** — answer questions about stored memories using retrieved context

The brain is completely optional and configurable per deployment.

## End-to-End Workflow — User Prompt → AI → Vectorizer → Response

```
User Prompt
   │
   ▼
AI Agent (Hermes/OpenClaw/OpenCode/Claude) receives prompt
   │ 1. Agent calls Vectorizer FIRST (recall)
   ├─► vectorizer_search / POST /messages/search {query, workspace_id} ─┐
   │   or vectorizer_chat / POST /workspaces/:id/chat {query, observer} │
   │   Vectorizer: embed query (nomic-embed-text 768d) → HNSW cosine   │
   │   + optional BM25 RRF (?hybrid=true) → dedup/sort → top-k +       │
   │   peer cards + conclusions/representations                          │
   │ ◄─ returns {results, distances} + representation (peer card)       │
   │                                                                     │
   ├─► 2. Agent injects results + peer card into LLM system/user prompt │
   │      (scope/session filtering via session_id, peer_id, scope)     │
   │                                                                     │
   ├─► 3. LLM generates answer (external or brain Ask/Chat)             │
   │      dialectic reasoning_level none/low/medium/high/max controls   │
   │      n_results + temperature (0.1→0.9)                              │
   │                                                                     │
   ├─► 4. Agent calls Vectorizer AGAIN (record)                         │
   ├─► vectorizer_add_message / POST /messages {workspace_id, session_id, role:"user", content: prompt}
   ├─► vectorizer_add_message {role:"assistant", content: answer}
   │   Vectorizer: ValidateResourceName + SanitizeString (strip \x00)   │
   │   → chunkText 4000 (word-boundary) → Embed 768d batch → Upsert   │
   │   → ws_<workspace_id> collection, metadata {message_id, role, created_at, scope, peer_id}
   │   → 3× retry backoff; 100k char cap, 429 rate limit (10/s)         │
   │                                                                     │
   └─► 5. Async side-effects (no user wait)                             │
       ├─ dreamer cron (10m, 768d): summarize recent session → ws_<id>_conclusions
       ├─ conclusions/representations build peer view
       ├─ TTL cleanup (TTL_HOURS or DELETE /ttl?before=)
       └─ webhooks Fire("message.created") if registered
            │
            ▼
       Next turn reuses accumulated memory → long-term persona.
```

**Step-by-step:**

1. **User sends prompt** → AI agent intercepts.
2. **Recall:** Agent calls `vectorizer_search` or `vectorizer_chat` (or `POST /messages/search` + `GET /representations`) with `workspace_id` (agent identity), `session_id` (thread), optional `scope`/`hybrid`/`temporal`. Vectorizer embeds query 768d, HNSW cosine search (plus BM25 RRF if requested), merges peer cards/conclusions.
3. **Inject:** Agent prepends returned memories as context to its LLM.
4. **Generate:** LLM produces final answer grounded in memories (with observer/observed perspective if dialectic).
5. **Record:** Agent stores both user prompt and its answer via `vectorizer_add_message` / `POST /messages`; Vectorizer chunks, embeds, upserts.
6. **Background:** Dreamer, TTL, webhooks run offline; future recalls benefit.
7. **Fallback:** If `VECTORIZER_URL` down, agent proceeds without memory (graceful degradation); if `X-API-Key`/JWT missing, `401`; metrics at `/metrics`, health at `/api/v1/health` (public).

Honcho parity: this mirrors Honcho's `Store → Reason (deriver) → Query (chat/representation) → Inject` loop, but single-binary Go/Chroma.

## Integrations (Honcho-style)

MCP (Claude Desktop, OpenCode, OpenClaw, Hermes via `mcp-remote`):
```json
{"mcpServers":{"vectorizer":{"command":"node","args":["./mcp/dist/index.js"],"env":{"VECTORIZER_URL":"http://localhost:8091","VECTORIZER_API_KEY":"...","VECTORIZER_WORKSPACE_ID":"my-agent"}}}}
```
Tools: `vectorizer_search`, `vectorizer_add_message`, `vectorizer_ask`, `vectorizer_summarize`, etc. (see `mcp/README.md`).

Skill: `.agents/skills/vectorizer/SKILL.md` + `skills/vectorizer-memory/` — recall/record loop (store after turns, search before answering).

SDKs: `sdks/typescript` (`@vectorizer/sdk`) + `sdks/python` (`vectorizer-ai`) — `new Vectorizer({baseUrl, apiKey}).search("...")`.

## Development

```bash
# Build locally
make build

# Run with local ChromaDB
make run

# Run tests
make test

# Clean build artifacts
make clean
```

### Project Structure

```
vectorizer/
├── config/           # Configuration loading (env vars)
├── internal/
│   ├── chromadb/     # ChromaDB v2 API client
│   ├── embedding/    # Embedding service (LM Studio / OpenAI)
│   ├── llmbrain/     # Optional LLM summarization & Q&A
│   ├── handlers/     # HTTP request handlers
│   ├── models/       # Data structures
│   └── store/        # Core storage logic + chunking
├── main.go           # Entry point, route setup
├── Dockerfile        # Container build
├── docker-compose.yml# Full stack (Vectorizer + ChromaDB)
├── Makefile          # Build/run commands
└── .env.example      # Configuration template
```

## License

MIT
