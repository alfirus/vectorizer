package models

import "testing"

// Workspace identity rule (2026-09-05): the human-readable name IS the ID.
// POST /workspaces used to mint a UUID while every other path (messages,
// code index, search) used the name — creating two parallel universes.
// These tests lock in canonicalization so the bug can't regress.
func TestCanonicalWorkspaceID(t *testing.T) {
	cases := map[string]string{
		"pilotv4":      "pilotv4",
		"PilotV4":      "pilotv4",
		"Pilot V4":     "pilot_v4",
		"pilot-v4":     "pilot_v4",
		"  maisarah  ": "maisarah",
		"code_pilotv4": "code_pilotv4",
		"":             "default",
		"!!!":          "default",
	}
	for in, want := range cases {
		if got := CanonicalWorkspaceID(in); got != want {
			t.Errorf("CanonicalWorkspaceID(%q) = %q, want %q", in, got, want)
		}
	}
	// Idempotency: canonicalizing twice = canonicalizing once.
	for _, in := range []string{"PilotV4", "pilot-v4", "Pilot V4"} {
		once := CanonicalWorkspaceID(in)
		if twice := CanonicalWorkspaceID(once); twice != once {
			t.Errorf("not idempotent: %q -> %q -> %q", in, once, twice)
		}
	}
}

func TestNewWorkspaceNameIsID(t *testing.T) {
	ws := NewWorkspace("PilotV4")
	if ws.ID != "pilotv4" {
		t.Errorf("NewWorkspace ID = %q, want %q", ws.ID, "pilotv4")
	}
	if ws.Name != ws.ID {
		t.Errorf("Name %q != ID %q — name must equal ID", ws.Name, ws.ID)
	}
}
