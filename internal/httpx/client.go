package httpx

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Client wraps http.Client with per-attempt timeouts, Retry-After honoring,
// and exponential backoff + jitter on 429/5xx — the LM Studio backpressure kit.
//
// Why: a slow local model queues requests server-side. A client with no
// timeout hangs forever; a caller that retries instantly doubles the queue.
// This client fails fast per attempt, waits out the server's asked delay,
// and spaces retries so the queue drains instead of growing.
type Client struct {
	HTTP       *http.Client
	Timeout    time.Duration // per-attempt timeout (fail fast, then back off)
	MaxRetries int           // retries AFTER the first attempt (0 = fire once)
	BaseDelay  time.Duration // first backoff step; doubles each retry
	MaxDelay   time.Duration // backoff ceiling (Retry-After can exceed on 429)
}

// New builds a Client with sane defaults for a slow local model:
// long per-attempt timeout (let the model finish), few retries, slow backoff.
func New(timeout time.Duration, maxRetries int) *Client {
	if timeout <= 0 {
		timeout = 5 * time.Minute
	}
	if maxRetries < 0 {
		maxRetries = 0
	}
	return &Client{
		HTTP:       &http.Client{Timeout: timeout},
		Timeout:    timeout,
		MaxRetries: maxRetries,
		BaseDelay:  2 * time.Second,
		MaxDelay:   60 * time.Second,
	}
}

// DoJSON marshals body (if non-nil), POSTs/GETs to url with headers,
// and returns the response bytes. Retries on 429 + 5xx with backoff;
// honors the Retry-After response header when present.
func (c *Client) DoJSON(method, url string, body interface{}, headers map[string]string) ([]byte, error) {
	var payload []byte
	if body != nil {
		var err error
		payload, err = json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("marshal request body: %w", err)
		}
	}

	delay := c.BaseDelay
	var lastErr error
	for attempt := 0; attempt <= c.MaxRetries; attempt++ {
		if attempt > 0 {
			time.Sleep(delay)
			delay = c.nextDelay(delay)
		}
		respBytes, retryAfter, retryable, err := c.once(method, url, payload, headers)
		if err == nil {
			return respBytes, nil
		}
		lastErr = err
		if !retryable {
			return nil, err
		}
		// Server asked us to wait — obey it (this is what drains LM Studio).
		if retryAfter > 0 {
			if retryAfter > 5*time.Minute {
				retryAfter = 5 * time.Minute
			}
			time.Sleep(retryAfter)
			delay = c.BaseDelay // reset backoff after an explicit server delay
		}
	}
	return nil, fmt.Errorf("after %d attempts: %w", c.MaxRetries+1, lastErr)
}

// once fires a single attempt. Returns (bytes, retryAfter, retryable, err).
func (c *Client) once(method, url string, payload []byte, headers map[string]string) ([]byte, time.Duration, bool, error) {
	var reqBody io.Reader
	if payload != nil {
		reqBody = bytes.NewReader(payload)
	}
	req, err := http.NewRequest(method, url, reqBody)
	if err != nil {
		return nil, 0, false, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		// Transport error (timeout, reset): retryable — the server may
		// have been too busy to answer at all.
		return nil, 0, true, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()
	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, 0, true, fmt.Errorf("read response body: %w", err)
	}
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return respBytes, 0, false, nil
	}
	retryable := resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500
	return nil, parseRetryAfter(resp.Header.Get("Retry-After")), retryable,
		fmt.Errorf("HTTP %d: %s", resp.StatusCode, truncate(string(respBytes), 500))
}

func (c *Client) nextDelay(d time.Duration) time.Duration {
	d *= 2
	if d > c.MaxDelay {
		d = c.MaxDelay
	}
	// ±25% jitter so a fleet of retries doesn't march in lockstep.
	j := 0.75 + 0.5*rand.Float64()
	return time.Duration(float64(d) * j)
}

// parseRetryAfter handles both delay-seconds and HTTP-date forms.
func parseRetryAfter(v string) time.Duration {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0
	}
	if secs, err := strconv.Atoi(v); err == nil && secs >= 0 {
		return time.Duration(secs) * time.Second
	}
	if t, err := http.ParseTime(v); err == nil {
		if d := time.Until(t); d > 0 {
			return d
		}
	}
	return 0
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
