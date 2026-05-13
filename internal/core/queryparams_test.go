package core

import (
	"strings"
	"testing"
	"time"

	"github.com/shaktimanai/shaktiman/internal/types"
)

func TestValidateDiffWindow_Valid(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   string
		want time.Duration
	}{
		{"24h", 24 * time.Hour},
		{"30m", 30 * time.Minute},
		{"1s", time.Second},
		{"720h", 720 * time.Hour},
	}
	for _, c := range cases {
		got, err := ValidateDiffWindow(c.in)
		if err != nil {
			t.Errorf("ValidateDiffWindow(%q): %v", c.in, err)
		}
		if got != c.want {
			t.Errorf("ValidateDiffWindow(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

// TestValidateDiffWindow_InvalidPreservesContractFragments asserts the well-
// known error substrings callers depend on. CLI tests in the cmd/shaktiman
// package match on these literal fragments — drift here breaks those tests.
func TestValidateDiffWindow_InvalidPreservesContractFragments(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, in, fragment string
	}{
		{"unparseable", "not-a-duration", "Go duration syntax"},
		{"zero", "0s", "must be positive"},
		{"negative", "-1h", "must be positive"},
		{"too-large", "9000h", "<= 720h"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := ValidateDiffWindow(c.in)
			if err == nil {
				t.Fatalf("expected error for %q", c.in)
			}
			if !strings.Contains(err.Error(), c.fragment) {
				t.Errorf("error %q missing contract fragment %q", err, c.fragment)
			}
		})
	}
}

func TestValidateDiffLimit(t *testing.T) {
	t.Parallel()
	for _, ok := range []int{1, 50, 250, 500} {
		if err := ValidateDiffLimit(ok); err != nil {
			t.Errorf("ValidateDiffLimit(%d) = %v, want nil", ok, err)
		}
	}
	for _, bad := range []int{0, -1, 501, 10000} {
		err := ValidateDiffLimit(bad)
		if err == nil {
			t.Errorf("ValidateDiffLimit(%d) = nil, want error", bad)
			continue
		}
		// "must be 1..500" is the contract fragment that CLI tests assert on.
		if !strings.Contains(err.Error(), "must be 1..500") {
			t.Errorf("error %q missing 'must be 1..500'", err)
		}
	}
}

func TestSearchOverfetchLimit(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name        string
		max         int
		hasFilter   bool
		want        int
		description string
	}{
		{"no-filter-passthrough", 10, false, 10, "engine returns exactly maxResults when no path filter"},
		{"filter-overfetch", 10, true, 30, "filter triples the budget"},
		{"filter-cap", 100, true, 200, "200 cap stops cubic blow-up"},
		{"filter-cap-already-large", 80, true, 200, "cap holds even when 3x is far above"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := SearchOverfetchLimit(c.max, c.hasFilter)
			if got != c.want {
				t.Errorf("SearchOverfetchLimit(%d, %v) = %d, want %d (%s)",
					c.max, c.hasFilter, got, c.want, c.description)
			}
		})
	}
}

func TestFilterByPathPrefix(t *testing.T) {
	t.Parallel()
	results := []types.ScoredResult{
		{Path: "internal/mcp/server.go"},
		{Path: "internal/core/engine.go"},
		{Path: "internal/mcp/tools.go"},
		{Path: "cmd/main.go"},
	}

	// Empty prefix: passthrough.
	out := FilterByPathPrefix(append([]types.ScoredResult(nil), results...), "", 10)
	if len(out) != 4 {
		t.Errorf("empty prefix dropped results: %d != 4", len(out))
	}

	// Prefix match.
	out = FilterByPathPrefix(append([]types.ScoredResult(nil), results...), "internal/mcp/", 10)
	if len(out) != 2 {
		t.Errorf("prefix match: %d != 2", len(out))
	}

	// Trim to maxResults.
	out = FilterByPathPrefix(append([]types.ScoredResult(nil), results...), "internal/", 2)
	if len(out) != 2 {
		t.Errorf("trim: %d != 2", len(out))
	}
}

func TestNormalizeDirection(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   string
		want string
		ok   bool
	}{
		{"callers", "incoming", true},
		{"callees", "outgoing", true},
		{"both", "both", true},
		{"", "", false},
		{"upward", "", false},
		{"INCOMING", "", false}, // storage vocabulary not accepted on input
	}
	for _, c := range cases {
		got, ok := NormalizeDirection(c.in)
		if got != c.want || ok != c.ok {
			t.Errorf("NormalizeDirection(%q) = (%q, %v), want (%q, %v)",
				c.in, got, ok, c.want, c.ok)
		}
	}
}
