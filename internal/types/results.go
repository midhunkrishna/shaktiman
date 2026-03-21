package types

// ScoredResult represents a chunk with its relevance score from retrieval.
type ScoredResult struct {
	ChunkID    int64   `json:"chunk_id"`
	Score      float64 `json:"score"`
	Path       string  `json:"path"`
	SymbolName string  `json:"symbol_name,omitempty"`
	Kind       string  `json:"kind"`
	StartLine  int     `json:"start_line"`
	EndLine    int     `json:"end_line"`
	Content    string  `json:"content"`
	TokenCount int     `json:"token_count"`
}

// ContextPackage is the assembled response for a context query,
// containing ranked chunks fitted to a token budget.
type ContextPackage struct {
	Chunks      []ScoredResult `json:"chunks"`
	TotalTokens int            `json:"total_tokens"`
	Strategy    string         `json:"strategy"` // keyword_l2 | filesystem_l3
	Meta        map[string]any `json:"meta,omitempty"`
}

// IndexStats holds aggregate statistics about the code index.
type IndexStats struct {
	TotalFiles   int            `json:"total_files"`
	TotalChunks  int            `json:"total_chunks"`
	TotalSymbols int            `json:"total_symbols"`
	Languages    map[string]int `json:"languages"`
	ParseErrors  int            `json:"parse_errors"`
	StaleFiles   int            `json:"stale_files"`
}
