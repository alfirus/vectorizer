# ADR 0004: File-graph (vault markdown + GRAPH.json) over Postgres

**Status:** Accepted
**Date:** 2026-11-08
**Authors:** Lina (Tech Lead, retroactive record of shipped decision)
**Accepted by:** Maisarah (Engineering Manager), 2026-11-08 — approved as-is during the P0/P1 publish review (t_0335f460 / t_e6be7d6c)

---

## Context

Agent memory needs lineage ("why was this recalled?") over a markdown vault:
documents, chunks, entities, and folders with typed relations. The relational
option was a Postgres schema (docs/chunks/entities tables + join tables for
edges). The alternative actually shipped is the file-graph: the vault itself
is the source of truth, `scripts/vault_index.py` chunks it, and
`scripts/graph_build.py` derives `vault/00-index/GRAPH.json` (`doc` / `chunk`
/ `entity` / `folder` nodes; `belongs_to` / `next_chunk` / `mentions` /
`links_to` edges) traversed with in-memory BFS at query time
(`WorkflowRerankScore`: vector top-k → 1-hop graph expansion → rerank).
See `docs/BLUEPRINT.md` §8.

## Decision

Keep the file-graph as the lineage layer: markdown files on disk,
`MEMORY_INDEX.json` hash-dedupe registry, `GRAPH.json` rebuilt from the vault
by `scripts/graph_build.py` (same chunker as the indexer, so chunk IDs line
up with ChromaDB vectors). No Postgres for graph or memory state; the Go
service holds no relational schema.

## Consequences

- **Positive:** vault is human-readable, git-diffable, and portable — memory
  survives the vector store (rebuild vectors from markdown, not vice versa);
  zero relational ops; graph rebuild is a pure function of the vault.
- **Negative:** in-memory BFS is bounded by single-process RAM; no concurrent
  writers, no transactional updates — concurrent `graph_build.py` runs can
  clobber `GRAPH.json`; large-vault rebuilds are O(files) with no incremental
  graph patching.
- **Assumptions:** vault fits on one operator's disk and in one process's
  memory; if multi-writer or >1M-chunk scale arrives, the graph layer needs a
  real store (superseding ADR), not a bigger JSON file.

---

## References

- `docs/BLUEPRINT.md` §8, `scripts/vault_index.py`, `scripts/graph_build.py`
- Audit 2026-11-08, M4/M6 (indexer now vendored; diagram fixed)
