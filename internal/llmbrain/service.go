package llmbrain

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/alfirus/vectorizer/internal/httpx"
)

// Output-token budget for the local brain. The current model
// (qwen3.8-27b@q3_k_xl) is a dense REASONING model served by LM Studio:
// it spends most of its budget on reasoning_content before emitting any
// content, and LM Studio ignores chat_template_kwargs enable_thinking=false.
// So every request must carry an explicit max_tokens — with no cap the model
// runs for minutes, and with the old MaxChars/4 cap reasoning ate ~95% of the
// budget and content came back empty ("empty summarization response").
const (
	// defaultMaxTokens bounds requests that set no MaxChars (chat/ask paths).
	defaultMaxTokens = 768
	// minMaxTokens is the floor when MaxChars is set: leaves headroom for the
	// reasoning trace so the visible content still fits.
	minMaxTokens = 384
)

// boundedMaxTokens computes the max_tokens cap for a brain request:
//   - MaxChars <= 0 → defaultMaxTokens (768) — never unbounded.
//   - MaxChars >  0 → max(384, MaxChars/2) — char-based, with reasoning headroom.
func boundedMaxTokens(maxChars int) int {
	if maxChars <= 0 {
		return defaultMaxTokens
	}
	if t := maxChars / 2; t > minMaxTokens {
		return t
	}
	return minMaxTokens
}

// chatMessage is the OpenAI-style message we read back from /chat/completions.
type chatMessage struct {
	Content          string `json:"content"`
	ReasoningContent string `json:"reasoning_content"`
}

// text returns the usable answer. Reasoning models can exhaust max_tokens
// during the reasoning phase and return an empty content field — in that case
// fall back to the (trimmed) reasoning trace instead of erroring out.
func (m chatMessage) text() string {
	if m.Content != "" {
		return m.Content
	}
	return strings.TrimSpace(m.ReasoningContent)
}

// Service handles optional LLM-powered summarization and Q&A per agent.
type Service struct {
	baseURL string
	apiKey  string
	model   string
	client  *http.Client  // legacy direct client (kept for compat)
	retry   *httpx.Client // backpressure-aware client (timeouts + Retry-After)
	sem     chan struct{} // cap on concurrent LM calls (slow local models queue)
}

// brainConcurrency reads LLM_MAX_INFLIGHT (default 2 — chat models are slow;
// TagVaultChunk fan-out + deriver + rerank share this budget).
func brainConcurrency() int {
	if v := os.Getenv("LLM_MAX_INFLIGHT"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return 2
}

// llmTimeoutFromEnv reads LLM_TIMEOUT_SECS (default 600s — local chat models
// are slower than embedders; the old hardcoded 10min stays the default).
func llmTimeoutFromEnv() time.Duration {
	if v := os.Getenv("LLM_TIMEOUT_SECS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return time.Duration(n) * time.Second
		}
	}
	return 10 * time.Minute
}

func llmRetriesFromEnv() int {
	if v := os.Getenv("LLM_MAX_RETRIES"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			return n
		}
	}
	return 2
}

func New(baseURL, apiKey, model string) *Service {
	return &Service{
		baseURL: baseURL,
		apiKey:  apiKey,
		model:   model,
		client:  &http.Client{Timeout: 10 * time.Minute},
		retry:   httpx.New(llmTimeoutFromEnv(), llmRetriesFromEnv()),
		sem:     make(chan struct{}, brainConcurrency()),
	}
}

