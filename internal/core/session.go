package core

import (
	"fmt"
	"math"
	"sync"
	"time"
)

// SessionHit identifies a result that was returned to the user.
type SessionHit struct {
	FilePath  string
	StartLine int
}

// SessionStore tracks recently accessed code locations for session-aware ranking.
// It uses an in-memory LRU map keyed on "filePath:startLine" for stability
// across chunk re-indexes (chunk IDs change but file paths and line numbers don't).
type SessionStore struct {
	mu      sync.RWMutex
	entries map[string]*sessionEntry
	order   []string // LRU order, most recent last
	maxSize int
}

type sessionEntry struct {
	accessCount         int
	lastAccessed        time.Time
	queriesSinceLastHit int
}

// NewSessionStore creates a session store with the given capacity.
func NewSessionStore(maxSize int) *SessionStore {
	if maxSize <= 0 {
		maxSize = 2000
	}
	return &SessionStore{
		entries: make(map[string]*sessionEntry, maxSize),
		order:   make([]string, 0, maxSize),
		maxSize: maxSize,
	}
}

func sessionKey(filePath string, startLine int) string {
	return fmt.Sprintf("%s:%d", filePath, startLine)
}

// RecordAccess records a single access to a code location.
func (s *SessionStore) RecordAccess(filePath string, startLine int) {
	key := sessionKey(filePath, startLine)

	s.mu.Lock()
	defer s.mu.Unlock()

	if e, ok := s.entries[key]; ok {
		e.accessCount++
		e.lastAccessed = time.Now()
		e.queriesSinceLastHit = 0
		s.moveToBack(key)
		return
	}

	s.evictIfNeeded()

	s.entries[key] = &sessionEntry{
		accessCount:  1,
		lastAccessed: time.Now(),
	}
	s.order = append(s.order, key)
}

// RecordBatch records accesses for a batch of search results.
func (s *SessionStore) RecordBatch(hits []SessionHit) {
	now := time.Now()

	s.mu.Lock()
	defer s.mu.Unlock()

	for _, h := range hits {
		key := sessionKey(h.FilePath, h.StartLine)

		if e, ok := s.entries[key]; ok {
			e.accessCount++
			e.lastAccessed = now
			e.queriesSinceLastHit = 0
			s.moveToBack(key)
			continue
		}

		s.evictIfNeeded()

		s.entries[key] = &sessionEntry{
			accessCount:  1,
			lastAccessed: now,
		}
		s.order = append(s.order, key)
	}
}

// Score returns a [0,1] session relevance score for a code location.
// Combines recency, frequency, and exploration decay.
func (s *SessionStore) Score(filePath string, startLine int) float64 {
	key := sessionKey(filePath, startLine)

	s.mu.RLock()
	e, ok := s.entries[key]
	if !ok {
		s.mu.RUnlock()
		return 0.0
	}
	// Copy values under read lock to avoid holding lock during math
	accessCount := e.accessCount
	lastAccessed := e.lastAccessed
	queriesSince := e.queriesSinceLastHit
	s.mu.RUnlock()

	minutesAgo := time.Since(lastAccessed).Minutes()

	// Recency: half-life ~10 minutes
	recency := math.Exp(-0.07 * minutesAgo)

	// Frequency: log-scaled, capped at 1.0
	frequency := math.Min(math.Log2(float64(accessCount)+1)/4.0, 1.0)

	// Exploration decay: penalize entries not seen in recent queries
	decay := math.Exp(-0.1 * float64(queriesSince))

	return recency * frequency * decay
}

// DecayAllExcept increments queriesSinceLastHit for all entries
// not in the given hit set.
func (s *SessionStore) DecayAllExcept(hits []SessionHit) {
	hitSet := make(map[string]bool, len(hits))
	for _, h := range hits {
		hitSet[sessionKey(h.FilePath, h.StartLine)] = true
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	for key, e := range s.entries {
		if !hitSet[key] {
			e.queriesSinceLastHit++
		}
	}
}

// Len returns the number of tracked entries.
func (s *SessionStore) Len() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.entries)
}

// evictIfNeeded removes the least recently accessed entry if at capacity.
// Must be called with s.mu held.
func (s *SessionStore) evictIfNeeded() {
	for len(s.entries) >= s.maxSize && len(s.order) > 0 {
		oldest := s.order[0]
		s.order = s.order[1:]
		delete(s.entries, oldest)
	}
}

// moveToBack moves a key to the back of the LRU order.
// Must be called with s.mu held.
func (s *SessionStore) moveToBack(key string) {
	for i, k := range s.order {
		if k == key {
			s.order = append(s.order[:i], s.order[i+1:]...)
			s.order = append(s.order, key)
			return
		}
	}
}
