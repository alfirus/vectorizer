package store

import (
	"reflect"
	"testing"
)

// The caller parser is pure so the migration cannot silently break callers():
// CodeCallers feeds it rows from BOTH the codeedges sidecar and the main
// collection, and the same edge arriving twice must yield one caller.

func TestCodeCallersFromPremiseDefines(t *testing.T) {
	got := codeCallersFrom([]map[string]interface{}{
		{"premise_ids": "defines:handleMentionEvent"},
		{"premise_ids": "defines:agentIsLive, defines:claimOpenTasks"},
	})
	want := []string{"handleMentionEvent", "agentIsLive", "claimOpenTasks"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

// The whole point of the sidecar move: an edge present in both the sidecar
// and the main collection (mid-migration) must not double the answer.
func TestCodeCallersFromDedupesAcrossSources(t *testing.T) {
	edge := map[string]interface{}{"premise_ids": "defines:main"}
	got := codeCallersFrom([]map[string]interface{}{edge, edge, edge})
	if !reflect.DeepEqual(got, []string{"main"}) {
		t.Errorf("got %v, want [main] — duplicate rows must collapse", got)
	}
}

// Order must stay stable: callers() answers are compared to recorded baselines
// (handleMentionEvent -> [main], agentIsLive -> [handleMentionEvent, ...]).
func TestCodeCallersFromPreservesFirstSeenOrder(t *testing.T) {
	got := codeCallersFrom([]map[string]interface{}{
		{"premise_ids": "defines:zeta"},
		{"premise_ids": "defines:alpha"},
		{"premise_ids": "defines:zeta"}, // repeat of the first
	})
	want := []string{"zeta", "alpha"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

// Non-defines premises (e.g. imports:) must be ignored — only defines: is a
// caller of a symbol.
func TestCodeCallersFromIgnoresNonDefinePremises(t *testing.T) {
	got := codeCallersFrom([]map[string]interface{}{
		{"premise_ids": "imports:fmt, file:backend/main.go"},
	})
	if len(got) != 0 {
		t.Errorf("got %v, want empty — imports/file are not callers", got)
	}
}

// Fallback: with no defines: at all, supporting_message_ids is used. This
// preserves the original reader's behaviour for edges written the other way.
func TestCodeCallersFromSupportingFallback(t *testing.T) {
	got := codeCallersFrom([]map[string]interface{}{
		{"premise_ids": "imports:fmt", "supporting_message_ids": "file:backend/main.go"},
	})
	if !reflect.DeepEqual(got, []string{"file:backend/main.go"}) {
		t.Errorf("got %v, want [file:backend/main.go]", got)
	}
}

// Nil metadata rows arrive when Chroma omits metadatas for a doc; the reader
// must skip them rather than panic (the original loop nil-checked too).
func TestCodeCallersFromSkipsNilAndEmpty(t *testing.T) {
	got := codeCallersFrom([]map[string]interface{}{
		nil,
		{},
		{"premise_ids": ""},
		{"premise_ids": "defines:real"},
	})
	if !reflect.DeepEqual(got, []string{"real"}) {
		t.Errorf("got %v, want [real]", got)
	}
}

// Empty input (no edges at all — e.g. workspace never indexed) returns nil,
// which callers() reports as count 0 rather than an error.
func TestCodeCallersFromEmpty(t *testing.T) {
	if got := codeCallersFrom(nil); got != nil {
		t.Errorf("got %v, want nil", got)
	}
	if got := codeCallersFrom([]map[string]interface{}{}); got != nil {
		t.Errorf("got %v, want nil", got)
	}
}

// Whitespace and case: premise tokens arrive CSV-joined from metadata, so a
// leading space must not produce a caller named " handleMentionEvent".
func TestCodeCallersFromTrimsWhitespace(t *testing.T) {
	got := codeCallersFrom([]map[string]interface{}{
		{"premise_ids": "  defines:handleMentionEvent , defines:agentIsLive"},
	})
	want := []string{"handleMentionEvent", "agentIsLive"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

// The sidecar name is what the migration creates; if it drifts from what
// AddCodeEdge writes, callers() silently reads an empty collection.
func TestCodeEdgeCollectionName(t *testing.T) {
	s := &Store{}
	cases := map[string]string{
		"code_aict": "ws_code_aict_codeedges",
		"code_ags":  "ws_code_ags_codeedges",
		"pilotv4":   "ws_pilotv4_codeedges",
		"PilotV4":   "ws_pilotv4_codeedges", // canonicalised
		"":          "ws_default_codeedges",
	}
	for in, want := range cases {
		if got := s.codeEdgeCollection(in); got != want {
			t.Errorf("codeEdgeCollection(%q) = %q, want %q", in, got, want)
		}
	}
}

// Chroma rejects names that are not 3-63 of [a-zA-Z0-9._-] starting and
// ending alnum (I hit this with __probe_*). Assert every name we can produce
// is acceptable, so a workspace id can never yield an uncreatable collection.
func TestCodeEdgeCollectionNameIsValidForChroma(t *testing.T) {
	s := &Store{}
	for _, ws := range []string{"code_aict", "code_ags", "a", "ws_1", "PilotV4", "x.y-z_9"} {
		name := s.codeEdgeCollection(ws)
		if len(name) < 3 || len(name) > 63 {
			t.Errorf("name %q length %d outside 3..63", name, len(name))
		}
		if name[0] == '_' || name[0] == '.' || name[0] == '-' {
			t.Errorf("name %q must start with alnum", name)
		}
		last := name[len(name)-1]
		if last == '_' || last == '.' || last == '-' {
			t.Errorf("name %q must end with alnum", name)
		}
	}
}
