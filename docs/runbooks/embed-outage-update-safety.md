# Runbook — embed outage + message-update safety (PUT / splice)

Owner: Aisyah (DevOps) · added 2026-09-28 (card t_5df7921d) · incident: t_0927b244 (2026-09-27)
Queue + create-signal: Lisa (Backend) · 2026-10-03 (card t_7c865ae4)

## The foot-gun this runbook exists for

Until 2026-09-28 `PUT /api/v1/messages/:id` (full replace **and** the `sections` splice) was
**delete-first**: `Store.UpdateMessage` called `DeleteMessage` for every chunk of the message
*before* `AddMessage`, and `AddMessage` only then asked the embedder for vectors. With the embed
path degraded (LM Studio down / model churn), the delete succeeded and the re-embed failed with
HTTP 500 — the record's chunks were gone and the replacement text was never stored.

Observed on 2026-09-27: 3 records destroyed mid-scrub (`balqis` 7 → 6, `maisarah` 869 → 867).
The update is **not** atomic across an embed failure; nothing in the API told the caller why.

## Current behaviour (t_5df7921d guard + t_7c865ae4 queue, image `822a7183e809`)

1. **Deferred-update inbox:** `PUT /api/v1/messages/:id` while the background embed probe
   reports anything other than `ok` (`degraded` / `unknown`) is accepted as
   **202 Accepted** `{"queued": true, "id": ..., "workspace_id": ..., "pending": N,
   "embedding": ..., "hint": "replays automatically when embedding ok"}`.
   Nothing is mutated yet — the intent (record id, workspace, content XOR sections,
   session/role/metadata, requester, timestamp) sits in the process-local pending
   inbox (`internal/store/pending.go`). Same-id re-PUTs coalesce (last-writer-wins).
   The response `id` **is** the queue key — `GET /messages/pending` returns it as
   `message_id`, so there is no separate `pending_id` field to look for.
   The 409 refuse-and-hint posture survives ONLY as the fallback when the inbox
   itself is unusable (full with nothing evictable) — never a silent drop.
2. **Automatic replay:** when the probe flips back to `ok`, the queued entries drain
   through the same write-first `UpdateMessage` / `UpdateMessageSections` paths.
   Mid-replay embed failures re-queue (attempts+1, parked as `failed` with
   `last_error` after 5 attempts) — they never destroy the underlying record.
   The drain is single-flight, and the slot is released on **every** pass,
   including the empty-inbox ones (2026-10-05 fix: an empty pass used to leak
   the slot, so the boot-time `ok` probe wedged replay permanently — queued
   entries sat `pending` forever while the ticker logged
   `embed_status=ok pending_count=1` with no `pending replay` line. Regression
   test: `TestDrainReleasesSingleFlightOnEmptyInbox`, which fails on the
   pre-fix code).
3. **Inbox visibility:** `GET /api/v1/messages/pending?workspace_id=...` lists entries
   with `status` (`pending` / `applied` / `failed`), `attempts`, `last_error`.
   Applied/failed entries are retained 1h for confirmation, then pruned.
   `/api/v1/metrics` carries `vectorizer_pending_updates` (gauge) and
   `vectorizer_pending_drops_total` (evictions by the 500-entry bound).
   The lister is telemetry-exempt (no usage-log noise) and spends no embeddings.
4. **Create-path degraded signal:** `POST /api/v1/messages` (and `/batch`) while the
   probe != `ok` fails fast with **503** `{"error": "embedding path not healthy ...",
   "embedding": ..., "stored": false, "hint": "retry this exact body ..."}` —
   no more hanging through `EMBED_TIMEOUT_SECS` x retries and storing nothing.
   Creates are deliberately NOT queued (the server mints the id; queue+replay
   would fork identities). `EMBED_TIMEOUT_SECS` semantics for the healthy path
   are unchanged.
5. **Write-first ordering (unchanged):** `Store.UpdateMessage` embeds+writes the new
   chunks first (via `AddMessage`) and only afterwards prunes stale tail chunk ids
   when the content shrank. An update that stores nothing (content-dedup skip) is
   an error rather than a false success.
6. Tests: `internal/handlers/update_guard_test.go` (`TestUpdateQueuesWhileDegraded`,
   `TestCreateRefusesFastWhileDegraded`, `TestPendingInboxCoalesceAndDrain`; the
   pre-queue `TestUpdateRefusesWhileEmbeddingDegraded` is retired/skipped),
   runs in `go test ./internal/...`.

Known limits: the inbox is process-local — a container recreate during a degraded
window loses queued entries (callers keep the id and retry; the 202 body says so).
**RECREATE IS THE DRAIN KILLER** (verified live 2026-10-03): the queue lives and
dies with the process that accepted it, and a recreate also starts a fresh probe
at `unknown` — so during a real outage, prefer waiting for probe recovery (the
drain fires on the degraded→ok flip plus a 30s reconciler tick) over recreating,
and re-PUT anything queued before a restart.
Bounded at 500 entries (oldest-pending evicted, counted on /metrics). Fail-fast
time-to-error on the store path is ~26s (`EMBED_TIMEOUT_SECS=12` x2 + backoff,
t_382c7f47); the 503 guard fires before any of that is spent.

## Operator procedure during an embed outage

