package embedding

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// V3: the credential must travel in the x-goog-api-key header, never in
// the URL — Go's url.Error embeds the full URL into transport failures,
// and that text is stored + served on the unauthenticated /health route.
func TestGoogleSendsKeyInHeaderNotURL(t *testing.T) {
	var gotHeader, gotQuery, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHeader = r.Header.Get("x-goog-api-key")
		gotQuery = r.URL.RawQuery
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(r.URL.Path, "batchEmbedContents") {
			_, _ = w.Write([]byte(`{"embeddings":[{"values":[0.1,0.2]},{"values":[0.3,0.4]}]}`))
		} else {
			_, _ = w.Write([]byte(`{"embedding":{"values":[0.1,0.2]}}`))
		}
	}))
	defer srv.Close()

	secret := "test-" + "secret-" + "1"
	g := NewGoogle(secret, "text-embedding-004", 2)
	g.SetBaseURL(srv.URL)

	res, err := g.Embed([]string{"hello"})
	if err != nil {
		t.Fatalf("single embed: %v", err)
	}
	if len(res) != 1 || len(res[0].Vector) != 2 {
		t.Fatalf("single embed: unexpected result %+v", res)
	}
	if gotHeader != secret {
		t.Errorf("single: x-goog-api-key header = %q, want the configured credential", gotHeader)
	}
	if gotQuery != "" {
		t.Errorf("single: URL query = %q, want empty (no ?key=)", gotQuery)
	}
	if !strings.Contains(gotPath, "embedContent") {
		t.Errorf("single: path = %q, want embedContent action", gotPath)
	}

	res, err = g.Embed([]string{"a", "b"})
	if err != nil {
		t.Fatalf("batch embed: %v", err)
	}
	if len(res) != 2 {
		t.Fatalf("batch embed: got %d results, want 2", len(res))
	}
	if gotHeader != secret {
		t.Errorf("batch: x-goog-api-key header = %q, want the configured credential", gotHeader)
	}
	if gotQuery != "" {
		t.Errorf("batch: URL query = %q, want empty (no ?key=)", gotQuery)
	}
}

// SetBaseURL must actually reroute (it is also the test seam above) —
func TestGoogleSetBaseURLReroutes(t *testing.T) {
	g := NewGoogle("k", "m", 2)
	g.SetBaseURL("http://example.com/")
	if got := g.endpoint("embedContent"); got != "http://example.com/v1beta/models/m:embedContent" {
		t.Errorf("endpoint = %q", got)
	}
	g.SetBaseURL("")
	if got := g.endpoint("embedContent"); !strings.HasPrefix(got, "https://generativelanguage.googleapis.com/") {
		t.Errorf("default endpoint = %q, want production host", got)
	}
}
