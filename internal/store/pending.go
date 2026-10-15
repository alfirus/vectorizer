package store

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/alfirus/vectorizer/internal/models"
)

// Deferred-update inbox (t_7c865ae4, follow-up to the t_5df7921d 409 guard).
//
// Why this exists: PUT /messages/:id used to answer a bare 409 while the
// embed probe was degraded, so the caller's splice intent died with the
// response — nothing persisted it, and the caller had to remember to retry.
// Per Sofia's rule ("queue splices behind the embed-fix card"), the handler
// now accepts the PUT into this inbox (202 queued) and the store replays it
// through the normal write-first paths once the probe returns ok.
//
// Design notes:
//   - Process-local, mutex-guarded map (no Chroma schema migration, visible
//     even when Chroma is the sick component). Degraded windows are minutes
//     (probe ticks every 30s, staleness pages at 15min), so surviving a
//     container recreate is explicitly NOT promised — callers keep the entry
//     id and can retry; the GET endpoint exposes status for reconciliation.
//     Consequence, verified live 2026-10-03: a queued entry lives and dies
//     with the process that accepted it. Restarting the container (or a
//     scratch twin) while degraded ORPHANS the intent — the entry never
//     replays, and the new process starts with an empty inbox. During a real
//     outage: RECREATE IS THE DRAIN KILLER — prefer waiting for probe
//     recovery over recreating, and re-PUT anything queued before a restart.
//   - Bounded: MaxPendingEntries caps memory; the oldest pending entry is
//     evicted (with pendingDrops counted LOUDLY on /metrics) instead of
//     silently growing. Same message_id re-queues coalesce (last-writer-wins).
//   - Replay runs UpdateMessage / UpdateMessageSections, which are write-first
//     (embed before delete), so a mid-replay embed failure leaves the record
//     intact and the entry re-queues with attempts+1 instead of destroying data.

const (
	// MaxPendingEntries bounds the inbox. 500 entries of ~100k-max content
	// is ~50MB worst case — acceptable for a transient degraded window, and
	// far above the handful of splices a real outage collects.
	MaxPendingEntries = 500
	// MaxReplayAttempts caps automatic replays. Beyond this the entry parks
	// as failed with last_error, visible on GET /messages/pending — a human
	// retries it explicitly instead of the loop spinning forever.
	MaxReplayAttempts = 5
	// PendingRetention keeps applied/failed entries briefly so callers can
	// confirm the outcome, then prunes them on the next enqueue/list pass.
	PendingRetention = time.Hour
)

// PendingStatus is the lifecycle state of a queued update.
const (
	PendingStatusPending  = "pending"
	PendingStatusApplied  = "applied"
	PendingStatusFailed   = "failed"
	PendingStatusDraining = "draining" // claimed by an in-flight replay pass
)

// PendingUpdate is one queued PUT /messages/:id (full replace XOR splice).
type PendingUpdate struct {
	MessageID   string                 `json:"message_id"`
	WorkspaceID string                 `json:"workspace_id"`
	Content     string                 `json:"content,omitempty"`
	Sections    map[string]string      `json:"sections,omitempty"`
	SessionID   string                 `json:"session_id,omitempty"`
	Role        string                 `json:"role,omitempty"`
	Metadata    map[string]interface{} `json:"metadata,omitempty"`
	Requester   string                 `json:"requester,omitempty"`
	QueuedAt    time.Time              `json:"queued_at"`
	Status      string                 `json:"status"`
	Attempts    int                    `json:"attempts"`
	LastError   string                 `json:"last_error,omitempty"`
	AppliedAt   time.Time              `json:"applied_at,omitempty"`
}

var (
	pendingMu     sync.Mutex
	pendingInbox  = map[string]*PendingUpdate{}
	pendingDrops  atomic.Uint64
	pendingSeq    atomic.Uint64
	pendingDrain  atomic.Bool // one replay pass at a time
	replayHookSet atomic.Bool
)

