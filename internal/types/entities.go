// Package types defines shared entity types, interfaces, and configuration
// used across all internal packages to prevent import cycles (IP-11).
package types

import (
	"fmt"
	"time"
)

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
	IsTest          bool   // true if file matches test file patterns
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
	ID               int64
	SrcSymbolID      int64
	DstSymbolID      int64
	SrcSymbolName    string // set by parser, resolved to ID during write
	DstSymbolName    string // set by parser, resolved to ID during write
	DstQualifiedName string // full import path (e.g. "java.util.List", "fmt", "std::collections::HashMap")
	Kind             string // imports | calls | type_ref | inherits | implements
	FileID           int64
	IsCrossFile      bool
}

// HydratedChunk holds chunk data joined with file metadata.
// Used by batch hydration to eliminate per-result queries.
type HydratedChunk struct {
	ChunkID    int64
	FileID     int64
	Path       string
	IsTest     bool
	SymbolName string
	Kind       string
	StartLine  int
	EndLine    int
	Content    string
	TokenCount int
}

// SiblingKey identifies a group of split sibling chunks by their shared attributes.
type SiblingKey struct {
	FileID     int64
	SymbolName string
	Kind       string
}

// String returns a map key for this SiblingKey.
func (k SiblingKey) String() string {
	return fmt.Sprintf("%d:%s:%s", k.FileID, k.SymbolName, k.Kind)
}

// WriteJobType distinguishes the kind of write operation.
type WriteJobType int

// WriteJobType values identify the operation a WriteJob performs.
const (
	WriteJobEnrichment WriteJobType = iota
	WriteJobFileDelete
	WriteJobSync // no-op barrier; signals Done without touching the database
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
