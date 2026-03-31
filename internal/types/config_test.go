package types

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig("/tmp/proj")
	if cfg.SearchMaxResults != 10 {
		t.Errorf("SearchMaxResults = %d, want 10", cfg.SearchMaxResults)
	}
	if cfg.SearchDefaultMode != "locate" {
		t.Errorf("SearchDefaultMode = %q, want locate", cfg.SearchDefaultMode)
	}
	if cfg.SearchMinScore != 0.15 {
		t.Errorf("SearchMinScore = %f, want 0.15", cfg.SearchMinScore)
	}
	if !cfg.ContextEnabled {
		t.Error("ContextEnabled = false, want true")
	}
	if cfg.ContextBudgetTokens != 4096 {
		t.Errorf("ContextBudgetTokens = %d, want 4096", cfg.ContextBudgetTokens)
	}
	if cfg.VectorBackend != "brute_force" {
		t.Errorf("VectorBackend = %q, want brute_force", cfg.VectorBackend)
	}
}

func TestLoadConfigFromFile_NoFile(t *testing.T) {
	dir := t.TempDir()
	cfg := DefaultConfig(dir)

	loaded, err := LoadConfigFromFile(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Defaults should be unchanged
	if loaded.SearchMaxResults != 10 {
		t.Errorf("SearchMaxResults = %d, want 10", loaded.SearchMaxResults)
	}
	if loaded.SearchDefaultMode != "locate" {
		t.Errorf("SearchDefaultMode = %q, want locate", loaded.SearchDefaultMode)
	}

	// Sample config should have been created
	path := filepath.Join(dir, ".shaktiman", "shaktiman.toml")
	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Error("sample config was not created")
	}
}

