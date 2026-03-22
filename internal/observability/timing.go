// Package observability provides lightweight timing and logging helpers.
package observability

import (
	"log/slog"
	"time"
)

// Op logs the start of an operation and returns a function that logs its duration.
// Usage: defer Op(logger, "cold_index", "root", root)()
func Op(logger *slog.Logger, op string, attrs ...any) func() {
	start := time.Now()
	logger.Info(op+" started", attrs...)
	return func() {
		all := append([]any{"duration_ms", time.Since(start).Milliseconds()}, attrs...)
		logger.Info(op+" completed", all...)
	}
}
