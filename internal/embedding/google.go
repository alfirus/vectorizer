package embedding

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/alfirus/vectorizer/internal/httpx"
)

// GoogleService handles text-to-embedding conversion via Google AI Studio API.
type GoogleService struct {
	apiKey     string
	model      string
	dimensions int
	client     *http.Client  // legacy (kept for compat)
	retry      *httpx.Client // timeouts + retries
}

func NewGoogle(apiKey, model string, dimensions int) *GoogleService {
	if dimensions <= 0 {
		dimensions = 768 // Default for text-embedding-004
	}
	return &GoogleService{
		apiKey:     apiKey,
		model:      model,
		dimensions: dimensions,
		client:     &http.Client{},
		retry:      httpx.New(timeoutFromEnv(), retriesFromEnv()),
	}
}

func (s *GoogleService) Model() string       { return s.model }
func (s *GoogleService) Dimensions() int     { return s.dimensions }
func (s *GoogleService) SetModel(m string)   { s.model = m }
func (s *GoogleService) SetDimensions(d int) { if d > 0 { s.dimensions = d } }
func (s *GoogleService) SetBaseURL(url string) { /* Google uses fixed API endpoint */ }

// Embed sends texts to Google AI Studio and returns embeddings.
func (s *GoogleService) Embed(texts []string) ([]EmbeddingResult, error) {
	if len(texts) == 0 {
		return nil, nil
	}

	// Use batch embed endpoint for multiple texts
	if len(texts) > 1 {
		return s.batchEmbed(texts)
	}

	// Single text embed
	return s.singleEmbed(texts[0])
}

func (s *GoogleService) singleEmbed(text string) ([]EmbeddingResult, error) {
	url := fmt.Sprintf("https://generativelanguage.googleapis.com/v1beta/models/%s:embedContent?key=%s", s.model, s.apiKey)

	body := map[string]interface{}{
		"model": fmt.Sprintf("models/%s", s.model),
		"content": map[string]interface{}{
			"parts": []map[string]string{
				{"text": text},
			},
		},
	}

	// Add output dimensionality if supported
	if s.dimensions > 0 {
		body["outputDimensionality"] = s.dimensions
	}

	resp, err := s.doJSON(http.MethodPost, url, body)
	if err != nil {
		return nil, fmt.Errorf("google embed request: %w", err)
	}

	var result struct {
		Embedding struct {
			Values []float64 `json:"values"`
		} `json:"embedding"`
	}
	if err := json.Unmarshal(resp, &result); err != nil {
		return nil, fmt.Errorf("parse google embed response: %w", err)
	}

	vec := make([]float32, len(result.Embedding.Values))
	for i, v := range result.Embedding.Values {
		vec[i] = float32(v)
	}

	return []EmbeddingResult{{Vector: vec}}, nil
}

func (s *GoogleService) batchEmbed(texts []string) ([]EmbeddingResult, error) {
	url := fmt.Sprintf("https://generativelanguage.googleapis.com/v1beta/models/%s:batchEmbedContents?key=%s", s.model, s.apiKey)

	requests := make([]map[string]interface{}, len(texts))
	for i, text := range texts {
		requests[i] = map[string]interface{}{
			"model": fmt.Sprintf("models/%s", s.model),
			"content": map[string]interface{}{
				"parts": []map[string]string{
					{"text": text},
				},
			},
		}
		// Add output dimensionality if supported
		if s.dimensions > 0 {
			requests[i]["outputDimensionality"] = s.dimensions
		}
	}

	body := map[string]interface{}{
		"requests": requests,
	}

	resp, err := s.doJSON(http.MethodPost, url, body)
	if err != nil {
		return nil, fmt.Errorf("google batch embed request: %w", err)
	}

	var result struct {
		Embeddings []struct {
			Values []float64 `json:"values"`
		} `json:"embeddings"`
	}
	if err := json.Unmarshal(resp, &result); err != nil {
		return nil, fmt.Errorf("parse google batch embed response: %w", err)
	}

	results := make([]EmbeddingResult, len(result.Embeddings))
	for i, e := range result.Embeddings {
		vec := make([]float32, len(e.Values))
		for j, v := range e.Values {
			vec[j] = float32(v)
		}
		results[i].Vector = vec
	}

	return results, nil
}

// EmbedSingle is a convenience wrapper for a single text.
func (s *GoogleService) EmbedSingle(text string) ([]float32, error) {
	results, err := s.Embed([]string{text})
	if err != nil {
		return nil, err
	}
	if len(results) == 0 || len(results[0].Vector) == 0 {
		return nil, fmt.Errorf("empty google embedding result")
	}
	return results[0].Vector, nil
}

func (s *GoogleService) doJSON(method, url string, body interface{}) ([]byte, error) {
	return s.retry.DoJSON(method, url, body, nil)
}
