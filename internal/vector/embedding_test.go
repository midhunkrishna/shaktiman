package vector

import (
	"testing"
	"time"
)

// ── Circuit Breaker Tests ──

func TestCircuitBreaker_InitialState(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		wantState CircuitState
		wantAllow bool
	}{
		{"starts closed and allows", StateClosed, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cb := NewCircuitBreaker()
			if got := cb.State(); got != tc.wantState {
				t.Fatalf("State() = %d, want %d", got, tc.wantState)
			}
			if got := cb.Allow(); got != tc.wantAllow {
				t.Fatalf("Allow() = %v, want %v", got, tc.wantAllow)
			}
		})
	}
}

func TestCircuitBreaker_TransitionToOpen(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		failures  int
		wantState CircuitState
		wantAllow bool
	}{
		{"below threshold stays closed", 2, StateClosed, true},
		{"at threshold transitions to open", 3, StateOpen, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cb := NewCircuitBreaker()
			for i := 0; i < tc.failures; i++ {
				cb.RecordFailure()
			}
			if got := cb.State(); got != tc.wantState {
				t.Fatalf("State() after %d failures = %d, want %d", tc.failures, got, tc.wantState)
			}
			if got := cb.Allow(); got != tc.wantAllow {
				t.Fatalf("Allow() after %d failures = %v, want %v", tc.failures, got, tc.wantAllow)
			}
		})
	}
}

func TestCircuitBreaker_HalfOpenProbe(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		wantState CircuitState
		wantAllow bool
	}{
		{"transitions to half-open after cooldown", StateHalfOpen, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cb := NewCircuitBreaker()
			cb.baseCooldown = time.Millisecond
			cb.currentCooldown = time.Millisecond

			// Trip to Open
			for i := 0; i < cb.failThreshold; i++ {
				cb.RecordFailure()
			}
			if got := cb.State(); got != StateOpen {
				t.Fatalf("expected StateOpen before cooldown, got %d", got)
			}

			time.Sleep(5 * time.Millisecond) // exceed cooldown

			if got := cb.Allow(); got != tc.wantAllow {
				t.Fatalf("Allow() after cooldown = %v, want %v", got, tc.wantAllow)
			}
			if got := cb.State(); got != tc.wantState {
				t.Fatalf("State() after cooldown Allow() = %d, want %d", got, tc.wantState)
			}
		})
	}
}

func TestCircuitBreaker_RecoveryOnSuccess(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		wantState CircuitState
	}{
		{"success in half-open recovers to closed", StateClosed},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cb := NewCircuitBreaker()
			cb.baseCooldown = time.Millisecond
			cb.currentCooldown = time.Millisecond

			// Trip to Open
			for i := 0; i < cb.failThreshold; i++ {
				cb.RecordFailure()
			}

			time.Sleep(5 * time.Millisecond)
			cb.Allow() // transitions Open -> HalfOpen

			if got := cb.State(); got != StateHalfOpen {
				t.Fatalf("expected StateHalfOpen, got %d", got)
			}

			cb.RecordSuccess()

			if got := cb.State(); got != tc.wantState {
				t.Fatalf("State() after success in half-open = %d, want %d", got, tc.wantState)
			}
			if !cb.Allow() {
				t.Fatal("Allow() should be true after recovery")
			}
		})
	}
}

