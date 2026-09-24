package store

import (
	"fmt"
	"os"
	"sync/atomic"
	"time"

	"github.com/alfirus/vectorizer/internal/embedding"
)

// EmbeddingProbe is the last observed state of the embedding provider.
//
// Why this exists: /health used to report "ok" from CONFIG ONLY (provider name
// + model string), so a dead LM Studio was invisible — and health.go's callers
// hardcoded "healthy" on top of that. A 30-minute embedder outage during a
// 2h4m index run went completely unreported.
type EmbeddingProbe struct {
	Status    string    // "ok" | "degraded" | "unknown"
	LastOK    time.Time // zero if never succeeded
	LastErr   string    // most recent failure, empty when ok
	CheckedAt time.Time // when the last probe finished
}

// probeTimeout bounds ONE embed call inside the background probe. Deliberately
// NOT EMBED_TIMEOUT_SECS (600s): this runs off the request path, so a long
// timeout only delays status updates. Generous enough for JIT model loading.
const probeTimeout = 8 * time.Second

// probeInterval is how often the background probe refreshes. Matches the
// Docker healthcheck interval (30s) so the two stay roughly in step.
const probeInterval = 30 * time.Second

// embedProbe holds the latest probe result. Package-level (like GlobalMetrics
// and GlobalUsage) so /health can read it without a Store field — keeps Store
// copyable and keeps the read lock-free, because /health MUST NOT block.
var embedProbe atomic.Pointer[EmbeddingProbe]

// EmbeddingHealth returns the cached probe result and never touches the
// network. Callers on the request path (health endpoints) rely on that: a
// synchronous probe would hang /health for up to EMBED_TIMEOUT_SECS and the
// Docker healthcheck (5s timeout, 3 retries) would restart the container.
func EmbeddingHealth() EmbeddingProbe {
	if p := embedProbe.Load(); p != nil {
		return *p
	}
	return EmbeddingProbe{Status: "unknown"}
}

// EmbeddingModelName is the configured model, independent of whether the
// provider answers — so a degraded report still names what is down.
func EmbeddingModelName() string {
	if m := os.Getenv("EMBED_MODEL"); m != "" {
		return m
	}
	return "text-embedding-nomic-embed-text-v2"
}

// StartEmbeddingProbe probes the embedder every probeInterval in the goroutine
// it spawns, seeding one result immediately. Returns a stop function.
//
// The probe runs in its own goroutine per tick so a hung embedder cannot
// stall the ticker loop; the per-probe timeout reaps it.
func StartEmbeddingProbe(embed embedding.Embedder) (stop func()) {
	if embed == nil {
		return func() {}
	}
	done := make(chan struct{})
	var once atomic.Bool

	run := func() {
		res := probeOnce(embed, probeTimeout)
		embedProbe.Store(&res)
	}
	go func() {
		run() // seed immediately — never serve "unknown" for a full interval
		t := time.NewTicker(probeInterval)
		defer t.Stop()
		for {
			select {
			case <-t.C:
				// Guard against pile-up if an embed call outlives its timeout.
				if once.CompareAndSwap(false, true) {
					go func() {
						run()
						once.Store(false)
					}()
				}
			case <-done:
				return
			}
		}
	}()
	var closed atomic.Bool
	return func() {
		if closed.CompareAndSwap(false, true) {
			close(done)
		}
	}
}

// probeOnce performs exactly one bounded embed call and reports the outcome.
// Pure with respect to package state — unit-testable.
func probeOnce(embed embedding.Embedder, timeout time.Duration) EmbeddingProbe {
	if embed == nil {
		return EmbeddingProbe{Status: "unknown", CheckedAt: time.Now()}
	}
	type outcome struct {
		err error
	}
	ch := make(chan outcome, 1) // buffered: the worker never blocks on send
	go func() {
		_, err := embed.EmbedSingle("health probe")
		ch <- outcome{err: err}
	}()

	now := time.Now()
	select {
	case o := <-ch:
		if o.err != nil {
			return EmbeddingProbe{Status: "degraded", LastErr: o.err.Error(), CheckedAt: now}
		}
		return EmbeddingProbe{Status: "ok", LastOK: now, CheckedAt: now}
	case <-time.After(timeout):
		return EmbeddingProbe{
			Status:    "degraded",
			LastErr:   fmt.Sprintf("embed call exceeded %s", timeout),
			CheckedAt: now,
		}
	}
}