// clonePendingUpdate hands the inbox a deep copy of every string an entry
// will outlive the request with. Without this the entry aliases Fiber's
// pooled ctx buffers and mutates under the caller — see the call-site
// comment in EnqueuePendingUpdate. Red/green: TestEnqueuePendingUpdateOwnsStrings.
func clonePendingUpdate(e *PendingUpdate) *PendingUpdate {
	if e == nil {
		return nil
	}
	cp := *e
	cp.MessageID = strings.Clone(e.MessageID)
	cp.WorkspaceID = strings.Clone(e.WorkspaceID)
	cp.SessionID = strings.Clone(e.SessionID)
	cp.Requester = strings.Clone(e.Requester)
	cp.Role = strings.Clone(e.Role)
	cp.Content = strings.Clone(e.Content)
	if e.Sections != nil {
		sections := make(map[string]string, len(e.Sections))
		for k, v := range e.Sections {
			sections[strings.Clone(k)] = strings.Clone(v)
		}
		cp.Sections = sections
	}
	return &cp
}

// EnqueuePendingUpdate stores a deferred PUT. Same message_id coalesces
// (last-writer-wins, attempts reset) so blind retries are idempotent.
// Returns the stored entry, or an error when the inbox had to evict.
func EnqueuePendingUpdate(e *PendingUpdate) (*PendingUpdate, error) {
	if e == nil || e.MessageID == "" {
		return nil, fmt.Errorf("message_id is required")
	}
	if e.Content == "" && len(e.Sections) == 0 {
		return nil, fmt.Errorf("content or sections are required")
	}
	// The inbox keeps entries far past the handler's lifetime, so it must own
	// its strings. Fiber returns unsafe strings that alias the pooled ctx
	// path/header buffers (Immutable is off unless the app sets it), and the
	// next request overwrites those bytes in place: live evidence from
	// t_7c865ae4 — the 202 answered id "deadbeef-1111-4000-8000-000000000009"
	// while two later calls reused the buffer and the queued entry came back
	// as "pending2-2222-4000-8000-000000000002" ("pending" written over the
	// first 7 bytes by GET /messages/pending, the tail still holding the
	// second request's id), which made the replay report "message not found".
	e = clonePendingUpdate(e)
	if e.Status == "" {
		e.Status = PendingStatusPending
	}
	if e.QueuedAt.IsZero() {
		e.QueuedAt = time.Now().UTC()
	}

	pendingMu.Lock()
	defer pendingMu.Unlock()
	pruneTerminalLocked()

	if _, exists := pendingInbox[e.MessageID]; exists {
		// Coalesce: the newest intent wins, retry budget restarts.
		e.Attempts = 0
		e.LastError = ""
		pendingInbox[e.MessageID] = e
		return e, nil
	}
	if len(pendingInbox) >= MaxPendingEntries {
		// Evict the oldest *pending* entry (never evict applied/failed that
		// callers may still be reading — prefer to fail the enqueue loudly).
		oldest := ""
		var oldestAt time.Time
		for id, p := range pendingInbox {
			if p.Status != PendingStatusPending {
				continue
			}
			if oldest == "" || p.QueuedAt.Before(oldestAt) {
				oldest, oldestAt = id, p.QueuedAt
			}
		}
		if oldest == "" {
			return nil, fmt.Errorf("pending queue full (%d entries, none evictable)", len(pendingInbox))
		}
		delete(pendingInbox, oldest)
		pendingDrops.Add(1)
	}
	pendingSeq.Add(1)
	pendingInbox[e.MessageID] = e
	return e, nil
}

// ListPendingUpdates returns a snapshot, optionally filtered by workspace.
// Telemetry-safe: no embedding spend, no Chroma I/O.
func ListPendingUpdates(workspaceID string) []*PendingUpdate {
	pendingMu.Lock()
	defer pendingMu.Unlock()
	pruneTerminalLocked()
	out := make([]*PendingUpdate, 0, len(pendingInbox))
	for _, p := range pendingInbox {
		if workspaceID != "" && p.WorkspaceID != workspaceID {
			continue
		}
		cp := *p
		out = append(out, &cp)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].QueuedAt.Before(out[j].QueuedAt) })
	return out
}