func TestCircuitBreaker_ExponentialBackoff(t *testing.T) {
	t.Parallel()

	t.Run("cooldown doubles on each open cycle", func(t *testing.T) {
		t.Parallel()
		cb := NewCircuitBreaker()
		cb.baseCooldown = time.Millisecond
		cb.currentCooldown = time.Millisecond
		cb.backoffMax = 100 * time.Millisecond

		// Cycle 1: trip to Open → cooldown should double to 2ms
		for i := 0; i < cb.failThreshold; i++ {
			cb.RecordFailure()
		}
		if got := cb.State(); got != StateOpen {
			t.Fatalf("cycle 1: State() = %d, want StateOpen", got)
		}
		if cb.currentCooldown != 2*time.Millisecond {
			t.Fatalf("cycle 1: cooldown = %v, want 2ms", cb.currentCooldown)
		}

		// Wait for cooldown, probe and fail again → cycle 2, cooldown 4ms
		time.Sleep(5 * time.Millisecond)
		cb.Allow() // Open → HalfOpen
		for i := 0; i < cb.failThreshold; i++ {
			cb.RecordFailure()
		}
		if cb.currentCooldown != 4*time.Millisecond {
			t.Fatalf("cycle 2: cooldown = %v, want 4ms", cb.currentCooldown)
		}

		// Still StateOpen, NOT StateDisabled — always recoverable
		if got := cb.State(); got != StateOpen {
			t.Fatalf("cycle 2: State() = %d, want StateOpen (not disabled)", got)
		}
	})

	t.Run("cooldown capped at backoffMax", func(t *testing.T) {
		t.Parallel()
		cb := NewCircuitBreaker()
		cb.baseCooldown = time.Millisecond
		cb.currentCooldown = time.Millisecond
		cb.backoffMax = 4 * time.Millisecond

		// Trip 5 times — cooldown: 2, 4, 4, 4, 4 (capped)
		for cycle := 0; cycle < 5; cycle++ {
			for i := 0; i < cb.failThreshold; i++ {
				cb.RecordFailure()
			}
			if cycle < 4 {
				time.Sleep(10 * time.Millisecond)
				cb.Allow()
			}
		}
		if cb.currentCooldown != 4*time.Millisecond {
			t.Fatalf("cooldown = %v, want cap at 4ms", cb.currentCooldown)
		}
	})

	t.Run("still recoverable after many cycles", func(t *testing.T) {
		t.Parallel()
		cb := NewCircuitBreaker()
		cb.baseCooldown = time.Millisecond
		cb.currentCooldown = time.Millisecond
		cb.backoffMax = 4 * time.Millisecond

		// Trip 5 times
		for cycle := 0; cycle < 5; cycle++ {
			for i := 0; i < cb.failThreshold; i++ {
				cb.RecordFailure()
			}
			if cycle < 4 {
				time.Sleep(10 * time.Millisecond)
				cb.Allow()
			}
		}

		// Wait for cooldown — should still allow probe
		time.Sleep(10 * time.Millisecond)
		if !cb.Allow() {
			t.Fatal("Allow() should be true after cooldown (never permanently disabled)")
		}
		if got := cb.State(); got != StateHalfOpen {
			t.Fatalf("State() = %d, want StateHalfOpen", got)
		}

		// Success resets everything
		cb.RecordSuccess()
		if got := cb.State(); got != StateClosed {
			t.Fatalf("State() after success = %d, want StateClosed", got)
		}
		if cb.currentCooldown != cb.baseCooldown {
			t.Fatalf("cooldown not reset: %v, want %v", cb.currentCooldown, cb.baseCooldown)
		}
	})
}

func TestCircuitBreaker_Reset(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		setupState func(cb *CircuitBreaker)
		wantState  CircuitState
		wantAllow  bool
	}{
		{
			"reset from open",
			func(cb *CircuitBreaker) {
				for i := 0; i < cb.failThreshold; i++ {
					cb.RecordFailure()
				}
			},
			StateClosed,
			true,
		},
		{
			"reset from open with escalated backoff",
			func(cb *CircuitBreaker) {
				// Trip multiple cycles to escalate backoff
				for cycle := 0; cycle < 5; cycle++ {
					for i := 0; i < cb.failThreshold; i++ {
						cb.RecordFailure()
					}
				}
			},
			StateClosed,
			true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cb := NewCircuitBreaker()
			tc.setupState(cb)
			cb.Reset()

			if got := cb.State(); got != tc.wantState {
				t.Fatalf("State() after Reset() = %d, want %d", got, tc.wantState)
			}
			if got := cb.Allow(); got != tc.wantAllow {
				t.Fatalf("Allow() after Reset() = %v, want %v", got, tc.wantAllow)
			}
		})
	}
}

// ── Embed Cache Tests ──

func TestEmbedCache_PutAndGet(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		key     string
		vec     []float32
		wantHit bool
	}{
		{"store and retrieve", "hello", []float32{1.0, 2.0, 3.0}, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cache := NewEmbedCache(8)
			cache.Put(tc.key, tc.vec)

			got, ok := cache.Get(tc.key)
			if ok != tc.wantHit {
				t.Fatalf("Get(%q) hit = %v, want %v", tc.key, ok, tc.wantHit)
			}
			if len(got) != len(tc.vec) {
				t.Fatalf("Get(%q) len = %d, want %d", tc.key, len(got), len(tc.vec))
			}
			for i := range tc.vec {
				if got[i] != tc.vec[i] {
					t.Fatalf("Get(%q)[%d] = %f, want %f", tc.key, i, got[i], tc.vec[i])
				}
			}
		})
	}
}

