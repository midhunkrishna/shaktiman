package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/shaktimanai/shaktiman/internal/types"
)

// Compile-time check: *Store satisfies WriterStore.
var _ types.WriterStore = (*Store)(nil)

// Store provides metadata CRUD operations backed by SQLite.
type Store struct {
	db *DB
}

// NewStore creates a Store wrapping the given dual-DB instance.
func NewStore(db *DB) *Store {
	return &Store{db: db}
}

// DB returns the underlying database for direct access when needed.
func (s *Store) DB() *DB {
	return s.db
}

// WithWriteTx executes fn within a write transaction using an opaque TxHandle.
func (s *Store) WithWriteTx(ctx context.Context, fn func(tx types.TxHandle) error) error {
	return s.db.WithWriteTxCtx(ctx, fn)
}

// ── File operations ──

// UpsertFile inserts or updates a file record. Returns the file ID.
func (s *Store) UpsertFile(ctx context.Context, file *types.FileRecord) (int64, error) {
	now := time.Now().UTC().Format(time.RFC3339Nano)

	var id int64
	err := s.db.WithWriteTx(func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `
			INSERT INTO files (path, content_hash, mtime, size, language, indexed_at, embedding_status, parse_quality, is_test)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(path) DO UPDATE SET
				content_hash = excluded.content_hash,
				mtime = excluded.mtime,
				size = excluded.size,
				language = excluded.language,
				indexed_at = excluded.indexed_at,
				embedding_status = excluded.embedding_status,
				parse_quality = excluded.parse_quality,
				is_test = excluded.is_test`,
			file.Path, file.ContentHash, file.Mtime, file.Size,
			file.Language, now,
			coalesce(file.EmbeddingStatus, "pending"),
			coalesce(file.ParseQuality, "full"),
			boolToInt(file.IsTest),
		)
		if err != nil {
			return fmt.Errorf("upsert file %s: %w", file.Path, err)
		}
		_ = res // LastInsertId is unreliable for ON CONFLICT DO UPDATE in SQLite

		// Always SELECT the file ID after upsert. sqlite3_last_insert_rowid()
		// is connection-scoped and returns stale values when the ON CONFLICT
		// DO UPDATE path is taken (it keeps the rowid from a previous INSERT,
		// possibly into a different table).
		err = tx.QueryRowContext(ctx, "SELECT id FROM files WHERE path = ?", file.Path).Scan(&id)
		if err != nil {
			return fmt.Errorf("get file id for %s: %w", file.Path, err)
		}
		return nil
	})
	return id, err
}

// GetFileByPath returns a file record by its project-relative path.
func (s *Store) GetFileByPath(ctx context.Context, path string) (*types.FileRecord, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, path, content_hash, mtime, size, language, indexed_at,
		       embedding_status, parse_quality, is_test
		FROM files WHERE path = ?`, path)

	var f types.FileRecord
	var indexedAt, language sql.NullString
	var isTest int
	err := row.Scan(&f.ID, &f.Path, &f.ContentHash, &f.Mtime, &f.Size,
		&language, &indexedAt, &f.EmbeddingStatus, &f.ParseQuality, &isTest)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get file %s: %w", path, err)
	}
	f.Language = language.String
	f.IndexedAt = indexedAt.String
	f.IsTest = isTest != 0
	return &f, nil
}

// ListFiles returns all tracked file records.
func (s *Store) ListFiles(ctx context.Context) ([]types.FileRecord, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, path, content_hash, mtime, size, language, indexed_at,
		       embedding_status, parse_quality, is_test
		FROM files ORDER BY path`)
	if err != nil {
		return nil, fmt.Errorf("list files: %w", err)
	}
	defer rows.Close()

	var files []types.FileRecord
	for rows.Next() {
		var f types.FileRecord
		var language, indexedAt sql.NullString
		var isTest int
		if err := rows.Scan(&f.ID, &f.Path, &f.ContentHash, &f.Mtime, &f.Size,
			&language, &indexedAt, &f.EmbeddingStatus, &f.ParseQuality, &isTest); err != nil {
			return nil, fmt.Errorf("scan file row: %w", err)
		}
		f.Language = language.String
		f.IndexedAt = indexedAt.String
		f.IsTest = isTest != 0
		files = append(files, f)
	}
	return files, rows.Err()
}

// DeleteFile removes a file and cascades to its chunks and symbols.
func (s *Store) DeleteFile(ctx context.Context, fileID int64) error {
	return s.db.WithWriteTx(func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, "DELETE FROM files WHERE id = ?", fileID)
		if err != nil {
			return fmt.Errorf("delete file %d: %w", fileID, err)
		}
		return nil
	})
}

