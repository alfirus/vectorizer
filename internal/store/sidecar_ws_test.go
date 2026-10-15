package store

import "testing"

// t_5f0d1cb0: sidecar collections must never surface as workspace ids.
func TestIsSidecarCollection(t *testing.T) {
	have := map[string]bool{
		"ws_code_aict":               true,
		"ws_code_aict_codeedges":     true,
		"ws_nadia":                   true,
		"ws_nadia_conclusions":       true,
		"ws_nadia_reasoning":         true,
		"ws_pilotv4":                 true,
		"ws_orphan_conclusions":      true, // parent ws_orphan MISSING
		"ws_conclusions_fan":         true, // real ws whose name starts with suffix word
		"health_check":               true, // non-ws collection
	}

	cases := map[string]bool{
		// Sidecars with a live parent: hidden.
		"ws_code_aict_codeedges": true,
		"ws_nadia_conclusions":   true,
		"ws_nadia_reasoning":     true,
		// Real workspaces (incl. parents of sidecars): kept.
		"ws_code_aict":   false,
		"ws_nadia":       false,
		"ws_pilotv4":     false,
		// Orphan sidecar (parent gone): kept — old behaviour, health flags it.
		"ws_orphan_conclusions": false,
		// Real workspace merely containing a suffix word: kept.
		"ws_conclusions_fan": false,
		// Degenerate: kept or hidden must not panic; hidden is fine.
		// Non-ws collections: never sidecars.
		"health_check": false,
	}
	for name, want := range cases {
		if got := isSidecarCollection(name, have); got != want {
			t.Errorf("isSidecarCollection(%q) = %v, want %v", name, got, want)
		}
	}
}
