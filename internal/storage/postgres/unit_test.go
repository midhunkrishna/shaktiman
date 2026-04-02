//go:build postgres

package postgres

import (
	"math"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	pgmigrations "github.com/shaktimanai/shaktiman/internal/storage/migrations/postgres"
	"github.com/shaktimanai/shaktiman/internal/types"
)

// ── PgTxHandle ──

func TestPgTxHandle_SatisfiesTxHandle(t *testing.T) {
	// Compile-time check is via var _ types.WriterStore = (*PgStore)(nil) in db.go.
	// This verifies the TxHandle marker at runtime.
	var txh types.TxHandle = PgTxHandle{Tx: nil}
	txh.IsTxHandle() // must not panic
}

// ── Migration SQL files ──

func TestMigrationSQL_ContainsAllTables(t *testing.T) {
	ddl, err := pgmigrations.FS.ReadFile("001_base_schema.sql")
	if err != nil {
		t.Fatalf("read migration file: %v", err)
	}
	content := string(ddl)

	expected := []string{
		"schema_version", "config",
		"files", "chunks", "symbols",
		"edges", "pending_edges",
		"diff_log", "diff_symbols",
		"access_log", "working_set",
		"tool_calls",
	}
	for _, table := range expected {
		if !strings.Contains(content, "CREATE TABLE IF NOT EXISTS "+table) {
			t.Errorf("migration SQL missing table: %s", table)
		}
	}
}

func TestMigrationSQL_HasTsvectorColumn(t *testing.T) {
	ddl, _ := pgmigrations.FS.ReadFile("001_base_schema.sql")
	content := string(ddl)
	if !strings.Contains(content, "content_tsv") {
		t.Error("migration SQL missing tsvector column content_tsv")
	}
	if !strings.Contains(content, "to_tsvector('simple'") {
		t.Error("migration SQL should use 'simple' dictionary for tsvector")
	}
}

func TestMigrationSQL_UsesBigserial(t *testing.T) {
	ddl, _ := pgmigrations.FS.ReadFile("001_base_schema.sql")
	content := string(ddl)
	if !strings.Contains(content, "BIGSERIAL PRIMARY KEY") {
		t.Error("migration SQL should use BIGSERIAL for primary keys")
	}
	if strings.Contains(content, "AUTOINCREMENT") {
		t.Error("migration SQL should not contain SQLite AUTOINCREMENT")
	}
}

func TestMigrationSQL_PgvectorFiles(t *testing.T) {
	ext, err := pgmigrations.FS.ReadFile("002_pgvector_extension.sql")
	if err != nil {
		t.Fatalf("read pgvector extension migration: %v", err)
	}
	if !strings.Contains(string(ext), "CREATE EXTENSION IF NOT EXISTS vector") {
		t.Error("pgvector extension migration missing CREATE EXTENSION")
	}

	idx, err := pgmigrations.FS.ReadFile("004_pgvector_hnsw_index.sql")
	if err != nil {
		t.Fatalf("read pgvector index migration: %v", err)
	}
	if !strings.Contains(string(idx), "CREATE INDEX CONCURRENTLY") {
		t.Error("pgvector index migration missing CREATE INDEX CONCURRENTLY")
	}
}

// ── buildTSQuery ──

func TestBuildTSQuery(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"single word", "hello", "'hello'"},
		{"multiple words", "hello world", "'hello' | 'world'"},
		{"strips operators", "hello & world | foo", "'hello' | 'world' | 'foo'"},
		{"strips quotes", "it's fine", "'its' | 'fine'"},
		{"strips asterisks", "func*", "'func'"},
		{"empty string", "", ""},
		{"only operators", "& | ! :", ""},
		{"preserves CamelCase", "HandleRequest", "'HandleRequest'"},
		{"preserves snake_case", "handle_request", "'handle_request'"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildTSQuery(tt.input)
			if got != tt.want {
				t.Errorf("buildTSQuery(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

// ── changeScore ──

func TestChangeScore_RecentHighMagnitude(t *testing.T) {
	now := time.Now()
	ts := now.Add(-1 * time.Hour) // 1 hour ago
	score := changeScore(now, ts, 100, 50)

	// exp(-0.05 * 1) * min(150/50, 1.0) = ~0.951 * 1.0
	if score < 0.9 || score > 1.0 {
		t.Errorf("score = %f, expected ~0.95 for recent high-magnitude change", score)
	}
}

func TestChangeScore_OldChange(t *testing.T) {
	now := time.Now()
	ts := now.Add(-48 * time.Hour) // 48 hours ago
	score := changeScore(now, ts, 50, 0)

	// exp(-0.05 * 48) * 1.0 = exp(-2.4) ≈ 0.09
	if score > 0.15 {
		t.Errorf("score = %f, expected < 0.15 for 48h old change", score)
	}
}

func TestChangeScore_ZeroMagnitude(t *testing.T) {
	now := time.Now()
	ts := now.Add(-1 * time.Hour)
	score := changeScore(now, ts, 0, 0)

	if score != 0 {
		t.Errorf("score = %f, expected 0 for zero magnitude", score)
	}
}

func TestChangeScore_SmallMagnitude(t *testing.T) {
	now := time.Now()
	ts := now // just now
	score := changeScore(now, ts, 5, 0)

	// exp(0) * min(5/50, 1.0) = 1.0 * 0.1 = 0.1
	if math.Abs(score-0.1) > 0.01 {
		t.Errorf("score = %f, expected ~0.1 for 5-line change", score)
	}
}

func TestChangeScore_FutureTimestamp(t *testing.T) {
	now := time.Now()
	ts := now.Add(1 * time.Hour) // future (clock skew)
	score := changeScore(now, ts, 50, 0)

	// exp(-0.05 * (-1)) = exp(0.05) ≈ 1.05, clamped by magnitude
	if score < 1.0 {
		t.Errorf("score = %f, expected >= 1.0 for future timestamp", score)
	}
}


// ── RawPool / IsTxHandle ──

func TestPgTxHandle_IsTxHandle(t *testing.T) {
	// Marker method should not panic with nil Tx.
	h := PgTxHandle{Tx: nil}
	h.IsTxHandle()
}

func TestPgStore_RawPool_ReturnsPool(t *testing.T) {
	// RawPool should return the same object as Pool, typed as any.
	s := &PgStore{pool: nil, schema: "public"}
	raw := s.RawPool()
	// A nil *pgxpool.Pool wrapped in an interface is NOT nil (typed nil).
	// Verify it's a *pgxpool.Pool.
	if _, ok := raw.(*pgxpool.Pool); !ok {
		t.Errorf("RawPool should return *pgxpool.Pool, got %T", raw)
	}
}

func TestPgStore_RawPool_TypeMatches(t *testing.T) {
	// When pool is set, RawPool() should return the same pointer.
	// We can't easily create a real pool here without Postgres, so just
	// verify the method exists and the interface assertion works.
	s := &PgStore{schema: "public"}
	type rawPooler interface{ RawPool() any }
	var _ rawPooler = s // compile-time check
}
