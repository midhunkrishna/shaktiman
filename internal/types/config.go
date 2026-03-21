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
	}
}