// DeleteFileByPath removes a file by path and cascades to chunks/symbols.
// Returns the file ID that was deleted, or 0 if not found.
func (s *Store) DeleteFileByPath(ctx context.Context, path string) (int64, error) {
	var fileID int64
	err := s.db.WithWriteTx(func(tx *sql.Tx) error {
		err := tx.QueryRowContext(ctx, "SELECT id FROM files WHERE path = ?", path).Scan(&fileID)
		if err != nil {
			return nil // not found → no-op
		}
		_, err = tx.ExecContext(ctx, "DELETE FROM files WHERE id = ?", fileID)
		return err
	})
	return fileID, err
}

// GetEmbeddedChunkIDsByFile returns IDs of chunks with embedded=1 for a file.
func (s *Store) GetEmbeddedChunkIDsByFile(ctx context.Context, fileID int64) ([]int64, error) {
	rows, err := s.db.QueryContext(ctx,
		"SELECT id FROM chunks WHERE file_id = ? AND embedded = 1", fileID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// UpdateChunkParents sets parent_chunk_id for the given chunk→parent mappings.
func (s *Store) UpdateChunkParents(ctx context.Context, updates map[int64]int64) error {
	if len(updates) == 0 {
		return nil
	}
	return s.db.WithWriteTx(func(tx *sql.Tx) error {
		stmt, err := tx.PrepareContext(ctx, "UPDATE chunks SET parent_chunk_id = ? WHERE id = ?")
		if err != nil {
			return err
		}
		defer stmt.Close()
		for chunkID, parentID := range updates {
			if _, err := stmt.ExecContext(ctx, parentID, chunkID); err != nil {
				return err
			}
		}
		return nil
	})
}

// ── Chunk operations ──

// InsertChunks bulk-inserts chunks for a file within a transaction.
// Returns the assigned chunk IDs in order.
func (s *Store) InsertChunks(ctx context.Context, fileID int64, chunks []types.ChunkRecord) ([]int64, error) {
	ids := make([]int64, len(chunks))
	err := s.db.WithWriteTx(func(tx *sql.Tx) error {
		stmt, err := tx.PrepareContext(ctx, `
			INSERT INTO chunks (file_id, parent_chunk_id, chunk_index, symbol_name, kind,
			                    start_line, end_line, content, token_count, signature, parse_quality)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
		if err != nil {
			return fmt.Errorf("prepare chunk insert: %w", err)
		}
		defer stmt.Close()

		for i, c := range chunks {
			res, err := stmt.ExecContext(ctx,
				fileID, c.ParentChunkID, c.ChunkIndex, c.SymbolName, c.Kind,
				c.StartLine, c.EndLine, c.Content, c.TokenCount, c.Signature,
				coalesce(c.ParseQuality, "full"))
			if err != nil {
				return fmt.Errorf("insert chunk %d: %w", i, err)
			}
			ids[i], _ = res.LastInsertId()
		}

		// Resolve ParentIndex → ParentChunkID (CA-10)
		for i, c := range chunks {
			if c.ParentIndex != nil && *c.ParentIndex < len(ids) {
				parentID := ids[*c.ParentIndex]
				if _, err := tx.ExecContext(ctx,
					"UPDATE chunks SET parent_chunk_id = ? WHERE id = ?",
					parentID, ids[i]); err != nil {
					return fmt.Errorf("set parent chunk %d: %w", i, err)
				}
			}
		}

		return nil
	})
	return ids, err
}

// GetChunksByFile returns all chunks for a file, ordered by chunk_index.
func (s *Store) GetChunksByFile(ctx context.Context, fileID int64) ([]types.ChunkRecord, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, file_id, parent_chunk_id, chunk_index, symbol_name, kind,
		       start_line, end_line, content, token_count, signature, parse_quality
		FROM chunks WHERE file_id = ? ORDER BY chunk_index`, fileID)
	if err != nil {
		return nil, fmt.Errorf("get chunks for file %d: %w", fileID, err)
	}
	defer rows.Close()
	return scanChunks(rows)
}

// GetChunkByID returns a single chunk by its ID.
func (s *Store) GetChunkByID(ctx context.Context, id int64) (*types.ChunkRecord, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, file_id, parent_chunk_id, chunk_index, symbol_name, kind,
		       start_line, end_line, content, token_count, signature, parse_quality
		FROM chunks WHERE id = ?`, id)

	var c types.ChunkRecord
	var parentID sql.NullInt64
	var symbolName, signature sql.NullString
	err := row.Scan(&c.ID, &c.FileID, &parentID, &c.ChunkIndex, &symbolName, &c.Kind,
		&c.StartLine, &c.EndLine, &c.Content, &c.TokenCount, &signature, &c.ParseQuality)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get chunk %d: %w", id, err)
	}
	if parentID.Valid {
		c.ParentChunkID = &parentID.Int64
	}
	c.SymbolName = symbolName.String
	c.Signature = signature.String
	return &c, nil
}

// GetSiblingChunks returns all chunks in the same file with the same
// symbol_name and kind, ordered by chunk_index. Used to reconstitute
// split method fragments at retrieval time.
func (s *Store) GetSiblingChunks(ctx context.Context, fileID int64, symbolName string, kind string) ([]types.ChunkRecord, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, file_id, parent_chunk_id, chunk_index, symbol_name, kind,
		       start_line, end_line, content, token_count, signature, parse_quality
		FROM chunks
		WHERE file_id = ? AND symbol_name = ? AND kind = ?
		ORDER BY chunk_index`, fileID, symbolName, kind)
	if err != nil {
		return nil, fmt.Errorf("get sibling chunks file=%d sym=%s kind=%s: %w", fileID, symbolName, kind, err)
	}
	defer rows.Close()
	return scanChunks(rows)
}

// DeleteChunksByFile removes all chunks for a file.
func (s *Store) DeleteChunksByFile(ctx context.Context, fileID int64) error {
	return s.db.WithWriteTx(func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, "DELETE FROM chunks WHERE file_id = ?", fileID)
		if err != nil {
			return fmt.Errorf("delete chunks for file %d: %w", fileID, err)
		}
		return nil
	})
}

// ── Symbol operations ──

// InsertSymbols bulk-inserts symbols for a file.
func (s *Store) InsertSymbols(ctx context.Context, fileID int64, symbols []types.SymbolRecord) ([]int64, error) {
	ids := make([]int64, len(symbols))
	err := s.db.WithWriteTx(func(tx *sql.Tx) error {
		stmt, err := tx.PrepareContext(ctx, `
			INSERT INTO symbols (chunk_id, file_id, name, qualified_name, kind,
			                     line, signature, visibility, is_exported)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`)
		if err != nil {
			return fmt.Errorf("prepare symbol insert: %w", err)
		}
		defer stmt.Close()

		for i, sym := range symbols {
			exported := 0
			if sym.IsExported {
				exported = 1
			}
			res, err := stmt.ExecContext(ctx,
				sym.ChunkID, fileID, sym.Name, sym.QualifiedName, sym.Kind,
				sym.Line, sym.Signature, sym.Visibility, exported)
			if err != nil {
				return fmt.Errorf("insert symbol %s: %w", sym.Name, err)
			}
			ids[i], _ = res.LastInsertId()
		}
		return nil
	})
	return ids, err
}

// GetSymbolsByFile returns all symbols for a file.
func (s *Store) GetSymbolsByFile(ctx context.Context, fileID int64) ([]types.SymbolRecord, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, chunk_id, file_id, name, qualified_name, kind,
		       line, signature, visibility, is_exported
		FROM symbols WHERE file_id = ? ORDER BY line`, fileID)
	if err != nil {
		return nil, fmt.Errorf("get symbols for file %d: %w", fileID, err)
	}
	defer rows.Close()
	return scanSymbols(rows)
}

// GetSymbolByName returns symbols matching the given name.
func (s *Store) GetSymbolByName(ctx context.Context, name string) ([]types.SymbolRecord, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, chunk_id, file_id, name, qualified_name, kind,
		       line, signature, visibility, is_exported
		FROM symbols WHERE name = ?`, name)
	if err != nil {
		return nil, fmt.Errorf("get symbols named %s: %w", name, err)
	}
	defer rows.Close()
	return scanSymbols(rows)
}

