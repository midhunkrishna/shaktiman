package core

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

func TestSessionStore_RecordAndScore(t *testing.T) {
	t.Parallel()

	ss := NewSessionStore(100)

	ss.RecordAccess("main.go", 10)
	ss.RecordAccess("main.go", 10)
	ss.RecordAccess("main.go", 10)

	score := ss.Score("main.go", 10)
	if score <= 0 {
		t.Errorf("expected positive score for accessed entry, got %f", score)
	}
	if score > 1.0 {
		t.Errorf("expected score <= 1.0, got %f", score)
	}

	// Unseen entry should score 0
	score2 := ss.Score("other.go", 1)
	if score2 != 0 {
		t.Errorf("expected 0 score for unseen entry, got %f", score2)
	}
}

func TestSessionStore_RecordBatch(t *testing.T) {
	t.Parallel()

	ss := NewSessionStore(100)

	hits := []SessionHit{
		{FilePath: "a.go", StartLine: 1},
		{FilePath: "b.go", StartLine: 5},
		{FilePath: "c.go", StartLine: 10},
	}
	ss.RecordBatch(hits)

	if ss.Len() != 3 {
		t.Errorf("expected 3 entries, got %d", ss.Len())
	}

	for _, h := range hits {
		score := ss.Score(h.FilePath, h.StartLine)
		if score <= 0 {
			t.Errorf("expected positive score for %s:%d, got %f", h.FilePath, h.StartLine, score)
		}
	}
}

func TestSessionStore_LRUEviction(t *testing.T) {
	t.Parallel()

	ss := NewSessionStore(3)

	ss.RecordAccess("a.go", 1)
	ss.RecordAccess("b.go", 1)
	ss.RecordAccess("c.go", 1)

	if ss.Len() != 3 {
		t.Fatalf("expected 3 entries, got %d", ss.Len())
	}

	// Adding a 4th should evict "a.go:1" (oldest)
	ss.RecordAccess("d.go", 1)

	if ss.Len() != 3 {
		t.Errorf("expected 3 entries after eviction, got %d", ss.Len())
	}

	// "a.go:1" should be evicted
	if score := ss.Score("a.go", 1); score != 0 {
		t.Errorf("expected 0 score for evicted entry a.go:1, got %f", score)
	}

	// "d.go:1" should be present
	if score := ss.Score("d.go", 1); score <= 0 {
		t.Errorf("expected positive score for d.go:1, got %f", score)
	}
}

func TestSessionStore_LRUEviction_AccessRefreshes(t *testing.T) {
	t.Parallel()

	ss := NewSessionStore(3)

	ss.RecordAccess("a.go", 1)
	ss.RecordAccess("b.go", 1)
	ss.RecordAccess("c.go", 1)

	// Re-access "a.go:1" to move it to back
	ss.RecordAccess("a.go", 1)

	// Adding "d.go" should now evict "b.go:1" (oldest after a's refresh)
	ss.RecordAccess("d.go", 1)

	if score := ss.Score("b.go", 1); score != 0 {
		t.Errorf("expected 0 for evicted b.go:1, got %f", score)
	}
	if score := ss.Score("a.go", 1); score <= 0 {
		t.Errorf("expected positive score for refreshed a.go:1, got %f", score)
	}
}

func TestSessionStore_ExplorationDecay(t *testing.T) {
	t.Parallel()

	ss := NewSessionStore(100)

	ss.RecordAccess("old.go", 1)
	ss.RecordAccess("old.go", 1)

	scoreBefore := ss.Score("old.go", 1)

	// Decay: simulate 5 queries where "old.go:1" wasn't in results
	for range 5 {
		ss.DecayAllExcept([]SessionHit{{FilePath: "new.go", StartLine: 1}})
	}

	scoreAfter := ss.Score("old.go", 1)
	if scoreAfter >= scoreBefore {
		t.Errorf("expected score to decrease after decay: before=%f, after=%f", scoreBefore, scoreAfter)
	}
}

