package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	mcpsdk "github.com/mark3labs/mcp-go/mcp"

	"github.com/shaktimanai/shaktiman/internal/observability"
	"github.com/shaktimanai/shaktiman/internal/types"
)

// mcpHandlerTimeout bounds a single MCP tool call. Long enough for a
// cold-cache hybrid search to complete (Ollama embed + DB read +
// re-rank can comfortably take tens of seconds); short enough that a
// genuinely wedged handler doesn't strand the daemon.
const mcpHandlerTimeout = 2 * time.Minute

// maxResponseBytes caps the marshaled response size returned by any
// MCP tool. The MCP framing layer doesn't enforce a frame-size cap
// directly, and oversized JSON has surfaced to clients as opaque
// "transport closed" errors. Capping at the handler boundary lets us
// fail loudly with a structured tool error instead.
const maxResponseBytes = 8 * 1024 * 1024 // 8 MiB

type resultCountKey struct{}

// withResultCount records the result count for the current handler
// invocation in the context-scoped slot installed by withMetrics. It
// returns result unchanged so handler call sites stay terse:
//
//	return withResultCount(ctx, mcpsdk.NewToolResultText(...), len(rows)), nil
//
// If ctx has no slot (e.g. handler called outside withMetrics in a
// test), the call is a no-op.
func withResultCount(ctx context.Context, result *mcpsdk.CallToolResult, count int) *mcpsdk.CallToolResult {
	if slot, ok := ctx.Value(resultCountKey{}).(*int); ok {
		*slot = count
	}
	return result
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

// withMetrics wraps an MCP tool handler with metrics recording,
// structured logging, a per-call timeout, a response-size cap, and a
// context-scoped result-count slot. The slot replaces the previous
// pointer-keyed sync.Map carrier, which leaked entries on handler-
// returns-nil-result paths and was theoretically vulnerable to
// pointer reuse.
func withMetrics(name string, recorder *MetricsRecorder, h handlerFunc) handlerFunc {
	logger := slog.Default().With("component", "mcp")
	return func(ctx context.Context, req mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
		start := time.Now()

		argsJSON, _ := json.Marshal(req.GetArguments())
		argsBytes := len(argsJSON)

		// Per-call timeout. We don't propagate the deadline to the parent
		// because the MCP server may dispatch the next request on the
		// same parent ctx.
		callCtx, cancel := context.WithTimeout(ctx, mcpHandlerTimeout)
		defer cancel()

		var resultCount int
		callCtx = context.WithValue(callCtx, resultCountKey{}, &resultCount)

		result, err := h(callCtx, req)

		// Response-size guard. An oversized JSON payload reaches the MCP
		// framing layer as a single write and has historically surfaced
		// to clients as opaque transport closures. Replace it with a
		// structured tool error so the operator sees the cause.
		respBytes := responseBytes(result)
		if respBytes > maxResponseBytes {
			err = nil
			result = mcpsdk.NewToolResultError(fmt.Sprintf(
				"response size %d bytes exceeds cap %d; refine the query (smaller scope, narrower path, fewer max results)",
				respBytes, maxResponseBytes))
			respBytes = responseBytes(result)
		}

		duration := time.Since(start)
		isErr := err != nil || (result != nil && result.IsError)
		tokensEst := respBytes / 4

		// In-memory metrics for `shaktiman stats`. Names are scoped by
		// tool so a hot tool's latency doesn't drown out the rest.
		m := observability.ScopeOnDefault("mcp.tool_call." + name)
		m.Inc("count")
		m.Observe("latency_ms", float64(duration.Milliseconds()))
		m.Observe("response_bytes", float64(respBytes))
		if isErr {
			m.Inc("error_count")
		}

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
