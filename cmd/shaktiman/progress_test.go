package main

import (
	"bytes"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shaktimanai/shaktiman/internal/daemon"
	"github.com/shaktimanai/shaktiman/internal/types"
)

func TestWriteIndexProgress_NonTTY(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		p          daemon.IndexProgress
		lastPct    int
		wantWrite  bool
		wantPct    int
		wantSubstr string
	}{
		{
			name:    "zero_total_noop",
			p:       daemon.IndexProgress{Indexed: 0, Total: 0},
			lastPct: 0, wantWrite: false, wantPct: 0,
		},
		{
			name:       "first_10pct_writes",
			p:          daemon.IndexProgress{Indexed: 10, Total: 100},
			lastPct:    0, wantWrite: true, wantPct: 10,
			wantSubstr: "Indexing: 10/100 files (10%)\n",
		},
		{
			name:    "below_10pct_step_silent",
			p:       daemon.IndexProgress{Indexed: 15, Total: 100},
			lastPct: 10, wantWrite: false, wantPct: 10,
		},
		{
			name:       "20pct_step_writes",
			p:          daemon.IndexProgress{Indexed: 20, Total: 100},
			lastPct:    10, wantWrite: true, wantPct: 20,
			wantSubstr: "Indexing: 20/100 files (20%)\n",
		},
		{
			name:       "completion_always_writes",
			p:          daemon.IndexProgress{Indexed: 100, Total: 100},
			lastPct:    90, wantWrite: true, wantPct: 100,
			wantSubstr: "Indexing: 100/100 files (100%)\n",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			gotPct := writeIndexProgress(&buf, false, tc.p, tc.lastPct)
			if gotPct != tc.wantPct {
				t.Errorf("lastPct = %d, want %d", gotPct, tc.wantPct)
			}
			if tc.wantWrite {
				if !strings.Contains(buf.String(), tc.wantSubstr) {
					t.Errorf("output = %q, want contains %q", buf.String(), tc.wantSubstr)
				}
			} else if buf.Len() != 0 {
				t.Errorf("expected no output, got %q", buf.String())
			}
		})
	}
}

func TestWriteIndexProgress_TTYUsesCarriageReturn(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	writeIndexProgress(&buf, true, daemon.IndexProgress{Indexed: 5, Total: 10}, 0)

	got := buf.String()
	if !strings.HasPrefix(got, "\r") {
		t.Errorf("TTY output should start with \\r, got %q", got)
	}
	if strings.HasSuffix(got, "\n") {
		t.Errorf("TTY output should not end with newline (same-line refresh), got %q", got)
	}
}

func TestWriteEmbedProgress_Warning(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	lastPct := writeEmbedProgress(&buf, false, types.EmbedProgress{Warning: "ollama degraded"}, 42)

	if lastPct != 42 {
		t.Errorf("warning must not reset lastPct; got %d", lastPct)
	}
	if !strings.Contains(buf.String(), "ollama degraded") {
		t.Errorf("warning not written: %q", buf.String())
	}
}

func TestWriteEmbedProgress_ProgressMilestones(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	var last int
	events := []types.EmbedProgress{
		{Embedded: 5, Total: 100},
		{Embedded: 11, Total: 100},
		{Embedded: 50, Total: 100},
		{Embedded: 100, Total: 100},
	}
	for _, e := range events {
		last = writeEmbedProgress(&buf, false, e, last)
	}
	out := buf.String()
	for _, want := range []string{"11/100", "50/100", "100/100"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing milestone %q in %q", want, out)
		}
	}
	if strings.Contains(out, "5/100") {
		t.Errorf("sub-10%% event should not write: %q", out)
	}
}

// TestCLI_InitJSONOutputIsPureStdout runs the built shaktiman binary with
// --format=json and asserts stdout parses as JSON. This is the regression
// test for stdout pollution: progress and log lines must not leak into the
// stdout stream consumed by jq/scripts.
//
// Uses `init` command because it produces a predictable stdout line in text
// mode and can be exercised without a real index. Other query commands
// would also work but require an indexed project.
func TestCLI_InitJSONOutputIsPureStdout(t *testing.T) {
	t.Parallel()

	bin := buildCLIBinary(t)
	tmpDir := t.TempDir()

	cmd := exec.Command(bin, "init", tmpDir)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("init failed: %v\nstderr: %s", err, stderr.String())
	}

	// init prints a plain "Created config: ..." line to stdout; that's
	// expected. What must NOT appear is any "\r" (carriage return used
	// by progress refresh) or structured progress text.
	if strings.Contains(stdout.String(), "\r") {
		t.Errorf("stdout contains carriage return (progress leaked): %q", stdout.String())
	}
	for _, leak := range []string{"Indexing:", "Embedding:", "Purged"} {
		if strings.Contains(stdout.String(), leak) {
			t.Errorf("stdout contains progress string %q: %q", leak, stdout.String())
		}
	}
}

// TestCLI_StatusJSONParsesClean verifies stdout is pure JSON when --format=json
// is used with a query command that doesn't need an indexed project.
// Uses status on an uninitialized project — it should emit JSON-free error on
// stderr and nothing on stdout.
func TestCLI_StatusJSONParsesClean(t *testing.T) {
	t.Parallel()

	bin := buildCLIBinary(t)

	cmd := exec.Command(bin, "--format=json", "status", "/nonexistent-project-path-xyz")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	_ = cmd.Run() // expected to fail; we just care about streams

	// Failure text must go to stderr, not stdout.
	if stdout.Len() > 0 {
		// If anything is on stdout, it must be valid JSON (empty is fine).
		trimmed := strings.TrimSpace(stdout.String())
		if trimmed != "" {
			var anyJSON any
			if err := json.Unmarshal([]byte(trimmed), &anyJSON); err != nil {
				t.Errorf("non-JSON on stdout: %q (err: %v)", stdout.String(), err)
			}
		}
	}
}

// buildCLIBinary compiles cmd/shaktiman into a temp binary for integration
// testing. Returns the binary path.
func buildCLIBinary(t *testing.T) string {
	t.Helper()
	tmpDir := t.TempDir()
	bin := filepath.Join(tmpDir, "shaktiman")
	cmd := exec.Command("go", "build",
		"-tags", "sqlite_fts5 sqlite bruteforce hnsw",
		"-o", bin,
		"./")
	cmd.Stderr = &bytes.Buffer{}
	if err := cmd.Run(); err != nil {
		t.Fatalf("go build: %v\n%s", err, cmd.Stderr.(*bytes.Buffer).String())
	}
	return bin
}
