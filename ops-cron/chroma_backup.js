#!/usr/bin/env node
// chroma_backup.js — snapshot helper for the vectorizer nightly backup.
// Run inside node:24-alpine with the chroma volume mounted (see vectorizer-backup-daily.sh):
//   docker run --rm -v vectorizer_chroma_data:/data -v <ops>:/scripts:ro -v <bk>:/backup \
//     node:24-alpine node /scripts/chroma_backup.js --date YYYY-MM-DD
//
// Does three things and prints ONE JSON line on stdout:
//   1. walks /data -> {files, bytes, newest_mtime_epoch}   (write-activity check: if
//      newest_mtime is recent, ingestion is happening right now)
//   2. VACUUM INTO /backup/chroma-sqlite-<date>.db         (consistent online copy of the
//      metadata db — the live chroma.sqlite3 in the tarball may be torn)
//   3. re-opens the COPY and verifies it: integrity_check + collections + embeddings counts
//
// NOTE: Chroma 1.0 does NOT store vectors in sqlite (embeddings table has no vector
// column) — the vectors live in the per-segment .bin files under /data/<uuid>/. So a
// usable backup needs BOTH the consistent sqlite copy AND the segment dirs (the tarball).
const { DatabaseSync } = require('node:sqlite');
const fs = require('node:fs');
const path = require('node:path');

const args = process.argv.slice(2);
const opt = (k, d) => { const i = args.indexOf(k); return i >= 0 ? args[i + 1] : d; };
const DATA = opt('--data', '/data');
const BACKUP = opt('--backup', '/backup');
const DATE = opt('--date', new Date().toISOString().slice(0, 10));

function walk(dir, acc) {
  for (const ent of fs.readdirSync(dir, { withFileTypes: true })) {
    const p = path.join(dir, ent.name);
    if (ent.isDirectory()) { walk(p, acc); continue; }
    if (!ent.isFile()) continue;
    const st = fs.statSync(p);
    acc.files += 1;
    acc.bytes += st.size;
    if (st.mtimeMs > acc.newest) acc.newest = st.mtimeMs;
  }
  return acc;
}

const acc = walk(DATA, { files: 0, bytes: 0, newest: 0 });

const dest = path.join(BACKUP, `chroma-sqlite-${DATE}.db`);
fs.rmSync(dest, { force: true });

const srcPath = path.join(DATA, 'chroma.sqlite3');
const src = new DatabaseSync(srcPath); // read/write open: WAL + live db handled by sqlite locking
src.exec(`VACUUM INTO '${dest.replace(/'/g, "''")}'`);
src.close();

const dst = new DatabaseSync(dest, { readOnly: true });
const integrity = dst.prepare('PRAGMA integrity_check').get();
const collections = dst.prepare('SELECT count(*) AS c FROM collections').get().c;
const embeddings = dst.prepare('SELECT count(*) AS c FROM embeddings').get().c;
dst.close();

console.log(JSON.stringify({
  files: acc.files,
  bytes: acc.bytes,
  newest_mtime_epoch: Math.floor(acc.newest / 1000),
  sqlite_dest: dest,
  sqlite_bytes: fs.statSync(dest).size,
  integrity: integrity.integrity_check,
  collections,
  embeddings,
}));
