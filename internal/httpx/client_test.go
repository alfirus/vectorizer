package httpx

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestDoJSONSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()
	c := New(5*time.Second, 2)
	out, err := c.DoJSON("GET", srv.URL, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(out) != `{"ok":true}` {
		t.Fatalf("unexpected body: %s", out)
	}
}

func TestDoJSONRetries429ThenSucceeds(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusTooManyRequests)
			w.Write([]byte(`busy`))
			return
		}
		w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()
	c := New(5*time.Second, 2)
	c.BaseDelay = 10 * time.Millisecond
	out, err := c.DoJSON("GET", srv.URL, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(out) != `{"ok":true}` {
		t.Fatalf("unexpected body: %s", out)
	}
	if calls.Load() != 2 {
		t.Fatalf("expected 2 calls, got %d", calls.Load())
	}
}

func TestDoJSONNoRetryOn400(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`bad`))
	}))
	defer srv.Close()
	c := New(5*time.Second, 3)
	_, err := c.DoJSON("GET", srv.URL, nil, nil)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if calls.Load() != 1 {
		t.Fatalf("expected 1 call (no retry on 400), got %d", calls.Load())
	}
}

func TestParseRetryAfter(t *testing.T) {
	if d := parseRetryAfter("3"); d != 3*time.Second {
		t.Fatalf("seconds form: got %v", d)
	}
	if d := parseRetryAfter(""); d != 0 {
		t.Fatalf("empty: got %v", d)
	}
	if d := parseRetryAfter("garbage"); d != 0 {
		t.Fatalf("garbage: got %v", d)
	}
}
