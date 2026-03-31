package parser

import (
	"strings"
	"testing"

	"github.com/shaktimanai/shaktiman/internal/types"
)

func TestSplitLargeChunks_Basic(t *testing.T) {
	t.Parallel()
	p, err := NewParser()
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()

	// Build a chunk that exceeds maxChunkTokens
	var sb strings.Builder
	for i := 0; i < 200; i++ {
		sb.WriteString("func doSomething() { return nil }\n")
	}
	content := sb.String()
	tokens := p.tokens.Count(content)
	if tokens <= maxChunkTokens {
		t.Skipf("test content only %d tokens, need > %d", tokens, maxChunkTokens)
	}

	chunks := []types.ChunkRecord{{
		ChunkIndex: 0,
		SymbolName: "BigFunc",
		Kind:       "function",
		StartLine:  1,
		EndLine:    200,
		Content:    content,
		TokenCount: tokens,
	}}

	result := p.splitLargeChunks(chunks)

	if len(result) < 2 {
		t.Fatalf("expected >= 2 chunks after split, got %d", len(result))
	}

	// Each chunk must be <= maxChunkTokens
	for i, c := range result {
		if c.TokenCount > maxChunkTokens {
			t.Errorf("chunk %d: TokenCount=%d exceeds max=%d", i, c.TokenCount, maxChunkTokens)
		}
	}

	// Symbol name and kind preserved
	for i, c := range result {
		if c.SymbolName != "BigFunc" {
			t.Errorf("chunk %d: SymbolName=%q, want %q", i, c.SymbolName, "BigFunc")
		}
		if c.Kind != "function" {
			t.Errorf("chunk %d: Kind=%q, want %q", i, c.Kind, "function")
		}
	}

	// Line ranges must be contiguous and cover the original range
	if result[0].StartLine != 1 {
		t.Errorf("first chunk StartLine=%d, want 1", result[0].StartLine)
	}
	if result[len(result)-1].EndLine != 200 {
		t.Errorf("last chunk EndLine=%d, want 200", result[len(result)-1].EndLine)
	}
}

func TestSplitLargeChunks_SmallChunkPassthrough(t *testing.T) {
	t.Parallel()
	p, err := NewParser()
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()

	chunks := []types.ChunkRecord{{
		ChunkIndex: 0,
		SymbolName: "Small",
		Kind:       "function",
		StartLine:  1,
		EndLine:    5,
		Content:    "func small() {}",
		TokenCount: 4,
	}}

	result := p.splitLargeChunks(chunks)
	if len(result) != 1 {
		t.Fatalf("expected 1 chunk (passthrough), got %d", len(result))
	}
	if result[0].Content != "func small() {}" {
		t.Errorf("content changed during passthrough")
	}
}

func TestSplitLargeChunks_NoQuadratic(t *testing.T) {
	t.Parallel()
	p, err := NewParser()
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()

	// Build a 2000-line chunk — the old O(n²) code would be very slow
	var sb strings.Builder
	for i := 0; i < 2000; i++ {
		sb.WriteString("var x" + strings.Repeat("a", 20) + " = 42 // some comment padding here\n")
	}
	content := sb.String()
	tokens := p.tokens.Count(content)

	chunks := []types.ChunkRecord{{
		ChunkIndex: 0,
		SymbolName: "HugeBlock",
		Kind:       "block",
		StartLine:  1,
		EndLine:    2000,
		Content:    content,
		TokenCount: tokens,
	}}

	result := p.splitLargeChunks(chunks)

	// Must produce multiple chunks
	if len(result) < 2 {
		t.Fatalf("expected multiple chunks from 2000-line block, got %d", len(result))
	}

	// Verify all chunks are within bounds
	for i, c := range result {
		if c.TokenCount > maxChunkTokens {
			t.Errorf("chunk %d: TokenCount=%d exceeds max=%d", i, c.TokenCount, maxChunkTokens)
		}
		if c.TokenCount == 0 {
			t.Errorf("chunk %d: has 0 tokens", i)
		}
	}

	// Verify no content is lost: total token count must match original
	totalTokens := 0
	for _, c := range result {
		totalTokens += c.TokenCount
	}
	// Token count across chunks may differ slightly from single-pass count
	// due to tokenizer boundary effects, but should be close
	originalTokens := p.tokens.Count(content)
	drift := float64(totalTokens-originalTokens) / float64(originalTokens)
	if drift < -0.05 || drift > 0.05 {
		t.Errorf("token count drift too large: total=%d, original=%d, drift=%.2f%%",
			totalTokens, originalTokens, drift*100)
	}
}

func TestSplitLargeChunks_PreservesParentIndex(t *testing.T) {
	t.Parallel()
	p, err := NewParser()
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()

	var sb strings.Builder
	for i := 0; i < 200; i++ {
		sb.WriteString("func doSomething() { return nil }\n")
	}
	content := sb.String()
	parentIdx := 0

	chunks := []types.ChunkRecord{{
		ChunkIndex:  1,
		SymbolName:  "Method",
		Kind:        "method",
		StartLine:   10,
		EndLine:     210,
		Content:     content,
		TokenCount:  p.tokens.Count(content),
		Signature:   "(ctx context.Context)",
		ParentIndex: &parentIdx,
	}}

	result := p.splitLargeChunks(chunks)
	for i, c := range result {
		if c.ParentIndex == nil {
			t.Errorf("chunk %d: ParentIndex is nil, expected %d", i, parentIdx)
		} else if *c.ParentIndex != parentIdx {
			t.Errorf("chunk %d: ParentIndex=%d, want %d", i, *c.ParentIndex, parentIdx)
		}
		if c.Signature != "(ctx context.Context)" {
			t.Errorf("chunk %d: Signature=%q, want %q", i, c.Signature, "(ctx context.Context)")
		}
	}
}

func BenchmarkSplitLargeChunks(b *testing.B) {
	p, err := NewParser()
	if err != nil {
		b.Fatal(err)
	}
	defer p.Close()

	var sb strings.Builder
	for i := 0; i < 2000; i++ {
		sb.WriteString("var x" + strings.Repeat("a", 20) + " = 42 // some comment padding here\n")
	}
	content := sb.String()
	tokens := p.tokens.Count(content)

	chunks := []types.ChunkRecord{{
		ChunkIndex: 0,
		SymbolName: "HugeBlock",
		Kind:       "block",
		StartLine:  1,
		EndLine:    2000,
		Content:    content,
		TokenCount: tokens,
	}}

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		p.splitLargeChunks(chunks)
	}
}
