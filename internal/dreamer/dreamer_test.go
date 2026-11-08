package dreamer

import (
	"testing"
	"time"
)

// Double Stop must not panic (F10: bare close(stop) panicked on 2nd call).
func TestStopIdempotent(t *testing.T) {
	d := New(nil, nil, time.Minute)
	d.Stop()
	d.Stop()
}

// Non-positive interval must fall back to the 3h default.
func TestNewDefaultInterval(t *testing.T) {
	d := New(nil, nil, 0)
	if d == nil {
		t.Fatal("New returned nil")
	}
	if got := d.interval; got != 3*time.Hour {
		t.Fatalf("interval = %v, want 3h", got)
	}
}

// Start with nil brain must be a no-op that Stop can still follow.
func TestStartNilBrain(t *testing.T) {
	d := New(nil, nil, time.Minute)
	d.Start()
	d.Stop()
}
