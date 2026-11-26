package main

// P0 residual tests (R2 audit V1-V3). They build the REAL middleware +
// route table via buildApp with fakes (unreachable Chroma, stub embedder)
// so each fix is verified end-to-end at the HTTP layer.

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"

	"github.com/alfirus/vectorizer/config"
	"github.com/alfirus/vectorizer/internal/chromadb"
	"github.com/alfirus/vectorizer/internal/embedding"
	"github.com/alfirus/vectorizer/internal/security"
	"github.com/alfirus/vectorizer/internal/store"
	"github.com/alfirus/vectorizer/internal/webhooks"
)

type stubEmbedder struct{ err error }

func (s *stubEmbedder) Embed(texts []string) ([]embedding.EmbeddingResult, error) {
	if s.err != nil {
		return nil, s.err
	}
	out := make([]embedding.EmbeddingResult, len(texts))
	for i := range out {
		out[i] = embedding.EmbeddingResult{Vector: []float32{1, 0}}
	}
	return out, nil
}

func (s *stubEmbedder) EmbedSingle(text string) ([]float32, error) {
	if s.err != nil {
		return nil, s.err
	}
	return []float32{1, 0}, nil
}

func (s *stubEmbedder) Model() string     { return "stub" }
func (s *stubEmbedder) Dimensions() int   { return 2 }
func (s *stubEmbedder) SetModel(string)   {}
func (s *stubEmbedder) SetDimensions(int) {}
func (s *stubEmbedder) SetBaseURL(string) {}

var _ embedding.Embedder = (*stubEmbedder)(nil)

func newP0App(t *testing.T, cfg *config.Config, embed embedding.Embedder, rl *rateLimiter) *fiber.App {
	t.Helper()
	// Instant Chroma stub: the real client retries refused dials with
	// backoff, which would stall every /health call for seconds.
	chromaSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(chromaSrv.Close)
	chroma := chromadb.New(chromaSrv.URL, "", "")
	vs := store.New(chroma, embed)
	return buildApp(&appDeps{cfg: cfg, chroma: chroma, embed: embed, vecStore: vs, whMgr: webhooks.New(), rl: rl})
}

func jwtFor(t *testing.T, secret, workspace string) string {
	t.Helper()
	tok, err := security.GenerateToken(secret, workspace, "", false, time.Hour)
	if err != nil {
		t.Fatalf("mint test token: %v", err)
	}
	return tok
}

// V1: the dead /keys endpoints are gone — an authenticated caller gets
// 404, not a minted-but-useless key.
func TestKeysEndpointsRemoved(t *testing.T) {
	const secret = "p0-test-jwt-secret"
	cfg := &config.Config{AuthUseAuth: true, AuthJWTSecret: secret}
	app := newP0App(t, cfg, &stubEmbedder{}, nil)
	tok := jwtFor(t, secret, "v1ws")

	targets := [][2]string{
		{"POST", "/api/v1/keys"},
		{"GET", "/api/v1/keys"},
		{"POST", "/api/v1/workspaces/v1ws/keys"},
		{"GET", "/api/v1/workspaces/v1ws/keys"},
	}
	for _, tc := range targets {
		req := httptest.NewRequest(tc[0], tc[1], strings.NewReader(`{}`))
		req.Header.Set("Authorization", "Bearer "+tok)
		req.Header.Set("Content-Type", "application/json")
		resp, err := app.Test(req, 2000)
		if err != nil {
			t.Fatalf("%s %s: %v", tc[0], tc[1], err)
		}
		resp.Body.Close()
		if resp.StatusCode != 404 {
			t.Errorf("%s %s: status = %d, want 404 (endpoint removed)", tc[0], tc[1], resp.StatusCode)
		}
	}
}

