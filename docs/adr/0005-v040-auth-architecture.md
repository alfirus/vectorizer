# ADR 0005: v0.4.0 fail-closed auth + gRPC parity + per-route JWT scoping (retro)

**Status:** Accepted
**Date:** 2026-11-26
**Authors:** Lina (Tech Lead — retroactive record of the shipped v0.4.0 change)
**Accepted by:** Maisarah (Engineering Manager), 2026-11-26 — P1 batch review (kanban t_8304a3c1)

---

## Context

Before v0.4.0 the vectorizer HTTP API was effectively open-by-default, the
gRPC server had no auth story, and JWT workspace scoping was unenforced on
several routes (R1 audit F1/F2/F3/F12). The P0 program closed these together
in the v0.4.0 publish (`docs/releases/v0.4.0.md`) without recording the
architecture change — exactly the "authentication / authorization model" and
"security architecture change" triggers in the CONTRIBUTING merge gate. This
ADR backfills that record.

## Decision

- **Fail-closed startup:** without an explicit opt-in (`VECTORIZER_ALLOW_OPEN`)
  the service refuses to serve unauthenticated traffic; API-key or HMAC-JWT is
  required on every non-telemetry route.
- **gRPC parity + loopback:** the gRPC server enforces the same auth via an
  interceptor and binds loopback-only (fail log on listen failure) — no
  second, weaker front door.
- **Per-route JWT scoping:** workspace-scoped claims are enforced per handler
  (`requireWorkspace`); cross-workspace search-all fusion stays scoped to the
  caller's workspaces; minted API keys that nothing verifies were deleted
  rather than fixed (the `/keys` endpoints are gone — unverifiable issuance is
  worse than no issuance).
- **Attribution as a contract:** `X-Agent` identifies the caller on
  usage-tracked calls (`REQUIRE_X_AGENT=true` drops the channel fallback);
  telemetry (`/health`, `/metrics`, usage reads) stays public by design.

## Consequences

- **Positive:** one auth mental model across HTTP + gRPC; JWT holders cannot
  escalate across workspaces; fleet callers are attributable in Daily Usage.
- **Negative:** every caller (agents, bridges, dashboard) must carry identity —
  the dashboard needed its own P1 fix round (X-Agent stamping) to comply;
  local curl debugging needs a key.
- **Assumptions:** the HMAC secret (`AUTH_JWT_SECRET`) and API key stay
  server-side secrets with rotation on leak (see dashboard C9 rotation, card
  t_c66687de); telemetry staying public remains acceptable.

---

## References

- `docs/releases/v0.4.0.md`; `internal/security/`, `internal/handlers/authz.go`
- R1 audit F1/F2/F3/F12 (fixed), R2 V1/V2/V3 (residuals, fixed in `ae07294`)
- Dashboard contract: `vectorizer-dashboard` ADR 0001
- Kanban t_8304a3c1 (P1 ADR catch-up, R2 V7)
