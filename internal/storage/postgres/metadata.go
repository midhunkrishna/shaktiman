package postgres

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/shaktimanai/shaktiman/internal/types"
)

// ── File operations ──

func (s *PgStore) UpsertFile(ctx context.Context, file *types.FileRecord) (int64, error) {
	now := time.Now().UTC()
	embStatus := file.EmbeddingStatus
	if embStatus == "" {
		embStatus = "pending"
	}
	pq := file.ParseQuality
	if pq == "" {
		pq = "full"
	}

	var id int64
	err := s.queryRow(ctx, `
		INSERT INTO files (path, content_hash, mtime, size, language, indexed_at, embedding_status, parse_quality, is_test, project_id)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		ON CONFLICT(project_id, path) DO UPDATE SET
			content_hash = EXCLUDED.content_hash,
			mtime = EXCLUDED.mtime,
			size = EXCLUDED.size,
			language = EXCLUDED.language,
			indexed_at = EXCLUDED.indexed_at,
			embedding_status = EXCLUDED.embedding_status,
			parse_quality = EXCLUDED.parse_quality,
			is_test = EXCLUDED.is_test,
			project_id = EXCLUDED.project_id
		RETURNING id`,
		file.Path, file.ContentHash, file.Mtime, file.Size,
		file.Language, now, embStatus, pq, file.IsTest, s.projectID,
	).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("upsert file %s: %w", file.Path, err)
	}
	return id, nil
}

func (s *PgStore) GetFileByPath(ctx context.Context, path string) (*types.FileRecord, error) {
	var f types.FileRecord
	var indexedAt *time.Time
	err := s.queryRow(ctx, `
		SELECT id, path, content_hash, mtime, size, language, indexed_at,
		       embedding_status, parse_quality, is_test
		FROM files WHERE path = $1 AND project_id = $2`, path, s.projectID,
	).Scan(&f.ID, &f.Path, &f.ContentHash, &f.Mtime, &f.Size,
		&f.Language, &indexedAt, &f.EmbeddingStatus, &f.ParseQuality, &f.IsTest)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get file %s: %w", path, err)
	}
	if indexedAt != nil {
		f.IndexedAt = indexedAt.Format(time.RFC3339Nano)
	}
	return &f, nil
}

func (s *PgStore) ListFiles(ctx context.Context) ([]types.FileRecord, error) {
	rows, err := s.query(ctx, `
		SELECT id, path, content_hash, mtime, size, language, indexed_at,
		       embedding_status, parse_quality, is_test
		FROM files WHERE project_id = $1 ORDER BY path`, s.projectID)
	if err != nil {
		return nil, fmt.Errorf("list files: %w", err)
	}
	defer rows.Close()

	var files []types.FileRecord
	for rows.Next() {
		var f types.FileRecord
		var indexedAt *time.Time
		if err := rows.Scan(&f.ID, &f.Path, &f.ContentHash, &f.Mtime, &f.Size,
			&f.Language, &indexedAt, &f.EmbeddingStatus, &f.ParseQuality, &f.IsTest); err != nil {
			return nil, fmt.Errorf("scan file: %w", err)
		}
		if indexedAt != nil {
			f.IndexedAt = indexedAt.Format(time.RFC3339Nano)
		}
		files = append(files, f)
	}
	return files, rows.Err()
}

func (s *PgStore) DeleteFile(ctx context.Context, fileID int64) error {
	return s.WithWriteTx(ctx, func(txh types.TxHandle) error {
		tx := txh.(PgTxHandle).Tx
		_, err := tx.Exec(ctx, "DELETE FROM files WHERE id = $1", fileID)
		return err
	})
}

func (s *PgStore) DeleteFileByPath(ctx context.Context, path string) (int64, error) {
	var fileID int64
	err := s.WithWriteTx(ctx, func(txh types.TxHandle) error {
		tx := txh.(PgTxHandle).Tx
		lookupErr := tx.QueryRow(ctx,
			"SELECT id FROM files WHERE path = $1 AND project_id = $2",
			path, s.projectID).Scan(&fileID)
		if errors.Is(lookupErr, pgx.ErrNoRows) {
			fileID = 0
			return nil // genuinely not found → no-op
		}
		if lookupErr != nil {
			return fmt.Errorf("lookup file %s: %w", path, lookupErr)
		}
		if _, err := tx.Exec(ctx, "DELETE FROM files WHERE id = $1", fileID); err != nil {
			return fmt.Errorf("delete file %d: %w", fileID, err)
		}
		return nil
	})
	return fileID, err
}

