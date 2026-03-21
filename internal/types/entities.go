// Package types defines shared entity types, interfaces, and configuration
// used across all internal packages to prevent import cycles (IP-11).
package types

import "time"

// FileRecord represents a source file tracked by the index.
type FileRecord struct {
	ID              int64
	Path            string  // project-relative path
	ContentHash     string  // SHA-256 hex
	Mtime           float64 // Unix timestamp with sub-second precision
	Size            int64
	Language        string
	IndexedAt       string // ISO8601
	EmbeddingStatus string // pending | partial | complete
	ParseQuality    string // full | partial | error | unparseable
}

// ChunkRecord represents a semantic code chunk within a file.
type ChunkRecord struct {
	ID            int64
	FileID        int64
	ParentChunkID *int64 // nullable — set after DB insert for nested chunks
	ChunkIndex    int    // positional order within file
	SymbolName    string
	Kind          string // function | class | method | type | interface | header | block
	StartLine     int
	EndLine       int
	Content       string
	TokenCount    int
	Signature     string
	ParseQuality  string // full | partial

	// ParentIndex is used during batch processing to link child chunks
	// to their parent before DB IDs are assigned (CA-10).
	ParentIndex *int
}

// SymbolRecord represents a named symbol extracted from a chunk.
type SymbolRecord struct {
	ID            int64
	ChunkID       int64
	FileID        int64
	Name          string
	QualifiedName string
	Kind          string // function | class | method | type | interface | variable | constant
	Line          int
	Signature     string
	Visibility    string // public | private | internal | exported
	IsExported    bool
}

// EdgeRecord represents a dependency edge between two symbols.
type EdgeRecord struct {
	ID            int64
	SrcSymbolID   int64
	DstSymbolID   int64
	SrcSymbolName string // set by parser, resolved to ID during write
	DstSymbolName string // set by parser, resolved to ID during write
	Kind          string // imports | calls | type_ref | inherits | implements
	FileID        int64
	IsCrossFile   bool
}

// WriteJobType distinguishes the kind of write operation.
type WriteJobType int

const (
	WriteJobEnrichment WriteJobType = iota
	WriteJobFileDelete
)

// WriteJob is submitted to the writer goroutine for serialized DB writes.
type WriteJob struct {
	Type        WriteJobType
	FilePath    string
	File        *FileRecord
	Chunks      []ChunkRecord
	Symbols     []SymbolRecord
	Edges       []EdgeRecord
	ContentHash string
	Timestamp   time.Time
	Done        chan error // optional; caller blocks on this for sync writes
}
