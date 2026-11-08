package handlers

import (
	"crypto/sha256"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/alfirus/vectorizer/internal/codeindex"
	"github.com/alfirus/vectorizer/internal/models"
	"github.com/alfirus/vectorizer/internal/store"
	"github.com/gofiber/fiber/v2"
)

// CodeHandler serves codebase indexing mode (phase 2): walk a server-local
// repo path, chunk per symbol, hash-skip unchanged files, record
// DEFINES/CALLS/IMPORTS as reasoning edges so trace/Expand answers
// "what calls X" structurally.
//
// Security: path must be server-local. The server only sees its own
// mounts; the agent passes a repo path ON THE SERVER (e.g. /data/repo/<name>).
// No file content crosses the wire — only the path string. Requests are
// jailed to CODE_INDEX_ROOT (see jailCodePath) — F5.
type CodeHandler struct{ store *store.Store }

func NewCodeHandler(s *store.Store) *CodeHandler { return &CodeHandler{store: s} }

// vecignore: always skip these dirs/names (plus .gitignore basenames).
// exports/ holds vault JSON dumps (memory content, NOT code) — never index.
// vault-data/ is the same class of junk (live vault JSON dumps) — never index.
var codeSkipDirs = map[string]bool{
	".git": true, "node_modules": true, "vendor": true, "__pycache__": true,
	".venv": true, "venv": true, "dist": true, "build": true, ".next": true,
	"target": true, ".idea": true, ".vscode": true, "bin": true, "obj": true,
	"exports": true, "vault-data": true,
}

const maxCodeFileBytes = 500 * 1024 // skip generated monsters

// IndexResult is the per-file + aggregate report. Skip counters make refusals
// visible: previously .yml/.sql files vanished with no trace in this output,
// which is why nobody noticed the config gap (Defect 6).
type IndexResult struct {
	WorkspaceID        string   `json:"workspace_id"`
	Files              int      `json:"files_indexed"`
	Skipped            int      `json:"files_skipped_unchanged"`
	SkippedUnsupported int      `json:"files_skipped_unsupported_ext"`
	SkippedTooLarge    int      `json:"files_skipped_too_large"`
	FilesSeen          int      `json:"files_seen"`
	Symbols            int      `json:"symbols"`
	Edges              int      `json:"edges"`
	Errors             []string `json:"errors,omitempty"`
	Indexed            []string `json:"indexed_files,omitempty"`
}

