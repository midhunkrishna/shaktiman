package qdrant

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// newTestServer creates an httptest server with a handler map.
// The handler receives the decoded request body (if any) and returns
// (statusCode, responseBody).
func newTestServer(t *testing.T, handlers map[string]http.HandlerFunc) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := r.Method + " " + r.URL.Path
		// Also try with query string for wait=true endpoints
		keyWithQuery := r.Method + " " + r.URL.RequestURI()
		h, ok := handlers[keyWithQuery]
		if !ok {
			h, ok = handlers[key]
		}
		if !ok {
			t.Logf("unhandled request: %s %s", r.Method, r.URL.RequestURI())
			http.NotFound(w, r)
			return
		}
		h(w, r)
	}))
}

func okEnvelope(t *testing.T, w http.ResponseWriter, result any) {
	t.Helper()
	data, _ := json.Marshal(result)
	resp := apiResponse{Status: "ok", Result: data}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func TestClient_Health(t *testing.T) {
	srv := newTestServer(t, map[string]http.HandlerFunc{
		"GET /": func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte(`{"title":"qdrant","version":"1.8.0"}`))
		},
	})
	defer srv.Close()

	c := NewClient(srv.URL, "")
	if err := c.Health(context.Background()); err != nil {
		t.Fatalf("Health: %v", err)
	}
}

func TestClient_Health_Unreachable(t *testing.T) {
	c := NewClient("http://127.0.0.1:1", "")
	err := c.Health(context.Background())
	if err == nil {
		t.Fatal("expected error for unreachable server")
	}
}

func TestClient_CreateCollection(t *testing.T) {
	var receivedBody CollectionConfig
	srv := newTestServer(t, map[string]http.HandlerFunc{
		"PUT /collections/test_col": func(w http.ResponseWriter, r *http.Request) {
			json.NewDecoder(r.Body).Decode(&receivedBody)
			okEnvelope(t, w, true)
		},
	})
	defer srv.Close()

	c := NewClient(srv.URL, "")
	err := c.CreateCollection(context.Background(), "test_col", 768)
	if err != nil {
		t.Fatalf("CreateCollection: %v", err)
	}
	if receivedBody.Vectors.Size != 768 {
		t.Errorf("dims = %d, want 768", receivedBody.Vectors.Size)
	}
	if receivedBody.Vectors.Distance != "Cosine" {
		t.Errorf("distance = %q, want Cosine", receivedBody.Vectors.Distance)
	}
}

func TestClient_GetCollection(t *testing.T) {
	srv := newTestServer(t, map[string]http.HandlerFunc{
		"GET /collections/my_col": func(w http.ResponseWriter, r *http.Request) {
			info := CollectionInfo{
				Status:       "green",
				PointsCount:  42,
				VectorsCount: 42,
				Config: CollectionInfoConfig{
					Params: CollectionInfoParams{
						Vectors: VectorsConfig{Size: 384, Distance: "Cosine"},
					},
				},
			}
			okEnvelope(t, w, info)
		},
	})
	defer srv.Close()

	c := NewClient(srv.URL, "")
	info, err := c.GetCollection(context.Background(), "my_col")
	if err != nil {
		t.Fatalf("GetCollection: %v", err)
	}
	if info.PointsCount != 42 {
		t.Errorf("PointsCount = %d, want 42", info.PointsCount)
	}
	if info.Config.Params.Vectors.Size != 384 {
		t.Errorf("dims = %d, want 384", info.Config.Params.Vectors.Size)
	}
}

func TestClient_GetCollection_NotFound(t *testing.T) {
	srv := newTestServer(t, map[string]http.HandlerFunc{
		"GET /collections/missing": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNotFound)
			w.Write([]byte(`{"status":{"error":"not found"}}`))
		},
	})
	defer srv.Close()

	c := NewClient(srv.URL, "")
	_, err := c.GetCollection(context.Background(), "missing")
	if err == nil {
		t.Fatal("expected error for missing collection")
	}
}

func TestClient_UpsertPoints(t *testing.T) {
	var receivedPoints struct {
		Points []Point `json:"points"`
	}
	srv := newTestServer(t, map[string]http.HandlerFunc{
		"PUT /collections/col/points?wait=true": func(w http.ResponseWriter, r *http.Request) {
			json.NewDecoder(r.Body).Decode(&receivedPoints)
			okEnvelope(t, w, struct {
				Status string `json:"status"`
			}{Status: "completed"})
		},
	})
	defer srv.Close()

	c := NewClient(srv.URL, "")
	err := c.UpsertPoints(context.Background(), "col", []Point{
		{ID: 1, Vector: []float32{0.1, 0.2}},
		{ID: 2, Vector: []float32{0.3, 0.4}},
	})
	if err != nil {
		t.Fatalf("UpsertPoints: %v", err)
	}
	if len(receivedPoints.Points) != 2 {
		t.Errorf("received %d points, want 2", len(receivedPoints.Points))
	}
}