// PendingCount returns the number of entries still awaiting replay.
func PendingCount() int {
	pendingMu.Lock()
	defer pendingMu.Unlock()
	n := 0
	for _, p := range pendingInbox {
		if p.Status == PendingStatusPending {
			n++
		}
	}
	return n
}

// PendingDrops returns the lifetime count of entries evicted by the bound.
func PendingDrops() uint64 { return pendingDrops.Load() }

// pruneTerminalLocked drops applied/failed entries older than retention.
// Caller must hold pendingMu.
func pruneTerminalLocked() {
	now := time.Now().UTC()
	for id, p := range pendingInbox {
		if (p.Status == PendingStatusApplied || p.Status == PendingStatusFailed) &&
			!p.AppliedAt.IsZero() && now.Sub(p.AppliedAt) > PendingRetention {
			delete(pendingInbox, id)
		}
	}
}

// claimDrainBatch atomically moves pending entries to draining and returns
// them. Single-flight: acquired=false means another pass holds the slot.
//
// acquired MUST be honoured by the caller even when batch is empty: the slot
// is taken by the CAS above, so a nil batch with acquired=true is a legitimate
// "nothing to do" pass, not a lost race. Returning only the batch used to leak
// the slot here (fixed 2026-10-05): the boot-time ok probe drained an empty
// inbox, wedged the flag, and every later drain silently no-ops — queued
// entries stayed "pending" forever while the ticker kept logging
// embed_status=ok pending_count=1.
func claimDrainBatch() (batch []*PendingUpdate, acquired bool) {
	if !pendingDrain.CompareAndSwap(false, true) {
		return nil, false
	}
	pendingMu.Lock()
	defer pendingMu.Unlock()
	for _, p := range pendingInbox {
		if p.Status == PendingStatusPending {
			p.Status = PendingStatusDraining
			batch = append(batch, p)
		}
	}
	sort.Slice(batch, func(i, j int) bool { return batch[i].QueuedAt.Before(batch[j].QueuedAt) })
	return batch, true
}

// markApplied records a successful replay.
func markApplied(id string) {
	pendingMu.Lock()
	defer pendingMu.Unlock()
	if p, ok := pendingInbox[id]; ok {
		p.Status = PendingStatusApplied
		p.LastError = ""
		p.AppliedAt = time.Now().UTC()
	}
}

// markRequeue returns an entry to pending (attempts+1) or parks it failed.
// Returns the entry's new status ("pending", "failed", or "" when gone).
func markRequeue(id, errMsg string) string {
	pendingMu.Lock()
	defer pendingMu.Unlock()
	p, ok := pendingInbox[id]
	if !ok {
		return ""
	}
	p.Attempts++
	p.LastError = errMsg
	if p.Attempts >= MaxReplayAttempts {
		p.Status = PendingStatusFailed
		p.AppliedAt = time.Now().UTC()
		return PendingStatusFailed
	}
	p.Status = PendingStatusPending
	return PendingStatusPending
}

// DrainPendingUpdates replays queued updates through the caller's apply
// function (which must run the normal write-first store paths). Entries
// that hit the attempt cap park as failed; everything else that errors
// re-queues. Safe to call when the embedder is healthy; callers should
// check EmbeddingHealth first — mid-replay embed failures just re-queue,
// they never destroy the underlying record.
func DrainPendingUpdates(apply func(*PendingUpdate) error) (applied, requeued, failed int) {
	batch, acquired := claimDrainBatch()
	if !acquired {
		// Another replay pass is in flight; it owns the entries.
		return 0, 0, 0
	}
	// Once the slot is acquired it is released on every exit path: an EMPTY
	// batch (inbox has nothing pending) must not leave pendingDrain stuck.
	defer pendingDrain.Store(false)
	for _, p := range batch {
		if err := apply(p); err != nil {
			status := markRequeue(p.MessageID, err.Error())
			fmt.Printf("pending replay: update failed id=%s attempt=%d status=%s err=%v\n",
				p.MessageID, p.Attempts+1, status, err)
			// markRequeue already flipped status to pending/failed; count it.
			if status == PendingStatusFailed {
				failed++
			} else {
				requeued++
			}
			continue
		}
		markApplied(p.MessageID)
		applied++
	}
	return applied, requeued, failed
}

