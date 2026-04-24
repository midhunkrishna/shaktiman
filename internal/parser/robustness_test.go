package parser

import (
	"context"
	"strings"
	"testing"

	"github.com/shaktimanai/shaktiman/internal/types"
)

// TestFindContainingChunk_ReturnsMinusOneOnMiss verifies the sentinel-value
// contract. Previously the function returned 0 for both "line is in chunk 0"
// and "line is in no chunk", silently mis-attributing orphan symbols to the
// header chunk.
func TestFindContainingChunk_ReturnsMinusOneOnMiss(t *testing.T) {
	t.Parallel()

	chunks := []types.ChunkRecord{
		{StartLine: 1, EndLine: 10},
		{StartLine: 11, EndLine: 20},
	}

	// In-range cases.
	if got := findContainingChunk(5, chunks); got != 0 {
		t.Errorf("line 5 should hit chunk 0; got %d", got)
	}
	if got := findContainingChunk(15, chunks); got != 1 {
		t.Errorf("line 15 should hit chunk 1; got %d", got)
	}

	// Miss cases must now return -1, not 0.
	if got := findContainingChunk(25, chunks); got != -1 {
		t.Errorf("line 25 (no chunk) should return -1; got %d", got)
	}
	if got := findContainingChunk(0, chunks); got != -1 {
		t.Errorf("line 0 (before any chunk) should return -1; got %d", got)
	}
	if got := findContainingChunk(0, nil); got != -1 {
		t.Errorf("no chunks at all should return -1; got %d", got)
	}
}

// TestParse_RecoverFromPanic verifies that a panic inside the parser pipeline
// is caught and converted to an error instead of taking down the goroutine.
// We exercise the recover path by forcing a panic via an injected token
// counter (easiest entry point without mocking the grammar itself).
func TestParse_RecoverFromPanic(t *testing.T) {
	t.Parallel()

	p, err := NewParser()
	if err != nil {
		t.Fatalf("NewParser: %v", err)
	}
	defer p.Close()

	// Replace the token counter with one that panics on any call.
	// Parse invokes tokens.Count inside chunkFile; this exercises the
	// recover at the top of Parse.
	p.tokens = &tokenCounter{} // zero-value counter — its internal fields are nil
	// Zero-value tokenCounter panics on Count because its encoder is nil.

	_, parseErr := p.Parse(context.Background(), ParseInput{
		FilePath: "test.go",
		Content:  []byte("package main\nfunc F() {}\n"),
		Language: "go",
	})
	if parseErr == nil {
		t.Fatalf("expected error from recovered panic, got nil")
	}
	if !strings.Contains(parseErr.Error(), "panic") {
		t.Errorf("error should mention panic recovery, got: %v", parseErr)
	}
}

// TestEdges_DedupIncludesQualifiedDst verifies that two imports of the same
// short name from different modules produce two distinct edges, not one.
// Pre-fix, the dedup key was src|dst|kind and silently collapsed them,
// discarding the second qualified path.
func TestEdges_DedupIncludesQualifiedDst(t *testing.T) {
	t.Parallel()

	ctx := &edgeContext{
		seen: make(map[string]bool),
	}
	ctx.addEdgeQualified("main", "Foo", "pkg_a.Foo", "imports")
	ctx.addEdgeQualified("main", "Foo", "pkg_b.Foo", "imports")

	if len(ctx.edges) != 2 {
		t.Fatalf("expected 2 edges for different qualified paths, got %d: %+v", len(ctx.edges), ctx.edges)
	}
	qualified := map[string]bool{
		ctx.edges[0].DstQualifiedName: true,
		ctx.edges[1].DstQualifiedName: true,
	}
	if !qualified["pkg_a.Foo"] || !qualified["pkg_b.Foo"] {
		t.Errorf("expected both qualified paths preserved, got %v", qualified)
	}
}

// TestEdges_DedupCollapsesExactDuplicates confirms the fix didn't break the
// existing guarantee: two identical edges (same src, dst, qualifiedDst, kind)
// still collapse to one.
func TestEdges_DedupCollapsesExactDuplicates(t *testing.T) {
	t.Parallel()

	ctx := &edgeContext{
		seen: make(map[string]bool),
	}
	ctx.addEdgeQualified("main", "Foo", "pkg_a.Foo", "imports")
	ctx.addEdgeQualified("main", "Foo", "pkg_a.Foo", "imports")

	if len(ctx.edges) != 1 {
		t.Fatalf("expected 1 edge for exact duplicate, got %d", len(ctx.edges))
	}
}