// Index walks path and indexes code files into workspace_id
// (default code_<repo-basename>).
// POST /code/index {"path": "/data/repo/<name>", "workspace_id"?: "...", "session_id"?: "..."}
//
// F5 jail: path must resolve inside CODE_INDEX_ROOT (comma-separated,
// default "/data/ai,/data/repo"). Symlinks escaping the root are skipped,
// workspace_id is validated. Requests outside the jail get 403.
func (h *CodeHandler) Index(c *fiber.Ctx) error {
	var req struct {
		Path        string `json:"path"`
		WorkspaceID string `json:"workspace_id"`
		SessionID   string `json:"session_id"`
	}
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request body"})
	}
	if req.Path == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "path is required (server-local repo path)"})
	}
	if req.WorkspaceID != "" && !models.ValidateResourceName(req.WorkspaceID) {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid workspace_id format"})
	}
	if !requireWorkspace(c, req.WorkspaceID) {
		return nil
	}
	root, err := jailCodePath(req.Path)
	if err != nil {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": "path is outside the indexable roots (CODE_INDEX_ROOT)"})
	}
	info, err := os.Stat(root)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "path is not readable on the server"})
	}
	// Single-file mode: index just this file (root files like main.go).
	// Walk root becomes its dir; rel paths stay repo-relative.
	walkRoot := root
	if !info.IsDir() {
		if codeindex.LanguageFor(root) == "" {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "file type not indexable"})
		}
		walkRoot = filepath.Dir(root)
	}
	ws := req.WorkspaceID
	if ws == "" {
		ws = "code_" + strings.ToLower(filepath.Base(root))
	}
	sess := req.SessionID
	if sess == "" {
		sess = fmt.Sprintf("index-%s", time.Now().UTC().Format("20060102-150405"))
	}

	gitignore := loadGitignore(walkRoot)
	res := &IndexResult{WorkspaceID: ws}

	// Phase A: walk + parse.
	type fileJob struct {
		rel  string
		src  string
		hash string
		fs   codeindex.FileSymbols
	}
	var jobs []fileJob
	singleFile := !info.IsDir()
	_ = filepath.WalkDir(walkRoot, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		// F5: never follow a symlink out of the jail, even when the
		// requested root itself is inside it.
		if d.Type()&os.ModeSymlink != 0 {
			if rp, everr := filepath.EvalSymlinks(p); everr != nil || !withinDir(walkRootResolved(walkRoot), rp) {
				res.Errors = append(res.Errors, relDot(p, walkRoot)+": skipped symlink escaping index root")
				if d.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
		}
		if singleFile && p != root {
			if d.IsDir() && p != walkRoot {
				return filepath.SkipDir
			}
			return nil
		}
		rel, _ := filepath.Rel(walkRoot, p)
		if singleFile {
			rel = filepath.Base(p)
		}
		if d.IsDir() {
			base := filepath.Base(p)
			if rel != "." && (codeSkipDirs[base] || gitignore[base]) {
				return filepath.SkipDir
			}
			return nil
		}
		res.FilesSeen++
		if codeindex.LanguageFor(p) == "" {
			res.SkippedUnsupported++
			return nil
		}
		if gitignore[filepath.Base(p)] {
			return nil
		}
		st, err := d.Info()
		if err != nil || st.Size() > maxCodeFileBytes {
			res.SkippedTooLarge++
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			res.Errors = append(res.Errors, rel+": read: "+err.Error())
			return nil
		}
		sum := sha256.Sum256(b)
		hash := fmt.Sprintf("%x", sum[:8])
		jobs = append(jobs, fileJob{rel: rel, src: string(b), hash: hash, fs: codeindex.ParseFile(rel, string(b))})
		return nil
	})
	sort.Slice(jobs, func(i, j int) bool { return jobs[i].rel < jobs[j].rel })

	// Phase B: hash-skip probe per file (one GetDocuments per file — cheap metadata fetch).
	var todo []fileJob
	for _, j := range jobs {
		if h.fileHashCurrent(ws, j.rel, j.hash) {
			res.Skipped++
			continue
		}
		todo = append(todo, j)
	}

	// Phase C: store chunks per file (one message per file — stable ID = path hash).
	for _, j := range todo {
		chunks := codeindex.ChunkFile(j.rel, j.src, 2000)
		if len(chunks) == 0 {
			continue
		}
		msgID := fmt.Sprintf("codefile_%x", sha256.Sum256([]byte(j.rel)))
		msg := models.NewMessage(ws, sess, "system", "")
		msg.ID = msgID[:24]
		msg.Metadata = map[string]interface{}{
			"source_type": "file", "source_path": j.rel, "language": j.fs.Language,
			"chunk_type": "codefile", "parent_doc_id": j.rel, "file_hash": j.hash,
			"importance": 3, "agent": "codeindex",
			"entities": strings.Join(symbolNameList(j.fs), ","),
		}
		var b strings.Builder
		for _, ch := range chunks {
			b.WriteString("## " + chunkTitle(ch) + "\n" + ch.Document + "\n")
		}
		if err := h.store.AddMessage(msg, b.String()); err != nil {
			res.Errors = append(res.Errors, j.rel+": store: "+err.Error())
			continue
		}
		res.Files++
		res.Symbols += len(j.fs.Symbols)
		res.Indexed = append(res.Indexed, j.rel)
	}

	// Phase D: graph edges (file DEFINES symbol via entities; CALLS via CallEdges).
	// Built from ALL walked files, not just hash-changed ones: Phase B skips
	// unchanged files into `todo`, so deriving edges from `todo` alone wrote
	// zero edges on any re-index — leaving callers broken after a deploy.
	// Edge IDs are deterministic, so re-writing every run is a no-op upsert.
	var parsed []codeindex.FileSymbols
	for _, j := range jobs {
		parsed = append(parsed, j.fs)
	}
	for _, e := range codeindex.CallEdges(parsed) {
		if err := h.store.AddCodeEdge(ws, "calls:"+e.Caller,
			[]string{"defines:" + e.Callee}, []string{"file:" + e.File}); err != nil {
			res.Errors = append(res.Errors, "edge "+e.Caller+"->"+e.Callee+": "+err.Error())
			continue
		}
		res.Edges++
		// Reverse orientation, so "who calls X" can be answered directly
		// (Defect 1): calledby:<callee> -> defines:<caller>.
		if err := h.store.AddCodeEdge(ws, "calledby:"+e.Callee,
			[]string{"defines:" + e.Caller}, []string{"file:" + e.File}); err != nil {
			res.Errors = append(res.Errors, "edge "+e.Callee+"<-"+e.Caller+": "+err.Error())
			continue
		}
		res.Edges++
	}
	for _, j := range todo {
		for _, imp := range j.fs.Imports {
			if err := h.store.AddCodeEdge(ws, "file:"+j.rel,
				[]string{"imports:" + imp}, []string{"file:" + j.rel}); err != nil {
				res.Errors = append(res.Errors, "edge imports "+j.rel+": "+err.Error())
				continue
			}
			res.Edges++
		}
	}
	// IndexResult.Errors was only ever returned in the response body. The
	// code_aict reindex ran 2h4m and the MCP client died before reading it, so
	// every failed file below was silently discarded. Log it server-side so an
	// interrupted run still shows what was skipped — that gap is exactly why
	// an embedder outage during indexing would have gone unreported.
	if n := len(res.Errors); n > 0 {
		log.Printf("code/index %s: completed with %d error(s):", ws, n)
		for i, e := range res.Errors {
			if i >= 20 {
				log.Printf("code/index %s:   ... %d more", ws, n-20)
				break
			}
			log.Printf("code/index %s:   %s", ws, e)
		}
	}
	log.Printf("code/index %s: done files=%d skipped=%d too_large=%d unsupported=%d edges=%d errors=%d",
		ws, res.Files, res.Skipped, res.SkippedTooLarge, res.SkippedUnsupported, res.Edges, len(res.Errors))
	return c.JSON(res)
}

