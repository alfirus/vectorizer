package store

import "testing"

// ResolveWorkspaceID is the single choke point for workspace identity.
// UUID-style IDs (legacy POST /workspaces output) must pass through
// UNCHANGED so old collections stay addressable until an explicit rename.
func TestResolveWorkspaceID(t *testing.T) {
	cases := map[string]string{
		"pilotv4":  "pilotv4",
		"PilotV4":  "pilotv4",
		"pilot-v4": "pilot_v4",
		"":         "default",
		// Legacy UUID form: untouched, never remapped.
		"fce1f490-d67d-4cbe-9b68-0a5e655a0679": "fce1f490-d67d-4cbe-9b68-0a5e655a0679",
	}
	for in, want := range cases {
		if got := ResolveWorkspaceID(in); got != want {
			t.Errorf("ResolveWorkspaceID(%q) = %q, want %q", in, got, want)
		}
	}
}
