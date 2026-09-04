package store

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// TraceResult is one node in a provenance trace.
type TraceResult struct {
	ID        string `json:"id"`
	Kind      string `json:"kind"` // "conclusion" | "message" | "edge"
	Document  string `json:"document,omitempty"`
	Workspace string `json:"workspace_id,omitempty"`
	PeerID    string `json:"peer_id,omitempty"`
	CreatedAt string `json:"created_at,omitempty"`
	Depth     int    `json:"depth"`
	Via       string `json:"via,omitempty"` // edge that led here ("premise" | "supported_by" | "cited_by")
}

// TraceForward answers "why do I believe conclusion X?": walks reasoning
// edges from the conclusion down through premises to supporting messages.
// BFS with visited-set; mirrors codebase-memory-mcp trace_path semantics
// (bounded depth, no cycles) but over memory provenance instead of CALLS.
func (s *Store) TraceForward(ws, conclusionID string, maxDepth int) ([]TraceResult, error) {
	if maxDepth <= 0 {
		maxDepth = 5
	}
	if maxDepth > 10 {
		maxDepth = 10
	}
	coll, err := s.chroma.GetCollection(reasoningCollection(ws))
	if err != nil {
		return []TraceResult{}, nil // no reasoning edges recorded yet
	}
	seen := map[string]bool{conclusionID: true}
	type item struct {
		id    string
		depth int
	}
	queue := []item{{conclusionID, 0}}
	var out []TraceResult
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		if cur.depth >= maxDepth {
			continue
		}
		edges, _ := s.chroma.GetDocuments(coll.ID, map[string]interface{}{"conclusion_id": cur.id}, 50, 0)
		for _, e := range edges {
			m, _ := e["metadata"].(map[string]interface{})
			if m == nil {
				continue
			}
			// Premises: other conclusions this one depends on.
			premStr, _ := m["premise_ids"].(string)
			for _, pid := range strings.Split(premStr, ",") {
				pid = strings.TrimSpace(pid)
				if pid == "" || seen[pid] {
					continue
				}
				seen[pid] = true
				out = append(out, TraceResult{ID: pid, Kind: "conclusion", Depth: cur.depth + 1, Via: "premise"})
				queue = append(queue, item{pid, cur.depth + 1})
			}
			// Supporting messages: raw memories backing this conclusion.
			msgStr, _ := m["supporting_message_ids"].(string)
			for _, mid := range strings.Split(msgStr, ",") {
				mid = strings.TrimSpace(mid)
				if mid == "" || seen[mid] {
					continue
				}
				seen[mid] = true
				doc := s.messagePreview(ws, mid)
				out = append(out, TraceResult{ID: mid, Kind: "message", Document: doc, Workspace: ws, Depth: cur.depth + 1, Via: "supported_by"})
			}
		}
	}
	return out, nil
}

// TraceReverse answers "what breaks if I forget message/conclusion X?":
// finds every conclusion that (transitively) depends on the target — the
// memory equivalent of a blast-radius / detect_changes scan. Walks the edge
// table in reverse: any edge whose premise_ids or supporting_message_ids
// mention a known-affected ID marks its conclusion_id affected, iteratively
// to fixpoint (bounded by maxDepth rounds).
func (s *Store) TraceReverse(ws, targetID string, maxDepth int) ([]TraceResult, error) {
	if maxDepth <= 0 {
		maxDepth = 5
	}
	if maxDepth > 10 {
		maxDepth = 10
	}
	coll, err := s.chroma.GetCollection(reasoningCollection(ws))
	if err != nil {
		return []TraceResult{}, nil
	}
	affected := map[string]bool{targetID: true}
	var out []TraceResult
	for round := 1; round <= maxDepth; round++ {
		edges, _ := s.chroma.GetDocuments(coll.ID, map[string]interface{}{"workspace_id": ws}, 500, 0)
		if len(edges) == 0 {
			break
		}
		grew := false
		for _, e := range edges {
			m, _ := e["metadata"].(map[string]interface{})
			if m == nil {
				continue
			}
			cid, _ := m["conclusion_id"].(string)
			if cid == "" || affected[cid] {
				continue
			}
			mentions := func(csv string) bool {
				for _, p := range strings.Split(csv, ",") {
					if affected[strings.TrimSpace(p)] && strings.TrimSpace(p) != "" {
						return true
					}
				}
				return false
			}
			premStr, _ := m["premise_ids"].(string)
			msgStr, _ := m["supporting_message_ids"].(string)
			if mentions(premStr) || mentions(msgStr) {
				affected[cid] = true
				grew = true
				out = append(out, TraceResult{ID: cid, Kind: "conclusion", Workspace: ws, Depth: round, Via: "cited_by"})
			}
		}
		if !grew {
			break
		}
	}
	// Most-direct dependents first.
	sort.Slice(out, func(i, j int) bool { return out[i].Depth < out[j].Depth })
	return out, nil
}

