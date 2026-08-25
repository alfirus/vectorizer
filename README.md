# Vectorizer — Semantic Memory Server

A lightweight, self-hosted memory server for AI agents. Stores messages as embeddings in ChromaDB with optional LLM-powered summarization and Q&A. Each agent gets isolated memory via workspace namespaces.

## Documentation

- **[Architecture Blueprint](docs/BLUEPRINT.md)** — full system design, data flows, API contract, config schema, deployment topology
- **This README** — quick start guide and usage reference

## Features

- **Workspace isolation** — `ws_<id>` collections, no cross-talk
- **Semantic + hybrid search** — vector cosine (HNSW) + BM25 RRF, `?hybrid=true`, temporal `after`/`before`, `grep` + `temporal` endpoints
- **Peers + peer cards** — `POST /workspaces/:id/peers`, `PUT /peers/:peer_id/card`
- **Agentic dialectic chat** — `POST /workspaces/:id/chat` (observer/observed, `reasoning_level` none/low/medium/high/max, 5 tools: `search_memory`, `search_messages`, `grep_messages`, `get_reasoning_chain`, `get_observation_context`, SSE streaming)
- **Reasoning graph + deriver** — `ws_<id>_reasoning` (premise edges, BFS `GetReasoningChain`), async deriver `2s/5msg` batch → `summarize→CreateConclusion+AddReasoningEdge`
- **Conclusions + representations + surprisal dreamer** — offline `summarize→embed 1536d→ws_<id>_conclusions` every `3h` with surprisal gate (`distance <0.15` skip)
- **Optional LLM brain** — SSE streaming, auto-fetch, summarization & RAG Q&A via `/chat/completions`
- **Embedder interface** — pluggable `embedding.Embedder` abstraction; swap providers without changing callers
- **Google AI Studio** — `EMBED_PROVIDER=google`, `GOOGLE_API_KEY`, `text-embedding-004` (768d default, batch+single)
- **Auth** — `X-API-Key` or JWT `w/p/ad` (`AUTH_USE_AUTH`, `scripts/generate_jwt/main.go`, Vectorizer parity)
- **Layered config** — `env > .env > config.toml > defaults` (`config.toml.example`, `BurntSushi/toml`)
- **Docker-ready** — one `docker compose up` (Chroma `1.0.0`, `qwen-embed` vLLM `1536d`, healthchecks, `qwen_cache`)
- **MCP + Skills + SDKs** — `mcp-remote` (13 tools + `vectorizer_chat`), `skills/vectorizer`, `@vectorizer/sdk` / `vectorizer-ai`
- **Evals** — `go run ./evals/run.go -file evals/data/sample.jsonl` (LongMemEval-style `recall` + `reasoning-grounded` via chat)

## Architecture

```
Agent → Vectorizer API → ChromaDB (vectors) + Embedding Service
  │                        │
  ├─ POST /messages       ├─ Qwen/Qwen3-Embedding-4B (1536d MRL, nomic-embed-text 768d fallback) (local)
  ├─ GET  /search         ├─ text-embedding-004 / text-embedding-005 (Google AI Studio)
  └─ POST /brain/ask      ├─ text-embedding-3-small (OpenAI)
                          └─ qwen3:8b, gpt-4o-mini, etc. (LLM brain)
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
| `EMBED_PROVIDER` | `lm-studio` | Embedding provider: `lm-studio`, `openai-compatible`, or `google` |
| `LM_STUDIO_URL` | `http://localhost:1234/v1` | LM Studio endpoint |
| `OAI_COMPATIBLE_URL` | *(empty)* | OpenAI-compatible embedding URL |
| `OAI_API_KEY` | *(empty)* | API key for OpenAI-compatible provider |
| `GOOGLE_API_KEY` | *(empty)* | Google AI Studio API key (for `google` provider) |
| `EMBED_MODEL` | `Qwen/Qwen3-Embedding-4B` | Embedding model name |
| `EMBED_DIMENSIONS` | `1536` | Embedding dimensions (1536 for Qwen3 MRL, 768 for Google) |
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
| `Qwen/Qwen3-Embedding-4B` | vLLM | 1536 | MRL (primary) |
| `nomic-embed-text` | LM Studio | 768 | Fallback (CPU) — fast local option |
| `text-embedding-004` | Google AI Studio | 768 | Batch+single embed, free tier |
| `text-embedding-005` | Google AI Studio | 768 | Latest Google model |
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
- **Agentic dialectic** — `AgentSystemPrompt` (observer/observed) + tool loop (`search_memory`, `grep`, `get_reasoning_chain`, etc.) for grounded answers

The brain is completely optional and configurable per deployment.

## Reasoning Maturity

