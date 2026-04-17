package qdrant

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

	"github.com/shaktimanai/shaktiman/internal/types"
)

// maxBatchSize is the maximum number of points per upsert/delete request.
const maxBatchSize = 100

// Store implements types.VectorStore backed by a Qdrant instance.
type Store struct {
	client     *Client
	collection string
	dims       int
	projectID  int64

	mu     sync.Mutex
	closed bool
}

// Compile-time check that Store implements VectorStore.
var _ types.VectorStore = (*Store)(nil)

// NewStore creates a Store and ensures the collection exists
// with the correct dimensionality.
func NewStore(client *Client, collection string, dims int, projectID int64) (*Store, error) {
	ctx := context.Background()

	// Try to get existing collection first.
	info, err := client.GetCollection(ctx, collection)
	if err != nil {
		// Collection doesn't exist — create it.
		if createErr := client.CreateCollection(ctx, collection, dims); createErr != nil {
			return nil, fmt.Errorf("create collection %q: %w", collection, createErr)
		}
	} else {
		// Collection exists — verify dimensionality matches.
		if info.Config.Params.Vectors.Size != dims {
			return nil, fmt.Errorf("collection %q has dims=%d, expected %d",
				collection, info.Config.Params.Vectors.Size, dims)
		}
	}

	// Best-effort payload index for project_id filtering.
	if projectID != 0 {
		if err := client.CreatePayloadIndex(ctx, collection, "project_id", "integer"); err != nil {
			slog.Warn("qdrant: could not create project_id payload index",
				"collection", collection, "err", err)
		}
	}

	return &Store{
		client:     client,
		collection: collection,
		dims:       dims,
		projectID:  projectID,
	}, nil
}

// projectFilter returns a Filter scoped to this store's projectID,
// or nil if projectID is 0 (unscoped).
func (s *Store) projectFilter() *Filter {
	if s.projectID == 0 {
		return nil
	}
	return &Filter{
		Must: []FilterCondition{
			{Key: "project_id", Match: &MatchValue{Value: s.projectID}},
		},
	}
}

// Search returns the topK most similar vectors by cosine similarity.
func (s *Store) Search(ctx context.Context, query []float32, topK int) ([]types.VectorResult, error) {
	if topK <= 0 {
		return nil, nil
	}

	results, err := s.client.SearchPoints(ctx, s.collection, SearchRequest{
		Vector: query,
		Limit:  topK,
		Filter: s.projectFilter(),
	})
	if err != nil {
		return nil, fmt.Errorf("qdrant search: %w", err)
	}

	out := make([]types.VectorResult, len(results))
	for i, r := range results {
		out[i] = types.VectorResult{
			ChunkID: r.ID,
			Score:   r.Score,
		}
	}
	return out, nil
}

// Upsert inserts or replaces a single vector.
func (s *Store) Upsert(ctx context.Context, chunkID int64, vector []float32) error {
	p := Point{ID: chunkID, Vector: vector}
	if s.projectID != 0 {
		p.Payload = map[string]any{"project_id": s.projectID}
	}
	return s.client.UpsertPoints(ctx, s.collection, []Point{p})
}

// UpsertBatch inserts multiple vectors, chunked to maxBatchSize per request.
func (s *Store) UpsertBatch(ctx context.Context, chunkIDs []int64, vectors [][]float32) error {
	if len(chunkIDs) != len(vectors) {
		return fmt.Errorf("chunkIDs len %d != vectors len %d", len(chunkIDs), len(vectors))
	}

	for i := 0; i < len(chunkIDs); i += maxBatchSize {
		end := i + maxBatchSize
		if end > len(chunkIDs) {
			end = len(chunkIDs)
		}

		points := make([]Point, end-i)
		for j := i; j < end; j++ {
			p := Point{ID: chunkIDs[j], Vector: vectors[j]}
			if s.projectID != 0 {
				p.Payload = map[string]any{"project_id": s.projectID}
			}
			points[j-i] = p
		}

		if err := s.client.UpsertPoints(ctx, s.collection, points); err != nil {
			return fmt.Errorf("upsert batch [%d:%d]: %w", i, end, err)
		}
	}
	return nil
}

// Delete removes vectors by chunk IDs, chunked to maxBatchSize per request.
func (s *Store) Delete(ctx context.Context, chunkIDs []int64) error {
	if len(chunkIDs) == 0 {
		return nil
	}

	for i := 0; i < len(chunkIDs); i += maxBatchSize {
		end := i + maxBatchSize
		if end > len(chunkIDs) {
			end = len(chunkIDs)
		}

		if err := s.client.DeletePoints(ctx, s.collection, chunkIDs[i:end]); err != nil {
			return fmt.Errorf("delete batch [%d:%d]: %w", i, end, err)
		}
	}
	return nil
}

// PurgeAll deletes all vectors for this project. When projectID is set,
// only this project's points are removed via filter-based delete.
// When unscoped (projectID=0), falls back to collection drop+recreate.
func (s *Store) PurgeAll(ctx context.Context) error {
	f := s.projectFilter()
	if f == nil {
		// Unscoped: drop and recreate collection (original behavior).
		if err := s.client.DeleteCollection(ctx, s.collection); err != nil {
			return fmt.Errorf("delete collection: %w", err)
		}
		return s.client.CreateCollection(ctx, s.collection, s.dims)
	}
	return s.client.DeletePointsByFilter(ctx, s.collection, *f)
}

// Has returns true if a vector exists for the given chunk ID.
func (s *Store) Has(ctx context.Context, chunkID int64) (bool, error) {
	points, err := s.client.GetPoints(ctx, s.collection, []int64{chunkID})
	if err != nil {
		return false, fmt.Errorf("qdrant has: %w", err)
	}
	return len(points) > 0, nil
}

// Count returns the exact number of points for this project.
func (s *Store) Count(ctx context.Context) (int, error) {
	return s.client.CountPoints(ctx, s.collection, s.projectFilter())
}

// Close marks the store as closed. The underlying HTTP client is not pooled
// per-store, so there is no connection to release.
func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	slog.Info("qdrant store closed", "collection", s.collection, "project_id", s.projectID)
	return nil
}

// Healthy returns true if Qdrant is reachable.
func (s *Store) Healthy(ctx context.Context) bool {
	return s.client.Health(ctx) == nil
}
