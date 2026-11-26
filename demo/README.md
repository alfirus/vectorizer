# Vectorizer demo — from zero to hybrid search in ~5 minutes

Self-contained stack (ChromaDB + Vectorizer) with three synthetic sample
notes (fictional Acme manufacturing data) under
[`vault/acme/vault/`](vault/acme/vault/). Nothing phones home; ports are
loopback-only.

## Prerequisites

- Docker (compose v2) + `curl` + `jq` (optional) + Python 3.9+ (stdlib only)
- **For the full loop:** an OpenAI-compatible embedding endpoint with model
  `text-embedding-nomic-embed-text-v2` — LM Studio defaults work
  (`http://host.docker.internal:1234/v1`). Without one, steps 1–2 and the
  offline preview still run; storing/searching waits for embeddings
  (the server refuses writes with a 503 + retry hint instead of hanging).

## Run it

From the repo root:

```bash
docker compose -f demo/docker-compose.demo.yml up -d --build
./demo/seed.sh
```

`seed.sh` does four things:

1. Waits for `GET /api/v1/health` (reports chroma + embedding status).
2. Creates workspace `acme` (idempotent — re-runs are safe).
3. Stores three sample memories via `POST /messages/batch`
   (skipped with a pointer if no embedding endpoint is up).
4. Runs a hybrid search (`vector + BM25`, honest `source` per hit).

Offline chunking preview (no server needed — shows what the indexer sees):

```bash
VAULT_ROOT=demo/vault python3 scripts/vault_index.py --workspace acme --dry-run
# would index: acme/vault/line3-upgrade-decision.md (4 chunks)
# would index: acme/vault/press-brake-blades.md (4 chunks)
# would index: acme/vault/safety-checklist.md (4 chunks)
```

To index for real (embedding endpoint up):

```bash
VAULT_ROOT=demo/vault VECTORIZER_URL=http://127.0.0.1:8091 \
  VECTORIZER_API_KEY=demo-key-change-me \
  python3 scripts/vault_index.py --workspace acme
```

## Tear down

```bash
docker compose -f demo/docker-compose.demo.yml down -v
```

(`-v` drops the demo Chroma volume. The sample vault stays — re-running
`seed.sh` against a fresh stack reproduces everything.)

## Same calls, explained

Each step mirrors [`README.md` § API in Action](../README.md#api-in-action)
against workspace `acme`, so the README doubles as the demo script
reference. Demo key is `demo-key-change-me` (loopback only, change it for
anything beyond your laptop).
