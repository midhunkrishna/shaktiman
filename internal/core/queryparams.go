package core

import (
	"fmt"
	"strings"
	"time"

	"github.com/shaktimanai/shaktiman/internal/types"
)

// MaxDiffWindow caps how far back a diff query can look. The bound is a
// product decision (review the last month, not the full history) and a
// safety net against absurd inputs.
const MaxDiffWindow = 720 * time.Hour

// Diff limit bounds. The upper bound prevents pathologically large result
// sets from a single tool call; the lower bound keeps the API honest.
const (
	MinDiffLimit = 1
	MaxDiffLimit = 500
)

// Search overfetch parameters. When a path filter is active the engine
// returns more results than the caller asked for so the post-filter has
// candidates to thin from.
const (
	overfetchMultiplier = 3
	overfetchCap        = 200
)

// ValidateDiffWindow parses a Go duration string like "24h" and validates
// that it is positive and at most MaxDiffWindow. The returned error message
// contains the well-known fragments callers historically asserted on
// ("must be positive", "<= 720h"). Both MCP and CLI wrap the error with
// their own caller-context prefix.
func ValidateDiffWindow(since string) (time.Duration, error) {
	d, err := time.ParseDuration(since)
	if err != nil {
		return 0, fmt.Errorf("%s — use Go duration syntax like '24h', '30m'", since)
	}
	if d <= 0 {
		return 0, fmt.Errorf("%s — must be positive", since)
	}
	if d > MaxDiffWindow {
		return 0, fmt.Errorf("%s — must be <= 720h (30 days)", since)
	}
	return d, nil
}

// ValidateDiffLimit returns nil when limit is within [MinDiffLimit,
// MaxDiffLimit] and a descriptive error otherwise. The error message
// contains the substring "must be 1..500" — a contract callers depend on.
func ValidateDiffLimit(limit int) error {
	if limit < MinDiffLimit || limit > MaxDiffLimit {
		return fmt.Errorf("%d — must be %d..%d", limit, MinDiffLimit, MaxDiffLimit)
	}
	return nil
}

// SearchOverfetchLimit returns the number of results the search engine
// should produce so the post-filter still has candidates to trim. When no
// path filter is active the engine returns exactly maxResults.
func SearchOverfetchLimit(maxResults int, hasPathFilter bool) int {
	if !hasPathFilter {
		return maxResults
	}
	n := maxResults * overfetchMultiplier
	if n > overfetchCap {
		n = overfetchCap
	}
	return n
}

// FilterByPathPrefix retains the results whose Path starts with pathPrefix
// and trims the slice to maxResults. Returns the input unchanged when
// pathPrefix is empty. Reuses the input backing array (results[:0]) so
// callers must not retain the original slice header after this call.
func FilterByPathPrefix(results []types.ScoredResult, pathPrefix string, maxResults int) []types.ScoredResult {
	if pathPrefix == "" {
		return results
	}
	filtered := results[:0]
	for _, r := range results {
		if strings.HasPrefix(r.Path, pathPrefix) {
			filtered = append(filtered, r)
		}
	}
	if len(filtered) > maxResults {
		filtered = filtered[:maxResults]
	}
	return filtered
}

// NormalizeDirection translates the user-facing direction vocabulary
// (callers / callees / both) used by the MCP and CLI surfaces to the
// storage-layer vocabulary (incoming / outgoing / both). Returns ok=false
// for any other value so callers can produce a "direction must be …" error
// matching their surface's wording.
func NormalizeDirection(s string) (storageDir string, ok bool) {
	switch s {
	case "callers":
		return "incoming", true
	case "callees":
		return "outgoing", true
	case "both":
		return "both", true
	}
	return "", false
}