1. Confirm: `ssh -i ~/.ssh/personal ubuntu@139.99.131.127 'curl -s http://127.0.0.1:8091/api/v1/health'`
   — REST 8091 is loopback-only on the server (t_02943c82); there is no tailnet path and sshd
   direct-tcpip forwarding is disabled, so check health **on the server**. `embedding: degraded`
   plus `embedding_last_ok` older than ~1 min = real outage.
2. Check the embedder itself (LM Studio on the tailnet host, port 1234): it needs a bearer token;
   a bare `401` means *alive*, `000` means *down*. Model-load churn on the single LM Studio slot
   is the usual root cause (see t_f58e4a48 / t_5df64e17).
3. **Writes are safe to leave running.** Splices/updates return 202 and queue automatically;
   check the backlog with `GET /api/v1/messages/pending` (or
   `curl -s http://127.0.0.1:8091/api/v1/messages/pending` on the server).
   Creates return 503 — tell callers to retry the exact body later. Never "retry until it
   works" against a pre-2026-09-28 build — that one deleted on every attempt.
4. Searches during an outage: hang or time out at 180s. Treat as unavailable, verify with
   a list call.
5. When health is `ok` again: the queue drains itself. Confirm with
   `GET /api/v1/messages/pending` (expect `applied`), then read each record back
   (`GET /api/v1/messages/:id?workspace_id=...`) before moving on. Entries parked
   as `failed` (5 attempts) need a human re-PUT.
6. If records were already destroyed before the fix, recovery needs the pre-loss text + the
   original `session_id`/`role` (`PUT` requires them when no chunks survive, otherwise 400
   `session_id is required`). Working example: `/home/ubuntu/scripts/restore/replay_restore_v2.py`
   and the incident kit in kanban card t_0927b244's workspace.

## What callers see (cheat sheet)

| state | POST /messages | PUT /messages/:id | POST /messages/search |
|---|---|---|---|
| `embedding: ok` | 201, stored | 200 `updated:true` | 200 with hits |
| `embedding: degraded` (pre-2026-09-28) | hang → nothing stored | **200-or-500 after deleting the record** | hang/timeout |
| `embedding: degraded` (2026-09-28 – 2026-10-03) | hang → nothing stored (~26s since t_382c7f47) | **409, record untouched** | hang/timeout |
| `embedding: degraded` (post-t_7c865ae4) | **503 `stored:false`, instant** | **202 `queued:true`, auto-replay on recovery** | hang/timeout |

## Root cause found 2026-10-05: the queued `message_id` mutated after the 202

The queue mechanics were fine; the *stored id* was not. Fiber v2.52 returns
unsafe strings from `c.Params()` / `c.Get()` / `c.Query()` that alias the pooled
ctx path and header buffers — `Immutable` defaults to false, and Fiber's own
docs on those getters say *"Returned value is only valid within the handler. Do
not store any references."* The handler stored `c.Params("id")` straight into
the inbox, so the entry kept pointing at bytes the next request overwrites.

Observed live (`t7c_idprobe2.sh`, image `012c6c623dd8`): the 202 answered
`id=deadbeef-1111-4000-8000-000000000009` while `GET /messages/pending` reported
that same entry as `message_id=pending2-2222-4000-8000-000000000002`. That value
is byte-exact for a reused buffer: a second PUT wrote `abcdef12-2222-…` over the
same path offset, then `GET /messages/pending` (24-byte path) wrote `pending`
over the first 7 bytes and left `2-2222-…` behind. Both queued entries came back
with that one id and the replay logged `message not found`.

Fix, at two layers (image `822a7183e809`, built 2026-10-05):

- `internal/store/pending.go` — `EnqueuePendingUpdate` now deep-copies every
  string the entry outlives the request with (`clonePendingUpdate`), so the
  inbox owns its keys no matter what a caller passes. Covered by
  `TestEnqueuePendingUpdateOwnsStrings` (red without the clone, green with it).
- `main.go` — `fiber.Config{Immutable: true}`, the remedy Fiber documents for
  this class of bug; it also protects every other handler that reads params,
  query or headers.

Rule for future work here: never keep a `c.Params()` / `c.Get()` / `c.Query()`
result in a struct, map or queue past the request without copying it.

## Deploying a fix to this service

- Image builds from `/opt/vectorizer` via `./up.sh build vectorizer`
  (pins `docker-compose.server.yml` + live-only `docker-compose.override.yml`).
- Go toolchain is not on the host: run `go vet` / `go test` in the build image, read-only module
  mode (`GOFLAGS=-mod=readonly`) — plain `go test -mod=mod` rewrites `go.mod`, and repo-wide
  `gofmt -w` reformats ~30 unrelated files. Format only files you added.
- Recreate (not restart) to pick up `.env` changes: `./up.sh recreate vectorizer`.
  NEVER use a bare `docker compose -f …` recreate — it drops the untracked
  live-only override (2026-10-03 fleet outage).
- Verify after deploy: `/health` embedding ok, `embedding_dim` 768, per-workspace
  `GET /api/v1/workspaces/<ws>/health` counts unchanged, `GET /api/v1/messages/pending`
  reachable (expect `count: 0` on a fresh deploy), plus a create→shrink→grow→delete round
  trip in a scratch workspace.
