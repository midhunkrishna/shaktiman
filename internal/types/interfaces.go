package types

import "context"

// FTSResult holds a single full-text search match.
type FTSResult struct {
	ChunkID int64
	Rank    float64 // BM25 rank (lower = more relevant)
}

// MetadataStore provides CRUD and query operations for files, chunks, and symbols.
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
	// GetSymbolByID returns a single symbol by its ID.
	GetSymbolByID(ctx context.Context, id int64) (*SymbolRecord, error)
	// DeleteSymbolsByFile removes all symbols for a file.
	DeleteSymbolsByFile(ctx context.Context, fileID int64) error

	// GetFilePathByID returns the project-relative path for a file ID.
	GetFilePathByID(ctx context.Context, fileID int64) (string, error)
	// GetIndexStats returns aggregate statistics about the index.
	GetIndexStats(ctx context.Context) (*IndexStats, error)

	// KeywordSearch performs FTS5 full-text search on chunk content.
	KeywordSearch(ctx context.Context, query string, limit int) ([]FTSResult, error)
	// ComputeChangeScores returns recency*magnitude scores for chunk IDs.
	ComputeChangeScores(ctx context.Context, chunkIDs []int64) (map[int64]float64, error)
	// Neighbors performs BFS graph traversal from a symbol.
	Neighbors(ctx context.Context, symbolID int64, maxDepth int, direction string) ([]int64, error)
}

// VectorResult holds a single vector similarity match.
type VectorResult struct {
	ChunkID int64   `json:"chunk_id"`
	Score   float64 `json:"score"` // cosine similarity normalized to [0,1]
}

// VectorStore provides vector similarity search operations.
// Default: brute-force in-process. Optional: Qdrant (Phase 3+).
type VectorStore interface {
	Search(ctx context.Context, query []float32, topK int) ([]VectorResult, error)
	Upsert(ctx context.Context, chunkID int64, vector []float32) error
	Delete(ctx context.Context, chunkIDs []int64) error
	Count(ctx context.Context) (int, error)
}

// Embedder produces vector embeddings from text.
type Embedder interface {
	Embed(ctx context.Context, text string) ([]float32, error)
	EmbedBatch(ctx context.Context, texts []string) ([][]float32, error)
}

// VectorDeleter removes vectors by chunk ID. Used to decouple the writer
// from the vector package while still cleaning up stale embeddings.
type VectorDeleter interface {
	Delete(ctx context.Context, chunkIDs []int64) error
}

// SessionScorer provides session-aware relevance scores for code locations.
type SessionScorer interface {
	Score(filePath string, startLine int) float64
}

// GraphStore provides graph traversal operations.
// Deprecated: Neighbors is now on MetadataStore. Kept for backward compatibility.
type GraphStore interface {
	Neighbors(ctx context.Context, symbolID int64, maxDepth int, direction string) ([]int64, error)
}

// EmbedJob represents a single chunk to be embedded.
type EmbedJob struct {
	ChunkID int64
	Content string
}

// EmbedSource provides pull-based access to chunks needing embedding.
// Used by EmbedWorker.RunFromDB for cursor-based embedding.
type EmbedSource interface {
	GetEmbedPage(ctx context.Context, afterID int64, limit int) ([]EmbedJob, error)
	MarkChunksEmbedded(ctx context.Context, chunkIDs []int64) error
	CountChunksNeedingEmbedding(ctx context.Context) (int, error)
}

// EmbedProgress reports embedding progress during RunFromDB.
type EmbedProgress struct {
	Embedded int
	Total    int
	Warning  string // non-empty during transient issues (e.g., circuit breaker retry)
}