// ApplyPendingUpdate runs one queued entry against a Store using the same
// write-first paths the live PUT handler uses. Unknown-workspace entries
// resolve through ListWorkspaces + CountMessageChunks, mirroring the
// handler's resolveMessageWorkspace.
func (s *Store) ApplyPendingUpdate(p *PendingUpdate) error {
	if p == nil {
		return fmt.Errorf("nil pending update")
	}
	ws := ResolveWorkspaceID(p.WorkspaceID)
	if ws == "" || ws == "default" {
		// Empty hint, or a hint that canonicalizes to nothing useful:
		// scan every known workspace for the id (mirrors the handler's
		// resolveMessageWorkspace fallback).
		if found := s.resolvePendingWorkspace(p.MessageID); found != "" {
			ws = found
		}
	}
	if ws == "" {
		return fmt.Errorf("message not found in any workspace")
	}
	if len(p.Sections) > 0 {
		res, err := s.UpdateMessageSections(ws, p.MessageID, p.Sections, p.Metadata)
		if err != nil {
			return err
		}
		_ = res
		return nil
	}
	if p.Content == "" {
		return fmt.Errorf("content is required")
	}
	content := models.SanitizeString(p.Content)
	role, sessionID := p.Role, p.SessionID
	if role == "" || sessionID == "" {
		if docs, err := s.GetMessageChunks(ws, p.MessageID); err == nil && len(docs) > 0 {
			if meta, ok := docs[0]["metadata"].(map[string]interface{}); ok {
				if role == "" {
					role, _ = meta["role"].(string)
				}
				if sessionID == "" {
					sessionID, _ = meta["session_id"].(string)
				}
			}
		}
	}
	if role == "" {
		role = "user"
	}
	if sessionID == "" {
		return fmt.Errorf("session_id is required (not supplied and not recoverable from stored chunks)")
	}
	msg := models.NewMessage(ws, sessionID, role, content)
	msg.ID = p.MessageID
	msg.Metadata = p.Metadata
	_, err := s.UpdateMessage(msg, content)
	return err
}

// resolvePendingWorkspace mirrors the handler's workspace resolution for
// replayed entries that queued without (or with a stale) workspace hint.
//
// Canonicalization MATTERS here (verified live 2026-10-03): callers PUT with
// the raw workspace name (e.g. "t7c_proof_123"), but Chroma collections and
// ListWorkspaces carry the CANONICAL id ("t7c_proof_123" — lowercased, dashes
// and spaces folded to underscores). CountMessageChunks(ws_hint, ...) on the
// raw hint misses its own collection, the scan over canonical ListWorkspaces
// names never matches the raw hint either, and the replay parks as failed
// "message not found in any workspace" forever. So the hint goes through
// ResolveWorkspaceID first, exactly like GetCollectionName does.
func (s *Store) resolvePendingWorkspace(messageID string) string {
	if ws, _ := s.ListWorkspaces(); ws != nil {
		for _, w := range ws {
			if n, err := s.CountMessageChunks(w, messageID); err == nil && n > 0 {
				return w
			}
		}
	}
	return ""
}

// SetReplayHook installs the drain-on-recovery callback exactly once.
// The probe loop calls it after a probe result flips to ok; main.go wires
// the store-bound closure AND a periodic reconciler (belt and suspenders —
// the once-only hook is consumed by the first ok, which on a healthy boot
// arrives before any outage). Returns false when a hook is already installed.
func SetReplayHook(hook func()) bool {
	return replayHookSet.CompareAndSwap(false, true) && storeReplayHook(hook)
}

var (
	replayHookMu sync.Mutex
	replayHook   func()
)

func storeReplayHook(hook func()) bool {
	replayHookMu.Lock()
	defer replayHookMu.Unlock()
	if replayHook != nil {
		return false
	}
	replayHook = hook
	return true
}

// fireReplayHook runs the installed drain callback, if any. Called by the
// probe loop on recovery — never on the request path.
func fireReplayHook() {
	replayHookMu.Lock()
	hook := replayHook
	replayHookMu.Unlock()
	if hook != nil {
		hook()
	}
}
