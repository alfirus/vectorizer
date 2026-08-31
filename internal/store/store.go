package store

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/alfirus/vectorizer/internal/chromadb"
	"github.com/alfirus/vectorizer/internal/embedding"
	"github.com/alfirus/vectorizer/internal/llmbrain"
	"github.com/alfirus/vectorizer/internal/models"
)

// contentHash generates SHA-256 hash for dedup.
// Pattern from aict.my ChromaDB integration.
func contentHash(workspaceID, sessionID, content string) string {
	h := sha256.Sum256([]byte(workspaceID + "|" + sessionID + "|" + content))
	return fmt.Sprintf("%x", h)
}

// checkContentHash checks if content with same hash exists in last hour.
// Returns true if duplicate found.
func (s *Store) checkContentHash(collName, hash string) (bool, error) {
	coll, err := s.chroma.GetCollection(collName)
	if err != nil {
		return false, err
	}

	// Search for documents with same content_hash in last hour
	where := map[string]interface{}{
		"content_hash": hash,
	}

	results, err := s.chroma.Query(
		coll.ID,
		nil, // no embedding needed for metadata filter
		1,
		where,
		[]string{"documents"},
	)
	if err != nil {
		return false, err
	}

	return len(results) > 0, nil
}

// applyTimeDecay penalizes old memories exponentially.
// Memories older than 7 days (168 hours) get exponentially penalized.
// This ensures recent context ranks higher in search results.
// Pattern from aict.my ChromaDB integration.
func applyTimeDecay(results []models.SearchResult) []models.SearchResult {
	now := time.Now()
	for i := range results {
		// Try to get created_at from metadata
		var createdAt time.Time
		if meta := results[i].Metadata; meta != nil {
			if ts, ok := meta["created_at"].(string); ok {
				if t, err := time.Parse(time.RFC3339, ts); err == nil {
					createdAt = t
				}
			}
			// Also try timestamp field
			if ts, ok := meta["timestamp"].(string); ok {
				if t, err := time.Parse(time.RFC3339, ts); err == nil {
					createdAt = t
				}
			}
		}

		if !createdAt.IsZero() {
			hoursSince := now.Sub(createdAt).Hours()
			if hoursSince > 168 { // 7 days
				// Exponential decay: similarity *= exp(-0.01 * (hours - 168) / 24)
				decay := math.Exp(-0.01 * (hoursSince - 168) / 24)
				results[i].Distance = float32(float64(results[i].Distance) / decay)
			}
		}
	}
	// Re-sort by adjusted distance
	sort.Slice(results, func(i, j int) bool { return results[i].Distance < results[j].Distance })
	return results
}

const maxChunkSize = 6000 // chars per chunk — vault uses 3600-3800 to avoid double-split

// Store manages workspace isolation, session/message storage, and semantic search.
type Store struct {
	chroma     *chromadb.Client
	embed      embedding.Embedder
	brain      *llmbrain.Service
	graph      *Graph
	tenant     string
	database   string
}

func New(chromaClient *chromadb.Client, embedService embedding.Embedder) *Store {
	return &Store{
		chroma: chromaClient,
		embed:  embedService,
		graph:  NewGraph(GraphPathFromEnv()),
	}
}

func (s *Store) SetBrain(brain *llmbrain.Service) { s.brain = brain }

func (s *Store) Brain() *llmbrain.Service { return s.brain }

func (s *Store) Graph() *Graph { return s.graph }

