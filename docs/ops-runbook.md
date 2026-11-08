# Ops runbook — backup, health, RPO/RTO, restore drill

Portable tooling for the two root scripts plus the production cron jobs
in `ops-cron/` (see `ops-cron/README.md` for the live host layout).

## Environment configuration (P2-3: no hardcoded paths)

Both scripts read every path, port, and URL from the environment, with
the previous Windows workstation values kept as `os.name == "nt"`
fallbacks and Linux-friendly defaults otherwise.

`vectorizer_backup.py`:

| Variable | Default (Linux) | Purpose |
|---|---|---|
| `VECTORIZER_BACKUP_DIR` | `./backups/vectorizer` | where `chroma-*.tar.gz` / `chroma-metadata-*.json.gz` land |
| `VECTORIZER_BACKUP_KEEP_DAYS` | `7` | prune backups older than N days |
| `CHROMA_URL` | `http://localhost:8100` | Method A: metadata backup over HTTP |
| `CHROMA_CONTAINER` | `vectorizer-chromadb` | Methods B/C: `docker cp` / `docker exec` target |

`vectorizer_healthcheck.py`:

| Variable | Default | Purpose |
|---|---|---|
| `VECTORIZER_ENV_FILE` | `./.env` | `.env` parsed for LM Studio token/URL |
| `ALERT_EMAIL` | `alfirus@gmail.com` | auto-heal failure recipient |
| `VECTORIZER_URL` | `http://localhost:8091` | vectorizer probe |
| `CHROMA_URL` | `http://localhost:8100` | chromadb probe |
| `DASHBOARD_URL` | `http://localhost:8094` | dashboard probe |
| `CHROMA_PORT` / `VECTORIZER_PORT` / `DASHBOARD_PORT` | `8100` / `8091` / `8094` | `docker run` fallback mappings |
| `COMPOSE_FILE` | `./docker-compose.yml` | auto-heal `docker compose up` target |
| `TELEGRAM_BOT_TOKEN` / `TELEGRAM_CHAT_ID` | empty (alerts skipped) | Telegram alerts |

Example (Linux server):

```sh
VECTORIZER_BACKUP_DIR=/home/ubuntu/backups/vectorizer \
CHROMA_URL=http://127.0.0.1:8100 \
python3 vectorizer_backup.py
```

## Compose backup profile

`docker-compose.yml` ships a profile-gated one-shot `backup` service
(Method A over the compose network, no Docker socket needed):

```sh
# one-off
docker compose --profile backup run --rm backup
# nightly cron (host)
0 3 * * * cd /opt/vectorizer && docker compose --profile backup run --rm backup >> /var/log/vectorizer-backup.log 2>&1
```

Retention inside the container honors `BACKUP_KEEP_DAYS` (default 7).

## RPO / RTO

- **RPO ≤ 24h**: nightly backup (03:00 UTC cron or the compose profile).
  Intraday vectors between backups are re-derivable from source vault
  markdown via the vault indexer, not from the backup.
- **RTO ≤ 1h**: restore is a volume swap + container restart (drill
  below); no code deploy is on the critical path.
- Production host jobs (`ops-cron/`) keep the same 7-day retention with
  mode-700 backup dirs; the sqlite `VACUUM INTO` copy is the consistent
  metadata source, the volume tarball carries the segment `.bin` files
  (Chroma 1.0 does not store vectors in sqlite).

## Restore drill (quarterly, or before every minor/major release)

1. Pick the newest `chroma-volume-<date>.tar.gz` + `chroma-sqlite-<date>.db`.
2. `ops-cron/restore-drill.sh <date>` — extracts to a scratch dir, swaps
   in the consistent sqlite copy, boots a throwaway Chroma on
   `127.0.0.1:8199`, and diffs live vs restored collection counts.
   `restore-drill-v2.sh` is the newer variant; either passing counts.
3. Record the drill (date, backup used, counts compared) in the release
   notes. A drill that fails blocks the release the same way a red eval
   gate does.
