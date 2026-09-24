package codeindex

import (
	"strings"
	"testing"
)

// Defect 4: config/schema/doc extensions must be accepted, not dropped.
func TestLanguageForCoverage(t *testing.T) {
	cases := map[string]string{
		"deploy/docker-compose.prod.yml": "yaml",
		"backend/migrations/014.up.sql":  "sql",
		"AGENTS.md":                      "markdown",
		"scripts/x.sh":                   "shell",
		"config.json":                    "json",
		"internal/store/store.go":        "go",
		"main.go":                        "go",
		"frontend/src/app/page.tsx":      "ts",
		"Dockerfile":                     "",
		".env":                           "",
		"unknown.zzz":                    "",
	}
	for p, want := range cases {
		if got := LanguageFor(p); got != want {
			t.Errorf("LanguageFor(%q) = %q, want %q", p, got, want)
		}
	}
}

// Defect 4: a symbol-less file must still produce retrievable text chunks.
func TestChunkFileTextOnly(t *testing.T) {
	yaml := "services:\n  backend:\n    environment:\n      ADK_BASE_URL: http://x\n"
	ch := ChunkFile("deploy/docker-compose.yml", yaml, 2000)
	if len(ch) == 0 {
		t.Fatal("yaml produced 0 chunks — config still invisible to search")
	}
	if ch[0].Metadata["chunk_type"] != "text" {
		t.Errorf("chunk_type = %q, want text", ch[0].Metadata["chunk_type"])
	}
	if !strings.Contains(ch[0].Document, "ADK_BASE_URL") {
		t.Errorf("chunk lost yaml content: %q", ch[0].Document)
	}
	if ch[0].Metadata["entities"] != "docker-compose.yml" {
		t.Errorf("entities = %q, want file base name", ch[0].Metadata["entities"])
	}
}

// Defect 4: SQL migrations likewise.
func TestChunkFileSQL(t *testing.T) {
	sql := "CREATE TABLE notifications_new AS SELECT * FROM notifications;\n"
	ch := ChunkFile("backend/migrations/014_partitioning.up.sql", sql, 2000)
	if len(ch) == 0 {
		t.Fatal("sql produced 0 chunks")
	}
	if !strings.Contains(ch[0].Document, "notifications_new") {
		t.Errorf("sql content missing: %q", ch[0].Document)
	}
}

// Regression guard: code files must keep the symbol path, not fall through
// to whole-text chunking (which would lose the overview + symbol entities).
func TestChunkFileCodeStillUsesSymbols(t *testing.T) {
	src := "package store\n\n// Hello does a thing.\nfunc Hello() string {\n\treturn \"hi\"\n}\n"
	ch := ChunkFile("store.go", src, 2000)
	if len(ch) == 0 {
		t.Fatal("go file produced 0 chunks")
	}
	if ch[0].Metadata["chunk_type"] != "file_overview" {
		t.Errorf("first chunk_type = %q, want file_overview", ch[0].Metadata["chunk_type"])
	}
	found := false
	for _, c := range ch {
		if c.Metadata["chunk_type"] == "symbol" && strings.Contains(c.Document, "func Hello") {
			found = true
		}
	}
	if !found {
		t.Errorf("no symbol chunk containing func Hello; chunks=%d", len(ch))
	}
}

// Defect 6: splitText must respect the size budget and lose nothing.
func TestSplitTextRespectsBudgetAndKeepsContent(t *testing.T) {
	big := strings.Repeat("line of config\n", 500)
	parts := splitText(big, 2000)
	if len(parts) < 2 {
		t.Fatalf("expected multiple parts, got %d", len(parts))
	}
	for i, p := range parts {
		if len(p) > 2100 {
			t.Errorf("part %d len %d exceeds budget", i, len(p))
		}
	}
	if strings.Join(parts, "") != big {
		t.Errorf("round-trip lost content: got %d bytes, want %d",
			len(strings.Join(parts, "")), len(big))
	}
}

func TestSplitTextTinyBudgetStillRoundTrips(t *testing.T) {
	src := "a\nb\nc\nd\ne\n"
	parts := splitText(src, 4)
	if strings.Join(parts, "") != src {
		t.Errorf("round-trip mismatch: %q", strings.Join(parts, ""))
	}
}
