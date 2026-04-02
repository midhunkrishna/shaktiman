package pgvector

import (
	"context"
	"testing"

	"github.com/shaktimanai/shaktiman/internal/types"
)

func TestNewPgVectorStore_NilPool(t *testing.T) {
	_, err := NewPgVectorStore(nil, 768)
	if err == nil {
		t.Fatal("expected error for nil pool")
	}
}

func TestUpsertBatch_LengthMismatch(t *testing.T) {
	s := &PgVectorStore{dims: 4}
	err := s.UpsertBatch(context.Background(), []int64{1, 2}, [][]float32{{0.1, 0.2, 0.3, 0.4}})
	if err == nil {
		t.Fatal("expected error for length mismatch")
	}
}

func TestUpsert_DimsMismatch(t *testing.T) {
	s := &PgVectorStore{dims: 4}
	err := s.Upsert(context.Background(), 1, []float32{0.1, 0.2})
	if err == nil {
		t.Fatal("expected error for dims mismatch")
	}
}

func TestUpsert_ZeroVector(t *testing.T) {
	s := &PgVectorStore{dims: 4}
	err := s.Upsert(context.Background(), 1, []float32{0, 0, 0, 0})
	if err == nil {
		t.Fatal("expected error for zero vector")
	}
}

func TestDelete_Empty(t *testing.T) {
	s := &PgVectorStore{dims: 4}
	if err := s.Delete(context.Background(), nil); err != nil {
		t.Fatalf("Delete nil: %v", err)
	}
	if err := s.Delete(context.Background(), []int64{}); err != nil {
		t.Fatalf("Delete empty: %v", err)
	}
}

func TestClose_Idempotent(t *testing.T) {
	s := &PgVectorStore{dims: 4}
	if err := s.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}

func TestSearch_TopKZero(t *testing.T) {
	s := &PgVectorStore{dims: 4}
	results, err := s.Search(context.Background(), []float32{1, 0, 0, 0}, 0)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if results != nil {
		t.Errorf("expected nil for topK=0")
	}
}

func TestSearch_ZeroQuery(t *testing.T) {
	s := &PgVectorStore{dims: 4}
	results, err := s.Search(context.Background(), []float32{0, 0, 0, 0}, 5)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if results != nil {
		t.Errorf("expected nil for zero query vector")
	}
}

func TestIsZeroVector(t *testing.T) {
	tests := []struct {
		name string
		v    []float32
		want bool
	}{
		{"all zeros", []float32{0, 0, 0}, true},
		{"has nonzero", []float32{0, 0.1, 0}, false},
		{"empty", []float32{}, true},
		{"single zero", []float32{0}, true},
		{"single nonzero", []float32{1}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isZeroVector(tt.v); got != tt.want {
				t.Errorf("isZeroVector(%v) = %v, want %v", tt.v, got, tt.want)
			}
		})
	}
}

func TestCompileTimeCheck(t *testing.T) {
	var _ types.VectorStore = (*PgVectorStore)(nil)
}

func TestMigrate_InvalidDims(t *testing.T) {
	if err := Migrate(context.Background(), nil, 0); err == nil {
		t.Fatal("expected error for dims=0")
	}
	if err := Migrate(context.Background(), nil, 5000); err == nil {
		t.Fatal("expected error for dims=5000")
	}
	// Nil pool should fail before dim check
	err := Migrate(context.Background(), nil, 768)
	if err == nil {
		t.Fatal("expected error for nil pool")
	}
	if err.Error() != "pgvector: pool is nil" {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestMigrate_ValidDimsBoundary(t *testing.T) {
	// Dims at boundaries should pass dim validation (fail on nil pool)
	for _, dims := range []int{1, 4096} {
		err := Migrate(context.Background(), nil, dims)
		if err == nil {
			t.Fatalf("expected error for nil pool with dims=%d", dims)
		}
		if err.Error() != "pgvector: pool is nil" {
			t.Errorf("dims=%d: expected nil pool error, got: %v", dims, err)
		}
	}
}

func TestUpsertBatch_EmptyBatch(t *testing.T) {
	s := &PgVectorStore{dims: 4}
	// Empty batch should be a no-op (no pool call)
	err := s.UpsertBatch(context.Background(), []int64{}, [][]float32{})
	if err != nil {
		t.Fatalf("UpsertBatch empty: %v", err)
	}
}

func TestUpsertBatch_AllZeroVectorsSkipped(t *testing.T) {
	// With no pool, this would panic on a real upsert. But if all vectors are
	// zero, the batch should skip them all and never touch the pool.
	s := &PgVectorStore{dims: 2}
	err := s.UpsertBatch(context.Background(),
		[]int64{1, 2},
		[][]float32{{0, 0}, {0, 0}})
	// No error because zero vectors are skipped and batch is empty
	if err != nil {
		t.Fatalf("UpsertBatch all-zero: %v", err)
	}
}

func TestSearch_NegativeTopK(t *testing.T) {
	s := &PgVectorStore{dims: 4}
	results, err := s.Search(context.Background(), []float32{1, 0, 0, 0}, -1)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if results != nil {
		t.Errorf("expected nil for negative topK")
	}
}

func TestValidateDimensions_NilPool(t *testing.T) {
	// ValidateDimensions with nil pool panics (pgxpool.QueryRow dereferences).
	// This is expected — callers must ensure pool is non-nil.
	// Verify panic is caught gracefully.
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic for nil pool")
		}
	}()
	_ = ValidateDimensions(context.Background(), nil, 768)
}
