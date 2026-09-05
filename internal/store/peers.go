package store

import (
	"fmt"
	"strings"
	"time"
)

func peerCollection(ws string) string { return fmt.Sprintf("ws_%s_peers", ResolveWorkspaceID(ws)) }
func peerCardCollection(ws string) string { return fmt.Sprintf("ws_%s_peer_cards", ResolveWorkspaceID(ws)) }

func (s *Store) CreatePeer(workspaceID, peerName string, metadata map[string]interface{}) (string, error) {
	if !strings.HasPrefix(peerCollection(workspaceID), "ws_") { return "", fmt.Errorf("invalid workspace") }
	coll, err := s.chroma.EnsureCollection(peerCollection(workspaceID), map[string]interface{}{"workspace_id": workspaceID})
	if err != nil { return "", err }
	id := peerName
	meta := map[string]interface{}{"peer_name": peerName, "workspace_id": workspaceID, "created_at": time.Now().UTC().Format(time.RFC3339)}
	for k,v:=range metadata { meta[k]=v }
	dummy := s.dummyVector()
	return id, s.chroma.UpsertDocuments(coll.ID, []string{id}, []string{peerName}, []map[string]interface{}{meta}, [][]float32{dummy})
}

func (s *Store) ListPeers(workspaceID string) ([]map[string]interface{}, error) {
	coll, err := s.chroma.GetCollection(peerCollection(workspaceID))
	if err != nil { return []map[string]interface{}{}, nil }
	return s.chroma.GetDocuments(coll.ID, map[string]interface{}{"workspace_id": workspaceID}, 1000, 0)
}

func (s *Store) SetPeerCard(workspaceID, peerID string, lines []string) error {
	coll, err := s.chroma.EnsureCollection(peerCardCollection(workspaceID), map[string]interface{}{"workspace_id": workspaceID})
	if err != nil { return err }
	meta := map[string]interface{}{"peer_id": peerID, "workspace_id": workspaceID, "updated_at": time.Now().UTC().Format(time.RFC3339)}
	content := strings.Join(lines, "\n")
	dummy := s.dummyVector()
	// also embed card for retrieval
	if emb, err2 := s.embed.Embed([]string{content}); err2 == nil && len(emb)>0 {
		return s.chroma.UpsertDocuments(coll.ID, []string{peerID}, []string{content}, []map[string]interface{}{meta}, [][]float32{emb[0].Vector})
	}
	return s.chroma.UpsertDocuments(coll.ID, []string{peerID}, []string{content}, []map[string]interface{}{meta}, [][]float32{dummy})
}

func (s *Store) GetPeerCard(workspaceID, peerID string) ([]string, error) {
	coll, err := s.chroma.GetCollection(peerCardCollection(workspaceID))
	if err != nil { return nil, nil }
	docs, err := s.chroma.GetDocuments(coll.ID, map[string]interface{}{"peer_id": peerID}, 1, 0)
	if err != nil || len(docs)==0 { return nil, nil }
	doc,_:= docs[0]["document"].(string)
	if doc=="" { return nil, nil }
	return strings.Split(doc, "\n"), nil
}
