package main

import (
	"testing"
	"time"
)

// Allow must enforce the per-key limit.
func TestRateLimiterEnforcesLimit(t *testing.T) {
	rl := newRateLimiter(3, time.Minute)
	for i := 0; i < 3; i++ {
		if !rl.Allow("k") {
			t.Fatalf("allow %d: unexpected deny", i)
		}
	}
	if rl.Allow("k") {
		t.Fatal("4th call over limit 3 must be denied")
	}
	if !rl.Allow("other") {
		t.Fatal("different key must have its own budget")
	}
}

// Expired entries must be evicted so the map cannot grow one entry per
// distinct key forever (F9).
func TestRateLimiterSweepEvictsExpired(t *testing.T) {
	rl := newRateLimiter(10, 50*time.Millisecond)
	for i := 0; i < 100; i++ {
		rl.Allow("key")
	}
	if got := rl.Size(); got != 1 {
		t.Fatalf("size = %d, want 1", got)
	}
	time.Sleep(100 * time.Millisecond) // let the window expire
	rl.lastSweep = time.Now().Add(-2 * rateLimiterSweepInterval)
	rl.Allow("fresh") // triggers a sweep
	if got := rl.Size(); got != 1 {
		t.Fatalf("size after sweep = %d, want 1 (only 'fresh')", got)
	}
}
