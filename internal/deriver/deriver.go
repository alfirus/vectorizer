package deriver
import (
	"strings"
	"sync/atomic"
	"time"


	"github.com/alfirus/vectorizer/internal/llmbrain"
	"github.com/alfirus/vectorizer/internal/store"
)

// Deriver extracts conclusions asynchronously
type Deriver struct {
	store *store.Store
	brain *llmbrain.Service
	queue chan queued
	stop  chan struct{}
	// drops counts facts silently discarded because the queue was full.
	// A monotonic counter (no silent loss): surfaced via Drops() and the
	// /metrics endpoint so a saturated queue pages instead of vanishing.
	drops atomic.Uint64
}

type queued struct { ws, sessionID, peerID, content, msgID string }

func New(s *store.Store, b *llmbrain.Service) *Deriver {
	return &Deriver{store: s, brain: b, queue: make(chan queued, 1000), stop: make(chan struct{})}
}

func (d *Deriver) Enqueue(ws, sessionID, peerID, msgID, content string) {
	select {
	case d.queue <- queued{ws, sessionID, peerID, content, msgID}:
	default:
		// Queue full — count it LOUDLY instead of vanishing the fact.
		d.drops.Add(1)
	}
}

// Drops returns the lifetime count of facts discarded on a full queue.
func (d *Deriver) Drops() uint64 { return d.drops.Load() }

// QueueDepth returns the current pending backlog (for /metrics alerting).
func (d *Deriver) QueueDepth() int { return len(d.queue) }

func (d *Deriver) Start() {
	if d.brain==nil { return }
	go func(){
		// debounce batch every 2s or 5 msgs
		var batch []queued
		ticker := time.NewTicker(2*time.Second)
		defer ticker.Stop()
		for {
			select {
			case q := <-d.queue:
				batch = append(batch, q)
				if len(batch) >= 5 { d.flush(batch); batch=nil }
			case <-ticker.C:
				if len(batch)>0 { d.flush(batch); batch=nil }
			case <-d.stop:
				return
			}
		}
	}()
}
func (d *Deriver) Stop(){ close(d.stop) }

func (d *Deriver) flush(batch []queued) {
	// Group by workspace
	groups := map[string][]queued{}
	for _, q := range batch { groups[q.ws]=append(groups[q.ws], q) }
	for ws, qs := range groups {
		var parts []string
		var msgIDs []string
		peerID := qs[0].peerID
		for _, q := range qs { parts=append(parts, q.content); msgIDs=append(msgIDs, q.msgID) }
		text := strings.Join(parts, "\n")
		if len(text) > 8000 { text=text[:8000] }
		resp, err := d.brain.Summarize(llmbrain.SummarizeRequest{Text: "Extract 1-3 concise facts/preferences about the peer from:\n"+text, MaxChars: 400})
		if err!=nil || resp.Summary=="" { continue }
		// Store as conclusions (one per line)
		for _, line := range strings.Split(resp.Summary, "\n") {
			line=strings.TrimSpace(line); if line=="" { continue }
			line=strings.TrimPrefix(line, "- "); line=strings.TrimPrefix(line, "* ")
			id, _ := d.store.CreateConclusion(ws, peerID, line, map[string]interface{}{"source":"deriver"})
			_ = d.store.AddReasoningEdge(ws, peerID, id, nil, msgIDs)
		}
	}
}
