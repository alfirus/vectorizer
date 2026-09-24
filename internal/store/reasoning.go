package store

import (
	"fmt"
	"hash/fnv"
	"strings"
	"time"
)

func fnvHash(s string) uint64 {
	h := fnv.New64a()
	h.Write([]byte(s))
	return h.Sum64()
}

func reasoningCollection(ws string) string {
	return fmt.Sprintf("ws_%s_reasoning", ResolveWorkspaceID(ws))
}

// Premise edge: conclusion depends on message_ids / other conclusion_ids
type ReasoningEdge struct {
	ID               string   `json:"id"`
	WorkspaceID      string   `json:"workspace_id"`
	PeerID           string   `json:"peer_id"`
	ConclusionID     string   `json:"conclusion_id"`
	PremiseIDs       []string `json:"premise_ids"`
	SupportingMsgIDs []string `json:"supporting_message_ids"`
	CreatedAt        string   `json:"created_at"`
}

func (s *Store) AddReasoningEdge(ws, peerID, conclusionID string, premiseIDs, msgIDs []string) error {
	coll, err := s.chroma.EnsureCollection(reasoningCollection(ws), map[string]interface{}{"workspace_id": ws})
	if err != nil {
		return err
	}
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
	if err != nil {
		return nil, nil
	}
	docs, _ := s.chroma.GetDocuments(coll.ID, map[string]interface{}{"conclusion_id": conclusionID}, 10, 0)
	// BFS expand premises
	seen := map[string]bool{conclusionID: true}
	queue := []string{conclusionID}
	var chain []map[string]interface{}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		edges, _ := s.chroma.GetDocuments(coll.ID, map[string]interface{}{"conclusion_id": cur}, 10, 0)
		for _, e := range edges {
			chain = append(chain, e)
			m, _ := e["metadata"].(map[string]interface{})
			premStr, _ := m["premise_ids"].(string)
			for _, pid := range strings.Split(premStr, ",") {
				pid = strings.TrimSpace(pid)
				if pid != "" && !seen[pid] {
					seen[pid] = true
					queue = append(queue, pid)
				}
			}
		}
	}
	// include starting node docs
	_ = docs
	return chain, nil
}

// codeEdgeCollection is the sidecar holding code-graph edges (CALLS/IMPORTS)
// for a workspace: ws_<ws>_codeedges.
//
// Why a sidecar (the previous code deliberately wrote these into MAIN):
// edges are stored with a zero vector because they carry no prose, and the
// workspace collections use space=l2. For a unit-norm query that makes
// L2(query, edge) = ||query|| = 1.0 exactly, while L2(query, content)
// = sqrt(2-2*cos) exceeds 1.0 whenever cosine similarity is below 0.5 — so
// the zero-vector edges OUT-RANK genuine content. Observed on code_aict:
// /query returned 200 codeedge rows at exactly distance 1.0000, a content
// doc could not be retrieved by its OWN vector (self-distance 0), and
// filtering chunk_type != "edge" yielded 0 rows because the beam was already
// full of edges. Brute-force cosine meanwhile ranked the right file at
// 0.4969 — the graph, not the data, was wrong.
//
// Edges are only ever READ by metadata (CodeCallers -> Chroma /get, no vector
// search), so they never needed to live in the vector index.
//
// Chroma names allow 3-63 of [a-zA-Z0-9._-] starting/ending alnum; this
// derives from GetCollectionName so it inherits whatever that produces.
func (s *Store) codeEdgeCollection(ws string) string {
	return s.GetCollectionName(ws) + "_codeedges"
}

