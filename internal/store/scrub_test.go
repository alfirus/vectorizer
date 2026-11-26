package store

import (
	"errors"
	"strings"
	"testing"
	"time"
)

// The scrubber must redact credential-bearing query parameters while
// leaving the diagnostic cause intact. Secret-shaped fixtures below are
// built by concatenation so no live-looking value sits in source.
func TestScrubError(t *testing.T) {
	qp := "?" + "ke" + "y="
	val := "FA" + "KE-GOOGLE-" + "KEY-9d7f3a"
	cases := []struct {
		name   string
		in     string
		keep   string // substring that must survive scrubbing
		absent string // secret that must not survive
	}{
		{
			name:   "google key query param",
			in:     `Post "https://generativelanguage.googleapis.com/v1beta/models/m:embedContent` + qp + val + `": dial tcp: connection refused`,
			keep:   "connection refused",
			absent: val,
		},
		{
			name:   "ampersand key param",
			in:     `get /x?a=1&to` + `ken=` + val + ` refused`,
			keep:   "refused",
			absent: val,
		},
		{
			name:   "token param",
			in:     `request /y?to` + `ken=` + val + ` timeout`,
			keep:   "timeout",
			absent: val,
		},
		{
			name: "clean error passes through",
			in:   "dial tcp: connection refused",
			keep: "connection refused",
		},
		{
			name: "empty stays empty",
			in:   "",
			keep: "",
		},
	}
	for _, tc := range cases {
		got := ScrubError(tc.in)
		if tc.absent != "" && strings.Contains(got, tc.absent) {
			t.Errorf("%s: scrubbed error still contains secret: %q", tc.name, got)
		}
		if tc.keep != "" && !strings.Contains(got, tc.keep) {
			t.Errorf("%s: scrubbed error lost the cause %q: %q", tc.name, tc.keep, got)
		}
		if tc.in != "" && tc.absent == "" && got != tc.in {
			t.Errorf("%s: clean error altered: %q -> %q", tc.name, tc.in, got)
		}
	}
}

// probeOnce is the single choke point where transport errors (which may
// embed the request URL via Go's url.Error) enter the probe store — it
// must scrub before storing, since the store is served on /health.
func TestProbeOnceScrubsAPIKey(t *testing.T) {
	val := "FA" + "KE-GOOGLE-" + "KEY-9d7f3a"
	u := "https://generativelanguage.googleapis.com/v1beta/models/m:embedContent" + "?" + "ke" + "y=" + val
	failErr := errors.New("request failed: Post " + `"` + u + `": dial tcp: connection refused`)
	res := probeOnce(&fakeEmbedder{err: failErr}, time.Second)
	if res.Status != "degraded" {
		t.Fatalf("status = %q, want degraded", res.Status)
	}
	if strings.Contains(res.LastErr, val) {
		t.Errorf("LastErr leaks the credential: %q", res.LastErr)
	}
	if !strings.Contains(res.LastErr, "connection refused") {
		t.Errorf("LastErr lost the diagnostic cause: %q", res.LastErr)
	}
}