func TestSessionStore_ScoreDecay(t *testing.T) {
	t.Parallel()

	ss := NewSessionStore(100)

	// Manually insert an entry with lastAccessed in the past
	key := sessionKey("aged.go", 1)
	ss.mu.Lock()
	entry := &sessionEntry{
		key:          key,
		accessCount:  5,
		lastAccessed: time.Now().Add(-20 * time.Minute),
	}
	elem := ss.order.PushBack(entry)
	ss.entries[key] = elem
	ss.mu.Unlock()

	// Also record a fresh access
	ss.RecordAccess("fresh.go", 1)
	for range 4 {
		ss.RecordAccess("fresh.go", 1)
	}

	agedScore := ss.Score("aged.go", 1)
	freshScore := ss.Score("fresh.go", 1)

	if agedScore >= freshScore {
		t.Errorf("expected aged score < fresh score: aged=%f, fresh=%f", agedScore, freshScore)
	}
}

func TestSessionStore_Concurrent(t *testing.T) {
	t.Parallel()

	ss := NewSessionStore(100)
	var wg sync.WaitGroup

	// Concurrent writers
	for i := range 10 {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			for j := range 100 {
				ss.RecordAccess(fmt.Sprintf("file%d.go", n), j)
			}
		}(i)
	}

	// Concurrent readers
	for range 5 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 100 {
				ss.Score("file0.go", 0)
				ss.Len()
			}
		}()
	}

	// Concurrent decay
	wg.Add(1)
	go func() {
		defer wg.Done()
		for range 50 {
			ss.DecayAllExcept([]SessionHit{{FilePath: "file0.go", StartLine: 0}})
		}
	}()

	wg.Wait()

	// Just verify it didn't panic or deadlock
	if ss.Len() == 0 {
		t.Error("expected non-zero entries after concurrent access")
	}
}

func TestNewSessionStore_ZeroMaxSize(t *testing.T) {
	t.Parallel()

	// Zero maxSize should default to 2000.
	ss := NewSessionStore(0)

	// Insert 2001 entries; if maxSize=2000, the store should evict.
	for i := 0; i < 2001; i++ {
		ss.RecordAccess(fmt.Sprintf("file%d.go", i), i)
	}
	if ss.Len() > 2000 {
		t.Errorf("Len() = %d, expected <= 2000 (default maxSize)", ss.Len())
	}
}

func TestRecordBatch_ReAccess(t *testing.T) {
	t.Parallel()

	ss := NewSessionStore(100)

	// First access.
	ss.RecordBatch([]SessionHit{
		{FilePath: "a.go", StartLine: 1},
	})
	s1 := ss.Score("a.go", 1)

	// Second access to same key -- score should increase.
	ss.RecordBatch([]SessionHit{
		{FilePath: "a.go", StartLine: 1},
	})
	s2 := ss.Score("a.go", 1)

	if s2 <= s1 {
		t.Errorf("score after re-access (%f) should be > initial score (%f)", s2, s1)
	}
}

func TestSessionStore_GenerationCounter(t *testing.T) {
	t.Parallel()

	ss := NewSessionStore(100)

	ss.RecordAccess("target.go", 1)
	ss.RecordAccess("target.go", 1)
	ss.RecordAccess("other.go", 1)

	scoreBeforeDecay := ss.Score("target.go", 1)

	// Decay 3 times — "target.go" not in hits
	for range 3 {
		ss.DecayAllExcept([]SessionHit{{FilePath: "other.go", StartLine: 1}})
	}

	scoreAfterDecay := ss.Score("target.go", 1)
	if scoreAfterDecay >= scoreBeforeDecay {
		t.Errorf("expected score to decrease after decay: before=%f, after=%f", scoreBeforeDecay, scoreAfterDecay)
	}

	// "other.go" was in hits — its score should not have decayed from generation
	otherBefore := ss.Score("other.go", 1)

	// One more decay with "other.go" in hits
	ss.DecayAllExcept([]SessionHit{{FilePath: "other.go", StartLine: 1}})

	otherAfter := ss.Score("other.go", 1)
	// other.go's generation is kept current, so decay component should be ~1.0
	// Score might change slightly due to time-based recency, but should be close
	if otherAfter < otherBefore*0.9 {
		t.Errorf("other.go score dropped too much despite being in hits: before=%f, after=%f", otherBefore, otherAfter)
	}
}

