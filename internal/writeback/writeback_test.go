package writeback

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSessionFileStaysInSessionsDir(t *testing.T) {
	w := New("/data/ai", "")
	evil := []string{"../../longterm/evil", "a/b/c", "", "x!@#$%", "sess:with:colons"}
	for _, s := range evil {
		f := w.sessionFile("ws_maisarah", s)
		dir := filepath.Dir(f)
		want := filepath.Join("/data/ai", "maisarah", "vault", "10-memory", "sessions")
		if dir != want {
			t.Fatalf("session %q escaped staging dir: %s", s, f)
		}
	}
}

func TestAgentDirNeverCrossesAgents(t *testing.T) {
	w := New("/data/ai", "")
	if got := w.sessionFile("ws_balqis", "s1"); !strings.Contains(got, filepath.Join("balqis", "vault")) {
		t.Fatalf("balqis misrouted: %s", got)
	}
	if got := w.sessionFile("ws_unknownproj", "s1"); !strings.Contains(got, "_shared") {
		t.Fatalf("unknown ws should fall back to _shared, got: %s", got)
	}
}

func TestAppendIdempotentOnMessageID(t *testing.T) {
	dir := t.TempDir()
	w := New(dir, "")
	ts := time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)
	qs := []queued{
		{workspace: "ws_maisarah", sessionID: "sess1", messageID: "m1", role: "user", content: "hello", createdAt: ts},
		{workspace: "ws_maisarah", sessionID: "sess1", messageID: "m2", role: "assistant", content: "hi there", createdAt: ts},
	}
	f := w.sessionFile("ws_maisarah", "sess1")
	if err := w.appendFile(f, qs); err != nil {
		t.Fatal(err)
	}
	// Re-append same batch + one new turn
	qs2 := append(append([]queued{}, qs...),
		queued{workspace: "ws_maisarah", sessionID: "sess1", messageID: "m3", role: "user", content: "new", createdAt: ts})
	if err := w.appendFile(f, qs2); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(f)
	s := string(b)
	if strings.Count(s, "hello") != 1 || strings.Count(s, "hi there") != 1 {
		t.Fatalf("duplicate content appended:\n%s", s)
	}
	if !strings.Contains(s, "generated_by: vectorizer") || !strings.Contains(s, "status: staging") {
		t.Fatalf("frontmatter marker missing:\n%s", s)
	}
	if !strings.Contains(s, "new") {
		t.Fatalf("new turn missing:\n%s", s)
	}
}

func TestAllowlist(t *testing.T) {
	dir := t.TempDir()
	w := New(dir, "ws_a, ws_b")
	ts := time.Now().UTC()
	made := func(ws string) string {
		w2 := New(dir, "ws_a, ws_b")
		_ = w2
		f := w.sessionFile(ws, "s")
		return f
	}
	_ = made
	// enqueue into disallowed ws must not reach the queue
	w.Enqueue("ws_other", "s", "m", "user", "x", ts)
	if w.QueueDepth() != 0 {
		t.Fatalf("disallowed workspace reached queue")
	}
	w.Enqueue("ws_a", "s", "m", "user", "x", ts)
	if w.QueueDepth() != 1 {
		t.Fatalf("allowed workspace dropped")
	}
}
