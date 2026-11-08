#!/usr/bin/env python3
"""vault_index.py — index a markdown vault into Vectorizer.

Reads every ``*.md`` under ``<VAULT_ROOT>/<workspace>/vault`` (skipping the
``00-index/`` bookkeeping dir), splits files with the documented
markdown-aware chunker (H1/H2/H3 sections, 3600-4800 chars + 200 overlap,
never inside ``` fences, prefixed with ``header_path``), and upserts changed
chunks via ``POST /messages/batch`` (batches of 10).

Unchanged files are skipped by SHA-256 hash against the registry at
``<vault>/00-index/MEMORY_INDEX.json`` (``--reindex`` forces a rebuild).

Stdlib only — runs with any Python 3.9+.

Usage:
    python scripts/vault_index.py --workspace maisarah [--dry-run] [--reindex]
    VAULT_ROOT=/data/ai VECTORIZER_URL=http://localhost:8091 \\
        VECTORIZER_API_KEY=secret python scripts/vault_index.py
"""

import argparse
import hashlib
import json
import os
import re
import sys
import urllib.request

# Documented chunker bounds (README: 3600-4800 chars + 200 overlap).
TARGET_MIN = 3600
TARGET_MAX = 4800
OVERLAP = 200

# The 11 documented flat metadata fields.
META_FIELDS = (
    "source_type", "source_path", "header_path", "chunk_type",
    "created_at", "tags", "importance", "agent", "language",
    "parent_doc_id", "doc_title",
)

HEADER_RE = re.compile(r"^(#{1,3})\s+(.*)$")
KNOWN_ENTITIES = [
    "Maisarah", "Alfirus", "Bukku", "DJI",
    "Vectorizer", "ChromaDB", "SynologyDrive",
]


def sha256_file(path):
    h = hashlib.sha256()
    with open(path, "rb") as f:
        for blk in iter(lambda: f.read(65536), b""):
            h.update(blk)
    return h.hexdigest()


def keyword_tags(rel_path, header_path):
    """Workflow tagger: tokens from folder path + headers (lowercased)."""
    words = re.findall(r"[A-Za-z]{3,}", rel_path + " " + header_path)
    seen, tags = set(), []
    for w in words:
        t = w.lower()
        if t not in seen:
            seen.add(t)
            tags.append(t)
    return tags[:12]


def extract_entities(text, header_path):
    """Known-list entities + TitleCase header tokens."""
    found = [e for e in KNOWN_ENTITIES if e in text]
    for tok in re.findall(r"\b[A-Z][a-z]{2,}(?:[A-Z][a-z]+)*\b", header_path):
        if tok not in found:
            found.append(tok)
    return found[:20]


def chunk_markdown(text, rel_path):
    """Split markdown into section-aware chunks.

    Sections break at H1/H2/H3 (never inside ``` fences); sections are
    packed to TARGET_MIN..TARGET_MAX chars with OVERLAP chars of overlap
    between consecutive chunks. Each chunk is prefixed with its header path.
    Returns a list of (header_path, chunk_type, body) tuples.
    """
    sections = []  # (header_path, level, lines)
    headers, buf, in_fence = [], [], False
    for line in text.splitlines(keepends=True):
        if line.strip().startswith("```"):
            in_fence = not in_fence
        m = HEADER_RE.match(line.rstrip("\n")) if not in_fence else None
        if m:
            if buf:
                sections.append(("/".join(headers), len(headers), buf))
                buf = []
            level = len(m.group(1))
            headers = headers[: level - 1] + [m.group(2).strip()]
        else:
            buf.append(line)
    if buf or not sections:
        sections.append(("/".join(headers), len(headers), buf))

    chunks = []
    for header_path, level, lines in sections:
        body = "".join(lines)
        start = 0
        ctype = "section_h%d" % level if level else "prose"
        if len(body) <= TARGET_MAX:
            chunks.append((header_path, ctype, body))
            continue
        while start < len(body):
            end = min(start + TARGET_MAX, len(body))
            if end < len(body):
                # Prefer a word boundary near the max.
                cut = body.rfind(" ", start + TARGET_MIN, end)
                end = cut if cut != -1 else end
            chunks.append((header_path, ctype, body[start:end]))
            if end >= len(body):
                break
            start = max(end - OVERLAP, start + 1)
    return chunks


def importance_for(rel_path):
    """Importance heuristic matching the documented vault layout."""
    p = rel_path.replace(os.sep, "/")
    if "longterm" in p or "decisions" in p:
        return 5
    if "howto" in p or "shared" in p or "_shared" in p:
        return 4
    if "topics" in p or "reference" in p or "projects" in p:
        return 3
    return 2