func (s *PgStore) GetEmbeddedChunkIDsByFile(ctx context.Context, fileID int64) ([]int64, error) {
	rows, err := s.query(ctx, `
		SELECT c.id FROM chunks c
		JOIN files f ON c.file_id = f.id
		WHERE c.file_id = $1 AND f.project_id = $2 AND c.embedded = 1`, fileID, s.projectID)
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

func (s *PgStore) UpdateChunkParents(ctx context.Context, updates map[int64]int64) error {
	if len(updates) == 0 {
		return nil
	}
	chunkIDs := make([]int64, 0, len(updates))
	parentIDs := make([]int64, 0, len(updates))
	for chunkID, parentID := range updates {
		chunkIDs = append(chunkIDs, chunkID)
		parentIDs = append(parentIDs, parentID)
	}
	return s.WithWriteTx(ctx, func(txh types.TxHandle) error {
		tx := txh.(PgTxHandle).Tx
		// Single batched UPDATE via UNNEST: one round-trip, one tx.
		_, err := tx.Exec(ctx, `
			UPDATE chunks AS c
			SET parent_chunk_id = v.parent_id
			FROM (SELECT unnest($1::bigint[]) AS chunk_id,
			             unnest($2::bigint[]) AS parent_id) AS v
			WHERE c.id = v.chunk_id`, chunkIDs, parentIDs)
		if err != nil {
			return fmt.Errorf("update chunk parents (%d rows): %w", len(updates), err)
		}
		return nil
	})
}

// ── Chunk operations ──

func (s *PgStore) InsertChunks(ctx context.Context, fileID int64, chunks []types.ChunkRecord) ([]int64, error) {
	ids := make([]int64, len(chunks))
	err := s.WithWriteTx(ctx, func(txh types.TxHandle) error {
		tx := txh.(PgTxHandle).Tx
		for i, c := range chunks {
			pq := c.ParseQuality
			if pq == "" {
				pq = "full"
			}
			err := tx.QueryRow(ctx, `
				INSERT INTO chunks (file_id, parent_chunk_id, chunk_index, symbol_name, kind,
				                    start_line, end_line, content, token_count, signature, parse_quality)
				VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
				RETURNING id`,
				fileID, c.ParentChunkID, c.ChunkIndex, c.SymbolName, c.Kind,
				c.StartLine, c.EndLine, c.Content, c.TokenCount, c.Signature, pq,
			).Scan(&ids[i])
			if err != nil {
				return fmt.Errorf("insert chunk %d: %w", i, err)
			}
		}

		// Resolve ParentIndex → ParentChunkID
		for i, c := range chunks {
			if c.ParentIndex != nil && *c.ParentIndex < len(ids) {
				parentID := ids[*c.ParentIndex]
				if _, err := tx.Exec(ctx,
					"UPDATE chunks SET parent_chunk_id = $1 WHERE id = $2",
					parentID, ids[i]); err != nil {
					return fmt.Errorf("set parent chunk %d: %w", i, err)
				}
			}
		}
		return nil
	})
	return ids, err
}

func (s *PgStore) GetChunksByFile(ctx context.Context, fileID int64) ([]types.ChunkRecord, error) {
	rows, err := s.query(ctx, `
		SELECT c.id, c.file_id, c.parent_chunk_id, c.chunk_index, c.symbol_name, c.kind,
		       c.start_line, c.end_line, c.content, c.token_count, c.signature, c.parse_quality
		FROM chunks c
		JOIN files f ON c.file_id = f.id
		WHERE c.file_id = $1 AND f.project_id = $2
		ORDER BY c.chunk_index`, fileID, s.projectID)
	if err != nil {
		return nil, fmt.Errorf("get chunks: %w", err)
	}
	defer rows.Close()
	return scanChunks(rows)
}

func (s *PgStore) GetChunkByID(ctx context.Context, id int64) (*types.ChunkRecord, error) {
	var c types.ChunkRecord
	var parentID *int64
	err := s.queryRow(ctx, `
		SELECT c.id, c.file_id, c.parent_chunk_id, c.chunk_index, c.symbol_name, c.kind,
		       c.start_line, c.end_line, c.content, c.token_count, c.signature, c.parse_quality
		FROM chunks c
		JOIN files f ON c.file_id = f.id
		WHERE c.id = $1 AND f.project_id = $2`, id, s.projectID,
	).Scan(&c.ID, &c.FileID, &parentID, &c.ChunkIndex, &c.SymbolName, &c.Kind,
		&c.StartLine, &c.EndLine, &c.Content, &c.TokenCount, &c.Signature, &c.ParseQuality)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get chunk %d: %w", id, err)
	}
	c.ParentChunkID = parentID
	return &c, nil
}