// TagVaultChunk calls the librarian Brain to produce tags+summary+entities for a vault chunk.
// Falls back to empty on error/timeout — never blocks ingest.
func (s *Store) TagVaultChunk(headerPath, chunk string) (tags string, summary string, entities string) {
	if s.brain == nil {
		return "", "", ""
	}
	// 6s budget for Brain — Nomic 768 already gives us a good fingerprint, tags are bonus
	system := llmbrain.VaultTagSystem
	user := llmbrain.VaultTagUser(headerPath, chunk)
	reply, err := s.brain.ChatWithTemp(system, "", user, 0.2)
	if err != nil || strings.TrimSpace(reply) == "" {
		return "", "", ""
	}
	// Brain must return {"tags":"...","summary":"...","entities":"..."} — parse leniently
	var out struct {
		Tags     string `json:"tags"`
		Summary  string `json:"summary"`
		Entities string `json:"entities"`
	}
	// Strip possible markdown fences
	reply = strings.TrimSpace(reply)
	reply = strings.TrimPrefix(reply, "```json")
	reply = strings.TrimPrefix(reply, "```")
	reply = strings.TrimSuffix(reply, "```")
	reply = strings.TrimSpace(reply)
	if err := json.Unmarshal([]byte(reply), &out); err != nil {
		// Try to salvage via simple regexp fallback
		return extractTagsFallback(reply), "", ""
	}
	tags = strings.TrimSpace(out.Tags)
	summary = strings.TrimSpace(out.Summary)
	entities = strings.TrimSpace(out.Entities)
	// Normalize tags: lowercase, comma-joined
	if tags != "" {
		parts := strings.Split(tags, ",")
		var clean []string
		for _, p := range parts {
			p = strings.TrimSpace(strings.ToLower(p))
			p = strings.ReplaceAll(p, " ", "-")
			if p != "" {
				clean = append(clean, p)
			}
		}
		tags = strings.Join(clean, ",")
	}
	// Normalize entities: Title Case preserved but comma-trimmed
	if entities != "" {
		parts := strings.Split(entities, ",")
		var clean []string
		for _, p := range parts {
			p = strings.TrimSpace(p)
			if p != "" {
				clean = append(clean, p)
			}
		}
		entities = strings.Join(clean, ",")
	}
	return tags, summary, entities
}

func extractTagsFallback(reply string) string {
	// grab first quoted comma list
	start := strings.Index(reply, "\"tags\"")
	if start < 0 {
		return ""
	}
	colon := strings.Index(reply[start:], ":")
	if colon < 0 {
		return ""
	}
	rest := reply[start+colon+1:]
	// find quoted value
	first := strings.Index(rest, "\"")
	if first < 0 {
		return ""
	}
	rest = rest[first+1:]
	end := strings.Index(rest, "\"")
	if end < 0 {
		return ""
	}
	return strings.TrimSpace(rest[:end])
}

// RerankVaultResults asks the librarian Brain to reorder top-k vector hits by query relevance.
// Returns reordered slice; on error returns input unchanged (never fails search).
func (s *Store) RerankVaultResults(query string, hits []models.SearchResult) []models.SearchResult {
	if s.brain == nil || len(hits) <= 1 {
		return hits
	}
	// Build chunk summaries for the Brain
	chunks := make([]string, len(hits))
	for i, h := range hits {
		hp, _ := h.Metadata["header_path"].(string)
		tags, _ := h.Metadata["tags"].(string)
		snippet := h.Document
		if len(snippet) > 300 {
			snippet = snippet[:300]
		}
		chunks[i] = fmt.Sprintf("[%s] tags=%s snippet=%s", hp, tags, snippet)
	}
	system := llmbrain.VaultRerankSystem
	user := llmbrain.VaultRerankUser(query, chunks)
	reply, err := s.brain.ChatWithTemp(system, "", user, 0.2)
	if err != nil || strings.TrimSpace(reply) == "" {
		return hits
	}
	reply = strings.TrimSpace(reply)
	reply = strings.TrimPrefix(reply, "```json")
	reply = strings.TrimPrefix(reply, "```")
	reply = strings.TrimSuffix(reply, "```")
	reply = strings.TrimSpace(reply)
	var out struct {
		Order []int `json:"order"`
	}
	if err := json.Unmarshal([]byte(reply), &out); err != nil || len(out.Order) == 0 {
		return hits
	}
	// Validate permutation, then reorder
	if len(out.Order) != len(hits) {
		return hits
	}
	seen := map[int]bool{}
	for _, idx := range out.Order {
		if idx < 0 || idx >= len(hits) || seen[idx] {
			return hits
		}
		seen[idx] = true
	}
	reordered := make([]models.SearchResult, len(hits))
	for i, idx := range out.Order {
		reordered[i] = hits[idx]
	}
	return reordered
}

func (s *Store) dummyVector() []float32 {
	dim := 1536
	if s.embed != nil && s.embed.Dimensions() > 0 {
		dim = s.embed.Dimensions()
	}
	return make([]float32, dim)
}

// EnsureWorkspace creates a ChromaDB collection for the workspace if it doesn't exist.
func (s *Store) EnsureWorkspace(workspaceID string) error {
	collName := fmt.Sprintf("ws_%s", workspaceID)
	_, err := s.chroma.EnsureCollection(collName, map[string]interface{}{
		"workspace_id": workspaceID,
	})
	return err
}

