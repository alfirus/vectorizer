package store

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/alfirus/vectorizer/internal/models"
)

// Section-splice updates (set_sections semantics).
// Why: UpdateMessage today is delete + re-add of the WHOLE message. For long
// vault docs (MEMORY.md, project notes) that means re-embedding thousands of
// unchanged tokens to change one paragraph — and chunk IDs shift for every
// chunk after the edit. Splice semantics rewrite only the named `## sections`
// (markdown H2), preserve everything else byte-for-byte, and only re-embed
// the chunks that actually changed. Retries with identical sections are
// no-ops (idempotent): if the section body is already equal, nothing is
// rewritten and no new embedding is spent.

var h2Re = regexp.MustCompile(`(?m)^(##\s+.+?)\s*$`)

// Section is one `## Heading` block: heading line + body up to the next H2.
type Section struct {
	Heading string // the full "## ..." line
	Body    string // everything after the heading until next H2 (or EOF)
}

// SplitSections parses markdown into (preamble, sections). Preamble is text
// before the first H2 (may be empty). Headings compare case-insensitively
// with ATX closers (`## X ##`) and extra whitespace normalized.
func SplitSections(md string) (preamble string, sections []Section) {
	locs := h2Re.FindAllStringIndex(md, -1)
	if len(locs) == 0 {
		return md, nil
	}
	preamble = md[:locs[0][0]]
	for i, loc := range locs {
		end := len(md)
		if i+1 < len(locs) {
			end = locs[i+1][0]
		}
		block := md[loc[0]:end]
		nl := strings.Index(block, "\n")
		var head, body string
		if nl < 0 {
			head, body = strings.TrimRight(block, "\n"), ""
		} else {
			head, body = block[:nl], block[nl+1:]
		}
		sections = append(sections, Section{Heading: head, Body: body})
	}
	return preamble, sections
}

// normHeading canonicalizes a heading for comparison: strip #'s, closers,
// collapse whitespace, lowercase.
func normHeading(h string) string {
	h = strings.Trim(h, "# \t")
	// strip ATX closer: "X ##" → "X"
	h = strings.TrimRight(strings.TrimSpace(h), "#")
	h = strings.Join(strings.Fields(h), " ")
	return strings.ToLower(h)
}

// SpliceSections returns the doc with named sections replaced. Keys match
// headings case-insensitively ("Limits" matches "## Limits"). Unknown keys
// are APPENDED as new `## Key` sections (upsert, not error) — matches
// codebase-memory-mcp set_sections behavior. changed[i] reports whether
// section i was actually modified (drives idempotent no-op + selective
// re-embed). sameContent reports full-doc equality for the fast path.
func SpliceSections(md string, updates map[string]string) (out string, changed []bool, sameContent bool) {
	pre, secs := SplitSections(md)
	if len(secs) == 0 && pre != "" {
		// No H2 structure: treat whole doc as one body. Single-key updates
		// replace everything; multi-key is ambiguous → error handled by caller.
		if len(updates) == 1 {
			for _, v := range updates {
				if v == pre {
					return md, []bool{false}, true
				}
				return v, []bool{true}, false
			}
		}
		return md, nil, true // caller rejects multi-key on structureless docs
	}
	byNorm := map[string]int{}
	for i, s := range secs {
		byNorm[normHeading(s.Heading)] = i
	}
	changed = make([]bool, len(secs))
	appended := []Section{}
	for k, v := range updates {
		nk := normHeading("## " + k)
		if idx, ok := byNorm[nk]; ok {
			// Compare whitespace-insensitively: stored bodies retain the
			// blank line before the next H2, callers shouldn't need to.
			if strings.TrimSpace(secs[idx].Body) == strings.TrimSpace(v) {
				continue // idempotent no-op for this section
			}
			// Preserve the original trailing separator style: keep the
			// stored body's trailing newlines, replace the inner text.
			trail := secs[idx].Body[len(strings.TrimRight(secs[idx].Body, "\n")):]
			secs[idx].Body = strings.TrimRight(v, "\n") + trail
			if secs[idx].Body == "" || !strings.HasSuffix(secs[idx].Body, "\n") {
				secs[idx].Body += "\n"
			}
			changed[idx] = true
		} else {
			appended = append(appended, Section{Heading: "## " + strings.TrimSpace(k), Body: v})
		}
	}
	var b strings.Builder
	b.WriteString(pre)
	for _, s := range secs {
		b.WriteString(s.Heading + "\n" + s.Body)
	}
	for _, s := range appended {
		if !strings.HasSuffix(b.String(), "\n") {
			b.WriteString("\n")
		}
		body := s.Body
		if body != "" && !strings.HasSuffix(body, "\n") {
			body += "\n"
		}
		b.WriteString(s.Heading + "\n" + body)
		changed = append(changed, true)
	}
	out = b.String()
	sameContent = out == md
	return out, changed, sameContent
}

// SpliceResult reports a section-splice update.
type SpliceResult struct {
	Updated          bool     `json:"updated"`
	Noop             bool     `json:"noop,omitempty"`
	Sections         []string `json:"sections_updated,omitempty"`
	ChunksReembedded int      `json:"chunks_reembedded"`
	MessageID        string   `json:"id"`
	WorkspaceID      string   `json:"workspace_id"`
}

