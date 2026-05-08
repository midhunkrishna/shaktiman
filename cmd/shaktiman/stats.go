package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"sort"
	"time"

	"github.com/spf13/cobra"

	"github.com/shaktimanai/shaktiman/internal/lockfile"
	"github.com/shaktimanai/shaktiman/internal/observability"
)

// statsCmd queries the running daemon's in-memory metrics registry
// over its Unix domain socket and prints a human-formatted summary.
// Requires the daemon to be running for that project; reports a clear
// hint when the socket is unavailable.
func statsCmd() *cobra.Command {
	var (
		root        string
		jsonOutput  bool
		dialTimeout time.Duration
	)
	cmd := &cobra.Command{
		Use:   "stats",
		Short: "Show in-memory observability counters from the running daemon",
		RunE: func(_ *cobra.Command, _ []string) error {
			sockPath, err := lockfile.SocketPathFor(root)
			if err != nil {
				return fmt.Errorf("compute socket path: %w", err)
			}

			snap, err := fetchStats(sockPath, dialTimeout)
			if err != nil {
				return fmt.Errorf("fetch stats: %w (is the daemon running for %s?)", err, root)
			}

			if jsonOutput || outputFormat == "json" {
				data, _ := json.MarshalIndent(snap, "", "  ")
				fmt.Println(string(data))
				return nil
			}

			printSnapshot(snap)
			return nil
		},
	}
	cmd.Flags().StringVar(&root, "root", ".", "Project root directory")
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Force JSON output regardless of --format")
	cmd.Flags().DurationVar(&dialTimeout, "dial-timeout", 2*time.Second, "Socket dial timeout")
	return cmd
}

func fetchStats(sockPath string, dialTimeout time.Duration) (observability.Snapshot, error) {
	client := &http.Client{
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return (&net.Dialer{Timeout: dialTimeout}).DialContext(ctx, "unix", sockPath)
			},
		},
		Timeout: 5 * time.Second,
	}
	resp, err := client.Get("http://unix/metrics")
	if err != nil {
		return observability.Snapshot{}, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return observability.Snapshot{}, fmt.Errorf("daemon returned %s", resp.Status)
	}
	var snap observability.Snapshot
	if err := json.NewDecoder(resp.Body).Decode(&snap); err != nil {
		return observability.Snapshot{}, fmt.Errorf("decode snapshot: %w", err)
	}
	return snap, nil
}

func printSnapshot(snap observability.Snapshot) {
	if len(snap.Counters) > 0 {
		fmt.Println("counters:")
		keys := sortedKeys(snap.Counters)
		for _, k := range keys {
			fmt.Printf("  %-50s %d\n", k, snap.Counters[k])
		}
	}
	if len(snap.Gauges) > 0 {
		fmt.Println("gauges:")
		keys := sortedKeys(snap.Gauges)
		for _, k := range keys {
			fmt.Printf("  %-50s %g\n", k, snap.Gauges[k])
		}
	}
	if len(snap.Histograms) > 0 {
		fmt.Println("histograms:")
		keys := sortedHistogramKeys(snap.Histograms)
		for _, k := range keys {
			h := snap.Histograms[k]
			fmt.Printf("  %s\n    count=%d mean=%.2f p50=%.2f p95=%.2f p99=%.2f min=%.2f max=%.2f\n",
				k, h.Count, h.Mean, h.P50, h.P95, h.P99, h.Min, h.Max)
		}
	}
	if len(snap.Counters) == 0 && len(snap.Gauges) == 0 && len(snap.Histograms) == 0 {
		fmt.Println("(no metrics recorded yet)")
	}
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func sortedHistogramKeys(m map[string]observability.HistogramOut) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
