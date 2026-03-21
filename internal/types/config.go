package types

import "path/filepath"

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
}

// DefaultConfig returns a Config with sane defaults for the given project root.
func DefaultConfig(projectRoot string) Config {
	return Config{
		ProjectRoot:       projectRoot,
		DBPath:            filepath.Join(projectRoot, ".shaktiman", "index.db"),
		MaxBudgetTokens:   8192,
		DefaultMaxResults: 50,
		WriterChannelSize: 500,
		EnrichmentWorkers: 4,
		Tokenizer:         "cl100k_base",
		WatcherEnabled:    true,
		WatcherDebounceMs: 200,
		OllamaURL:         "http://localhost:11434",
		EmbeddingModel:    "nomic-embed-text",
		EmbeddingDims:     768,
		EmbeddingsPath:    filepath.Join(projectRoot, ".shaktiman", "embeddings.bin"),
		EmbedBatchSize:    32,
		EmbedEnabled:      true,
	}
}