// GetSymbolByID returns a single symbol by its ID.
func (s *Store) GetSymbolByID(ctx context.Context, id int64) (*types.SymbolRecord, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, chunk_id, file_id, name, qualified_name, kind,
		       line, signature, visibility, is_exported
		FROM symbols WHERE id = ?`, id)

	var sym types.SymbolRecord
	var qualName, signature, visibility sql.NullString
	var exported int
	err := row.Scan(&sym.ID, &sym.ChunkID, &sym.FileID, &sym.Name, &qualName, &sym.Kind,
		&sym.Line, &signature, &visibility, &exported)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get symbol %d: %w", id, err)
	}
	sym.QualifiedName = qualName.String
	sym.Signature = signature.String
	sym.Visibility = visibility.String
	sym.IsExported = exported != 0
	return &sym, nil
}

// DeleteSymbolsByFile removes all symbols for a file.
func (s *Store) DeleteSymbolsByFile(ctx context.Context, fileID int64) error {
	return s.db.WithWriteTx(func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, "DELETE FROM symbols WHERE file_id = ?", fileID)
		if err != nil {
			return fmt.Errorf("delete symbols for file %d: %w", fileID, err)
		}
		return nil
	})
}

// ── File path lookup ──

// GetFilePathByID returns the project-relative path for a file ID.
func (s *Store) GetFilePathByID(ctx context.Context, fileID int64) (string, error) {
	var path string
	err := s.db.QueryRowContext(ctx, "SELECT path FROM files WHERE id = ?", fileID).Scan(&path)
	if err != nil {
		return "", fmt.Errorf("get path for file %d: %w", fileID, err)
	}
	return path, nil
}

// GetFileIsTestByID returns the is_test flag for a file ID.
func (s *Store) GetFileIsTestByID(ctx context.Context, fileID int64) (bool, error) {
	var isTest int
	err := s.db.QueryRowContext(ctx, "SELECT is_test FROM files WHERE id = ?", fileID).Scan(&isTest)
	if err != nil {
		return false, fmt.Errorf("get is_test for file %d: %w", fileID, err)
	}
	return isTest != 0, nil
}

// ── Stats ──

// GetIndexStats returns aggregate statistics about the index.
func (s *Store) GetIndexStats(ctx context.Context) (*types.IndexStats, error) {
	stats := &types.IndexStats{Languages: make(map[string]int)}

	if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM files").Scan(&stats.TotalFiles); err != nil {
		return nil, fmt.Errorf("count files: %w", err)
	}
	if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM chunks").Scan(&stats.TotalChunks); err != nil {
		return nil, fmt.Errorf("count chunks: %w", err)
	}
	if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM symbols").Scan(&stats.TotalSymbols); err != nil {
		return nil, fmt.Errorf("count symbols: %w", err)
	}
	if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM files WHERE parse_quality IN ('error', 'unparseable')").Scan(&stats.ParseErrors); err != nil {
		return nil, fmt.Errorf("count parse errors: %w", err)
	}

	rows, err := s.db.QueryContext(ctx, "SELECT language, COUNT(*) FROM files WHERE language != '' GROUP BY language")
	if err != nil {
		return nil, fmt.Errorf("count languages: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var lang string
		var count int
		if err := rows.Scan(&lang, &count); err != nil {
			return nil, fmt.Errorf("scan language row: %w", err)
		}
		stats.Languages[lang] = count
	}
	return stats, rows.Err()
}

// ── Embedding helpers ──

// VectorCounter can report how many vectors exist for given chunk IDs.
type VectorCounter interface {
	Count(ctx context.Context) (int, error)
}

// GetChunksNeedingEmbedding returns chunks whose parent file is not yet fully
// embedded (embedding_status != 'complete'). The caller should additionally
// filter by vector store contents for crash-recovery reconciliation.
func (s *Store) GetChunksNeedingEmbedding(ctx context.Context, vs VectorCounter) ([]EmbedJobRecord, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT c.id, c.content
		FROM chunks c
		JOIN files f ON c.file_id = f.id
		WHERE f.embedding_status != 'complete'
		ORDER BY c.id`)
	if err != nil {
		return nil, fmt.Errorf("query chunks for embedding: %w", err)
	}
	defer rows.Close()

	var jobs []EmbedJobRecord
	for rows.Next() {
		var j EmbedJobRecord
		if err := rows.Scan(&j.ChunkID, &j.Content); err != nil {
			return nil, fmt.Errorf("scan embed chunk: %w", err)
		}
		jobs = append(jobs, j)
	}
	return jobs, rows.Err()
}

