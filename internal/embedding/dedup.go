package embedding

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"strconv"

	"golang.org/x/sync/singleflight"
)

var errEmptyResult = errors.New("empty embedding result")

// Dedup wraps an Embedder so concurrent identical Embed calls share one
// in-flight request instead of queueing duplicates on the model server.
//
// Why: code-index retries, rerank fan-out, and parallel ingest can fire the
// same texts at once. Against a slow local model each duplicate sits in the
// server queue; singleflight collapses them into one call.
type Dedup struct {
	inner Embedder
	group singleflight.Group
}

func WrapDedup(inner Embedder) *Dedup {
	return &Dedup{inner: inner}
}

func (d *Dedup) Embed(texts []string) ([]EmbeddingResult, error) {
	key := embedKey(texts)
	v, err, _ := d.group.Do(key, func() (interface{}, error) {
		return d.inner.Embed(texts)
	})
	if err != nil {
		return nil, err
	}
	return v.([]EmbeddingResult), nil
}

func (d *Dedup) EmbedSingle(text string) ([]float32, error) {
	results, err := d.Embed([]string{text})
	if err != nil {
		return nil, err
	}
	if len(results) == 0 || len(results[0].Vector) == 0 {
		return nil, errEmptyResult
	}
	return results[0].Vector, nil
}

func (d *Dedup) Model() string              { return d.inner.Model() }
func (d *Dedup) Dimensions() int            { return d.inner.Dimensions() }
func (d *Dedup) SetModel(m string)          { d.inner.SetModel(m) }
func (d *Dedup) SetDimensions(x int)        { d.inner.SetDimensions(x) }
func (d *Dedup) SetBaseURL(u string)        { d.inner.SetBaseURL(u) }

func embedKey(texts []string) string {
	h := sha256.New()
	for _, t := range texts {
		h.Write([]byte(t))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

// embedConcurrency returns EMBED_MAX_INFLIGHT (default 4): cap on concurrent
// embed calls so a burst of ingest/index work can't flood a local model.
func embedConcurrency() int {
	if v := os.Getenv("EMBED_MAX_INFLIGHT"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return 4
}

// Gate wraps an Embedder with a semaphore cap on in-flight calls.
type Gate struct {
	inner Embedder
	sem   chan struct{}
}

func WrapGate(inner Embedder) *Gate {
	return &Gate{inner: inner, sem: make(chan struct{}, embedConcurrency())}
}

func (g *Gate) Embed(texts []string) ([]EmbeddingResult, error) {
	g.sem <- struct{}{}
	defer func() { <-g.sem }()
	return g.inner.Embed(texts)
}

func (g *Gate) EmbedSingle(text string) ([]float32, error) {
	results, err := g.Embed([]string{text})
	if err != nil {
		return nil, err
	}
	if len(results) == 0 || len(results[0].Vector) == 0 {
		return nil, errEmptyResult
	}
	return results[0].Vector, nil
}

func (g *Gate) Model() string       { return g.inner.Model() }
func (g *Gate) Dimensions() int     { return g.inner.Dimensions() }
func (g *Gate) SetModel(m string)   { g.inner.SetModel(m) }
func (g *Gate) SetDimensions(x int) { g.inner.SetDimensions(x) }
func (g *Gate) SetBaseURL(u string) { g.inner.SetBaseURL(u) }
