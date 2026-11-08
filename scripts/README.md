# scripts/ — vault indexer + migration tooling

## Vault indexer (Quick Start A / Vault workflow)

- `vault_index.py` — markdown-aware chunker (H1/H2/H3 sections, 3600-4800
  chars + 200 overlap, never inside ``` fences) → `POST /messages/batch`
  (batches of 10). Hash-dedupe registry: `<vault>/00-index/MEMORY_INDEX.json`.
- `graph_build.py` — rebuilds `<vault>/00-index/GRAPH.json`
  (`doc/chunk/entity/folder` nodes; `belongs_to/next_chunk/mentions/links_to`
  edges) from the same chunker, so chunk IDs line up with ChromaDB vectors.

Both are stdlib-only (Python 3.9+) and take `--workspace`, `--vault-root`
(`VAULT_ROOT`, default `/data/ai`), `--url` (`VECTORIZER_URL`), `--api-key`
(`VECTORIZER_API_KEY`), plus `--dry-run` / `--reindex` (indexer only).

Note on paths: `vault/00-index/` is the **on-disk index directory inside your
vault data** (`<VAULT_ROOT>/<workspace>/vault/00-index/` — `MEMORY_INDEX.json`,
`GRAPH.json`), not a directory in this repo. The scripts that maintain it live
here in `scripts/`.

## Other tooling

- `migrate-768-to-1536.sh`, `migrate_workspaces.py`, `provision_option_c.sh`
- `generate_jwt/` — JWT minting helper
- `exports/` — sample workspace export fixtures

Backup (`vectorizer_backup.py`) and healthcheck (`vectorizer_healthcheck.py`)
live at the **repo root**, not in `scripts/`.
