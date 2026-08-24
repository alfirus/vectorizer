package webhooks

import (
	"bytes"
	"encoding/json"
	"net/http"
	"sync"
)

type Endpoint struct{ URL string; Events []string }

type Manager struct {
	mu sync.RWMutex; endpoints map[string][]Endpoint
}

func New() *Manager { return &Manager{endpoints: make(map[string][]Endpoint)} }

func (m *Manager) Register(workspaceID, url string, events []string) {
	m.mu.Lock(); defer m.mu.Unlock()
	m.endpoints[workspaceID]=append(m.endpoints[workspaceID], Endpoint{URL:url, Events:events})
}
func (m *Manager) List(workspaceID string) []Endpoint {
	m.mu.RLock(); defer m.mu.RUnlock()
	return append([]Endpoint(nil), m.endpoints[workspaceID]...)
}
func (m *Manager) Fire(workspaceID, event string, payload interface{}) {
	m.mu.RLock(); eps:=append([]Endpoint(nil), m.endpoints[workspaceID]...); m.mu.RUnlock()
	data,_:=json.Marshal(map[string]interface{}{"event":event,"workspace_id":workspaceID,"data":payload})
	for _, ep:=range eps {
		should:=len(ep.Events)==0
		for _, e:=range ep.Events { if e==event { should=true; break } }
		if !should { continue }
		go http.Post(ep.URL, "application/json", bytes.NewReader(data))
	}
}
