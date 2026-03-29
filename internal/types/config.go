package types

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
)

// Config holds all runtime configuration for a Shaktiman instance.
// Treat as immutable after initialization (CFG-2).
type Config struct {
	ProjectRoot       string
	DBPath            string
	MaxBudgetTokens   int
	DefaultMaxResults int
	WriterChannelSize int
	EnrichmentWorkers int
	Tokenizer         string
	WatcherEnabled    bool
	WatcherDebounceMs int

	// Embedding configuration (Phase 3)
	OllamaURL      string // Ollama API base URL
	EmbeddingModel string // model name (e.g. "nomic-embed-text")
	EmbeddingDims  int    // vector dimensionality (e.g. 768)
	EmbeddingsPath string // binary persistence file path
	EmbedBatchSize int    // max texts per Ollama batch request
	EmbedEnabled   bool   // whether to run the embedding worker

	// MCP search tool configuration
	SearchMaxResults  int     // 1-200, default 10
	SearchDefaultMode string  // "locate" or "full", default "locate"
	SearchMinScore    float64 // 0.0-1.0, default 0.15

	// MCP context tool configuration
	ContextEnabled      bool // whether to register the context tool
	ContextBudgetTokens int  // 256-32768, default 4096

	// Vector backend configuration
	VectorBackend string // "brute_force" (default) or "hnsw"
}

// DefaultConfig returns a Config with sane defaults for the given project root.
func DefaultConfig(projectRoot string) Config {
	return Config{
		ProjectRoot:       projectRoot,
		DBPath:            filepath.Join(projectRoot, ".shaktiman", "index.db"),
		MaxBudgetTokens:   4096,
		DefaultMaxResults: 10,
		WriterChannelSize: 500,
		EnrichmentWorkers: 4,
		Tokenizer:         "cl100k_base",
		WatcherEnabled:    true,
		WatcherDebounceMs: 200,
		OllamaURL:         "http://localhost:11434",
		EmbeddingModel:    "nomic-embed-text",
		EmbeddingDims:     768,
		EmbeddingsPath:    filepath.Join(projectRoot, ".shaktiman", "embeddings.bin"),
		EmbedBatchSize:    128,
		EmbedEnabled:      true,

		SearchMaxResults:  10,
		SearchDefaultMode: "locate",
		SearchMinScore:    0.15,

		ContextEnabled:      true,
		ContextBudgetTokens: 4096,

		VectorBackend: "brute_force",
	}
}

// tomlConfig mirrors the TOML file structure for deserialization.
// Pointer fields distinguish "key present" from "key absent".
type tomlConfig struct {
	Search  tomlSearch  `toml:"search"`
	Context tomlContext `toml:"context"`
	Vector  tomlVector  `toml:"vector"`
}

type tomlVector struct {
	Backend *string `toml:"backend"`
}

type tomlSearch struct {
	MaxResults  *int     `toml:"max_results"`
	DefaultMode *string  `toml:"default_mode"`
	MinScore    *float64 `toml:"min_score"`
}

type tomlContext struct {
	Enabled      *bool `toml:"enabled"`
	BudgetTokens *int  `toml:"budget_tokens"`
}

// LoadConfigFromFile reads shaktiman.toml and merges values into cfg.
// Missing keys retain their existing (default) values. If the file does not
// exist, a sample config is written (best-effort) and cfg is returned unchanged.
func LoadConfigFromFile(cfg Config) (Config, error) {
	path := filepath.Join(cfg.ProjectRoot, ".shaktiman", "shaktiman.toml")
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		WriteSampleConfig(cfg.ProjectRoot)
		return cfg, nil
	}
	if err != nil {
		return cfg, fmt.Errorf("read config %s: %w", path, err)
	}

	var tc tomlConfig
	if err := toml.Unmarshal(data, &tc); err != nil {
		return cfg, fmt.Errorf("parse config %s: %w", path, err)
	}

	if v := tc.Search.MaxResults; v != nil {
		if *v < 1 || *v > 200 {
			return cfg, fmt.Errorf("config: search.max_results must be 1-200, got %d", *v)
		}
		cfg.SearchMaxResults = *v
		cfg.DefaultMaxResults = *v
	}
	if v := tc.Search.DefaultMode; v != nil {
		if *v != "locate" && *v != "full" {
			return cfg, fmt.Errorf("config: search.default_mode must be 'locate' or 'full', got %q", *v)
		}
		cfg.SearchDefaultMode = *v
	}
	if v := tc.Search.MinScore; v != nil {
		if *v < 0.0 || *v > 1.0 {
			return cfg, fmt.Errorf("config: search.min_score must be 0.0-1.0, got %f", *v)
		}
		cfg.SearchMinScore = *v
	}
	if v := tc.Context.Enabled; v != nil {
		cfg.ContextEnabled = *v
	}
	if v := tc.Context.BudgetTokens; v != nil {
		if *v < 256 || *v > 32768 {
			return cfg, fmt.Errorf("config: context.budget_tokens must be 256-32768, got %d", *v)
		}
		cfg.ContextBudgetTokens = *v
		cfg.MaxBudgetTokens = *v
	}
	if v := tc.Vector.Backend; v != nil {
		if *v != "brute_force" && *v != "hnsw" {
			return cfg, fmt.Errorf("config: vector.backend must be 'brute_force' or 'hnsw', got %q", *v)
		}
		cfg.VectorBackend = *v
	}

	return cfg, nil
}

const sampleConfig = `# Shaktiman configuration
# Uncomment and modify values to override defaults.

[search]
# max_results = 10        # Max results per search (1-200)
# default_mode = "locate"  # "locate" (headers only) or "full" (with source code)
# min_score = 0.15         # Drop results below this relevance score (0.0-1.0)

[context]
# enabled = true           # Set false to disable the context tool entirely
# budget_tokens = 4096     # Default token budget for context assembly (256-32768)

[vector]
# backend = "brute_force"  # "brute_force" or "hnsw"
`

// WriteSampleConfig creates a sample shaktiman.toml with commented-out defaults.
// Errors are logged but not fatal.
func WriteSampleConfig(projectRoot string) {
	dir := filepath.Join(projectRoot, ".shaktiman")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		slog.Debug("write sample config: mkdir", "err", err)
		return
	}
	path := filepath.Join(dir, "shaktiman.toml")
	if err := os.WriteFile(path, []byte(sampleConfig), 0o644); err != nil {
		slog.Debug("write sample config", "err", err)
	}
}
