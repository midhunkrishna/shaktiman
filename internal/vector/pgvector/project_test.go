//go:build pgvector

package pgvector

import (
	"context"
	"testing"
)

func TestPgVector_SearchIsolation(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	setupSchema(t, pool, 4)

	storeA, err := NewStore(pool, 4, 1)
	if err != nil {
		t.Fatalf("NewStore A: %v", err)
	}
	defer storeA.Close()

	// Insert a second project row for project B.
	pool.Exec(ctx, `INSERT INTO projects (id, root_path, name) VALUES (2, '/tmp/project-B', 'B') ON CONFLICT DO NOTHING`)

	storeB, err := NewStore(pool, 4, 2)
	if err != nil {
		t.Fatalf("NewStore B: %v", err)
	}
	defer storeB.Close()

	// Seed chunk rows for project B.
	pool.Exec(ctx, `INSERT INTO files (id, path, content_hash, mtime, language, indexed_at, project_id)
		VALUES (2, 'b.go', 'xyz', 0, 'go', NOW(), 2) ON CONFLICT DO NOTHING`)
	pool.Exec(ctx, `INSERT INTO chunks (id, file_id, chunk_index, kind, start_line, end_line, content, token_count)
		VALUES (20, 2, 0, 'function', 1, 10, 'test', 10) ON CONFLICT DO NOTHING`)

	// Upsert vectors in each project.
	if err := storeA.Upsert(ctx, 1, []float32{1, 0, 0, 0}); err != nil {
		t.Fatalf("Upsert A: %v", err)
	}
	if err := storeB.Upsert(ctx, 20, []float32{0.9, 0.1, 0, 0}); err != nil {
		t.Fatalf("Upsert B: %v", err)
	}

	// Search from project A should not return project B's vector.
	resultsA, err := storeA.Search(ctx, []float32{1, 0, 0, 0}, 10)
	if err != nil {
		t.Fatalf("Search A: %v", err)
	}
	for _, r := range resultsA {
		if r.ChunkID == 20 {
			t.Error("project A search returned project B's chunk (id=20)")
		}
	}
	if len(resultsA) != 1 || resultsA[0].ChunkID != 1 {
		t.Errorf("project A search: expected [chunk_id=1], got %v", resultsA)
	}

	// Search from project B should not return project A's vector.
	resultsB, err := storeB.Search(ctx, []float32{1, 0, 0, 0}, 10)
	if err != nil {
		t.Fatalf("Search B: %v", err)
	}
	for _, r := range resultsB {
		if r.ChunkID == 1 {
			t.Error("project B search returned project A's chunk (id=1)")
		}
	}
	if len(resultsB) != 1 || resultsB[0].ChunkID != 20 {
		t.Errorf("project B search: expected [chunk_id=20], got %v", resultsB)
	}

	t.Cleanup(func() { pool.Exec(ctx, "DROP TABLE IF EXISTS embeddings") })
}

func TestPgVector_CountIsolation(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	setupSchema(t, pool, 4)

	storeA, err := NewStore(pool, 4, 1)
	if err != nil {
		t.Fatalf("NewStore A: %v", err)
	}
	defer storeA.Close()

	pool.Exec(ctx, `INSERT INTO projects (id, root_path, name) VALUES (2, '/tmp/project-B', 'B') ON CONFLICT DO NOTHING`)

	storeB, err := NewStore(pool, 4, 2)
	if err != nil {
		t.Fatalf("NewStore B: %v", err)
	}
	defer storeB.Close()

	pool.Exec(ctx, `INSERT INTO files (id, path, content_hash, mtime, language, indexed_at, project_id)
		VALUES (2, 'b.go', 'xyz', 0, 'go', NOW(), 2) ON CONFLICT DO NOTHING`)
	pool.Exec(ctx, `INSERT INTO chunks (id, file_id, chunk_index, kind, start_line, end_line, content, token_count)
		VALUES (20, 2, 0, 'function', 1, 10, 'test', 10) ON CONFLICT DO NOTHING`)

	storeA.Upsert(ctx, 1, []float32{1, 0, 0, 0})
	storeA.Upsert(ctx, 2, []float32{0, 1, 0, 0})
	storeB.Upsert(ctx, 20, []float32{0, 0, 1, 0})

	countA, _ := storeA.Count(ctx)
	countB, _ := storeB.Count(ctx)

	if countA != 2 {
		t.Errorf("project A count: expected 2, got %d", countA)
	}
	if countB != 1 {
		t.Errorf("project B count: expected 1, got %d", countB)
	}

	t.Cleanup(func() { pool.Exec(ctx, "DROP TABLE IF EXISTS embeddings") })
}
