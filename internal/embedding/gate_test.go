package embedding

import (
	"errors"
	"testing"
)

type fakeEmbedder struct {
	vec []float32
	err error
}

func (f *fakeEmbedder) Embed(texts []string) ([]EmbeddingResult, error) {
	if f.err != nil {
		return nil, f.err
	}
	out := make([]EmbeddingResult, len(texts))
	for i := range texts {
		out[i] = EmbeddingResult{Vector: f.vec}
	}
	return out, nil
}
func (f *fakeEmbedder) EmbedSingle(text string) ([]float32, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.vec, nil
}
func (f *fakeEmbedder) Model() string      { return "fake" }
func (f *fakeEmbedder) Dimensions() int    { return 3 }
func (f *fakeEmbedder) SetModel(m string)  {}
func (f *fakeEmbedder) SetDimensions(d int) {}
func (f *fakeEmbedder) SetBaseURL(u string) {}

// Gate must pass calls through to the inner embedder unchanged.
func TestGatePassthrough(t *testing.T) {
	inner := &fakeEmbedder{vec: []float32{0.1, 0.2, 0.3}}
	g := WrapGate(inner)
	v, err := g.EmbedSingle("hello")
	if err != nil {
		t.Fatalf("EmbedSingle: %v", err)
	}
	if len(v) != 3 {
		t.Fatalf("len = %d, want 3", len(v))
	}
	if g.Model() != "fake" || g.Dimensions() != 3 {
		t.Fatal("Model/Dimensions not forwarded")
	}
}

// Gate must surface inner errors (so the health probe can report degraded).
func TestGateErrorPassthrough(t *testing.T) {
	inner := &fakeEmbedder{err: errors.New("boom")}
	g := WrapGate(inner)
	if _, err := g.EmbedSingle("hello"); err == nil {
		t.Fatal("expected error, got nil")
	}
}

// Unwrap must expose the inner embedder so the health probe can bypass
// the Gate semaphore (F14).
func TestGateUnwrap(t *testing.T) {
	inner := &fakeEmbedder{vec: []float32{1}}
	g := WrapGate(inner)
	if got := g.Unwrap(); got != Embedder(inner) {
		t.Fatal("Unwrap did not return the inner embedder")
	}
}