func TestLoadConfigFromFile_FullFile(t *testing.T) {
	dir := t.TempDir()
	cfgDir := filepath.Join(dir, ".shaktiman")
	os.MkdirAll(cfgDir, 0o755)

	toml := `[search]
max_results = 25
default_mode = "full"
min_score = 0.30

[context]
enabled = false
budget_tokens = 2048

[vector]
backend = "hnsw"
`
	os.WriteFile(filepath.Join(cfgDir, "shaktiman.toml"), []byte(toml), 0o644)

	cfg := DefaultConfig(dir)
	loaded, err := LoadConfigFromFile(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if loaded.SearchMaxResults != 25 {
		t.Errorf("SearchMaxResults = %d, want 25", loaded.SearchMaxResults)
	}
	if loaded.SearchDefaultMode != "full" {
		t.Errorf("SearchDefaultMode = %q, want full", loaded.SearchDefaultMode)
	}
	if loaded.SearchMinScore != 0.30 {
		t.Errorf("SearchMinScore = %f, want 0.30", loaded.SearchMinScore)
	}
	if loaded.ContextEnabled {
		t.Error("ContextEnabled = true, want false")
	}
	if loaded.ContextBudgetTokens != 2048 {
		t.Errorf("ContextBudgetTokens = %d, want 2048", loaded.ContextBudgetTokens)
	}
	// DefaultMaxResults and MaxBudgetTokens should sync
	if loaded.DefaultMaxResults != 25 {
		t.Errorf("DefaultMaxResults = %d, want 25", loaded.DefaultMaxResults)
	}
	if loaded.MaxBudgetTokens != 2048 {
		t.Errorf("MaxBudgetTokens = %d, want 2048", loaded.MaxBudgetTokens)
	}
	if loaded.VectorBackend != "hnsw" {
		t.Errorf("VectorBackend = %q, want hnsw", loaded.VectorBackend)
	}
}

func TestLoadConfigFromFile_PartialFile(t *testing.T) {
	dir := t.TempDir()
	cfgDir := filepath.Join(dir, ".shaktiman")
	os.MkdirAll(cfgDir, 0o755)

	// Only search section, context left at defaults
	toml := `[search]
max_results = 5
`
	os.WriteFile(filepath.Join(cfgDir, "shaktiman.toml"), []byte(toml), 0o644)

	cfg := DefaultConfig(dir)
	loaded, err := LoadConfigFromFile(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if loaded.SearchMaxResults != 5 {
		t.Errorf("SearchMaxResults = %d, want 5", loaded.SearchMaxResults)
	}
	// Context should retain defaults
	if !loaded.ContextEnabled {
		t.Error("ContextEnabled should remain true")
	}
	if loaded.ContextBudgetTokens != 4096 {
		t.Errorf("ContextBudgetTokens = %d, want 4096", loaded.ContextBudgetTokens)
	}
	// SearchDefaultMode should retain default
	if loaded.SearchDefaultMode != "locate" {
		t.Errorf("SearchDefaultMode = %q, want locate", loaded.SearchDefaultMode)
	}
	// VectorBackend should retain default when [vector] section absent
	if loaded.VectorBackend != "brute_force" {
		t.Errorf("VectorBackend = %q, want brute_force", loaded.VectorBackend)
	}
}

func TestLoadConfigFromFile_InvalidValues(t *testing.T) {
	tests := []struct {
		name string
		toml string
	}{
		{"max_results too high", `[search]` + "\n" + `max_results = 500`},
		{"max_results too low", `[search]` + "\n" + `max_results = 0`},
		{"invalid mode", `[search]` + "\n" + `default_mode = "fast"`},
		{"min_score too high", `[search]` + "\n" + `min_score = 1.5`},
		{"min_score negative", `[search]` + "\n" + `min_score = -0.1`},
		{"budget too low", `[context]` + "\n" + `budget_tokens = 100`},
		{"budget too high", `[context]` + "\n" + `budget_tokens = 99999`},
		{"invalid backend", `[vector]` + "\n" + `backend = "faiss"`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			cfgDir := filepath.Join(dir, ".shaktiman")
			os.MkdirAll(cfgDir, 0o755)
			os.WriteFile(filepath.Join(cfgDir, "shaktiman.toml"), []byte(tt.toml), 0o644)

			cfg := DefaultConfig(dir)
			_, err := LoadConfigFromFile(cfg)
			if err == nil {
				t.Error("expected error, got nil")
			}
		})
	}
}

func TestLoadConfigFromFile_MalformedTOML(t *testing.T) {
	dir := t.TempDir()
	cfgDir := filepath.Join(dir, ".shaktiman")
	os.MkdirAll(cfgDir, 0o755)
	os.WriteFile(filepath.Join(cfgDir, "shaktiman.toml"), []byte(`{{{not toml`), 0o644)

	cfg := DefaultConfig(dir)
	_, err := LoadConfigFromFile(cfg)
	if err == nil {
		t.Error("expected error for malformed TOML, got nil")
	}
}

func TestWriteSampleConfig(t *testing.T) {
	dir := t.TempDir()
	WriteSampleConfig(dir)

	path := filepath.Join(dir, ".shaktiman", "shaktiman.toml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("sample config not written: %v", err)
	}

	content := string(data)
	// All values should be commented out
	if !contains(content, "# max_results") {
		t.Error("expected commented max_results")
	}
	if !contains(content, "# default_mode") {
		t.Error("expected commented default_mode")
	}
	if !contains(content, "# budget_tokens") {
		t.Error("expected commented budget_tokens")
	}
	if !contains(content, "# backend") {
		t.Error("expected commented backend")
	}

	// Loading the sample should produce default config (all commented out)
	cfg := DefaultConfig(dir)
	loaded, err := LoadConfigFromFile(cfg)
	if err != nil {
		t.Fatalf("loading sample config failed: %v", err)
	}
	if loaded.SearchMaxResults != 10 {
		t.Errorf("SearchMaxResults = %d, want 10 (sample should not override defaults)", loaded.SearchMaxResults)
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && searchString(s, substr)
}

func searchString(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func TestWriteSampleConfig_MkdirAllFails(t *testing.T) {
	t.Parallel()

	// Use a path with a null byte -- os.MkdirAll will fail with EINVAL.
	// WriteSampleConfig should silently return (log and exit), not panic.
	WriteSampleConfig("/invalid\x00path")
}

func TestLoadConfigFromFile_TestPatterns(t *testing.T) {
	dir := t.TempDir()
	cfgDir := filepath.Join(dir, ".shaktiman")
	os.MkdirAll(cfgDir, 0o755)

	toml := `[test]
patterns = ["*_test.go", "testdata/", "e2e/"]
`
	os.WriteFile(filepath.Join(cfgDir, "shaktiman.toml"), []byte(toml), 0o644)

	cfg := DefaultConfig(dir)
	loaded, err := LoadConfigFromFile(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(loaded.TestPatterns) != 3 {
		t.Fatalf("TestPatterns len = %d, want 3", len(loaded.TestPatterns))
	}
	want := []string{"*_test.go", "testdata/", "e2e/"}
	for i, p := range loaded.TestPatterns {
		if p != want[i] {
			t.Errorf("TestPatterns[%d] = %q, want %q", i, p, want[i])
		}
	}
}

func TestLoadConfigFromFile_TestPatternsAbsent(t *testing.T) {
	dir := t.TempDir()
	cfgDir := filepath.Join(dir, ".shaktiman")
	os.MkdirAll(cfgDir, 0o755)

	toml := `[search]
max_results = 5
`
	os.WriteFile(filepath.Join(cfgDir, "shaktiman.toml"), []byte(toml), 0o644)

	cfg := DefaultConfig(dir)
	loaded, err := LoadConfigFromFile(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if loaded.TestPatterns != nil {
		t.Errorf("TestPatterns = %v, want nil", loaded.TestPatterns)
	}
}

func TestDefaultTestPatterns_GoOnly(t *testing.T) {
	patterns := DefaultTestPatterns([]string{"go"})
	want := []string{"*_test.go", "testdata/"}
	if len(patterns) != len(want) {
		t.Fatalf("len = %d, want %d: %v", len(patterns), len(want), patterns)
	}
	for i, p := range patterns {
		if p != want[i] {
			t.Errorf("patterns[%d] = %q, want %q", i, p, want[i])
		}
	}
}

func TestDefaultTestPatterns_MultiLanguage(t *testing.T) {
	patterns := DefaultTestPatterns([]string{"go", "typescript", "python"})
	// Should be sorted and deduplicated
	if len(patterns) == 0 {
		t.Fatal("expected non-empty patterns")
	}
	// Check sorted
	for i := 1; i < len(patterns); i++ {
		if patterns[i] < patterns[i-1] {
			t.Errorf("patterns not sorted: %q before %q", patterns[i-1], patterns[i])
		}
	}
	// Check deduplication: __tests__/ appears in both TS and JS, but we only have TS here
	seen := make(map[string]bool)
	for _, p := range patterns {
		if seen[p] {
			t.Errorf("duplicate pattern: %q", p)
		}
		seen[p] = true
	}
}

func TestDefaultTestPatterns_SharedPatternsDeduplicated(t *testing.T) {
	// typescript and javascript both have __tests__/
	patterns := DefaultTestPatterns([]string{"typescript", "javascript"})
	count := 0
	for _, p := range patterns {
		if p == "__tests__/" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("__tests__/ appeared %d times, want 1", count)
	}
}

func TestDefaultTestPatterns_UnknownLanguage(t *testing.T) {
	patterns := DefaultTestPatterns([]string{"cobol"})
	if len(patterns) != 0 {
		t.Errorf("expected empty patterns for unknown language, got %v", patterns)
	}
}

func TestDefaultTestPatterns_Empty(t *testing.T) {
	patterns := DefaultTestPatterns(nil)
	if len(patterns) != 0 {
		t.Errorf("expected empty patterns for nil languages, got %v", patterns)
	}
}

func TestWriteSampleConfig_AlreadyExists(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	// Create the config file first.
	WriteSampleConfig(dir)

	// Call again -- should overwrite without error (idempotent).
	WriteSampleConfig(dir)

	// File should still be valid.
	cfg := DefaultConfig(dir)
	loaded, err := LoadConfigFromFile(cfg)
	if err != nil {
		t.Fatalf("LoadConfigFromFile after double write: %v", err)
	}
	def := DefaultConfig(dir)
	if loaded.SearchMaxResults != def.SearchMaxResults {
		t.Errorf("SearchMaxResults = %d, want %d", loaded.SearchMaxResults, def.SearchMaxResults)
	}
}
