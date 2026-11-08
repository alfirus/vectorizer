# ADR 0002: ChromaDB over pgvector

**Status:** Accepted
**Date:** 2026-11-08
**Authors:** Lina (Tech Lead, retroactive record of shipped decision)
**Accepted by:** Maisarah (Engineering Manager), 2026-11-08 — approved as-is during the P0/P1 publish review (t_0335f460 / t_e6be7d6c)

---

## Context

Vectorizer needs a vector store for semantic memory over markdown-vault
chunks. The two candidates at build time were ChromaDB (standalone vector
database, HNSW built in, REST API) and pgvector (Postgres extension — vectors
alongside relational data). The service already had no relational needs: all
state is append-only memory (messages, conclusions, peer cards) keyed by
workspace, with no joins, no transactions, and no existing Postgres in the
stack. See `docs/BLUEPRINT.md` §8 ("Why ChromaDB over pgvector?").

## Decision

Use ChromaDB as the sole store, via a thin `internal/chromadb/` client over
the ChromaDB v2 REST API. Per-workspace collections (`ws_<id>`,
`ws_<id>_conclusions`, `ws_<id>_dream`, `ws_<id>_peers`, `ws_<id>_peer_cards`,
`ws_<id>_scopes`, `ws_<id>_reasoning`) provide hard agent isolation. No
Postgres/pgvector anywhere in the stack.

## Consequences

- **Positive:** single-container dependency, no schema/migration management;
  HNSW index configured at collection creation; cosine distance by default
  (correct for text embeddings); language-agnostic REST surface reused by the
  Go service, the MCP proxy, and the dashboard.
- **Negative:** single-node ChromaDB is a scale and availability ceiling —
  no replication story, and a volume misconfiguration wipes vectors on
  recreate (see audit R4). Collection-per-workspace adds overhead past
  ~100 agents (sharding or a single collection with `workspace_id` metadata
  filtering would be needed).
- **Assumptions:** recall quality from Chroma HNSW + hybrid BM25/RRF fusion in
  `internal/store` stays sufficient; if multi-replica or transactional needs
  appear, this decision must be revisited (superseding ADR).

---

## References

- `docs/BLUEPRINT.md` §8 — "Why ChromaDB over pgvector?"
- Audit 2026-11-08, risks R4 (single-node ChromaDB) and P2-4 (multi-replica)
