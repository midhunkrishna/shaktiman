package main

import (
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/shaktimanai/shaktiman/internal/observability"
)

// TestFetchStats_Roundtrip stands up a Unix-socket HTTP server that
// mimics the daemon's /metrics endpoint and verifies fetchStats
// decodes the snapshot correctly. Guards against future signature
// drift between the daemon emitter and the CLI consumer.
func TestFetchStats_Roundtrip(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	sockPath := filepath.Join(dir, "test.sock")
	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatalf("net.Listen: %v", err)
	}
	defer ln.Close()
	t.Cleanup(func() { os.Remove(sockPath) })

	want := observability.Snapshot{
		Counters: map[string]uint64{
			"daemon.lock.acquired": 1,
			"mcp.tool_call.search.count": 42,
		},
		Gauges:     map[string]float64{},
		Histograms: map[string]observability.HistogramOut{},
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/metrics", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(want)
	})
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: time.Second}
	go func() { _ = srv.Serve(ln) }()
	defer srv.Close()

	got, err := fetchStats(sockPath, time.Second)
	if err != nil {
		t.Fatalf("fetchStats: %v", err)
	}
	if got.Counters["daemon.lock.acquired"] != 1 {
		t.Errorf("counter daemon.lock.acquired = %d, want 1", got.Counters["daemon.lock.acquired"])
	}
	if got.Counters["mcp.tool_call.search.count"] != 42 {
		t.Errorf("counter mcp.tool_call.search.count = %d, want 42",
			got.Counters["mcp.tool_call.search.count"])
	}
}

func TestFetchStats_NoDaemon(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	sockPath := filepath.Join(dir, "missing.sock")
	if _, err := fetchStats(sockPath, 200*time.Millisecond); err == nil {
		t.Fatal("expected error when socket is unreachable")
	}
}
