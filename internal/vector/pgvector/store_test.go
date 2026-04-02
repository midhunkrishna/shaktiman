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
}
