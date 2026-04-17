//go:build sqlite_fts5

package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
)

// setupLargeBenchStore creates an in-memory DB with 150K chunks spread across
// 1000 files (150 chunks per file). Uses raw SQL with batched inserts for speed.
// Returns the store and all chunk IDs in ascending order.
func setupLargeBenchStore(b *testing.B) (*Store, []int64) {
	b.Helper()

	db, err := Open(OpenInput{InMemory: true})
	if err != nil {
		b.Fatalf("Open: %v", err)
	}
	b.Cleanup(func() { db.Close() })

	if err := Migrate(db); err != nil {
		b.Fatalf("Migrate: %v", err)
	}

	const (
		numFiles      = 1000
		chunksPerFile = 150
		totalChunks   = numFiles * chunksPerFile
	)

	store := NewStore(db)
	allChunkIDs := make([]int64, 0, totalChunks)

	err = db.WithWriteTx(func(tx *sql.Tx) error {
		// Insert files.
		fileStmt, err := tx.Prepare(`INSERT INTO files (path, content_hash, mtime, size, language, embedding_status, parse_quality)
			VALUES (?, 'hash', 1.0, 1024, 'go', 'pending', 'full')`)
		if err != nil {
			return fmt.Errorf("prepare file insert: %w", err)
		}
		defer fileStmt.Close()

		fileIDs := make([]int64, numFiles)
		for i := 0; i < numFiles; i++ {
			res, err := fileStmt.Exec(fmt.Sprintf("pkg/file_%04d.go", i))
			if err != nil {
				return fmt.Errorf("insert file %d: %w", i, err)
			}
			fileIDs[i], _ = res.LastInsertId()
		}

		// Insert chunks in batches.
		chunkStmt, err := tx.Prepare(`INSERT INTO chunks (file_id, chunk_index, symbol_name, kind, start_line, end_line, content, token_count, parse_quality, embedded)
			VALUES (?, ?, ?, 'function', ?, ?, ?, 10, 'full', 0)`)
		if err != nil {
			return fmt.Errorf("prepare chunk insert: %w", err)
		}
		defer chunkStmt.Close()

		for fi, fid := range fileIDs {
			for ci := 0; ci < chunksPerFile; ci++ {
				startLine := ci*10 + 1
				res, err := chunkStmt.Exec(
					fid, ci,
					fmt.Sprintf("func_%d_%d", fi, ci),
					startLine, startLine+9,
					fmt.Sprintf("func chunk_%d_%d() {}", fi, ci),
				)
				if err != nil {
					return fmt.Errorf("insert chunk file=%d chunk=%d: %w", fi, ci, err)
				}
				id, _ := res.LastInsertId()
				allChunkIDs = append(allChunkIDs, id)
			}
		}
		return nil
	})
	if err != nil {
		b.Fatalf("bulk insert: %v", err)
	}

	return store, allChunkIDs
}

// BenchmarkGetEmbedPage measures cursor-based embed page retrieval.
// Target: <5ms per page of 256 from a 150K chunk DB.
func BenchmarkGetEmbedPage(b *testing.B) {
	store, _ := setupLargeBenchStore(b)
	ctx := context.Background()

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		// Walk through pages using cursor pagination.
		afterID := int64(0)
		for {
			page, err := store.GetEmbedPage(ctx, afterID, 256)
			if err != nil {
				b.Fatalf("GetEmbedPage: %v", err)
			}
			if len(page) == 0 {
				break
			}
			afterID = page[len(page)-1].ChunkID
		}
	}
}

// BenchmarkMarkChunksEmbedded measures marking batches of 32 chunk IDs as embedded.
// Target: <10ms per batch of 32.
func BenchmarkMarkChunksEmbedded(b *testing.B) {
	store, allChunkIDs := setupLargeBenchStore(b)
	ctx := context.Background()

	const batchSize = 32
	// Pre-compute batches to avoid allocation in the hot loop.
	numBatches := len(allChunkIDs) / batchSize
	batches := make([][]int64, numBatches)
	for i := 0; i < numBatches; i++ {
		batches[i] = allChunkIDs[i*batchSize : (i+1)*batchSize]
	}

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		batch := batches[i%numBatches]
		if err := store.MarkChunksEmbedded(ctx, batch); err != nil {
			b.Fatalf("MarkChunksEmbedded: %v", err)
		}
	}
}

