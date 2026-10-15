package handlers

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
	"unsafe"

	"github.com/gofiber/fiber/v2"

	"github.com/alfirus/vectorizer/internal/embedding"
	"github.com/alfirus/vectorizer/internal/store"
)

// deadEmbedder fails every embed call, so the background probe settles on
// "degraded" - the exact state the t_0927b244 splices ran in on 2026-09-27.
type deadEmbedder struct{}

func (deadEmbedder) Embed([]string) ([]embedding.EmbeddingResult, error) {
	return nil, errors.New("embedder down")
}
func (deadEmbedder) EmbedSingle(string) ([]float32, error) { return nil, errors.New("embedder down") }
func (deadEmbedder) Model() string                         { return "dead" }
func (deadEmbedder) Dimensions() int                       { return 768 }
func (deadEmbedder) SetModel(string)                       {}
func (deadEmbedder) SetDimensions(int)                     {}
func (deadEmbedder) SetBaseURL(string)                     {}

// healthyEmbedder restores a sane package-level probe state afterwards.
type healthyEmbedder struct{}

func (healthyEmbedder) Embed(texts []string) ([]embedding.EmbeddingResult, error) {
	out := make([]embedding.EmbeddingResult, len(texts))
	for i := range out {
		out[i] = embedding.EmbeddingResult{Vector: make([]float32, 768)}
	}
	return out, nil
}
func (healthyEmbedder) EmbedSingle(string) ([]float32, error) { return make([]float32, 768), nil }
func (healthyEmbedder) Model() string                         { return "ok" }
func (healthyEmbedder) Dimensions() int                       { return 768 }
func (healthyEmbedder) SetModel(string)                       {}
func (healthyEmbedder) SetDimensions(int)                     {}
func (healthyEmbedder) SetBaseURL(string)                     {}

// TestUpdateRefusesWhileEmbeddingDegraded locks in the t_5df7921d fix: PUT
// /messages/:id must answer 409 while embedding != ok, instead of deleting
// chunks it cannot rebuild.
//
// NOTE (t_7c865ae4): the handler now answers 202 queued while degraded (see
// TestUpdateQueuesWhileDegraded below); the 409 survives only as the fallback
// when the inbox itself is unusable. This pre-queue test is RETIRED — it is
// kept in source as documentation but skipped, so the suite reflects the
// live contract (202 queued) instead of asserting the old 409.
func TestUpdateRefusesWhileEmbeddingDegraded(t *testing.T) {
	t.Skip("retired by t_7c865ae4: degraded PUT now queues (202), 409 is only the queue-full fallback")
	stop := store.StartEmbeddingProbe(deadEmbedder{})
	defer func() {
		stop()
		stopHealthy := store.StartEmbeddingProbe(healthyEmbedder{})
		time.Sleep(50 * time.Millisecond)
		stopHealthy()
	}()

	deadline := time.Now().Add(3 * time.Second)
	for store.EmbeddingHealth().Status == "unknown" && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if st := store.EmbeddingHealth().Status; st != "degraded" {
		t.Fatalf("probe status = %q, want degraded (guard test needs a dead embedder)", st)
	}

	app := fiber.New()
	h := &MessagesHandler{}
	app.Put("/api/v1/messages/:id", h.UpdateMessageContent)

	body, _ := json.Marshal(map[string]string{
		"workspace_id": "t5df_guard_test",
		"content":      "replacement text that would previously have destroyed the old chunks",
		"session_id":   "guard-test",
		"role":         "user",
	})
	req := httptest.NewRequest("PUT", "/api/v1/messages/deadbeef-0000-0000-0000-000000000000",
		bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req, 2000)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	if resp.StatusCode != fiber.StatusConflict {
		t.Fatalf("status = %d, want 409 - destructive update must be refused while embedding != ok",
			resp.StatusCode)
	}
	var payload map[string]interface{}
	_ = json.NewDecoder(resp.Body).Decode(&payload)
	if payload["embedding"] != "degraded" {
		t.Errorf("embedding field = %v, want degraded", payload["embedding"])
	}
}

