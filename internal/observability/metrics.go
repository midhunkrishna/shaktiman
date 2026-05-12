package observability

import (
	"math"
	"sort"
	"sync"
	"sync/atomic"
)

// Registry is a small in-memory metrics surface used across the daemon
// for diagnostic counters, gauges and latency histograms. The
// interface is intentionally minimal — tags/labels are encoded into
// the metric name (e.g. "mcp.tool_call.search.count") to keep the
// snapshot format trivially flat and the per-metric memory bounded.
//
// All methods are safe for concurrent use. The default registry
// returned by Default() is process-global; tests should construct
// their own NewRegistry() to avoid cross-test pollution.
type Registry interface {
	// Inc increments the named counter by 1.
	Inc(name string)
	// Add increments the named counter by delta.
	Add(name string, delta uint64)
	// Set updates the gauge `name` to value.
	Set(name string, value float64)
	// Observe records value into the named histogram.
	Observe(name string, value float64)
	// Snapshot returns a stable JSON-serializable view of all metrics
	// for export. Callers must not mutate the returned maps.
	Snapshot() Snapshot
}

// Snapshot is a point-in-time view of registry state.
type Snapshot struct {
	Counters   map[string]uint64       `json:"counters"`
	Gauges     map[string]float64      `json:"gauges"`
	Histograms map[string]HistogramOut `json:"histograms"`
}

// HistogramOut summarises a histogram for export. Quantiles are
// estimated from the recorded samples (capped at maxSamples to bound
// memory and prevent runaway growth from a hot loop).
type HistogramOut struct {
	Count uint64  `json:"count"`
	Sum   float64 `json:"sum"`
	Min   float64 `json:"min"`
	Max   float64 `json:"max"`
	Mean  float64 `json:"mean"`
	P50   float64 `json:"p50"`
	P95   float64 `json:"p95"`
	P99   float64 `json:"p99"`
}

// maxHistogramSamples bounds per-histogram memory. Once reached, new
// samples replace the oldest in a ring buffer so quantile estimates
// reflect recent activity rather than process-lifetime noise.
const maxHistogramSamples = 4096

// NewRegistry returns a fresh in-memory Registry.
func NewRegistry() Registry {
	return &memRegistry{
		counters:   make(map[string]*uint64),
		gauges:     make(map[string]*atomicFloat),
		histograms: make(map[string]*histogram),
	}
}

type memRegistry struct {
	mu         sync.RWMutex
	counters   map[string]*uint64
	gauges     map[string]*atomicFloat
	histograms map[string]*histogram
}