func TestSessionStore_GenerationEquivalence(t *testing.T) {
	t.Parallel()

	// Verify that generation-based decay produces the same mathematical result
	// as the old queriesSinceLastHit approach: decay = exp(-0.1 * queriesSince)
	ss := NewSessionStore(100)
	ss.RecordAccess("a.go", 1)

	// Decay 5 times
	for range 5 {
		ss.DecayAllExcept(nil)
	}

	score := ss.Score("a.go", 1)
	// Expected decay factor: exp(-0.1 * 5) = exp(-0.5) ≈ 0.6065
	// Score = recency * frequency * decay
	// recency ≈ 1.0 (just accessed), frequency = log2(2)/4 = 0.25
	// So score ≈ 1.0 * 0.25 * 0.6065 ≈ 0.15
	if score <= 0 {
		t.Errorf("expected positive score, got %f", score)
	}

	// The key invariant: queriesSince should be 5
	ss.mu.RLock()
	elem := ss.entries[sessionKey("a.go", 1)]
	lastGen := elem.Value.(*sessionEntry).lastGeneration
	ss.mu.RUnlock()
	currentGen := ss.generation.Load()
	queriesSince := currentGen - lastGen
	if queriesSince != 5 {
		t.Errorf("expected queriesSince=5, got %d", queriesSince)
	}
}

func TestSessionStore_DecayAllExcept_HitReset(t *testing.T) {
	t.Parallel()

	ss := NewSessionStore(100)
	ss.RecordAccess("a.go", 1)

	// Decay 3 times without "a.go"
	for range 3 {
		ss.DecayAllExcept(nil)
	}

	scoreDecayed := ss.Score("a.go", 1)

	// Now record "a.go" as a hit via RecordAccess (updates lastGeneration)
	ss.RecordAccess("a.go", 1)

	scoreRefreshed := ss.Score("a.go", 1)
	if scoreRefreshed <= scoreDecayed {
		t.Errorf("expected refreshed score > decayed score: refreshed=%f, decayed=%f", scoreRefreshed, scoreDecayed)
	}
}

// ── Benchmarks ──

func BenchmarkSessionStore_Score(b *testing.B) {
	ss := NewSessionStore(2000)
	// Pre-populate
	for i := range 1000 {
		ss.RecordAccess(fmt.Sprintf("file%d.go", i), i*10)
	}

	b.ResetTimer()
	for range b.N {
		ss.Score("file500.go", 5000)
	}
}

func BenchmarkSessionStore_RecordAccess(b *testing.B) {
	ss := NewSessionStore(2000)

	b.ResetTimer()
	for i := range b.N {
		ss.RecordAccess(fmt.Sprintf("file%d.go", i%500), (i%50)*10)
	}
}

func BenchmarkSessionStore_RecordBatch(b *testing.B) {
	ss := NewSessionStore(2000)
	hits := make([]SessionHit, 10)
	for i := range hits {
		hits[i] = SessionHit{FilePath: fmt.Sprintf("file%d.go", i), StartLine: i * 10}
	}

	b.ResetTimer()
	for range b.N {
		ss.RecordBatch(hits)
	}
}

func BenchmarkSessionStore_DecayAllExcept(b *testing.B) {
	ss := NewSessionStore(2000)
	// Pre-populate with 1000 entries
	for i := range 1000 {
		ss.RecordAccess(fmt.Sprintf("file%d.go", i), i*10)
	}
	hits := []SessionHit{{FilePath: "file0.go", StartLine: 0}}

	b.ResetTimer()
	for range b.N {
		ss.DecayAllExcept(hits)
	}
}