// EmbedJobRecord carries chunk data needed for embedding.
type EmbedJobRecord = struct {
	ChunkID int64
	Content string
}

// GetEmbedPage returns up to limit chunks that need embedding (embedded = 0),
// with c.id > afterID. This is the cursor-based pagination method for RunFromDB.
func (s *Store) GetEmbedPage(ctx context.Context, afterID int64, limit int) ([]types.EmbedJob, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT c.id, c.content FROM chunks c WHERE c.embedded = 0 AND c.id > ? ORDER BY c.id LIMIT ?`,
		afterID, limit)
	if err != nil {
		return nil, fmt.Errorf("get embed page: %w", err)
	}
	defer rows.Close()

	var jobs []types.EmbedJob
	for rows.Next() {
		var j types.EmbedJob
		if err := rows.Scan(&j.ChunkID, &j.Content); err != nil {
			return nil, fmt.Errorf("scan embed page row: %w", err)
		}
		jobs = append(jobs, j)
	}
	return jobs, rows.Err()
}

// CountChunksNeedingEmbedding returns the number of chunks with embedded = 0.
func (s *Store) CountChunksNeedingEmbedding(ctx context.Context) (int, error) {
	var count int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM chunks WHERE embedded = 0`).Scan(&count); err != nil {
		return 0, fmt.Errorf("count chunks needing embedding: %w", err)
	}
	return count, nil
}

