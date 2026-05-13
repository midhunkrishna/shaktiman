package observability

import (
	"testing"
)

func TestScope_PrependsPrefix(t *testing.T) {
	t.Parallel()
	r := NewRegistry()
	s := NewScope(r, "mcp.tool_call.search")

	s.Inc("count")
	s.Inc("count")
	s.Add("count", 5)
	s.Observe("latency_ms", 12)
	s.Set("inflight", 3)

	snap := r.Snapshot()
	if got := snap.Counters["mcp.tool_call.search.count"]; got != 7 {
		t.Errorf("counter = %d, want 7", got)
	}
	if got := snap.Gauges["mcp.tool_call.search.inflight"]; got != 3 {
		t.Errorf("gauge = %v, want 3", got)
	}
	if snap.Histograms["mcp.tool_call.search.latency_ms"].Count != 1 {
		t.Errorf("histogram count = %d, want 1",
			snap.Histograms["mcp.tool_call.search.latency_ms"].Count)
	}
}

// TestScope_EmptyPrefix verifies a scope with an empty prefix is a no-op
// translator — useful when a caller wants to pass a scope through code that
// might or might not actually be scoped.
func TestScope_EmptyPrefix(t *testing.T) {
	t.Parallel()
	r := NewRegistry()
	s := NewScope(r, "")
	s.Inc("foo")
	if r.Snapshot().Counters["foo"] != 1 {
		t.Error("empty-prefix scope should not mutate suffix")
	}
}

// TestScope_EmptySuffix exercises the corner where caller passes "" — the
// prefix alone is used. This is unusual but should not panic or produce a
// spurious trailing dot.
func TestScope_EmptySuffix(t *testing.T) {
	t.Parallel()
	r := NewRegistry()
	NewScope(r, "myprefix").Inc("")
	if r.Snapshot().Counters["myprefix"] != 1 {
		t.Errorf("empty-suffix scope should record under prefix alone, got %v",
			r.Snapshot().Counters)
	}
	if _, hasDottedKey := r.Snapshot().Counters["myprefix."]; hasDottedKey {
		t.Error("scope must not produce a trailing-dot metric name")
	}
}

func TestScopeOnDefault_UsesDefaultRegistry(t *testing.T) {
	// Not parallel: mutates the process-global default registry.
	prev := SetDefault(NewRegistry())
	t.Cleanup(func() { SetDefault(prev) })

	ScopeOnDefault("test.scope_on_default").Inc("hits")
	snap := Default().Snapshot()
	if snap.Counters["test.scope_on_default.hits"] != 1 {
		t.Errorf("ScopeOnDefault did not record on default registry: %v", snap.Counters)
	}
}
