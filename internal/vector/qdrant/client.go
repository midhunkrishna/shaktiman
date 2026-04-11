// Package qdrant provides a VectorStore backed by a Qdrant instance via REST API.
package qdrant

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Client is a thin HTTP client for the Qdrant REST API.
type Client struct {
	baseURL    string
	apiKey     string
	httpClient *http.Client
}

// NewClient creates a Qdrant REST client.
func NewClient(baseURL, apiKey string) *Client {
	return &Client{
		baseURL: baseURL,
		apiKey:  apiKey,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// ── Request / Response types ──

// Point is a Qdrant point with an integer ID, vector, and optional payload.
type Point struct {
	ID      int64              `json:"id"`
	Vector  []float32          `json:"vector"`
	Payload map[string]any     `json:"payload,omitempty"`
}

// SearchRequest is the body for POST /collections/{name}/points/search.
type SearchRequest struct {
	Vector         []float32 `json:"vector"`
	Limit          int       `json:"limit"`
	WithPayload    bool      `json:"with_payload"`
	ScoreThreshold *float64  `json:"score_threshold,omitempty"`
	Filter         *Filter   `json:"filter,omitempty"`
}

// SearchResult is a single scored point returned by search.
type SearchResult struct {
	ID      int64          `json:"id"`
	Version int            `json:"version"`
	Score   float64        `json:"score"`
	Payload map[string]any `json:"payload,omitempty"`
}

// ScrollRequest is the body for POST /collections/{name}/points/scroll.
type ScrollRequest struct {
	Limit       int         `json:"limit"`
	WithPayload bool        `json:"with_payload"`
	WithVector  bool        `json:"with_vector"`
	Filter      *Filter     `json:"filter,omitempty"`
	Offset      *int64      `json:"offset,omitempty"`
}

// ScrollResponse is the response from scroll.
type ScrollResponse struct {
	Points     []ScrollPoint `json:"points"`
	NextPageOffset *int64    `json:"next_page_offset"`
}

// ScrollPoint is a point returned by scroll.
type ScrollPoint struct {
	ID      int64          `json:"id"`
	Payload map[string]any `json:"payload,omitempty"`
	Vector  []float32      `json:"vector,omitempty"`
}

// Filter for point selection.
type Filter struct {
	Must []FilterCondition `json:"must,omitempty"`
}

// FilterCondition is a single filter clause.
// For ID filtering use HasID; for payload matching use Key+Match.
type FilterCondition struct {
	HasID *HasIDCondition `json:"has_id,omitempty"`
	Key   string         `json:"key,omitempty"`
	Match *MatchValue    `json:"match,omitempty"`
}

// HasIDCondition matches points by ID.
type HasIDCondition struct {
	HasID []int64 `json:"has_id"`
}

// MatchValue matches a payload field against an exact value.
type MatchValue struct {
	Value any `json:"value"`
}

// DeletePointsRequest is the body for POST /collections/{name}/points/delete.
type DeletePointsRequest struct {
	Points []int64 `json:"points"`
}

// CollectionConfig is the body for PUT /collections/{name}.
type CollectionConfig struct {
	Vectors VectorsConfig `json:"vectors"`
}

// VectorsConfig defines vector parameters for a collection.
type VectorsConfig struct {
	Size     int    `json:"size"`
	Distance string `json:"distance"` // "Cosine", "Euclid", "Dot"
}

// CollectionInfo is returned by GET /collections/{name}.
type CollectionInfo struct {
	Status        string        `json:"status"`
	VectorsCount  int           `json:"vectors_count"`
	PointsCount   int           `json:"points_count"`
	Config        CollectionInfoConfig `json:"config"`
}

// CollectionInfoConfig wraps vector params in collection info response.
type CollectionInfoConfig struct {
	Params CollectionInfoParams `json:"params"`
}

// CollectionInfoParams holds vector config from collection info.
type CollectionInfoParams struct {
	Vectors VectorsConfig `json:"vectors"`
}

// apiResponse wraps the standard Qdrant envelope: {"status":"ok","result":...}
type apiResponse struct {
	Status string          `json:"status"`
	Result json.RawMessage `json:"result"`
}

// ── API methods ──

// Health checks if Qdrant is reachable (GET /).
func (c *Client) Health(ctx context.Context) error {
	_, err := c.doRequest(ctx, http.MethodGet, "/", nil)
	return err
}

// CreateCollection creates a collection with the given vector config.
// Returns nil if the collection already exists.
func (c *Client) CreateCollection(ctx context.Context, name string, dims int) error {
	body := CollectionConfig{
		Vectors: VectorsConfig{Size: dims, Distance: "Cosine"},
	}
	_, err := c.doRequest(ctx, http.MethodPut, "/collections/"+name, body)
	return err
}

// GetCollection returns collection info, or an error if it doesn't exist.
func (c *Client) GetCollection(ctx context.Context, name string) (*CollectionInfo, error) {
	data, err := c.doRequest(ctx, http.MethodGet, "/collections/"+name, nil)
	if err != nil {
		return nil, err
	}
	var info CollectionInfo
	if err := json.Unmarshal(data, &info); err != nil {
		return nil, fmt.Errorf("unmarshal collection info: %w", err)
	}
	return &info, nil
}

// DeleteCollection deletes a collection by name.
func (c *Client) DeleteCollection(ctx context.Context, name string) error {
	_, err := c.doRequest(ctx, http.MethodDelete, "/collections/"+name, nil)
	return err
}

// CreatePayloadIndex creates an index on a payload field for faster filtering.
func (c *Client) CreatePayloadIndex(ctx context.Context, collection, fieldName, fieldSchema string) error {
	body := struct {
		FieldName   string `json:"field_name"`
		FieldSchema string `json:"field_schema"`
	}{FieldName: fieldName, FieldSchema: fieldSchema}
	_, err := c.doRequest(ctx, http.MethodPut, "/collections/"+collection+"/index?wait=true", body)
	return err
}

// UpsertPoints inserts or updates points in a collection.
func (c *Client) UpsertPoints(ctx context.Context, collection string, points []Point) error {
	body := struct {
		Points []Point `json:"points"`
	}{Points: points}
	_, err := c.doRequest(ctx, http.MethodPut, "/collections/"+collection+"/points?wait=true", body)
	return err
}

// SearchPoints performs a vector similarity search.
func (c *Client) SearchPoints(ctx context.Context, collection string, req SearchRequest) ([]SearchResult, error) {
	data, err := c.doRequest(ctx, http.MethodPost, "/collections/"+collection+"/points/search", req)
	if err != nil {
		return nil, err
	}
	var results []SearchResult
	if err := json.Unmarshal(data, &results); err != nil {
		return nil, fmt.Errorf("unmarshal search results: %w", err)
	}
	return results, nil
}

// DeletePoints removes points by their IDs.
func (c *Client) DeletePoints(ctx context.Context, collection string, ids []int64) error {
	body := struct {
		Points []int64 `json:"points"`
	}{Points: ids}
	_, err := c.doRequest(ctx, http.MethodPost, "/collections/"+collection+"/points/delete?wait=true", body)
	return err
}

// DeletePointsByFilter removes all points matching the filter.
func (c *Client) DeletePointsByFilter(ctx context.Context, collection string, filter Filter) error {
	body := struct {
		Filter Filter `json:"filter"`
	}{Filter: filter}
	_, err := c.doRequest(ctx, http.MethodPost, "/collections/"+collection+"/points/delete?wait=true", body)
	return err
}

// ScrollPoints lists points with optional filtering and pagination.
func (c *Client) ScrollPoints(ctx context.Context, collection string, req ScrollRequest) (*ScrollResponse, error) {
	data, err := c.doRequest(ctx, http.MethodPost, "/collections/"+collection+"/points/scroll", req)
	if err != nil {
		return nil, err
	}
	var resp ScrollResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("unmarshal scroll response: %w", err)
	}
	return &resp, nil
}

// GetPoints retrieves specific points by ID.
func (c *Client) GetPoints(ctx context.Context, collection string, ids []int64) ([]ScrollPoint, error) {
	body := struct {
		IDs         []int64 `json:"ids"`
		WithPayload bool    `json:"with_payload"`
		WithVector  bool    `json:"with_vector"`
	}{IDs: ids, WithPayload: false, WithVector: false}
	data, err := c.doRequest(ctx, http.MethodPost, "/collections/"+collection+"/points", body)
	if err != nil {
		return nil, err
	}
	var points []ScrollPoint
	if err := json.Unmarshal(data, &points); err != nil {
		return nil, fmt.Errorf("unmarshal points: %w", err)
	}
	return points, nil
}

// CountPoints returns the number of points in a collection, optionally filtered.
func (c *Client) CountPoints(ctx context.Context, collection string, filter *Filter) (int, error) {
	body := struct {
		Exact  bool    `json:"exact"`
		Filter *Filter `json:"filter,omitempty"`
	}{Exact: true, Filter: filter}
	data, err := c.doRequest(ctx, http.MethodPost, "/collections/"+collection+"/points/count", body)
	if err != nil {
		return 0, err
	}
	var result struct {
		Count int `json:"count"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return 0, fmt.Errorf("unmarshal count: %w", err)
	}
	return result.Count, nil
}

// doRequest executes an HTTP request against the Qdrant API.
func (c *Client) doRequest(ctx context.Context, method, path string, body any) (json.RawMessage, error) {
	var bodyReader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("marshal request body: %w", err)
		}
		bodyReader = bytes.NewReader(data)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.apiKey != "" {
		req.Header.Set("api-key", c.apiKey)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("qdrant request %s %s: %w", method, path, err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("qdrant %s %s returned %d: %s", method, path, resp.StatusCode, string(respBody))
	}

	// Health endpoint returns non-JSON; return raw bytes
	if path == "/" {
		return respBody, nil
	}

	var envelope apiResponse
	if err := json.Unmarshal(respBody, &envelope); err != nil {
		return nil, fmt.Errorf("unmarshal response envelope: %w", err)
	}
	if envelope.Status != "ok" {
		return nil, fmt.Errorf("qdrant error: status=%s body=%s", envelope.Status, string(respBody))
	}
	return envelope.Result, nil
}