- **Reasoning graph:** `ws_<id>_reasoning` stores `conclusion_id → premise_ids + supporting_message_ids`; `GetReasoningChain` BFS traverses, `GetObservationContext` windows `±2` chunks. Enables grounding via BFS traversal.
- **Deriver:** async `internal/deriver` (`Enqueue` on `POST /messages`), `2s/5msg` flush → `Summarize("Extract 1-3 facts")` → `CreateConclusion(ws, peer, line) + AddReasoningEdge`. Async background extraction.
- **Dreamer:** `3h` cron, `QueryConclusions` distance `<0.15` surprisal skip, then `Summarize` → `CreateConclusion{source: dreamer}` (1536d).
- **Agentic chat:** `POST /workspaces/:id/chat` loops up to `maxTools` per `reasoning_level` (`none=0, low=2, medium=4, high=6, max=8`, `temp 0.1→0.9`), parsing `{"tool":"...","args":{...}}` JSON and dispatching to `QueryConclusions/Search/Grep/GetReasoningChain/GetObservationContext` before synthesis.

## End-to-End Workflow — User Prompt → AI → Vectorizer → Response

```
User Prompt
   │
   ▼
AI Agent (Hermes/OpenClaw/OpenCode/Claude) receives prompt
   │ 1. Agent calls Vectorizer FIRST (recall)
   ├─► vectorizer_search / POST /messages/search {query, workspace_id} ─┐
   │   or vectorizer_chat / POST /workspaces/:id/chat {query, observer} │
   │   Vectorizer: embed query (Qwen/Qwen3-Embedding-4B (1536d MRL)) → HNSW cosine   │
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
   │   → chunkText 4000 (word-boundary) → Embed 1536d batch (Qwen3) → Upsert   │
   │   → ws_<workspace_id> collection, metadata {message_id, role, created_at, scope, peer_id}
   │   → 3× retry backoff; 100k char cap, 429 rate limit (10/s)         │
   │                                                                     │
   └─► 5. Async side-effects (no user wait)                             │
       ├─ dreamer cron (3h, 1536d (Qwen3-Embedding-4B MRL)): summarize recent session → ws_<id>_conclusions
       ├─ conclusions/representations build peer view
       ├─ TTL cleanup (TTL_HOURS or DELETE /ttl?before=)
       └─ webhooks Fire("message.created") if registered
            │
            ▼
       Next turn reuses accumulated memory → long-term persona.
```

**Step-by-step:**

1. **User sends prompt** → AI agent intercepts.
2. **Recall:** Agent calls `vectorizer_search` or `vectorizer_chat` (or `POST /messages/search` + `GET /representations`) with `workspace_id` (agent identity), `session_id` (thread), optional `scope`/`hybrid`/`temporal`. Vectorizer embeds query 1536d (Qwen3-Embedding-4B MRL), HNSW cosine search (plus BM25 RRF if requested), merges peer cards/conclusions.
3. **Inject:** Agent prepends returned memories as context to its LLM.
4. **Generate:** LLM produces final answer grounded in memories (with observer/observed perspective if dialectic, `reasoning_level` `n_results` scaling).
5. **Record:** Agent stores both user prompt and its answer via `vectorizer_add_message` / `POST /messages`; Vectorizer chunks, embeds `1536d`, upserts `ws_<id>`, enqueues deriver (`2s/5msg` → conclusions), fires `webhooks`. `POST /workspaces/:id/chat` also **auto-stores** answer as `ws_<id>_conclusions` + `ws_<id>_reasoning` edge + `assistant` message for rolling-window continuity.
6. **Background:** Dreamer (`3h`, rolling-window `8000` tokens via `FitContextWithinTokens`), TTL, webhooks run offline; `GET /sessions/:id/context?tokens=10000` enforces budget via `EstimateTokens`.
7. **Fallback:** If `VECTORIZER_URL` down, agent proceeds without memory (graceful degradation); if `X-API-Key`/JWT missing, `401`; metrics at `/metrics`, health at `/api/v1/health` (public).

This mirrors the `Store → Reason (deriver) → Query (chat/representation) → Inject` loop, single-binary Go/Chroma.

## Integrations

MCP (Claude Desktop, OpenCode, OpenClaw, Hermes via `mcp-remote`):
```json
{"mcpServers":{"vectorizer":{"command":"node","args":["./mcp/dist/index.js"],"env":{"VECTORIZER_URL":"http://localhost:8091","VECTORIZER_API_KEY":"Bearer <jwt>","VECTORIZER_WORKSPACE_ID":"shared-proj"}}}}
```
Tools: `vectorizer_search`, `vectorizer_add_message` (strict `peer_id` must match JWT `p`), `vectorizer_chat` (dialectic), `vectorizer_create_peer`, etc. (see `mcp/README.md:1`).

