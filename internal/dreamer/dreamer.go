package dreamer

import (
	"strings"
	"time"

	"github.com/alfirus/vectorizer/internal/llmbrain"
	"github.com/alfirus/vectorizer/internal/store"
)

// Dreamer periodically summarizes recent session context and stores as conclusions (768d).
type Dreamer struct {
	store *store.Store
	brain *llmbrain.Service
	interval time.Duration
	stop chan struct{}
}

func New(s *store.Store, brain *llmbrain.Service, interval time.Duration) *Dreamer {
	if interval<=0 { interval=3*time.Hour }
	return &Dreamer{store: s, brain: brain, interval: interval, stop: make(chan struct{})}
}

func (d *Dreamer) Start() {
	if d.brain==nil { return }
	go func(){
		ticker:=time.NewTicker(d.interval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C: d.runOnce()
			case <-d.stop: return
			}
		}
	}()
}
func (d *Dreamer) Stop(){ close(d.stop) }

func (d *Dreamer) RunOnce(){ d.runOnce() }

func (d *Dreamer) runOnce() {
	workspaces, _ := d.store.ListWorkspaces()
	for _, ws := range workspaces {
		sessions, _ := d.store.ListSessions(ws)
		for _, sess := range sessions {
			m, _ := sess["metadata"].(map[string]interface{})
			sid, _ := m["session_id"].(string)
			if sid=="" { continue }
			docs, _ := d.store.GetMessages(ws, sid, 20, 0)
			if len(docs)<3 { continue }
			var parts []string
			for _, doc := range docs { if t,ok:=doc["document"].(string); ok && t!="" { parts=append(parts, t)}}
			text:=strings.Join(parts, "\n")
			if text=="" { continue }
			resp, err:=d.brain.Summarize(llmbrain.SummarizeRequest{Text: text, MaxChars: 500})
			if err!=nil || resp.Summary=="" { continue }
			_, _ = d.store.CreateConclusion(ws, "", resp.Summary, map[string]interface{}{"source":"dreamer","session_id":sid})
		}
	}
}
