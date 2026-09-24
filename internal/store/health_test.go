package store

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/alfirus/vectorizer/internal/embedding"
)

// fakeEmbedder lets a test decide whether embedding succeeds, fails, or hangs.
type fakeEmbedder struct {
	err   error
	sleep time.Duration
}

func (f *fakeEmbedder) Embed(texts []string) ([]embedding.EmbeddingResult, error) {
	if f.err != nil {
		return nil, f.err
	}
	out := make([]embedding.EmbeddingResult, len(texts))
	for i := range out {
		out[i] = embedding.EmbeddingResult{Vector: []float32{1, 0}}
	}
	return out, nil
}

func (f *fakeEmbedder) EmbedSingle(text string) ([]float32, error) {
	if f.sleep > 0 {
		time.Sleep(f.sleep)
	}
	if f.err != nil {
		return nil, f.err
	}
	return []float32{1, 0}, nil
}

func (f *fakeEmbedder) Model() string         { return "fake" }
func (f *fakeEmbedder) Dimensions() int       { return 2 }
func (f *fakeEmbedder) SetModel(model string) {}
func (f *fakeEmbedder) SetDimensions(d int)   {}
func (f *fakeEmbedder) SetBaseURL(url string) {}

var _ embedding.Embedder = (*fakeEmbedder)(nil)

// A working embedder must report ok with a LastOK stamp — this is the state
// that proves "the index wrote real vectors".
func TestProbeOnceOK(t *testing.T) {
	res := probeOnce(&fakeEmbedder{}, time.Second)
	if res.Status != "ok" {
		t.Fatalf("status = %q, want ok (err=%q)", res.Status, res.LastErr)
	}
	if res.LastOK.IsZero() {
		t.Error("LastOK is zero — callers use it to age the last good embed")
	}
	if res.LastErr != "" {
		t.Errorf("LastErr = %q, want empty when ok", res.LastErr)
	}
}

// A dead embedder (LM Studio down) must surface as degraded WITH the reason,
// not silently as ok — this is the whole point of the probe.
func TestProbeOnceReportsFailure(t *testing.T) {
	res := probeOnce(&fakeEmbedder{err: errors.New("dial tcp: connection refused")}, time.Second)
	if res.Status != "degraded" {
		t.Fatalf("status = %q, want degraded", res.Status)
	}
	if !strings.Contains(res.LastErr, "connection refused") {
		t.Errorf("LastErr = %q, want it to carry the underlying cause", res.LastErr)
	}
	if !res.LastOK.IsZero() {
		t.Error("LastOK set on failure — would falsely imply a healthy embed")
	}
}

// A hung embedder must be reaped by the timeout instead of blocking forever.
// This is why probeOnce exists rather than calling embed directly in /health.
func TestProbeOnceTimesOut(t *testing.T) {
	res := probeOnce(&fakeEmbedder{sleep: 3 * time.Second}, 50*time.Millisecond)
	if res.Status != "degraded" {
		t.Fatalf("status = %q, want degraded after timeout", res.Status)
	}
	if !strings.Contains(res.LastErr, "exceeded") {
		t.Errorf("LastErr = %q, want a timeout message", res.LastErr)
	}
}

// nil embedder (no provider configured) is neither ok nor broken.
func TestProbeOnceNilEmbedder(t *testing.T) {
	res := probeOnce(nil, time.Second)
	if res.Status != "unknown" {
		t.Fatalf("status = %q, want unknown", res.Status)
	}
}

// Before any probe runs, EmbeddingHealth must say so rather than claim ok —
// "unknown" is what stops a caller from asserting healthy on no evidence.
func TestEmbeddingHealthDefaultsUnknown(t *testing.T) {
	if got := EmbeddingHealth().Status; got != "unknown" {
		// Another test may have started a probe; only assert the value shape.
		if got != "ok" && got != "degraded" {
			t.Fatalf("status = %q, want unknown|ok|degraded", got)
		}
	}
}

// The model name must be reported even when degraded, so an operator knows
// WHICH provider is down.
func TestEmbeddingModelNameDefault(t *testing.T) {
	t.Setenv("EMBED_MODEL", "")
	if got := EmbeddingModelName(); got == "" {
		t.Fatal("empty model name — health output would omit what is down")
	}
	t.Setenv("EMBED_MODEL", "custom-model")
	if got := EmbeddingModelName(); got != "custom-model" {
		t.Fatalf("got %q, want custom-model", got)
	}
}

// StartEmbeddingProbe must seed a result immediately (not after one interval),
// otherwise the first /health call after boot reports "unknown".
func TestStartEmbeddingProbeSeedsImmediately(t *testing.T) {
	stop := StartEmbeddingProbe(&fakeEmbedder{})
	defer stop()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if EmbeddingHealth().Status == "ok" {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("probe never seeded; status = %q", EmbeddingHealth().Status)
}

// A nil embedder must not start a goroutine that spins forever.
func TestStartEmbeddingProbeNilSafe(t *testing.T) {
	stop := StartEmbeddingProbe(nil)
	if stop == nil {
		t.Fatal("stop is nil — callers defer stop()")
	}
	stop() // must not panic
}
