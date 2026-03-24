package format

// SymbolResult is a symbol lookup result for display.
type SymbolResult struct {
	Name       string `json:"name"`
	Kind       string `json:"kind"`
	Line       int    `json:"line"`
	Signature  string `json:"signature,omitempty"`
	Visibility string `json:"visibility"`
	FilePath   string `json:"file_path"`
}

// DepResult is a dependency graph result for display.
type DepResult struct {
	Name     string `json:"name"`
	Kind     string `json:"kind"`
	FilePath string `json:"file_path"`
	Line     int    `json:"line"`
}

// DiffResult is a recent-diff result for display.
type DiffResult struct {
	FileID       int64    `json:"file_id"`
	FilePath     string   `json:"file_path"`
	ChangeType   string   `json:"change_type"`
	LinesAdded   int      `json:"lines_added"`
	LinesRemoved int      `json:"lines_removed"`
	Timestamp    string   `json:"timestamp"`
	Symbols      []string `json:"affected_symbols,omitempty"`
}
