//go:build postgres

package postgres

import (
	"context"
	"testing"
)

// TestMigration008_CheckConstraintsExist verifies that migration 008
// installed the defense-in-depth CHECK constraints on files and
// embeddings.
func TestMigration008_CheckConstraintsExist(t *testing.T) {
	store := newProjectTestStore(t)
	ctx := context.Background()

	type want struct {
		table      string
		constraint string
	}
	cases := []want{
		{"files", "files_project_id_not_null"},
		{"embeddings", "embeddings_project_id_not_null"},
	}

	for _, c := range cases {
		var found bool
		err := store.Pool().QueryRow(ctx, `
			SELECT EXISTS (
				SELECT 1 FROM pg_constraint
				WHERE conname = $1 AND contype = 'c'
			)`, c.constraint).Scan(&found)
		if err != nil {
			t.Fatalf("query %s: %v", c.constraint, err)
		}
		if !found {
			t.Errorf("constraint %s missing on %s after Migrate", c.constraint, c.table)
		}
	}
}