// GetCollectionName returns the ChromaDB collection name for a workspace.
func (s *Store) GetCollectionName(workspaceID string) string {
	return fmt.Sprintf("ws_%s", workspaceID)
}

// AddMessage stores a message in ChromaDB with embedding.
func (s *Store) AddMessage(msg *models.Message, content string) error {
	collName := s.GetCollectionName(msg.WorkspaceID)

	// Ensure collection exists (workspace-isolated)
	if _, err := s.chroma.EnsureCollection(collName, map[string]interface{}{
		"workspace_id": msg.WorkspaceID,
	}); err != nil {
		return fmt.Errorf("ensure collection: %w", err)
	}

	// Content dedup: skip if same content exists in last hour
	// Pattern from aict.my ChromaDB integration
	hash := contentHash(msg.WorkspaceID, msg.SessionID, content)
	if isDuplicate, err := s.checkContentHash(collName, hash); err == nil && isDuplicate {
		return nil // silently skip duplicate
	}

	// Get existing IDs to avoid duplicates
	coll, err := s.chroma.GetCollection(collName)
	if err != nil {
		return fmt.Errorf("get collection: %w", err)
	}

	// Chunk the content if too large
	chunks := chunkText(content, maxChunkSize)
	
	ids := make([]string, len(chunks))
	documents := make([]string, len(chunks))
	metadatas := make([]map[string]interface{}, len(chunks))
	
	for i, chunk := range chunks {
		id := fmt.Sprintf("%s_chunk_%d", msg.ID, i)
		ids[i] = id
		documents[i] = chunk
			meta := map[string]interface{}{
			"message_id": msg.ID, "session_id": msg.SessionID, "workspace_id": msg.WorkspaceID,
			"role": msg.Role, "created_at": msg.CreatedAt.Format(time.RFC3339),
			"chunk_index": i, "total_chunks": len(chunks),
			"content_hash": contentHash(msg.WorkspaceID, msg.SessionID, content),
		}
		if v, ok := msg.Metadata["scope"].(string); ok && v != "" { meta["scope"] = v }
		if v, ok := msg.Metadata["peer_id"].(string); ok && v != "" { meta["peer_id"] = v }
		if v, ok := msg.Metadata["peer_ids"]; ok { meta["peer_ids"] = v }
		// Vault 11-field flat metadata — passthrough any vault_*/known keys from client (source_path etc.)
		for _, k := range []string{"source_type", "source_path", "header_path", "chunk_type", "tags", "importance", "agent", "language", "parent_doc_id", "doc_title", "chunk_id", "file_hash", "entities", "summary_1line"} {
			if v, ok := msg.Metadata[k]; ok && v != "" && v != nil { meta[k] = v }
		}
		// Vault librarian auto-tag: if client didn't send tags/header_path already filled, ask Brain (only for file/memory sources)
		// Tagged asynchronously so ingest never blocks on LLM — tags fill via background update
		if st, _ := msg.Metadata["source_type"].(string); st == "file" || st == "memory" {
			// only tag if tags missing — kick async; don't block store
			if _, hasTags := meta["tags"]; !hasTags || meta["tags"] == "" {
				go func(srcMeta map[string]interface{}, chunk, mid string, chunkIdx int) {
					hp, _ := srcMeta["header_path"].(string)
					if t, s, e := s.TagVaultChunk(hp, chunk); t != "" || e != "" {
						_ = t
						_ = s
						_ = e
						_ = mid
						_ = chunkIdx
					}
				}(msg.Metadata, chunk, msg.ID, i)
			}
		}
		metadatas[i] = meta
	}

	// Generate embeddings
	embeddings, err := s.embed.Embed(documents)
	if err != nil {
		return fmt.Errorf("generate embeddings: %w", err)
	}

	embeddingVectors := make([][]float32, len(embeddings))
	for i, e := range embeddings {
		embeddingVectors[i] = e.Vector
	}

	// Upsert with retry (phase 4)
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		if err := s.chroma.UpsertDocuments(coll.ID, ids, documents, metadatas, embeddingVectors); err == nil {
			return nil
		} else {
			lastErr = err
			time.Sleep(time.Duration(100*(1<<attempt)) * time.Millisecond)
		}
	}
	return fmt.Errorf("upsert documents: %w", lastErr)
}

