package store

import (
	"fmt"
	"strings"
	"time"

	"github.com/alfirus/vectorizer/internal/models"
)

// sidecarSuffixes are the Chroma collection suffixes used by per-workspace
// sidecar collections (conclusions, reasoning edges, code-graph edges). They
// are NOT workspaces: ws_<ws>_conclusions (conclusions.go), ws_<ws>_reasoning
// and ws_<ws>_codeedges (reasoning.go) all store rows stamped with the PARENT
// workspace id, so listing one as a workspace yields a phantom id whose
// filtered reads always return 0 rows (t_5f0d1cb0).
var sidecarSuffixes = []string{"_conclusions", "_reasoning", "_codeedges"}

// isSidecarCollection reports whether a Chroma collection name is a workspace
// sidecar rather than a workspace itself. have is the full set of collection
// names from the same ListCollections call: a name is only treated as a
// sidecar when its parent collection (ws_<base>) exists alongside it, so a
// real workspace that merely happens to end in a sidecar suffix is never
// hidden — it stays listed (and health flags it if it is unreadable).
func isSidecarCollection(name string, have map[string]bool) bool {
	if !strings.HasPrefix(name, "ws_") {
		return false
	}
	for _, suf := range sidecarSuffixes {
		if strings.HasSuffix(name, suf) {
			base := strings.TrimSuffix(name, suf)
			if base == "ws_" {
				return true // degenerate: no workspace name left
			}
			return have[base]
		}
	}
	return false
}

func (s *Store) ListWorkspaces() ([]string, error) {
	colls, err := s.chroma.ListCollections()
	if err != nil {
		return nil, err
	}
	have := make(map[string]bool, len(colls))
	for _, c := range colls {
		have[c.Name] = true
	}
	var out []string
	for _, c := range colls {
		if !strings.HasPrefix(c.Name, "ws_") {
			continue
		}
		if isSidecarCollection(c.Name, have) {
			continue
		}
		out = append(out, strings.TrimPrefix(c.Name, "ws_"))
	}
	return out, nil
}

// CountReadableRows counts the rows actually readable under the workspace
// filter (where workspace_id=<id>) in the workspace collection. This is the
// ground truth workspace health must assert: GetWorkspaceStats reports the
// RAW collection count with no filter, so for a phantom id it echoes the
// sidecar rows while every filtered read path returns 0 (t_5f0d1cb0).
// A missing collection is 0 rows, not an error (workspace simply empty/new).
func (s *Store) CountReadableRows(workspaceID string) (int, error) {
	coll, err := s.chroma.GetCollection(s.GetCollectionName(workspaceID))
	if err != nil {
		return 0, nil
	}
	docs, err := s.chroma.GetDocuments(coll.ID, map[string]interface{}{"workspace_id": workspaceID}, 0, 0)
	if err != nil {
		return 0, err
	}
	return len(docs), nil
}

func (s *Store) GetMessages(workspaceID, sessionID string, limit, offset int) ([]map[string]interface{}, error) {
	collName := s.GetCollectionName(workspaceID)
	coll, err := s.chroma.GetCollection(collName)
	if err != nil {
		return []map[string]interface{}{}, nil
	}
	where := map[string]interface{}{"workspace_id": workspaceID}
	if sessionID != "" {
		where["session_id"] = sessionID
	}
	// Use Get endpoint via chroma
	docs, err := s.chroma.GetDocuments(coll.ID, where, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("get messages: %w", err)
	}
	return docs, nil
}

// bm25Where builds the server-side filter for the BM25 doc-scan leg of
// HybridSearchWithScope: workspace (+ session when set, matching
// GetMessages) plus a peer_id $ne codeindex clause so the scan window is
// spent on content rows, not code-graph edges. chroma normalizeWhere wraps
// the multi-key map in $and and passes the $ne value through untouched.
func bm25Where(workspaceID, sessionID string) map[string]interface{} {
	where := map[string]interface{}{"workspace_id": workspaceID}
	if sessionID != "" {
		where["session_id"] = sessionID
	}
	where["peer_id"] = map[string]interface{}{"$ne": "codeindex"}
	return where
}

// getBM25Docs fetches candidate docs for the BM25 leg with edges excluded
// server-side. Same collection lookup as GetMessages; limit applies to
// MATCHING rows, so a generous window costs only content rows back.
func (s *Store) getBM25Docs(workspaceID, sessionID string, limit int) ([]map[string]interface{}, error) {
	collName := s.GetCollectionName(workspaceID)
	coll, err := s.chroma.GetCollection(collName)
	if err != nil {
		return []map[string]interface{}{}, nil
	}
	docs, err := s.chroma.GetDocuments(coll.ID, bm25Where(workspaceID, sessionID), limit, 0)
	if err != nil {
		return nil, fmt.Errorf("get bm25 docs: %w", err)
	}
	return docs, nil
}

