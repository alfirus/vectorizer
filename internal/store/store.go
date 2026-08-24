package store

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/alfirus/vectorizer/internal/chromadb"
	"github.com/alfirus/vectorizer/internal/embedding"
	"github.com/alfirus/vectorizer/internal/models"
)

const maxChunkSize = 4000 // chars per chunk

// Store manages workspace isolation, session/message storage, and semantic search.
type Store struct {
	chroma     *chromadb.Client
	embed      *embedding.Service
	tenant     string
	database   string
}

func New(chromaClient *chromadb.Client, embedService *embedding.Service) *Store {
	return &Store{
		chroma: chromaClient,
		embed:  embedService,
	}
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
	
	// Ensure collection exists
	if _, err := s.chroma.EnsureCollection(collName, map[string]interface{}{
		"workspace_id": msg.WorkspaceID,
		"session_id":   msg.SessionID,
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
		metadatas[i] = map[string]interface{}{
			"message_id":  msg.ID,
			"session_id":  msg.SessionID,
			"workspace_id": msg.WorkspaceID,
			"role":        msg.Role,
			"created_at":  msg.CreatedAt.Format(time.RFC3339),
			"chunk_index": i,
			"total_chunks": len(chunks),
		}
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

	// Upsert to ChromaDB
	if err := s.chroma.UpsertDocuments(coll.ID, ids, documents, metadatas, embeddingVectors); err != nil {
		return fmt.Errorf("upsert documents: %w", err)
	}

	return nil
}

// Search performs semantic search across messages.
func (s *Store) Search(query string, workspaceID string, sessionID string, role string, nResults int) ([]models.SearchResult, error) {
	if nResults <= 0 {
		nResults = 10
	}

	// Generate query embedding
	queryEmbedding, err := s.embed.EmbedSingle(query)
	if err != nil {
		return nil, fmt.Errorf("generate query embedding: %w", err)
	}

	// Build where filter
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
			continue // skip on error
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
	for _, r := range results {
		if _, ok := seen[r.ID]; ok {
			continue
		}
		seen[r.ID] = struct{}{}
		deduped = append(deduped, r)
	}
	if len(deduped) > nResults {
		deduped = deduped[:nResults]
	}
	return deduped, nil
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
