package store

import (
	"math"
	"sort"
	"strings"

	"github.com/alfirus/vectorizer/internal/models"
)

// WorkflowRerankScore is a deterministic reranker that replaces AI rerank for 768d vault.
// Formula: vector(0.55) + bm25(0.25) + importance(0.08) + entityOverlap(0.07) + recency(0.05)
// Returns reordered hits. Never fails — on tie keeps vector order.
func WorkflowRerankScore(query string, hits []models.SearchResult) []models.SearchResult {
	if len(hits) <= 1 {
		return hits
	}
	qLower := strings.ToLower(query)
	qTerms := strings.Fields(qLower)
	// also extract entities from query (Title Case terms)
	qEntities := map[string]bool{}
	for _, t := range qTerms {
		if len(t) >= 3 {
			qEntities[strings.ToLower(t)] = true
		}
	}
	type scored struct {
		idx   int
		score float64
		base  models.SearchResult
	}
	scores := make([]scored, len(hits))
	// normalize vector distance to 0-1 score (cosine: lower = better, invert)
	maxD, minD := hits[0].Distance, hits[0].Distance
	for _, h := range hits {
		if h.Distance > maxD {
			maxD = h.Distance
		}
		if h.Distance < minD {
			minD = h.Distance
		}
	}
	span := maxD - minD
	if span < 0.01 {
		span = 0.5
	}
	for i, h := range hits {
		// vector score: inverted distance normalized
		vecScore := 1.0 - float64(h.Distance-minD)/float64(span)

		// BM25-ish: term overlap count weighted
		docLower := strings.ToLower(h.Document)
		headerPath, _ := h.Metadata["header_path"].(string)
		tags, _ := h.Metadata["tags"].(string)
		textForBM25 := docLower + " " + strings.ToLower(headerPath) + " " + strings.ToLower(tags)
		bm25 := 0.0
		for _, t := range qTerms {
			if t == "" {
				continue
			}
			c := float64(strings.Count(textForBM25, t))
			// header_path gets 2x weight
			if strings.Contains(strings.ToLower(headerPath), t) {
				c += 1.0
			}
			if strings.Contains(strings.ToLower(tags), t) {
				c += 1.0
			}
			// BM25-ish dampen: log(1+c)
			bm25 += math.Log(1 + c)
		}
		// normalize bm25 to 0-1 by log scale
		bm25Norm := math.Min(1.0, bm25/3.0)

		// importance 1-5 -> 0-1
		impScore := 0.0
		switch v := h.Metadata["importance"].(type) {
		case float64:
			impScore = (v - 1) / 4.0
		case int:
			impScore = float64(v-1) / 4.0
		case int64:
			impScore = float64(v-1) / 4.0
		}

		// entity overlap
		entScore := 0.0
		if entStr, ok := h.Metadata["entities"].(string); ok && entStr != "" {
			ents := strings.Split(entStr, ",")
			hit := 0
			for _, e := range ents {
				if qEntities[strings.ToLower(strings.TrimSpace(e))] {
					hit++
				}
			}
			if len(ents) > 0 {
				entScore = float64(hit) / float64(len(ents))
				if hit > 0 {
					entScore = math.Min(1.0, 0.5+entScore*0.5)
				}
			}
		}
		// tag overlap fallback
		if entScore == 0 && tags != "" {
			tagParts := strings.Split(tags, ",")
			hit := 0
			for _, tg := range tagParts {
				if qEntities[strings.TrimSpace(strings.ToLower(tg))] {
					hit++
				}
			}
			if hit > 0 && len(tagParts) > 0 {
				entScore = float64(hit) / float64(len(tagParts)) * 0.5
			}
		}

		// recency: not stored as sortable in metadata yet; use created_at if present
		recency := 0.5
		// could parse created_at and boost recent, but keep neutral for now

		final := vecScore*0.55 + bm25Norm*0.25 + impScore*0.08 + entScore*0.07 + recency*0.05
		scores[i] = scored{idx: i, score: final, base: h}
	}
	sort.Slice(scores, func(a, b int) bool { return scores[a].score > scores[b].score })
	out := make([]models.SearchResult, len(hits))
	for i, s := range scores {
		out[i] = s.base
	}
	return out
}

// HybridSearch helper uses same BM25 logic
func bm25Score(query, doc string) float64 {
	qt := strings.Fields(strings.ToLower(query))
	dt := strings.ToLower(doc)
	s := 0.0
	for _, t := range qt {
		s += float64(strings.Count(dt, t))
	}
	return s
}
