package llmbrain

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// Service handles optional LLM-powered summarization and Q&A per agent.
type Service struct {
	baseURL string
	apiKey  string
	model   string
	client  *http.Client
}

func New(baseURL, apiKey, model string) *Service {
	return &Service{
		baseURL: baseURL,
		apiKey:  apiKey,
		model:   model,
		client:  &http.Client{},
	}
}

// SummarizeRequest is a summarization request.
type SummarizeRequest struct {
	Text     string `json:"text"`
	MaxChars int    `json:"max_chars,omitempty"` // optional limit on output length
}

// SummarizeResponse is the response from the LLM brain.
type SummarizeResponse struct {
	Summary string `json:"summary"`
}

// QAResponse is the response to a question about agent memory.
type QAResponse struct {
	Answer string `json:"answer"`
}

// Summarize sends text to the LLM for summarization.
func (s *Service) Summarize(req SummarizeRequest) (*SummarizeResponse, error) {
	messages := []map[string]interface{}{
		{"role": "system", "content": "You are a concise summarizer. Extract key facts and insights from the given text. Be brief but comprehensive."},
		{"role": "user", "content": req.Text},
	}

	body := map[string]interface{}{
		"model":    s.model,
		"messages": messages,
		"temperature": 0.3,
	}
	if req.MaxChars > 0 {
		body["max_tokens"] = req.MaxChars / 4 // rough char-to-token ratio
	}

	respBody, err := s.doJSON(http.MethodPost, "/chat/completions", body)
	if err != nil {
		return nil, fmt.Errorf("summarize request: %w", err)
	}

	var result struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("parse summarize response: %w", err)
	}

	if len(result.Choices) == 0 || result.Choices[0].Message.Content == "" {
		return nil, fmt.Errorf("empty summarization response")
	}

	return &SummarizeResponse{Summary: result.Choices[0].Message.Content}, nil
}

func (s *Service) Chat(system, context, question string) (string, error) { return s.ChatWithTemp(system, context, question, 0.3) }
func (s *Service) ChatWithHistory(messages []map[string]interface{}, temp float32) (string, error) {
	if temp==0 { temp=0.3 }
	body := map[string]interface{}{"model": s.model, "messages": messages, "temperature": temp}
	respBody, err := s.doJSON(http.MethodPost, "/chat/completions", body)
	if err != nil { return "", err }
	var result struct{ Choices []struct{ Message struct{ Content string `json:"content"`} `json:"message"`} `json:"choices"`}
	if err := json.Unmarshal(respBody, &result); err != nil { return "", err }
	if len(result.Choices)==0 { return "", fmt.Errorf("empty") }
	return result.Choices[0].Message.Content, nil
}
func (s *Service) ChatWithTemp(system, context, question string, temp float32) (string, error) {
	if temp==0 { temp=0.3 }
	msgs := []map[string]interface{}{{"role": "system", "content": system}, {"role": "user", "content": fmt.Sprintf("Context:\n%s\n\nQuestion: %s", context, question)}}
	body := map[string]interface{}{"model": s.model, "messages": msgs, "temperature": temp}
	respBody, err := s.doJSON(http.MethodPost, "/chat/completions", body)
	if err != nil { return "", err }
	var result struct{ Choices []struct{ Message struct{ Content string `json:"content"`} `json:"message"`} `json:"choices"`}
	if err := json.Unmarshal(respBody, &result); err != nil { return "", err }
	if len(result.Choices)==0 { return "", fmt.Errorf("empty") }
	return result.Choices[0].Message.Content, nil
}

// Ask sends a question about agent memory to the LLM.
func (s *Service) Ask(question string, context string) (*QAResponse, error) {
	messages := []map[string]interface{}{
		{"role": "system", "content": "You are an assistant answering questions based on the provided memory context. Use only information from the context when possible. If the answer is not in the context, say so."},
		{"role": "user", "content": fmt.Sprintf("Context:\n%s\n\nQuestion: %s", context, question)},
	}

	body := map[string]interface{}{
		"model":     s.model,
		"messages":  messages,
		"temperature": 0.3,
	}

	respBody, err := s.doJSON(http.MethodPost, "/chat/completions", body)
	if err != nil {
		return nil, fmt.Errorf("ask request: %w", err)
	}

	var result struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("parse ask response: %w", err)
	}

	if len(result.Choices) == 0 || result.Choices[0].Message.Content == "" {
		return nil, fmt.Errorf("empty answer response")
	}

	return &QAResponse{Answer: result.Choices[0].Message.Content}, nil
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