// Symbols lists indexed symbols, optionally filtered by file substring.
// GET /code/symbols?workspace_id=...&file=...
func (h *CodeHandler) Symbols(c *fiber.Ctx) error {
	ws := c.Query("workspace_id")
	if ws == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "workspace_id is required"})
	}
	if !requireWorkspace(c, ws) {
		return nil
	}
	fileFilter := strings.ToLower(c.Query("file"))
	// offset+limit: a 200-item hard cap with no paging silently hid files on
	// any repo with >200 source files, and `count` read like a true total.
	offset := c.QueryInt("offset", 0)
	limit := c.QueryInt("limit", 200)
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	if offset < 0 {
		offset = 0
	}
	docs, err := h.store.QueryByMetadata(ws, map[string]interface{}{"source_type": "file"}, 5000)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "query failed"})
	}
	type sym struct {
		File     string `json:"file"`
		Language string `json:"language"`
		Symbols  string `json:"symbols"`
	}
	// Dedupe BEFORE offset/limit: AddMessage splits any file over 6000 chars
	// into N chunk docs, each carrying source_type=file. Counting rows meant
	// counting chunk copies — 001_init.up.sql listed 4x, total 637 for 355
	// files, and offsets landed mid-file. Prefer the overview row, i.e. the
	// one that actually carries symbol entities.
	uniq := map[string]*sym{}
	var order []string
	for _, d := range docs {
		meta, _ := d["metadata"].(map[string]interface{})
		fp, _ := meta["source_path"].(string)
		if fp == "" {
			continue
		}
		ent, _ := meta["entities"].(string)
		lang, _ := meta["language"].(string)
		if cur, ok := uniq[fp]; ok {
			if cur.Symbols == "" && ent != "" {
				cur.Symbols = ent
			}
			if cur.Language == "" && lang != "" {
				cur.Language = lang
			}
			continue
		}
		uniq[fp] = &sym{File: fp, Language: lang, Symbols: ent}
		order = append(order, fp)
	}
	var out []sym
	matched := 0
	for _, fp := range order {
		if fileFilter != "" && !strings.Contains(strings.ToLower(fp), fileFilter) {
			continue
		}
		matched++
		if matched <= offset {
			continue
		}
		if len(out) >= limit {
			continue
		}
		out = append(out, *uniq[fp])
	}
	return c.JSON(fiber.Map{
		"workspace_id": ws, "count": len(out), "total": matched,
		"offset": offset, "limit": limit, "files": out,
	})
}

