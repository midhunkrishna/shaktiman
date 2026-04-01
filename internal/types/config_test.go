package types

import (
	"os"
	"path/filepath"
	"testing"
	"time"
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
	if cfg.DatabaseBackend != "sqlite" {
		t.Errorf("DatabaseBackend = %q, want sqlite", cfg.DatabaseBackend)
	}
	if cfg.PostgresMaxOpen != 20 {
		t.Errorf("PostgresMaxOpen = %d, want 20", cfg.PostgresMaxOpen)
	}
	if cfg.PostgresMaxIdle != 10 {
		t.Errorf("PostgresMaxIdle = %d, want 10", cfg.PostgresMaxIdle)
	}
	if cfg.PostgresSchema != "public" {
		t.Errorf("PostgresSchema = %q, want public", cfg.PostgresSchema)
	}
	if cfg.QdrantCollection != "shaktiman" {
		t.Errorf("QdrantCollection = %q, want shaktiman", cfg.QdrantCollection)
	}
	if cfg.EmbedTimeout != 120*time.Second {
		t.Errorf("EmbedTimeout = %v, want 120s", cfg.EmbedTimeout)
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

func TestLoadConfigFromFile_DatabaseSection(t *testing.T) {
	dir := t.TempDir()
	cfgDir := filepath.Join(dir, ".shaktiman")
	os.MkdirAll(cfgDir, 0o755)

	tomlData := `[database]
backend = "postgres"

[postgres]
connection_string = "postgres://localhost:5432/shaktiman"
max_open_conns = 30
max_idle_conns = 15
schema = "myschema"
`
	os.WriteFile(filepath.Join(cfgDir, "shaktiman.toml"), []byte(tomlData), 0o644)

	cfg := DefaultConfig(dir)
	loaded, err := LoadConfigFromFile(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if loaded.DatabaseBackend != "postgres" {
		t.Errorf("DatabaseBackend = %q, want postgres", loaded.DatabaseBackend)
	}
	if loaded.PostgresConnString != "postgres://localhost:5432/shaktiman" {
		t.Errorf("PostgresConnString = %q", loaded.PostgresConnString)
	}
	if loaded.PostgresMaxOpen != 30 {
		t.Errorf("PostgresMaxOpen = %d, want 30", loaded.PostgresMaxOpen)
	}
	if loaded.PostgresMaxIdle != 15 {
		t.Errorf("PostgresMaxIdle = %d, want 15", loaded.PostgresMaxIdle)
	}
	if loaded.PostgresSchema != "myschema" {
		t.Errorf("PostgresSchema = %q, want myschema", loaded.PostgresSchema)
	}
}

func TestLoadConfigFromFile_QdrantSection(t *testing.T) {
	dir := t.TempDir()
	cfgDir := filepath.Join(dir, ".shaktiman")
	os.MkdirAll(cfgDir, 0o755)

	tomlData := `[vector]
backend = "qdrant"

[qdrant]
url = "http://localhost:6334"
collection = "my_index"
api_key = "secret"
`
	os.WriteFile(filepath.Join(cfgDir, "shaktiman.toml"), []byte(tomlData), 0o644)

	cfg := DefaultConfig(dir)
	loaded, err := LoadConfigFromFile(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if loaded.VectorBackend != "qdrant" {
		t.Errorf("VectorBackend = %q, want qdrant", loaded.VectorBackend)
	}
	if loaded.QdrantURL != "http://localhost:6334" {
		t.Errorf("QdrantURL = %q", loaded.QdrantURL)
	}
	if loaded.QdrantCollection != "my_index" {
		t.Errorf("QdrantCollection = %q, want my_index", loaded.QdrantCollection)
	}
	if loaded.QdrantAPIKey != "secret" {
		t.Errorf("QdrantAPIKey = %q, want secret", loaded.QdrantAPIKey)
	}
}

func TestLoadConfigFromFile_EmbeddingSection(t *testing.T) {
	dir := t.TempDir()
	cfgDir := filepath.Join(dir, ".shaktiman")
	os.MkdirAll(cfgDir, 0o755)

	tomlData := `[embedding]
ollama_url = "http://remote:11434"
model = "all-minilm"
dims = 384
batch_size = 64
timeout = "60s"
`
	os.WriteFile(filepath.Join(cfgDir, "shaktiman.toml"), []byte(tomlData), 0o644)

	cfg := DefaultConfig(dir)
	loaded, err := LoadConfigFromFile(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if loaded.OllamaURL != "http://remote:11434" {
		t.Errorf("OllamaURL = %q", loaded.OllamaURL)
	}
	if loaded.EmbeddingModel != "all-minilm" {
		t.Errorf("EmbeddingModel = %q, want all-minilm", loaded.EmbeddingModel)
	}
	if loaded.EmbeddingDims != 384 {
		t.Errorf("EmbeddingDims = %d, want 384", loaded.EmbeddingDims)
	}
	if loaded.EmbedBatchSize != 64 {
		t.Errorf("EmbedBatchSize = %d, want 64", loaded.EmbedBatchSize)
	}
	if loaded.EmbedTimeout != 60*time.Second {
		t.Errorf("EmbedTimeout = %v, want 60s", loaded.EmbedTimeout)
	}
}

func TestLoadConfigFromFile_EnvVarOverrides(t *testing.T) {
	dir := t.TempDir()
	cfgDir := filepath.Join(dir, ".shaktiman")
	os.MkdirAll(cfgDir, 0o755)

	tomlData := `[postgres]
connection_string = "postgres://toml-value"

[qdrant]
api_key = "toml-key"
`
	os.WriteFile(filepath.Join(cfgDir, "shaktiman.toml"), []byte(tomlData), 0o644)

	// Env vars override TOML
	t.Setenv("SHAKTIMAN_POSTGRES_URL", "postgres://env-value")
	t.Setenv("SHAKTIMAN_QDRANT_API_KEY", "env-key")

	cfg := DefaultConfig(dir)
	loaded, err := LoadConfigFromFile(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if loaded.PostgresConnString != "postgres://env-value" {
		t.Errorf("PostgresConnString = %q, want postgres://env-value (env should override TOML)", loaded.PostgresConnString)
	}
	if loaded.QdrantAPIKey != "env-key" {
		t.Errorf("QdrantAPIKey = %q, want env-key (env should override TOML)", loaded.QdrantAPIKey)
	}
}

func TestLoadConfigFromFile_PgvectorBackend(t *testing.T) {
	dir := t.TempDir()
	cfgDir := filepath.Join(dir, ".shaktiman")
	os.MkdirAll(cfgDir, 0o755)

	tomlData := `[vector]
backend = "pgvector"
`
	os.WriteFile(filepath.Join(cfgDir, "shaktiman.toml"), []byte(tomlData), 0o644)

	cfg := DefaultConfig(dir)
	loaded, err := LoadConfigFromFile(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if loaded.VectorBackend != "pgvector" {
		t.Errorf("VectorBackend = %q, want pgvector", loaded.VectorBackend)
	}
}

func TestValidateBackendConfig(t *testing.T) {
	tests := []struct {
		name    string
		modify  func(*Config)
		wantErr bool
	}{
		{
			name:    "default config is valid",
			modify:  func(c *Config) {},
			wantErr: false,
		},
		{
			name: "pgvector requires postgres",
			modify: func(c *Config) {
				c.VectorBackend = "pgvector"
				c.DatabaseBackend = "sqlite"
			},
			wantErr: true,
		},
		{
			name: "pgvector with postgres is valid",
			modify: func(c *Config) {
				c.VectorBackend = "pgvector"
				c.DatabaseBackend = "postgres"
				c.PostgresConnString = "postgres://localhost/db"
			},
			wantErr: false,
		},
		{
			name: "postgres requires connection string",
			modify: func(c *Config) {
				c.DatabaseBackend = "postgres"
				c.PostgresConnString = ""
			},
			wantErr: true,
		},
		{
			name: "postgres with connection string is valid",
			modify: func(c *Config) {
				c.DatabaseBackend = "postgres"
				c.PostgresConnString = "postgres://localhost/db"
			},
			wantErr: false,
		},
		{
			name: "qdrant requires url",
			modify: func(c *Config) {
				c.VectorBackend = "qdrant"
				c.QdrantURL = ""
			},
			wantErr: true,
		},
		{
			name: "qdrant with url is valid",
			modify: func(c *Config) {
				c.VectorBackend = "qdrant"
				c.QdrantURL = "http://localhost:6334"
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := DefaultConfig("/tmp/proj")
			tt.modify(&cfg)
			err := ValidateBackendConfig(cfg)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateBackendConfig() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
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
		{"invalid vector backend", `[vector]` + "\n" + `backend = "faiss"`},
		{"invalid database backend", `[database]` + "\n" + `backend = "mysql"`},
		{"postgres max_open_conns < 1", `[postgres]` + "\n" + `max_open_conns = 0`},
		{"postgres max_idle_conns < 0", `[postgres]` + "\n" + `max_idle_conns = -1`},
		{"embedding dims too high", `[embedding]` + "\n" + `dims = 5000`},
		{"embedding dims too low", `[embedding]` + "\n" + `dims = 0`},
		{"embedding batch_size < 1", `[embedding]` + "\n" + `batch_size = 0`},
		{"embedding invalid timeout", `[embedding]` + "\n" + `timeout = "not-a-duration"`},
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
	// New sections
	if !contains(content, "[database]") {
		t.Error("expected [database] section")
	}
	if !contains(content, "[postgres]") {
		t.Error("expected [postgres] section")
	}
	if !contains(content, "[qdrant]") {
		t.Error("expected [qdrant] section")
	}
	if !contains(content, "[embedding]") {
		t.Error("expected [embedding] section")
	}
	if !contains(content, "# connection_string") {
		t.Error("expected commented connection_string")
	}
	if !contains(content, "# ollama_url") {
		t.Error("expected commented ollama_url")
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

func TestLoadConfigFromFile_BackwardCompatibility(t *testing.T) {
	// Old TOML files without new sections should parse without error
	// and retain default values for new fields.
	dir := t.TempDir()
	cfgDir := filepath.Join(dir, ".shaktiman")
	os.MkdirAll(cfgDir, 0o755)

	oldToml := `[search]
max_results = 25

[vector]
backend = "hnsw"
`
	os.WriteFile(filepath.Join(cfgDir, "shaktiman.toml"), []byte(oldToml), 0o644)

	cfg := DefaultConfig(dir)
	loaded, err := LoadConfigFromFile(cfg)
	if err != nil {
		t.Fatalf("old TOML should parse without error: %v", err)
	}
	if loaded.SearchMaxResults != 25 {
		t.Errorf("SearchMaxResults = %d, want 25", loaded.SearchMaxResults)
	}
	if loaded.VectorBackend != "hnsw" {
		t.Errorf("VectorBackend = %q, want hnsw", loaded.VectorBackend)
	}
	// New fields should retain defaults
	if loaded.DatabaseBackend != "sqlite" {
		t.Errorf("DatabaseBackend = %q, want sqlite", loaded.DatabaseBackend)
	}
	if loaded.PostgresMaxOpen != 20 {
		t.Errorf("PostgresMaxOpen = %d, want 20", loaded.PostgresMaxOpen)
	}
	if loaded.QdrantCollection != "shaktiman" {
		t.Errorf("QdrantCollection = %q, want shaktiman", loaded.QdrantCollection)
	}
	if loaded.EmbedTimeout != 120*time.Second {
		t.Errorf("EmbedTimeout = %v, want 120s", loaded.EmbedTimeout)
	}
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
