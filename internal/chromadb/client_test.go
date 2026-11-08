package chromadb

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// queryStub serves a fixed /query payload regardless of the request.
func queryStub(t *testing.T, payload string) *Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(payload))
	}))
	t.Cleanup(srv.Close)
	return New(srv.URL, "default_tenant", "default_database")
}

func TestQueryEmptyIds(t *testing.T) {
	c := queryStub(t, `{"ids": [], "documents": [], "metadatas": [], "distances": []}`)
	// F8: empty Chroma response must return empty (no index-out-of-range panic).
	res, err := c.Query("coll", [][]float32{{0.1}}, 5, nil, nil)
	if err != nil {
		t.Fatalf("empty ids should not error: %v", err)
	}
	if len(res) != 0 {
		t.Errorf("expected 0 results, got %d", len(res))
	}
}

func TestQueryShapeMismatch(t *testing.T) {
	c := queryStub(t, `{"ids": [["a","b"]], "documents": [["only-one"]], "metadatas": [[{},{}]], "distances": [[0.1,0.2]]}`)
	// F8: mismatched parallel arrays must error (handler maps to 500 JSON),
	// never panic with a dropped connection.
	_, err := c.Query("coll", [][]float32{{0.1}}, 5, nil, nil)
	if err == nil {
		t.Error("shape mismatch should return an error")
	}
}

func TestQueryNormal(t *testing.T) {
	c := queryStub(t, `{"ids": [["a","b"]], "documents": [["doc-a","doc-b"]], "metadatas": [[{"k":"v"},{}]], "distances": [[0.1,0.2]]}`)
	res, err := c.Query("coll", [][]float32{{0.1}}, 5, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 2 || res[0].ID != "a" || res[1].Document != "doc-b" {
		t.Errorf("unexpected results: %+v", res)
	}
}
