package mcp

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"testing"
	"time"

	mcpsdk "github.com/mark3labs/mcp-go/mcp"
	_ "github.com/mattn/go-sqlite3"

	"github.com/shaktimanai/shaktiman/internal/types"
)

// testMetricsWriter wraps a raw *sql.DB to satisfy types.MetricsWriter for tests.
type testMetricsWriter struct{ db *sql.DB }

func (w *testMetricsWriter) RecordToolCalls(_ context.Context, records []types.ToolCallRecord) error {
	tx, err := w.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	stmt, err := tx.Prepare(`INSERT INTO tool_calls (session_id, timestamp, tool_name, args_json,
		args_bytes, response_bytes, response_tokens_est, result_count, duration_ms, is_error)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, r := range records {
		isErr := 0
		if r.IsError {
			isErr = 1
		}
		if _, err := stmt.Exec(r.SessionID, r.Timestamp.UTC().Format(time.RFC3339Nano),
			r.ToolName, r.ArgsJSON, r.ArgsBytes, r.ResponseBytes,
			r.ResponseTokensEst, r.ResultCount, r.DurationMs, isErr); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// testDB creates an in-memory SQLite database with the tool_calls schema.
func testDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open in-memory db: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	_, err = db.Exec(`CREATE TABLE IF NOT EXISTS tool_calls (
		id                  INTEGER PRIMARY KEY AUTOINCREMENT,
		session_id          TEXT NOT NULL,
		timestamp           TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%%H:%%M:%%fZ', 'now')),
		tool_name           TEXT NOT NULL,
		args_json           TEXT,
		args_bytes          INTEGER NOT NULL DEFAULT 0,
		response_bytes      INTEGER NOT NULL DEFAULT 0,
		response_tokens_est INTEGER NOT NULL DEFAULT 0,
		result_count        INTEGER NOT NULL DEFAULT 0,
		duration_ms         INTEGER NOT NULL DEFAULT 0,
		is_error            INTEGER NOT NULL DEFAULT 0
	)`)
	if err != nil {
		t.Fatalf("create tool_calls table: %v", err)
	}
	return db
}

func testRecorder(t *testing.T, db *sql.DB, sessionID string) *MetricsRecorder {
	t.Helper()
	return NewMetricsRecorder(MetricsRecorderInput{
		Writer:    &testMetricsWriter{db: db},
		SessionID: sessionID,
		Logger:    slog.Default(),
	})
}

// ── Unit tests ──

func TestMetricsRecorder_Record(t *testing.T) {
	t.Parallel()
	db := testDB(t)
	r := testRecorder(t, db, "test-session")

	r.Record(ToolCallRecord{
		Timestamp: time.Now(),
		ToolName:  "search",
		ArgsJSON:  `{"query":"test"}`,
		ArgsBytes: 16,
	})

	if r.Pending() != 1 {
		t.Fatalf("Pending() = %d, want 1", r.Pending())
	}
}

func TestMetricsRecorder_DropOnFull(t *testing.T) {
	t.Parallel()
	db := testDB(t)
	r := NewMetricsRecorder(MetricsRecorderInput{
		Writer:    &testMetricsWriter{db: db},
		SessionID: "full-test",
		Logger:    slog.Default(),
	})

	// Fill the channel completely
	for i := 0; i < metricsChannelCap; i++ {
		r.Record(ToolCallRecord{
			Timestamp: time.Now(),
			ToolName:  fmt.Sprintf("tool-%d", i),
		})
	}

	// This should not block — it drops
	done := make(chan struct{})
	go func() {
		r.Record(ToolCallRecord{
			Timestamp: time.Now(),
			ToolName:  "dropped",
		})
		close(done)
	}()

	select {
	case <-done:
		// Success: did not block
	case <-time.After(time.Second):
		t.Fatal("Record() blocked on full channel")
	}
}

func TestWithMetrics_CapturesFields(t *testing.T) {
	t.Parallel()
	db := testDB(t)
	r := testRecorder(t, db, "capture-test")

	handler := func(_ context.Context, _ mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
		return mcpsdk.NewToolResultText("hello world"), nil
	}

	wrapped := withMetrics("test_tool", r, handler)

	req := mcpsdk.CallToolRequest{}
	req.Params.Name = "test_tool"
	req.Params.Arguments = map[string]any{"query": "find me"}

	result, err := wrapped(context.Background(), req)
	if err != nil {
		t.Fatalf("wrapped handler error: %v", err)
	}
	if result == nil {
		t.Fatal("result is nil")
	}

	// Check that a record was queued
	if r.Pending() != 1 {
		t.Fatalf("Pending() = %d, want 1", r.Pending())
	}
}

func TestWithMetrics_ErrorCase(t *testing.T) {
	t.Parallel()
	db := testDB(t)
	r := testRecorder(t, db, "error-test")

	handler := func(_ context.Context, _ mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
		return mcpsdk.NewToolResultError("something went wrong"), nil
	}

	wrapped := withMetrics("failing_tool", r, handler)
	req := mcpsdk.CallToolRequest{}
	_, _ = wrapped(context.Background(), req)

	// Drain and verify the record has is_error set
	ctx, cancel := context.WithCancel(context.Background())
	go r.Run(ctx)

	// Give recorder time to flush
	time.Sleep(1500 * time.Millisecond)
	cancel()
	time.Sleep(100 * time.Millisecond)

	var isError int
	err := db.QueryRow("SELECT is_error FROM tool_calls WHERE tool_name = 'failing_tool'").Scan(&isError)
	if err != nil {
		t.Fatalf("query is_error: %v", err)
	}
	if isError != 1 {
		t.Errorf("is_error = %d, want 1", isError)
	}
}

func TestWithMetrics_NilRecorder(t *testing.T) {
	t.Parallel()
	handler := func(_ context.Context, _ mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
		return mcpsdk.NewToolResultText("ok"), nil
	}

	// Should not panic with nil recorder
	wrapped := withMetrics("nil_recorder_tool", nil, handler)
	result, err := wrapped(context.Background(), mcpsdk.CallToolRequest{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("result is nil")
	}
}

func TestWithResultCount(t *testing.T) {
	t.Parallel()

	result := mcpsdk.NewToolResultText("test")
	tagged := withResultCount(result, 42)

	// Should return same pointer
	if tagged != result {
		t.Error("withResultCount should return same pointer")
	}

	// Should extract the count
	got := extractResultCount(result)
	if got != 42 {
		t.Errorf("extractResultCount = %d, want 42", got)
	}

	// Second extraction should return 0 (LoadAndDelete)
	got = extractResultCount(result)
	if got != 0 {
		t.Errorf("second extractResultCount = %d, want 0 (already deleted)", got)
	}
}

func TestExtractResultCount_Nil(t *testing.T) {
	t.Parallel()
	got := extractResultCount(nil)
	if got != 0 {
		t.Errorf("extractResultCount(nil) = %d, want 0", got)
	}
}

func TestExtractResultCount_Untagged(t *testing.T) {
	t.Parallel()
	result := mcpsdk.NewToolResultText("untagged")
	got := extractResultCount(result)
	if got != 0 {
		t.Errorf("extractResultCount(untagged) = %d, want 0", got)
	}
}

func TestWithMetrics_ResultCount(t *testing.T) {
	t.Parallel()
	db := testDB(t)
	r := testRecorder(t, db, "result-count-test")

	handler := func(_ context.Context, _ mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
		result := mcpsdk.NewToolResultText("some results")
		return withResultCount(result, 7), nil
	}

	wrapped := withMetrics("counted_tool", r, handler)
	req := mcpsdk.CallToolRequest{}
	_, _ = wrapped(context.Background(), req)

	// Drain
	ctx, cancel := context.WithCancel(context.Background())
	go r.Run(ctx)
	time.Sleep(1500 * time.Millisecond)
	cancel()
	time.Sleep(100 * time.Millisecond)

	var resultCount int
	err := db.QueryRow("SELECT result_count FROM tool_calls WHERE tool_name = 'counted_tool'").Scan(&resultCount)
	if err != nil {
		t.Fatalf("query result_count: %v", err)
	}
	if resultCount != 7 {
		t.Errorf("result_count = %d, want 7", resultCount)
	}
}

func TestResponseBytes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		result   *mcpsdk.CallToolResult
		expected int
	}{
		{"nil result", nil, 0},
		{"text content", mcpsdk.NewToolResultText("hello"), 5},
		{"empty text", mcpsdk.NewToolResultText(""), 0},
		{"error result", mcpsdk.NewToolResultError("bad"), 3},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := responseBytes(tt.result)
			if got != tt.expected {
				t.Errorf("responseBytes() = %d, want %d", got, tt.expected)
			}
		})
	}
}

// ── Integration tests ──

func TestMetricsEndToEnd(t *testing.T) {
	t.Parallel()
	db := testDB(t)
	r := testRecorder(t, db, "e2e-session")

	ctx, cancel := context.WithCancel(context.Background())
	go r.Run(ctx)

	// Wrap a handler and call it
	handler := func(_ context.Context, _ mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
		return mcpsdk.NewToolResultText("search results here"), nil
	}
	wrapped := withMetrics("search", r, handler)

	req := mcpsdk.CallToolRequest{}
	req.Params.Arguments = map[string]any{"query": "auth", "max_results": float64(5)}

	_, _ = wrapped(context.Background(), req)

	// Wait for flush (ticker fires every second)
	time.Sleep(1500 * time.Millisecond)
	cancel()
	time.Sleep(100 * time.Millisecond)

	// Verify record was persisted
	var (
		sessionID    string
		toolName     string
		argsBytes    int
		responseBytes int
		tokensEst    int
		durationMs   int64
		isError      int
	)
	err := db.QueryRow(`
		SELECT session_id, tool_name, args_bytes, response_bytes,
		       response_tokens_est, duration_ms, is_error
		FROM tool_calls WHERE tool_name = 'search'
	`).Scan(&sessionID, &toolName, &argsBytes, &responseBytes, &tokensEst, &durationMs, &isError)
	if err != nil {
		t.Fatalf("query tool_calls: %v", err)
	}

	if sessionID != "e2e-session" {
		t.Errorf("session_id = %q, want %q", sessionID, "e2e-session")
	}
	if toolName != "search" {
		t.Errorf("tool_name = %q, want %q", toolName, "search")
	}
	if responseBytes != len("search results here") {
		t.Errorf("response_bytes = %d, want %d", responseBytes, len("search results here"))
	}
	if tokensEst != len("search results here")/4 {
		t.Errorf("response_tokens_est = %d, want %d", tokensEst, len("search results here")/4)
	}
	if argsBytes <= 0 {
		t.Errorf("args_bytes = %d, want > 0", argsBytes)
	}
	if isError != 0 {
		t.Errorf("is_error = %d, want 0", isError)
	}
}

func TestMetricsAcrossSessions(t *testing.T) {
	t.Parallel()
	db := testDB(t)

	// Insert records for two different sessions
	for _, sid := range []string{"session-1", "session-2"} {
		for i := 0; i < 3; i++ {
			err := InsertToolCall(&testMetricsWriter{db: db}, ToolCallRecord{
				SessionID:     sid,
				Timestamp:     time.Now(),
				ToolName:      "search",
				ArgsJSON:      `{"query":"test"}`,
				ArgsBytes:     16,
				ResponseBytes: 100 * (i + 1),
			})
			if err != nil {
				t.Fatalf("InsertToolCall: %v", err)
			}
		}
	}

	// Query per-session stats
	rows, err := db.Query(`
		SELECT session_id, COUNT(*), SUM(response_bytes)
		FROM tool_calls
		GROUP BY session_id
		ORDER BY session_id
	`)
	if err != nil {
		t.Fatalf("query stats: %v", err)
	}
	defer rows.Close()

	type sessionStats struct {
		sessionID     string
		count         int
		totalRespBytes int
	}
	var stats []sessionStats
	for rows.Next() {
		var s sessionStats
		if err := rows.Scan(&s.sessionID, &s.count, &s.totalRespBytes); err != nil {
			t.Fatalf("scan: %v", err)
		}
		stats = append(stats, s)
	}

	if len(stats) != 2 {
		t.Fatalf("got %d sessions, want 2", len(stats))
	}
	for _, s := range stats {
		if s.count != 3 {
			t.Errorf("session %s: count = %d, want 3", s.sessionID, s.count)
		}
		// 100 + 200 + 300 = 600
		if s.totalRespBytes != 600 {
			t.Errorf("session %s: totalRespBytes = %d, want 600", s.sessionID, s.totalRespBytes)
		}
	}
}

func TestMetricsRecorder_GracefulShutdown(t *testing.T) {
	t.Parallel()
	db := testDB(t)
	r := testRecorder(t, db, "shutdown-test")

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		r.Run(ctx)
		close(done)
	}()

	// Queue several records
	for i := 0; i < 5; i++ {
		r.Record(ToolCallRecord{
			Timestamp: time.Now(),
			ToolName:  fmt.Sprintf("tool-%d", i),
			ArgsBytes: 10,
		})
	}

	// Cancel context to trigger shutdown drain
	cancel()

	// Wait for Run to exit
	select {
	case <-done:
		// Success
	case <-time.After(5 * time.Second):
		t.Fatal("Run() did not exit after context cancellation")
	}

	// All 5 records should have been drained and persisted
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM tool_calls WHERE session_id = 'shutdown-test'").Scan(&count); err != nil {
		t.Fatalf("count query: %v", err)
	}
	if count != 5 {
		t.Errorf("count = %d, want 5", count)
	}
}

func TestMetricsRecorder_BatchInsert(t *testing.T) {
	t.Parallel()
	db := testDB(t)
	r := testRecorder(t, db, "batch-test")

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		r.Run(ctx)
		close(done)
	}()

	// Send exactly metricsBatchSize records to trigger a batch flush
	for i := 0; i < metricsBatchSize; i++ {
		r.Record(ToolCallRecord{
			Timestamp: time.Now(),
			ToolName:  fmt.Sprintf("batch-tool-%d", i),
			ArgsBytes: i,
		})
	}

	// Wait for batch to flush (should happen immediately when batch size reached)
	time.Sleep(500 * time.Millisecond)

	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM tool_calls WHERE session_id = 'batch-test'").Scan(&count); err != nil {
		t.Fatalf("count query: %v", err)
	}
	if count != metricsBatchSize {
		t.Errorf("count = %d, want %d (batch should have flushed)", count, metricsBatchSize)
	}

	cancel()
	<-done
}

func TestInsertToolCall_Success(t *testing.T) {
	t.Parallel()
	db := testDB(t)
	rec := ToolCallRecord{
		SessionID:  "insert-test",
		Timestamp:  time.Now(),
		ToolName:   "search",
		ArgsJSON:   `{"query":"test"}`,
		DurationMs: 100,
		ResultCount: 5,
	}
	if err := InsertToolCall(&testMetricsWriter{db: db}, rec); err != nil {
		t.Fatalf("InsertToolCall: %v", err)
	}
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM tool_calls").Scan(&count); err != nil {
		t.Fatalf("count query: %v", err)
	}
	if count != 1 {
		t.Errorf("count = %d, want 1", count)
	}
}

func TestMetricsRecorder_Pending(t *testing.T) {
	t.Parallel()
	db := testDB(t)
	r := testRecorder(t, db, "pending-test")

	if r.Pending() != 0 {
		t.Errorf("Pending() = %d, want 0 initially", r.Pending())
	}

	r.Record(ToolCallRecord{Timestamp: time.Now(), ToolName: "a"})
	r.Record(ToolCallRecord{Timestamp: time.Now(), ToolName: "b"})

	if r.Pending() != 2 {
		t.Errorf("Pending() = %d, want 2", r.Pending())
	}
}

func TestFlush_BeginTxError(t *testing.T) {
	t.Parallel()
	db := testDB(t)
	r := testRecorder(t, db, "flush-begin-err")

	// Close the DB to force Begin() to fail.
	db.Close()

	// flush should log the error but not panic.
	r.flush([]ToolCallRecord{
		{Timestamp: time.Now(), ToolName: "tool-a", SessionID: "s"},
	})
}

func TestFlush_PrepareError(t *testing.T) {
	t.Parallel()
	// Create a DB without the tool_calls table to force Prepare to fail.
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	r := testRecorder(t, db, "flush-prepare-err")

	// flush should log the error but not panic.
	r.flush([]ToolCallRecord{
		{Timestamp: time.Now(), ToolName: "tool-b", SessionID: "s"},
	})
}

func TestInsertToolCall_Error(t *testing.T) {
	t.Parallel()
	db := testDB(t)
	db.Close() // close DB to force error

	err := InsertToolCall(&testMetricsWriter{db: db}, ToolCallRecord{
		SessionID: "err-test",
		Timestamp: time.Now(),
		ToolName:  "search",
	})
	if err == nil {
		t.Fatal("expected error from InsertToolCall on closed DB")
	}
}

func TestInsertToolCall_IsErrorFlag(t *testing.T) {
	t.Parallel()
	db := testDB(t)

	err := InsertToolCall(&testMetricsWriter{db: db}, ToolCallRecord{
		SessionID: "flag-test",
		Timestamp: time.Now(),
		ToolName:  "search",
		IsError:   true,
	})
	if err != nil {
		t.Fatalf("InsertToolCall: %v", err)
	}

	var isErr int
	if err := db.QueryRow("SELECT is_error FROM tool_calls WHERE session_id = 'flag-test'").Scan(&isErr); err != nil {
		t.Fatalf("query: %v", err)
	}
	if isErr != 1 {
		t.Errorf("is_error = %d, want 1", isErr)
	}
}

func TestWithResultCount_Nil(t *testing.T) {
	t.Parallel()
	// withResultCount(nil, ...) should not panic.
	result := withResultCount(nil, 5)
	if result != nil {
		t.Errorf("expected nil result, got %v", result)
	}
}