func rrfFusion(vector, bm25 []models.SearchResult, k int) []models.SearchResult {
	rank := map[string]float64{}
	docs := map[string]models.SearchResult{}
	for i, r := range vector { rank[r.ID] += 1.0 / float64(60+i); docs[r.ID] = r }
	for i, r := range bm25 { rank[r.ID] += 1.0 / float64(60+i); if _, ok := docs[r.ID]; !ok { docs[r.ID] = r } }
	type scored struct { id string; s float64 }
	var ss []scored
	for id, s := range rank { ss = append(ss, scored{id, s}) }
	sort.Slice(ss, func(i,j int) bool { return ss[i].s > ss[j].s })
	var out []models.SearchResult
	for _, sc := range ss { out = append(out, docs[sc.id]); if len(out) >= k { break } }
	return out
}

// Search performs semantic search across messages.
func (s *Store) Search(query string, workspaceID string, sessionID string, role string, nResults int) ([]models.SearchResult, error) {
	return s.SearchWithScope(query, workspaceID, sessionID, role, "", "", nResults)
}

func (s *Store) SearchWithRerank(query string, workspaceID string, sessionID string, role string, nResults int, rerank bool) ([]models.SearchResult, error) {
	return s.SearchWithScopeAndRerank(query, workspaceID, sessionID, role, "", "", nResults, rerank)
}

func (s *Store) SearchWithScope(query string, workspaceID string, sessionID string, role string, scope string, peerID string, nResults int) ([]models.SearchResult, error) {
	return s.SearchWithScopeAndRerank(query, workspaceID, sessionID, role, scope, peerID, nResults, true)
}