// DeleteMessage removes every chunk carrying message_id (all chunk_N parts).
// Returns the number of chunks deleted. Wrong facts must be forgettable —
// without this, a confabulated auto-stored conclusion is immortal.
func (s *Store) DeleteMessage(workspaceID, messageID string) (int, error) {
	collName := s.GetCollectionName(workspaceID)
	coll, err := s.chroma.GetCollection(collName)
	if err != nil {
		return 0, fmt.Errorf("get collection: %w", err)
	}
	existing, err := s.chroma.GetDocuments(coll.ID, map[string]interface{}{"message_id": messageID}, 0, 0)
	if err != nil {
		return 0, fmt.Errorf("lookup chunks: %w", err)
	}
	if len(existing) == 0 {
		return 0, nil
	}
	ids := make([]string, 0, len(existing))
	for _, d := range existing {
		if id, ok := d["id"].(string); ok && id != "" {
			ids = append(ids, id)
		}
	}
	if err := s.chroma.DeleteDocuments(coll.ID, ids); err != nil {
		return 0, fmt.Errorf("delete chunks: %w", err)
	}
	return len(ids), nil
}

// UpdateMessage replaces a message's content: write the NEW chunks first, then
// prune the chunk ids the new content no longer covers. Returns the new chunk
// count. This is the correction path for wrong memories.
//
// Order matters. The old implementation called DeleteMessage BEFORE AddMessage,
// so every old chunk was destroyed before the embedder was asked for a vector: a
// dead embedder meant "record gone, replacement never stored" (t_0927b244,
// 2026-09-27 - 3 records lost: balqis 7->6, maisarah 869->867). AddMessage embeds
// before it writes, so a failure now returns an error while the previous record
// is still fully intact and retryable. HTTP callers are additionally refused
// up-front by the handler guard (UpdateMessageContent) when the embed probe
// reports anything other than ok.
func (s *Store) UpdateMessage(msg *models.Message, content string) (int, error) {
	chunks := chunkText(content, maxChunkSize)
	// Count BEFORE writing so tail chunks the new version drops can be pruned.
	prev, err := s.CountMessageChunks(msg.WorkspaceID, msg.ID)
	if err != nil {
		return 0, err
	}
	// AddMessage embeds first and only then upserts: an embed failure here
	// leaves every previous chunk in place (nothing has been deleted yet).
	if err := s.AddMessage(msg, content); err != nil {
		return 0, err
	}
	if n, cerr := s.CountMessageChunks(msg.WorkspaceID, msg.ID); cerr == nil && n == 0 {
		// AddMessage silently skips work it considers a duplicate; that must not
		// report success for an update that stored nothing. Surface it instead.
		return 0, fmt.Errorf("update stored no chunks (content dedup skipped the write)")
	}
	if prev > len(chunks) {
		// Content shrank: drop the now-stale tail chunks (their ids are the same
		// ones AddMessage would have overwritten had the content grown).
		coll, gerr := s.chroma.GetCollection(s.GetCollectionName(msg.WorkspaceID))
		if gerr != nil {
			return len(chunks), fmt.Errorf("stale-chunk prune skipped: %w", gerr)
		}
		stale := make([]string, 0, prev-len(chunks))
		for i := len(chunks); i < prev; i++ {
			stale = append(stale, fmt.Sprintf("%s_chunk_%d", msg.ID, i))
		}
		if derr := s.chroma.DeleteDocuments(coll.ID, stale); derr != nil {
			return len(chunks), fmt.Errorf("prune stale chunks: %w", derr)
		}
	}
	return len(chunks), nil
}

// GetMessageChunks fetches every stored chunk for one message ID.
func (s *Store) GetMessageChunks(workspaceID, messageID string) ([]map[string]interface{}, error) {
	collName := s.GetCollectionName(workspaceID)
	coll, err := s.chroma.GetCollection(collName)
	if err != nil {
		return nil, fmt.Errorf("get collection: %w", err)
	}
	return s.chroma.GetDocuments(coll.ID, map[string]interface{}{"message_id": messageID}, 0, 0)
}

// QueryByMetadata fetches raw docs from a workspace collection by metadata
// filter (code/symbol lookups, hash-skip probes). Thin wrapper over
// Chroma GetDocuments — no embedding spend.
func (s *Store) QueryByMetadata(workspaceID string, where map[string]interface{}, limit int) ([]map[string]interface{}, error) {
	coll, err := s.chroma.GetCollection(s.GetCollectionName(workspaceID))
	if err != nil {
		return nil, fmt.Errorf("get collection: %w", err)
	}
	if limit <= 0 {
		limit = 100
	}
	return s.chroma.GetDocuments(coll.ID, where, limit, 0)
}

