# Vectorizer prod → LM Studio brain path (ns539881 → alfirusdesktop)

Status: canonical since 2026-09-29 00:5x MYT (kanban `t_8da84258`, owner aisyah).
Read this before touching `LM_STUDIO_URL`, `OAI_COMPATIBLE_URL`, `LLM_MODEL`, the
LM Studio bind on alfirusdesktop, or any `tailscale serve` line.

## The path

```
ns539881  /opt/vectorizer (Go, live deploys ONLY via ./up.sh — see up.sh header)
            LM_STUDIO_URL      = http://100.121.188.113:1234/v1   (LLM brain)
            OAI_COMPATIBLE_URL = http://100.121.188.113:1234/v1   (embeddings —
                                  config.Config.EmbedBaseURL(), i.e. THIS forward
                                  also carries every embed call, not just chat)
                  │  100.121.188.113 = Tailscale IP of alfirusdesktop (tailnet only)
                  ▼
alfirusdesktop   tailscale serve forward (the canonical fix, see Decision 1)
                 tailscale serve --bg --tcp=1234 tcp://127.0.0.1:1234
                 listeners: 100.121.188.113:1234 + [fd7a:…]:1234, tailnet only
                  ▼
                 127.0.0.1:1234  LM Studio (loopback bind)
```

## Decision 1 — reachability: KEEP the `tailscale serve` forward, never a `0.0.0.0:1234` bind

Exactly one mechanism must be true at a time; the one that is true is the forward.
Do not "fix" reachability by binding LM Studio to `0.0.0.0:1234` — if you ever do,
you must also run `tailscale serve --tcp=1234 off` on alfirusdesktop, and expect the
bind to be silently reverted by the next LM Studio restart.

Why the forward wins (measured, not assumed):

- LM Studio's bind reverts to `127.0.0.1:1234` on every server restart — observed
  2026-09-27 01:41 MYT (lms-cli restart) and 2026-09-27 04:25 MYT (D8 restart).
- Symptom when the bind reverts and the forward is absent: from ns539881,
  `curl http://100.121.188.113:1234/...` hangs 30 s (no RST);
  `POST /api/v1/brain/summarize` → `{"error":"failed to summarize"}` after ~96 s;
  `GET /api/v1/health` → `embedding: degraded (embed call exceeded 8s)`.
  Both are one root cause (reachability), not a code defect.
- The forward is independent of that bind, is tailnet-only (narrower than the old
  LAN-wide `0.0.0.0:1234`), and is re-applied automatically by the watchdog below.
- Remove it only with an explicit decision recorded here:
  `tailscale serve --tcp=1234 off` (run on alfirusdesktop).

## Decision 2 — model residency: prod's model stays resident, PINNED (no TTL)

- Prod's model = `LLM_MODEL` in `/opt/vectorizer/.env` **and** in
  `docker-compose.server.yml` (both must match — the compose value wins for the
  container). Container log line confirms what is actually wired:
  `LLM Brain: ENABLED (lm-studio, model: …)`.
- **LM Studio does not JIT-load.** A request that names an unloaded model logs
  `[ERROR][<model>] Model is unloaded.` and the API returns
  `{"error":"Model is unloaded."}` in ~14 s. There is no auto-load on demand, so a
  resident model is the only thing standing between prod and a failed summarize.
- **`jitModelTTL` is enabled (3600 s)** in `~/.lmstudio/settings.json` on
  alfirusdesktop: anything loaded *with* `--ttl` (or by an auto/TTL load) idle-evicts
  1 h after its last use — `lms ps --json` shows `ttlMs: 3600000`.
- **Pinned = loaded explicitly with NO `--ttl`** → `lms ps --json` shows
  `ttlMs: null` → never idle-evicts (the embedding model is `ttlMs: null` too).
- Re-pin manually (on alfirusdesktop, only while the model is idle — an unload
  kills an in-flight generation):

  ```bash
  lms unload <model>
  lms load <model> -c 128000 --parallel 2 --identifier <model> -y   # ~26 s, no --ttl
  ```

  `lms load` on an already-loaded identifier just errors
  `A model with identifier … already exists` — you must unload first.

## Self-heal watchdog (alfirusdesktop)

- Hermes cron job `5195ec0a5a8a`, every 5 min, `no_agent` (script only):
  `C:\Users\alfir\AppData\Local\hermes\profiles\aisyah\scripts\vectorizer-brain-watchdog.sh`
- Log / state: `C:\Users\alfir\.hermes-ops\vectorizer-brain-watchdog.log` and `.state`
- It re-applies the forward, starts the LM Studio server when `lms server status`
  reports it is not running (an empty `lms ps` while the server *is* up instead
  triggers a direct load attempt — a duplicate load is refused server-side with
  `already exists`, so it cannot double-load), reloads the prod model pinned when it
  is not resident (deferring while another model is mid-generation), and re-pins if a
  TTL creeps back in (only while idle).
- Silent when healthy; prints exactly one line per state change (fix / anomaly /
  recovery) and delivers it to the connected gateway channels.

## Verify (from ns539881)

```bash
curl -s http://127.0.0.1:8091/api/v1/health        # -> "status":"ok", "embedding":"ok"

K=$(grep -m1 '^DEFAULT_API_KEY=' /opt/vectorizer/.env | cut -d= -f2- | tr -d '\r')
curl -s -X POST http://127.0.0.1:8091/api/v1/brain/summarize \
  -H 'Content-Type: application/json' -H "X-API-Key: $K" \
  -d '{"text":"summarize this","max_chars":400}'   # -> {"summary":"…"} non-empty

# reachability of the brain path itself (401 without a token == reachable, not a hang)
curl -s -m 5 -o /dev/null -w '%{http_code}\n' http://100.121.188.113:1234/v1/models
```

`/api/v1/brain/summarize` requires `text` (or `workspace_id`/`session_id`) plus the
`X-API-Key` header; a bare `{"max_chars":400}` body is rejected with
`{"error":"text, or workspace_id/session_id is required"}`.

## Symptom → cause → fix

| Symptom | Cause | Fix |
|---|---|---|
| summarize fails after ~96 s, health `embedding: degraded` | forward gone / LM Studio unreachable from the tailnet | watchdog re-applies it; or run the `tailscale serve` line above |
| `{"error":"Model is unloaded."}` fast (~14 s) | prod model not resident | watchdog reloads it pinned; or manual re-pin |
| `{"error":"missing API key"}` from :8091 | `X-API-Key` header missing | send `DEFAULT_API_KEY` (value never pasted anywhere) |
| load fails with `Engine protocol startup was aborted` | not enough free memory — another model still resident | unload the other model first (or wait for idle), then load |

## Known drift — model choice itself is NOT settled by this runbook

`LLM_MODEL` was `qwen3.8-27b@q3_k_xl` when card `t_8da84258` was written. The
`.env` was edited 2026-09-27 15:37 MYT and both `.env` and `docker-compose.server.yml`
now say `qwen3.6-35b-a3b-uncensored-hauhaucs-aggressive`; the qwen3.8 weights are no
longer on alfirusdesktop (`lms ls` → 5 models, none qwen3.8 — only stale default-config
JSONs under `~/.lmstudio/.internal/user-concrete-model-default-config/`). This runbook
makes the **current** model durable; whether prod should be repointed (and the weights
re-downloaded) is a separate product/model decision, tracked outside this file.

Unchanged by this run: embedding config (`EMBED_MODEL=text-embedding-nomic-embed-text-v2`,
768 d), vault mounts, `LLM_TIMEOUT_SECS=600`, `LLM_MAX_INFLIGHT=1`.
