package vector

import "sync"

// EmbedCache is an LRU cache for query embeddings.
type EmbedCache struct {
	mu      sync.Mutex
	entries map[string][]float32
	order   []string
	maxSize int
}

// NewEmbedCache creates a cache with the given maximum entry count.
func NewEmbedCache(maxSize int) *EmbedCache {
	return &EmbedCache{
		entries: make(map[string][]float32, maxSize),
		maxSize: maxSize,
	}
}

// Get returns a cached embedding if present. Returns a copy to prevent mutation.
func (c *EmbedCache) Get(query string) ([]float32, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	vec, ok := c.entries[query]
	if ok {
		c.moveToEnd(query)
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

	if _, exists := c.entries[query]; exists {
		c.entries[query] = cp
		c.moveToEnd(query)
		return
	}

	if len(c.order) >= c.maxSize {
		oldest := c.order[0]
		c.order = c.order[1:]
		delete(c.entries, oldest)
	}

	c.entries[query] = cp
	c.order = append(c.order, query)
}

func (c *EmbedCache) moveToEnd(key string) {
	for i, k := range c.order {
		if k == key {
			c.order = append(c.order[:i], c.order[i+1:]...)
			c.order = append(c.order, key)
			return
		}
	}
}
