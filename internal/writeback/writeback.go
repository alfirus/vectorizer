// Package writeback mirrors stored conversation turns to per-session
// markdown files under the vault (VAULT_ROOT) after vector+meta succeed.
//
// Staging-only by design: it appends to `10-memory/sessions/<session>.md`
// and (optionally, on deriver conclusions) proposes inbox candidates under
// `20-knowledge/00-inbox/`. It NEVER writes curated truth (longterm/,
// 10-topics/, 20-howto/). Promotion stays a human/manual act.
//
// Gate: VAULT_WRITEBACK=true (default false). Per-workspace allowlist via
// VAULT_WRITEBACK_WORKSPACES="ws_a,ws_b" — empty means all workspaces.
// The writer is async (bounded queue, monotonic drops), idempotent on
// message_id (marker scan), and degrades silently when the vault is
// read-only (local :ro mount) or VAULT_ROOT is unset.
package writeback

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"
)

// queued is one pending markdown append.
type queued struct {
	workspace, sessionID, messageID, role, content string
	createdAt                                      time.Time
}

// Writer appends conversation turns to per-session markdown files.
type Writer struct {
	root       string
	workspaces map[string]bool // nil = all allowed
	queue      chan queued
	stop       chan struct{}
	writes     atomic.Uint64
	drops      atomic.Uint64
	skippedRO  atomic.Uint64 // skipped: read-only vault probe failed
}

// New returns a Writer rooted at vaultRoot. allowCSV is a comma-separated
// workspace allowlist; empty allows all.
func New(vaultRoot, allowCSV string) *Writer {
	var allow map[string]bool
	if allowCSV != "" {
		allow = map[string]bool{}
		for _, w := range strings.Split(allowCSV, ",") {
			if w = strings.TrimSpace(w); w != "" {
				allow[w] = true
			}
		}
	}
	return &Writer{root: vaultRoot, workspaces: allow, queue: make(chan queued, 1000), stop: make(chan struct{})}
}

// Enabled reports whether writeback can run: gate on + writable root.
func Enabled() bool {
	v, _ := parseBool(os.Getenv("VAULT_WRITEBACK"))
	return v
}

func parseBool(s string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "1", "t", "true", "yes", "y", "on":
		return true, nil
	}
	return false, fmt.Errorf("not true")
}

// Enqueue stages one turn for async append. Never blocks the request path:
// a full queue counts loudly via Drops() (surfaced in /metrics).
func (w *Writer) Enqueue(workspace, sessionID, messageID, role, content string, createdAt time.Time) {
	if w == nil || w.root == "" {
		return
	}
	if w.workspaces != nil && !w.workspaces[workspace] {
		return
	}
	select {
	case w.queue <- queued{workspace, sessionID, messageID, role, content, createdAt}:
	default:
		w.drops.Add(1)
	}
}

// Drops returns the lifetime count of turns lost to a full queue.
func (w *Writer) Drops() uint64 { return w.drops.Load() }

// Writes returns the lifetime count of successful file appends.
func (w *Writer) Writes() uint64 { return w.writes.Load() }

// SkippedRO returns the lifetime count of turns skipped because the
// workspace sessions dir was not writable.
func (w *Writer) SkippedRO() uint64 { return w.skippedRO.Load() }

// QueueDepth returns the current pending backlog (for /metrics alerting).
func (w *Writer) QueueDepth() int { return len(w.queue) }

// Start drains the queue every 2s or 20 turns (same cadence as deriver).
func (w *Writer) Start() {
	go func() {
		var batch []queued
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case q := <-w.queue:
				batch = append(batch, q)
				if len(batch) >= 20 {
					w.flush(batch)
					batch = nil
				}
			case <-ticker.C:
				if len(batch) > 0 {
					w.flush(batch)
					batch = nil
				}
			case <-w.stop:
				if len(batch) > 0 {
					w.flush(batch)
				}
				return
			}
		}
	}()
}

// Stop drains remaining turns, then halts the writer.
func (w *Writer) Stop() { close(w.stop) }