// CountChunksEmbedded returns the number of chunks with embedded = 1.
func (s *Store) CountChunksEmbedded(ctx context.Context) (int, error) {
	var count int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM chunks WHERE embedded = 1`).Scan(&count); err != nil {
		return 0, fmt.Errorf("count embedded chunks: %w", err)
	}
	return count, nil
}

// ResetAllEmbeddedFlags sets all chunks to embedded = 0 and all files to
// embedding_status = 'pending'. Used when the vector store is out of sync
// with the DB (e.g. embeddings.bin was deleted).
func (s *Store) ResetAllEmbeddedFlags(ctx context.Context) error {
	return s.db.WithWriteTx(func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx,
			`UPDATE chunks SET embedded = 0 WHERE embedded = 1`); err != nil {
			return fmt.Errorf("reset embedded flags: %w", err)
		}
		if _, err := tx.ExecContext(ctx,
			`UPDATE files SET embedding_status = 'pending' WHERE embedding_status != 'pending'`); err != nil {
			return fmt.Errorf("reset file embedding status: %w", err)
		}
		return nil
	})
}

// GetEmbeddedChunkIDs returns up to limit chunk IDs where embedded = 1 and
// id > afterID, ordered by id. Used for cursor-based reconciliation against
// the vector store.
func (s *Store) GetEmbeddedChunkIDs(ctx context.Context, afterID int64, limit int) ([]int64, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id FROM chunks WHERE embedded = 1 AND id > ? ORDER BY id LIMIT ?`,
		afterID, limit)
	if err != nil {
		return nil, fmt.Errorf("get embedded chunk IDs: %w", err)
	}
	defer rows.Close()

	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan embedded chunk ID: %w", err)
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// ResetEmbeddedFlags sets embedded = 0 for the given chunk IDs and recalculates
// file embedding_status for affected files. Used for targeted reconciliation
// when only some vectors are missing from the store.
func (s *Store) ResetEmbeddedFlags(ctx context.Context, chunkIDs []int64) error {
	if len(chunkIDs) == 0 {
		return nil
	}

	return s.db.WithWriteTx(func(tx *sql.Tx) error {
		const batchLimit = 500
		for start := 0; start < len(chunkIDs); start += batchLimit {
			end := start + batchLimit
			if end > len(chunkIDs) {
				end = len(chunkIDs)
			}
			batch := chunkIDs[start:end]

			placeholders := make([]string, len(batch))
			args := make([]any, len(batch))
			for i, id := range batch {
				placeholders[i] = "?"
				args[i] = id
			}
			ph := strings.Join(placeholders, ",")

			if _, err := tx.ExecContext(ctx,
				fmt.Sprintf("UPDATE chunks SET embedded = 0 WHERE id IN (%s)", ph),
				args...); err != nil {
				return fmt.Errorf("reset embedded flags: %w", err)
			}

			rows, err := tx.QueryContext(ctx,
				fmt.Sprintf("SELECT DISTINCT file_id FROM chunks WHERE id IN (%s)", ph),
				args...)
			if err != nil {
				return fmt.Errorf("get affected file IDs: %w", err)
			}
			var fileIDs []int64
			for rows.Next() {
				var fid int64
				if err := rows.Scan(&fid); err != nil {
					rows.Close()
					return fmt.Errorf("scan file ID: %w", err)
				}
				fileIDs = append(fileIDs, fid)
			}
			rows.Close()
			if err := rows.Err(); err != nil {
				return fmt.Errorf("iterate file IDs: %w", err)
			}

			for _, fid := range fileIDs {
				var embeddedCount int
				if err := tx.QueryRowContext(ctx,
					"SELECT COUNT(*) FROM chunks WHERE file_id = ? AND embedded = 1", fid,
				).Scan(&embeddedCount); err != nil {
					return fmt.Errorf("count embedded for file %d: %w", fid, err)
				}
				status := "pending"
				if embeddedCount > 0 {
					status = "partial"
				}
				if _, err := tx.ExecContext(ctx,
					"UPDATE files SET embedding_status = ? WHERE id = ?",
					status, fid); err != nil {
					return fmt.Errorf("update file %d embedding status: %w", fid, err)
				}
			}
		}
		return nil
	})
}

