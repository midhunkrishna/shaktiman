package mcp

import "testing"

func TestMode_Valid(t *testing.T) {
	t.Parallel()
	for _, m := range []Mode{ModeLocate, ModeFull} {
		if !m.Valid() {
			t.Errorf("%q should be a valid Mode", m)
		}
	}
	if Mode("partial").Valid() {
		t.Error("'partial' should be invalid Mode")
	}
}

func TestScope_Valid(t *testing.T) {
	t.Parallel()
	for _, s := range []Scope{ScopeImpl, ScopeTest, ScopeAll} {
		if !s.Valid() {
			t.Errorf("%q should be a valid Scope", s)
		}
	}
	if Scope("none").Valid() {
		t.Error("'none' should be invalid Scope")
	}
}

func TestDirection_Valid(t *testing.T) {
	t.Parallel()
	for _, d := range []Direction{DirectionCallers, DirectionCallees, DirectionBoth} {
		if !d.Valid() {
			t.Errorf("%q should be a valid Direction", d)
		}
	}
	if Direction("sideways").Valid() {
		t.Error("'sideways' should be invalid Direction")
	}
}

// TestAllScopes_OrderIsStable pins the public order so existing
// integrations that read the MCP tool enum metadata don't see
// surprising shuffles.
func TestAllScopes_OrderIsStable(t *testing.T) {
	t.Parallel()
	got := AllScopes()
	want := []string{"impl", "test", "all"}
	if len(got) != len(want) {
		t.Fatalf("len = %d, want %d", len(got), len(want))
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("AllScopes()[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestAllDirections_OrderIsStable(t *testing.T) {
	t.Parallel()
	got := AllDirections()
	want := []string{"callers", "callees", "both"}
	if len(got) != len(want) {
		t.Fatalf("len = %d, want %d", len(got), len(want))
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("AllDirections()[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}
