package types

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"time"

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
	VectorBackend string // "brute_force" (default), "hnsw", "qdrant", or "pgvector"

	// Database backend configuration
	DatabaseBackend string // "sqlite" (default) or "postgres"

	// PostgreSQL configuration (used when DatabaseBackend = "postgres")
	PostgresConnString string
	PostgresMaxOpen    int    // connection pool max open (default: 20)
	PostgresMaxIdle    int    // connection pool max idle (default: 10)
	PostgresSchema     string // Postgres schema name (default: "public")

	// Qdrant configuration (used when VectorBackend = "qdrant")
	QdrantURL        string // Qdrant HTTP API URL (e.g. "http://localhost:6334")
	QdrantCollection string // collection name (default: "shaktiman")
	QdrantAPIKey     string // optional API key (prefer env var SHAKTIMAN_QDRANT_API_KEY)

	// Embedding timeout
	EmbedTimeout time.Duration // HTTP timeout per embedding request (default: 120s)

	// Test file detection patterns (glob patterns and directory prefixes)
	TestPatterns []string // e.g. ["*_test.go", "testdata/", "*.test.ts"]
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

		VectorBackend:   "brute_force",
		DatabaseBackend: "sqlite",

		PostgresMaxOpen: 20,
		PostgresMaxIdle: 10,
		PostgresSchema:  "public",

		QdrantCollection: "shaktiman",

		EmbedTimeout: 120 * time.Second,
	}
}

// tomlConfig mirrors the TOML file structure for deserialization.
// Pointer fields distinguish "key present" from "key absent".
type tomlConfig struct {
	Database  tomlDatabase  `toml:"database"`
	Postgres  tomlPostgres  `toml:"postgres"`
	Search    tomlSearch    `toml:"search"`
	Context   tomlContext   `toml:"context"`
	Vector    tomlVector    `toml:"vector"`
	Qdrant    tomlQdrant    `toml:"qdrant"`
	Embedding tomlEmbedding `toml:"embedding"`
	Test      tomlTest      `toml:"test"`
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

type tomlTest struct {
	Patterns []string `toml:"patterns"`
}

type tomlDatabase struct {
	Backend *string `toml:"backend"`
}

type tomlPostgres struct {
	ConnectionString *string `toml:"connection_string"`
	MaxOpenConns     *int    `toml:"max_open_conns"`
	MaxIdleConns     *int    `toml:"max_idle_conns"`
	Schema           *string `toml:"schema"`
}

type tomlQdrant struct {
	URL        *string `toml:"url"`
	Collection *string `toml:"collection"`
	APIKey     *string `toml:"api_key"`
}

type tomlEmbedding struct {
	OllamaURL *string `toml:"ollama_url"`
	Model     *string `toml:"model"`
	Dims      *int    `toml:"dims"`
	BatchSize *int    `toml:"batch_size"`
	Timeout   *string `toml:"timeout"`
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
		switch *v {
		case "brute_force", "hnsw", "qdrant", "pgvector":
			cfg.VectorBackend = *v
		default:
			return cfg, fmt.Errorf("config: vector.backend must be 'brute_force', 'hnsw', 'qdrant', or 'pgvector', got %q", *v)
		}
	}
	if len(tc.Test.Patterns) > 0 {
		cfg.TestPatterns = tc.Test.Patterns
	}

	// [database] section
	if v := tc.Database.Backend; v != nil {
		if *v != "sqlite" && *v != "postgres" {
			return cfg, fmt.Errorf("config: database.backend must be 'sqlite' or 'postgres', got %q", *v)
		}
		cfg.DatabaseBackend = *v
	}

	// [postgres] section
	if v := tc.Postgres.ConnectionString; v != nil {
		cfg.PostgresConnString = *v
	}
	if v := tc.Postgres.MaxOpenConns; v != nil {
		if *v < 1 {
			return cfg, fmt.Errorf("config: postgres.max_open_conns must be >= 1, got %d", *v)
		}
		cfg.PostgresMaxOpen = *v
	}
	if v := tc.Postgres.MaxIdleConns; v != nil {
		if *v < 0 {
			return cfg, fmt.Errorf("config: postgres.max_idle_conns must be >= 0, got %d", *v)
		}
		cfg.PostgresMaxIdle = *v
	}
	if v := tc.Postgres.Schema; v != nil {
		cfg.PostgresSchema = *v
	}

	// [qdrant] section
	if v := tc.Qdrant.URL; v != nil {
		cfg.QdrantURL = *v
	}
	if v := tc.Qdrant.Collection; v != nil {
		cfg.QdrantCollection = *v
	}
	if v := tc.Qdrant.APIKey; v != nil {
		cfg.QdrantAPIKey = *v
	}

	// [embedding] section
	if v := tc.Embedding.OllamaURL; v != nil {
		cfg.OllamaURL = *v
	}
	if v := tc.Embedding.Model; v != nil {
		cfg.EmbeddingModel = *v
	}
	if v := tc.Embedding.Dims; v != nil {
		if *v < 1 || *v > 4096 {
			return cfg, fmt.Errorf("config: embedding.dims must be 1-4096, got %d", *v)
		}
		cfg.EmbeddingDims = *v
	}
	if v := tc.Embedding.BatchSize; v != nil {
		if *v < 1 {
			return cfg, fmt.Errorf("config: embedding.batch_size must be >= 1, got %d", *v)
		}
		cfg.EmbedBatchSize = *v
	}
	if v := tc.Embedding.Timeout; v != nil {
		d, err := time.ParseDuration(*v)
		if err != nil {
			return cfg, fmt.Errorf("config: embedding.timeout: %w", err)
		}
		cfg.EmbedTimeout = d
	}

	// Environment variable overrides for secrets (highest priority after CLI flags)
	if v := os.Getenv("SHAKTIMAN_POSTGRES_URL"); v != "" {
		cfg.PostgresConnString = v
	}
	if v := os.Getenv("SHAKTIMAN_QDRANT_API_KEY"); v != "" {
		cfg.QdrantAPIKey = v
	}

	return cfg, nil
}