// BenchmarkCountChunksNeedingEmbedding measures the COUNT query on 150K chunks.
// Target: <50ms on 150K DB.
func BenchmarkCountChunksNeedingEmbedding(b *testing.B) {
	store, _ := setupLargeBenchStore(b)
	ctx := context.Background()

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		count, err := store.CountChunksNeedingEmbedding(ctx)
		if err != nil {
			b.Fatalf("CountChunksNeedingEmbedding: %v", err)
		}
		if count == 0 {
			b.Fatal("expected non-zero count")
		}
	}
}

// setupLargeBenchStoreWithSymbols extends setupLargeBenchStore with symbols and edges.
// Returns the store, all chunk IDs, and all symbol IDs.
func setupLargeBenchStoreWithSymbols(b *testing.B) (*Store, []int64, []int64) {
	b.Helper()

	db, err := Open(OpenInput{InMemory: true})
	if err != nil {
		b.Fatalf("Open: %v", err)
	}
	b.Cleanup(func() { db.Close() })

	if err := Migrate(db); err != nil {
		b.Fatalf("Migrate: %v", err)
	}

	const (
		numFiles      = 200
		chunksPerFile = 20
		totalChunks   = numFiles * chunksPerFile
	)

	store := NewStore(db)
	allChunkIDs := make([]int64, 0, totalChunks)
	allSymbolIDs := make([]int64, 0, totalChunks)

	err = db.WithWriteTx(func(tx *sql.Tx) error {
		fileStmt, err := tx.Prepare(`INSERT INTO files (path, content_hash, mtime, size, language, embedding_status, parse_quality)
			VALUES (?, 'hash', 1.0, 1024, 'go', 'pending', 'full')`)
		if err != nil {
			return err
		}
		defer fileStmt.Close()

		chunkStmt, err := tx.Prepare(`INSERT INTO chunks (file_id, chunk_index, symbol_name, kind, start_line, end_line, content, token_count, parse_quality, embedded)
			VALUES (?, ?, ?, 'function', ?, ?, ?, 10, 'full', 0)`)
		if err != nil {
			return err
		}
		defer chunkStmt.Close()

		symStmt, err := tx.Prepare(`INSERT INTO symbols (chunk_id, file_id, name, qualified_name, kind, line, signature, visibility, is_exported)
			VALUES (?, ?, ?, '', 'function', ?, '', 'public', 1)`)
		if err != nil {
			return err
		}
		defer symStmt.Close()

		edgeStmt, err := tx.Prepare(`INSERT INTO edges (src_symbol_id, dst_symbol_id, kind) VALUES (?, ?, 'calls')`)
		if err != nil {
			return err
		}
		defer edgeStmt.Close()

		for fi := 0; fi < numFiles; fi++ {
			fRes, err := fileStmt.Exec(fmt.Sprintf("pkg/file_%04d.go", fi))
			if err != nil {
				return err
			}
			fid, _ := fRes.LastInsertId()

			fileSymIDs := make([]int64, 0, chunksPerFile)

			for ci := 0; ci < chunksPerFile; ci++ {
				startLine := ci*10 + 1
				symName := fmt.Sprintf("Func_%d_%d", fi, ci)
				cRes, err := chunkStmt.Exec(fid, ci, symName, startLine, startLine+9,
					fmt.Sprintf("func %s() {}", symName))
				if err != nil {
					return err
				}
				cid, _ := cRes.LastInsertId()
				allChunkIDs = append(allChunkIDs, cid)

				sRes, err := symStmt.Exec(cid, fid, symName, startLine)
				if err != nil {
					return err
				}
				sid, _ := sRes.LastInsertId()
				allSymbolIDs = append(allSymbolIDs, sid)
				fileSymIDs = append(fileSymIDs, sid)
			}

			// Create call edges between consecutive symbols in this file
			for i := 0; i < len(fileSymIDs)-1; i++ {
				if _, err := edgeStmt.Exec(fileSymIDs[i], fileSymIDs[i+1]); err != nil {
					return err
				}
			}
		}
		return nil
	})
	if err != nil {
		b.Fatalf("bulk insert: %v", err)
	}

	return store, allChunkIDs, allSymbolIDs
}

