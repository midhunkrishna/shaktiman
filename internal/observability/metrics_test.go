package observability

import (
	"math"
	"sync"
	"testing"
)

func TestRegistry_CounterAndGauge(t *testing.T) {
	t.Parallel()

	r := NewRegistry()
	r.Inc("calls")
	r.Inc("calls")
	r.Add("calls", 10)
	r.Set("queue_depth", 7)

	snap := r.Snapshot()
	if got := snap.Counters["calls"]; got != 12 {
		t.Errorf("counters[calls] = %d, want 12", got)
	}
	if got := snap.Gauges["queue_depth"]; got != 7 {
		t.Errorf("gauges[queue_depth] = %v, want 7", got)
	}
}

func TestRegistry_HistogramQuantiles(t *testing.T) {
	t.Parallel()

	r := NewRegistry()
	for i := 1; i <= 100; i++ {
		r.Observe("latency_ms", float64(i))
	}
	snap := r.Snapshot()
	h := snap.Histograms["latency_ms"]

	if h.Count != 100 {
		t.Errorf("count = %d, want 100", h.Count)
	}
	if h.Min != 1 {
		t.Errorf("min = %v, want 1", h.Min)
	}
	if h.Max != 100 {
		t.Errorf("max = %v, want 100", h.Max)
	}
	if math.Abs(h.Mean-50.5) > 0.01 {
		t.Errorf("mean = %v, want ~50.5", h.Mean)
	}
	// P50 estimate: index 49 → value 50; tolerate ±1.
	if h.P50 < 49 || h.P50 > 51 {
		t.Errorf("p50 = %v, want ~50", h.P50)
	}
	if h.P95 < 94 || h.P95 > 96 {
		t.Errorf("p95 = %v, want ~95", h.P95)
	}
	if h.P99 < 98 || h.P99 > 100 {
		t.Errorf("p99 = %v, want ~99", h.P99)
	}
}

// TestHistogram_RingReservoir asserts that observing more samples
// than maxHistogramSamples does not unboundedly grow memory; it also
// keeps the count accurate so percentages of total still make sense.
func TestHistogram_RingReservoir(t *testing.T) {
	t.Parallel()

	r := NewRegistry()
	for i := 0; i < maxHistogramSamples*2; i++ {
		r.Observe("noisy", float64(i))
	}
	snap := r.Snapshot()
	h := snap.Histograms["noisy"]

	if h.Count != uint64(maxHistogramSamples*2) {
		t.Errorf("count = %d, want %d (count must reflect every observe)",
			h.Count, maxHistogramSamples*2)
	}
	// Last maxHistogramSamples observations dominate the recent quantile
	// view, so p99 should be in the high-recent range.
	if h.P99 < float64(maxHistogramSamples) {
		t.Errorf("p99 = %v, want >= %d", h.P99, maxHistogramSamples)
	}
}

func TestHistogram_RejectsNaNAndInf(t *testing.T) {
	t.Parallel()

	r := NewRegistry()
	r.Observe("clean", math.NaN())
	r.Observe("clean", math.Inf(1))
	r.Observe("clean", math.Inf(-1))
	r.Observe("clean", 1.0)

	h := r.Snapshot().Histograms["clean"]
	if h.Count != 1 {
		t.Errorf("count = %d, want 1 (NaN/Inf must be rejected)", h.Count)
	}
}

// TestRegistry_ConcurrentSafe stress-tests the lock-free counter and
// gauge paths to guard against future regressions.
func TestRegistry_ConcurrentSafe(t *testing.T) {
	t.Parallel()

	r := NewRegistry()
	var wg sync.WaitGroup
	const writers = 16
	const itersPerWriter = 1000

	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < itersPerWriter; j++ {
				r.Inc("hits")
				r.Set("last_writer", float64(j))
				r.Observe("burst_ms", float64(j%100))
			}
		}()
	}
	wg.Wait()

	snap := r.Snapshot()
	if got := snap.Counters["hits"]; got != writers*itersPerWriter {
		t.Errorf("counters[hits] = %d, want %d", got, writers*itersPerWriter)
	}
}

func TestSetDefault_RoundTrip(t *testing.T) {
	r := NewRegistry()
	prev := SetDefault(r)
	t.Cleanup(func() { SetDefault(prev) })

	if Default() != r {
		t.Error("Default() did not return the swapped-in registry")
	}
}