// MarkChunksEmbedded marks individual chunks as embedded and updates the parent
// file's embedding_status based on cumulative progress. Files where all chunks
// are now embedded get status 'complete'; files with some embedded get 'partial'.
func (s *Store) MarkChunksEmbedded(ctx context.Context, chunkIDs []int64) error {
	if len(chunkIDs) == 0 {
		return nil
	}

	return s.db.WithWriteTx(func(tx *sql.Tx) error {
		// Process in batches of 500 to stay under SQLite's variable limit.
		const batchLimit = 500
		for start := 0; start < len(chunkIDs); start += batchLimit {
			end := start + batchLimit
			if end > len(chunkIDs) {
				end = len(chunkIDs)
			}
			batch := chunkIDs[start:end]

			placeholders := make([]string, len(batch))
			args := make([]any, len(batch))
			for i, id := range batch {
				placeholders[i] = "?"
				args[i] = id
			}
			ph := strings.Join(placeholders, ",")

			// Step 1: Mark individual chunks as embedded.
			if _, err := tx.ExecContext(ctx,
				fmt.Sprintf("UPDATE chunks SET embedded = 1 WHERE id IN (%s)", ph),
				args...); err != nil {
				return fmt.Errorf("mark chunks embedded: %w", err)
			}

			// Step 2: Get affected file IDs.
			rows, err := tx.QueryContext(ctx,
				fmt.Sprintf("SELECT DISTINCT file_id FROM chunks WHERE id IN (%s)", ph),
				args...)
			if err != nil {
				return fmt.Errorf("get affected file IDs: %w", err)
			}
			var fileIDs []int64
			for rows.Next() {
				var fid int64
				if err := rows.Scan(&fid); err != nil {
					rows.Close()
					return fmt.Errorf("scan file ID: %w", err)
				}
				fileIDs = append(fileIDs, fid)
			}
			rows.Close()
			if err := rows.Err(); err != nil {
				return fmt.Errorf("iterate file IDs: %w", err)
			}

			// Step 3: Update file embedding status based on cumulative progress.
			for _, fid := range fileIDs {
				var remaining int
				if err := tx.QueryRowContext(ctx,
					"SELECT COUNT(*) FROM chunks WHERE file_id = ? AND embedded = 0",
					fid).Scan(&remaining); err != nil {
					return fmt.Errorf("count remaining for file %d: %w", fid, err)
				}
				if remaining == 0 {
					if _, err := tx.ExecContext(ctx,
						"UPDATE files SET embedding_status = 'complete' WHERE id = ?", fid); err != nil {
						return fmt.Errorf("mark file complete: %w", err)
					}
				} else {
					if _, err := tx.ExecContext(ctx,
						"UPDATE files SET embedding_status = 'partial' WHERE id = ? AND embedding_status != 'complete'",
						fid); err != nil {
						return fmt.Errorf("mark file partial: %w", err)
					}
				}
			}
		}
		return nil
	})
}

// EmbeddingReadiness returns the fraction of chunks with embeddings [0.0, 1.0].
func (s *Store) EmbeddingReadiness(ctx context.Context, vectorCount int) (float64, error) {
	var totalChunks int
	if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM chunks").Scan(&totalChunks); err != nil {
		return 0, fmt.Errorf("count chunks: %w", err)
	}
	if totalChunks == 0 {
		return 0, nil
	}
	return float64(vectorCount) / float64(totalChunks), nil
}

// ── Helpers ──

func scanChunks(rows *sql.Rows) ([]types.ChunkRecord, error) {
	var chunks []types.ChunkRecord
	for rows.Next() {
		var c types.ChunkRecord
		var parentID sql.NullInt64
		var symbolName, signature sql.NullString
		if err := rows.Scan(&c.ID, &c.FileID, &parentID, &c.ChunkIndex, &symbolName, &c.Kind,
			&c.StartLine, &c.EndLine, &c.Content, &c.TokenCount, &signature, &c.ParseQuality); err != nil {
			return nil, fmt.Errorf("scan chunk row: %w", err)
		}
		if parentID.Valid {
			c.ParentChunkID = &parentID.Int64
		}
		c.SymbolName = symbolName.String
		c.Signature = signature.String
		chunks = append(chunks, c)
	}
	return chunks, rows.Err()
}

func scanSymbols(rows *sql.Rows) ([]types.SymbolRecord, error) {
	var symbols []types.SymbolRecord
	for rows.Next() {
		var sym types.SymbolRecord
		var qualName, signature, visibility sql.NullString
		var exported int
		if err := rows.Scan(&sym.ID, &sym.ChunkID, &sym.FileID, &sym.Name, &qualName, &sym.Kind,
			&sym.Line, &signature, &visibility, &exported); err != nil {
			return nil, fmt.Errorf("scan symbol row: %w", err)
		}
		sym.QualifiedName = qualName.String
		sym.Signature = signature.String
		sym.Visibility = visibility.String
		sym.IsExported = exported != 0
		symbols = append(symbols, sym)
	}
	return symbols, rows.Err()
}

