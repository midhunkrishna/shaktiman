//go:build qdrant

package testutil

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/shaktimanai/shaktiman/internal/types"
	"github.com/shaktimanai/shaktiman/internal/vector/qdrant"
)

func init() {
	extraVectorFactories["qdrant"] = newQdrantTestStore
}

func newQdrantTestStore(t *testing.T, dims int) types.VectorStore {
	t.Helper()

	url := os.Getenv("SHAKTIMAN_TEST_QDRANT_URL")
	if url == "" {
		t.Skip("SHAKTIMAN_TEST_QDRANT_URL not set")
	}
	apiKey := os.Getenv("SHAKTIMAN_TEST_QDRANT_API_KEY")
	client := qdrant.NewClient(url, apiKey)

	safe := strings.NewReplacer("/", "_", " ", "_").Replace(t.Name())
	collection := fmt.Sprintf("testutil_%s", safe)

	store, err := qdrant.NewQdrantStore(client, collection, dims, 1)
	if err != nil {
		t.Fatalf("NewQdrantStore: %v", err)
	}

	t.Cleanup(func() {
		store.Close()
		// Best-effort collection cleanup via raw HTTP DELETE.
		req, _ := http.NewRequestWithContext(context.Background(), "DELETE", url+"/collections/"+collection, nil)
		if apiKey != "" {
			req.Header.Set("api-key", apiKey)
		}
		resp, err := http.DefaultClient.Do(req)
		if err == nil {
			resp.Body.Close()
		}
	})
	return store
}