// messagePreview fetches the first chunk of a message for trace display.
func (s *Store) messagePreview(ws, messageID string) string {
	chunks, err := s.GetMessageChunks(ws, messageID)
	if err != nil || len(chunks) == 0 {
		return ""
	}
	if doc, ok := chunks[0]["document"].(string); ok {
		if len(doc) > 300 {
			return doc[:300] + "…"
		}
		return doc
	}
	return ""
}

// StaleCandidate is one memory flagged by the dead-knowledge scan.
type StaleCandidate struct {
	ID         string  `json:"id"`
	Kind       string  `json:"kind"` // "message" | "conclusion"
	Document   string  `json:"document,omitempty"`
	Workspace  string  `json:"workspace_id"`
	CreatedAt  string  `json:"created_at,omitempty"`
	AgeDays    float64 `json:"age_days"`
	Importance float64 `json:"importance,omitempty"`
	Reason     string  `json:"reason"`
}

// ScanStale finds dead knowledge: messages/conclusions older than maxAgeDays
// that were never reinforced (no reasoning edge references them) and aren't
// protected as timeless (system role / importance>=4 — same exemption as
// decay). Feed the results into review (confirm → TTLDelete or DeleteMessage)
// rather than auto-deleting: scan proposes, human disposes.
func (s *Store) ScanStale(ws string, maxAgeDays int, limit int) ([]StaleCandidate, error) {
	if maxAgeDays <= 0 {
		maxAgeDays = 90
	}
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	cutoff := time.Now().Add(-time.Duration(maxAgeDays) * 24 * time.Hour)

	// Build the reinforced set: every ID mentioned in any reasoning edge.
	reinforced := map[string]bool{}
	if coll, err := s.chroma.GetCollection(reasoningCollection(ws)); err == nil {
		if edges, _ := s.chroma.GetDocuments(coll.ID, map[string]interface{}{"workspace_id": ws}, 1000, 0); edges != nil {
			for _, e := range edges {
				m, _ := e["metadata"].(map[string]interface{})
				if m == nil {
					continue
				}
				if cid, _ := m["conclusion_id"].(string); cid != "" {
					reinforced[cid] = true
				}
				for _, csv := range []string{fmt.Sprint(m["premise_ids"]), fmt.Sprint(m["supporting_message_ids"])} {
					for _, p := range strings.Split(csv, ",") {
						if p = strings.TrimSpace(p); p != "" {
							reinforced[p] = true
						}
					}
				}
			}
		}
	}

	var out []StaleCandidate
	seenMsg := map[string]bool{}

	// 1. Messages: group chunks by message_id, check oldest created_at.
	collName := s.GetCollectionName(ws)
	if coll, err := s.chroma.GetCollection(collName); err == nil {
		docs, _ := s.chroma.GetDocuments(coll.ID, map[string]interface{}{"workspace_id": ws}, 2000, 0)
		for _, d := range docs {
			m, _ := d["metadata"].(map[string]interface{})
			if m == nil {
				continue
			}
			mid, _ := m["message_id"].(string)
			if mid == "" || seenMsg[mid] || reinforced[mid] {
				continue
			}
			seenMsg[mid] = true
			role, _ := m["role"].(string)
			if role == "system" {
				continue // timeless — same exemption as decayExempt
			}
			imp := toFloat(m["importance"])
			if imp >= 4 {
				continue // timeless fact
			}
			created, _ := m["created_at"].(string)
			ts, err := time.Parse(time.RFC3339, created)
			if err != nil || ts.After(cutoff) {
				continue
			}
			doc, _ := d["document"].(string)
			if len(doc) > 200 {
				doc = doc[:200] + "…"
			}
			reason := "old, never reinforced by any reasoning edge"
			if role == "assistant" {
				reason = "old assistant output, never cited as premise or support"
			}
			out = append(out, StaleCandidate{
				ID: mid, Kind: "message", Document: doc, Workspace: ws,
				CreatedAt: created, AgeDays: time.Since(ts).Hours() / 24,
				Importance: imp, Reason: reason,
			})
			if len(out) >= limit {
				break
			}
		}
	}

	// 2. Conclusions: same treatment.
	if len(out) < limit {
		if concs, _ := s.ListConclusions(ws, "", 500, 0); concs != nil {
			for _, c := range concs {
				id := fmt.Sprint(c["id"])
				if id == "" || reinforced[id] {
					continue
				}
				m, _ := c["metadata"].(map[string]interface{})
				created := ""
				if m != nil {
					created, _ = m["created_at"].(string)
				}
				ts, err := time.Parse(time.RFC3339, created)
				if err != nil || ts.After(cutoff) {
					continue
				}
				doc, _ := c["document"].(string)
				if len(doc) > 200 {
					doc = doc[:200] + "…"
				}
				out = append(out, StaleCandidate{
					ID: id, Kind: "conclusion", Document: doc, Workspace: ws,
					CreatedAt: created, AgeDays: time.Since(ts).Hours() / 24,
					Reason: "old conclusion, never used as premise",
				})
				if len(out) >= limit {
					break
				}
			}
		}
	}

	sort.Slice(out, func(i, j int) bool { return out[i].AgeDays > out[j].AgeDays })
	return out, nil
}