func coalesce(val, fallback string) string {
	if val == "" {
		return fallback
	}
	return val
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// batchInClause builds "?,?,?" placeholders and []any args for an IN clause.
// Callers must chunk inputs to stay under SQLite's variable limit (999).
const batchLimit = 500

func inClauseInt64(ids []int64) (string, []any) {
	ph := make([]string, len(ids))
	args := make([]any, len(ids))
	for i, id := range ids {
		ph[i] = "?"
		args[i] = id
	}
	return strings.Join(ph, ","), args
}

func inClauseStrings(vals []string) (string, []any) {
	ph := make([]string, len(vals))
	args := make([]any, len(vals))
	for i, v := range vals {
		ph[i] = "?"
		args[i] = v
	}
	return strings.Join(ph, ","), args
}

// ---------------------------------------------------------------------------
// Batch query methods (BatchMetadataStore interface)
// ---------------------------------------------------------------------------

// BatchGetSymbolIDsForChunks returns chunkID → symbolID for each chunk.
// Replicates lookupSymbolForChunk: prefers a symbol in the same file as the chunk,
// falls back to the first symbol with that name.
func (s *Store) BatchGetSymbolIDsForChunks(ctx context.Context, chunkIDs []int64) (map[int64]int64, error) {
	result := make(map[int64]int64, len(chunkIDs))
	for start := 0; start < len(chunkIDs); start += batchLimit {
		end := start + batchLimit
		if end > len(chunkIDs) {
			end = len(chunkIDs)
		}
		batch := chunkIDs[start:end]
		ph, args := inClauseInt64(batch)

		rows, err := s.db.QueryContext(ctx, fmt.Sprintf(`
			SELECT c.id, c.file_id, c.symbol_name, s.id AS sym_id, s.file_id AS sym_file_id
			FROM chunks c
			JOIN symbols s ON s.name = c.symbol_name
			WHERE c.id IN (%s) AND c.symbol_name != ''
			ORDER BY c.id, CASE WHEN s.file_id = c.file_id THEN 0 ELSE 1 END, s.id
		`, ph), args...)
		if err != nil {
			return nil, fmt.Errorf("batch get symbol IDs: %w", err)
		}

		for rows.Next() {
			var chunkID, fileID int64
			var symbolName string
			var symID, symFileID int64
			if err := rows.Scan(&chunkID, &fileID, &symbolName, &symID, &symFileID); err != nil {
				rows.Close()
				return nil, fmt.Errorf("scan symbol ID: %w", err)
			}
			// ORDER BY ensures same-file match comes first; keep only the first per chunk.
			if _, exists := result[chunkID]; !exists {
				result[chunkID] = symID
			}
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, err
		}
	}
	return result, nil
}

// BatchNeighbors returns symbolID → []neighborSymbolID for each seed.
// Pragmatic first pass: delegates to existing Neighbors() per symbol.
func (s *Store) BatchNeighbors(ctx context.Context, symbolIDs []int64, maxDepth int) (map[int64][]int64, error) {
	result := make(map[int64][]int64, len(symbolIDs))
	for _, symID := range symbolIDs {
		neighbors, err := s.Neighbors(ctx, symID, maxDepth, "both")
		if err != nil {
			continue
		}
		result[symID] = neighbors
	}
	return result, nil
}

// BatchGetChunkIDsForSymbols returns symbolID → chunkID mapping.
func (s *Store) BatchGetChunkIDsForSymbols(ctx context.Context, symbolIDs []int64) (map[int64]int64, error) {
	result := make(map[int64]int64, len(symbolIDs))
	for start := 0; start < len(symbolIDs); start += batchLimit {
		end := start + batchLimit
		if end > len(symbolIDs) {
			end = len(symbolIDs)
		}
		batch := symbolIDs[start:end]
		ph, args := inClauseInt64(batch)

		rows, err := s.db.QueryContext(ctx, fmt.Sprintf(
			"SELECT id, chunk_id FROM symbols WHERE id IN (%s)", ph), args...)
		if err != nil {
			return nil, fmt.Errorf("batch get chunk IDs: %w", err)
		}
		for rows.Next() {
			var symID, chunkID int64
			if err := rows.Scan(&symID, &chunkID); err != nil {
				rows.Close()
				return nil, fmt.Errorf("scan chunk ID: %w", err)
			}
			result[symID] = chunkID
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, err
		}
	}
	return result, nil
}

// BatchHydrateChunks returns chunk data joined with file path and is_test.
// Eliminates per-result GetChunkByID + GetFilePathByID + GetFileIsTestByID queries.
func (s *Store) BatchHydrateChunks(ctx context.Context, chunkIDs []int64) ([]types.HydratedChunk, error) {
	if len(chunkIDs) == 0 {
		return nil, nil
	}
	var results []types.HydratedChunk
	for start := 0; start < len(chunkIDs); start += batchLimit {
		end := start + batchLimit
		if end > len(chunkIDs) {
			end = len(chunkIDs)
		}
		batch := chunkIDs[start:end]
		ph, args := inClauseInt64(batch)

		rows, err := s.db.QueryContext(ctx, fmt.Sprintf(`
			SELECT c.id, c.file_id, c.symbol_name, c.kind,
			       c.start_line, c.end_line, c.content, c.token_count,
			       f.path, f.is_test
			FROM chunks c
			JOIN files f ON c.file_id = f.id
			WHERE c.id IN (%s)
		`, ph), args...)
		if err != nil {
			return nil, fmt.Errorf("batch hydrate chunks: %w", err)
		}
		for rows.Next() {
			var h types.HydratedChunk
			var isTest int
			if err := rows.Scan(
				&h.ChunkID, &h.FileID, &h.SymbolName, &h.Kind,
				&h.StartLine, &h.EndLine, &h.Content, &h.TokenCount,
				&h.Path, &isTest,
			); err != nil {
				rows.Close()
				return nil, fmt.Errorf("scan hydrated chunk: %w", err)
			}
			h.IsTest = isTest != 0
			results = append(results, h)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, err
		}
	}
	return results, nil
}

// BatchGetSiblingChunks returns all sibling chunks for the given keys.
// Each key is (fileID, symbolName, kind). Results are keyed by SiblingKey.String().
func (s *Store) BatchGetSiblingChunks(ctx context.Context, keys []types.SiblingKey) (map[string][]types.HydratedChunk, error) {
	result := make(map[string][]types.HydratedChunk, len(keys))
	for _, k := range keys {
		rows, err := s.db.QueryContext(ctx, `
			SELECT c.id, c.file_id, c.symbol_name, c.kind,
			       c.start_line, c.end_line, c.content, c.token_count,
			       f.path, f.is_test
			FROM chunks c
			JOIN files f ON c.file_id = f.id
			WHERE c.file_id = ? AND c.symbol_name = ? AND c.kind = ?
			ORDER BY c.chunk_index`, k.FileID, k.SymbolName, k.Kind)
		if err != nil {
			return nil, fmt.Errorf("batch sibling chunks: %w", err)
		}
		var chunks []types.HydratedChunk
		for rows.Next() {
			var h types.HydratedChunk
			var isTest int
			if err := rows.Scan(
				&h.ChunkID, &h.FileID, &h.SymbolName, &h.Kind,
				&h.StartLine, &h.EndLine, &h.Content, &h.TokenCount,
				&h.Path, &isTest,
			); err != nil {
				rows.Close()
				return nil, fmt.Errorf("scan sibling chunk: %w", err)
			}
			h.IsTest = isTest != 0
			chunks = append(chunks, h)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, err
		}
		if len(chunks) > 1 {
			result[k.String()] = chunks
		}
	}
	return result, nil
}

// BatchGetFileHashes returns path → contentHash for existing files.
func (s *Store) BatchGetFileHashes(ctx context.Context, paths []string) (map[string]string, error) {
	result := make(map[string]string, len(paths))
	for start := 0; start < len(paths); start += batchLimit {
		end := start + batchLimit
		if end > len(paths) {
			end = len(paths)
		}
		batch := paths[start:end]
		ph, args := inClauseStrings(batch)

		rows, err := s.db.QueryContext(ctx, fmt.Sprintf(
			"SELECT path, content_hash FROM files WHERE path IN (%s)", ph), args...)
		if err != nil {
			return nil, fmt.Errorf("batch get file hashes: %w", err)
		}
		for rows.Next() {
			var path, hash string
			if err := rows.Scan(&path, &hash); err != nil {
				rows.Close()
				return nil, fmt.Errorf("scan file hash: %w", err)
			}
			result[path] = hash
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, err
		}
	}
	return result, nil
}

// ── Metrics ──

// RecordToolCalls batch-inserts MCP tool call metrics.
func (s *Store) RecordToolCalls(ctx context.Context, records []types.ToolCallRecord) error {
	return s.db.WithWriteTx(func(tx *sql.Tx) error {
		stmt, err := tx.PrepareContext(ctx, `
			INSERT INTO tool_calls (session_id, timestamp, tool_name, args_json,
				args_bytes, response_bytes, response_tokens_est, result_count,
				duration_ms, is_error)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
		if err != nil {
			return fmt.Errorf("prepare tool_calls insert: %w", err)
		}
		defer stmt.Close()

		for _, rec := range records {
			isErr := 0
			if rec.IsError {
				isErr = 1
			}
			ts := rec.Timestamp.UTC().Format("2006-01-02T15:04:05.000Z")
			if _, err := stmt.ExecContext(ctx,
				rec.SessionID, ts, rec.ToolName, rec.ArgsJSON,
				rec.ArgsBytes, rec.ResponseBytes, rec.ResponseTokensEst,
				rec.ResultCount, rec.DurationMs, isErr,
			); err != nil {
				return fmt.Errorf("insert tool call %s: %w", rec.ToolName, err)
			}
		}
		return nil
	})
}

// ── Config key-value ──

// GetConfig returns the value for a config key. Returns empty string and nil
// error if the key is absent.
func (s *Store) GetConfig(ctx context.Context, key string) (string, error) {
	var value string
	err := s.db.QueryRowContext(ctx, "SELECT value FROM config WHERE key = ?", key).Scan(&value)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("get config %q: %w", key, err)
	}
	return value, nil
}

// SetConfig writes or overwrites a config key/value pair.
func (s *Store) SetConfig(ctx context.Context, key, value string) error {
	return s.db.WithWriteTx(func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `
			INSERT INTO config (key, value) VALUES (?, ?)
			ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, value)
		if err != nil {
			return fmt.Errorf("set config %q: %w", key, err)
		}
		return nil
	})
}