// ValidateBackendConfig checks that the configured backend combination is valid.
// Call after all config sources (defaults, TOML, env, CLI) have been merged.
func ValidateBackendConfig(cfg Config) error {
	if cfg.VectorBackend == "pgvector" && cfg.DatabaseBackend != "postgres" {
		return fmt.Errorf("config: vector.backend 'pgvector' requires database.backend 'postgres'")
	}
	if cfg.DatabaseBackend == "postgres" && cfg.PostgresConnString == "" {
		return fmt.Errorf("config: database.backend 'postgres' requires postgres.connection_string or SHAKTIMAN_POSTGRES_URL")
	}
	if cfg.VectorBackend == "qdrant" && cfg.QdrantURL == "" {
		return fmt.Errorf("config: vector.backend 'qdrant' requires qdrant.url")
	}
	return nil
}

const sampleConfig = `# Shaktiman configuration
# Uncomment and modify values to override defaults.

[database]
# backend = "sqlite"             # "sqlite" (default) or "postgres"

[postgres]
# connection_string = "postgres://user:pass@localhost:5432/shaktiman?sslmode=disable"
# max_open_conns = 20            # Connection pool max open (default: 20)
# max_idle_conns = 10            # Connection pool max idle (default: 10)
# schema = "public"              # Postgres schema (default: "public")

[search]
# max_results = 10               # Max results per search (1-200)
# default_mode = "locate"        # "locate" (headers only) or "full" (with source code)
# min_score = 0.15               # Drop results below this relevance score (0.0-1.0)

[context]
# enabled = true                 # Set false to disable the context tool entirely
# budget_tokens = 4096           # Default token budget for context assembly (256-32768)

[vector]
# backend = "brute_force"        # "brute_force", "hnsw", "qdrant", or "pgvector"

[qdrant]
# url = "http://localhost:6334"  # Qdrant HTTP API URL
# collection = "shaktiman"       # Collection name (default: "shaktiman")
# api_key = ""                   # API key (prefer env var SHAKTIMAN_QDRANT_API_KEY)

[embedding]
# ollama_url = "http://localhost:11434"  # Ollama API base URL
# model = "nomic-embed-text"             # Embedding model name
# dims = 768                             # Vector dimensionality
# batch_size = 128                       # Texts per batch request
# timeout = "120s"                       # HTTP timeout per request

[test]
# patterns = ["*_test.go", "testdata/"]  # Glob patterns identifying test files
#                                         # Auto-populated after first index
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

// langTestPatterns maps each supported language to its default test file patterns.
// Patterns ending with "/" are directory prefixes; others are basename globs.
var langTestPatterns = map[string][]string{
	"go":         {"*_test.go", "testdata/"},
	"python":     {"test_*.py", "*_test.py"},
	"typescript": {"*.test.ts", "*.spec.ts", "*.test.tsx", "*.spec.tsx", "__tests__/"},
	"javascript": {"*.test.js", "*.spec.js", "*.test.jsx", "*.spec.jsx", "*.test.mjs", "*.spec.mjs", "__tests__/"},
	"java":       {"*Test.java", "*Tests.java", "src/test/"},
"rust":       {"tests/"},
	"bash":       {"test_*.sh", "*_test.sh"},
}

// DefaultTestPatterns returns a deduplicated, sorted flat list of test file
// patterns for the given languages. Used to auto-populate [test].patterns in
// shaktiman.toml after indexing.
func DefaultTestPatterns(languages []string) []string {
	seen := make(map[string]bool)
	var patterns []string
	for _, lang := range languages {
		for _, p := range langTestPatterns[lang] {
			if !seen[p] {
				seen[p] = true
				patterns = append(patterns, p)
			}
		}
	}
	sort.Strings(patterns)
	return patterns
}
