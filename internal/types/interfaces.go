package types

import (
	"context"
	"time"
)

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
	// GetSymbolByName returns symbols matching the given name (case-sensitive).
	GetSymbolByName(ctx context.Context, name string) ([]SymbolRecord, error)
	// GetSymbolByNameCI returns symbols matching the given name case-insensitively.
	GetSymbolByNameCI(ctx context.Context, name string) ([]SymbolRecord, error)
	// GetSymbolByID returns a single symbol by its ID.
	GetSymbolByID(ctx context.Context, id int64) (*SymbolRecord, error)
	// DeleteSymbolsByFile removes all symbols for a file.
	DeleteSymbolsByFile(ctx context.Context, fileID int64) error

	// GetFilePathByID returns the project-relative path for a file ID.
	GetFilePathByID(ctx context.Context, fileID int64) (string, error)
	// GetFileIsTestByID returns whether a file is classified as a test file.
	GetFileIsTestByID(ctx context.Context, fileID int64) (bool, error)
	// GetIndexStats returns aggregate statistics about the index.
	GetIndexStats(ctx context.Context) (*IndexStats, error)

	// KeywordSearch performs FTS5 full-text search on chunk content.
	KeywordSearch(ctx context.Context, query string, limit int) ([]FTSResult, error)
	// ComputeChangeScores returns recency*magnitude scores for chunk IDs.
	ComputeChangeScores(ctx context.Context, chunkIDs []int64) (map[int64]float64, error)
	// Neighbors performs BFS graph traversal from a symbol.
	Neighbors(ctx context.Context, symbolID int64, maxDepth int, direction string) ([]int64, error)

	// DeleteFileByPath removes a file by path and cascades to chunks/symbols.
	// Returns the file ID that was deleted, or 0 if not found.
	DeleteFileByPath(ctx context.Context, path string) (int64, error)
	// GetEmbeddedChunkIDsByFile returns IDs of chunks with embedded=1 for a file.
	GetEmbeddedChunkIDsByFile(ctx context.Context, fileID int64) ([]int64, error)
	// UpdateChunkParents sets parent_chunk_id for the given chunk→parent mappings.
	UpdateChunkParents(ctx context.Context, updates map[int64]int64) error
}

// BatchMetadataStore extends MetadataStore with batch query methods.
// Callers use type assertion to detect support and fall back to per-item queries.
type BatchMetadataStore interface {
	MetadataStore

	// BatchGetSymbolIDsForChunks returns chunkID → symbolID mapping.
	// Replicates lookupSymbolForChunk same-file-match fallback logic.
	BatchGetSymbolIDsForChunks(ctx context.Context, chunkIDs []int64) (map[int64]int64, error)

	// BatchNeighbors returns symbolID → []neighborSymbolID for each seed.
	BatchNeighbors(ctx context.Context, symbolIDs []int64, maxDepth int) (map[int64][]int64, error)

	// BatchGetChunkIDsForSymbols returns symbolID → chunkID mapping.
	BatchGetChunkIDsForSymbols(ctx context.Context, symbolIDs []int64) (map[int64]int64, error)

	// BatchHydrateChunks returns chunk data joined with file paths and is_test.
	BatchHydrateChunks(ctx context.Context, chunkIDs []int64) ([]HydratedChunk, error)

	// BatchGetFileHashes returns path → contentHash for existing files.
	BatchGetFileHashes(ctx context.Context, paths []string) (map[string]string, error)
}

// VectorResult holds a single vector similarity match.
type VectorResult struct {
	ChunkID int64   `json:"chunk_id"`
	Score   float64 `json:"score"` // cosine similarity normalized to [0,1]
}

// VectorStore provides vector similarity search and storage operations.
// Default implementation: BruteForceStore (in-memory). Alternative backends
// (HNSW, Qdrant, pgvector) implement this same interface.
type VectorStore interface {
	Search(ctx context.Context, query []float32, topK int) ([]VectorResult, error)
	Upsert(ctx context.Context, chunkID int64, vector []float32) error
	UpsertBatch(ctx context.Context, chunkIDs []int64, vectors [][]float32) error
	Delete(ctx context.Context, chunkIDs []int64) error
	Has(ctx context.Context, chunkID int64) (bool, error)
	Count(ctx context.Context) (int, error)
	Close() error
	// Healthy returns true if the store is reachable and operational.
	// In-process backends (BruteForce, HNSW) always return true.
	// External backends (Qdrant, pgvector) perform a connectivity check.
	Healthy(ctx context.Context) bool
}

