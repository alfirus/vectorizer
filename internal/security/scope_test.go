package security

import (
	"testing"
	"time"
)

func TestAllows(t *testing.T) {
	var nilClaims *Claims
	if !nilClaims.Allows("anything") {
		t.Error("nil claims (API-key mode) should allow everything")
	}
	admin := &Claims{Workspace: "a", Admin: true}
	if !admin.Allows("b") {
		t.Error("admin should allow any workspace")
	}
	unscoped := &Claims{}
	if !unscoped.Allows("b") {
		t.Error("claim without workspace should allow (nothing to enforce)")
	}
	scoped := &Claims{Workspace: "a"}
	if !scoped.Allows("a") {
		t.Error("scoped claim should allow its own workspace")
	}
	if scoped.Allows("b") {
		t.Error("scoped claim must deny other workspaces (F2)")
	}
	if scoped.Allows("") {
		t.Error("scoped claim must deny empty workspace (fail closed)")
	}
}

func TestParseRoundTrip(t *testing.T) {
	tok, err := GenerateToken("secret", "ws1", "peer1", false, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	claims, err := ParseToken("secret", tok)
	if err != nil {
		t.Fatal(err)
	}
	if claims.Workspace != "ws1" || claims.Peer != "peer1" || claims.Admin {
		t.Errorf("unexpected claims: %+v", claims)
	}
	if _, err := ParseToken("wrong", tok); err == nil {
		t.Error("wrong secret must fail")
	}
}
