# Sharing prep — awesome-selfhosted + r/selfhosted (t_6d2db280)

Ready-to-paste submissions for the Vectorizer launch. LICENCE is MIT,
demo is one command (`demo/`), no cloud account needed for the core loop
(LM Studio or any OpenAI-compatible endpoint for embeddings).

## awesome-selfhosted PR

Repo: `awesome-selfhosted/awesome-selfhosted`, file `software/...` —
Vectorizer fits under **Knowledge Management / Personal Dashboards**
(neighbour entries: mem0, Bypass — check current headings at PR time).

Suggested entry (keep to their one-line format):

```text
[Vectorizer](https://github.com/alfirus/vectorizer) — Self-hosted semantic
memory server for AI agents. Workspace-isolated vector memory (ChromaDB),
markdown vault as source of truth, hybrid vector+BM25 search, MCP server.
`MIT` `Go/Docker`
```

PR checklist: one commit, entry alphabetical in its section, link resolves,
demo link optional (`demo/` in-repo). Maintainers ask for a live demo or
screenshots — point at `README.md` § API in Action + `demo/seed.sh` output.

## r/selfhosted launch post (draft)

Title: `Vectorizer — self-hosted semantic memory for AI agents (Go + ChromaDB, MIT)`

Body:

```text
Hi all — sharing Vectorizer, the memory layer I run for my agents.

What it is: a small Go server that stores agent messages as embeddings in
ChromaDB, one isolated workspace per agent. The twist: markdown files are
the source of truth (git-backed vault), the vector index is a fingerprint
beside it — including a file-graph (GRAPH.json) and a workflow librarian
(tagging + rerank in code, not LLM calls, ~150ms).

- Hybrid search (vector cosine + identifier-aware BM25 fusion), honest
  per-hit source labels
- MCP server (25 tools), Next.js dashboard, offline evals
- One-command demo: docker compose -f demo/docker-compose.demo.yml up -d
  --build && ./demo/seed.sh (Chroma + server + 3 sample notes)
- Nightly backups with restore drills documented in ops-cron/README.md

MIT licensed. Happy to answer questions on the embedding setup
(LM Studio Nomic 768d default, Qwen3-Embedding opt-in).
```

Post timing: weekday mornings UTC; answer the first wave of comments
(embeddings + backup questions) within a few hours — that decides whether
the post survives new-queue.