func (s *PgStore) GetSiblingChunks(ctx context.Context, fileID int64, symbolName string, kind string) ([]types.ChunkRecord, error) {
	rows, err := s.query(ctx, `
		SELECT c.id, c.file_id, c.parent_chunk_id, c.chunk_index, c.symbol_name, c.kind,
		       c.start_line, c.end_line, c.content, c.token_count, c.signature, c.parse_quality
		FROM chunks c
		JOIN files f ON c.file_id = f.id
		WHERE c.file_id = $1 AND c.symbol_name = $2 AND c.kind = $3 AND f.project_id = $4
		ORDER BY c.chunk_index`, fileID, symbolName, kind, s.projectID)
	if err != nil {
		return nil, fmt.Errorf("get sibling chunks: %w", err)
	}
	defer rows.Close()
	return scanChunks(rows)
}

func (s *PgStore) DeleteChunksByFile(ctx context.Context, fileID int64) error {
	return s.WithWriteTx(ctx, func(txh types.TxHandle) error {
		tx := txh.(PgTxHandle).Tx
		_, err := tx.Exec(ctx, "DELETE FROM chunks WHERE file_id = $1", fileID)
		return err
	})
}

// ── Symbol operations ──

func (s *PgStore) InsertSymbols(ctx context.Context, fileID int64, symbols []types.SymbolRecord) ([]int64, error) {
	ids := make([]int64, len(symbols))
	err := s.WithWriteTx(ctx, func(txh types.TxHandle) error {
		tx := txh.(PgTxHandle).Tx
		for i, sym := range symbols {
			err := tx.QueryRow(ctx, `
				INSERT INTO symbols (chunk_id, file_id, name, qualified_name, kind,
				                     line, signature, visibility, is_exported)
				VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
				RETURNING id`,
				sym.ChunkID, fileID, sym.Name, sym.QualifiedName, sym.Kind,
				sym.Line, sym.Signature, sym.Visibility, sym.IsExported,
			).Scan(&ids[i])
			if err != nil {
				return fmt.Errorf("insert symbol %s: %w", sym.Name, err)
			}
		}
		return nil
	})
	return ids, err
}

func (s *PgStore) GetSymbolsByFile(ctx context.Context, fileID int64) ([]types.SymbolRecord, error) {
	rows, err := s.query(ctx, `
		SELECT s.id, s.chunk_id, s.file_id, s.name, s.qualified_name, s.kind,
		       s.line, s.signature, s.visibility, s.is_exported
		FROM symbols s
		JOIN files f ON s.file_id = f.id
		WHERE s.file_id = $1 AND f.project_id = $2
		ORDER BY s.line`, fileID, s.projectID)
	if err != nil {
		return nil, fmt.Errorf("get symbols: %w", err)
	}
	defer rows.Close()
	return scanSymbols(rows)
}

func (s *PgStore) GetSymbolByName(ctx context.Context, name string) ([]types.SymbolRecord, error) {
	rows, err := s.query(ctx, `
		SELECT s.id, s.chunk_id, s.file_id, s.name, s.qualified_name, s.kind,
		       s.line, s.signature, s.visibility, s.is_exported
		FROM symbols s
		JOIN files f ON s.file_id = f.id
		WHERE s.name = $1 AND f.project_id = $2`, name, s.projectID)
	if err != nil {
		return nil, fmt.Errorf("get symbols named %s: %w", name, err)
	}
	defer rows.Close()
	return scanSymbols(rows)
}

func (s *PgStore) GetSymbolByID(ctx context.Context, id int64) (*types.SymbolRecord, error) {
	var sym types.SymbolRecord
	err := s.queryRow(ctx, `
		SELECT s.id, s.chunk_id, s.file_id, s.name, s.qualified_name, s.kind,
		       s.line, s.signature, s.visibility, s.is_exported
		FROM symbols s
		JOIN files f ON s.file_id = f.id
		WHERE s.id = $1 AND f.project_id = $2`, id, s.projectID,
	).Scan(&sym.ID, &sym.ChunkID, &sym.FileID, &sym.Name, &sym.QualifiedName, &sym.Kind,
		&sym.Line, &sym.Signature, &sym.Visibility, &sym.IsExported)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get symbol %d: %w", id, err)
	}
	return &sym, nil
}

