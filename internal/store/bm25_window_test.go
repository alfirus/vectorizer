package store

import "testing"

// bm25Where must emit workspace_id (+ session_id when set) and a peer_id
// $ne codeindex clause. chroma normalizeWhere wraps the multi-key map in
// $and and passes the $ne value through untouched, so the filter is applied
// server-side on /get.
func TestBM25WhereEmitsNeClause(t *testing.T) {
	w := bm25Where("code_aict", "sess1")
	if w["workspace_id"] != "code_aict" {
		t.Errorf("workspace_id = %v, want code_aict", w["workspace_id"])
	}
	if w["session_id"] != "sess1" {
		t.Errorf("session_id = %v, want sess1", w["session_id"])
	}
	ne, ok := w["peer_id"].(map[string]interface{})
	if !ok {
		t.Fatalf("peer_id = %v (%T), want map with $ne", w["peer_id"], w["peer_id"])
	}
	if ne["$ne"] != "codeindex" {
		t.Errorf("peer_id.$ne = %v, want codeindex", ne["$ne"])
	}

	// No session: session_id key must be absent (matching GetMessages).
	w2 := bm25Where("code_aict", "")
	if _, ok := w2["session_id"]; ok {
		t.Errorf("session_id present for empty session: %v", w2)
	}
	if ne2, ok := w2["peer_id"].(map[string]interface{}); !ok || ne2["$ne"] != "codeindex" {
		t.Errorf("peer_id $ne clause missing without session: %v", w2)
	}
}

// hybridScanWindow must never drop below the 4000 floor for small n and
// must still scale with large requests.
func TestHybridScanWindowFloor(t *testing.T) {
	for _, n := range []int{0, 1, 5, 10, 100} {
		if got := hybridScanWindow(n); got < 4000 {
			t.Errorf("hybridScanWindow(%d) = %d, want >= 4000", n, got)
		}
	}
	if got := hybridScanWindow(2000); got != 8000 {
		t.Errorf("hybridScanWindow(2000) = %d, want 8000 (nResults*4)", got)
	}
}

// matchBM25Where is a test-only simulation of Chroma $and/$eq/$ne semantics
// for the shape bm25Where emits. It proves the contract: a doc carrying
// peer_id=codeindex does NOT match the BM25 scan filter, so edges are not
// in the returned set.
func matchBM25Where(where map[string]interface{}, meta map[string]interface{}) bool {
	conds := []map[string]interface{}{where}
	if and, ok := where["$and"].([]map[string]interface{}); ok {
		conds = and
	}
	for _, c := range conds {
		for k, v := range c {
			mv, present := meta[k]
			if op, ok := v.(map[string]interface{}); ok {
				if ne, ok := op["$ne"]; ok {
					if present && mv == ne {
						return false
					}
					continue
				}
				return false // unknown operator: no match
			}
			if !present || mv != v {
				return false
			}
		}
	}
	return true
}

func TestBM25WhereExcludesCodeindexPeer(t *testing.T) {
	w := bm25Where("code_aict", "")
	docs := []struct {
		name string
		meta map[string]interface{}
		want bool
	}{
		{"codefile content", map[string]interface{}{"workspace_id": "code_aict", "peer_id": "codefile"}, true},
		{"chat content", map[string]interface{}{"workspace_id": "code_aict", "peer_id": "chatindex"}, true},
		{"no peer (legacy)", map[string]interface{}{"workspace_id": "code_aict"}, true},
		{"codeindex edge", map[string]interface{}{"workspace_id": "code_aict", "peer_id": "codeindex"}, false},
		{"other workspace", map[string]interface{}{"workspace_id": "other", "peer_id": "codefile"}, false},
	}
	for _, d := range docs {
		if got := matchBM25Where(w, d.meta); got != d.want {
			t.Errorf("%s: match = %v, want %v", d.name, got, d.want)
		}
	}
}
