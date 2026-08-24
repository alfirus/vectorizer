package embedding

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// Service handles text-to-embedding conversion via external providers (Qwen3 1536d MRL).
type Service struct {
	baseURL    string
	apiKey     string
	model      string
	dimensions int
	client     *http.Client
}

func New(baseURL, apiKey, model string) *Service {
	return &Service{
		baseURL: baseURL,
		apiKey:  apiKey,
		model:   model,
		dimensions: 1536,
		client:  &http.Client{},
	}
}

func NewWithDimensions(baseURL, apiKey, model string, dimensions int) *Service {
	if dimensions <= 0 { dimensions = 1536 }
	return &Service{baseURL: baseURL, apiKey: apiKey, model: model, dimensions: dimensions, client: &http.Client{}}
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
		"model": s.model,
		"input": texts,
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

// doJSON performs an HTTP request and returns the response body as JSON bytes.
func (s *Service) doJSON(method, path string, body interface{}) ([]byte, error) {
	var reqBody io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("marshal request body: %w", err)
		}
		reqBody = bytes.NewReader(data)
	}

	url := s.baseURL + path
	req, err := http.NewRequest(method, url, reqBody)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if s.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+s.apiKey)
	}

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response body: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(bodyBytes))
	}

	return bodyBytes, nil
}