// BenchmarkBatchHydrateChunks measures batch chunk hydration (JOIN chunks+files).
func BenchmarkBatchHydrateChunks(b *testing.B) {
	store, allChunkIDs, _ := setupLargeBenchStoreWithSymbols(b)
	ctx := context.Background()

	// Pick 200 chunk IDs spread across the dataset
	ids := make([]int64, 200)
	step := len(allChunkIDs) / 200
	for i := range ids {
		ids[i] = allChunkIDs[i*step]
	}

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		results, err := store.BatchHydrateChunks(ctx, ids)
		if err != nil {
			b.Fatalf("BatchHydrateChunks: %v", err)
		}
		if len(results) == 0 {
			b.Fatal("expected non-empty results")
		}
	}
}

// BenchmarkBatchGetFileHashes measures batch file hash lookup.
func BenchmarkBatchGetFileHashes(b *testing.B) {
	store, _, _ := setupLargeBenchStoreWithSymbols(b)
	ctx := context.Background()

	paths := make([]string, 200)
	for i := range paths {
		paths[i] = fmt.Sprintf("pkg/file_%04d.go", i)
	}

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		result, err := store.BatchGetFileHashes(ctx, paths)
		if err != nil {
			b.Fatalf("BatchGetFileHashes: %v", err)
		}
		if len(result) == 0 {
			b.Fatal("expected non-empty results")
		}
	}
}

// BenchmarkBatchGetSymbolIDsForChunks measures batch chunk→symbol resolution.
func BenchmarkBatchGetSymbolIDsForChunks(b *testing.B) {
	store, allChunkIDs, _ := setupLargeBenchStoreWithSymbols(b)
	ctx := context.Background()

	ids := make([]int64, 200)
	step := len(allChunkIDs) / 200
	for i := range ids {
		ids[i] = allChunkIDs[i*step]
	}

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		result, err := store.BatchGetSymbolIDsForChunks(ctx, ids)
		if err != nil {
			b.Fatalf("BatchGetSymbolIDsForChunks: %v", err)
		}
		if len(result) == 0 {
			b.Fatal("expected non-empty results")
		}
	}
}

// BenchmarkBatchGetChunkIDsForSymbols measures batch symbol→chunk resolution.
func BenchmarkBatchGetChunkIDsForSymbols(b *testing.B) {
	store, _, allSymbolIDs := setupLargeBenchStoreWithSymbols(b)
	ctx := context.Background()

	ids := make([]int64, 200)
	step := len(allSymbolIDs) / 200
	for i := range ids {
		ids[i] = allSymbolIDs[i*step]
	}

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		result, err := store.BatchGetChunkIDsForSymbols(ctx, ids)
		if err != nil {
			b.Fatalf("BatchGetChunkIDsForSymbols: %v", err)
		}
		if len(result) == 0 {
			b.Fatal("expected non-empty results")
		}
	}
}

// BenchmarkBatchNeighbors measures batch BFS neighbor traversal.
func BenchmarkBatchNeighbors(b *testing.B) {
	store, _, allSymbolIDs := setupLargeBenchStoreWithSymbols(b)
	ctx := context.Background()

	// Smaller batch — this calls Neighbors() per symbol internally
	ids := make([]int64, 50)
	step := len(allSymbolIDs) / 50
	for i := range ids {
		ids[i] = allSymbolIDs[i*step]
	}

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		result, err := store.BatchNeighbors(ctx, ids, 2)
		if err != nil {
			b.Fatalf("BatchNeighbors: %v", err)
		}
		if len(result) == 0 {
			b.Fatal("expected non-empty results")
		}
	}
}
