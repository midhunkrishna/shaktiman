package parser

import (
	"testing"

	tree_sitter "github.com/tree-sitter/go-tree-sitter"

	"github.com/shaktimanai/shaktiman/internal/types"
)

// TestWalkHelpers_NilSafety verifies that the iter helpers tolerate nil
// nodes; tree-sitter ChildByFieldName returns nil for missing fields, so
// passing such results into the helpers must not panic.
func TestWalkHelpers_NilSafety(t *testing.T) {
	t.Parallel()
	called := false
	iterNamedChildren(nil, func(_ *tree_sitter.Node) { called = true })
	iterChildren(nil, func(_ *tree_sitter.Node) { called = true })
	if got := findNamedChild(nil, func(_ *tree_sitter.Node) bool { return true }); got != nil {
		t.Fatalf("findNamedChild(nil) = %v, want nil", got)
	}
	if called {
		t.Fatal("nil node must not invoke the closure")
	}
}

// TestVisibilityFor verifies the simple two-state visibility map.
func TestVisibilityFor(t *testing.T) {
	t.Parallel()
	if got := visibilityFor(true); got != "exported" {
		t.Errorf("visibilityFor(true) = %q, want %q", got, "exported")
	}
	if got := visibilityFor(false); got != "internal" {
		t.Errorf("visibilityFor(false) = %q, want %q", got, "internal")
	}
}

// TestBuildSymbol_FieldsAndVisibility verifies buildSymbol sets every field
// consistently. ChunkID resolution is exercised through the existing
// parser_test.go tests; here we only assert the wiring.
func TestBuildSymbol_FieldsAndVisibility(t *testing.T) {
	t.Parallel()
	chunks := []types.ChunkRecord{{StartLine: 1, EndLine: 10}}

	exp := buildSymbol("Foo", "function", 5, true, "(x int) int", chunks)
	if exp.Name != "Foo" || exp.Kind != "function" || exp.Line != 5 ||
		exp.Signature != "(x int) int" || !exp.IsExported ||
		exp.Visibility != "exported" || exp.ChunkID != 0 {
		t.Fatalf("exported symbol mis-built: %+v", exp)
	}

	internal := buildSymbol("bar", "variable", 7, false, "", chunks)
	if internal.Visibility != "internal" || internal.IsExported {
		t.Fatalf("internal visibility wrong: %+v", internal)
	}

	// Line outside any chunk → ChunkID == -1 sentinel.
	orphan := buildSymbol("x", "variable", 99, false, "", chunks)
	if orphan.ChunkID != -1 {
		t.Fatalf("orphan ChunkID = %d, want -1", orphan.ChunkID)
	}
}
