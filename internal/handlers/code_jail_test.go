package handlers

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWithinDir(t *testing.T) {
	if !withinDir("/a/b", "/a/b") {
		t.Error("root itself should be within")
	}
	if !withinDir("/a/b", "/a/b/c/d.go") {
		t.Error("child should be within")
	}
	if withinDir("/a/b", "/a/bc/x") {
		t.Error("sibling prefix must not match")
	}
	if withinDir("/a/b", "/a/../etc/passwd") {
		t.Error("lexical escape must not match")
	}
}

func TestJailCodePath(t *testing.T) {
	base := t.TempDir()
	allowed := filepath.Join(base, "repo")
	if err := os.MkdirAll(allowed, 0o755); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(base, "secret")
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODE_INDEX_ROOT", allowed)

	if _, err := jailCodePath(allowed); err != nil {
		t.Errorf("allowlisted root should jail-pass: %v", err)
	}
	if _, err := jailCodePath(outside); err == nil {
		t.Error("path outside roots must be rejected (F5)")
	}
	// symlink inside the jail pointing out must fail closed
	link := filepath.Join(allowed, "evil")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("cannot create symlink: %v", err)
	}
	if _, err := jailCodePath(link); err == nil {
		t.Error("escaping symlink must be rejected (F5)")
	}
}
