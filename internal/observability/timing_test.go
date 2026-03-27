package observability

import (
	"bytes"
	"log/slog"
	"testing"
)

func TestOp(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))

	done := Op(logger, "test_op", "key", "val")
	done()

	out := buf.String()
	if !bytes.Contains([]byte(out), []byte("test_op started")) {
		t.Errorf("expected 'test_op started' in log output, got: %s", out)
	}
	if !bytes.Contains([]byte(out), []byte("test_op completed")) {
		t.Errorf("expected 'test_op completed' in log output, got: %s", out)
	}
	if !bytes.Contains([]byte(out), []byte("duration_ms")) {
		t.Errorf("expected 'duration_ms' in log output, got: %s", out)
	}
	if !bytes.Contains([]byte(out), []byte("key=val")) {
		t.Errorf("expected 'key=val' in log output, got: %s", out)
	}
}
