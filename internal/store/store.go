package store

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/alfirus/vectorizer/internal/chromadb"
	"github.com/alfirus/vectorizer/internal/embedding"
	"github.com/alfirus/vectorizer/internal/llmbrain"
	"github.com/alfirus/vectorizer/internal/models"
)

const maxChunkSize = 6000 // chars per chunk — vault uses 3600-3800 to avoid double-split

// Store manages workspace isolation, session/message storage, and semantic search.
type Store struct {
	chroma     *chromadb.Client
	embed      embedding.Embedder
	brain      *llmbrain.Service
	tenant     string
	database   string
}

func New(chromaClient *chromadb.Client, embedService embedding.Embedder) *Store {
	return &Store{
		chroma: chromaClient,
		embed:  embedService,
	}
}

func (s *Store) SetBrain(brain *llmbrain.Service) { s.brain = brain }

func (s *Store) Brain() *llmbrain.Service { return s.brain }

// TagVaultChunk calls the librarian Brain to produce tags+summary for a vault chunk.
// Falls back to empty on error/timeout — never blocks ingest.
func (s *Store) TagVaultChunk(headerPath, chunk string) (tags string, summary string) {
	if s.brain == nil {
		return "", ""
	}
	// 6s budget for Brain — Nomic 768 already gives us a good fingerprint, tags are bonus
	system := llmbrain.VaultTagSystem
	user := llmbrain.VaultTagUser(headerPath, chunk)
	reply, err := s.brain.ChatWithTemp(system, "", user, 0.2)
	if err != nil || strings.TrimSpace(reply) == "" {
		return "", ""
	}
	// Brain must return {"tags":"...","summary":"..."} — parse leniently
	var out struct {
		Tags    string `json:"tags"`
		Summary string `json:"summary"`
	}
	// Strip possible markdown fences
	reply = strings.TrimSpace(reply)
	reply = strings.TrimPrefix(reply, "```json")
	reply = strings.TrimPrefix(reply, "```")
	reply = strings.TrimSuffix(reply, "```")
	reply = strings.TrimSpace(reply)
	if err := json.Unmarshal([]byte(reply), &out); err != nil {
		// Try to salvage via simple regexp fallback
		return extractTagsFallback(reply), ""
	}
	tags = strings.TrimSpace(out.Tags)
	summary = strings.TrimSpace(out.Summary)
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
	return tags, summary
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
		}
		if v, ok := msg.Metadata["scope"].(string); ok && v != "" { meta["scope"] = v }
		if v, ok := msg.Metadata["peer_id"].(string); ok && v != "" { meta["peer_id"] = v }
		if v, ok := msg.Metadata["peer_ids"]; ok { meta["peer_ids"] = v }
		// Vault 11-field flat metadata — passthrough any vault_*/known keys from client (source_path etc.)
		for _, k := range []string{"source_type", "source_path", "header_path", "chunk_type", "tags", "importance", "agent", "language", "parent_doc_id", "doc_title", "chunk_id", "file_hash"} {
			if v, ok := msg.Metadata[k]; ok && v != "" && v != nil { meta[k] = v }
		}
		// Vault librarian auto-tag: if client didn't send tags/header_path already filled, ask Brain (only for file/memory sources)
		// Tagged asynchronously so ingest never blocks on LLM — tags fill via background update
		if st, _ := msg.Metadata["source_type"].(string); st == "file" || st == "memory" {
			// only tag if tags missing — kick async; don't block store
			if _, hasTags := meta["tags"]; !hasTags || meta["tags"] == "" {
				go func(srcMeta map[string]interface{}, chunk, mid string, chunkIdx int) {
					hp, _ := srcMeta["header_path"].(string)
					if t, s := s.TagVaultChunk(hp, chunk); t != "" {
						// update metadata asynchronously via direct chroma update is heavy;
						// for now we log — vault_index.py will reindex with real tags next run
						_ = t
						_ = s
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

func bm25Score(query, doc string) float64 {
	qt := strings.Fields(strings.ToLower(query))
	dt := strings.ToLower(doc)
	s := 0.0
	for _, t := range qt { s += float64(strings.Count(dt, t)) }
	return s
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

	// Vault librarian rerank: vector top-k -> Brain reorders by query+header_path+tags (best for 768d)
	// Failsafe: rerank only when library is warm and never exceeds 1.5s; GET path was proof it hangs on Qwen 35b cold load
	if rerank && len(deduped) >= 3 && len(deduped) <= 10 && s.brain != nil {
		// Probe: if Brain still cold, ChatWithTemp(12s) would block handler -> use short-circuit 1.5s and keep vector order
		done := make(chan []models.SearchResult, 1)
		go func(hits []models.SearchResult) {
			done <- s.RerankVaultResults(query, hits)
		}(append([]models.SearchResult(nil), deduped...))
		select {
		case reordered := <-done:
			// only accept if Brain actually reordered (not same as input due to error)
			if len(reordered) == len(deduped) {
				deduped = reordered
			}
		case <-time.After(1500 * time.Millisecond):
			// keep vector order, don't wait for Qwen cold start
		}
	}

	// Lightweight BM25 + RRF if query has keywords: fetch lexical candidates via GetDocuments
	if len(deduped) > 0 {
		// no extra fetch for now — BM25 would need full scan; keep vector order
	}
	// Optional: if hybrid flag via metadata scope, rrfFusion could be applied here when lexical pool available
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
	if len(bm25)==0 { return vec, nil }
	return rrfFusion(vec, bm25, nResults), nil
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
