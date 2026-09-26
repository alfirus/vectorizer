package llmbrain

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// --- helpers ---------------------------------------------------------------

// brainServer stands up an LM Studio-compatible /chat/completions stub that
// records every request body it receives and answers with replyJSON.
func brainServer(t *testing.T, replyJSON string) (*Service, *[]map[string]interface{}) {
	t.Helper()
	var got []map[string]interface{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read body: %v", err)
		}
		var body map[string]interface{}
		if err := json.Unmarshal(raw, &body); err != nil {
			t.Errorf("decode body %q: %v", raw, err)
		}
		got = append(got, body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, replyJSON)
	}))
	t.Cleanup(srv.Close)
	return New(srv.URL, "test-key", "qwen3.8-27b@q3_k_xl"), &got
}

func lastMaxTokens(t *testing.T, bodies *[]map[string]interface{}) float64 {
	t.Helper()
	if len(*bodies) == 0 {
		t.Fatal("no request reached the server")
	}
	last := (*bodies)[len(*bodies)-1]
	v, ok := last["max_tokens"]
	if !ok {
		t.Fatalf("request body has no max_tokens key: %v", last)
	}
	n, ok := v.(float64)
	if !ok {
		t.Fatalf("max_tokens is %T, want number", v)
	}
	return n
}

const contentReply = `{"choices":[{"message":{"content":"the answer"}}]}`

// --- boundedMaxTokens ------------------------------------------------------

func TestBoundedMaxTokens(t *testing.T) {
	cases := []struct {
		name     string
		maxChars int
		want     int
	}{
		{"unset is bounded by default", 0, 768},
		{"negative is bounded by default", -5, 768},
		{"tiny MaxChars floors at 384", 100, 384},
		{"small MaxChars floors at 384", 400, 384}, // old code sent 100 → starved
		{"MaxChars exactly 768 → 384", 768, 384},
		{"large MaxChars uses half", 4000, 2000},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := boundedMaxTokens(tc.maxChars); got != tc.want {
				t.Fatalf("boundedMaxTokens(%d) = %d, want %d", tc.maxChars, got, tc.want)
			}
		})
	}
}

// --- request-body construction --------------------------------------------

func TestSummarizeAlwaysSendsMaxTokens(t *testing.T) {
	cases := []struct {
		name     string
		maxChars int
		want     float64
	}{
		{"no MaxChars → 768", 0, 768},
		{"MaxChars=400 → 384 (reasoning headroom)", 400, 384},
		{"MaxChars=4000 → 2000", 4000, 2000},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc, bodies := brainServer(t, contentReply)
			if _, err := svc.Summarize(SummarizeRequest{Text: "hello", MaxChars: tc.maxChars}); err != nil {
				t.Fatalf("Summarize: %v", err)
			}
			if got := lastMaxTokens(t, bodies); got != tc.want {
				t.Fatalf("max_tokens = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestChatPathsWithoutMaxCharsSend768(t *testing.T) {
	calls := []struct {
		name string
		call func(*Service) (string, error)
	}{
		{"Chat", func(s *Service) (string, error) { return s.Chat("sys", "ctx", "q") }},
		{"ChatWithTemp", func(s *Service) (string, error) {
			return s.ChatWithTemp("sys", "ctx", "q", 0.7)
		}},
		{"ChatWithHistory", func(s *Service) (string, error) {
			return s.ChatWithHistory([]map[string]interface{}{
				{"role": "user", "content": "q"},
			}, 0)
		}},
		{"Ask", func(s *Service) (string, error) {
			resp, err := s.Ask("q", "ctx")
			if err != nil {
				return "", err
			}
			return resp.Answer, nil
		}},
	}
	for _, tc := range calls {
		t.Run(tc.name, func(t *testing.T) {
			svc, bodies := brainServer(t, contentReply)
			if _, err := tc.call(svc); err != nil {
				t.Fatalf("%s: %v", tc.name, err)
			}
			if got := lastMaxTokens(t, bodies); got != 768 {
				t.Fatalf("max_tokens = %v, want 768 (output must always be bounded)", got)
			}
		})
	}
}

// --- content / reasoning_content fallback ----------------------------------

func TestSummarizeFallsBackToReasoningContent(t *testing.T) {
	// Reasoning model exhausted its budget during thinking → empty content.
	reply := `{"choices":[{"message":{"content":"","reasoning_content":"  the useful trace  \n"}}]}`
	svc, _ := brainServer(t, reply)
	resp, err := svc.Summarize(SummarizeRequest{Text: "hello", MaxChars: 400})
	if err != nil {
		t.Fatalf("Summarize: %v", err)
	}
	if resp.Summary != "the useful trace" {
		t.Fatalf("Summary = %q, want trimmed reasoning fallback", resp.Summary)
	}
}

func TestSummarizePrefersContentOverReasoning(t *testing.T) {
	reply := `{"choices":[{"message":{"content":"visible answer","reasoning_content":"scratch"}}]}`
	svc, _ := brainServer(t, reply)
	resp, err := svc.Summarize(SummarizeRequest{Text: "hello"})
	if err != nil {
		t.Fatalf("Summarize: %v", err)
	}
	if resp.Summary != "visible answer" {
		t.Fatalf("Summary = %q, want visible answer", resp.Summary)
	}
}

func TestSummarizeStillErrorsWhenBothEmpty(t *testing.T) {
	svc, _ := brainServer(t, `{"choices":[{"message":{"content":"","reasoning_content":"   "}}]}`)
	_, err := svc.Summarize(SummarizeRequest{Text: "hello"})
	if err == nil || !strings.Contains(err.Error(), "empty summarization response") {
		t.Fatalf("err = %v, want empty summarization response", err)
	}
}

func TestSummarizeErrorsWithoutChoices(t *testing.T) {
	svc, _ := brainServer(t, `{"choices":[]}`)
	_, err := svc.Summarize(SummarizeRequest{Text: "hello"})
	if err == nil || !strings.Contains(err.Error(), "empty summarization response") {
		t.Fatalf("err = %v, want empty summarization response", err)
	}
}

func TestChatWithTempFallsBackToReasoningContent(t *testing.T) {
	reply := `{"choices":[{"message":{"content":null,"reasoning_content":" reasoned out \n"}}]}`
	svc, _ := brainServer(t, reply)
	got, err := svc.ChatWithTemp("sys", "ctx", "q", 0)
	if err != nil {
		t.Fatalf("ChatWithTemp: %v", err)
	}
	if got != "reasoned out" {
		t.Fatalf("got %q, want trimmed reasoning fallback", got)
	}
}

func TestChatWithHistoryFallsBackToReasoningContent(t *testing.T) {
	reply := `{"choices":[{"message":{"content":"","reasoning_content":" trace "}}]}`
	svc, _ := brainServer(t, reply)
	got, err := svc.ChatWithHistory([]map[string]interface{}{{"role": "user", "content": "q"}}, 0.3)
	if err != nil {
		t.Fatalf("ChatWithHistory: %v", err)
	}
	if got != "trace" {
		t.Fatalf("got %q, want trimmed reasoning fallback", got)
	}
}

func TestAskFallsBackToReasoningContent(t *testing.T) {
	reply := `{"choices":[{"message":{"content":"","reasoning_content":" reasoned answer "}}]}`
	svc, _ := brainServer(t, reply)
	resp, err := svc.Ask("q", "ctx")
	if err != nil {
		t.Fatalf("Ask: %v", err)
	}
	if resp.Answer != "reasoned answer" {
		t.Fatalf("Answer = %q, want trimmed reasoning fallback", resp.Answer)
	}
}
