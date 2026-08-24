package store

import (
	"fmt"
	"strings"
	"time"
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
