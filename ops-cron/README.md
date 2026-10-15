# vectorizer-ops — nightly backup + hourly health probe (ns539881)

Installed by card t_551df9d9 (2026-10-05). Two cron jobs in `ubuntu`'s crontab:

| job | schedule (host time, TZ = UTC) | script | log |
|---|---|---|---|
| `vectorizer-backup-daily` | `0 3 * * *` (03:00 UTC = 11:00 MYT) | `/opt/vectorizer/ops-cron/vectorizer-backup-daily.sh` | `/var/log/vectorizer/vectorizer-backup-daily.log` |
| `vectorizer-health-1h` | `0 * * * *` | `/opt/vectorizer/ops-cron/vectorizer-health-1h.sh` | `/var/log/vectorizer/vectorizer-health-1h.log` |

Shared helper: `vz-alert.sh` (POST/resolve alerts in Alertmanager `127.0.0.1:9093`),
`chroma_backup.js` (sqlite snapshot, runs in a throwaway `node:24-alpine` container).

## What the backup contains — `/home/ubuntu/backups/vectorizer/` (mode 700, 7-day retention)

- `chroma-volume-<date>.tar.gz` — the **whole `vectorizer_chroma_data` docker volume**.
  This is the only complete copy of the data: Chroma 1.0 does **not** store vectors in
  sqlite (the `embeddings` table has no vector column), the vectors live in the per-segment
  `data_level0.bin` files under `/data/<uuid>/`. Never restore sqlite without these.
- `chroma-sqlite-<date>.db` — consistent online copy of `chroma.sqlite3` made with
  `VACUUM INTO` (metadata, FTS, collection/config state). The live db file inside the
  tarball can be torn if a write landed mid-copy — prefer this one on restore.
- `vectorizer-config-<date>.tar.gz` — `.env`, compose files, `up.sh` (mode 600; contains secrets).

Write-safety: before tarring, the job waits up to 2 minutes for a quiet window (no file in
the volume modified in the last 120 s), then proceeds hot with a `WARN` if ingestion is
persistent. The sqlite copy is consistent regardless. Chroma is **never stopped** for a backup.

## Health probe checks (one line per check + SUMMARY)

`vectorizer-api` (8091 `/api/v1/health` → 200), `chromadb` (8102 version → 200 **and**
collections > 0), `chromadb-read` (sampled readability, t_4a2e438e: resolve
`ws_code_ags` + `probe-content-only` + `ws__global` to UUIDs, GET `/count` each
expecting a numeric body, plus one POST `/get` limit=1 expecting ids — catches the
compactor/backfill fault class from t_d1b3b72e that stays green on version+count>0;
recurrence runbook `cd /opt/vectorizer && ./up.sh recreate vectorizer-chromadb`),
`dashboard` (8092 → 200/307), `mcp` (8093 → any HTTP answer), the four
containers running/healthy, and **backup freshness** (newest `chroma-volume-*.tar.gz` ≤ 26 h
old; skipped while the backup lock is held).

## Alerting

Failures POST to Alertmanager → existing receivers (t_4a12ed0f): `severity=critical` goes to
**owner Telegram + email**, everything else email only. Both alerts below are critical.

- `VectorizerHealth` — any service/container check failing. Re-posted hourly while broken
  (refreshes a 90-minute `endsAt`), resolved automatically when checks pass. A dead probe
  self-expires within 90 min, so a stuck alert is impossible.
- `VectorizerBackupStale` — no backup in the last 26 h (this is the watchdog that makes
  "silently missing backup" impossible — it fired for 2 nights in Sep and would now page).
- `VectorizerBackupFailed` — the nightly job itself failing (fired immediately with the reason).

State flags live in `~ubuntu/.local/state/vectorizer-ops/`. Alertmanager being down is
covered by the Prometheus `ContainerDown` rules on every container.

## Scheduled I/O window silence (`vz-silence.sh`)

A backup streams ~15 GB across the single md3 RAID1 (read 5 GB volume → write 5 GB tar →
re-read 5 GB for pigz). Measured on the first run: avg I/O queue >50 for 10+ min, i.e.
`DiskIOSaturated` warning + **critical** fired for real. So the backup job calls
`vz-silence.sh start 75` right after taking its lock: one Alertmanager silence with a regex
matcher `alertname=~DiskIOSaturated|DStateProcesses|HighCPU`,
`createdBy: vectorizer-backup-daily`,
hard 75-minute expiry (self-destructs even if the job dies, no Prometheus rule touched).
Everything else — SiteDown, ContainerDown, LowMemory, DiskFull, and the three vectorizer
alerts above — stays fully audible during the window. Manual control: `vz-silence.sh start 90`
/ `vz-silence.sh end`.

