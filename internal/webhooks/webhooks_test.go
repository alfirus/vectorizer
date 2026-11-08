package webhooks

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestValidateEndpointURL(t *testing.T) {
	bad := map[string]string{
		"empty":          "",
		"no-scheme":      "example.com/hook",
		"gopher":         "gopher://203.0.113.10/hook",
		"file":           "file:///etc/passwd",
		"loopback":       "http://127.0.0.1:9999/hook",
		"loopback-ipv6":  "http://[::1]/hook",
		"private-10":     "http://10.0.0.5/hook",
		"private-172":    "http://172.16.4.4/hook",
		"private-192":    "http://192.168.1.20/hook",
		"metadata":       "http://169.254.169.254/latest",
		"unresolvable":   "http://nonexistent-invalid-xyz12345.test/hook",
	}
	for name, raw := range bad {
		if err := ValidateEndpointURL(raw); err == nil {
			t.Errorf("%s: %q should be rejected (SSRF guard)", name, raw)
		}
	}
	// TEST-NET addresses are public-unicast per Go's net package: allowed
	// without any DNS lookup (IP literals resolve without network).
	for _, raw := range []string{"http://203.0.113.10/hook", "https://198.51.100.7:8443/x"} {
		if err := ValidateEndpointURL(raw); err != nil {
			t.Errorf("%q should be allowed: %v", raw, err)
		}
	}
}

func TestRegisterRejectsSSRF(t *testing.T) {
	m := New()
	if err := m.Register("ws", "http://169.254.169.254/", nil); err == nil {
		t.Error("Register must reject metadata-service URL")
	}
	if len(m.List("ws")) != 0 {
		t.Error("rejected endpoint must not be stored")
	}
}

func TestRegisterAndList(t *testing.T) {
	m := New()
	// NOTE: Register enforces the SSRF guard (P0) — hostnames must resolve
	// to public IPs. TEST-NET literals (203.0.113.0/24, 198.51.100.0/24)
	// are public-unicast per Go's net package and need no DNS, so tests
	// stay hermetic. Loopback/httptest URLs are rejected by design.
	mustRegister := func(ws, url string, events []string) {
		t.Helper()
		if err := m.Register(ws, url, events); err != nil {
			t.Fatalf("Register(%q) = %v", url, err)
		}
	}
	mustRegister("ws1", "http://203.0.113.10/hook", []string{"message.created"})
	mustRegister("ws1", "http://203.0.113.11/other", nil) // nil events = all events
	mustRegister("ws2", "http://198.51.100.7/ws2", []string{"other"})

	eps := m.List("ws1")
	if len(eps) != 2 {
		t.Fatalf("List(ws1) = %d endpoints, want 2", len(eps))
	}
	if got := m.List("ws2")[0].URL; got != "http://198.51.100.7/ws2" {
		t.Fatalf("URL = %q", got)
	}
	if len(m.List("nope")) != 0 {
		t.Fatal("unknown workspace must list zero endpoints")
	}
}

// Fire must deliver a JSON event envelope to matching endpoints only.
func TestFireDelivers(t *testing.T) {
	var got []map[string]interface{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]interface{}
		_ = json.NewDecoder(r.Body).Decode(&body)
		got = append(got, body)
		w.WriteHeader(200)
	}))
	defer srv.Close()

	m := New()
	// Inject directly: srv.URL is loopback, which Register's SSRF guard
	// (P0) rejects by design. Fire's delivery path is what this tests.
	m.mu.Lock()
	m.endpoints["ws1"] = []Endpoint{
		{URL: srv.URL, Events: []string{"message.created"}},
		{URL: srv.URL + "/nomatch", Events: []string{"something.else"}},
	}
	m.mu.Unlock()

	m.Fire("ws1", "message.created", map[string]string{"id": "m1"})

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && len(got) == 0 {
		time.Sleep(20 * time.Millisecond)
	}
	if len(got) != 1 {
		t.Fatalf("deliveries = %d, want 1", len(got))
	}
	if got[0]["event"] != "message.created" || got[0]["workspace_id"] != "ws1" {
		t.Fatalf("envelope mismatch: %v", got[0])
	}
}
