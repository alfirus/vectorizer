package grpc

import (
	"context"
	"testing"
)

// gRPC contract: unauthenticated-by-design surface must at least answer
// Health (liveness), pinned to the same version story as REST.
func TestHealth(t *testing.T) {
	s := New(nil, nil)
	resp, err := s.Health(context.Background(), nil)
	if err != nil {
		t.Fatalf("Health: %v", err)
	}
	if resp.GetStatus() != "ok" {
		t.Fatalf("status = %q, want ok", resp.GetStatus())
	}
	if resp.GetVersion() == "" {
		t.Fatal("version must be set")
	}
}
