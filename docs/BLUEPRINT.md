# Vectorizer — Architecture Blueprint

**Version:** 0.1.0  
**Status:** Initial implementation  
**Last updated:** 2026-08-24

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

- **Message storage** with automatic chunking and embedding generation
- **Semantic search** across stored messages using vector similarity
- **Optional LLM brain** for summarization and question answering
- **Workspace isolation** — each agent gets its own namespace (ChromaDB collection)

### Design Goals

1. **Zero hidden AI costs** — embeddings are configurable; the LLM brain is opt-in
2. **Agent isolation** — workspaces map to separate ChromaDB collections, no cross-talk
3. **Provider flexibility** — LM Studio (local), OpenAI-compatible APIs for both embedding and chat
4. **Docker-first** — one `docker compose up` runs the full stack

### What It Is NOT

- Not a database replacement — it's an append-only memory layer on top of ChromaDB
- Not a session store — sessions are logical groupings, not persisted independently
- Not a full Honcho clone — no derivation pipeline, no dialectic endpoint, no user profiles

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
| **Config** | `config/` | Environment variable loading, provider selection |
| **ChromaDB Client** | `internal/chromadb/` | ChromaDB v2 REST API wrapper (collections, upsert, query) |
| **Embedding Service** | `internal/embedding/` | Text-to-vector conversion via LM Studio or OpenAI-compatible APIs |
| **LLM Brain** | `internal/llmbrain/` | Optional summarization and Q&A via chat completions API |
| **Models** | `internal/models/` | Data structures (Workspace, Session, Message, SearchRequest) |
| **Store** | `internal/store/` | Core storage logic: chunking, embedding generation, upsert, search |
| **Handlers** | `internal/handlers/` | HTTP request handlers for Fiber routes |
| **Main** | `main.go` | Dependency wiring, middleware setup, route registration |

---

## 3. Component Design

### 3.1 Config (`config/config.go`)

Loads all configuration from environment variables with sensible defaults.

**Key design decisions:**
- Embedding and LLM brain have **separate provider configs** — you can use LM Studio for embeddings but OpenAI for the brain, or vice versa
- `LLM_ENABLED` is a boolean gate; when false, the brain service is nil and `/brain/*` routes are never registered
- Fallback chain: env var → `.env` file (via godotenv) → hardcoded default

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

### 3.4 LLM Brain (`internal/llmbrain/service.go`)

Optional service for summarization and Q&A. Uses the OpenAI-compatible `/chat/completions` endpoint.

**Methods:**
- `Summarize(req)` → sends text with system prompt "You are a concise summarizer" to the LLM
- `Ask(question, context)` → RAG-style: combines retrieved context with user question

**Prompt templates:**
- Summarization: System prompt enforces conciseness. Temperature 0.3 for consistency.
- Q&A: System prompt restricts answers to provided context. Falls back gracefully if answer isn't in context.

### 3.5 Store (`internal/store/store.go`)

Core business logic layer. Orchestrates embedding generation, chunking, and ChromaDB operations.

**Key methods:**
- `AddMessage(msg, content)` → chunks text, generates embeddings, upserts to ChromaDB
- `Search(query, workspaceID, sessionID, role, nResults)` → generates query embedding, searches across workspaces with metadata filtering
- `GetWorkspaceStats(workspaceID)` → returns document count

**Chunking strategy:**
- Max 4000 characters per chunk (configurable via `maxChunkSize` constant)
- Word-boundary aware: tries to break at the last space if past halfway point
- Each chunk gets metadata: message_id, session_id, workspace_id, role, created_at, chunk_index, total_chunks

**Collection naming:** `ws_<workspace_id>` — simple prefix convention for easy identification and filtering.

### 3.6 Handlers (`internal/handlers/`)

Three handler packages, each responsible for a domain:

| Handler | Routes | Responsibility |
|---------|--------|----------------|
| `WorkspacesHandler` | GET/POST /workspaces, GET /workspaces/:id | Workspace CRUD (in-memory) |
| `MessagesHandler` | POST /messages, POST /messages/batch, GET/POST /messages/search, GET /workspaces/:id/stats | Message storage and semantic search |
| `BrainHandler` | POST /brain/summarize, POST /brain/ask | LLM-powered summarization and Q&A |

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

### Configuration Precedence

1. Environment variables (highest priority)
2. `.env` file (loaded via godotenv)
3. Hardcoded defaults (lowest priority)

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

### Phase 1: Core Stability (current)
- [x] Message storage with embedding generation
- [x] Semantic search with metadata filtering
- [x] Optional LLM brain (summarize + ask)
- [x] Docker Compose deployment
- [ ] Workspace persistence (SQLite/PostgreSQL for workspace CRUD)
- [ ] Session/message retrieval endpoints

### Phase 2: Search Enhancements
- [ ] Hybrid search (semantic + keyword via BM25)
- [ ] Result ranking and deduplication across chunks from the same message
- [ ] Pagination support for large result sets
- [ ] Date range filtering in search

### Phase 3: Brain Integration
- [ ] Auto-fetch workspace/session messages for brain endpoints
- [ ] Streaming responses for long summaries (SSE)
- [ ] Conversation history summarization (rolling window)
- [ ] Multi-turn Q&A with conversation context

### Phase 4: Scale & Reliability
- [ ] Health check endpoint for ChromaDB dependency
- [ ] Graceful degradation when embedding service is unavailable
- [ ] Rate limiting per API key
- [ ] Metrics endpoints (/metrics for Prometheus)
- [ ] Batch upsert with retry logic and backoff

### Phase 5: Advanced Features
- [ ] Message TTL / automatic cleanup of old memories
- [ ] Cross-workspace search (with deduplication)
- [ ] Embedding model hot-swap without restart
- [ ] Webhook notifications for memory events
- [ ] gRPC interface alongside REST

---

*This blueprint is a living document. Update it as the architecture evolves.*
