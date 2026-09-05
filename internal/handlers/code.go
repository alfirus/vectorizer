package handlers

import (
	"crypto/sha256"
	"fmt"
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
// Security: path must be server-local. The server runs on ns539881; the
// agent passes a repo path ON THE SERVER (e.g. /opt/vectorizer). No
// file content crosses the wire — only the path string.
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

// IndexResult is the per-file + aggregate report.
type IndexResult struct {
	WorkspaceID string         `json:"workspace_id"`
	Files       int            `json:"files_indexed"`
	Skipped     int            `json:"files_skipped_unchanged"`
	Symbols     int            `json:"symbols"`
	Edges       int            `json:"edges"`
	Errors      []string       `json:"errors,omitempty"`
	Indexed     []string       `json:"indexed_files,omitempty"`
}

// Index walks path and indexes code files into workspace_id
// (default code_<repo-basename>).
// POST /code/index {"path": "/opt/vectorizer", "workspace_id"?: "...", "session_id"?: "..."}
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
	info, err := os.Stat(req.Path)
	if err != nil || !info.IsDir() {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "path is not a readable directory on the server"})
	}
	ws := req.WorkspaceID
	if ws == "" {
		ws = "code_" + strings.ToLower(filepath.Base(req.Path))
	}
	sess := req.SessionID
	if sess == "" {
		sess = fmt.Sprintf("index-%s", time.Now().UTC().Format("20060102-150405"))
	}

	gitignore := loadGitignore(req.Path)
	res := &IndexResult{WorkspaceID: ws}

	// Phase A: walk + parse.
	type fileJob struct {
		rel  string
		src  string
		hash string
		fs   codeindex.FileSymbols
	}
	var jobs []fileJob
	_ = filepath.WalkDir(req.Path, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		rel, _ := filepath.Rel(req.Path, p)
		if d.IsDir() {
			base := filepath.Base(p)
			if rel != "." && (codeSkipDirs[base] || gitignore[base]) {
				return filepath.SkipDir
			}
			return nil
		}
		if codeindex.LanguageFor(p) == "" {
			return nil
		}
		if gitignore[filepath.Base(p)] {
			return nil
		}
		st, err := d.Info()
		if err != nil || st.Size() > maxCodeFileBytes {
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
	var parsed []codeindex.FileSymbols
	for _, j := range todo {
		parsed = append(parsed, j.fs)
	}
	for _, e := range codeindex.CallEdges(parsed) {
		if err := h.store.AddReasoningEdge(ws, "codeindex", "calls:"+e.Caller,
			[]string{"defines:" + e.Callee}, []string{"file:" + e.File}); err != nil {
			res.Errors = append(res.Errors, "edge "+e.Caller+"->"+e.Callee+": "+err.Error())
			continue
		}
		res.Edges++
	}
	for _, j := range todo {
		for _, imp := range j.fs.Imports {
			if err := h.store.AddReasoningEdge(ws, "codeindex", "file:"+j.rel,
				[]string{"imports:" + imp}, []string{"file:" + j.rel}); err != nil {
				res.Errors = append(res.Errors, "edge imports "+j.rel+": "+err.Error())
				continue
			}
			res.Edges++
		}
	}
	return c.JSON(res)
}

// Symbols lists indexed symbols, optionally filtered by file substring.
// GET /code/symbols?workspace_id=...&file=...
func (h *CodeHandler) Symbols(c *fiber.Ctx) error {
	ws := c.Query("workspace_id")
	if ws == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "workspace_id is required"})
	}
	fileFilter := strings.ToLower(c.Query("file"))
	docs, err := h.store.QueryByMetadata(ws, map[string]interface{}{"source_type": "file"}, 500)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "query failed"})
	}
	type sym struct {
		File     string `json:"file"`
		Language string `json:"language"`
		Symbols  string `json:"symbols"`
	}
	var out []sym
	for _, d := range docs {
		meta, _ := d["metadata"].(map[string]interface{})
		fp, _ := meta["source_path"].(string)
		if fileFilter != "" && !strings.Contains(strings.ToLower(fp), fileFilter) {
			continue
		}
		ent, _ := meta["entities"].(string)
		lang, _ := meta["language"].(string)
		out = append(out, sym{File: fp, Language: lang, Symbols: ent})
		if len(out) >= 200 {
			break
		}
	}
	return c.JSON(fiber.Map{"workspace_id": ws, "count": len(out), "files": out})
}

// Callers answers "what calls X" via CALLS reasoning edges.
// GET /code/callers?workspace_id=...&symbol=...
func (h *CodeHandler) Callers(c *fiber.Ctx) error {
	ws := c.Query("workspace_id")
	symbol := c.Query("symbol")
	if ws == "" || symbol == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "workspace_id and symbol are required"})
	}
	chain, err := h.store.GetReasoningChain(ws, "calls:"+symbol)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "query failed"})
	}
	var callers []string
	for _, e := range chain {
		m, _ := e["metadata"].(map[string]interface{})
		if m == nil {
			continue
		}
		if prem, ok := m["premise_ids"].(string); ok {
			for _, p := range strings.Split(prem, ",") {
				if strings.HasPrefix(p, "defines:") {
					callers = append(callers, strings.TrimPrefix(p, "defines:"))
				}
			}
		}
		if len(callers) == 0 {
			if msg, ok := m["supporting_message_ids"].(string); ok && msg != "" {
				callers = append(callers, msg)
			}
		}
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
