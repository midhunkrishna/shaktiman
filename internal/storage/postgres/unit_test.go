package postgres

import (
	"math"
	"strings"
	"testing"
	"time"

	"github.com/shaktimanai/shaktiman/internal/types"
)

// ── PgTxHandle ──

func TestPgTxHandle_SatisfiesTxHandle(t *testing.T) {
	// Compile-time check is via var _ types.WriterStore = (*PgStore)(nil) in db.go.
	// This verifies the TxHandle marker at runtime.
	var txh types.TxHandle = PgTxHandle{Tx: nil}
	txh.IsTxHandle() // must not panic
}

// ── Schema DDL ──

func TestSchemaDDL_ContainsAllTables(t *testing.T) {
	expected := []string{
		"schema_version", "config",
		"files", "chunks", "symbols",
		"edges", "pending_edges",
		"diff_log", "diff_symbols",
		"access_log", "working_set",
		"tool_calls",
	}

	ddlJoined := strings.Join(schemaDDL, "\n")
	for _, table := range expected {
		if !strings.Contains(ddlJoined, "CREATE TABLE IF NOT EXISTS "+table) {
			t.Errorf("schema DDL missing table: %s", table)
		}
	}
}

func TestSchemaDDL_HasTsvectorColumn(t *testing.T) {
	ddlJoined := strings.Join(schemaDDL, "\n")
	if !strings.Contains(ddlJoined, "content_tsv") {
		t.Error("schema DDL missing tsvector column content_tsv")
	}
	if !strings.Contains(ddlJoined, "to_tsvector('simple'") {
		t.Error("schema DDL should use 'simple' dictionary for tsvector")
	}
}

func TestSchemaDDL_HasGINIndex(t *testing.T) {
	ddlJoined := strings.Join(schemaDDL, "\n")
	if !strings.Contains(ddlJoined, "USING GIN(content_tsv)") {
		t.Error("schema DDL missing GIN index on content_tsv")
	}
}

func TestSchemaDDL_UsesBigserial(t *testing.T) {
	ddlJoined := strings.Join(schemaDDL, "\n")
	if !strings.Contains(ddlJoined, "BIGSERIAL PRIMARY KEY") {
		t.Error("schema DDL should use BIGSERIAL for primary keys")
	}
	if strings.Contains(ddlJoined, "AUTOINCREMENT") {
		t.Error("schema DDL should not contain SQLite AUTOINCREMENT")
	}
}

func TestSchemaDDL_UsesTimestamptz(t *testing.T) {
	ddlJoined := strings.Join(schemaDDL, "\n")
	if !strings.Contains(ddlJoined, "TIMESTAMPTZ") {
		t.Error("schema DDL should use TIMESTAMPTZ for timestamps")
	}
	if strings.Contains(ddlJoined, "strftime") {
		t.Error("schema DDL should not contain SQLite strftime")
	}
}

func TestSchemaDDL_UsesBoolean(t *testing.T) {
	ddlJoined := strings.Join(schemaDDL, "\n")
	// files.is_test should be BOOLEAN, not INTEGER
	if !strings.Contains(ddlJoined, "is_test          BOOLEAN") &&
		!strings.Contains(ddlJoined, "is_test    BOOLEAN") &&
		!strings.Contains(ddlJoined, "is_test BOOLEAN") {
		t.Error("files.is_test should use BOOLEAN type")
	}
}

func TestSchemaDDL_IndexCount(t *testing.T) {
	count := 0
	for _, stmt := range schemaDDL {
		if strings.HasPrefix(strings.TrimSpace(stmt), "CREATE INDEX") {
			count++
		}
	}
	// Should have at least as many indexes as SQLite (26+)
	if count < 20 {
		t.Errorf("expected at least 20 indexes, got %d", count)
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

// ── pgSchemaVersion ──

func TestPgSchemaVersion(t *testing.T) {
	if pgSchemaVersion < 1 {
		t.Errorf("pgSchemaVersion = %d, must be >= 1", pgSchemaVersion)
	}
}
