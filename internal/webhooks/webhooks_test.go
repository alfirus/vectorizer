package webhooks

import (
	"testing"
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
