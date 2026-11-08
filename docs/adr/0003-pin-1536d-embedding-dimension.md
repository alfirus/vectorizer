# ADR 0003: Pin 1536d (Qwen3-Embedding-4B MRL) as the canonical embedding dimension

**Status:** SUPERSEDED by P0-1 (2026-11-08, publish) — DO NOT IMPLEMENT AS WRITTEN
**Date:** 2026-11-08
**Authors:** Lina (Tech Lead, retroactive record of shipped decision)

---

## Supersession notice

This ADR's decision (1536d Qwen as canonical) was **rejected at publish time**
in favor of the P0-1 reconciliation: **768d Nomic
(`text-embedding-nomic-embed-text-v2`) is canonical; 1536d Qwen
(`Qwen/Qwen3-Embedding-4B` MRL) is opt-in** for GPU deployments. The original
proposal is preserved below for history. A deployment must still pick ONE dim
— mixing dims in one collection is unsupported. Canonical statement now lives
in `docs/BLUEPRINT.md` (v0.4.0), `config/config.go`, `.env.example`, and
`config.toml.example`.

---

## Context

The stack has hosted two embedding regimes: 768d Nomic
(`text-embedding-nomic-embed-text-v2`, local primary in early docs) and 1536d
`Qwen/Qwen3-Embedding-4B` via MRL (vLLM `qwen-embed` profile, GPU), with
Google AI Studio `text-embedding-004/005` (768d) as a hosted fallback. A
`scripts/migrate-768-to-1536.sh` migration exists, and the audit found live
collections potentially in both dims — mixed-dim collections silently degrade
recall (cosine across different spaces is meaningless). README still presents
768d Nomic as primary while `config.toml.example`, `.env.example`, and
BLUEPRINT §§3/6 pin 1536d (audit M2). A single canonical dimension is needed.

## Decision

Pin **1536d (`Qwen/Qwen3-Embedding-4B` MRL)** as the canonical dimension: the
same dim for `ws_<id>` and every derived collection (`_conclusions`, `_dream`,
`_peers`, ...), enforced by `EMBED_DIMENSIONS=1536` defaults and the
`dummyVector()` dim-consistency path in `internal/store`. 768d Nomic (local)
and Google 768d remain supported fallbacks for non-GPU deployments, but a
deployment must pick ONE dim — mixing dims in one collection is unsupported.
The README-vs-config presentation contradiction (M2) is left to the P0 docs
reconciliation to word correctly; this ADR fixes the engineering choice.

## Consequences

- **Positive:** one recall space across messages, conclusions, dreams, and
  peers; MRL keeps 1536d quality with a matryoshka path to 2560d; GPU batch
  embed keeps ingestion fast.
- **Negative:** GPU (or a 1536d-capable endpoint) becomes the default
  requirement; CPU-only / offline deployments fall back to 768d and must
  re-index from scratch — there is no safe in-place dim conversion, and no
  dimension-consistency check guards against mixed collections yet (audit R6).
- **Assumptions:** Qwen3-Embedding-4B stays available via vLLM or an
  OpenAI-compatible endpoint; if the primary embedder changes dims, a full
  re-index + migration ADR is required.

---

## References

- `docs/BLUEPRINT.md` §§3/6 (1536d pinned), `scripts/migrate-768-to-1536.sh`
- Audit 2026-11-08, M2 (dim contradiction) and R6 (migration debt)
