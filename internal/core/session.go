package core

import (
	"container/list"
	"fmt"
	"math"
	"sync"
	"sync/atomic"
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
//
// Uses container/list for O(1) LRU operations and an atomic generation counter
// for O(1) decay instead of iterating all entries.
type SessionStore struct {
	mu         sync.RWMutex
	entries    map[string]*list.Element
	order      *list.List
	maxSize    int
	generation atomic.Int64
}

type sessionEntry struct {
	key            string
	accessCount    int
	lastAccessed   time.Time
	lastGeneration int64
}

// NewSessionStore creates a session store with the given capacity.
func NewSessionStore(maxSize int) *SessionStore {
	if maxSize <= 0 {
		maxSize = 2000
	}
	return &SessionStore{
		entries: make(map[string]*list.Element, maxSize),
		order:   list.New(),
		maxSize: maxSize,
	}
}

func sessionKey(filePath string, startLine int) string {
	return fmt.Sprintf("%s:%d", filePath, startLine)
}

// RecordAccess records a single access to a code location.
func (s *SessionStore) RecordAccess(filePath string, startLine int) {
	key := sessionKey(filePath, startLine)
	gen := s.generation.Load()

	s.mu.Lock()
	defer s.mu.Unlock()

	if elem, ok := s.entries[key]; ok {
		e := elem.Value.(*sessionEntry)
		e.accessCount++
		e.lastAccessed = time.Now()
		e.lastGeneration = gen
		s.order.MoveToBack(elem)
		return
	}

	s.evictIfNeeded()

	entry := &sessionEntry{
		key:            key,
		accessCount:    1,
		lastAccessed:   time.Now(),
		lastGeneration: gen,
	}
	elem := s.order.PushBack(entry)
	s.entries[key] = elem
}

// RecordAndDecay records accesses for a batch of search results and advances
// the session generation in a single lock-held operation. Hit entries'
// lastGeneration is set to the new generation (exempting them from decay);
// all other entries' queriesSince grows implicitly via the generation bump.
//
// This is the correct primitive for recording a query's results. The two-call
// pattern (RecordBatch followed by DecayAllExcept) is not safe under
// concurrent callers: two searches can interleave their record/decay pairs
// and cause one caller's hits to see a generation advanced by the other
// caller before its own decay-exempt write, under-crediting recency.
func (s *SessionStore) RecordAndDecay(hits []SessionHit) {
	now := time.Now()

	s.mu.Lock()
	defer s.mu.Unlock()

	gen := s.generation.Add(1)

	for _, h := range hits {
		key := sessionKey(h.FilePath, h.StartLine)

		if elem, ok := s.entries[key]; ok {
			e := elem.Value.(*sessionEntry)
			e.accessCount++
			e.lastAccessed = now
			e.lastGeneration = gen
			s.order.MoveToBack(elem)
			continue
		}

		s.evictIfNeeded()

		entry := &sessionEntry{
			key:            key,
			accessCount:    1,
			lastAccessed:   now,
			lastGeneration: gen,
		}
		elem := s.order.PushBack(entry)
		s.entries[key] = elem
	}
}

// RecordBatch records accesses for a batch of search results.
//
// Deprecated for the record+decay query pattern: use RecordAndDecay instead.
// This method remains for callers that want to record accesses without
// advancing the generation (e.g. warmup/migration).
func (s *SessionStore) RecordBatch(hits []SessionHit) {
	now := time.Now()
	gen := s.generation.Load()

	s.mu.Lock()
	defer s.mu.Unlock()

	for _, h := range hits {
		key := sessionKey(h.FilePath, h.StartLine)

		if elem, ok := s.entries[key]; ok {
			e := elem.Value.(*sessionEntry)
			e.accessCount++
			e.lastAccessed = now
			e.lastGeneration = gen
			s.order.MoveToBack(elem)
			continue
		}

		s.evictIfNeeded()

		entry := &sessionEntry{
			key:            key,
			accessCount:    1,
			lastAccessed:   now,
			lastGeneration: gen,
		}
		elem := s.order.PushBack(entry)
		s.entries[key] = elem
	}
}

// Score returns a [0,1] session relevance score for a code location.
// Combines recency, frequency, and exploration decay.
func (s *SessionStore) Score(filePath string, startLine int) float64 {
	key := sessionKey(filePath, startLine)

	s.mu.RLock()
	elem, ok := s.entries[key]
	if !ok {
		s.mu.RUnlock()
		return 0.0
	}
	e := elem.Value.(*sessionEntry)
	// Copy values under read lock to avoid holding lock during math
	accessCount := e.accessCount
	lastAccessed := e.lastAccessed
	lastGen := e.lastGeneration
	s.mu.RUnlock()

	currentGen := s.generation.Load()
	queriesSince := int(currentGen - lastGen)

	minutesAgo := time.Since(lastAccessed).Minutes()

	// Recency: half-life ~10 minutes
	recency := math.Exp(-0.07 * minutesAgo)

	// Frequency: log-scaled, capped at 1.0
	frequency := math.Min(math.Log2(float64(accessCount)+1)/4.0, 1.0)

	// Exploration decay: penalize entries not seen in recent queries
	decay := math.Exp(-0.1 * float64(queriesSince))

	return recency * frequency * decay
}

// DecayAllExcept increments the generation counter and updates hit entries.
// O(len(hits)) instead of O(len(entries)).
// Invariant: must be called exactly once per query cycle for the generation
// counter to be equivalent to per-entry queriesSinceLastHit counting.
//
// Deprecated for the record+decay query pattern: use RecordAndDecay instead.
// This method remains for callers that want to advance the generation
// without recording new accesses.
func (s *SessionStore) DecayAllExcept(hits []SessionHit) {
	gen := s.generation.Add(1)

	s.mu.Lock()
	defer s.mu.Unlock()

	for _, h := range hits {
		key := sessionKey(h.FilePath, h.StartLine)
		if elem, ok := s.entries[key]; ok {
			elem.Value.(*sessionEntry).lastGeneration = gen
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
	for len(s.entries) >= s.maxSize {
		front := s.order.Front()
		if front == nil {
			break
		}
		evicted := s.order.Remove(front).(*sessionEntry)
		delete(s.entries, evicted.key)
	}
}