// waitForProbe blocks until the probe leaves unknown (seed result arrived).
// Kept for the retired guard test above; live tests use startDeadProbe.
func waitForProbe(t *testing.T, want string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for store.EmbeddingHealth().Status == "unknown" && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if st := store.EmbeddingHealth().Status; st != want {
		t.Fatalf("probe status = %q, want %q", st, want)
	}
}

// startDeadProbe brackets a degraded-window test and restores a sane probe
// state afterwards (same pattern as the retired guard test — the
// package-level probe is shared process state).
//
// Probe-start ordering hazard: StartEmbeddingProbe seeds its first result in
// a fresh goroutine, so back-to-back Start calls from sequential tests race:
// a STALE ok from test N's healthy-restore probe can land AFTER test N+1's
// dead seed, and the 30s ticker never fires fast enough to fix it within the
// test. Mitigation: poll until the status is ACTUALLY degraded (not just
// non-unknown), which proves the dead seed is the last writer. The stopped
// restore probe's ticker is dead, so its stale ok fires at most once —
// polling outlasts it either way.
func startDeadProbe(t *testing.T) func() {
	t.Helper()
	stop := store.StartEmbeddingProbe(deadEmbedder{})
	deadline := time.Now().Add(10 * time.Second)
	for store.EmbeddingHealth().Status != "degraded" && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if st := store.EmbeddingHealth().Status; st != "degraded" {
		stop()
		t.Fatalf("probe status = %q, want degraded (dead seed never landed — stale restore probe won the race)", st)
	}
	return func() {
		stop()
		stopHealthy := store.StartEmbeddingProbe(healthyEmbedder{})
		time.Sleep(50 * time.Millisecond)
		stopHealthy()
	}
}