// UpdateMessageSections splices named `## sections` into a stored message
// and re-embeds ONLY the chunks that changed. Full-doc flow:
//  1. Reassemble current content from stored chunks (ordered by chunk_index).
//  2. SpliceSections → new doc. Identical → Noop, zero embedding spend.
//  3. Otherwise diff old vs new chunk lists; upsert changed chunks only.
//     (Unchanged chunks keep IDs + embeddings; added chunks get fresh IDs;
//     removed tail chunks are deleted.)
// Message ID stays stable throughout — trace/reasoning references survive.
func (s *Store) UpdateMessageSections(workspaceID, messageID string, updates map[string]string, meta map[string]interface{}) (*SpliceResult, error) {
	res := &SpliceResult{MessageID: messageID, WorkspaceID: workspaceID}
	chunks, err := s.GetMessageChunks(workspaceID, messageID)
	if err != nil {
		return nil, err
	}
	if len(chunks) == 0 {
		return nil, fmt.Errorf("message not found")
	}
	// Order chunks + recover base metadata from chunk 0.
	sort.Slice(chunks, func(i, j int) bool {
		mi, _ := chunks[i]["metadata"].(map[string]interface{})
		mj, _ := chunks[j]["metadata"].(map[string]interface{})
		return toFloat(mi["chunk_index"]) < toFloat(mj["chunk_index"])
	})
	var b strings.Builder
	var baseMeta map[string]interface{}
	for _, ch := range chunks {
		if doc, ok := ch["document"].(string); ok {
			b.WriteString(doc)
		}
		if baseMeta == nil {
			if m, ok := ch["metadata"].(map[string]interface{}); ok {
				baseMeta = m
			}
		}
	}
	current := b.String()
	newDoc, changed, same := SpliceSections(current, updates)
	_ = changed
	if same {
		res.Updated = true
		res.Noop = true
		return res, nil
	}

	// Chunk both versions; diff.
	oldChunks := chunkText(current, maxChunkSize)
	newChunks := chunkText(newDoc, maxChunkSize)
	collName := s.GetCollectionName(workspaceID)
	coll, err := s.chroma.GetCollection(collName)
	if err != nil {
		return nil, fmt.Errorf("get collection: %w", err)
	}
	// Recover role/session/scope for re-embedded chunks.
	role, _ := baseMeta["role"].(string)
	sessionID, _ := baseMeta["session_id"].(string)
	if role == "" {
		role = "user"
	}
	msg := &models.Message{ID: messageID, WorkspaceID: workspaceID, SessionID: sessionID, Role: role, CreatedAt: time.Now().UTC(), Metadata: meta}

	var upIDs, upDocs []string
	var upMetas []map[string]interface{}
	updatedSections := []string{}
	for k := range updates {
		updatedSections = append(updatedSections, k)
	}
	sort.Strings(updatedSections)
	reembedded := 0
	for i, nc := range newChunks {
		id := fmt.Sprintf("%s_chunk_%d", messageID, i)
		var old string
		if i < len(oldChunks) {
			old = oldChunks[i]
		}
		if nc == old {
			continue // unchanged chunk: keep stored embedding
		}
		m := map[string]interface{}{
			"message_id": messageID, "session_id": sessionID, "workspace_id": workspaceID,
			"role": role, "created_at": baseMeta["created_at"],
			"chunk_index": i, "total_chunks": len(newChunks),
			"content_hash": contentHash(workspaceID, sessionID, newDoc),
		}
		for _, k := range []string{"scope", "peer_id", "peer_ids", "source_type", "source_path", "header_path", "chunk_type", "tags", "importance", "agent", "language", "parent_doc_id", "doc_title", "file_hash", "entities", "summary_1line"} {
			if v, ok := baseMeta[k]; ok && v != nil && v != "" {
				m[k] = v
			}
		}
		upIDs = append(upIDs, id)
		upDocs = append(upDocs, nc)
		upMetas = append(upMetas, m)
		reembedded++
	}
	if len(upIDs) > 0 {
		embs, err := s.embed.Embed(upDocs)
		if err != nil {
			return nil, fmt.Errorf("embed changed chunks: %w", err)
		}
		vecs := make([][]float32, len(embs))
		for i, e := range embs {
			vecs[i] = e.Vector
		}
		var lastErr error
		for attempt := 0; attempt < 3; attempt++ {
			if err := s.chroma.UpsertDocuments(coll.ID, upIDs, upDocs, upMetas, vecs); err == nil {
				lastErr = nil
				break
			} else {
				lastErr = err
			}
			time.Sleep(time.Duration(100*(1<<attempt)) * time.Millisecond)
		}
		if lastErr != nil {
			return nil, fmt.Errorf("upsert changed chunks: %w", lastErr)
		}
	}
	// Delete removed tail chunks (doc shrank).
	if len(newChunks) < len(oldChunks) {
		var tail []string
		for i := len(newChunks); i < len(oldChunks); i++ {
			tail = append(tail, fmt.Sprintf("%s_chunk_%d", messageID, i))
		}
		_ = s.chroma.DeleteDocuments(coll.ID, tail)
	}
	_ = msg
	res.Updated = true
	res.Sections = updatedSections
	res.ChunksReembedded = reembedded
	return res, nil
}