// acquire blocks until a brain slot frees up — this is the backpressure valve
// that keeps TagVaultChunk fan-out + deriver + rerank from flooding LM Studio.
func (s *Service) acquire() { s.sem <- struct{}{} }
func (s *Service) release() { <-s.sem }

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
		"model":       s.model,
		"messages":    messages,
		"temperature": 0.3,
		// Always bound output: reasoning models run minutes uncapped.
		"max_tokens": boundedMaxTokens(req.MaxChars),
	}

	respBody, err := s.doJSON(http.MethodPost, "/chat/completions", body)
	if err != nil {
		return nil, fmt.Errorf("summarize request: %w", err)
	}

	var result struct {
		Choices []struct {
			Message chatMessage `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("parse summarize response: %w", err)
	}

	if len(result.Choices) == 0 {
		return nil, fmt.Errorf("empty summarization response")
	}
	summary := result.Choices[0].Message.text()
	if summary == "" {
		return nil, fmt.Errorf("empty summarization response")
	}

	return &SummarizeResponse{Summary: summary}, nil
}

func (s *Service) Chat(system, context, question string) (string, error) { return s.ChatWithTemp(system, context, question, 0.3) }
func (s *Service) ChatWithHistory(messages []map[string]interface{}, temp float32) (string, error) {
	if temp == 0 {
		temp = 0.3
	}
	body := map[string]interface{}{
		"model":       s.model,
		"messages":    messages,
		"temperature": temp,
		"max_tokens":  boundedMaxTokens(0), // always bound output
	}
	respBody, err := s.doJSON(http.MethodPost, "/chat/completions", body)
	if err != nil {
		return "", err
	}
	var result struct {
		Choices []struct {
			Message chatMessage `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return "", err
	}
	if len(result.Choices) == 0 {
		return "", fmt.Errorf("empty")
	}
	return result.Choices[0].Message.text(), nil
}
func (s *Service) ChatWithTemp(system, context, question string, temp float32) (string, error) {
	if temp == 0 {
		temp = 0.3
	}
	msgs := []map[string]interface{}{{"role": "system", "content": system}, {"role": "user", "content": fmt.Sprintf("Context:\n%s\n\nQuestion: %s", context, question)}}
	body := map[string]interface{}{
		"model":       s.model,
		"messages":    msgs,
		"temperature": temp,
		"max_tokens":  boundedMaxTokens(0), // always bound output
	}
	respBody, err := s.doJSON(http.MethodPost, "/chat/completions", body)
	if err != nil {
		return "", err
	}
	var result struct {
		Choices []struct {
			Message chatMessage `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return "", err
	}
	if len(result.Choices) == 0 {
		return "", fmt.Errorf("empty")
	}
	return result.Choices[0].Message.text(), nil
}

// Ask sends a question about agent memory to the LLM.
func (s *Service) Ask(question string, context string) (*QAResponse, error) {
	messages := []map[string]interface{}{
		{"role": "system", "content": "You are an assistant answering questions based on the provided memory context. Use only information from the context when possible. If the answer is not in the context, say so."},
		{"role": "user", "content": fmt.Sprintf("Context:\n%s\n\nQuestion: %s", context, question)},
	}

	body := map[string]interface{}{
		"model":       s.model,
		"messages":    messages,
		"temperature": 0.3,
		// Always bound output: reasoning models run minutes uncapped.
		"max_tokens": boundedMaxTokens(0),
	}

	respBody, err := s.doJSON(http.MethodPost, "/chat/completions", body)
	if err != nil {
		return nil, fmt.Errorf("ask request: %w", err)
	}

	var result struct {
		Choices []struct {
			Message chatMessage `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("parse ask response: %w", err)
	}

	if len(result.Choices) == 0 {
		return nil, fmt.Errorf("empty answer response")
	}
	answer := result.Choices[0].Message.text()
	if answer == "" {
		return nil, fmt.Errorf("empty answer response")
	}

	return &QAResponse{Answer: answer}, nil
}

// doJSON performs an HTTP request with backpressure-aware retries
// (per-attempt timeout, Retry-After honoring, exponential backoff).
// The semaphore caps concurrent LM calls so a burst of tag/rerank/derive
// work queues in-process (cheap) instead of inside LM Studio (expensive).
func (s *Service) doJSON(method, path string, body interface{}) ([]byte, error) {
	s.acquire()
	defer s.release()
	url := s.baseURL + path
	headers := map[string]string{}
	if s.apiKey != "" {
		headers["Authorization"] = "Bearer " + s.apiKey
	}
	return s.retry.DoJSON(method, url, body, headers)
}
