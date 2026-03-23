package mcp

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"

	mcpsdk "github.com/mark3labs/mcp-go/mcp"
)

// resultCountMap carries result count from handlers to withMetrics.
// Keyed by *CallToolResult pointer; cleaned up via LoadAndDelete after each call.
var resultCountMap sync.Map

// withResultCount attaches a result count to a CallToolResult for metrics extraction.
func withResultCount(result *mcpsdk.CallToolResult, count int) *mcpsdk.CallToolResult {
	if result != nil {
		resultCountMap.Store(result, count)
	}
	return result
}

// extractResultCount retrieves and removes the result count attached to a CallToolResult.
func extractResultCount(result *mcpsdk.CallToolResult) int {
	if result == nil {
		return 0
	}
	if v, ok := resultCountMap.LoadAndDelete(result); ok {
		return v.(int)
	}
	return 0
}

// ToolCallRecord holds metrics for a single MCP tool invocation.
type ToolCallRecord struct {
	SessionID        string
	Timestamp        time.Time
	ToolName         string
	ArgsJSON         string
	ArgsBytes        int
	ResponseBytes    int
	ResponseTokensEst int
	ResultCount      int
	DurationMs       int64
	IsError          bool
}

// MetricsRecorderInput configures a MetricsRecorder.
type MetricsRecorderInput struct {
	DB        *sql.DB
	SessionID string
	Logger    *slog.Logger
}

// MetricsRecorder persists MCP tool call metrics to SQLite asynchronously.
// Records are buffered in a channel and batch-inserted by a dedicated goroutine.
type MetricsRecorder struct {
	db        *sql.DB
	sessionID string
	logger    *slog.Logger
	ch        chan ToolCallRecord
}

const metricsChannelCap = 256
const metricsBatchSize = 10

// NewMetricsRecorder creates a recorder. Call Run() in a goroutine to start processing.
func NewMetricsRecorder(input MetricsRecorderInput) *MetricsRecorder {
	return &MetricsRecorder{
		db:        input.DB,
		sessionID: input.SessionID,
		logger:    input.Logger,
		ch:        make(chan ToolCallRecord, metricsChannelCap),
	}
}

// Record enqueues a tool call record. Non-blocking: drops on full channel.
func (r *MetricsRecorder) Record(rec ToolCallRecord) {
	rec.SessionID = r.sessionID
	select {
	case r.ch <- rec:
	default:
		r.logger.Warn("metrics channel full, dropping record", "tool", rec.ToolName)
	}
}

// Run processes queued records until ctx is cancelled, then drains remaining.
func (r *MetricsRecorder) Run(ctx context.Context) {
	batch := make([]ToolCallRecord, 0, metricsBatchSize)
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	for {
		select {
		case rec := <-r.ch:
			batch = append(batch, rec)
			if len(batch) >= metricsBatchSize {
				r.flush(batch)
				batch = batch[:0]
			}
		case <-ticker.C:
			if len(batch) > 0 {
				r.flush(batch)
				batch = batch[:0]
			}
		case <-ctx.Done():
			// Drain remaining records from channel
			close(r.ch)
			for rec := range r.ch {
				batch = append(batch, rec)
			}
			if len(batch) > 0 {
				r.flush(batch)
			}
			return
		}
	}
}

// flush batch-inserts records into the tool_calls table.
func (r *MetricsRecorder) flush(batch []ToolCallRecord) {
	tx, err := r.db.Begin()
	if err != nil {
		r.logger.Error("metrics flush: begin tx", "err", err)
		return
	}

	stmt, err := tx.Prepare(`
		INSERT INTO tool_calls (session_id, timestamp, tool_name, args_json,
			args_bytes, response_bytes, response_tokens_est, result_count,
			duration_ms, is_error)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		tx.Rollback()
		r.logger.Error("metrics flush: prepare", "err", err)
		return
	}
	defer stmt.Close()

	for _, rec := range batch {
		isErr := 0
		if rec.IsError {
			isErr = 1
		}
		ts := rec.Timestamp.UTC().Format("2006-01-02T15:04:05.000Z")
		if _, err := stmt.Exec(
			rec.SessionID, ts, rec.ToolName, rec.ArgsJSON,
			rec.ArgsBytes, rec.ResponseBytes, rec.ResponseTokensEst,
			rec.ResultCount, rec.DurationMs, isErr,
		); err != nil {
			r.logger.Error("metrics flush: insert", "tool", rec.ToolName, "err", err)
		}
	}

	if err := tx.Commit(); err != nil {
		r.logger.Error("metrics flush: commit", "err", err)
	}
}

// SessionID returns the session identifier for this recorder.
func (r *MetricsRecorder) SessionID() string {
	return r.sessionID
}

// responseBytes extracts the total byte size from a CallToolResult.
func responseBytes(result *mcpsdk.CallToolResult) int {
	if result == nil {
		return 0
	}
	total := 0
	for _, c := range result.Content {
		if tc, ok := c.(mcpsdk.TextContent); ok {
			total += len(tc.Text)
		}
	}
	return total
}

// withMetrics wraps an MCP tool handler with metrics recording and structured logging.
func withMetrics(name string, recorder *MetricsRecorder, h handlerFunc) handlerFunc {
	logger := slog.Default().With("component", "mcp")
	return func(ctx context.Context, req mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
		start := time.Now()

		argsJSON, _ := json.Marshal(req.GetArguments())
		argsBytes := len(argsJSON)

		result, err := h(ctx, req)

		duration := time.Since(start)
		isErr := err != nil || (result != nil && result.IsError)
		respBytes := responseBytes(result)
		tokensEst := respBytes / 4
		resultCount := extractResultCount(result)

		// Detailed metrics at debug level
		logger.Debug("tool_call_metrics",
			"tool", name,
			"args", string(argsJSON),
			"duration_ms", duration.Milliseconds(),
			"is_error", isErr,
			"args_bytes", argsBytes,
			"response_bytes", respBytes,
			"response_tokens_est", tokensEst,
			"result_count", resultCount,
		)

		// Async persist
		if recorder != nil {
			recorder.Record(ToolCallRecord{
				Timestamp:         start,
				ToolName:          name,
				ArgsJSON:          string(argsJSON),
				ArgsBytes:         argsBytes,
				ResponseBytes:     respBytes,
				ResponseTokensEst: tokensEst,
				ResultCount:       resultCount,
				DurationMs:        duration.Milliseconds(),
				IsError:           isErr,
			})
		}

		return result, err
	}
}

// Pending returns the number of unprocessed records in the channel.
func (r *MetricsRecorder) Pending() int {
	return len(r.ch)
}

// InsertToolCall inserts a single tool call record directly (for testing).
func InsertToolCall(db *sql.DB, rec ToolCallRecord) error {
	isErr := 0
	if rec.IsError {
		isErr = 1
	}
	ts := rec.Timestamp.UTC().Format("2006-01-02T15:04:05.000Z")
	_, err := db.Exec(`
		INSERT INTO tool_calls (session_id, timestamp, tool_name, args_json,
			args_bytes, response_bytes, response_tokens_est, result_count,
			duration_ms, is_error)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		rec.SessionID, ts, rec.ToolName, rec.ArgsJSON,
		rec.ArgsBytes, rec.ResponseBytes, rec.ResponseTokensEst,
		rec.ResultCount, rec.DurationMs, isErr,
	)
	if err != nil {
		return fmt.Errorf("insert tool call: %w", err)
	}
	return nil
}