func TestClient_SearchPoints(t *testing.T) {
	srv := newTestServer(t, map[string]http.HandlerFunc{
		"POST /collections/col/points/search": func(w http.ResponseWriter, r *http.Request) {
			results := []SearchResult{
				{ID: 10, Score: 0.95},
				{ID: 20, Score: 0.80},
			}
			okEnvelope(t, w, results)
		},
	})
	defer srv.Close()

	c := NewClient(srv.URL, "")
	results, err := c.SearchPoints(context.Background(), "col", SearchRequest{
		Vector: []float32{1, 0, 0},
		Limit:  2,
	})
	if err != nil {
		t.Fatalf("SearchPoints: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("got %d results, want 2", len(results))
	}
	if results[0].ID != 10 || results[0].Score != 0.95 {
		t.Errorf("result[0] = %+v", results[0])
	}
}

func TestClient_DeletePoints(t *testing.T) {
	var receivedBody struct {
		Points []int64 `json:"points"`
	}
	srv := newTestServer(t, map[string]http.HandlerFunc{
		"POST /collections/col/points/delete?wait=true": func(w http.ResponseWriter, r *http.Request) {
			json.NewDecoder(r.Body).Decode(&receivedBody)
			okEnvelope(t, w, struct {
				Status string `json:"status"`
			}{Status: "completed"})
		},
	})
	defer srv.Close()

	c := NewClient(srv.URL, "")
	err := c.DeletePoints(context.Background(), "col", []int64{1, 2, 3})
	if err != nil {
		t.Fatalf("DeletePoints: %v", err)
	}
	if len(receivedBody.Points) != 3 {
		t.Errorf("deleted %d points, want 3", len(receivedBody.Points))
	}
}

func TestClient_ScrollPoints(t *testing.T) {
	srv := newTestServer(t, map[string]http.HandlerFunc{
		"POST /collections/col/points/scroll": func(w http.ResponseWriter, r *http.Request) {
			resp := ScrollResponse{
				Points: []ScrollPoint{
					{ID: 1},
					{ID: 2},
				},
			}
			okEnvelope(t, w, resp)
		},
	})
	defer srv.Close()

	c := NewClient(srv.URL, "")
	resp, err := c.ScrollPoints(context.Background(), "col", ScrollRequest{Limit: 10})
	if err != nil {
		t.Fatalf("ScrollPoints: %v", err)
	}
	if len(resp.Points) != 2 {
		t.Errorf("got %d points, want 2", len(resp.Points))
	}
}

func TestClient_GetPoints(t *testing.T) {
	srv := newTestServer(t, map[string]http.HandlerFunc{
		"POST /collections/col/points": func(w http.ResponseWriter, r *http.Request) {
			points := []ScrollPoint{{ID: 42}}
			okEnvelope(t, w, points)
		},
	})
	defer srv.Close()

	c := NewClient(srv.URL, "")
	points, err := c.GetPoints(context.Background(), "col", []int64{42})
	if err != nil {
		t.Fatalf("GetPoints: %v", err)
	}
	if len(points) != 1 || points[0].ID != 42 {
		t.Errorf("unexpected points: %+v", points)
	}
}

func TestClient_CountPoints(t *testing.T) {
	srv := newTestServer(t, map[string]http.HandlerFunc{
		"POST /collections/col/points/count": func(w http.ResponseWriter, r *http.Request) {
			okEnvelope(t, w, struct {
				Count int `json:"count"`
			}{Count: 99})
		},
	})
	defer srv.Close()

	c := NewClient(srv.URL, "")
	count, err := c.CountPoints(context.Background(), "col")
	if err != nil {
		t.Fatalf("CountPoints: %v", err)
	}
	if count != 99 {
		t.Errorf("count = %d, want 99", count)
	}
}

func TestClient_APIKey_Header(t *testing.T) {
	var gotKey string
	srv := newTestServer(t, map[string]http.HandlerFunc{
		"GET /": func(w http.ResponseWriter, r *http.Request) {
			gotKey = r.Header.Get("api-key")
			w.Write([]byte(`{"title":"qdrant"}`))
		},
	})
	defer srv.Close()

	c := NewClient(srv.URL, "secret-key-123")
	c.Health(context.Background())
	if gotKey != "secret-key-123" {
		t.Errorf("api-key header = %q, want %q", gotKey, "secret-key-123")
	}
}

func TestClient_ErrorResponse(t *testing.T) {
	srv := newTestServer(t, map[string]http.HandlerFunc{
		"GET /collections/bad": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte(`{"status":{"error":"internal error"}}`))
		},
	})
	defer srv.Close()

	c := NewClient(srv.URL, "")
	_, err := c.GetCollection(context.Background(), "bad")
	if err == nil {
		t.Fatal("expected error for 500 response")
	}
}

func TestClient_NonOKStatus(t *testing.T) {
	srv := newTestServer(t, map[string]http.HandlerFunc{
		"POST /collections/col/points/count": func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(apiResponse{Status: "error", Result: json.RawMessage(`"something went wrong"`)})
		},
	})
	defer srv.Close()

	c := NewClient(srv.URL, "")
	_, err := c.CountPoints(context.Background(), "col")
	if err == nil {
		t.Fatal("expected error for non-ok status")
	}
}
