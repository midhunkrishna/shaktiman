package main

import (
	"testing"
	"time"
)

// TestPromotionBackoff_InRange verifies the jitter helper produces
// durations within the declared bounds. Without jitter, concurrent
// proxies all re-exec simultaneously on leader exit and race the cold
// leader's socket startup.
func TestPromotionBackoff_InRange(t *testing.T) {
	t.Parallel()

	for range 1000 {
		got := promotionBackoff()
		if got < promotionBackoffMin {
			t.Errorf("backoff %v < min %v", got, promotionBackoffMin)
		}
		if got > promotionBackoffMax {
			t.Errorf("backoff %v > max %v", got, promotionBackoffMax)
		}
	}
}

// TestPromotionBackoff_Distribution sanity-checks that the helper does
// not collapse to a single value (which would indicate a constant
// rather than a jittered choice).
func TestPromotionBackoff_Distribution(t *testing.T) {
	t.Parallel()

	seen := make(map[time.Duration]int)
	for range 500 {
		seen[promotionBackoff()]++
	}
	if len(seen) < 50 {
		t.Errorf("expected >50 distinct backoff values, got %d", len(seen))
	}
}
