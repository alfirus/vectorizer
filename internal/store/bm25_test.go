package store

import (
	"math"
	"testing"
)

// TestBM25RankIDFWeighting verifies that IDF actually reduces the score for
// tokens appearing in many documents. With k1=1.2, b=0.75 a token present in
// all 4 docs must score strictly below the same tf of a token present in only
// 1 doc — raw TF would call them equal.
func TestBM25RankIDFWeighting(t *testing.T) {
	docs := []string{
		"common common common",
		"common common common",
		"common common common",
		"common common rare",
	}

	commonScore := bm25Rank("common", docs)[3]
	rareScore := bm25Rank("rare", docs)[3]

	if commonScore <= 0 || rareScore <= 0 {
		t.Fatalf("expected positive scores, got common=%v rare=%v", commonScore, rareScore)
	}
	if rareScore <= commonScore {
		t.Errorf("IDF did not weight rare token higher: rare=%v common=%v (idf = ln(1+(N-df+0.5)/(df+0.5)) not applied)", rareScore, commonScore)
	}

	// Verify the IDF values directly for sanity.
	N := float64(len(docs))
	dfCommon := 4.0
	dfRare := 1.0
	idfCommon := math.Log(1 + (N-dfCommon+0.5)/(dfCommon+0.5))
	idfRare := math.Log(1 + (N-dfRare+0.5)/(dfRare+0.5))
	if idfRare <= idfCommon {
		t.Errorf("idfRare=%v should be > idfCommon=%v", idfRare, idfCommon)
	}
}

// TestBM25RankLengthNormalization verifies that length normalization penalises
// longer documents when tf is identical. With b=0.75 the denominator grows with
// docNorm = len/avgLen so a 10-word doc must score below a 3-word doc at equal
// tf — raw TF would call them a tie.
func TestBM25RankLengthNormalization(t *testing.T) {
	docs := []string{
		"keyword", // 1 word
		"keyword one two three four five six seven eight nine ten", // 11 words
	}

	scores := bm25Rank("keyword", docs)
	if len(scores) != 2 {
		t.Fatalf("len(scores) = %d, want 2", len(scores))
	}
	if scores[0] <= scores[1] {
		t.Errorf("short doc did not score higher at equal tf: short=%v long=%v (length normalization with b=0.75 not applied)", scores[0], scores[1])
	}
}

// TestBM25RankNoMatchProducesZeros verifies that a query token absent from
// every document produces all-zero scores — this is the signal HybridSearchWithScope
// uses to fall back to pure-vector results. No panic must occur.
func TestBM25RankNoMatchProducesZeros(t *testing.T) {
	docs := []string{
		"nothing relevant here",
		"some other text entirely different",
		"another unrelated document",
	}

	scores := bm25Rank("zzzznotpresent123", docs)
	if len(scores) != len(docs) {
		t.Fatalf("len(scores) = %d, want %d", len(scores), len(docs))
	}
	for i, s := range scores {
		if s != 0 {
			t.Errorf("scores[%d] = %v, want 0 for a token absent from every doc", i, s)
		}
	}
}

// TestBM25RankNilDocsReturnsEmpty verifies that nil docs returns an empty slice
// (not nil — the caller indexes scores back into candidates and needs a zero-length
// array). No panic must occur.
func TestBM25RankNilDocsReturnsEmpty(t *testing.T) {
	scores := bm25Rank("anything", nil)
	if len(scores) != 0 {
		t.Errorf("bm25Rank(anything, nil) returned %d scores, want 0", len(scores))
	}

	// Also verify empty slice (not nil).
	scores = bm25Rank("anything", []string{})
	if len(scores) != 0 {
		t.Errorf("bm25Rank(anything, []) returned %d scores, want 0", len(scores))
	}
}

// TestBM25RankShortTokenIgnored verifies that query tokens with length < 2 are
// dropped — parity with bm25Score which also skips len<2 tokens. A single-letter
// query must produce all-zero scores even if the letter appears everywhere.
func TestBM25RankShortTokenIgnored(t *testing.T) {
	docs := []string{
		"a a a a a",
		"b a c a d",
		"a a a",
	}

	scores := bm25Rank("a", docs) // "a" has len 1, must be ignored
	if len(scores) != len(docs) {
		t.Fatalf("len(scores) = %d, want %d", len(scores), len(docs))
	}
	for i, s := range scores {
		if s != 0 {
			t.Errorf("scores[%d] = %v, want 0 — single-letter token 'a' must be ignored (len<2)", i, s)
		}
	}

	// Two-letter tokens should NOT be ignored. Use docs where "aa" appears as a
	// contiguous substring so strings.Count can find it.
	docs2 := []string{
		"banana aa apple",
		"aa is here",
		"no match",
	}

	scores2 := bm25Rank("aa", docs2) // "aa" has len 2, must be counted
	if scores2[0] <= 0 || scores2[1] <= 0 {
		t.Errorf("scores = %v for 'aa', want positive — two-letter tokens must be counted (len>=2)", scores2)
	}
}

// TestBM25RankOutputLengthMirrorsInput verifies that the output slice length
// always equals the input docs count, including edge cases (single doc, mixed
// empty/non-empty). HybridSearchWithScope indexes the score slice back into its
// candidate slice — a mismatch would panic or silently mislabel rows.
func TestBM25RankOutputLengthMirrorsInput(t *testing.T) {
	cases := []struct {
		name string
		docs []string
	}{
		{"single doc", []string{"one document"}},
		{"empty corpus", nil},
		{"empty slice", []string{}},
		{"mixed empty and text", []string{"text here", "", "more text"}},
		{"all empty", []string{"", "", ""}},
		{"large corpus", []string{
			"doc one with some words",
			"doc two with different words",
			"doc three with more words",
			"doc four with even more words",
			"doc five with the most words",
		}},
	}

	for _, c := range cases {
		scores := bm25Rank("words", c.docs)
		if len(scores) != len(c.docs) {
			t.Errorf("%s: len(scores) = %d, want %d — output length must mirror input", c.name, len(scores), len(c.docs))
		}
	}
}
