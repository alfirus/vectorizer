package codeindex

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const sampleGo = `package store

import (
	"fmt"
	"strings"
)

// UpdateMessage rewrites a message.
func (s *Store) UpdateMessage(msg *Message, content string) (int, error) {
	return s.helper(content)
}

func (s *Store) helper(c string) int { return len(c) }

// Config holds settings.
type Config struct {
	Name string
}
`

func TestParseGo(t *testing.T) {
	fs := ParseFile("internal/store/x.go", sampleGo)
	if fs.Language != "go" {
		t.Fatalf("lang = %q", fs.Language)
	}
	if len(fs.Imports) != 2 {
		t.Errorf("imports = %v", fs.Imports)
	}
	names := map[string]bool{}
	for _, s := range fs.Symbols {
		names[s.Name] = true
	}
	for _, want := range []string{"UpdateMessage", "helper", "Config"} {
		if !names[want] {
			t.Errorf("missing symbol %q (got %v)", want, names)
		}
	}
	if !strings.Contains(fs.Overview, "UpdateMessage") {
		t.Errorf("overview missing symbol index: %q", fs.Overview)
	}
}

func TestChunkMerge(t *testing.T) {
	chunks := ChunkFile("x.go", sampleGo, 2000)
	if len(chunks) < 2 {
		t.Fatalf("want overview + symbol chunks, got %d", len(chunks))
	}
	if chunks[0].Metadata["chunk_type"] != "file_overview" {
		t.Errorf("chunk0 type = %q", chunks[0].Metadata["chunk_type"])
	}
	if chunks[1].Metadata["language"] != "go" {
		t.Errorf("chunk1 lang = %q", chunks[1].Metadata["language"])
	}
}

func TestCallEdges(t *testing.T) {
	fs := ParseFile("x.go", sampleGo)
	edges := CallEdges([]FileSymbols{fs})
	found := false
	for _, e := range edges {
		if e.Caller == "UpdateMessage" && e.Callee == "helper" {
			found = true
		}
	}
	if !found {
		t.Errorf("missing UpdateMessage->helper edge (got %v)", edges)
	}
}

func TestSelfIndex(t *testing.T) {
	// Dogfood: index this package's own extractor.
	src, err := os.ReadFile("extractor.go")
	if err != nil {
		t.Skip("run from package dir")
	}
	fs := ParseFile(filepath.Join("internal", "codeindex", "extractor.go"), string(src))
	if len(fs.Symbols) < 5 {
		t.Errorf("self-index found only %d symbols", len(fs.Symbols))
	}
}
