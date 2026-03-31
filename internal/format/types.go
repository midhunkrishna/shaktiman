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

// SymbolRef describes a symbol that references an unresolved name (e.g. an import of an external type).
type SymbolRef struct {
	Symbol   string `json:"symbol"`
	Kind     string `json:"kind"`
	FilePath string `json:"file_path"`
	Line     int    `json:"line"`
	Via      string `json:"via"`
}

// SymbolsWithRefs is the enriched response when no definitions are found
// but the name is referenced via pending edges (imports, type_ref, etc.).
type SymbolsWithRefs struct {
	Definitions  []SymbolResult `json:"definitions"`
	ReferencedBy []SymbolRef    `json:"referenced_by"`
	Note         string         `json:"note"`
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
