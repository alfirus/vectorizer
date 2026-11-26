# ADR 0001: Go service + ChromaDB + vault mirror (initial architecture)

**Status:** Accepted
**Date:** 2026-11-26
**Authors:** Lina (Tech Lead — reconstructed; the original decision was never
recorded, numbering started at 0002)
**Accepted by:** Maisarah (Engineering Manager), 2026-11-26 — P1 batch review (kanban t_8304a3c1)

---

## Context

`docs/adr/` numbering starts at 0002 with no 0001 on file, and the
CONTRIBUTING merge gate requires an ADR for technology-stack choices. This
record backfills the founding decision from shipped code reality (Go service
at `main.go`, `internal/chromadb/` client, `scripts/vault_index.py`, LM Studio
compatible LLM calls) so the sequence is complete and the original trade-offs
are explicit.

At build time the need was: semantic memory over per-agent markdown vaults,
searchable by meaning, writable at chat speed, runnable on one host next to
ChromaDB and an LM Studio-compatible LLM endpoint.

## Decision

Build vectorizer as a single Go HTTP service (later gRPC alongside) with:

- ChromaDB as the sole vector/metadata store (detailed in ADR 0002);
- Nomic 768-d embeddings as the canonical dimension (1536-d opt-in came later);
- the markdown vault as the human-readable mirror: every stored turn optionally
  mirrored to per-session staging files (`VAULT_WRITEBACK`), with
  `scripts/vault_index.py` / `graph_build.py` as the bulk on-ramp;
- an LM Studio / OpenAI-compatible chat-completions endpoint for summarization
  (deriver/dreamer) and brain fan-out — never a vendored model.

## Consequences

- **Positive:** one binary, one vector store, one model endpoint — the whole
  stack runs on a single host; vault files stay the durable human-readable copy.
- **Negative:** single-host ceiling on every axis (compute, storage, availability);
  no model fallback when the LLM endpoint is cold.
- **Assumptions:** 768-d recall stays sufficient; single-host ops remain acceptable
  (off-host replication is an open follow-up, card t_87d57b8f).

---

## References

- `docs/BLUEPRINT.md` (original design doc)
- ADR 0002 (ChromaDB over pgvector), ADR 0004 (file graph over Postgres)
- Kanban t_8304a3c1 (P1 ADR catch-up, R2 V7)
