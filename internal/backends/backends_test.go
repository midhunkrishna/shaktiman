package backends

import (
	"testing"

	"github.com/shaktimanai/shaktiman/internal/types"
)

func TestEmbeddingsPath_BruteForce(t *testing.T) {
	cfg := types.Config{
		VectorBackend:  "brute_force",
		EmbeddingsPath: "/tmp/embeddings.bin",
	}
	got := EmbeddingsPath(cfg)
	if got != "/tmp/embeddings.bin" {
		t.Errorf("EmbeddingsPath = %q, want /tmp/embeddings.bin", got)
	}
}

func TestEmbeddingsPath_HNSW(t *testing.T) {
	cfg := types.Config{
		VectorBackend:  "hnsw",
		EmbeddingsPath: "/tmp/embeddings.bin",
	}
	got := EmbeddingsPath(cfg)
	if got != "/tmp/embeddings.hnsw" {
		t.Errorf("EmbeddingsPath = %q, want /tmp/embeddings.hnsw", got)
	}
}

func TestEmbeddingsPath_HNSW_NoExtension(t *testing.T) {
	cfg := types.Config{
		VectorBackend:  "hnsw",
		EmbeddingsPath: "/tmp/embeddings",
	}
	got := EmbeddingsPath(cfg)
	if got != "/tmp/embeddings.hnsw" {
		t.Errorf("EmbeddingsPath = %q, want /tmp/embeddings.hnsw", got)
	}
}

func TestEmbeddingsPath_Pgvector(t *testing.T) {
	cfg := types.Config{
		VectorBackend:  "pgvector",
		EmbeddingsPath: "/tmp/embeddings.bin",
	}
	got := EmbeddingsPath(cfg)
	if got != "/tmp/embeddings.bin" {
		t.Errorf("EmbeddingsPath = %q, want /tmp/embeddings.bin", got)
	}
}

func TestClose_PartialInit(t *testing.T) {
	// VectorStore=nil, dbCloser=nil — Close must not panic
	b := &Backends{}
	if err := b.Close(); err != nil {
		t.Fatalf("Close on empty Backends: %v", err)
	}
}

func TestClose_NilVectorStore(t *testing.T) {
	closed := false
	b := &Backends{
		dbCloser: func() error { closed = true; return nil },
	}
	if err := b.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if !closed {
		t.Error("expected dbCloser to be called")
	}
}
