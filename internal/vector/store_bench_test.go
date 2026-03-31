package vector

import (
	"context"
	"fmt"
	"math/rand"
	"path/filepath"
	"testing"
)

func BenchmarkCosineSimilarity768(b *testing.B) {
	rng := rand.New(rand.NewSource(42))
	a := make([]float32, 768)
	q := make([]float32, 768)
	for i := range a {
		a[i] = rng.Float32()
		q[i] = rng.Float32()
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		cosineSimilarity(q, a)
	}
}

func BenchmarkBruteForceSearch(b *testing.B) {
	for _, n := range []int{1000, 10000, 50000, 100000} {
		b.Run(fmt.Sprintf("n=%d", n), func(b *testing.B) {
			rng := rand.New(rand.NewSource(42))
			store := NewBruteForceStore(768)
			for i := 0; i < n; i++ {
				vec := make([]float32, 768)
				for j := range vec {
					vec[j] = rng.Float32()
				}
				store.Upsert(context.Background(), int64(i+1), vec)
			}
			query := make([]float32, 768)
			for j := range query {
				query[j] = rng.Float32()
			}
			b.ResetTimer()
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				store.Search(context.Background(), query, 20)
			}
		})
	}
}

func BenchmarkSaveToDisk(b *testing.B) {
	rng := rand.New(rand.NewSource(42))
	store := NewBruteForceStore(768)
	for i := 0; i < 50000; i++ {
		vec := make([]float32, 768)
		for j := range vec {
			vec[j] = rng.Float32()
		}
		store.Upsert(context.Background(), int64(i+1), vec)
	}
	dir := b.TempDir()
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		store.SaveToDisk(filepath.Join(dir, "bench.bin"))
	}
}