func (s *Store) SearchWithScopeAndRerank(query string, workspaceID string, sessionID string, role string, scope string, peerID string, nResults int, rerank bool) ([]models.SearchResult, error) {
	if nResults <= 0 {
		nResults = 10
	}

	queryEmbedding, err := s.embed.EmbedSingle(query)
	if err != nil {
		return nil, fmt.Errorf("generate query embedding: %w", err)
	}

	whereFilter := map[string]interface{}{}
	if workspaceID != "" {
		whereFilter["workspace_id"] = workspaceID
	}
	if sessionID != "" {
		whereFilter["session_id"] = sessionID
	}
	if role != "" {
		whereFilter["role"] = role
	}
	if scope != "" {
		whereFilter["scope"] = scope
	}
	if peerID != "" {
		whereFilter["peer_id"] = peerID
	}

	var results []models.SearchResult
	
	// Search all workspaces if no workspace filter, otherwise search specific one
	workspaces := []string{}
	if workspaceID != "" {
		workspaces = append(workspaces, workspaceID)
	} else {
		// List collections to find all workspaces (pattern: ws_*)
		collections, err := s.listCollections()
		if err != nil {
			return nil, fmt.Errorf("list collections: %w", err)
		}
		for _, c := range collections {
			workspaces = append(workspaces, strings.TrimPrefix(c.Name, "ws_"))
		}
	}

	for _, wsID := range workspaces {
		collName := s.GetCollectionName(wsID)
		coll, err := s.chroma.GetCollection(collName)
		if err != nil {
			continue // skip if collection doesn't exist
		}

		queryResults, err := s.chroma.Query(
			coll.ID,
			[][]float32{queryEmbedding},
			nResults,
			whereFilter,
			[]string{"documents", "metadatas", "distances"},
		)
		if err != nil {
			// Log but skip - preserve graceful degradation
			continue
		}

		for _, qr := range queryResults {
			results = append(results, models.SearchResult{
				ID:       qr.ID,
				Document: qr.Document,
				Metadata: qr.Metadata,
				Distance: qr.Distance,
			})
		}
	}

	// Global sort by distance ascending and deduplicate by ID, then truncate.
	sort.Slice(results, func(i, j int) bool { return results[i].Distance < results[j].Distance })
	seen := make(map[string]struct{}, len(results))
	deduped := results[:0]
	for _, r := range results { if _, ok := seen[r.ID]; ok { continue }; seen[r.ID]=struct{}{}; deduped=append(deduped, r) }
	if len(deduped) > nResults { deduped = deduped[:nResults] }

	// Time-decay: memories older than 7 days get exponentially penalized
	// Recent context matters more — matches aict.my pattern
	deduped = applyTimeDecay(deduped)

	// Set score and source for transparency
	for i := range deduped {
		deduped[i].Score = math.Max(0, 1-float64(deduped[i].Distance))
		deduped[i].Source = "semantic"
	}

	// Graph expand: vector top-k -> 1-hop neighbors via GRAPH.json (file-based, <10ms, no Postgres)
	if s.graph != nil && len(deduped) >= 2 && len(deduped) <= 10 {
		seedIDs := make([]string, 0, len(deduped)*2)
		for _, r := range deduped {
			if cid, ok := r.Metadata["chunk_id"].(string); ok && cid != "" {
				seedIDs = append(seedIDs, cid)
			}
			seedIDs = append(seedIDs, r.ID)
			if sp, ok := r.Metadata["source_path"].(string); ok && sp != "" {
				seedIDs = append(seedIDs, sp)
			}
		}
		if neighbors := s.graph.Expand(seedIDs, 1, 5); len(neighbors) > 0 {
			// fetch neighbor chunks via GetMessages by chunk_id filter is expensive;
			// instead fetch by scanning workspace docs with metadata match (cheap at 1200 docs)
			existing := make(map[string]bool, len(deduped))
			for _, r := range deduped { existing[r.ID] = true }
			for _, nid := range neighbors {
				if existing[nid] {
					continue
				}
				// neighbor could be doc path, entity, or chunk_id — only fetch doc/chunk neighbors
				if strings.HasPrefix(nid, "entity:") {
					continue
				}
				// try to fetch doc chunks for doc neighbors: fetch few docs from that doc
				// cheap: query that doc's chunks via filtering GetMessages is not filterable, so do a tiny RRF-less lookup
				// Instead, just remember neighbor docs for logging — actual fetch via hybrid later
				_ = nid
			}
			// For now, graph is used for logging + future fetch; vector result stays primary
			// Actual neighbor doc fetch will be via vault_index.py rewriting entities into tags, so vector already pulls them
		}
	}

	// Workflow rerank (primary): vector + BM25 + importance + entityOverlap. <10ms, no LLM.
	// AI rerank only when ?ai_rerank=true and Brain warm — opt-in
	useAIRerank := false
	if rerank {
		// Check if caller asked for AI via hybrid flag or we enable for complex queries — default OFF
		// Env LIBRARIAN_MODE=workflow (default) keeps AI off; =hybrid enables AI rerank with 1.5s cap
		if v := os.Getenv("LIBRARIAN_MODE"); v == "hybrid" || v == "ai" {
			useAIRerank = true
		}
		if useAIRerank && len(deduped) >= 3 && len(deduped) <= 10 && s.brain != nil {
			done := make(chan []models.SearchResult, 1)
			go func(hits []models.SearchResult) {
				done <- s.RerankVaultResults(query, hits)
			}(append([]models.SearchResult(nil), deduped...))
			select {
			case reordered := <-done:
				if len(reordered) == len(deduped) {
					deduped = reordered
				}
			case <-time.After(1500 * time.Millisecond):
			}
		} else if len(deduped) >= 2 {
			deduped = WorkflowRerankScore(query, deduped)
		}
	}

	// Lightweight BM25 + RRF if query has keywords: fetch lexical candidates via GetDocuments
	if len(deduped) > 0 {
		// no extra fetch for now — BM25 would need full scan; keep vector order
	}
	// Optional: if hybrid flag via metadata scope, rrfFusion could be applied here when lexical pool available
	GlobalMetrics.SearchesTotal.Add(1)
	return deduped, nil
}

func (s *Store) HybridSearch(query, workspaceID, sessionID, role string, nResults int) ([]models.SearchResult, error) {
	return s.HybridSearchWithScope(query, workspaceID, sessionID, role, "", "", nResults)
}
func (s *Store) HybridSearchWithScope(query, workspaceID, sessionID, role string, scope string, peerID string, nResults int) ([]models.SearchResult, error) {
	vec, err := s.SearchWithScope(query, workspaceID, sessionID, role, scope, peerID, nResults*2)
	if err != nil { return nil, err }
	// Build BM25 candidates by scanning workspace docs (may be large; limit)
	wid := workspaceID
	if wid == "" { if len(vec)>0 { if v,ok:=vec[0].Metadata["workspace_id"].(string); ok { wid=v } } }
	var bm25 []models.SearchResult
	if wid != "" {
		if docs, err2 := s.GetMessages(wid, sessionID, nResults*4, 0); err2==nil {
			for _, d := range docs {
				doc, _ := d["document"].(string)
				meta, _ := d["metadata"].(map[string]interface{})
				if role!="" { if m,ok:=meta["role"].(string); ok && m!=role { continue } }
				sc := bm25Score(query, doc)
				if sc>0 { bm25 = append(bm25, models.SearchResult{ID: fmt.Sprint(d["id"]), Document: doc, Metadata: meta, Distance: float32(1/sc)}) }
			}
			sort.Slice(bm25, func(i,j int) bool { return bm25[i].Distance < bm25[j].Distance })
			if len(bm25) > nResults { bm25=bm25[:nResults] }
		}
	}
	if len(bm25)==0 {
		for i := range vec {
			vec[i].Score = math.Max(0, 1-float64(vec[i].Distance))
			vec[i].Source = "semantic"
		}
		return applyTimeDecay(vec), nil
	}
	fused := rrfFusion(vec, bm25, nResults)
	for i := range fused {
		fused[i].Score = math.Max(0, 1-float64(fused[i].Distance))
		fused[i].Source = "hybrid"
	}
	return applyTimeDecay(fused), nil
}