func (s *PgStore) DeleteSymbolsByFile(ctx context.Context, fileID int64) error {
	return s.WithWriteTx(ctx, func(txh types.TxHandle) error {
		tx := txh.(PgTxHandle).Tx
		_, err := tx.Exec(ctx, "DELETE FROM symbols WHERE file_id = $1", fileID)
		return err
	})
}

// ── Lookups ──

func (s *PgStore) GetFilePathByID(ctx context.Context, fileID int64) (string, error) {
	var path string
	err := s.queryRow(ctx, "SELECT path FROM files WHERE id = $1 AND project_id = $2", fileID, s.projectID).Scan(&path)
	if err != nil {
		return "", fmt.Errorf("get path for file %d: %w", fileID, err)
	}
	return path, nil
}

func (s *PgStore) GetFileIsTestByID(ctx context.Context, fileID int64) (bool, error) {
	var isTest bool
	err := s.queryRow(ctx, "SELECT is_test FROM files WHERE id = $1 AND project_id = $2", fileID, s.projectID).Scan(&isTest)
	if err != nil {
		return false, fmt.Errorf("get is_test for file %d: %w", fileID, err)
	}
	return isTest, nil
}

// ── Stats ──

func (s *PgStore) GetIndexStats(ctx context.Context) (*types.IndexStats, error) {
	stats := &types.IndexStats{Languages: make(map[string]int)}

	if err := s.queryRow(ctx, "SELECT COUNT(*) FROM files WHERE project_id = $1", s.projectID).Scan(&stats.TotalFiles); err != nil {
		return nil, fmt.Errorf("count files: %w", err)
	}
	if err := s.queryRow(ctx, "SELECT COUNT(*) FROM chunks WHERE file_id IN (SELECT id FROM files WHERE project_id = $1)", s.projectID).Scan(&stats.TotalChunks); err != nil {
		return nil, fmt.Errorf("count chunks: %w", err)
	}
	if err := s.queryRow(ctx, "SELECT COUNT(*) FROM symbols WHERE file_id IN (SELECT id FROM files WHERE project_id = $1)", s.projectID).Scan(&stats.TotalSymbols); err != nil {
		return nil, fmt.Errorf("count symbols: %w", err)
	}
	if err := s.queryRow(ctx, "SELECT COUNT(*) FROM files WHERE project_id = $1 AND parse_quality IN ('error', 'unparseable')", s.projectID).Scan(&stats.ParseErrors); err != nil {
		return nil, fmt.Errorf("count parse errors: %w", err)
	}

	rows, err := s.query(ctx, "SELECT language, COUNT(*) FROM files WHERE project_id = $1 AND language != '' GROUP BY language", s.projectID)
	if err != nil {
		return nil, fmt.Errorf("list languages: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var lang string
		var count int
		if err := rows.Scan(&lang, &count); err != nil {
			return nil, fmt.Errorf("scan language stat: %w", err)
		}
		stats.Languages[lang] = count
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate language stats: %w", err)
	}
	return stats, nil
}

// ── Full-text search (tsvector with 'simple' dictionary) ──

func (s *PgStore) KeywordSearch(ctx context.Context, query string, limit int) ([]types.FTSResult, error) {
	if query == "" {
		return nil, nil
	}

	tsQuery := buildTSQuery(query)
	if tsQuery == "" {
		return nil, nil
	}

	rows, err := s.query(ctx, `
		SELECT c.id, -ts_rank(c.content_tsv, to_tsquery('simple', $1)) AS rank
		FROM chunks c
		JOIN files f ON c.file_id = f.id
		WHERE f.project_id = $3 AND c.content_tsv @@ to_tsquery('simple', $1)
		ORDER BY rank
		LIMIT $2`, tsQuery, limit, s.projectID)
	if err != nil {
		return nil, fmt.Errorf("FTS search %q: %w", query, err)
	}
	defer rows.Close()

	var results []types.FTSResult
	for rows.Next() {
		var r types.FTSResult
		if err := rows.Scan(&r.ChunkID, &r.Rank); err != nil {
			return nil, fmt.Errorf("scan FTS result: %w", err)
		}
		results = append(results, r)
	}
	return results, rows.Err()
}

// ── EmbedSource interface ──

func (s *PgStore) GetEmbedPage(ctx context.Context, afterID int64, limit int) ([]types.EmbedJob, error) {
	rows, err := s.query(ctx,
		`SELECT c.id, c.content FROM chunks c
		 JOIN files f ON c.file_id = f.id
		 WHERE f.project_id = $3 AND c.embedded = 0 AND c.id > $1
		 ORDER BY c.id LIMIT $2`,
		afterID, limit, s.projectID)
	if err != nil {
		return nil, fmt.Errorf("get embed page: %w", err)
	}
	defer rows.Close()

	var jobs []types.EmbedJob
	for rows.Next() {
		var j types.EmbedJob
		if err := rows.Scan(&j.ChunkID, &j.Content); err != nil {
			return nil, err
		}
		jobs = append(jobs, j)
	}
	return jobs, rows.Err()
}

func (s *PgStore) MarkChunksEmbedded(ctx context.Context, chunkIDs []int64) error {
	if len(chunkIDs) == 0 {
		return nil
	}
	return s.WithWriteTx(ctx, func(txh types.TxHandle) error {
		tx := txh.(PgTxHandle).Tx

		// Mark chunks
		_, err := tx.Exec(ctx,
			"UPDATE chunks SET embedded = 1 WHERE id = ANY($1)", chunkIDs)
		if err != nil {
			return fmt.Errorf("mark chunks embedded: %w", err)
		}

		// Update file embedding_status
		rows, err := tx.Query(ctx,
			"SELECT DISTINCT file_id FROM chunks WHERE id = ANY($1)", chunkIDs)
		if err != nil {
			return err
		}
		var fileIDs []int64
		for rows.Next() {
			var fid int64
			if err := rows.Scan(&fid); err != nil {
				rows.Close()
				return fmt.Errorf("scan file id: %w", err)
			}
			fileIDs = append(fileIDs, fid)
		}
		rows.Close()

		for _, fid := range fileIDs {
			var remaining int
			if err := tx.QueryRow(ctx,
				"SELECT COUNT(*) FROM chunks WHERE file_id = $1 AND embedded = 0", fid,
			).Scan(&remaining); err != nil {
				return fmt.Errorf("count remaining embeddings for file %d: %w", fid, err)
			}
			if remaining == 0 {
				if _, err := tx.Exec(ctx, "UPDATE files SET embedding_status = 'complete' WHERE id = $1", fid); err != nil {
					return fmt.Errorf("mark file %d embedding complete: %w", fid, err)
				}
			} else {
				if _, err := tx.Exec(ctx, "UPDATE files SET embedding_status = 'partial' WHERE id = $1 AND embedding_status != 'complete'", fid); err != nil {
					return fmt.Errorf("mark file %d embedding partial: %w", fid, err)
				}
			}
		}
		return nil
	})
}

func (s *PgStore) CountChunksNeedingEmbedding(ctx context.Context) (int, error) {
	var count int
	err := s.queryRow(ctx,
		`SELECT COUNT(*) FROM chunks c JOIN files f ON c.file_id = f.id
		 WHERE f.project_id = $1 AND c.embedded = 0`, s.projectID).Scan(&count)
	return count, err
}

// ── EmbeddingReconciler interface ──

func (s *PgStore) CountChunksEmbedded(ctx context.Context) (int, error) {
	var count int
	err := s.queryRow(ctx,
		`SELECT COUNT(*) FROM chunks c JOIN files f ON c.file_id = f.id
		 WHERE f.project_id = $1 AND c.embedded = 1`, s.projectID).Scan(&count)
	return count, err
}

func (s *PgStore) ResetAllEmbeddedFlags(ctx context.Context) error {
	return s.WithWriteTx(ctx, func(txh types.TxHandle) error {
		tx := txh.(PgTxHandle).Tx
		if _, err := tx.Exec(ctx,
			`UPDATE chunks SET embedded = 0
			 WHERE embedded = 1 AND file_id IN (SELECT id FROM files WHERE project_id = $1)`, s.projectID); err != nil {
			return fmt.Errorf("reset chunk embedded flags: %w", err)
		}
		if _, err := tx.Exec(ctx,
			`UPDATE files SET embedding_status = 'pending'
			 WHERE project_id = $1 AND embedding_status != 'pending'`, s.projectID); err != nil {
			return fmt.Errorf("reset file embedding_status: %w", err)
		}
		return nil
	})
}

func (s *PgStore) GetEmbeddedChunkIDs(ctx context.Context, afterID int64, limit int) ([]int64, error) {
	rows, err := s.query(ctx,
		`SELECT c.id FROM chunks c
		 JOIN files f ON c.file_id = f.id
		 WHERE f.project_id = $3 AND c.embedded = 1 AND c.id > $1
		 ORDER BY c.id LIMIT $2`,
		afterID, limit, s.projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan embedded chunk id: %w", err)
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func (s *PgStore) ResetEmbeddedFlags(ctx context.Context, chunkIDs []int64) error {
	if len(chunkIDs) == 0 {
		return nil
	}
	return s.WithWriteTx(ctx, func(txh types.TxHandle) error {
		tx := txh.(PgTxHandle).Tx
		if _, err := tx.Exec(ctx, "UPDATE chunks SET embedded = 0 WHERE id = ANY($1)", chunkIDs); err != nil {
			return fmt.Errorf("reset embedded flags: %w", err)
		}

		rows, err := tx.Query(ctx, "SELECT DISTINCT file_id FROM chunks WHERE id = ANY($1)", chunkIDs)
		if err != nil {
			return err
		}
		var fileIDs []int64
		for rows.Next() {
			var fid int64
			if err := rows.Scan(&fid); err != nil {
				rows.Close()
				return fmt.Errorf("scan file id: %w", err)
			}
			fileIDs = append(fileIDs, fid)
		}
		rows.Close()

		for _, fid := range fileIDs {
			var embCount int
			if err := tx.QueryRow(ctx, "SELECT COUNT(*) FROM chunks WHERE file_id = $1 AND embedded = 1", fid).Scan(&embCount); err != nil {
				return fmt.Errorf("count embedded chunks for file %d: %w", fid, err)
			}
			status := "pending"
			if embCount > 0 {
				status = "partial"
			}
			if _, err := tx.Exec(ctx, "UPDATE files SET embedding_status = $1 WHERE id = $2", status, fid); err != nil {
				return fmt.Errorf("set embedding_status for file %d: %w", fid, err)
			}
		}
		return nil
	})
}

// PurgeAll deletes all indexed data while preserving schema and migrations.
// TRUNCATE files CASCADE cascades to: chunks, symbols, edges, pending_edges,
// diff_log, diff_symbols, and embeddings (pgvector FK). Standalone tables
// (access_log, working_set, tool_calls) are truncated explicitly.
// Preserved: goose_db_version, schema_version, config.
// PurgeAll deletes all indexed data for the current project while preserving
// schema, migrations, and the project registry entry.
// DELETE FROM files WHERE project_id cascades to: chunks, symbols, edges,
// pending_edges, diff_log, diff_symbols (via ON DELETE CASCADE FKs).
// Session-level tables (access_log, working_set, tool_calls) are not scoped
// by project_id and are left intact.
// Preserved: goose_db_version, projects, config (except parser_algorithm_version).
func (s *PgStore) PurgeAll(ctx context.Context) error {
	_, err := s.pool.Exec(ctx, "DELETE FROM files WHERE project_id = $1", s.projectID)
	if err != nil {
		return fmt.Errorf("purge files for project %d: %w", s.projectID, err)
	}
	// Clear parser version so the next index records the current version.
	_, err = s.pool.Exec(ctx, "DELETE FROM config WHERE key = 'parser_algorithm_version'")
	if err != nil {
		return fmt.Errorf("purge parser version config: %w", err)
	}
	return nil
}

func (s *PgStore) EmbeddingReadiness(ctx context.Context, vectorCount int) (float64, error) {
	var totalChunks int
	if err := s.queryRow(ctx,
		`SELECT COUNT(*) FROM chunks c JOIN files f ON c.file_id = f.id
		 WHERE f.project_id = $1`, s.projectID).Scan(&totalChunks); err != nil {
		return 0, err
	}
	if totalChunks == 0 {
		return 0, nil
	}
	return float64(vectorCount) / float64(totalChunks), nil
}

// ── Change scores ──

func (s *PgStore) ComputeChangeScores(ctx context.Context, chunkIDs []int64) (map[int64]float64, error) {
	// Delegate to diff.go implementation
	return computeChangeScoresPg(ctx, s, chunkIDs)
}

// ── Neighbors ──

func (s *PgStore) Neighbors(ctx context.Context, symbolID int64, maxDepth int, direction string) ([]int64, error) {
	// Delegate to graph.go implementation
	return neighborsPg(ctx, s, symbolID, maxDepth, direction)
}

// ── Batch methods ──

func (s *PgStore) BatchGetSymbolIDsForChunks(ctx context.Context, chunkIDs []int64) (map[int64]int64, error) {
	result := make(map[int64]int64, len(chunkIDs))
	rows, err := s.query(ctx, `
		SELECT c.id, s.id AS sym_id
		FROM chunks c
		JOIN files cf ON c.file_id = cf.id
		JOIN symbols s ON s.name = c.symbol_name
		JOIN files sf ON s.file_id = sf.id
		WHERE c.id = ANY($1) AND c.symbol_name != ''
		  AND cf.project_id = $2 AND sf.project_id = $2
		ORDER BY c.id, CASE WHEN s.file_id = c.file_id THEN 0 ELSE 1 END, s.id
	`, chunkIDs, s.projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var chunkID, symID int64
		if err := rows.Scan(&chunkID, &symID); err != nil {
			return nil, fmt.Errorf("scan chunk→symbol id: %w", err)
		}
		if _, exists := result[chunkID]; !exists {
			result[chunkID] = symID
		}
	}
	return result, rows.Err()
}

func (s *PgStore) BatchNeighbors(ctx context.Context, symbolIDs []int64, maxDepth int) (map[int64][]int64, error) {
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

func (s *PgStore) BatchGetChunkIDsForSymbols(ctx context.Context, symbolIDs []int64) (map[int64]int64, error) {
	result := make(map[int64]int64, len(symbolIDs))
	rows, err := s.query(ctx, `
		SELECT s.id, s.chunk_id
		FROM symbols s
		JOIN files f ON s.file_id = f.id
		WHERE s.id = ANY($1) AND f.project_id = $2`, symbolIDs, s.projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var symID, chunkID int64
		if err := rows.Scan(&symID, &chunkID); err != nil {
			return nil, fmt.Errorf("scan symbol→chunk id: %w", err)
		}
		result[symID] = chunkID
	}
	return result, rows.Err()
}

func (s *PgStore) BatchHydrateChunks(ctx context.Context, chunkIDs []int64) ([]types.HydratedChunk, error) {
	if len(chunkIDs) == 0 {
		return nil, nil
	}
	rows, err := s.query(ctx, `
		SELECT c.id, c.file_id, c.symbol_name, c.kind,
		       c.start_line, c.end_line, c.content, c.token_count,
		       f.path, f.is_test
		FROM chunks c
		JOIN files f ON c.file_id = f.id
		WHERE f.project_id = $2 AND c.id = ANY($1)`, chunkIDs, s.projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []types.HydratedChunk
	for rows.Next() {
		var h types.HydratedChunk
		if err := rows.Scan(&h.ChunkID, &h.FileID, &h.SymbolName, &h.Kind,
			&h.StartLine, &h.EndLine, &h.Content, &h.TokenCount,
			&h.Path, &h.IsTest); err != nil {
			return nil, err
		}
		results = append(results, h)
	}
	return results, rows.Err()
}

func (s *PgStore) BatchGetSiblingChunks(ctx context.Context, keys []types.SiblingKey) (map[string][]types.HydratedChunk, error) {
	result := make(map[string][]types.HydratedChunk, len(keys))
	for _, k := range keys {
		rows, err := s.query(ctx, `
			SELECT c.id, c.file_id, c.symbol_name, c.kind,
			       c.start_line, c.end_line, c.content, c.token_count,
			       f.path, f.is_test
			FROM chunks c
			JOIN files f ON c.file_id = f.id
			WHERE f.project_id = $4 AND c.file_id = $1 AND c.symbol_name = $2 AND c.kind = $3
			ORDER BY c.chunk_index`, k.FileID, k.SymbolName, k.Kind, s.projectID)
		if err != nil {
			return nil, fmt.Errorf("batch sibling chunks: %w", err)
		}
		var chunks []types.HydratedChunk
		for rows.Next() {
			var h types.HydratedChunk
			if err := rows.Scan(&h.ChunkID, &h.FileID, &h.SymbolName, &h.Kind,
				&h.StartLine, &h.EndLine, &h.Content, &h.TokenCount,
				&h.Path, &h.IsTest); err != nil {
				rows.Close()
				return nil, fmt.Errorf("scan sibling chunk: %w", err)
			}
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

func (s *PgStore) BatchGetFileHashes(ctx context.Context, paths []string) (map[string]string, error) {
	result := make(map[string]string, len(paths))
	rows, err := s.query(ctx,
		"SELECT path, content_hash FROM files WHERE project_id = $2 AND path = ANY($1)", paths, s.projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var path, hash string
		if err := rows.Scan(&path, &hash); err != nil {
			return nil, fmt.Errorf("scan file path/hash: %w", err)
		}
		result[path] = hash
	}
	return result, rows.Err()
}

// ── Query helpers ──

// buildTSQuery converts a user query string into a Postgres tsquery expression.
// Splits on whitespace, removes tsquery special characters, wraps terms in quotes,
// joins with OR. Returns empty string if no valid terms remain.
func buildTSQuery(query string) string {
	words := strings.Fields(query)
	var terms []string
	for _, w := range words {
		clean := strings.Map(func(r rune) rune {
			if r == '\'' || r == '&' || r == '|' || r == '!' || r == ':' || r == '*' {
				return -1
			}
			return r
		}, w)
		clean = strings.TrimSpace(clean)
		if clean != "" {
			terms = append(terms, "'"+clean+"'")
		}
	}
	if len(terms) == 0 {
		return ""
	}
	return strings.Join(terms, " | ")
}

// changeScore computes the recency*magnitude score for a diff entry.
// Score = exp(-0.05 * hours_since_change) * min(magnitude / 50.0, 1.0)
func changeScore(now, ts time.Time, linesAdded, linesRemoved int) float64 {
	hours := now.Sub(ts).Hours()
	magnitude := float64(linesAdded + linesRemoved)
	return math.Exp(-0.05*hours) * math.Min(magnitude/50.0, 1.0)
}

// ── Scan helpers ──

func scanChunks(rows pgx.Rows) ([]types.ChunkRecord, error) {
	var chunks []types.ChunkRecord
	for rows.Next() {
		var c types.ChunkRecord
		var parentID *int64
		if err := rows.Scan(&c.ID, &c.FileID, &parentID, &c.ChunkIndex, &c.SymbolName, &c.Kind,
			&c.StartLine, &c.EndLine, &c.Content, &c.TokenCount, &c.Signature, &c.ParseQuality); err != nil {
			return nil, err
		}
		c.ParentChunkID = parentID
		chunks = append(chunks, c)
	}
	return chunks, rows.Err()
}

func scanSymbols(rows pgx.Rows) ([]types.SymbolRecord, error) {
	var symbols []types.SymbolRecord
	for rows.Next() {
		var sym types.SymbolRecord
		if err := rows.Scan(&sym.ID, &sym.ChunkID, &sym.FileID, &sym.Name, &sym.QualifiedName, &sym.Kind,
			&sym.Line, &sym.Signature, &sym.Visibility, &sym.IsExported); err != nil {
			return nil, err
		}
		symbols = append(symbols, sym)
	}
	return symbols, rows.Err()
}

// ── Config key-value ──

// GetConfig returns the value for a config key. Returns empty string and nil
// error if the key is absent.
func (s *PgStore) GetConfig(ctx context.Context, key string) (string, error) {
	var value string
	err := s.queryRow(ctx, "SELECT value FROM config WHERE key = $1", key).Scan(&value)
	if err == pgx.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("get config %q: %w", key, err)
	}
	return value, nil
}

// SetConfig writes or overwrites a config key/value pair.
func (s *PgStore) SetConfig(ctx context.Context, key, value string) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO config (key, value) VALUES ($1, $2)
		ON CONFLICT(key) DO UPDATE SET value = EXCLUDED.value`, key, value)
	if err != nil {
		return fmt.Errorf("set config %q: %w", key, err)
	}
	return nil
}

// ── Metrics ──

// RecordToolCalls batch-inserts MCP tool call metrics.
func (s *PgStore) RecordToolCalls(ctx context.Context, records []types.ToolCallRecord) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	for _, rec := range records {
		if _, err := tx.Exec(ctx, `
			INSERT INTO tool_calls (session_id, timestamp, tool_name, args_json,
				args_bytes, response_bytes, response_tokens_est, result_count,
				duration_ms, is_error)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
			rec.SessionID, rec.Timestamp.UTC(), rec.ToolName, rec.ArgsJSON,
			rec.ArgsBytes, rec.ResponseBytes, rec.ResponseTokensEst,
			rec.ResultCount, rec.DurationMs, rec.IsError,
		); err != nil {
			return fmt.Errorf("insert tool call %s: %w", rec.ToolName, err)
		}
	}

	return tx.Commit(ctx)
}
