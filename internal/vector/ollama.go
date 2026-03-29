package vector

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"
)

// OllamaClient calls the Ollama embedding API over HTTP.
type OllamaClient struct {
	baseURL string
	model   string
	client  *http.Client
	logger  *slog.Logger
}

// OllamaClientInput configures an OllamaClient.
type OllamaClientInput struct {
	BaseURL string
	Model   string
	Timeout time.Duration
}

// NewOllamaClient creates an Ollama HTTP client.
func NewOllamaClient(input OllamaClientInput) *OllamaClient {
	timeout := input.Timeout
	if timeout == 0 {
		timeout = 120 * time.Second
	}
	return &OllamaClient{
		baseURL: input.BaseURL,
		model:   input.Model,
		client:  &http.Client{Timeout: timeout},
		logger:  slog.Default().With("component", "ollama"),
	}
}

type ollamaEmbedRequest struct {
	Model    string `json:"model"`
	Input    any    `json:"input"`    // string or []string
	Truncate bool   `json:"truncate"` // truncate input to fit model context window
}

// maxSafeEmbedChars is a conservative client-side limit for warning about
// oversized inputs. nomic-embed-text has 8192 token context; ~4 chars/token
// = ~32K chars. We warn at 28K to leave headroom.
const maxSafeEmbedChars = 28000

type ollamaEmbedResponse struct {
	Embeddings [][]float32 `json:"embeddings"`
}

// Embed produces a single embedding vector for the given text.
func (c *OllamaClient) Embed(ctx context.Context, text string) ([]float32, error) {
	vecs, err := c.EmbedBatch(ctx, []string{text})
	if err != nil {
		return nil, err
	}
	if len(vecs) == 0 {
		return nil, fmt.Errorf("ollama returned 0 embeddings for 1 input")
	}
	return vecs[0], nil
}

// EmbedBatch produces embedding vectors for multiple texts in one HTTP call.
// Sends truncate=true so Ollama clips oversized inputs to the model's context
// window instead of returning an error.
func (c *OllamaClient) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	for i, t := range texts {
		if len(t) > maxSafeEmbedChars {
			c.logger.Warn("oversized input will be truncated by Ollama",
				"index", i, "chars", len(t), "limit", maxSafeEmbedChars)
		}
	}

	body, err := json.Marshal(ollamaEmbedRequest{Model: c.model, Input: texts, Truncate: true})
	if err != nil {
		return nil, fmt.Errorf("marshal embed request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/api/embed", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create embed request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("embed HTTP call: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, &EmbedHTTPError{StatusCode: resp.StatusCode, Body: string(respBody)}
	}

	const maxEmbedResponseSize = 50 * 1024 * 1024 // 50MB
	var result ollamaEmbedResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxEmbedResponseSize)).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode embed response: %w", err)
	}
	if len(result.Embeddings) != len(texts) {
		return nil, fmt.Errorf("ollama returned %d embeddings for %d inputs",
			len(result.Embeddings), len(texts))
	}
	return result.Embeddings, nil
}

// Healthy returns true if the Ollama API is reachable.
func (c *OllamaClient) Healthy(ctx context.Context) bool {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/", nil)
	if err != nil {
		return false
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}
