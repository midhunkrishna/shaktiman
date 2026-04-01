package types

import "time"

// DiffLogEntry represents a file-level change record.
type DiffLogEntry struct {
	ID           int64
	FileID       int64
	Timestamp    string
	ChangeType   string // add | modify | delete | rename
	LinesAdded   int
	LinesRemoved int
	HashBefore   string
	HashAfter    string
}

// DiffSymbolEntry represents a symbol-level change within a diff.
type DiffSymbolEntry struct {
	SymbolName string
	SymbolID   int64  // 0 if unknown
	ChangeType string // added | modified | removed | signature_changed
	ChunkID    int64  // 0 if unknown
}

// RecentDiffsInput configures a recent diffs query.
type RecentDiffsInput struct {
	Since  time.Time
	FileID int64 // 0 for all files
	Limit  int   // 0 for no limit
}

// PendingEdgeCaller holds a source symbol ID and the edge kind from a pending edge.
type PendingEdgeCaller struct {
	SrcSymbolID      int64
	Kind             string
	DstQualifiedName string
}