// TestUpdateQueuesWhileDegraded (t_7c865ae4): a PUT while degraded must be
// accepted as 202 queued (not 409), appear on GET /messages/pending, and
// coalesce re-queues of the same id.
func TestUpdateQueuesWhileDegraded(t *testing.T) {
	defer startDeadProbe(t)()

	app := fiber.New()
	h := &MessagesHandler{}
	app.Put("/api/v1/messages/:id", h.UpdateMessageContent)
	app.Get("/messages/pending", h.ListPendingUpdates)

	put := func(id, content string) (int, map[string]interface{}) {
		t.Helper()
		body, _ := json.Marshal(map[string]string{
			"workspace_id": "t7c_queue_test",
			"content":      content,
			"session_id":   "queue-test",
			"role":         "user",
		})
		req := httptest.NewRequest("PUT", "/api/v1/messages/"+id, bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		resp, err := app.Test(req, 2000)
		if err != nil {
			t.Fatalf("request failed: %v", err)
		}
		var payload map[string]interface{}
		_ = json.NewDecoder(resp.Body).Decode(&payload)
		return resp.StatusCode, payload
	}

	// NOTE: the inbox is package-level shared state; use a unique id per
	// test so parallel/sequential runs cannot coalesce across tests.
	id := "7c865ae4-0000-4000-8000-000000000001"
	st, payload := put(id, "first intent while degraded")
	if st != fiber.StatusAccepted {
		t.Fatalf("status = %d, want 202 (degraded PUT must queue)", st)
	}
	if payload["queued"] != true {
		t.Errorf("queued = %v, want true", payload["queued"])
	}
	if payload["embedding"] != "degraded" {
		t.Errorf("embedding = %v, want degraded", payload["embedding"])
	}

	// Same id re-queued: last-writer-wins, still 202.
	if st, _ := put(id, "second intent supersedes the first"); st != fiber.StatusAccepted {
		t.Fatalf("re-queue status = %d, want 202", st)
	}

	// Visible on the inbox lister, coalesced to one entry with latest content.
	req := httptest.NewRequest("GET", "/messages/pending?workspace_id=t7c_queue_test", nil)
	resp, err := app.Test(req, 2000)
	if err != nil {
		t.Fatalf("pending list failed: %v", err)
	}
	var listed struct {
		Pending []map[string]interface{} `json:"pending"`
		Count   int                      `json:"count"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&listed)
	if listed.Count != 1 {
		t.Fatalf("pending count = %d, want 1 (coalesced)", listed.Count)
	}
	if listed.Pending[0]["content"] != "second intent supersedes the first" {
		t.Errorf("queued content = %v, want last-writer-wins", listed.Pending[0]["content"])
	}
	if listed.Pending[0]["status"] != "pending" {
		t.Errorf("status = %v, want pending", listed.Pending[0]["status"])
	}
	// The queued entry must be keyed by the id the caller PUT to — replay
	// resolves the record through it, so any drift here means "message not
	// found" on recovery (seen live 2026-10-05).
	if mid, _ := listed.Pending[0]["message_id"].(string); mid != id {
		t.Errorf("queued message_id = %q, want %q (queue key must equal the PUT id)", mid, id)
	}

	// Splice shape queues too (sections path, not just full replace).
	secBody, _ := json.Marshal(map[string]interface{}{
		"workspace_id": "t7c_queue_test",
		"sections":     map[string]string{"Limits": "new limits while degraded"},
		"session_id":   "queue-test",
		"role":         "user",
	})
	secReq := httptest.NewRequest("PUT", "/api/v1/messages/7c865ae4-0000-4000-8000-000000000002",
		bytes.NewReader(secBody))
	secReq.Header.Set("Content-Type", "application/json")
	secResp, err := app.Test(secReq, 2000)
	if err != nil {
		t.Fatalf("splice request failed: %v", err)
	}
	if secResp.StatusCode != fiber.StatusAccepted {
		t.Fatalf("splice status = %d, want 202", secResp.StatusCode)
	}

	// Malformed PUTs still 400 while degraded — no queue ticket for junk.
	badBody, _ := json.Marshal(map[string]string{"workspace_id": "t7c_queue_test"})
	badReq := httptest.NewRequest("PUT", "/api/v1/messages/7c865ae4-0000-4000-8000-000000000003",
		bytes.NewReader(badBody))
	badReq.Header.Set("Content-Type", "application/json")
	badResp, err := app.Test(badReq, 2000)
	if err != nil {
		t.Fatalf("bad request failed: %v", err)
	}
	if badResp.StatusCode != fiber.StatusBadRequest {
		t.Errorf("malformed status = %d, want 400", badResp.StatusCode)
	}
}

// TestCreateRefusesFastWhileDegraded (t_7c865ae4): POST /messages while
// degraded must fail fast with 503 + an embedding error class instead of
// hanging through EMBED_TIMEOUT_SECS and storing nothing.
func TestCreateRefusesFastWhileDegraded(t *testing.T) {
	defer startDeadProbe(t)()

	app := fiber.New()
	h := &MessagesHandler{}
	app.Post("/api/v1/messages", h.AddMessage)
	app.Post("/messages/batch", h.AddBatchMessages)

	body, _ := json.Marshal(map[string]string{
		"workspace_id": "t7c_queue_test",
		"content":      "create attempt during outage",
		"session_id":   "queue-test",
		"role":         "user",
	})
	req := httptest.NewRequest("POST", "/api/v1/messages", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req, 2000)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	if resp.StatusCode != fiber.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 (degraded create must fail fast)", resp.StatusCode)
	}
	var payload map[string]interface{}
	_ = json.NewDecoder(resp.Body).Decode(&payload)
	if payload["embedding"] != "degraded" {
		t.Errorf("embedding field = %v, want degraded", payload["embedding"])
	}
	if payload["stored"] != false {
		t.Errorf("stored = %v, want false (explicit not-stored signal)", payload["stored"])
	}

	// Batch create degrades the same way (one 503, not per-message hangs).
	batchBody, _ := json.Marshal(map[string]interface{}{
		"workspace_id": "t7c_queue_test",
		"messages": []map[string]string{
			{"session_id": "queue-test", "role": "user", "content": "batch one"},
		},
	})
	batchReq := httptest.NewRequest("POST", "/messages/batch", bytes.NewReader(batchBody))
	batchReq.Header.Set("Content-Type", "application/json")
	batchResp, err := app.Test(batchReq, 2000)
	if err != nil {
		t.Fatalf("batch request failed: %v", err)
	}
	if batchResp.StatusCode != fiber.StatusServiceUnavailable {
		t.Errorf("batch status = %d, want 503", batchResp.StatusCode)
	}
}

// TestPendingInboxCoalesceAndDrain is the store-level contract: enqueue
// coalesces on message id, the drain applies through the caller's function,
// failures re-queue with attempts, and the cap parks entries as failed.
func TestPendingInboxCoalesceAndDrain(t *testing.T) {
	const ws = "t7c_inbox_unit"
	mkID := func(suffix string) string { return "7c865ae4-1111-4000-8000-000000000" + suffix }

	if _, err := store.EnqueuePendingUpdate(&store.PendingUpdate{
		MessageID: mkID("01") + "x", WorkspaceID: ws, Content: "v1",
	}); err != nil {
		t.Fatalf("enqueue failed: %v", err)
	}
	got, err := store.EnqueuePendingUpdate(&store.PendingUpdate{
		MessageID: mkID("01") + "x", WorkspaceID: ws, Content: "v2",
	})
	if err != nil {
		t.Fatalf("re-enqueue failed: %v", err)
	}
	if got.Content != "v2" || got.Attempts != 0 {
		t.Errorf("coalesce = content %q attempts %d, want v2/0", got.Content, got.Attempts)
	}

	applied, requeued, failed := store.DrainPendingUpdates(func(p *store.PendingUpdate) error {
		return nil
	})
	// NOTE: the inbox is process-shared, so other tests' entries may drain
	// in the same pass — assert at-least, not exact.
	if applied < 1 || requeued != 0 || failed != 0 {
		t.Errorf("drain = %d/%d/%d, want applied>=1 requeued=0 failed=0", applied, requeued, failed)
	}

	// Failure path: always-failing apply re-queues until the cap parks it.
	// The apply fails ONLY failID so other tests' leftover pending entries
	// (shared process inbox) still drain cleanly in the same passes.
	failID := mkID("02") + "x"
	if _, err := store.EnqueuePendingUpdate(&store.PendingUpdate{
		MessageID: failID, WorkspaceID: ws, Content: "doomed",
	}); err != nil {
		t.Fatalf("enqueue doomed failed: %v", err)
	}
	for i := 0; i < store.MaxReplayAttempts; i++ {
		store.DrainPendingUpdates(func(p *store.PendingUpdate) error {
			if p.MessageID == failID {
				return errors.New("boom")
			}
			return nil
		})
	}
	found := false
	for _, p := range store.ListPendingUpdates(ws) {
		if p.MessageID == failID {
			found = true
			if p.Status != "failed" {
				t.Errorf("doomed status = %q, want failed after %d attempts", p.Status, store.MaxReplayAttempts)
			}
			if p.Attempts != store.MaxReplayAttempts {
				t.Errorf("doomed attempts = %d, want %d", p.Attempts, store.MaxReplayAttempts)
			}
		}
	}
	if !found {
		t.Errorf("doomed entry vanished from the inbox (must park as failed, not drop)")
	}
}

// TestDrainReleasesSingleFlightOnEmptyInbox (t_7c865ae4, fix 2026-10-05):
// a drain pass that takes the single-flight slot on an EMPTY inbox must
// release it. It used to leak the slot, so the boot-time "ok" probe wedged
// the drain permanently: every later pass returned without applying, queued
// entries stayed status=pending forever, and the only symptom was the ticker
// logging "embed_status=ok pending_count=1" with no "pending replay" line
// (observed live in the t_7c865ae4 e2e run).
func TestDrainReleasesSingleFlightOnEmptyInbox(t *testing.T) {
	// 1. Drain what earlier tests queued, so the inbox starts empty.
	store.DrainPendingUpdates(func(*store.PendingUpdate) error { return nil })

	// 2. Empty passes are no-ops: nothing applied, nothing requeued, no error.
	for i := 0; i < 3; i++ {
		applied, requeued, failed := store.DrainPendingUpdates(func(*store.PendingUpdate) error {
			t.Fatalf("apply must not run when the inbox is empty")
			return nil
		})
		if applied != 0 || requeued != 0 || failed != 0 {
			t.Fatalf("empty drain pass %d = %d/%d/%d, want 0/0/0", i, applied, requeued, failed)
		}
	}

	// 3. The slot must still be free: a freshly queued entry is applied on the
	// very next pass. With the leaked slot this stays at 0 forever.
	id := "7c865ae4-0000-4000-8000-000000000009"
	if _, err := store.EnqueuePendingUpdate(&store.PendingUpdate{
		MessageID: id, WorkspaceID: "t7c_inbox_unit", Content: "queued after empty passes",
	}); err != nil {
		t.Fatalf("enqueue failed: %v", err)
	}
	targetApplied := 0
	_, requeued, failed := store.DrainPendingUpdates(func(p *store.PendingUpdate) error {
		if p.MessageID == id {
			targetApplied++
		}
		return nil
	})
	if targetApplied != 1 {
		t.Fatalf("drain after empty passes applied the queued entry %d times, want 1 (single-flight slot leaked?)", targetApplied)
	}
	if requeued != 0 || failed != 0 {
		t.Errorf("requeued=%d failed=%d, want 0/0 for a healthy apply", requeued, failed)
	}
}

// TestEnqueuePendingUpdateOwnsStrings (t_7c865ae4, fix 2026-10-05): Fiber's
// c.Params()/c.Get() hand out unsafe strings that alias the pooled ctx path
// and header buffers (Immutable was off), and the inbox stored them past the
// request. Reproduced by enqueuing an unsafe view of a []byte and then
// overwriting that byte slice the way fasthttp does when the next request
// reuses the buffer: the 202 already reported the original id to the caller,
// so the queued entry must still carry it. Without clonePendingUpdate the
// entry reads back "pending2-2222-…", which is exactly what made the live
// replay answer "message not found" in the t_7c865ae4 e2e run.
func TestEnqueuePendingUpdateOwnsStrings(t *testing.T) {
	const ws = "t7c_alias_unit"
	const original = "deadbeef-1111-4000-8000-000000000009"

	buf := []byte(original)
	aliased := unsafe.String(unsafe.SliceData(buf), len(buf)) // shares buf, like Fiber's getString

	if _, err := store.EnqueuePendingUpdate(&store.PendingUpdate{
		MessageID: aliased, WorkspaceID: ws, Content: "queued value",
	}); err != nil {
		t.Fatalf("enqueue failed: %v", err)
	}

	// The following request reuses the pooled path buffer: GET
	// /messages/pending writes "pending" over the first 7 bytes while the
	// tail still holds the previous request's id.
	copy(buf, "pending2-2222-4000-8000-000000000002")

	found, corrupted := false, ""
	for _, p := range store.ListPendingUpdates(ws) {
		switch {
		case p.MessageID == original:
			found = true
		case strings.HasPrefix(p.MessageID, "pending2-"):
			corrupted = p.MessageID
		}
	}
	if corrupted != "" {
		t.Errorf("queued message_id was rewritten to %q — the inbox aliases the caller's buffer", corrupted)
	}
	if !found {
		t.Fatalf("pending entry for %s not found with its original id after enqueue", original)
	}
	store.DrainPendingUpdates(func(*store.PendingUpdate) error { return nil })
}
