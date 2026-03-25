//go:build sqlite_fts5

package storage

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