## Restore procedure (verified by drill — see card t_551df9d9)
```bash
DATE=YYYY-MM-DD
BK=/home/ubuntu/backups/vectorizer
mkdir -p /tmp/vz-restore && cd /tmp/vz-restore
pigz -dc "$BK/chroma-volume-$DATE.tar.gz" | tar -xf -
cp "$BK/chroma-sqlite-$DATE.db" chroma.sqlite3     # consistent db wins over the live one
docker run -d --name vz-restore-test -p 127.0.0.1:8199:8000 \
  -v /tmp/vz-restore:/data chromadb/chroma:1.0.0
curl -s http://127.0.0.1:8199/api/v2/tenants/default_tenant/databases/default_database/collections | jq length
```

Compare collection count and per-collection document counts against production
(`http://127.0.0.1:8102/...`), then `docker rm -f vz-restore-test`.

Config restore: extract `vectorizer-config-<date>.tar.gz` over `/opt/vectorizer/`
(only with the stack stopped — deploys go through `up.sh` only, per the standing rule).

### Drill result (2026-10-05, `restore-drill-v2.sh`, archive `chroma-volume-2026-10-04`)

- extracted 4.7 G, swapped in `chroma-sqlite-2026-10-04.db`, started a scratch chroma on
  `127.0.0.1:8199` — came up, `HTTP 200`, version 1.0.0
- collection sets: `live=119 restored=119 identical_set=yes` (id + name, byte-identical JSON)
- counts by UUID: `ws_code_aict_codeedges 17016=17016`, `ws_code_ags 8358=8358`,
  `ws_code_vectorizer 4063=4063`, `ws_alya_conclusions 378=378`, `ws_code_aict 637=637`
  (a 6th sample, `probe-content-only`, errored on the **live** instance during the drill —
  see the compactor finding below — while the restored copy answered `637`)
- filtered read on the restored sidecar (`where workspace_id=code_aict`): 3 rows with
  documents, `calls:get_api_key` — i.e. restored data is queryable, not just present
- restored sqlite: 55,605 embedding rows = live

### Drill result (2026-10-14, `restore-drill-v2.sh`, archive `chroma-volume-2026-10-14`, run for t_79c254ce)

- extracted 1.8 G, swapped in `chroma-sqlite-2026-10-14.db`, scratch chroma on
  `127.0.0.1:8199` — came up, `HTTP 200`
- collection sets: `live=121 restored=121 identical_set=yes` (id + name)
- counts by UUID: `ws_code_ags 8358=8358`, `ws_code_vectorizer 4063=4063`,
  `probe-content-only 637=637`, `ws_alya_conclusions 693=693`,
  `ws_code_aict 637=637` MATCH; `ws_code_aict_codeedges` restored `17016`
  while live errored mid-drill, then answered `17016` on immediate re-check
  (transient under drill load — same compactor/backfill fault class as the
  2026-10-05 open finding, not a backup defect)
- filtered read on the restored sidecar (`where workspace_id=code_aict`): 3 rows,
  `calls:get_api_key` — READ_CHECK=OK
- restored sqlite: 62,762 embedding rows; scratch container + workdir removed
  after the drill (verified clean)
- backup footprint is now 4.1 G total under 7-day retention (task text "698M" was stale)

## Known limitations / follow-ups

- **Open finding (2026-10-05, not caused by this job):** the *live* chromadb fails
  `count`/`get` on 9 of 119 collections with
  `InternalError: Error executing plan: Error sending backfill request to compactor` —
  `ws_t7c_e2e_1791148918`, `ws_t7c_e2e_1791149995`, `ws_t7c_e2e_1791150607`,
  `ws_t7c_probe_2788`, `t7c_probe3_10484`, `probe-content-only`, `probe-content-l2`,
  `ws_probe`, `ws__global`. The identical collections served from the restored snapshot
  answer normally, so the on-disk data is fine and the fault is live service state.
  Follow-up: t_d1b3b72e (@nadia).

- **Same-host only.** The backup sits on the same 1.8 T disk as the live volume; it protects
  against app-level corruption, bad migrations and accidental drops, **not** disk/host loss.
  Off-host replication (rsync is installed, no remote endpoint configured) is a separate
  decision — follow-up: t_87d57b8f (@maisarah, triage).
- `/opt/vectorizer/vault-data` (11 G) and the git repo history are **not** part of this backup.
- Retention is 7 days (measured ≈ 500 MB/day → ~3.5 GB of disk for backups; the 160 MB
  daily archive is 4.97 GB uncompressed).
- `/opt/vectorizer/vectorizer_backup.py` and `vectorizer_healthcheck.py` are stale
  Windows-local scripts (hardcoded `C:\Users\...` paths); they are not used by this job.

Re-run `install.sh` any time — it is idempotent.