// codeCallersFrom derives the caller list for a set of calledby:<X> edge
// metadata maps.
//
// Pure so it is unit-testable without a Chroma: CodeCallers feeds it rows
// from BOTH the codeedges sidecar and the (pre-migration) main collection, so
// the same edge seen in both places must collapse to one caller.
//
// Sequence mirrors the original single-collection reader: every edge's
// premise_ids is consumed first, and supporting_message_ids is consulted only
// if no premise yielded a define. For calledby edges the write side always
// emits premise_ids="defines:<caller>", so that fallback cannot fire on this
// path — preserved for parity rather than removed.
func codeCallersFrom(edgeMetas []map[string]interface{}) []string {
	seen := make(map[string]bool, len(edgeMetas))
	add := func(callers *[]string, v string) {
		v = strings.TrimSpace(v)
		if v == "" || seen[v] {
			return
		}
		seen[v] = true
		*callers = append(*callers, v)
	}

	var callers []string
	for _, m := range edgeMetas {
		if m == nil {
			continue
		}
		prem, ok := m["premise_ids"].(string)
		if !ok {
			continue
		}
		for _, p := range strings.Split(prem, ",") {
			// Trim BEFORE the prefix test, as the original reader did —
			// premise_ids is ","-joined, so split leaves " defines:X" with a
			// leading space that HasPrefix would otherwise reject.
			p = strings.TrimSpace(p)
			if strings.HasPrefix(p, "defines:") {
				add(&callers, strings.TrimPrefix(p, "defines:"))
			}
		}
	}
	if len(callers) == 0 {
		for _, m := range edgeMetas {
			if m == nil {
				continue
			}
			if msg, ok := m["supporting_message_ids"].(string); ok {
				add(&callers, msg)
			}
		}
	}
	return callers
}

// AddCodeEdge stores a code-graph edge (CALLS/IMPORTS) in the workspace's
// codeedges SIDECAR collection — kept out of the main collection because the
// zero-vector edges poison that collection's HNSW index (see
// codeEdgeCollection). Doc shape mirrors ReasoningEdge so
// GetReasoningChain-style readers work; IDs are deterministic (no dup
// pile-up on re-index).
func (s *Store) AddCodeEdge(ws, conclusionID string, premiseIDs, msgIDs []string) error {
	coll, err := s.chroma.EnsureCollection(s.codeEdgeCollection(ws), map[string]interface{}{"workspace_id": ResolveWorkspaceID(ws)})
	if err != nil {
		return err
	}
	id := fmt.Sprintf("codeedge_%x", fnvHash(conclusionID+strings.Join(premiseIDs, ",")))
	meta := map[string]interface{}{
		"workspace_id": ws, "peer_id": "codeindex", "conclusion_id": conclusionID,
		"premise_ids": strings.Join(premiseIDs, ","), "supporting_message_ids": strings.Join(msgIDs, ","),
		"chunk_type": "edge", // never surfaced as content (Defect 3)
		"created_at": time.Now().UTC().Format(time.RFC3339),
	}
	dummy := s.dummyVector()
	return s.chroma.UpsertDocuments(coll.ID, []string{id}, []string{conclusionID}, []map[string]interface{}{meta}, [][]float32{dummy})
}

// CodeCallers reads CALLS edges for symbol. Returns defining symbols
// (defines:X) or file refs.
//
// Reads BOTH locations: the codeedges sidecar (where AddCodeEdge now writes)
// and the main workspace collection (rows written before the move). Both are
// metadata /get calls — no vector search — so the answer is independent of
// HNSW state, and callers() keeps working before, during and after the
// migration. codeCallersFrom dedupes an edge seen in both places.
func (s *Store) CodeCallers(ws, symbol string) ([]string, error) {
	// calledby:<X> -> defines:<caller> is the reader's orientation. The old
	// query read conclusion_id "calls:"+symbol, which stored CALLER as the
	// conclusion and therefore returned the symbol's CALLEES mislabelled as
	// callers (Defect 1). Write side emits both orientations.
	where := map[string]interface{}{"conclusion_id": "calledby:" + symbol}

	var metas []map[string]interface{}
	for _, name := range []string{s.codeEdgeCollection(ws), s.GetCollectionName(ws)} {
		coll, err := s.chroma.GetCollection(name)
		if err != nil {
			// Sidecar not created yet, or main absent for this workspace.
			continue
		}
		edges, _ := s.chroma.GetDocuments(coll.ID, where, 50, 0)
		for _, e := range edges {
			m, _ := e["metadata"].(map[string]interface{})
			metas = append(metas, m)
		}
	}
	return codeCallersFrom(metas), nil
}

func (s *Store) GetObservationContext(ws, sessionID string, chunkID string, window int) ([]map[string]interface{}, error) {
	if window <= 0 {
		window = 2
	}
	// Find target chunk's index
	all, _ := s.GetMessages(ws, sessionID, 1000, 0)
	var idx = -1
	for i, d := range all {
		if fmt.Sprint(d["id"]) == chunkID {
			idx = i
			break
		}
	}
	if idx == -1 {
		return nil, fmt.Errorf("chunk not found")
	}
	start := idx - window
	if start < 0 {
		start = 0
	}
	end := idx + window + 1
	if end > len(all) {
		end = len(all)
	}
	return all[start:end], nil
}
