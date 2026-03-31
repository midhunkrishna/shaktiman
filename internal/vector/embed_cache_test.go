package vector

import (
	"fmt"
	"sync"
	"testing"
)

func TestEmbedCache_PutGet(t *testing.T) {
	t.Parallel()
	c := NewEmbedCache(10)

	vec := []float32{1.0, 2.0, 3.0}
	c.Put("hello", vec)

	got, ok := c.Get("hello")
	if !ok {
		t.Fatal("expected cache hit")
	}
	if len(got) != 3 || got[0] != 1.0 || got[1] != 2.0 || got[2] != 3.0 {
		t.Errorf("unexpected vector: %v", got)
	}
}

func TestEmbedCache_GetMiss(t *testing.T) {
	t.Parallel()
	c := NewEmbedCache(10)

	_, ok := c.Get("missing")
	if ok {
		t.Error("expected cache miss")
	}
}

func TestEmbedCache_ReturnsCopy(t *testing.T) {
	t.Parallel()
	c := NewEmbedCache(10)

	c.Put("key", []float32{1.0, 2.0})
	got, _ := c.Get("key")
	got[0] = 999.0 // mutate the returned copy

	got2, _ := c.Get("key")
	if got2[0] != 1.0 {
		t.Errorf("cache entry was mutated: got %v", got2)
	}
}

func TestEmbedCache_StoresCopy(t *testing.T) {
	t.Parallel()
	c := NewEmbedCache(10)

	vec := []float32{1.0, 2.0}
	c.Put("key", vec)
	vec[0] = 999.0 // mutate original

	got, _ := c.Get("key")
	if got[0] != 1.0 {
		t.Errorf("cache stored reference not copy: got %v", got)
	}
}

func TestEmbedCache_LRUEviction(t *testing.T) {
	t.Parallel()
	c := NewEmbedCache(3)

	c.Put("a", []float32{1.0})
	c.Put("b", []float32{2.0})
	c.Put("c", []float32{3.0})

	// Adding "d" should evict "a" (oldest)
	c.Put("d", []float32{4.0})

	if _, ok := c.Get("a"); ok {
		t.Error("expected 'a' to be evicted")
	}
	if _, ok := c.Get("d"); !ok {
		t.Error("expected 'd' to be present")
	}
}

func TestEmbedCache_LRUEviction_AccessRefreshes(t *testing.T) {
	t.Parallel()
	c := NewEmbedCache(3)

	c.Put("a", []float32{1.0})
	c.Put("b", []float32{2.0})
	c.Put("c", []float32{3.0})

	// Access "a" to refresh it
	c.Get("a")

	// Adding "d" should evict "b" (now oldest)
	c.Put("d", []float32{4.0})

	if _, ok := c.Get("a"); !ok {
		t.Error("expected 'a' to survive (was refreshed)")
	}
	if _, ok := c.Get("b"); ok {
		t.Error("expected 'b' to be evicted")
	}
}

func TestEmbedCache_UpdateExisting(t *testing.T) {
	t.Parallel()
	c := NewEmbedCache(3)

	c.Put("a", []float32{1.0})
	c.Put("b", []float32{2.0})
	c.Put("a", []float32{10.0}) // update

	got, ok := c.Get("a")
	if !ok {
		t.Fatal("expected hit")
	}
	if got[0] != 10.0 {
		t.Errorf("expected updated value 10.0, got %v", got[0])
	}

	// Size should still be 2, not 3
	c.Put("c", []float32{3.0})
	c.Put("d", []float32{4.0})
	// "b" was oldest non-updated, should be evicted
	if _, ok := c.Get("b"); ok {
		t.Error("expected 'b' to be evicted")
	}
}

func TestEmbedCache_Concurrent(t *testing.T) {
	t.Parallel()
	c := NewEmbedCache(100)
	var wg sync.WaitGroup

	for i := range 10 {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			for j := range 100 {
				key := fmt.Sprintf("key_%d_%d", n, j)
				c.Put(key, []float32{float32(n), float32(j)})
				c.Get(key)
			}
		}(i)
	}

	wg.Wait()
	// Just verify no panic or deadlock
}

func BenchmarkEmbedCache_Put(b *testing.B) {
	c := NewEmbedCache(1000)
	vec := make([]float32, 768)

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		c.Put(fmt.Sprintf("query_%d", i%1000), vec)
	}
}

func BenchmarkEmbedCache_Get(b *testing.B) {
	c := NewEmbedCache(1000)
	vec := make([]float32, 768)
	for i := range 1000 {
		c.Put(fmt.Sprintf("query_%d", i), vec)
	}

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		c.Get(fmt.Sprintf("query_%d", i%1000))
	}
}
