# Codebase Indexing Mode — Spec + Prototype Status

## Goal
Give Vectorizer a `code` workspace type: ingest a repo file-by-file with
`{file, symbol, language}` metadata and DEFINES/CALLS/IMPORTS edges in
GRAPH.json — so agents ask "what calls X" structurally instead of grepping.
Adapted from codebase-memory-mcp's core loop, minus tree-sitter/LSP (regex
symbol extraction per language; Go + Python + TS first).

## Design (fits existing architecture — no new infra)

- **Workspace naming:** `code_<repo>` (e.g. `code_vectorizer`). Normal
  workspace isolation applies; `_global` merge + floors apply unchanged.
- **Chunk = symbol, not 4000-char window.** One chunk per top-level
  def (func/type/class/interface). Body + docstring. Small symbols merge
  to a ~2000-char floor so Chroma isn't spammed with 3-line getters.
- **Metadata per chunk** (existing keys, already passed through by
  AddMessage — no store changes needed):
  `source_type=file, source_path=<rel path>, language=<go|python|ts>,
  chunk_type=symbol|import|file_overview, entities=<symbol name>,
  parent_doc_id=<file path>, importance=3 (code defaults searchable but
  never timeless)`.
- **Graph edges** (free-form Relation strings — fits graph.go today):
  `file DEFINES symbol`, `symbol CALLS symbol` (same-repo, name-matched,
  post-pass), `file IMPORTS path`. Query via existing `Expand()`.
- **File overview chunk:** first chunk per file = package/clause + import
  list + symbol index. Makes "what's in pkg X" answerable in one hit.
- **Re-index = content-hash skip:** `file_hash` per file; unchanged files
  skip re-embed (same pattern as AddMessage contentHash dedup).
- **New MCP tools (phase 2):** `vectorizer_index_repo {path, workspace?}`,
  `vectorizer_symbols {workspace_id, file?}`, `vectorizer_callers {symbol}`.
  Phase 1 ships the extractor as a library + CLI; HTTP/MCP wiring follows
  once the chunk shape is validated on a real repo.

## Deliberately NOT copying
Tree-sitter grammars, Hybrid LSP, Cynex batching, depth-weighted decay on
code, Louvain clustering — wrong layer or overkill at this scale. Regex
symbols + BFS Expand is enough until slowness is proven.

## Prototype (this repo, phase 1 — DONE 2026-09-04)
`internal/codeindex/` — stdlib-only extractor:
- `ParseFile(path, src)` → FileSymbols{Language, Imports, Symbols[]}
  (Go: `^func`, `^type`; Python: `^def`, `^class`; TS: `^function`,
  `^class`, `=>`-assigned consts). Unknown extensions skipped.
- `ChunkFile(path, src)` → overview chunk + one chunk per symbol with
  the metadata map above, merged to 2000-char floor.
- `CallEdges(symbols)` → CALLS pairs by name-mention in bodies
  (same-repo, excludes self, stdlib-blind — precision over recall).
- Covered by unit test (`extractor_test.go`).

## Phase 2 (not started)
1. `POST /code/index {path, workspace_id?}` handler: walk repo (respect
   `.gitignore` + `vecignore`), hash-skip unchanged, AddMessage per chunk,
   graph.AddEdge DEFINES/CALLS/IMPORTS.
2. MCP: index_repo / symbols / callers tools.
3. Validate on vectorizer repo itself: index `internal/store`, ask
   "what calls UpdateMessage" via trace/Expand, compare vs grep.
