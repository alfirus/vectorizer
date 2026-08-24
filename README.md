# Vectorizer — Semantic Memory Server

A lightweight, self-hosted memory server for AI agents. Stores messages as embeddings in ChromaDB with optional LLM-powered summarization and Q&A. Each agent gets isolated memory via workspace namespaces.

## Documentation

- **[Architecture Blueprint](docs/BLUEPRINT.md)** — full system design, data flows, API contract, config schema, deployment topology
- **This README** — quick start guide and usage reference

## Features

- **Workspace isolation** — each agent has its own namespace (ChromaDB collection)
- **Semantic search** — find relevant memories by meaning, not keywords
- **Optional LLM brain** — enable/disable per deployment for summarization & Q&A
- **Multi-provider embeddings** — LM Studio (local), OpenAI-compatible APIs
- **API key auth** — protect your endpoint with a secret key
- **Docker-ready** — one `docker compose up` to run everything

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
