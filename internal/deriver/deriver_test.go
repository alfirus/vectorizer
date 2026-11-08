package deriver

import "testing"

// Double Stop must not panic (F10: bare close(stop) panicked on 2nd call).
func TestStopIdempotent(t *testing.T) {
	d := New(nil, nil)
	d.Stop()
	d.Stop()
}

// Enqueue beyond capacity must count drops instead of blocking or losing silently.
func TestEnqueueDropsCounted(t *testing.T) {
	d := New(nil, nil)
	for i := 0; i < 1005; i++ {
		d.Enqueue("ws", "s", "p", "m", "content")
	}
	if got := d.Drops(); got != 5 {
		t.Fatalf("drops = %d, want 5 (queue cap 1000)", got)
	}
	if got := d.QueueDepth(); got != 1000 {
		t.Fatalf("queue depth = %d, want 1000", got)
	}
}

// Start with nil brain must be a no-op that Stop can still follow.
func TestStartNilBrain(t *testing.T) {
	d := New(nil, nil)
	d.Start()
	d.Stop()
}