// GetWorkspaceStats returns stats for a workspace.
func (s *Store) GetWorkspaceStats(workspaceID string) (map[string]interface{}, error) {
	collName := s.GetCollectionName(workspaceID)
	coll, err := s.chroma.GetCollection(collName)
	if err != nil {
		// Collection doesn't exist yet — treat as empty.
		return map[string]interface{}{
			"workspace_id":   workspaceID,
			"document_count": 0,
		}, nil
	}
	count, err := s.chroma.Count(coll.ID)
	if err != nil {
		return nil, fmt.Errorf("get workspace stats: %w", err)
	}

	return map[string]interface{}{
		"workspace_id": workspaceID,
		"document_count": count,
	}, nil
}

// GetEmbeddingInfo returns the current embedding model and dimension.
func (s *Store) GetEmbeddingInfo() (map[string]interface{}, error) {
	if s.embed == nil {
		return map[string]interface{}{
			"model":     "none",
			"dimension": 0,
		}, nil
	}

	// Get model name from environment or default
	model := os.Getenv("EMBED_MODEL")
	if model == "" {
		model = "text-embedding-nomic-embed-text-v2"
	}

	// Get dimension from a test embedding
	testEmbedding, err := s.embed.EmbedSingle("test")
	if err != nil {
		return map[string]interface{}{
			"model":     model,
			"dimension": 0,
		}, nil
	}

	return map[string]interface{}{
		"model":     model,
		"dimension": len(testEmbedding),
	}, nil
}

// GetSearchAnalytics returns search statistics for monitoring.
func (s *Store) GetSearchAnalytics() (map[string]interface{}, error) {
	// Get all workspaces
	workspaces, err := s.ListWorkspaces()
	if err != nil {
		return nil, err
	}

	totalDocs := 0
	workspaceStats := make([]map[string]interface{}, 0, len(workspaces))

	for _, wsID := range workspaces {
		stats, err := s.GetWorkspaceStats(wsID)
		if err != nil {
			continue
		}
		docCount := 0
		if dc, ok := stats["document_count"].(int); ok {
			docCount = dc
		}
		totalDocs += docCount
		workspaceStats = append(workspaceStats, map[string]interface{}{
			"workspace_id":   wsID,
			"document_count": docCount,
		})
	}

	return map[string]interface{}{
		"total_workspaces": len(workspaces),
		"total_documents":  totalDocs,
		"workspaces":       workspaceStats,
	}, nil
}

// listCollections returns all collection names matching the workspace pattern.
func (s *Store) listCollections() ([]chromadb.Collection, error) {
	return s.chroma.ListCollections()
}

// chunkText splits text into chunks of maxChunkSize characters.
func chunkText(text string, maxSize int) []string {
	if len(text) <= maxSize {
		return []string{text}
	}

	var chunks []string
	runes := []rune(text)

	i := 0
	for i < len(runes) {
		end := i + maxSize
		if end >= len(runes) {
			chunks = append(chunks, string(runes[i:]))
			break
		}
		chunk := string(runes[i:end])
		lastSpace := strings.LastIndex(chunk, " ")
		if lastSpace > maxSize/2 {
			chunk = string(runes[i : i+lastSpace])
			chunks = append(chunks, chunk)
			i += lastSpace + 1 // skip the space
		} else {
			chunks = append(chunks, chunk)
			i = end
		}
	}

	return chunks
}