// VectorPersister is optionally implemented by vector stores that need
// explicit disk persistence (e.g. BruteForceStore). Stores with built-in
// persistence (Qdrant, HNSW) do not implement this.
type VectorPersister interface {
	SaveToDisk(path string) error
	LoadFromDisk(path string) error
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
	Skipped  int    // chunks that could not be embedded after retries
	Warning  string // non-empty during transient issues (e.g., circuit breaker retry)
}

// TxHandle is an opaque transaction handle. Each backend defines its own
// concrete type (e.g., *sql.Tx for SQLite, pgx.Tx for Postgres).
// Methods that participate in transactions accept TxHandle.
type TxHandle interface {
	// IsTxHandle is a marker method identifying transaction handle implementations.
	IsTxHandle()
}

// DiffStore provides diff log and symbol change tracking.
type DiffStore interface {
	InsertDiffLog(ctx context.Context, tx TxHandle, entry DiffLogEntry) (int64, error)
	InsertDiffSymbols(ctx context.Context, tx TxHandle, diffID int64, symbols []DiffSymbolEntry) error
	GetRecentDiffs(ctx context.Context, input RecentDiffsInput) ([]DiffLogEntry, error)
	GetDiffSymbols(ctx context.Context, diffID int64) ([]DiffSymbolEntry, error)
}

// GraphMutator provides graph write operations (edges, pending edges).
// Extends the existing read-only Neighbors method on MetadataStore.
type GraphMutator interface {
	InsertEdges(ctx context.Context, tx TxHandle, fileID int64, edges []EdgeRecord, symbolIDs map[string]int64, language string) error
	ResolvePendingEdges(ctx context.Context, tx TxHandle, newSymbolNames []string) error
	DeleteEdgesByFile(ctx context.Context, tx TxHandle, fileID int64) error
	PendingEdgeCallers(ctx context.Context, dstName string) ([]int64, error)
	PendingEdgeCallersWithKind(ctx context.Context, dstName string) ([]PendingEdgeCaller, error)
}

// EmbeddingReconciler provides embedding state management for crash recovery.
type EmbeddingReconciler interface {
	CountChunksEmbedded(ctx context.Context) (int, error)
	ResetAllEmbeddedFlags(ctx context.Context) error
	GetEmbeddedChunkIDs(ctx context.Context, afterID int64, limit int) ([]int64, error)
	ResetEmbeddedFlags(ctx context.Context, chunkIDs []int64) error
	EmbeddingReadiness(ctx context.Context, vectorCount int) (float64, error)
}

// StoreLifecycle provides backend-specific startup and bulk-write hooks.
// Returned as a separate value by the factory — not embedded in WriterStore.
// SQLite: manages FTS5 triggers and index rebuild.
// Postgres: nil (generated tsvector columns handle FTS automatically).
type StoreLifecycle interface {
	OnStartup(ctx context.Context) error
	OnBulkWriteBegin(ctx context.Context) error
	OnBulkWriteEnd(ctx context.Context) error
}

// MetricsWriter persists MCP tool call metrics.
type MetricsWriter interface {
	RecordToolCalls(ctx context.Context, records []ToolCallRecord) error
}

// ToolCallRecord holds a single MCP tool invocation metric.
type ToolCallRecord struct {
	SessionID         string
	Timestamp         time.Time
	ToolName          string
	ArgsJSON          string
	ArgsBytes         int
	ResponseBytes     int
	ResponseTokensEst int
	ResultCount       int
	DurationMs        int64
	IsError           bool
}

// WriterStore is the composite interface required by the daemon's write path.
// The daemon's writer.go and enrichment.go depend on all of these capabilities.
// StoreLifecycle is NOT embedded here — it is returned separately by the
// factory and may be nil (e.g., Postgres needs no lifecycle hooks).
type WriterStore interface {
	MetadataStore
	DiffStore
	GraphMutator
	EmbeddingReconciler
	EmbedSource
	MetricsWriter
	WithWriteTx(ctx context.Context, fn func(tx TxHandle) error) error
}
