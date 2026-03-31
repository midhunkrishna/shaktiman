package vector

import (
	"container/list"
	"sync"
)

// EmbedCache is an LRU cache for query embeddings.
// Uses container/list for O(1) move-to-back and eviction.
type EmbedCache struct {
	mu       sync.Mutex
	entries  map[string]*list.Element
	order    *list.List
	maxSize  int
}

type cacheEntry struct {
	key string
	vec []float32
}

// NewEmbedCache creates a cache with the given maximum entry count.
func NewEmbedCache(maxSize int) *EmbedCache {
	return &EmbedCache{
		entries: make(map[string]*list.Element, maxSize),
		order:   list.New(),
		maxSize: maxSize,
	}
}

// Get returns a cached embedding if present. Returns a copy to prevent mutation.
func (c *EmbedCache) Get(query string) ([]float32, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	elem, ok := c.entries[query]
	if ok {
		c.order.MoveToBack(elem)
		vec := elem.Value.(*cacheEntry).vec
		cp := make([]float32, len(vec))
		copy(cp, vec)
		return cp, true
	}
	return nil, false
}

// Put stores a copy of the query embedding in the cache, evicting the oldest if full.
func (c *EmbedCache) Put(query string, vec []float32) {
	cp := make([]float32, len(vec))
	copy(cp, vec)

	c.mu.Lock()
	defer c.mu.Unlock()

	if elem, exists := c.entries[query]; exists {
		elem.Value.(*cacheEntry).vec = cp
		c.order.MoveToBack(elem)
		return
	}

	if c.order.Len() >= c.maxSize {
		front := c.order.Front()
		if front != nil {
			evicted := c.order.Remove(front).(*cacheEntry)
			delete(c.entries, evicted.key)
		}
	}

	elem := c.order.PushBack(&cacheEntry{key: query, vec: cp})
	c.entries[query] = elem
}
