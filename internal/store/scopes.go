package store

import (
	"fmt"
	"strings"
	"time"
)

func scopeCollection(ws string) string { return fmt.Sprintf("ws_%s_scopes", ws) }

func (s *Store) CreateScope(workspaceID, scopeName string, sessionIDs []string) error {
	coll, err := s.chroma.EnsureCollection(scopeCollection(workspaceID), map[string]interface{}{"workspace_id": workspaceID})
	if err != nil { return err }
	meta := map[string]interface{}{"scope_name": scopeName, "workspace_id": workspaceID, "sessions": strings.Join(sessionIDs, ","), "created_at": time.Now().UTC().Format(time.RFC3339)}
	dummy := make([]float32, 768)
	return s.chroma.UpsertDocuments(coll.ID, []string{scopeName}, []string{scopeName}, []map[string]interface{}{meta}, [][]float32{dummy})
}

func (s *Store) ListScopes(workspaceID string) ([]map[string]interface{}, error) {
	coll, err := s.chroma.GetCollection(scopeCollection(workspaceID))
	if err != nil { return []map[string]interface{}{}, nil }
	return s.chroma.GetDocuments(coll.ID, map[string]interface{}{"workspace_id": workspaceID}, 100, 0)
}

func (s *Store) GetScope(workspaceID, scopeName string) (map[string]interface{}, error) {
	coll, err := s.chroma.GetCollection(scopeCollection(workspaceID))
	if err != nil { return nil, err }
	docs, _ := s.chroma.GetDocuments(coll.ID, map[string]interface{}{"scope_name": scopeName}, 1, 0)
	if len(docs)==0 { return nil, fmt.Errorf("not found") }
	return docs[0], nil
}

func (s *Store) AddSessionsToScope(workspaceID, scopeName string, sessionIDs []string) error {
	existing, _ := s.GetScope(workspaceID, scopeName)
	var current []string
	if existing != nil {
		if m, ok := existing["metadata"].(map[string]interface{}); ok {
			if ses, ok := m["sessions"].(string); ok && ses != "" { current = strings.Split(ses, ",") }
		}
	}
	set := map[string]bool{}
	for _, s := range current { set[s]=true }
	for _, s := range sessionIDs { set[s]=true }
	var merged []string
	for k := range set { merged = append(merged, k) }
	return s.CreateScope(workspaceID, scopeName, merged)
}

func (s *Store) RemoveSessionFromScope(workspaceID, scopeName, sessionID string) error {
	existing, err := s.GetScope(workspaceID, scopeName)
	if err != nil { return err }
	m, _ := existing["metadata"].(map[string]interface{})
	sesStr, _ := m["sessions"].(string)
	parts := strings.Split(sesStr, ",")
	var kept []string
	for _, p := range parts { if p != sessionID && p != "" { kept = append(kept, p) } }
	return s.CreateScope(workspaceID, scopeName, kept)
}

func (s *Store) GetScopeSessions(workspaceID, scopeName string) ([]string, error) {
	sc, err := s.GetScope(workspaceID, scopeName)
	if err != nil { return nil, err }
	m, _ := sc["metadata"].(map[string]interface{})
	sesStr, _ := m["sessions"].(string)
	if sesStr == "" { return []string{}, nil }
	return strings.Split(sesStr, ","), nil
}
