package store

import (
	"crypto/rand"
	"encoding/hex"
	"math"
	"sort"
	"sync"

	"github.com/alfirus/vectorizer/internal/models"
)

type APIKey struct {
	ID          string `json:"id"`
	WorkspaceID string `json:"workspace_id"`
	Prefix      string `json:"prefix"`
	CreatedAt   string `json:"created_at"`
}

var (
	keyStore = sync.Map{}
)

func GenerateAPIKey(workspaceID string) *APIKey {
	b := make([]byte, 16)
	rand.Read(b)
	id := "sk_" + hex.EncodeToString(b[:8])
	prefix := id[:8]
	k := &APIKey{ID: id, WorkspaceID: workspaceID, Prefix: prefix}
	keyStore.Store(id, k)
	return k
}

func ListKeys(workspaceID string) []*APIKey {
	var out []*APIKey
	keyStore.Range(func(k, v interface{}) bool {
		apiKey := v.(*APIKey)
		if workspaceID == "" || apiKey.WorkspaceID == workspaceID {
			out = append(out, apiKey)
		}
		return true
	})
	return out
}

func (s *Store) DeleteWorkspace(workspaceID string) error {
	// Delete all collections with prefix ws_<id>*
	colls, _ := s.chroma.ListCollections()
	for _, c := range colls {
		if len(c.Name) >= len("ws_"+workspaceID) && c.Name[:len("ws_"+workspaceID)] == "ws_"+workspaceID {
			_ = s.chroma.DeleteCollection(c.ID)
		}
	}
	return nil
}

func (s *Store) UpdateWorkspace(workspaceID string, metadata map[string]interface{}) error {
	collName := s.GetCollectionName(workspaceID)
	_, err := s.chroma.EnsureCollection(collName, metadata)
	return err
}

func (s *Store) SearchWorkspace(workspaceID, query string, n int) ([]models.SearchResult, error) {
	return s.Search(query, workspaceID, "", "", n)
}

func (s *Store) QueryConclusions(workspaceID, query string, n int) ([]map[string]interface{}, error) {
	// semantic search over conclusions collection (+ shared _global scope —
	// same rule as Search: scoped queries also see global conclusions).
	colls := []string{conclusionsCollection(workspaceID)}
	if workspaceID != "" && workspaceID != "_global" {
		colls = append(colls, conclusionsCollection("_global"))
	}
	emb, err := s.embed.EmbedSingle(query)
	if err != nil { return nil, err }
	var out []map[string]interface{}
	for _, collName := range colls {
		coll, err := s.chroma.GetCollection(collName)
		if err != nil { continue } // missing collection = no global facts yet
		results, err := s.chroma.Query(coll.ID, [][]float32{emb}, n, nil, []string{"documents","metadatas","distances"})
		if err != nil { continue }
		for _, r := range results {
			score := math.Max(0, 1-float64(r.Distance))
			out = append(out, map[string]interface{}{"id": r.ID, "document": r.Document, "metadata": r.Metadata, "distance": r.Distance, "score": score})
		}
	}
	// Global hits merge with local ones — best score wins, then truncate.
	sort.Slice(out, func(i, j int) bool { return scoreOf(out[i]) > scoreOf(out[j]) })
	seen := map[string]bool{}
	deduped := out[:0]
	for _, m := range out {
		id, _ := m["id"].(string)
		if seen[id] { continue }
		seen[id] = true
		deduped = append(deduped, m)
	}
	if len(deduped) > n { deduped = deduped[:n] }
	return deduped, nil
}

func scoreOf(m map[string]interface{}) float64 {
	s, _ := m["score"].(float64)
	return s
}