func toFloat(v interface{}) float64 {
	switch t := v.(type) {
	case float64:
		return t
	case float32:
		return float64(t)
	case int:
		return float64(t)
	case int64:
		return float64(t)
	case string:
		var f float64
		_, _ = fmt.Sscanf(t, "%f", &f)
		return f
	}
	return 0
}

// Brief is the one-shot session-start overview: workspace stats + top
// entities + recent conclusions in a single call, so agents don't need
// stats + representation + recent-messages as 3 round trips (the
// get_architecture token-saving pattern: one summary call, not N probes).
type Brief struct {
	WorkspaceID      string                   `json:"workspace_id"`
	DocumentCount    int                      `json:"document_count"`
	ConclusionCount  int                      `json:"conclusion_count"`
	SessionCount     int                      `json:"session_count"`
	Representation   string                   `json:"representation"`
	RecentConcls     []map[string]interface{} `json:"recent_conclusions"`
	TopEntities      []string                 `json:"top_entities,omitempty"`
	DeriverDrops     int64                    `json:"deriver_drops_total,omitempty"`
	StaleCount       int                      `json:"stale_count,omitempty"`
	GeneratedAt      string                   `json:"generated_at"`
}

// GetBrief builds the one-shot overview. maxConclusions caps the
// representation pull; includeStale runs a cheap stale-count alongside.
func (s *Store) GetBrief(ws, peerID string, maxConclusions int, includeStale bool) (*Brief, error) {
	if maxConclusions <= 0 || maxConclusions > 50 {
		maxConclusions = 10
	}
	b := &Brief{WorkspaceID: ws, GeneratedAt: time.Now().UTC().Format(time.RFC3339)}
	if stats, err := s.GetWorkspaceStats(ws); err == nil {
		if dc, ok := stats["document_count"].(int); ok {
			b.DocumentCount = dc
		}
	}
	if concs, _ := s.ListConclusions(ws, peerID, maxConclusions, 0); concs != nil {
		b.ConclusionCount = len(concs)
		b.RecentConcls = concs
	}
	if sessions, _ := s.ListSessions(ws); sessions != nil {
		b.SessionCount = len(sessions)
	}
	if text, _, _ := s.GetRepresentation(ws, peerID, "", maxConclusions); text != "" {
		b.Representation = text
	}
	// Top entities: sample recent docs, count entity mentions.
	entityCount := map[string]int{}
	if coll, err := s.chroma.GetCollection(s.GetCollectionName(ws)); err == nil {
		if docs, _ := s.chroma.GetDocuments(coll.ID, map[string]interface{}{"workspace_id": ws}, 100, 0); docs != nil {
			for _, d := range docs {
				m, _ := d["metadata"].(map[string]interface{})
				if m == nil {
					continue
				}
				if ents, _ := m["entities"].(string); ents != "" {
					for _, e := range strings.Split(ents, ",") {
						if e = strings.TrimSpace(e); e != "" {
							entityCount[e]++
						}
					}
				}
			}
		}
	}
	type kv struct {
		k string
		v int
	}
	var sorted []kv
	for k, v := range entityCount {
		sorted = append(sorted, kv{k, v})
	}
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].v > sorted[j].v })
	for i := 0; i < len(sorted) && i < 10; i++ {
		b.TopEntities = append(b.TopEntities, sorted[i].k)
	}
	if includeStale {
		if stale, _ := s.ScanStale(ws, 90, 200); stale != nil {
			b.StaleCount = len(stale)
		}
	}
	return b, nil
}
