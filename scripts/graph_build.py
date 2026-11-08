#!/usr/bin/env python3
"""graph_build.py — rebuild the vault file-graph (GRAPH.json).

Derives nodes (``doc`` / ``chunk`` / ``entity`` / ``folder``) and edges
(``belongs_to`` / ``next_chunk`` / ``mentions`` / ``links_to``) from the
markdown vault itself, using the same chunker as ``vault_index.py`` so chunk
IDs line up with the vectors in ChromaDB.

Output: ``<vault>/00-index/GRAPH.json`` — ``{"nodes": [...], "edges": [...]}``.

Stdlib only — runs with any Python 3.9+.

Usage:
    python scripts/graph_build.py --workspace maisarah
"""

import argparse
import hashlib
import json
import os
import re
import sys

import vault_index as vi

WIKILINK_RE = re.compile(r"\[\[([^\]]+)\]\]")


def main():
    ap = argparse.ArgumentParser(description="Rebuild vault GRAPH.json.")
    ap.add_argument("--workspace", default=os.environ.get("WORKSPACE", "maisarah"))
    ap.add_argument("--vault-root", default=os.environ.get("VAULT_ROOT", "/data/ai"))
    args = ap.parse_args()

    vault = os.path.join(args.vault_root, args.workspace, "vault")
    if not os.path.isdir(vault):
        print("FATAL: vault dir not found: %s" % vault, file=sys.stderr)
        return 1
    index_dir = os.path.join(vault, "00-index")
    os.makedirs(index_dir, exist_ok=True)

    nodes, edges = [], []
    seen_nodes = set()

    def add_node(node_id, ntype, label):
        if node_id not in seen_nodes:
            seen_nodes.add(node_id)
            nodes.append({"id": node_id, "type": ntype, "label": label})

    def add_edge(src, rel, dst):
        edges.append({"src": src, "rel": rel, "dst": dst})

    # Folder nodes mirror the on-disk layout.
    for root, dirs, _files in os.walk(vault):
        dirs[:] = sorted(d for d in dirs if d != "00-index")
        rel = os.path.relpath(root, args.vault_root).replace(os.sep, "/")
        add_node("folder:%s" % rel, "folder", rel)

    for root, dirs, files in os.walk(vault):
        dirs[:] = sorted(d for d in dirs if d != "00-index")
        for name in sorted(files):
            if not name.endswith(".md"):
                continue
            path = os.path.join(root, name)
            rel = os.path.relpath(path, args.vault_root).replace(os.sep, "/")
            with open(path, encoding="utf-8", errors="replace") as f:
                text = f.read()
            doc_id = "doc:" + hashlib.sha256(rel.encode()).hexdigest()[:16]
            add_node(doc_id, "doc", rel)
            parent = os.path.dirname(rel)
            if parent:
                add_edge(doc_id, "belongs_to", "folder:%s" % parent)

            chunks = vi.chunk_markdown(text, rel)
            prev_chunk = None
            for i, (header_path, _ctype, body) in enumerate(chunks):
                chunk_id = "chunk:%s:%d" % (doc_id[4:], i)
                add_node(chunk_id, "chunk", "%s#%d" % (rel, i))
                add_edge(chunk_id, "belongs_to", doc_id)
                if prev_chunk:
                    add_edge(prev_chunk, "next_chunk", chunk_id)
                prev_chunk = chunk_id
                for ent in vi.extract_entities(body, header_path):
                    ent_id = "entity:%s" % ent.lower()
                    add_node(ent_id, "entity", ent)
                    add_edge(chunk_id, "mentions", ent_id)
            for link in sorted(set(WIKILINK_RE.findall(text))):
                add_edge(doc_id, "links_to", "doc:%s" % link.strip())

    out = os.path.join(index_dir, "GRAPH.json")
    with open(out, "w") as f:
        json.dump({"nodes": nodes, "edges": edges}, f, indent=2)
    print("GRAPH.json: %d docs/chunks, %d nodes, %d edges -> %s"
          % (len([n for n in nodes if n["type"] == "doc"]),
             len(nodes), len(edges), out))
    return 0


if __name__ == "__main__":
    sys.exit(main())
