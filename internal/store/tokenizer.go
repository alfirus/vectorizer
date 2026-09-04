package store

import (
	"regexp"
	"strings"
	"unicode"
)

// Tokenizer: identifier-aware lexical splitting for BM25 paths.
// Why: plain strings.Fields treats `RAG_MIN_SCORE`, `vectorizer_trace` and
// `GetMessageChunks` as single opaque tokens, so a query for "trace" or
// "score" never lexically matches them. Splitting on case transitions,
// underscores, hyphens and digit boundaries (the cbm_camel_split pattern
// from codebase-memory-mcp) fixes exact-identifier recall in both rerank
// term-overlap and HybridSearch BM25 scoring.

var nonAlnum = regexp.MustCompile(`[^a-zA-Z0-9]+`)

// SplitIdentifier breaks one identifier into sub-tokens:
// "GetMessageChunks" → [get message chunks], "RAG_MIN_SCORE" → [rag min score],
// "vec-test-bin" → [vec test bin], "utf8Decoder" → [utf8 decoder].
func SplitIdentifier(s string) []string {
	// First split on separators (underscore, hyphen, dots, etc.).
	parts := nonAlnum.Split(s, -1)
	var out []string
	for _, p := range parts {
		out = append(out, splitCamel(p)...)
	}
	return out
}

// splitCamel splits on lower→Upper and letter↔digit boundaries, lowercasing.
func splitCamel(s string) []string {
	if s == "" {
		return nil
	}
	runes := []rune(s)
	var toks []string
	start := 0
	for i := 1; i < len(runes); i++ {
		prev, cur := runes[i-1], runes[i]
		boundary := false
		switch {
		// lower/digit→Upper: "getMessage" → "get|Message",
		// "utf8Decoder" → "utf8|Decoder"
		case (unicode.IsLower(prev) || unicode.IsDigit(prev)) && unicode.IsUpper(cur):
			boundary = true
		// Keep digit runs glued to a short preceding lowercase run
		// (utf8, phi4, 4B); split only when the letter-run is long
		// (likely a hash or version stamp, not a word).
		case unicode.IsLetter(prev) != unicode.IsLetter(cur) && (unicode.IsDigit(prev) || unicode.IsDigit(cur)):
			// measure current letter/digit run length back to start
			runLen := i - start
			if runLen > 4 {
				boundary = true
			}
		// ACRONYM→Word: "RAGMin" → "RAG|Min" (upper-run followed by Upper+lower)
		case unicode.IsUpper(prev) && unicode.IsUpper(cur):
			if i+1 < len(runes) && unicode.IsLower(runes[i+1]) {
				boundary = true
			}
		}
		if boundary {
			toks = append(toks, string(runes[start:i]))
			start = i
		}
	}
	toks = append(toks, string(runes[start:]))
	var out []string
	for _, t := range toks {
		if t = strings.ToLower(t); t != "" {
			out = append(out, t)
		}
	}
	return out
}

// LexTokens tokenizes free text for BM25: whitespace fields + identifier
// sub-tokens for any field containing case transitions or separators, so
// both "trace" and "vectorizer_trace" match a doc containing the latter.
func LexTokens(text string) []string {
	var out []string
	for _, f := range strings.Fields(strings.ToLower(text)) {
		out = append(out, f)
		for _, sub := range SplitIdentifier(f) {
			if sub != f {
				out = append(out, sub)
			}
		}
	}
	return out
}

// LexTokenSet returns the deduped token set of a text (for overlap counts).
func LexTokenSet(text string) map[string]bool {
	set := map[string]bool{}
	for _, t := range LexTokens(text) {
		if len(t) >= 2 {
			set[t] = true
		}
	}
	return set
}
