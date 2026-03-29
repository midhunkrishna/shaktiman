package storage

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/shaktimanai/shaktiman/internal/types"
)

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

// ── File operations ──

// UpsertFile inserts or updates a file record. Returns the file ID.
func (s *Store) UpsertFile(ctx context.Context, file *types.FileRecord) (int64, error) {
	now := time.Now().UTC().Format(time.RFC3339Nano)

	var id int64
	err := s.db.WithWriteTx(func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `
			INSERT INTO files (path, content_hash, mtime, size, language, indexed_at, embedding_status, parse_quality)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(path) DO UPDATE SET
				content_hash = excluded.content_hash,
				mtime = excluded.mtime,
				size = excluded.size,
				language = excluded.language,
				indexed_at = excluded.indexed_at,
				embedding_status = excluded.embedding_status,
				parse_quality = excluded.parse_quality`,
			file.Path, file.ContentHash, file.Mtime, file.Size,
			file.Language, now,
			coalesce(file.EmbeddingStatus, "pending"),
			coalesce(file.ParseQuality, "full"),
		)
		if err != nil {
			return fmt.Errorf("upsert file %s: %w", file.Path, err)
		}

		id, err = res.LastInsertId()
		if err != nil {
			return fmt.Errorf("last insert id: %w", err)
		}
		// ON CONFLICT UPDATE doesn't set LastInsertId — query it
		if id == 0 {
			err = tx.QueryRowContext(ctx, "SELECT id FROM files WHERE path = ?", file.Path).Scan(&id)
			if err != nil {
				return fmt.Errorf("get file id for %s: %w", file.Path, err)
			}
		}
		return nil
	})
	return id, err
}

// GetFileByPath returns a file record by its project-relative path.
func (s *Store) GetFileByPath(ctx context.Context, path string) (*types.FileRecord, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, path, content_hash, mtime, size, language, indexed_at,
		       embedding_status, parse_quality
		FROM files WHERE path = ?`, path)

	var f types.FileRecord
	var indexedAt, language sql.NullString
	err := row.Scan(&f.ID, &f.Path, &f.ContentHash, &f.Mtime, &f.Size,
		&language, &indexedAt, &f.EmbeddingStatus, &f.ParseQuality)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get file %s: %w", path, err)
	}
	f.Language = language.String
	f.IndexedAt = indexedAt.String
	return &f, nil
}

// ListFiles returns all tracked file records.
func (s *Store) ListFiles(ctx context.Context) ([]types.FileRecord, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, path, content_hash, mtime, size, language, indexed_at,
		       embedding_status, parse_quality
		FROM files ORDER BY path`)
	if err != nil {
		return nil, fmt.Errorf("list files: %w", err)
	}
	defer rows.Close()

	var files []types.FileRecord
	for rows.Next() {
		var f types.FileRecord
		var language, indexedAt sql.NullString
		if err := rows.Scan(&f.ID, &f.Path, &f.ContentHash, &f.Mtime, &f.Size,
			&language, &indexedAt, &f.EmbeddingStatus, &f.ParseQuality); err != nil {
			return nil, fmt.Errorf("scan file row: %w", err)
		}
		f.Language = language.String
		f.IndexedAt = indexedAt.String
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
