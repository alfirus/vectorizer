# Vectorizer — Architecture Blueprint

**Version:** 0.3.0  
**Status:** Honcho-competitive agentic (self-hosted, Go/Chroma 1536d Qwen3, reasoning graph + deriver)  
**Last updated:** 2026-08-25

---

## Table of Contents

1. [System Overview](#1-system-overview)
2. [Architecture](#2-architecture)
3. [Component Design](#3-component-design)
4. [Data Flow](#4-data-flow)
5. [API Contract](#5-api-contract)
6. [Configuration Schema](#6-configuration-schema)
7. [Deployment Topology](#7-deployment-topology)
8. [Design Decisions & Tradeoffs](#8-design-decisions--tradeoffs)
9. [Future Roadmap](#9-future-roadmap)

---

## 1. System Overview

Vectorizer is a self-hosted semantic memory server for AI agents. It provides:

- **Message storage** with automatic chunking and embedding generation (4000 chars, `Qwen/Qwen3-Embedding-4B` 1536d via MRL, `nomic-embed-text` 768d fallback)
- **Semantic + hybrid search** (vector cosine HNSW + BM25 RRF) with scoping and temporal filters
- **Peers + peer cards** (Honcho `Peer` parity, `ws_<id>_peers` / `ws_<id>_peer_cards`)
- **Dialectic chat** (`POST /workspaces/:id/chat`, observer/observed, `reasoning_level` none/low/medium/high/max)
- **Conclusions + representations + dreamer** (offline `summarize→embed 1536d→ws_<id>_conclusions`, `qwen-embed` MRL)
- **Optional LLM brain** for summarization and Q&A (SSE streaming, auto-fetch)
- **Workspace isolation** — each agent gets its own namespace (`ws_<id>`)
- **MCP + Skills + SDKs** (Honcho-style `mcp-remote`, `npx skills add`, `@vectorizer/sdk` / `vectorizer-ai`)

### Design Goals

1. **Zero hidden AI costs** — embeddings are configurable; the LLM brain is opt-in
2. **Agent isolation** — workspaces map to separate ChromaDB collections, no cross-talk
3. **Provider flexibility** — LM Studio (local), OpenAI-compatible APIs for both embedding and chat
4. **Docker-first** — one `docker compose up` runs the full stack

### What It Is NOT

- Not a database replacement — it's an append-only memory layer on top of ChromaDB
- Not a session store — sessions are logical groupings, not persisted independently
- Honcho-competitive core: peers, dialectic, conclusions/representations, dreamer, scopes — but single-binary Go/Chroma (no Postgres/pgvector), 1536d pinned (Qwen3 4B MRL)

---

## 2. Architecture

### High-Level Diagram

```
┌─────────────┐     ┌──────────────────┐     ┌─────────────┐
│   Agent(s)  │────▶│  Vectorizer API  │────▶│  ChromaDB   │
│             │     │  (Fiber/Go)      │     │  (vectors)  │
│ Workspace A │     │                  │     └─────────────┘
│ Workspace B │     │  ┌──────────┐    ┌─────────────┐
│ Workspace C │     │  │ Embedding│    │ LLM Brain   │◀──┐
└─────────────┘     │  │ Service  │    │ (optional)  │   │
                    │  └──────────┘    └─────────────┘   │
                    └──────────────────┘                  │
                                                          │
                    ┌──────────────────┐                  │
                    │ LM Studio /      │◀─────────────────┘
                    │ OpenAI-Compatible│
                    └──────────────────┘
```

### Component Layers

| Layer | Package | Responsibility |
|-------|---------|----------------|
| **Config** | `config/` | Layered config: `env > .env > config.toml > defaults` (Honcho `TOML_CONFIG` parity, BurntSushi/toml) |
| **Security** | `internal/security/` | JWT `w/p/ad` claims, HS256, `scripts/generate_jwt.go` (Honcho `src/security.py` parity) |
| **ChromaDB Client** | `internal/chromadb/` | ChromaDB v2 REST API wrapper (collections, upsert, query, get, heartbeat) |
| **Embedding Service** | `internal/embedding/` | Text-to-vector conversion (1536d `Qwen3-Embedding-4B` MRL, `dimensions` param, fallback `nomic-embed-text` 768d) |
| **LLM Brain** | `internal/llmbrain/` | Chat/summarize via `/chat/completions` (`ChatWithTemp`, `ChatWithHistory`, `prompts.go` AgentSystemPrompt) |
| **Models** | `internal/models/` | Workspace, Session, Message (with `Metadata`), Peer, PeerCard, SearchRequest |
| **Store** | `internal/store/` | Chunking, embed (`1536d`), upsert (retry), search (vector+BM25 RRF), conclusions, `reasoning.go` (`ws_<id>_reasoning`), peers, sessions, grep, TTL, scopes |
| **Deriver** | `internal/deriver/` | Async `Enqueue` → `2s/5msg` batch → `Summarize → CreateConclusion + AddReasoningEdge` (Honcho `src/deriver`) |
| **Dreamer** | `internal/dreamer/` | Surprisal `3h` cron: `QueryConclusions` distance `<0.15` skip → `summarize→embed 1536d→ws_<id>_conclusions` |
| **Webhooks** | `internal/webhooks/` | In-mem endpoint registry + `Fire` (Honcho `src/webhooks`) |
| **gRPC** | `internal/grpc/` + `proto/vectorizer.proto` | `VectorizerService` (`AddMessage/Search/Chat/Health`) alongside REST `:50051` |
| **Handlers** | `internal/handlers/` | Workspaces, messages (deriver Enqueue), sessions, peers, chat (agentic 5-tool loop), scopes, conclusions, keys, webhooks, brain, admin |
| **MCP** | `mcp/` | Stdio MCP proxy (`@modelcontextprotocol/sdk`, 13 tools, Honcho `mcp/` parity) |
| **Main** | `main.go` | Wiring, deriver start/stop, JWT/X-API-Key auth, rate limit, health/metrics, gRPC, dreamer cron |

---

## 3. Component Design

### 3.1 Config (`config/config.go`, `config.toml.example`)

Layered config, Honcho `src/config.py` parity: `env > .env > config.toml > defaults` via `BurntSushi/toml`. Sections `[app]`, `[db]`, `[auth]`, `[embedding]`, `[llm]`.

**Key design decisions:**
- Embedding and LLM brain have **separate provider configs**
- `LLM_ENABLED` gate; `AUTH_USE_AUTH` JWT gate (Honcho `AUTH_USE_AUTH`)
- Pinned 1536d (`Qwen/Qwen3-Embedding-4B` MRL), same dim for `ws_<id>` / `ws_<id>_conclusions` / `ws_<id>_dream` / `ws_<id>_peers`

**Config fields:**

| Field | Type | Default | Purpose |
|-------|------|---------|---------|
| `Port` | int | 8091 | HTTP server port |
| `ChromaHost` | string | localhost | ChromaDB hostname |
| `ChromaPort` | int | 8100 | ChromaDB port |
| `DefaultAPIKey` | string | "" | API key for auth (empty = disabled) |
| `EmbedProvider` | string | lm-studio | Embedding provider: "lm-studio" or "openai-compatible" |
| `LmStudioURL` | string | http://localhost:1234/v1 | LM Studio endpoint |
| `OAICompatibleURL` | string | "" | OpenAI-compatible embedding URL |
| `OAIAPIKey` | string | "" | API key for OpenAI-compatible provider |
| `EmbedModel` | string | nomic-embed-text | Embedding model name |
| `LLMEnabled` | bool | false | Enable LLM brain endpoints |
| `LLMProvider` | string | lm-studio | LLM provider: "lm-studio" or "openai-compatible" |
| `LLMLmStudioURL` | string | (same as embed) | LM Studio chat endpoint |
| `LLMOAICompatibleURL` | string | "" | OpenAI-compatible chat URL |
| `LLMOAIAPIKey` | string | "" | API key for LLM provider |
| `LLMModel` | string | qwen3:8b | LLM model name |
| `ChromaTenant` | string | default_tenant | ChromaDB tenant (v2 API) |
| `ChromaDatabase` | string | default_database | ChromaDB database (v2 API) |
| `TTLHours` | int | 0 | Auto-delete `created_at` < now-TTL (0=disabled) |
| `AuthUseAuth` | bool | false | Enable JWT auth (`AUTH_USE_AUTH`, Honcho parity) |
| `AuthJWTSecret` | string | "" | HS256 secret (`AUTH_JWT_SECRET`, `w/p/ad` claims) |

### 3.2 ChromaDB Client (`internal/chromadb/client.go`)

Direct HTTP client for ChromaDB v2 REST API. No ORM, no abstraction layer — just thin wrappers around the actual API endpoints.

**Methods:**
- `EnsureCollection(name, metadata)` → POST to collections endpoint with `get_or_create: true`
- `GetCollection(name)` → GET collection by name (returns ID for operations)
- `DeleteCollection(id)` → DELETE collection
- `AddDocuments(collectionID, ids, docs, metadatas, embeddings)` → POST /add
- `UpsertDocuments(...)` → POST /upsert (idempotent — overwrites existing IDs)
- `Query(collectionID, queryEmbeddings, nResults, where, include)` → POST /query with pre-computed embeddings
- `ListCollections()` → GET all collections
- `Count(collectionID)` → GET document count
- `DeleteDocuments(ids)` → POST /delete by ID
- `DeleteByFilter(where)` → POST /delete by metadata filter

**ChromaDB configuration:**
- Distance metric: **cosine** (semantic similarity, not Euclidean distance)
- HNSW parameters: `ef_construction=200`, `ef_search=128` for high recall
- Collections are created with metadata tags (`workspace_id`, `session_id`) for filtering

### 3.3 Embedding Service (`internal/embedding/service.go`)

Pluggable embedding provider. Supports any OpenAI-compatible `/embeddings` endpoint, including:
- LM Studio local server (no API key needed)
- OpenAI API (`text-embedding-3-small`, `text-embedding-3-large`)
- Any other compatible provider (Cohere, Voyage AI, etc.)

**Methods:**
- `Embed(texts []string)` → batch embedding, returns `[][]float32`
- `EmbedSingle(text string)` → convenience wrapper for single text

**Response format:** Parses OpenAI-compatible response structure:
```json
{ "data": [{ "embedding": [0.1, 0.2, ...] }] }
```

### 3.4 LLM Brain (`internal/llmbrain/service.go` + `prompts.go`)

Optional service for summarization and agentic Q&A. Uses the OpenAI-compatible `/chat/completions` endpoint.

**Methods:**
- `Summarize(req)` → concise summarizer, temp 0.3
- `Ask(question, context)` → RAG, temp 0.3
- `ChatWithTemp(system, context, question, temp)` → dialectic temp per `reasoning_level`
- `ChatWithHistory(messages, temp)` → agentic loop history (Honcho `DialecticAgent` tool loop)
- `AgentSystemPrompt(observer, observed, obsCard, obsdCard)` → Honcho `dialectic/prompts.py` parity (perspective + peer card + 5 tools listing)

### 3.5 Store (`internal/store/store.go` + `reasoning.go` + `conclusions.go`)

Core business logic layer. Orchestrates embedding generation (1536d), chunking, ChromaDB, and reasoning graph.

**Key methods:**
- `AddMessage(msg, content)` → chunks 4000, embed 1536d batch, upsert `ws_<workspace_id>` with `scope`/`peer_id` metadata, 3× retry
- `Search` / `HybridSearch` → vector `EmbedSingle` + `HNSW cosine` (+ BM25 RRF), dedup/sort, temporal `after`/`before` via `SearchWithOptions`
- `GetReasoningChain(ws, conclusionID)` → BFS over `ws_<id>_reasoning` (`AddReasoningEdge`: `conclusion_id → premise_ids + supporting_message_ids`), `GetObservationContext(ws, session, chunkID, window=2)` → surrounding chunks
- `CreateConclusion` / `QueryConclusions` → `ws_<id>_conclusions` (1536d) + `GetRepresentation` (conclusions + session messages)
- `dummyVector()` → `embed.Dimensions()` dynamic (1536 for Qwen3)

**Chunking strategy:**
- Max 4000 characters per chunk (`maxChunkSize`), word-boundary, `chunk_index`/`total_chunks` metadata

**Collection naming:** `ws_<workspace_id>`, `ws_<id>_conclusions`, `ws_<id>_reasoning`, `ws_<id>_peers`, `ws_<id>_peer_cards`, `ws_<id>_scopes` — all same `1536d` dim (Honcho `(observer, observed)` parity, flattened to workspace).

### 3.6 Handlers (`internal/handlers/`)

| Handler | Routes | Responsibility |
|---------|--------|----------------|
| `WorkspacesHandler` | GET/POST /workspaces, GET /workspaces/:id | Workspace CRUD (Chroma-backed `ws_<id>`, `EnsureCollection`) |
| `MessagesHandler` | POST /messages, POST /messages/batch, GET/POST /messages/search, GET /workspaces/:id/stats, GET /messages | Messages (scope/peer_ids, NUL-strip, ValidateResourceName, hybrid `?hybrid=true`) |
| `SessionsHandler` | POST/GET /sessions | Sessions `{peer_ids, scope}` (`SaveSessionMeta` 1536d marker) |
| `PeersHandler` | POST/GET /workspaces/:id/peers, PUT/GET /peers/:peer_id/card | Peers + peer cards (`ws_<id>_peers`, `ws_<id>_peer_cards` 1536d) |
| `ChatHandler` | POST/GET /workspaces/:id/chat | Dialectic chat (observer/observed, `reasoning_level` none/low/medium/high/max) |
| `IngestHandler` | POST /messages/upload, GET /messages/grep, GET /messages/temporal, DELETE /workspaces/:id/ttl | Upload, grep, temporal search, TTL cleanup |
| `ConclusionsHandler` | POST/GET /conclusions, DELETE /conclusions/:id, GET /representations | Conclusions (`ws_<id>_conclusions` 1536d) + representations |
| `WebhooksHandler` | POST/GET /webhooks | Endpoint registry |
| `BrainHandler` | POST /brain/summarize (+ stream SSE), POST /brain/ask | LLM summarize/ask (auto-fetch, `ChatWithTemp`) |

### 3.7 Main (`main.go`)

Dependency injection and wiring:

1. Load config from env vars
2. Initialize ChromaDB client (with health check)
3. Initialize embedding service based on provider config
4. Initialize store with chromadb + embedding dependencies
5. Conditionally initialize LLM brain if `LLM_ENABLED=true`
6. Create handlers, wire them to their dependencies
7. Set up Fiber app with middleware chain: logger → CORS → optional API key auth
8. Register routes under `/api/v1/` prefix

**Middleware order:**
```
Request → Logger → CORS → API Key Auth (if enabled) → Router → Handler
```

---

## 4. Data Flow

### 4.1 Message Storage Flow

```
Agent POST /messages {workspace_id, session_id, role, content}
    │
    ▼
MessagesHandler.AddMessage()
    │
    ▼
Store.AddMessage(msg, content)
    │
    ├──► chunkText(content, 4000)     // split into chunks
    │
    ├──► embedService.Embed(documents) // generate vectors
    │
    └──► chroma.UpsertDocuments()      // store in ChromaDB
         │
         ▼
    Collection: ws_<workspace_id>
    Documents: [chunk_0, chunk_1, ...]
    Metadata:  {message_id, session_id, workspace_id, role, created_at}
```

### 4.2 Semantic Search Flow

```
Agent GET /messages/search?q=...&workspace_id=...
    │
    ▼
MessagesHandler.SearchMessagesSimple()
    │
    ▼
Store.Search(query, workspaceID, sessionID, role, nResults)
    │
    ├──► embedService.EmbedSingle(query)  // generate query vector
    │
    ├──► Build where filter from params   // workspace_id, session_id, role
    │
    ├──► For each workspace:
    │       chroma.Query(collectionID, queryEmbedding, nResults, whereFilter)
    │           │
    │           ▼
    │      ChromaDB HNSW search (cosine distance)
    │           │
    │           ▼
    │      Return top-k chunks with metadata + distances
    │
    └──► Aggregate results across workspaces
         │
         ▼
    JSON response: {results: [...], count: N}
```

### 4.3 LLM Brain Flow (when enabled)

#### Summarization

```
Agent POST /brain/summarize {text, max_chars, workspace_id?}
    │
    ▼
BrainHandler.Summarize()
    │
    ├──► If text provided: use directly
    └──► If workspace/session provided: fetch messages (future)
         │
         ▼
    brain.Summarize(SummarizeRequest{text, max_chars})
         │
         ▼
    POST /chat/completions {model, messages:[system+user], temperature:0.3}
         │
         ▼
    LLM returns summary → JSON response: {summary: "..."}
```

#### Q&A

```
Agent POST /brain/ask {question, context?, workspace_id?}
    │
    ▼
BrainHandler.Ask()
    │
    ├──► If context provided: use directly
    └──► If workspace/session provided: search + pass results as context (future)
         │
         ▼
    brain.Ask(question, context)
         │
         ▼
    POST /chat/completions {model, messages:[system+user], temperature:0.3}
         │
         ▼
    LLM returns answer → JSON response: {answer: "..."}
```

---

## 5. API Contract

### Base URL

All endpoints under `/api/v1/`. Health check is the only exception (public, no auth).

### Authentication

When `DEFAULT_API_KEY` is set, all requests require header:
```
X-API-Key: <your-secret-key>
```

Health check (`GET /api/v1/health`) is always public.

### Endpoints

#### GET `/api/v1/health`

**Response (200):**
```json
{
  "status": "ok",
  "name": "vectorizer",
  "version": "0.1.0",
  "llm_enabled": false
}
```

#### POST `/api/v1/workspaces`

Create a new workspace namespace.

**Request:**
```json
{ "name": "agent-sarah" }
```

**Response (201):**
```json
{
  "id": "550e8400-e29b-41d4-a716-446655440000",
  "name": "agent-sarah"
}
```

#### GET `/api/v1/workspaces`

List all workspaces.

**Response (200):**
```json
{ "workspaces": [] }
```

#### GET `/api/v1/workspaces/:id`

Get workspace by ID.

**Response (200):**
```json
{
  "id": "...",
  "name": "agent-sarah"
}
```

#### POST `/api/v1/messages`

Store a single message with automatic embedding.

**Request:**
```json
{
  "workspace_id": "ws-abc123",
  "session_id": "sess-def456",
  "role": "user",
  "content": "Hello, I need help with my account"
}
```

**Response (201):**
```json
{
  "id": "...",
  "workspace_id": "ws-abc123",
  "session_id": "sess-def456",
  "role": "user",
  "stored": true
}
```

#### POST `/api/v1/messages/batch`

Store multiple messages in one request.

**Request:**
```json
{
  "messages": [
    { "session_id": "sess-def456", "role": "user", "content": "..." },
    { "session_id": "sess-def456", "role": "assistant", "content": "..." }
  ]
}
```

**Response (201):**
```json
{
  "results": [
    { "id": "...", "session_id": "sess-def456", "role": "user", "stored": true },
    { "id": "...", "session_id": "sess-def456", "role": "assistant", "stored": true }
  ]
}
```

#### POST `/api/v1/messages/search` (JSON body)

Semantic search with metadata filtering.

**Request:**
```json
{
  "query": "account billing issue",
  "n_results": 5,
  "where": {
    "workspace_id": "ws-abc123",
    "role": "user"
  }
}
```

**Response (200):**
```json
{
  "results": [
    {
      "id": "..._chunk_0",
      "document": "Hello, I need help with my account billing...",
      "metadata": {
        "message_id": "...",
        "session_id": "sess-def456",
        "workspace_id": "ws-abc123",
        "role": "user",
        "created_at": "2026-08-24T...",
        "chunk_index": 0,
        "total_chunks": 1
      },
      "distance": 0.15
    }
  ],
  "count": 1
}
```

#### GET `/api/v1/messages/search` (query params)

Simpler search using query parameters.

**Query params:** `q`, `workspace_id`, `session_id`, `role`, `n_results`

**Response:** Same as POST /messages/search.

#### GET `/api/v1/workspaces/:id/stats`

Get workspace document count.

**Response (200):**
```json
{
  "workspace_id": "ws-abc123",
  "document_count": 42
}
```

#### POST `/api/v1/brain/summarize` (requires LLM_ENABLED)

Summarize text using the configured LLM.

**Request:**
```json
{
  "text": "Long conversation transcript...",
  "max_chars": 500,
  "workspace_id": "ws-abc123"
}
```

**Response (200):**
```json
{
  "summary": "The user called about a billing discrepancy on their account..."
}
```

#### POST `/api/v1/brain/ask` (requires LLM_ENABLED)

Ask a question about stored memories.

**Request:**
```json
{
  "question": "What did the user ask about?",
  "context": "...",
  "workspace_id": "ws-abc123"
}
```

**Response (200):**
```json
{
  "answer": "The user asked about a billing discrepancy on their account..."
}
```

### Error Responses

All errors follow this format:
```json
{ "error": "description of what went wrong" }
```

HTTP status codes:
- `400` — Bad request (missing fields, invalid values)
- `401` — Missing API key
- `403` — Invalid API key
- `500` — Internal server error (ChromaDB down, embedding service unreachable)

---

## 6. Configuration Schema

### Environment Variables Reference

| Variable | Required | Default | Description |
|----------|----------|---------|-------------|
| `PORT` | No | `8091` | HTTP listen port |
| `CHROMA_HOST` | No | `localhost` | ChromaDB hostname |
| `CHROMA_PORT` | No | `8100` | ChromaDB port |
| `CHROMA_TENANT` | No | `default_tenant` | ChromaDB v2 tenant |
| `CHROMA_DATABASE` | No | `default_database` | ChromaDB v2 database |
| `DEFAULT_API_KEY` | No | *(empty)* | API key for auth (set to enable) |

**Embedding config:**

| Variable | Required | Default | Description |
|----------|----------|---------|-------------|
| `EMBED_PROVIDER` | No | `lm-studio` | Provider: `lm-studio` or `openai-compatible` |
| `LM_STUDIO_URL` | Conditional | `http://localhost:1234/v1` | LM Studio endpoint (required if provider=lm-studio) |
| `OAI_COMPATIBLE_URL` | Conditional | *(empty)* | OpenAI-compatible URL (required if provider=openai-compatible) |
| `OAI_API_KEY` | Conditional | *(empty)* | API key for OpenAI-compatible provider |
| `EMBED_MODEL` | No | `nomic-embed-text` | Embedding model name |

**LLM Brain config:**

| Variable | Required | Default | Description |
|----------|----------|---------|-------------|
| `LLM_ENABLED` | No | `false` | Enable LLM brain endpoints |
| `LLM_PROVIDER` | Conditional | `lm-studio` | Provider: `lm-studio` or `openai-compatible` |
| `LLM_STUDIO_URL` | Conditional | *(same as embed)* | LM Studio chat endpoint |
| `LLM_OAI_COMPATIBLE_URL` | Conditional | *(empty)* | OpenAI-compatible chat URL |
| `LLM_OAI_API_KEY` | Conditional | *(empty)* | API key for LLM provider |
| `LLM_MODEL` | No | `qwen3:8b` | LLM model name |

### Configuration Precedence (Honcho `TOML_CONFIG` parity)

1. Environment variables (highest)
2. `.env` file (godotenv)
3. `config.toml` / `config/config.toml` (`BurntSushi/toml`, disable via `VECTORIZER_CONFIG_TOML_DISABLED`)
4. Hardcoded defaults
5. Sections `[app]`, `[db]`, `[auth]`, `[embedding]`, `[llm]` in `config.toml.example`

---

## 7. Deployment Topology

### Docker Compose (Recommended)

```yaml
services:
  vectorizer:    # Go binary, port 8091
  chromadb:      # ChromaDB container, port 8100, persistent volume
```

**Ports:**
- `8091` — Vectorizer API
- `8100` — ChromaDB (internal + exposed for debugging)

**Volumes:**
- `chroma_data` — Persistent ChromaDB storage (survives container restarts)

### Production Deployment

For production on a VPS (e.g., 213.32.27.115):

```bash
# Clone, configure, deploy
git clone https://github.com/alfirus/vectorizer.git /opt/vectorizer
cd /opt/vectorizer
cp .env.example .env
# Edit .env with production values
docker compose up -d
```

**Recommended:** Place behind a reverse proxy (nginx/Caddy) for TLS termination. The Vectorizer itself doesn't handle HTTPS.

### Resource Requirements

| Component | CPU | Memory | Disk |
|-----------|-----|--------|------|
| Vectorizer | Minimal (<50m) | ~30MB | Negligible |
| ChromaDB | Low (~100m) | ~200MB+ (scales with data) | Depends on vector count |

The embedding model loads into memory on first call. `nomic-embed-text` is ~250MB RAM. The LLM brain model loads separately when enabled.

---

## 8. Design Decisions & Tradeoffs

### Why Go?

- **Low memory footprint** — the binary is statically linked, no runtime overhead
- **Fast startup** — sub-second cold start vs Python's import latency
- **Single binary deployment** — no pip install, no virtualenv, no Python version conflicts
- **Matches existing backend ecosystem** — consistent with other Go services in the project

### Why ChromaDB over pgvector?

- **Simpler ops** — single container, no PostgreSQL dependency
- **Built-in vector index** — HNSW is configured at collection creation, no manual index management
- **Cosine distance by default** — correct for text embeddings without tuning
- **REST API first** — language-agnostic, easy to integrate from any client

### Why per-workspace collections?

Each workspace gets its own ChromaDB collection (`ws_<id>`). This provides:
- **Hard isolation** — no cross-talk between agents
- **Independent scaling** — large workspaces don't slow down small ones
- **Easy cleanup** — delete a workspace by dropping its collection

Tradeoff: more collections = slightly higher ChromaDB overhead. For 100+ agents, consider per-workspace sharding or a single collection with workspace_id metadata filtering.

### Why chunk at 4000 chars?

This is a conservative default that works well for most messages. Tradeoffs:
- **Smaller chunks** → better precision in search but more documents to scan
- **Larger chunks** → fewer documents but risk losing context boundaries

The chunking is word-boundary aware (breaks at spaces) to avoid cutting words mid-stream.

### Why no session persistence?

Sessions are logical groupings passed through metadata, not persisted as first-class entities. Reasons:
- ChromaDB collections already provide the isolation sessions need
- Adding a separate session table adds complexity without clear benefit for the current use case
- Sessions can be queried via metadata filters (`where.session_id = "..."`)

### Why is the LLM brain optional?

Not every deployment needs summarization or Q&A. By making it opt-in:
- **Zero-cost deployments** — pure storage + search with no LLM calls
- **Provider flexibility** — use LM Studio for embeddings, OpenAI for the brain, or both local
- **Cost control** — agents that only need semantic search don't pay for chat completions

### Why not auto-fetch context for brain endpoints?

The `/brain/summarize` and `/brain/ask` endpoints accept `workspace_id` and `session_id` but currently require the caller to pass pre-fetched content. This keeps the brain service decoupled from the store — it only needs text, nothing else. Future enhancement: have the handler fetch messages before calling the brain.

---

## 9. Future Roadmap

### Phase 1: Core Stability — done
- [x] Message storage with embedding generation (1536d Qwen3 MRL, chunking, retry, deriver enqueue)
- [x] Semantic search with metadata filtering + hybrid RRF + `scope`/`peer_id`
- [x] Optional LLM brain (summarize + ask, auto-fetch, SSE, `ChatWithHistory` tool loop)
- [x] Docker Compose deployment (`chromadb:1.0.0` + `qwen-embed` vLLM `1536d`)
- [x] Workspace persistence (`ws_<id>` via Chroma, `EnsureCollection`)
- [x] Session/message retrieval (`POST/GET /sessions`, `GET /messages`, `GET /messages/temporal`, `grep`, `Grep`)

### Phase 2: Search Enhancements — done
- [x] Hybrid search (BM25 + RRF `HybridSearch` + `rrfFusion`)
- [x] Result ranking and deduplication (global sort, `distance` ascending)
- [x] Pagination (`limit`/`offset` `0..100`, `SearchWithOptions`)
- [x] Date range filtering (`after`/`before` RFC3339)
- [x] Grep (`GET /messages/grep`) + temporal (`GET /messages/temporal`) + `get_observation_context`

### Phase 3: Brain Integration — done (agentic, Honcho parity)
- [x] Auto-fetch for brain endpoints + `POST /workspaces/:id/chat` (observer/observed, `AgentSystemPrompt`)
- [x] Agentic dialectic loop: `5` tools (`search_memory`, `search_messages`, `grep_messages`, `get_reasoning_chain`, `get_observation_context`) via `ChatWithHistory`, `maxTools` per `reasoning_level` (`none=0, low=2, medium=4, high=6, max=8`)
- [x] Streaming responses (SSE `GET /brain/summarize/stream`, `GET /workspaces/:id/chat/stream`)
- [x] Conversation history summarization (surprisal dreamer `3h`, `distance<0.15` skip)
- [x] Deriver (`internal/deriver`, `2s/5msg` batch → `Summarize Extract 1-3 facts → CreateConclusion + AddReasoningEdge`)
- [x] Reasoning graph (`ws_<id>_reasoning`, `GetReasoningChain` BFS, `GetObservationContext` window)

### Phase 4: Scale & Reliability — done
- [x] Health check with ChromaDB `Heartbeat` (degraded if down, `version 0.3.0`)
- [x] Graceful degradation (deriver queue + `3×` retry, `continue` on query error)
- [x] Rate limiting (`10/s` token bucket per API key / JWT `w`, `429`, `isHealthOrMetrics`)
- [x] Metrics (`/metrics` Prometheus, `vectorizer_messages_total`)
- [x] Batch upsert with retry/backoff (3× exponential, `100ms * 2^attempt`)
- [x] JWT scoping (`w` must match `:workspace_id`/`:id`, `Admin` bypass)

### Phase 5: Advanced Features — done (Honcho parity)
- [x] Message TTL (`DELETE /workspaces/:id/ttl`, `TTL_HOURS`, `TTLDelete`)
- [x] Cross-workspace search (deduped `Search` fan-out, `HybridSearch` fallback)
- [x] Webhook notifications (`POST/GET /webhooks`, `DELETE /:id`, `GET /test`, `Fire`)
- [x] Conclusions/representations (`POST/GET /conclusions`, `POST /conclusions/batch`, `POST /conclusions/query`, `ws_<id>_conclusions` 1536d, `GetRepresentation`)
- [x] Peers + peer cards (`POST/GET /workspaces/:id/peers`, `PUT /workspaces/:id/peers/:peer_id`, `PUT/GET /peers/:peer_id/card`, `ws_<id>_peers`, `ws_<id>_peer_cards`)
- [x] JWT auth (`w/p/ad`, `AUTH_USE_AUTH`, `AUTH_JWT_SECRET`, `scripts/generate_jwt/main.go`)
- [x] Layered config (`config.toml`, `env > .env > config.toml > defaults`, `BurntSushi/toml`, `EMBED_DIMENSIONS=1536`)
- [x] Eval harness (`evals/run.go` `recall` + `reasoning-grounded` via `chat`, `evals/data/sample.jsonl`)
- [x] Embedding model hot-swap without restart (`POST /admin/embedding {model, base_url, dimensions}`, `GET /admin/embedding`, `SetModel/SetDimensions`)
- [x] gRPC interface alongside REST (`proto/vectorizer.proto` `AddMessage/Search/Chat/Health`, `vectorizerpb`, `internal/grpc`, `GRPC_PORT` 50051)
- [x] Scopes (`ws_<id>_scopes`, `POST/GET /workspaces/:id/scopes`, `POST/:scope_id/sessions`, `DELETE/:scope_id/sessions/:sid`, `GET/:scope_id/status`)
- [x] Keys (`POST/GET /workspaces/:id/keys`, `POST/GET /keys`, `sk_*`)

---

*This blueprint is a living document. Update it as the architecture evolves.*
