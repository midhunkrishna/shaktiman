package main

import (
	"bytes"
	"log/slog"
	"strings"
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

func TestParseFlags_Defaults(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	got, err := parseFlags([]string{"/tmp/proj"}, &buf)
	if err != nil {
		t.Fatalf("parseFlags: %v", err)
	}
	if got.projectRoot != "/tmp/proj" {
		t.Errorf("projectRoot = %q, want /tmp/proj", got.projectRoot)
	}
	if got.logLevel != "" {
		t.Errorf("logLevel = %q, want empty default", got.logLevel)
	}
	if got.pprofAddr != "" {
		t.Errorf("pprofAddr = %q, want empty default", got.pprofAddr)
	}
}

func TestParseFlags_AllSet(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	got, err := parseFlags(
		[]string{"--log-level", "debug", "--pprof-addr", "127.0.0.1:6060", "/tmp/p"},
		&buf,
	)
	if err != nil {
		t.Fatalf("parseFlags: %v", err)
	}
	if got.logLevel != "debug" {
		t.Errorf("logLevel = %q, want debug", got.logLevel)
	}
	if got.pprofAddr != "127.0.0.1:6060" {
		t.Errorf("pprofAddr = %q", got.pprofAddr)
	}
	if got.projectRoot != "/tmp/p" {
		t.Errorf("projectRoot = %q", got.projectRoot)
	}
}

func TestParseFlags_Version(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	got, err := parseFlags([]string{"--version"}, &buf)
	if err != nil {
		t.Fatalf("parseFlags --version: %v", err)
	}
	if !got.showVersion {
		t.Error("showVersion not set")
	}
}

func TestParseFlags_MissingProjectRoot(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	_, err := parseFlags([]string{}, &buf)
	if err == nil {
		t.Fatal("expected error for missing project-root")
	}
	if !strings.Contains(err.Error(), "project-root") {
		t.Errorf("error %q should mention project-root", err)
	}
}

// TestResolveLogLevel_Precedence walks the precedence chain
// flag → env → default and confirms each step.
func TestResolveLogLevel_Precedence(t *testing.T) {
	t.Setenv("SHAKTIMAN_LOG_LEVEL", "warn")

	lvl, warning := resolveLogLevel("debug")
	if lvl != slog.LevelDebug {
		t.Errorf("flag-set: got %v, want debug", lvl)
	}
	if warning != "" {
		t.Errorf("unexpected warning: %q", warning)
	}

	lvl, warning = resolveLogLevel("")
	if lvl != slog.LevelWarn {
		t.Errorf("env-set: got %v, want warn", lvl)
	}
	if warning != "" {
		t.Errorf("unexpected warning: %q", warning)
	}

	t.Setenv("SHAKTIMAN_LOG_LEVEL", "")
	lvl, warning = resolveLogLevel("")
	if lvl != slog.LevelInfo {
		t.Errorf("default: got %v, want info", lvl)
	}
	if warning != "" {
		t.Errorf("unexpected warning: %q", warning)
	}
}

// TestResolveLogLevel_UnknownWarns ensures bad values surface as a
// warning string instead of being silently swallowed.
func TestResolveLogLevel_UnknownWarns(t *testing.T) {
	t.Setenv("SHAKTIMAN_LOG_LEVEL", "")

	lvl, warning := resolveLogLevel("not-a-level")
	if lvl != slog.LevelInfo {
		t.Errorf("unknown flag value: got %v, want info fallback", lvl)
	}
	if warning == "" {
		t.Error("expected warning for unknown level, got empty")
	}
	if !strings.Contains(warning, "--log-level") {
		t.Errorf("warning %q should mention --log-level source", warning)
	}

	t.Setenv("SHAKTIMAN_LOG_LEVEL", "garbage")
	_, warning = resolveLogLevel("")
	if !strings.Contains(warning, "SHAKTIMAN_LOG_LEVEL") {
		t.Errorf("warning %q should mention env source", warning)
	}
}

// TestVersionLine_NonEmpty guards against accidental nil/empty
// regressions in the banner-version helper.
func TestVersionLine_NonEmpty(t *testing.T) {
	t.Parallel()

	v := versionLine()
	if v == "" {
		t.Fatal("versionLine returned empty string")
	}
	if !strings.Contains(v, "shaktimand") {
		t.Errorf("versionLine missing program name: %q", v)
	}
}

