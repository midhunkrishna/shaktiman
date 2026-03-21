package types

import "context"

// MetadataStore provides CRUD operations for files, chunks, and symbols.
type MetadataStore interface {
	// UpsertFile inserts or updates a file record, returning the file ID.
	UpsertFile(ctx context.Context, file *FileRecord) (int64, error)
	// GetFileByPath returns a file record by its project-relative path.
	GetFileByPath(ctx context.Context, path string) (*FileRecord, error)
	// ListFiles returns all tracked file records.
	ListFiles(ctx context.Context) ([]FileRecord, error)
	// DeleteFile removes a file and cascades to its chunks and symbols.
	DeleteFile(ctx context.Context, fileID int64) error

	// InsertChunks bulk-inserts chunks for a file, returning assigned IDs.
	InsertChunks(ctx context.Context, fileID int64, chunks []ChunkRecord) ([]int64, error)
	// GetChunksByFile returns all chunks for a file, ordered by chunk_index.
	GetChunksByFile(ctx context.Context, fileID int64) ([]ChunkRecord, error)
	// GetChunkByID returns a single chunk by its ID.
	GetChunkByID(ctx context.Context, id int64) (*ChunkRecord, error)
	// DeleteChunksByFile removes all chunks for a file.
	DeleteChunksByFile(ctx context.Context, fileID int64) error

	// InsertSymbols bulk-inserts symbols for a file, returning assigned IDs.
	InsertSymbols(ctx context.Context, fileID int64, symbols []SymbolRecord) ([]int64, error)
	// GetSymbolsByFile returns all symbols for a file.
	GetSymbolsByFile(ctx context.Context, fileID int64) ([]SymbolRecord, error)
	// GetSymbolByName returns symbols matching the given name.
	GetSymbolByName(ctx context.Context, name string) ([]SymbolRecord, error)
	// DeleteSymbolsByFile removes all symbols for a file.
	DeleteSymbolsByFile(ctx context.Context, fileID int64) error

	// GetIndexStats returns aggregate statistics about the index.
	GetIndexStats(ctx context.Context) (*IndexStats, error)
}

// VectorStore provides vector similarity search operations.
// Default: brute-force in-process. Optional: Qdrant (Phase 3+).
type VectorStore interface {
	Search(ctx context.Context, query []float32, limit int) ([]ScoredResult, error)
	Upsert(ctx context.Context, chunkID int64, vector []float32) error
	Delete(ctx context.Context, chunkIDs []int64) error
	Count(ctx context.Context) (int, error)
}

// GraphStore provides graph traversal operations.
// Default: SQLite recursive CTEs. Optional: CSR (Phase 3+).
type GraphStore interface {
	Neighbors(ctx context.Context, symbolID int64, maxDepth int, direction string) ([]int64, error)
}
