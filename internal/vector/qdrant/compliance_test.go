package qdrant

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/shaktimanai/shaktiman/internal/types"
	"github.com/shaktimanai/shaktiman/internal/vector/vectortest"
)

func TestQdrantCompliance(t *testing.T) {
	url := os.Getenv("SHAKTIMAN_TEST_QDRANT_URL")
	if url == "" {
		t.Skip("set SHAKTIMAN_TEST_QDRANT_URL to run Qdrant compliance tests")
	}

	vectortest.RunVectorStoreTests(t, func(t *testing.T, dims int) types.VectorStore {
		t.Helper()
		client := NewClient(url, os.Getenv("SHAKTIMAN_TEST_QDRANT_API_KEY"))

		// Use a unique collection per test to avoid interference.
		// Sanitize t.Name() — it contains "/" which breaks URL paths.
		safe := strings.NewReplacer("/", "_", " ", "_").Replace(t.Name())
		collection := fmt.Sprintf("shaktiman_test_%s", safe)

		store, err := NewQdrantStore(client, collection, dims)
		if err != nil {
			t.Fatalf("NewQdrantStore: %v", err)
		}

		// Cleanup: delete collection after test
		t.Cleanup(func() {
			store.Close()
			// Best-effort delete via raw HTTP — no API method needed
			client.doRequest(context.Background(), "DELETE", "/collections/"+collection, nil)
		})

		return store
	})
}
