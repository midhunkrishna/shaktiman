package mcp

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync"
	"time"

	mcpsdk "github.com/mark3labs/mcp-go/mcp"

	"github.com/shaktimanai/shaktiman/internal/types"
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

// ToolCallRecord is an alias for types.ToolCallRecord.
type ToolCallRecord = types.ToolCallRecord

// MetricsRecorderInput configures a MetricsRecorder.
type MetricsRecorderInput struct {
	Writer    types.MetricsWriter
	SessionID string
	Logger    *slog.Logger
}

// MetricsRecorder persists MCP tool call metrics asynchronously via MetricsWriter.
// Records are buffered in a channel and batch-inserted by a dedicated goroutine.
type MetricsRecorder struct {
	writer    types.MetricsWriter
	sessionID string
	logger    *slog.Logger
	ch        chan ToolCallRecord
}

const metricsChannelCap = 256
const metricsBatchSize = 10

// NewMetricsRecorder creates a recorder. Call Run() in a goroutine to start processing.
func NewMetricsRecorder(input MetricsRecorderInput) *MetricsRecorder {
	return &MetricsRecorder{
		writer:    input.Writer,
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
			// Drain remaining records without closing the channel.
			// Record() may still be called by in-flight MCP handlers;
			// closing would cause a send-on-closed-channel panic.
			// The channel is unreferenced after Run returns and will be GC'd.
			deadline := time.After(time.Second)
		drain:
			for {
				select {
				case rec := <-r.ch:
					batch = append(batch, rec)
				case <-deadline:
					break drain
				}
			}
			if len(batch) > 0 {
				r.flush(batch)
			}
			return
		}
	}
}

// flush batch-inserts records via MetricsWriter.
func (r *MetricsRecorder) flush(batch []ToolCallRecord) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := r.writer.RecordToolCalls(ctx, batch); err != nil {
		r.logger.Error("metrics flush", "err", err, "count", len(batch))
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
func InsertToolCall(w types.MetricsWriter, rec ToolCallRecord) error {
	return w.RecordToolCalls(context.Background(), []ToolCallRecord{rec})
}
