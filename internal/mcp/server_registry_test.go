//go:build sqlite_fts5

package mcp

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/shaktimanai/shaktiman/internal/types"
)

// TestToolRegistry_NamesAreUnique guards against accidental duplicates when a
// new entry is appended to toolRegistry().
func TestToolRegistry_NamesAreUnique(t *testing.T) {
	t.Parallel()
	seen := map[string]bool{}
	for _, e := range toolRegistry() {
		if seen[e.name] {
			t.Errorf("duplicate tool name in registry: %q", e.name)
		}
		seen[e.name] = true
	}
}

// TestToolRegistry_ContextGate verifies the context tool's gate predicate
// matches cfg.ContextEnabled. Only the context entry is gated today; flag any
// future entry that adds an unexpected gate so reviewers notice.
func TestToolRegistry_ContextGate(t *testing.T) {
	t.Parallel()
	for _, e := range toolRegistry() {
		switch e.name {
		case "context":
			if e.gate == nil {
				t.Fatal("context tool must have a gate")
			}
			if e.gate(types.Config{ContextEnabled: false}) {
				t.Error("context gate must reject ContextEnabled=false")
			}
			if !e.gate(types.Config{ContextEnabled: true}) {
				t.Error("context gate must accept ContextEnabled=true")
			}
		default:
			if e.gate != nil {
				t.Errorf("unexpected gate on tool %q", e.name)
			}
		}
	}
}

// TestJSONResult_Success verifies jsonResult marshals payload and tags the
// result with resultCount via the metrics slot.
func TestJSONResult_Success(t *testing.T) {
	t.Parallel()
	slot := 0
	ctx := context.WithValue(context.Background(), resultCountKey{}, &slot)

	r := jsonResult(ctx, map[string]int{"a": 1}, 42, "marshal results: ")
	if r == nil {
		t.Fatal("nil result")
	}
	if r.IsError {
		t.Fatalf("unexpected error result: %+v", r)
	}
	if slot != 42 {
		t.Errorf("resultCount slot = %d, want 42", slot)
	}
	// Verify the payload round-trips so callers know the marshalled form is
	// the JSON encoding of the input, not some sanitized variant.
	textOut := extractText(t, r)
	var got map[string]int
	if err := json.Unmarshal([]byte(textOut), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got["a"] != 1 {
		t.Errorf("payload mismatch: %v", got)
	}
}

// TestJSONResult_MarshalFailureSanitized verifies that marshal errors are
// wrapped with the supplied label and routed through sanitizeError. The
// payload here is a chan, which json.Marshal cannot encode.
func TestJSONResult_MarshalFailureSanitized(t *testing.T) {
	t.Parallel()
	r := jsonResult(context.Background(), make(chan int), 0, "marshal x: ")
	if r == nil || !r.IsError {
		t.Fatalf("expected error result, got %+v", r)
	}
	text := extractText(t, r)
	if text == "" || !contains(text, "marshal x: ") {
		t.Errorf("expected marshal label in error text, got %q", text)
	}
}

// extractText pulls the text payload out of a CallToolResult. Mirrors the
// hand-rolled extraction used in tools_test.go.
func extractText(t *testing.T, r interface{}) string {
	t.Helper()
	type contentBag struct {
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
	}
	data, err := json.Marshal(r)
	if err != nil {
		t.Fatalf("re-marshal: %v", err)
	}
	var b contentBag
	if err := json.Unmarshal(data, &b); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(b.Content) == 0 {
		return ""
	}
	return b.Content[0].Text
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
