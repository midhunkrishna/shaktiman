package vector

import (
	"errors"
	"fmt"

	"github.com/shaktimanai/shaktiman/internal/types"
)

// Sentinel errors for embedding operations.
var (
	ErrOllamaUnreachable = errors.New("ollama is unreachable")
	ErrCircuitOpen       = errors.New("circuit breaker is open")
	ErrPermanentEmbed    = errors.New("permanent embedding failure")
	errBatchShrunk       = errors.New("batch shrunk")
)

// EmbedHTTPError wraps non-200 responses from the embedding API with
// the HTTP status code for error classification (permanent vs transient).
type EmbedHTTPError struct {
	StatusCode int
	Body       string
}

func (e *EmbedHTTPError) Error() string {
	return fmt.Sprintf("ollama embed returned %d: %s", e.StatusCode, e.Body)
}

// Unwrap returns ErrPermanentEmbed for 4xx errors (client errors that retrying
// won't fix), nil for 5xx (transient server errors).
func (e *EmbedHTTPError) Unwrap() error {
	if e.StatusCode >= 400 && e.StatusCode < 500 {
		return ErrPermanentEmbed
	}
	return nil
}

// deferredJob tracks a chunk that failed transiently during Phase 1 and
// should be retried after all normal processing completes.
type deferredJob struct {
	job types.EmbedJob
}
