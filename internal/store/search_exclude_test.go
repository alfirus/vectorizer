package store

import (
	"testing"
)

// Fleet-wide search exclusion (t_ce4e18be, upstreamed via t_247beff1):
// finance eyes only — the finance workspace must never enter an unscoped
// fan-out, while scoped reads keep working (enforced at the call sites,
// not here).

func TestSearchExcludedDefaultCoversFinance(t *testing.T) {
	t.Setenv("SEARCH_EXCLUDE_WORKSPACES", "")
	if !SearchExcludedWorkspace("finance") {
		t.Fatal("default exclusion set must hide finance")
	}
	if SearchExcludedWorkspace("ags") {
		t.Fatal("default exclusion set must not hide ordinary workspaces")
	}
}

func TestSearchExcludedCSVParsing(t *testing.T) {
	t.Setenv("SEARCH_EXCLUDE_WORKSPACES", " finance ,PilotV4,, ")
	if !SearchExcludedWorkspace("finance") {
		t.Fatal("exact match must be excluded")
	}
	if !SearchExcludedWorkspace("FINANCE") {
		t.Fatal("matching must be case-insensitive")
	}
	if !SearchExcludedWorkspace("pilotv4") {
		t.Fatal("second CSV entry must be excluded")
	}
	if SearchExcludedWorkspace("finance_reports") {
		t.Fatal("prefix lookalikes must NOT be excluded")
	}
	if SearchExcludedWorkspace("") {
		t.Fatal("empty id must not be excluded")
	}
}

func TestSearchExcludedEmptyEnvHidesNothingExtra(t *testing.T) {
	t.Setenv("SEARCH_EXCLUDE_WORKSPACES", "   ")
	if SearchExcludedWorkspace("ags") {
		t.Fatal("blank env must not exclude ordinary workspaces")
	}
}
