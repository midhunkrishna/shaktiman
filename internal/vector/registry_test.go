package vector

import (
	"context"
	"testing"

	"github.com/shaktimanai/shaktiman/internal/types"
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

func TestVectorStoreConfigFrom_BasicFields(t *testing.T) {
	t.Parallel()
	cfg := types.Config{
		VectorBackend:    "brute_force",
		EmbeddingDims:    768,
		QdrantURL:        "http://localhost:6333",
		QdrantCollection: "my_col",
		QdrantAPIKey:     "secret",
	}
	vsc := VectorStoreConfigFrom(cfg, nil)
	if vsc.Backend != "brute_force" {
		t.Errorf("Backend = %q, want brute_force", vsc.Backend)
	}
	if vsc.Dims != 768 {
		t.Errorf("Dims = %d, want 768", vsc.Dims)
	}
	if vsc.QdrantURL != "http://localhost:6333" {
		t.Errorf("QdrantURL = %q", vsc.QdrantURL)
	}
	if vsc.QdrantCollection != "my_col" {
		t.Errorf("QdrantCollection = %q", vsc.QdrantCollection)
	}
	if vsc.QdrantAPIKey != "secret" {
		t.Errorf("QdrantAPIKey = %q", vsc.QdrantAPIKey)
	}
	if vsc.PgPool != nil {
		t.Error("PgPool should be nil for non-pgvector backend")
	}
}

// fakePoolStore simulates a MetadataStore that has a RawPool() method.
type fakePoolStore struct{ pool any }

func (f *fakePoolStore) RawPool() any { return f.pool }

func TestVectorStoreConfigFrom_PgVector_ExtractsPool(t *testing.T) {
	t.Parallel()
	sentinel := "fake-pool"
	cfg := types.Config{
		VectorBackend: "pgvector",
		EmbeddingDims: 384,
	}
	vsc := VectorStoreConfigFrom(cfg, &fakePoolStore{pool: sentinel})
	if vsc.PgPool != sentinel {
		t.Errorf("PgPool = %v, want %q", vsc.PgPool, sentinel)
	}
	if vsc.Store == nil {
		t.Error("Store should be set")
	}
}

func TestVectorStoreConfigFrom_PgVector_NilStore(t *testing.T) {
	t.Parallel()
	cfg := types.Config{
		VectorBackend: "pgvector",
		EmbeddingDims: 768,
	}
	vsc := VectorStoreConfigFrom(cfg, nil)
	if vsc.PgPool != nil {
		t.Error("PgPool should be nil when store is nil")
	}
}

func TestVectorStoreConfigFrom_PgVector_StoreWithoutPool(t *testing.T) {
	t.Parallel()
	// Store that doesn't implement RawPool()
	cfg := types.Config{
		VectorBackend: "pgvector",
		EmbeddingDims: 768,
	}
	vsc := VectorStoreConfigFrom(cfg, "not-a-pooler")
	if vsc.PgPool != nil {
		t.Error("PgPool should be nil when store lacks RawPool()")
	}
}

func TestVectorStoreConfigFrom_NonPgVector_IgnoresPool(t *testing.T) {
	t.Parallel()
	cfg := types.Config{
		VectorBackend: "qdrant",
		EmbeddingDims: 768,
	}
	vsc := VectorStoreConfigFrom(cfg, &fakePoolStore{pool: "should-be-ignored"})
	if vsc.PgPool != nil {
		t.Error("PgPool should be nil for non-pgvector backend")
	}
}