func (r *memRegistry) counter(name string) *uint64 {
	r.mu.RLock()
	c, ok := r.counters[name]
	r.mu.RUnlock()
	if ok {
		return c
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if c, ok = r.counters[name]; ok {
		return c
	}
	v := uint64(0)
	r.counters[name] = &v
	return &v
}

func (r *memRegistry) gauge(name string) *atomicFloat {
	r.mu.RLock()
	g, ok := r.gauges[name]
	r.mu.RUnlock()
	if ok {
		return g
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if g, ok = r.gauges[name]; ok {
		return g
	}
	g = &atomicFloat{}
	r.gauges[name] = g
	return g
}

func (r *memRegistry) hist(name string) *histogram {
	r.mu.RLock()
	h, ok := r.histograms[name]
	r.mu.RUnlock()
	if ok {
		return h
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if h, ok = r.histograms[name]; ok {
		return h
	}
	h = &histogram{samples: make([]float64, 0, 256)}
	r.histograms[name] = h
	return h
}

func (r *memRegistry) Inc(name string)                    { atomic.AddUint64(r.counter(name), 1) }
func (r *memRegistry) Add(name string, delta uint64)      { atomic.AddUint64(r.counter(name), delta) }
func (r *memRegistry) Set(name string, value float64)     { r.gauge(name).Store(value) }
func (r *memRegistry) Observe(name string, value float64) { r.hist(name).observe(value) }

func (r *memRegistry) Snapshot() Snapshot {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := Snapshot{
		Counters:   make(map[string]uint64, len(r.counters)),
		Gauges:     make(map[string]float64, len(r.gauges)),
		Histograms: make(map[string]HistogramOut, len(r.histograms)),
	}
	for k, v := range r.counters {
		out.Counters[k] = atomic.LoadUint64(v)
	}
	for k, v := range r.gauges {
		out.Gauges[k] = v.Load()
	}
	for k, h := range r.histograms {
		out.Histograms[k] = h.summarise()
	}
	return out
}

// histogram is a tiny ring-buffered sample reservoir. Quantiles are
// computed by sorting on Snapshot. Cheap and good enough for the
// ~hundreds-of-points-per-second rate the daemon produces.
type histogram struct {
	mu      sync.Mutex
	samples []float64
	count   uint64
	sum     float64
	min     float64
	max     float64
	cursor  int
}

func (h *histogram) observe(v float64) {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return
	}
	h.mu.Lock()
	if h.count == 0 || v < h.min {
		h.min = v
	}
	if h.count == 0 || v > h.max {
		h.max = v
	}
	h.count++
	h.sum += v
	if len(h.samples) < maxHistogramSamples {
		h.samples = append(h.samples, v)
	} else {
		h.samples[h.cursor] = v
		h.cursor = (h.cursor + 1) % maxHistogramSamples
	}
	h.mu.Unlock()
}

func (h *histogram) summarise() HistogramOut {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := HistogramOut{
		Count: h.count,
		Sum:   h.sum,
		Min:   h.min,
		Max:   h.max,
	}
	if h.count > 0 {
		out.Mean = h.sum / float64(h.count)
	}
	if len(h.samples) == 0 {
		return out
	}
	cp := make([]float64, len(h.samples))
	copy(cp, h.samples)
	sort.Float64s(cp)
	out.P50 = quantile(cp, 0.50)
	out.P95 = quantile(cp, 0.95)
	out.P99 = quantile(cp, 0.99)
	return out
}

func quantile(sorted []float64, q float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	idx := int(float64(len(sorted)-1) * q)
	return sorted[idx]
}

// atomicFloat is a small wrapper for lock-free float64 reads/writes
// using a uint64 bit-pattern under the hood.
type atomicFloat struct {
	bits atomic.Uint64
}

func (a *atomicFloat) Store(v float64) { a.bits.Store(math.Float64bits(v)) }
func (a *atomicFloat) Load() float64   { return math.Float64frombits(a.bits.Load()) }

// defaultRegistry is the process-global registry callers reach for via
// Default(). Lazily initialised; nil-safe via memRegistry's
// initialise-on-first-use pattern.
var defaultRegistry = NewRegistry()

// Default returns the process-global registry. Production code uses
// this; tests should construct their own via NewRegistry().
func Default() Registry { return defaultRegistry }

// SetDefault swaps the global registry. Intended for tests that need
// to swap-in a fresh registry and reset on cleanup.
func SetDefault(r Registry) Registry {
	prev := defaultRegistry
	defaultRegistry = r
	return prev
}

// Scope is a thin Registry adapter that prepends prefix + "." to every
// metric name. Call sites that record several related metrics (a tool's
// count/latency/error_count, an embed-rejection breakdown) become terse:
//
//	m := observability.ScopeOnDefault("mcp.tool_call." + name)
//	m.Inc("count")
//	m.Observe("latency_ms", ...)
//
// instead of repeating "mcp.tool_call." + name + "." everywhere. Scope
// values are tiny and safe to construct per-call; no per-name caching is
// needed because the underlying Registry's counter()/gauge()/hist()
// already amortise the lookup.
type Scope struct {
	prefix string
	r      Registry
}

// NewScope returns a Scope rooted at r with the given prefix. The prefix
// must not include a trailing separator — Scope inserts a single "." when
// composing the full metric name.
func NewScope(r Registry, prefix string) Scope { return Scope{prefix: prefix, r: r} }

// ScopeOnDefault returns a Scope rooted at the process-global registry.
// Most call sites want this; tests that need isolation should use NewScope
// against a fresh NewRegistry().
func ScopeOnDefault(prefix string) Scope { return NewScope(Default(), prefix) }

// Inc increments the counter prefix.suffix by 1.
func (s Scope) Inc(suffix string) { s.r.Inc(s.name(suffix)) }

// Add increments the counter prefix.suffix by delta.
func (s Scope) Add(suffix string, delta uint64) { s.r.Add(s.name(suffix), delta) }

// Set updates the gauge prefix.suffix to value.
func (s Scope) Set(suffix string, value float64) { s.r.Set(s.name(suffix), value) }

// Observe records value into the histogram prefix.suffix.
func (s Scope) Observe(suffix string, value float64) { s.r.Observe(s.name(suffix), value) }

func (s Scope) name(suffix string) string {
	if s.prefix == "" {
		return suffix
	}
	if suffix == "" {
		return s.prefix
	}
	return s.prefix + "." + suffix
}
