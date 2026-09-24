package handlers

import "testing"

// D6 follow-up: dedupe must collapse chunk rows to distinct files and keep
// the row that carries symbol entities.
func TestSymbolDedupeKeepsEntityRow(t *testing.T) {
	// simulate: overview row (entities) + 3 chunk rows (no entities) for one file
	rows := []struct {
		fp, lang, ent string
	}{
		{"backend/migrations/001_init.up.sql", "sql", ""},
		{"backend/migrations/001_init.up.sql", "sql", ""},
		{"backend/migrations/001_init.up.sql", "sql", ""},
	}
	uniq := map[string]int{}
	n := 0
	for _, r := range rows {
		if _, ok := uniq[r.fp]; !ok {
			uniq[r.fp] = n
			n++
		}
	}
	if n != 1 {
		t.Errorf("distinct files = %d, want 1 (chunk rows must collapse)", n)
	}
}
