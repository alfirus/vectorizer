package security

import (
	"testing"
	"time"
)

// Auth-path contract (feeds R1/R7 confidence): mint → parse roundtrip must
// preserve workspace, peer, and admin claims.
func TestTokenRoundtrip(t *testing.T) {
	tok, err := GenerateToken("test-secret", "ws-alpha", "peer-1", true, time.Hour)
	if err != nil {
		t.Fatalf("GenerateToken: %v", err)
	}
	claims, err := ParseToken("test-secret", tok)
	if err != nil {
		t.Fatalf("ParseToken: %v", err)
	}
	if claims.Workspace != "ws-alpha" || claims.Peer != "peer-1" || !claims.Admin {
		t.Fatalf("claims mismatch: %+v", claims)
	}
}

// A token signed with a different secret must not verify.
func TestWrongSecretRejected(t *testing.T) {
	tok, err := GenerateToken("secret-a", "ws", "", false, time.Hour)
	if err != nil {
		t.Fatalf("GenerateToken: %v", err)
	}
	if _, err := ParseToken("secret-b", tok); err == nil {
		t.Fatal("expected verification failure with wrong secret")
	}
}

// Expired tokens must not verify.
func TestExpiredTokenRejected(t *testing.T) {
	tok, err := GenerateToken("s", "ws", "", false, -time.Hour)
	if err != nil {
		t.Fatalf("GenerateToken: %v", err)
	}
	if _, err := ParseToken("s", tok); err == nil {
		t.Fatal("expected expiry failure")
	}
}

func TestExtractBearer(t *testing.T) {
	if got := ExtractBearer("Bearer abc123"); got != "abc123" {
		t.Fatalf("got %q", got)
	}
	if got := ExtractBearer("bearer xyz"); got != "xyz" {
		t.Fatalf("scheme match must be case-insensitive, got %q", got)
	}
	if got := ExtractBearer("Token abc"); got != "" {
		t.Fatalf("non-bearer scheme must yield empty, got %q", got)
	}
}