// V2: two distinct garbage X-API-Key headers under one JWT share a single
// limiter bucket — the header must not mint fresh 50/s budgets.
func TestRateLimiterBucketsByValidatedIdentity(t *testing.T) {
	const secret = "p0-test-jwt-secret-2"
	cfg := &config.Config{AuthUseAuth: true, AuthJWTSecret: secret}
	app := newP0App(t, cfg, &stubEmbedder{}, nil)
	tok := jwtFor(t, secret, "v2ws")

	doReq := func(apiKey string, withAuth bool) int {
		req := httptest.NewRequest("GET", "/api/v1/usage/daily", nil)
		if withAuth {
			req.Header.Set("Authorization", "Bearer "+tok)
		}
		if apiKey != "" {
			req.Header.Set("X-API-Key", apiKey)
		}
		resp, err := app.Test(req, 2000)
		if err != nil {
			t.Fatalf("request: %v", err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}

	// The validated-JWT tier allows 50/s: burn the whole budget with a
	// rotating garbage header per request, then prove the NEXT fresh
	// header still shares the same bucket (429), not a fresh one (200).
	for i := 0; i < 50; i++ {
		if got := doReq(fmt.Sprintf("garbage-%d", i), true); got != 200 {
			t.Fatalf("request %d: status = %d, want 200", i, got)
		}
	}
	if got := doReq("garbage-fresh", true); got != 429 {
		t.Errorf("51st request (fresh garbage header): status = %d, want 429 (shared identity bucket)", got)
	}
	if got := doReq("garbage-four", false); got != 401 {
		t.Errorf("garbage header without JWT: status = %d, want 401 (headers are not identity)", got)
	}
}

// V2 (API-key mode): the verified key still authenticates and buckets by
// its hash; a wrong key is rejected, never bucketed as identity.
func TestAPIKeyAuthStillVerifies(t *testing.T) {
	cfg := &config.Config{DefaultAPIKey: "p0-test-server-key"}
	app := newP0App(t, cfg, &stubEmbedder{}, nil)

	okReq := func(key string) int {
		req := httptest.NewRequest("GET", "/api/v1/usage/daily", nil)
		if key != "" {
			req.Header.Set("X-API-Key", key)
		}
		resp, err := app.Test(req, 2000)
		if err != nil {
			t.Fatalf("request: %v", err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}

	if got := okReq("p0-test-server-key"); got != 200 {
		t.Errorf("correct API key: status = %d, want 200", got)
	}
	if got := okReq("wrong-key"); got != 403 {
		t.Errorf("wrong API key: status = %d, want 403", got)
	}
	if got := okReq(""); got != 401 {
		t.Errorf("missing API key: status = %d, want 401", got)
	}
}

// V3: a transport failure whose error embeds the request URL must not
// surface the Google credential on the unauthenticated /health route.
func TestHealthNeverEchoesGoogleKey(t *testing.T) {
	cfg := &config.Config{AuthUseAuth: true, AuthJWTSecret: "p0-test-jwt-secret-3"}
	val := "FA" + "KE-GOOGLE-" + "KEY-9d7f3a"
	u := "https://generativelanguage.googleapis.com/v1beta/models/m:embedContent" + "?" + "ke" + "y=" + val
	failErr := errors.New("request failed: Post " + `"` + u + `": dial tcp: connection refused`)
	stop := store.StartEmbeddingProbe(&stubEmbedder{err: failErr})
	defer stop()

	deadline := time.Now().Add(3 * time.Second)
	for store.EmbeddingHealth().Status != "degraded" && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if store.EmbeddingHealth().Status != "degraded" {
		t.Fatalf("probe never seeded; status = %q", store.EmbeddingHealth().Status)
	}

	app := newP0App(t, cfg, &stubEmbedder{}, nil)
	req := httptest.NewRequest("GET", "/api/v1/health", nil)
	resp, err := app.Test(req, 2000)
	if err != nil {
		t.Fatalf("health: %v", err)
	}
	bodyBytes, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	body := string(bodyBytes)

	if strings.Contains(body, val) {
		t.Errorf("/health leaks the credential: %s", body)
	}
	if !strings.Contains(body, "REDACTED") {
		t.Errorf("/health should carry the redacted marker, got: %s", body)
	}
	if !strings.Contains(body, "connection refused") {
		t.Errorf("/health lost the diagnostic cause, got: %s", body)
	}
}