func TestEmbedCache_Eviction(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		maxSize    int
		keys       []string
		evictedKey string
		survivorKey string
	}{
		{
			"oldest evicted when full",
			2,
			[]string{"a", "b", "c"},
			"a",
			"c",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cache := NewEmbedCache(tc.maxSize)
			for _, k := range tc.keys {
				cache.Put(k, []float32{1.0})
			}

			if _, ok := cache.Get(tc.evictedKey); ok {
				t.Fatalf("Get(%q) should miss after eviction", tc.evictedKey)
			}
			if _, ok := cache.Get(tc.survivorKey); !ok {
				t.Fatalf("Get(%q) should hit, was not evicted", tc.survivorKey)
			}
		})
	}
}

func TestEmbedCache_LRUOrder(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		maxSize     int
		puts        []string
		accessKey   string // key to access (move to end) before final put
		finalPut    string
		wantEvicted string
		wantPresent string
	}{
		{
			"recently accessed survives eviction",
			2,
			[]string{"a", "b"},
			"a",      // access "a" to make it most recent
			"c",      // insert "c", should evict "b" (now oldest)
			"b",
			"a",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cache := NewEmbedCache(tc.maxSize)
			for _, k := range tc.puts {
				cache.Put(k, []float32{1.0})
			}

			// Access to promote in LRU order
			cache.Get(tc.accessKey)

			// Insert one more to trigger eviction
			cache.Put(tc.finalPut, []float32{2.0})

			if _, ok := cache.Get(tc.wantEvicted); ok {
				t.Fatalf("Get(%q) should miss, expected eviction", tc.wantEvicted)
			}
			if _, ok := cache.Get(tc.wantPresent); !ok {
				t.Fatalf("Get(%q) should hit, recently accessed", tc.wantPresent)
			}
		})
	}
}

func TestEmbedCache_Miss(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		key  string
	}{
		{"nonexistent key returns miss", "nonexistent"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cache := NewEmbedCache(4)

			vec, ok := cache.Get(tc.key)
			if ok {
				t.Fatalf("Get(%q) should return false for nonexistent key", tc.key)
			}
			if vec != nil {
				t.Fatalf("Get(%q) vec should be nil on miss, got %v", tc.key, vec)
			}
		})
	}
}

func TestEmbedCache_SliceIsolation(t *testing.T) {
	t.Parallel()

	t.Run("Put copies input slice", func(t *testing.T) {
		t.Parallel()
		cache := NewEmbedCache(4)
		vec := []float32{1.0, 2.0, 3.0}
		cache.Put("key", vec)

		// Mutate the original slice
		vec[0] = 999.0

		// Cache should be unaffected
		got, ok := cache.Get("key")
		if !ok {
			t.Fatal("expected cache hit")
		}
		if got[0] != 1.0 {
			t.Fatalf("cache corrupted: got[0] = %f, want 1.0", got[0])
		}
	})

	t.Run("Get returns independent copy", func(t *testing.T) {
		t.Parallel()
		cache := NewEmbedCache(4)
		cache.Put("key", []float32{1.0, 2.0, 3.0})

		got1, _ := cache.Get("key")
		got1[0] = 999.0

		// Second Get should return original value
		got2, _ := cache.Get("key")
		if got2[0] != 1.0 {
			t.Fatalf("cache corrupted via Get mutation: got2[0] = %f, want 1.0", got2[0])
		}
	})
}

func TestCircuitBreaker_HalfOpenSingleProbe(t *testing.T) {
	t.Parallel()

	cb := NewCircuitBreaker()
	cb.baseCooldown = time.Millisecond
	cb.currentCooldown = time.Millisecond

	// Trip to Open
	for i := 0; i < cb.failThreshold; i++ {
		cb.RecordFailure()
	}

	time.Sleep(5 * time.Millisecond)

	// First Allow: transitions to HalfOpen, allows probe
	if !cb.Allow() {
		t.Fatal("first Allow() after cooldown should return true")
	}
	if got := cb.State(); got != StateHalfOpen {
		t.Fatalf("State() = %d, want StateHalfOpen", got)
	}

	// Second Allow while probe in flight: should reject
	if cb.Allow() {
		t.Fatal("second Allow() in HalfOpen should return false (probe in flight)")
	}

	// RecordFailure clears probe flag, trips back to Open
	cb.RecordFailure()
	if got := cb.State(); got != StateOpen {
		t.Fatalf("State() after failure in HalfOpen = %d, want StateOpen", got)
	}
}
