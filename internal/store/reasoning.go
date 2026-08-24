package store

import (
	"fmt"
	"strings"
	"time"
)

func reasoningCollection(ws string) string { return fmt.Sprintf("ws_%s_reasoning", ws) }

// Premise edge: conclusion depends on message_ids / other conclusion_ids
type ReasoningEdge struct {
	ID              string   `json:"id"`
	WorkspaceID     string   `json:"workspace_id"`
	PeerID          string   `json:"peer_id"`
	ConclusionID    string   `json:"conclusion_id"`
	PremiseIDs      []string `json:"premise_ids"`
	SupportingMsgIDs []string `json:"supporting_message_ids"`
	CreatedAt       string   `json:"created_at"`
}

func (s *Store) AddReasoningEdge(ws, peerID, conclusionID string, premiseIDs, msgIDs []string) error {
	coll, err := s.chroma.EnsureCollection(reasoningCollection(ws), map[string]interface{}{"workspace_id": ws})
	if err != nil { return err }
	id := fmt.Sprintf("edge_%d", time.Now().UnixNano())
	meta := map[string]interface{}{
		"workspace_id": ws, "peer_id": peerID, "conclusion_id": conclusionID,
		"premise_ids": strings.Join(premiseIDs, ","), "supporting_message_ids": strings.Join(msgIDs, ","),
		"created_at": time.Now().UTC().Format(time.RFC3339),
	}
	dummy := s.dummyVector()
	// store edge as doc = conclusionID for semantic reachability (optional)
	return s.chroma.UpsertDocuments(coll.ID, []string{id}, []string{conclusionID}, []map[string]interface{}{meta}, [][]float32{dummy})
}

func (s *Store) GetReasoningChain(ws, conclusionID string) ([]map[string]interface{}, error) {
	coll, err := s.chroma.GetCollection(reasoningCollection(ws))
	if err != nil { return nil, nil }
	docs, _ := s.chroma.GetDocuments(coll.ID, map[string]interface{}{"conclusion_id": conclusionID}, 10, 0)
	// BFS expand premises
	seen := map[string]bool{conclusionID: true}
	queue := []string{conclusionID}
	var chain []map[string]interface{}
	for len(queue) > 0 {
		cur := queue[0]; queue = queue[1:]
		edges, _ := s.chroma.GetDocuments(coll.ID, map[string]interface{}{"conclusion_id": cur}, 10, 0)
		for _, e := range edges {
			chain = append(chain, e)
			m,_:= e["metadata"].(map[string]interface{})
			premStr,_:= m["premise_ids"].(string)
			for _, pid := range strings.Split(premStr, ",") {
				pid = strings.TrimSpace(pid)
				if pid!="" && !seen[pid] {
					seen[pid]=true
					queue=append(queue, pid)
				}
			}
		}
	}
	// include starting node docs
	_ = docs
	return chain, nil
}

func (s *Store) GetObservationContext(ws, sessionID string, chunkID string, window int) ([]map[string]interface{}, error) {
	if window<=0 { window=2 }
	// Find target chunk's index
	all, _ := s.GetMessages(ws, sessionID, 1000, 0)
	var idx = -1
	for i, d := range all {
		if fmt.Sprint(d["id"]) == chunkID { idx=i; break }
	}
	if idx==-1 { return nil, fmt.Errorf("chunk not found") }
	start := idx - window; if start<0 { start=0 }
	end := idx + window + 1; if end>len(all) { end=len(all) }
	return all[start:end], nil
}