Skill: `skills/vectorizer/SKILL.md` + `skills/vectorizer-memory/` — recall/record loop (store after turns with `peer_id`, search before answering with `scope`).

SDKs: `sdks/typescript` (`@vectorizer/sdk`) + `sdks/python` (`vectorizer-ai`) — `new Vectorizer({baseUrl, apiKey}).search("...")`.

## 3-Agent Setup (Option C — Recommended)

Shared `shared-proj`, peers `alpha/beta/gamma`, strict JWT (`peer_id` must match `p`), all share LM Studio GGUF `Qwen3-Embedding-4B` `1536d` at `http://host.docker.internal:1234/v1`:

```bash
# 1. Generate JWTs (requires AUTH_JWT_SECRET)
export AUTH_JWT_SECRET=$(openssl rand -hex 32); echo "AUTH_JWT_SECRET=$AUTH_JWT_SECRET" >> .env
go run ./scripts/generate_jwt --workspace shared-proj --admin --expires 30d  # → ADMIN_TOKEN
go run ./scripts/generate_jwt --workspace shared-proj --peer alpha --expires 30d  # → ALPHA_JWT
go run ./scripts/generate_jwt --workspace shared-proj --peer beta --expires 30d   # → BETA_JWT
go run ./scripts/generate_jwt --workspace shared-proj --peer gamma --expires 30d  # → GAMMA_JWT

# 2. Provision workspace/peers/scopes/sessions (max coverage)
VECTORIZER_URL=http://localhost:8091 ADMIN_TOKEN=$ADMIN_TOKEN ./scripts/provision_option_c.sh
# Creates: scopes proj-frontend, proj-backend, proj-research, shared-all, private-alpha/beta/gamma
# Sessions: pair-alpha-beta, pair-beta-gamma, pair-alpha-gamma, pair-all, sess-*-private

# 3. Per-agent MCP (repeat for each agent, change JWT + peer_id handling)
# Agent alpha: mcp env VECTORIZER_API_KEY=Bearer $ALPHA_JWT, calls always with peer_id=alpha
# Agent beta:  Bearer $BETA_JWT, peer_id=beta — rejected if mismatched (403)
# Agent gamma: Bearer $GAMMA_JWT, peer_id=gamma

# 4. Docker (LM Studio GGUF on host)
docker compose up -d  # vectorizer + chromadb; qwen-embed profile "gpu" ignored (LM Studio mode)
# Verify LM Studio loads Qwen3-Embedding-4B-GGUF and returns 1536d:
curl -X POST http://host.docker.internal:1234/v1/embeddings -H "Content-Type: application/json" \
  -d '{"model":"Qwen/Qwen3-Embedding-4B-GGUF","input":["test"],"dimensions":1536}' | jq '.data[0].embedding | length' # → 1536
```

Scopes coverage suggestion (already in provision script): `proj-frontend`, `proj-backend`, `proj-research` (cross-agent pairs), `shared-all` (all pairs), `private-*` per agent. Search with `where.scope="proj-frontend"` to isolate.

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
│   ├── embedding/    # Embedding providers (Embedder interface, Google AI Studio, OpenAI-compatible)
│   │   ├── interface.go  # Embedder interface definition
│   │   ├── google.go     # Google AI Studio provider (batch+single, text-embedding-004)
│   │   └── service.go    # OpenAI-compatible provider (LM Studio, vLLM, etc.)
│   ├── llmbrain/     # LLM brain + prompts.go (AgentSystemPrompt)
│   ├── deriver/      # Async deriver
│   ├── dreamer/      # Surprisal dreamer (3h, 1536d)
│   ├── handlers/     # Workspaces, messages, peers, chat (agentic), scopes, conclusions, keys, webhooks
│   ├── models/       # Workspace, Session, Message, Peer, PeerCard, SearchRequest
│   ├── store/        # Store + reasoning.go, scopes.go, keys.go, conclusions.go, peers.go
│   ├── grpc/         # gRPC server (VectorizerService)
│   ├── security/     # JWT w/p/ad
│   └── webhooks/     # Webhook manager
├── proto/            # vectorizer.proto (gRPC)
├── vectorizerpb/     # Generated protobuf
├── mcp/              # MCP proxy (13 tools)
├── skills/           # Skills (vectorizer, memory, integration)
├── sdks/             # TS + Python SDKs
├── evals/            # Eval harness (LongMemEval, reasoning-grounded)
├── main.go           # Entry point, wiring, auth, rate limit, gRPC
├── Dockerfile        # Container build (Go 1.25, exposes 8091+50051)
├── docker-compose.yml# Full stack (Vectorizer + ChromaDB + qwen-embed vLLM)
├── Makefile          # Build/run commands
└── .env.example      # Configuration template
```

## License

MIT
