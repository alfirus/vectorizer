package store

import (
	"fmt"
	"strings"
	"time"
)

func conclusionsCollection(workspaceID string) string { return fmt.Sprintf("ws_%s_conclusions", ResolveWorkspaceID(workspaceID)) }

func (s *Store) CreateConclusion(workspaceID, peerID, content string, metadata map[string]interface{}) (string, error) {
	collName := conclusionsCollection(workspaceID)
	coll, err := s.chroma.EnsureCollection(collName, map[string]interface{}{"workspace_id": workspaceID})
	if err != nil { return "", err }
	id := fmt.Sprintf("concl_%d", time.Now().UnixNano())
	meta := map[string]interface{}{"workspace_id": workspaceID, "peer_id": peerID, "created_at": time.Now().UTC().Format(time.RFC3339), "type": "conclusion"}
	for k,v:=range metadata { meta[k]=v }
	emb, err := s.embed.Embed([]string{content})
	if err != nil { return "", err }
	vecs := [][]float32{emb[0].Vector}
	return id, s.chroma.UpsertDocuments(coll.ID, []string{id}, []string{content}, []map[string]interface{}{meta}, vecs)
}

func (s *Store) ListConclusions(workspaceID, peerID string, limit, offset int) ([]map[string]interface{}, error) {
	collName := conclusionsCollection(workspaceID)
	coll, err := s.chroma.GetCollection(collName)
	if err != nil { return []map[string]interface{}{}, nil }
	where := map[string]interface{}{"workspace_id": workspaceID}
	if peerID != "" { where["peer_id"] = peerID }
	return s.chroma.GetDocuments(coll.ID, where, limit, offset)
}

func (s *Store) DeleteConclusion(workspaceID, id string) error {
	collName := conclusionsCollection(workspaceID)
	coll, err := s.chroma.GetCollection(collName)
	if err != nil { return err }
	return s.chroma.DeleteDocuments(coll.ID, []string{id})
}

func (s *Store) GetRepresentation(workspaceID, peerID, sessionID string, maxConclusions int) (string, []map[string]interface{}, error) {
	if maxConclusions<=0 { maxConclusions=25 }
	// If sessionID set, filter; else all
	concls, err := s.ListConclusions(workspaceID, peerID, maxConclusions, 0)
	if err != nil { return "", nil, err }
	// Also pull recent session messages as context
	var parts []string
	for _, c := range concls {
		if doc, ok := c["document"].(string); ok { parts = append(parts, "- "+doc) }
	}
	if sessionID != "" {
		if docs, err2 := s.GetMessages(workspaceID, sessionID, 10, 0); err2==nil {
			for _, d := range docs {
				if doc, ok := d["document"].(string); ok { parts = append(parts, doc) }
			}
		}
	}
	// Trim scope prefix handling
	_ = strings.TrimPrefix
	return strings.Join(parts, "\n"), concls, nil
}
