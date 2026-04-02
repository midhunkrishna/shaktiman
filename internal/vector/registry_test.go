package vector

import (
	"context"
	"testing"
)

func TestNewVectorStore_BruteForce(t *testing.T) {
	t.Parallel()

	vs, err := NewVectorStore(VectorStoreConfig{
		Backend: "brute_force",
		Dims:    4,
	})
	if err != nil {
		t.Fatalf("NewVectorStore brute_force: %v", err)
	}
	defer vs.Close()

	// Verify functional
	ctx := context.Background()
	if err := vs.Upsert(ctx, 1, []float32{0.1, 0.2, 0.3, 0.4}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	count, _ := vs.Count(ctx)
	if count != 1 {
		t.Errorf("Count = %d, want 1", count)
	}
	if !vs.Healthy(ctx) {
		t.Error("expected Healthy = true")
	}
}

func TestNewVectorStore_HNSW(t *testing.T) {
	t.Parallel()

	vs, err := NewVectorStore(VectorStoreConfig{
		Backend: "hnsw",
		Dims:    4,
	})
	if err != nil {
		t.Fatalf("NewVectorStore hnsw: %v", err)
	}
	defer vs.Close()

	ctx := context.Background()
	if err := vs.Upsert(ctx, 1, []float32{0.1, 0.2, 0.3, 0.4}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	count, _ := vs.Count(ctx)
	if count != 1 {
		t.Errorf("Count = %d, want 1", count)
	}
}

func TestNewVectorStore_DefaultIsBruteForce(t *testing.T) {
	t.Parallel()

	// Empty backend should default to brute_force
	vs, err := NewVectorStore(VectorStoreConfig{
		Backend: "",
		Dims:    4,
	})
	if err != nil {
		t.Fatalf("NewVectorStore empty backend: %v", err)
	}
	defer vs.Close()

	// Should be a BruteForceStore
	if _, ok := vs.(*BruteForceStore); !ok {
		t.Errorf("expected *BruteForceStore, got %T", vs)
	}
}

func TestNewVectorStore_UnknownBackend(t *testing.T) {
	t.Parallel()

	_, err := NewVectorStore(VectorStoreConfig{
		Backend: "faiss",
		Dims:    4,
	})
	if err == nil {
		t.Fatal("expected error for unknown backend")
	}
}

func TestHasVectorStore(t *testing.T) {
	if !HasVectorStore("brute_force") {
		t.Error("expected brute_force to be registered")
	}
	if !HasVectorStore("hnsw") {
		t.Error("expected hnsw to be registered")
	}
	if HasVectorStore("qdrant") {
		t.Error("expected qdrant to NOT be registered yet")
	}
}
