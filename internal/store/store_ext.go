package store

import (
	"fmt"
	"strings"
	"time"

	"github.com/alfirus/vectorizer/internal/models"
)

func (s *Store) ListWorkspaces() ([]string, error) {
	colls, err := s.chroma.ListCollections()
	if err != nil {
		return nil, err
	}
	var out []string
	for _, c := range colls {
		if strings.HasPrefix(c.Name, "ws_") {
			out = append(out, strings.TrimPrefix(c.Name, "ws_"))
		}
	}
	return out, nil
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

// UpdateMessage replaces a message's content: delete all old chunks, then
// re-add with the same message ID (fresh embedding + chunking). Returns the
// new chunk count. This is the correction path for wrong memories.
func (s *Store) UpdateMessage(msg *models.Message, content string) (int, error) {
	if _, err := s.DeleteMessage(msg.WorkspaceID, msg.ID); err != nil {
		return 0, err
	}
	if err := s.AddMessage(msg, content); err != nil {
		return 0, err
	}
	chunks := chunkText(content, maxChunkSize)
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
