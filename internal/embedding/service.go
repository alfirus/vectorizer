package embedding

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/alfirus/vectorizer/internal/httpx"
)

// Service handles text-to-embedding conversion via external providers (Qwen3 1536d MRL).
type Service struct {
	baseURL    string
	apiKey     string
	model      string
	dimensions int
	retry      *httpx.Client // backpressure-aware client (timeouts + Retry-After)
}

// timeoutFromEnv reads EMBED_TIMEOUT_SECS (default 300s for slow local models).
func timeoutFromEnv() time.Duration {
	if v := os.Getenv("EMBED_TIMEOUT_SECS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return time.Duration(n) * time.Second
		}
	}
	return 5 * time.Minute
}

func retriesFromEnv() int {
	if v := os.Getenv("EMBED_MAX_RETRIES"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			return n
		}
	}
	return 2
}

func newRetryClient() *httpx.Client {
	return httpx.New(timeoutFromEnv(), retriesFromEnv())
}

func New(baseURL, apiKey, model string) *Service {
	return &Service{
		baseURL:    baseURL,
		apiKey:     apiKey,
		model:      model,
		dimensions: 1536,
		retry:      newRetryClient(),
	}
}

func NewWithDimensions(baseURL, apiKey, model string, dimensions int) *Service {
	if dimensions <= 0 {
		dimensions = 1536
	}
	return &Service{baseURL: baseURL, apiKey: apiKey, model: model, dimensions: dimensions, retry: newRetryClient()}
}

func (s *Service) Model() string { return s.model }
func (s *Service) Dimensions() int { return s.dimensions }
func (s *Service) SetModel(model string) { s.model = model }
func (s *Service) SetDimensions(d int) { if d > 0 { s.dimensions = d } }
func (s *Service) SetBaseURL(url string) { s.baseURL = url }

// EmbeddingResult is a single embedding vector.
type EmbeddingResult struct {
	Vector []float32 `json:"-"` // not serialized
}

// Embed sends texts to the provider and returns embeddings.
func (s *Service) Embed(texts []string) ([]EmbeddingResult, error) {
	if len(texts) == 0 {
		return nil, nil
	}

	body := map[string]interface{}{
		"model":      s.model,
		"input":      texts,
		"dimensions": s.dimensions,
	}

	resp, err := s.doJSON(http.MethodPost, "/embeddings", body)
	if err != nil {
		return nil, fmt.Errorf("embedding request: %w", err)
	}

	var result struct {
		Data []struct {
			Embedding []float64 `json:"embedding"`
		} `json:"data"`
	}
	if err := json.Unmarshal(resp, &result); err != nil {
		return nil, fmt.Errorf("parse embedding response: %w", err)
	}

	results := make([]EmbeddingResult, len(result.Data))
	for i, d := range result.Data {
		vec := make([]float32, len(d.Embedding))
		for j, v := range d.Embedding {
			vec[j] = float32(v)
		}
		results[i].Vector = vec
	}

	return results, nil
}

// EmbedSingle is a convenience wrapper for a single text.
func (s *Service) EmbedSingle(text string) ([]float32, error) {
	results, err := s.Embed([]string{text})
	if err != nil {
		return nil, err
	}
	if len(results) == 0 || len(results[0].Vector) == 0 {
		return nil, fmt.Errorf("empty embedding result")
	}
	return results[0].Vector, nil
}

// doJSON performs an HTTP request with backpressure-aware retries
// (per-attempt timeout, Retry-After honoring, exponential backoff).
func (s *Service) doJSON(method, path string, body interface{}) ([]byte, error) {
	url := s.baseURL + path
	headers := map[string]string{}
	if s.apiKey != "" {
		headers["Authorization"] = "Bearer " + s.apiKey
	}
	return s.retry.DoJSON(method, url, body, headers)
}