// Callers answers "what calls X" via CALLS reasoning edges.
// GET /code/callers?workspace_id=...&symbol=...
func (h *CodeHandler) Callers(c *fiber.Ctx) error {
	ws := c.Query("workspace_id")
	symbol := c.Query("symbol")
	if ws == "" || symbol == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "workspace_id and symbol are required"})
	}
	if !requireWorkspace(c, ws) {
		return nil
	}
	callers, err := h.store.CodeCallers(ws, symbol)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "query failed"})
	}
	if callers == nil {
		callers = []string{}
	}
	return c.JSON(fiber.Map{"workspace_id": ws, "symbol": symbol, "callers": callers, "count": len(callers)})
}

// fileHashCurrent checks whether source_path is already indexed at this hash.
func (h *CodeHandler) fileHashCurrent(ws, rel, hash string) bool {
	docs, err := h.store.QueryByMetadata(ws, map[string]interface{}{"source_path": rel}, 5)
	if err != nil || len(docs) == 0 {
		return false
	}
	for _, d := range docs {
		if meta, ok := d["metadata"].(map[string]interface{}); ok {
			if fh, _ := meta["file_hash"].(string); fh == hash {
				return true
			}
		}
	}
	return false
}

func symbolNameList(fs codeindex.FileSymbols) []string {
	var out []string
	for _, s := range fs.Symbols {
		out = append(out, s.Name)
	}
	return out
}

func chunkTitle(ch codeindex.Chunk) string {
	if ch.Metadata["chunk_type"] == "file_overview" {
		return "Overview"
	}
	// Whole-text files (yaml/sql/md/sh/json) have no symbol entities.
	if ch.Metadata["chunk_type"] == "text" {
		if e := ch.Metadata["entities"]; e != "" {
			return e
		}
		return "Content"
	}
	if e := ch.Metadata["entities"]; e != "" {
		parts := strings.Split(e, ",")
		if len(parts) > 3 {
			return strings.Join(parts[:3], ", ") + fmt.Sprintf(" (+%d)", len(parts)-3)
		}
		return strings.ReplaceAll(e, ",", ", ")
	}
	return "Symbols"
}

// loadGitignore returns basenames ignored via .gitignore (flat-name match,
// prototype-grade: full glob semantics not needed for skips).
func loadGitignore(root string) map[string]bool {
	out := map[string]bool{}
	b, err := os.ReadFile(filepath.Join(root, ".gitignore"))
	if err != nil {
		return out
	}
	for _, ln := range strings.Split(string(b), "\n") {
		ln = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(ln), "/"))
		if ln == "" || strings.HasPrefix(ln, "#") || strings.Contains(ln, "/") || strings.Contains(ln, "*") {
			continue
		}
		out[ln] = true
	}
	return out
}

// codeIndexRoots returns the allowlisted roots for /code/index from
// CODE_INDEX_ROOT (comma-separated). Default is the in-container mounts.
func codeIndexRoots() []string {
	raw := os.Getenv("CODE_INDEX_ROOT")
	if strings.TrimSpace(raw) == "" {
		raw = "/data/ai,/data/repo"
	}
	var roots []string
	for _, r := range strings.Split(raw, ",") {
		if r = strings.TrimSpace(r); r != "" {
			roots = append(roots, r)
		}
	}
	return roots
}

// withinDir reports whether p sits inside root (or is root itself).
func withinDir(root, p string) bool {
	if p == root {
		return true
	}
	rel, err := filepath.Rel(root, p)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// resolveForJail returns the absolute, symlink-resolved form of p,
// falling back to the lexical absolute path when it does not exist
// (e.g. an allowlist root that is not mounted on this host).
func resolveForJail(p string) string {
	abs, err := filepath.Abs(p)
	if err != nil {
		return p
	}
	if rp, err := filepath.EvalSymlinks(abs); err == nil {
		return rp
	}
	return abs
}

// jailCodePath resolves the requested index path and requires it to sit
// inside one of the allowlisted roots. Symlinks are resolved, so
// /allowed/link -> /etc fails closed.
func jailCodePath(path string) (string, error) {
	resolved := resolveForJail(path)
	for _, r := range codeIndexRoots() {
		if withinDir(resolveForJail(r), resolved) {
			return resolved, nil
		}
	}
	return "", fmt.Errorf("path outside indexable roots")
}

// walkRootResolved resolves the walk root once per request for the
// in-walk symlink check.
func walkRootResolved(walkRoot string) string { return resolveForJail(walkRoot) }

func relDot(p, walkRoot string) string {
	if rel, err := filepath.Rel(walkRoot, p); err == nil {
		return rel
	}
	return p
}
