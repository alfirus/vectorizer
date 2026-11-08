package webhooks

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"sync"
	"time"
)

type Endpoint struct{ URL string; Events []string }

// Server-side fire policy: bounded timeout, drained bodies, capped
// in-flight deliveries. Webhooks are best-effort notifications, never a
// reason to hold goroutines or connections open.
const (
	webhookTimeout   = 10 * time.Second
	maxWebhookFlight = 16
)

type Manager struct {
	mu       sync.RWMutex
	endpoints map[string][]Endpoint
	client   *http.Client
	flight   chan struct{}
}

func New() *Manager {
	return &Manager{
		endpoints: make(map[string][]Endpoint),
		client:    &http.Client{Timeout: webhookTimeout},
		flight:    make(chan struct{}, maxWebhookFlight),
	}
}

// ValidateEndpointURL rejects anything that is not an explicit http(s) URL
// and refuses loopback / private / link-local targets (SSRF guard). DNS
// names must resolve, and every resolved IP must be public.
func ValidateEndpointURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return fmt.Errorf("invalid webhook URL")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("webhook URL must be http(s)")
	}
	host := u.Hostname()
	ips, err := net.LookupIP(host)
	if err != nil || len(ips) == 0 {
		return fmt.Errorf("webhook host does not resolve")
	}
	for _, ip := range ips {
		if !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsMulticast() || ip.IsLinkLocalUnicast() || ip.IsUnspecified() {
			return fmt.Errorf("webhook target must be a public address")
		}
	}
	return nil
}

func (m *Manager) Register(workspaceID, url string, events []string) error {
	if err := ValidateEndpointURL(url); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.endpoints[workspaceID] = append(m.endpoints[workspaceID], Endpoint{URL: url, Events: events})
	return nil
}
func (m *Manager) List(workspaceID string) []Endpoint {
	m.mu.RLock(); defer m.mu.RUnlock()
	return append([]Endpoint(nil), m.endpoints[workspaceID]...)
}
func (m *Manager) Fire(workspaceID, event string, payload interface{}) {
	m.mu.RLock()
	eps := append([]Endpoint(nil), m.endpoints[workspaceID]...)
	m.mu.RUnlock()
	data, _ := json.Marshal(map[string]interface{}{"event": event, "workspace_id": workspaceID, "data": payload})
	for _, ep := range eps {
		should := len(ep.Events) == 0
		for _, e := range ep.Events {
			if e == event {
				should = true
				break
			}
		}
		if !should {
			continue
		}
		select {
		case m.flight <- struct{}{}:
			go m.deliver(ep.URL, data)
		default:
			log.Printf("webhook fire: in-flight cap reached, dropping delivery to %s", ep.URL)
		}
	}
}

func (m *Manager) deliver(target string, data []byte) {
	defer func() { <-m.flight }()
	resp, err := m.client.Post(target, "application/json", bytes.NewReader(data))
	if err != nil {
		log.Printf("webhook deliver %s: %v", target, err)
		return
	}
	_, _ = bytes.NewBuffer(nil).ReadFrom(resp.Body)
	_ = resp.Body.Close()
}