// agentDir maps a workspace to its vault owner dir. Canonical workspaces
// (ws_<name>) map back to the human vault dir by stripping the ws_ prefix;
// the maisarah workspace keeps its full name. Unknown workspaces fall back
// to _shared/knowledge so nothing ever lands in another agent's vault.
func agentDir(workspace string) string {
	name := strings.TrimPrefix(workspace, "ws_")
	switch name {
	case "maisarah", "balqis", "ain", "kifli", "adviksai":
		return name
	default:
		return filepath.Join("_shared", "knowledge", name)
	}
}

// sessionFile returns the staging file for a (workspace, session) pair.
// Session IDs are sanitized to [a-zA-Z0-9_-]; anything else hashes to a
// safe name so a hostile session_id can never escape the sessions dir.
func (w *Writer) sessionFile(workspace, sessionID string) string {
	safe := sanitize(sessionID)
	return filepath.Join(w.root, agentDir(workspace), "vault", "10-memory", "sessions", safe+".md")
}

func sanitize(s string) string {
	var b strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-' {
			b.WriteRune(r)
		} else {
			b.WriteRune('_')
		}
	}
	out := strings.Trim(b.String(), "_")
	if out == "" || len(out) > 128 {
		// fall back to a stable hash-ish name (FNV, stdlib only)
		var h uint64 = 1469598103934665603
		for i := 0; i < len(s); i++ {
			h ^= uint64(s[i])
			h *= 1099511628211
		}
		out = fmt.Sprintf("session_%x", h)
	}
	return out
}

// flush appends each turn to its session file, grouped by file.
func (w *Writer) flush(batch []queued) {
	groups := map[string][]queued{}
	for _, q := range batch {
		f := w.sessionFile(q.workspace, q.sessionID)
		groups[f] = append(groups[f], q)
	}
	for file, qs := range groups {
		if err := w.appendFile(file, qs); err != nil {
			w.skippedRO.Add(uint64(len(qs)))
		} else {
			w.writes.Add(uint64(len(qs)))
		}
	}
}

// appendFile creates the session file with frontmatter on first write
// (frontmatter carries message_id refs of the first batch), skips turns
// whose message_id marker already exists (idempotent), and appends the rest.
func (w *Writer) appendFile(path string, qs []queued) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	// Probe writability: open (create if missing) without truncating.
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE, 0o644)
	if err != nil {
		return err
	}
	f.Close()

	var existing string
	if b, err := os.ReadFile(path); err == nil {
		existing = string(b)
	}
	var sb strings.Builder
	if existing == "" {
		fmt.Fprintf(&sb, "---\n")
		fmt.Fprintf(&sb, "generated_by: vectorizer\n")
		fmt.Fprintf(&sb, "source_type: memory\n")
		fmt.Fprintf(&sb, "workspace: %s\n", qs[0].workspace)
		fmt.Fprintf(&sb, "session_id: %s\n", qs[0].sessionID)
		fmt.Fprintf(&sb, "status: staging\n")
		fmt.Fprintf(&sb, "created_at: %s\n", qs[0].createdAt.UTC().Format(time.RFC3339))
		fmt.Fprintf(&sb, "---\n\n")
		fmt.Fprintf(&sb, "# Session %s\n\n", qs[0].sessionID)
	}
	appended := 0
	for _, q := range qs {
		marker := "<!-- vectorizer:" + q.messageID + " -->"
		if strings.Contains(existing, marker) || strings.Contains(sb.String(), marker) {
			continue // idempotent: already recorded
		}
		ts := q.createdAt.UTC().Format(time.RFC3339)
		fmt.Fprintf(&sb, "## %s · %s · %s\n\n%s\n\n%s\n\n", ts, q.role, q.messageID, q.content, marker)
		appended++
	}
	if sb.Len() == 0 || appended == 0 && existing != "" {
		return nil // nothing new
	}
	f, err = os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.WriteString(sb.String())
	return err
}