// CountMessageChunks counts stored chunks for one message ID (cheap
// existence probe used by workspace resolution).
func (s *Store) CountMessageChunks(workspaceID, messageID string) (int, error) {
	docs, err := s.GetMessageChunks(workspaceID, messageID)
	if err != nil {
		return 0, err
	}
	return len(docs), nil
}

func parseDate(s string) (time.Time, bool) {
	if s == "" {
		return time.Time{}, false
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}, false
	}
	return t, true
}

func (s *Store) Grep(workspaceID, sessionID, q string, limit int) ([]map[string]interface{}, error) {
	docs, err := s.GetMessages(workspaceID, sessionID, 200, 0)
	if err != nil { return nil, err }
	q = strings.ToLower(q)
	var out []map[string]interface{}
	for _, d := range docs {
		doc, _ := d["document"].(string)
		if strings.Contains(strings.ToLower(doc), q) { out=append(out, d); if len(out)>=limit { break } }
	}
	return out, nil
}

func (s *Store) TTLDelete(workspaceID string, olderThan string) (int, error) {
	t, ok := parseDate(olderThan)
	if !ok { return 0, fmt.Errorf("invalid date") }
	collName := s.GetCollectionName(workspaceID)
	coll, err := s.chroma.GetCollection(collName)
	if err != nil { return 0, err }
	docs, _ := s.chroma.GetDocuments(coll.ID, map[string]interface{}{"workspace_id": workspaceID}, 1000, 0)
	var ids []string
	for _, d := range docs {
		m, _ := d["metadata"].(map[string]interface{})
		raw, _ := m["created_at"].(string)
		if pt, ok2:=parseDate(raw); ok2 && pt.Before(t) { ids=append(ids, fmt.Sprint(d["id"])) }
	}
	if len(ids)>0 { _ = s.chroma.DeleteDocuments(coll.ID, ids) }
	return len(ids), nil
}

func (s *Store) SaveSessionMeta(workspaceID, sessionID string, peerIDs []string, scope string) error {
	collName := s.GetCollectionName(workspaceID)
	coll, err := s.chroma.EnsureCollection(collName, map[string]interface{}{"workspace_id": workspaceID})
	if err != nil { return err }
	meta := map[string]interface{}{"workspace_id": workspaceID, "session_id": sessionID, "type": "session_meta", "created_at": time.Now().UTC().Format(time.RFC3339)}
	if len(peerIDs)>0 { meta["peer_ids"]=strings.Join(peerIDs, ",") }
	if scope!="" { meta["scope"]=scope }
	// Upsert zero-vector marker (chroma requires embeddings; use dummy 1536d for Qwen3)
	dummy := s.dummyVector()
	return s.chroma.UpsertDocuments(coll.ID, []string{"session_meta_"+sessionID}, []string{""}, []map[string]interface{}{meta}, [][]float32{dummy})
}
func (s *Store) ListSessions(workspaceID string) ([]map[string]interface{}, error) {
	collName := s.GetCollectionName(workspaceID)
	coll, err := s.chroma.GetCollection(collName)
	if err != nil { return []map[string]interface{}{}, nil }
	return s.chroma.GetDocuments(coll.ID, map[string]interface{}{"type": "session_meta"}, 100, 0)
}

// SearchWithOptions adds pagination + date range.
func (s *Store) SearchWithOptions(query, workspaceID, sessionID, role string, nResults, offset int, after, before string) ([]map[string]interface{}, int, error) {
	results, err := s.Search(query, workspaceID, sessionID, role, nResults+offset)
	if err != nil {
		return nil, 0, err
	}
	afterT, hasAfter := parseDate(after)
	beforeT, hasBefore := parseDate(before)
	filtered := results[:0]
	for _, r := range results {
		if hasAfter || hasBefore {
			raw, _ := r.Metadata["created_at"].(string)
			t, ok := parseDate(raw)
			if !ok {
				continue
			}
			if hasAfter && t.Before(afterT) {
				continue
			}
			if hasBefore && t.After(beforeT) {
				continue
			}
		}
		filtered = append(filtered, r)
	}
	total := len(filtered)
	if offset >= total {
		return []map[string]interface{}{}, total, nil
	}
	end := offset + nResults
	if end > total {
		end = total
	}
	page := filtered[offset:end]
	out := make([]map[string]interface{}, len(page))
	for i, r := range page {
		out[i] = map[string]interface{}{"id": r.ID, "document": r.Document, "metadata": r.Metadata, "distance": r.Distance}
	}
	return out, total, nil
}