def build_messages(chunks, rel_path, workspace, file_hash):
    parent_doc_id = hashlib.sha256(rel_path.encode()).hexdigest()[:16]
    doc_title = os.path.splitext(os.path.basename(rel_path))[0]
    lang = "ms" if workspace in ("maisarah", "family") else "en"
    msgs = []
    for i, (header_path, ctype, body) in enumerate(chunks):
        content = ("# %s\n%s" % (header_path, body)) if header_path else body
        first_line = next((ln.strip() for ln in body.splitlines() if ln.strip()), "")
        msgs.append({
            "workspace_id": workspace,
            "role": "vault",
            "content": content,
            "metadata": {
                "source_type": "vault-md",
                "source_path": rel_path,
                "header_path": header_path,
                "chunk_type": ctype,
                "created_at": "",
                "tags": keyword_tags(rel_path, header_path),
                "importance": importance_for(rel_path),
                "agent": workspace,
                "language": lang,
                "parent_doc_id": parent_doc_id,
                "doc_title": doc_title,
                "entities": extract_entities(body, header_path),
                "summary_1line": first_line[:200],
                "chunk_id": "%s:%d" % (parent_doc_id, i),
                "file_hash": file_hash,
            },
        })
    return msgs


def api_post(url, api_key, payload):
    req = urllib.request.Request(
        url.rstrip("/") + "/api/v1/messages/batch",
        data=json.dumps(payload).encode(),
        headers={"Content-Type": "application/json", "X-API-Key": api_key},
        method="POST",
    )
    with urllib.request.urlopen(req, timeout=120) as resp:
        return resp.status, resp.read().decode()[:200]


def main():
    ap = argparse.ArgumentParser(description="Index a markdown vault into Vectorizer.")
    ap.add_argument("--workspace", default=os.environ.get("WORKSPACE", "maisarah"))
    ap.add_argument("--vault-root", default=os.environ.get("VAULT_ROOT", "/data/ai"))
    ap.add_argument("--url", default=os.environ.get("VECTORIZER_URL", "http://localhost:8091"))
    ap.add_argument("--api-key", default=os.environ.get("VECTORIZER_API_KEY", ""))
    ap.add_argument("--dry-run", action="store_true")
    ap.add_argument("--reindex", action="store_true")
    args = ap.parse_args()

    vault = os.path.join(args.vault_root, args.workspace, "vault")
    if not os.path.isdir(vault):
        print("FATAL: vault dir not found: %s" % vault, file=sys.stderr)
        return 1
    index_dir = os.path.join(vault, "00-index")
    os.makedirs(index_dir, exist_ok=True)
    registry_path = os.path.join(index_dir, "MEMORY_INDEX.json")
    try:
        with open(registry_path) as f:
            registry = json.load(f)
    except (OSError, ValueError):
        registry = {}

    if not args.api_key and not args.dry_run:
        print("FATAL: set VECTORIZER_API_KEY (or --api-key), or use --dry-run.", file=sys.stderr)
        return 1

    to_index, skipped = 0, 0
    batch, results = [], {"ok": 0, "fail": 0}
    for root, dirs, files in os.walk(vault):
        dirs[:] = sorted(d for d in dirs if d != "00-index")
        for name in sorted(files):
            if not name.endswith(".md"):
                continue
            path = os.path.join(root, name)
            rel = os.path.relpath(path, args.vault_root).replace(os.sep, "/")
            digest = sha256_file(path)
            if not args.reindex and registry.get(rel) == digest:
                skipped += 1
                continue
            with open(path, encoding="utf-8", errors="replace") as f:
                text = f.read()
            chunks = chunk_markdown(text, rel)
            to_index += 1
            if args.dry_run:
                print("would index: %s (%d chunks)" % (rel, len(chunks)))
                continue
            batch.extend(build_messages(chunks, rel, args.workspace, digest))
            registry[rel] = digest
            while len(batch) >= 10:
                try:
                    api_post(args.url, args.api_key, {"messages": batch[:10]})
                    results["ok"] += 10
                except Exception as e:  # noqa: BLE001 — report and continue
                    results["fail"] += 10
                    print("batch failed: %s" % e, file=sys.stderr)
                batch = batch[10:]

    if batch and not args.dry_run:
        try:
            api_post(args.url, args.api_key, {"messages": batch})
            results["ok"] += len(batch)
        except Exception as e:  # noqa: BLE001
            results["fail"] += len(batch)
            print("batch failed: %s" % e, file=sys.stderr)

    with open(registry_path, "w") as f:
        json.dump(registry, f, indent=2, sort_keys=True)

    print("To index: %d files, skipped %d | upserted ok=%d fail=%d"
          % (to_index, skipped, results["ok"], results["fail"]))
    return 1 if results["fail"] else 0


if __name__ == "__main__":
    sys.exit(main())
