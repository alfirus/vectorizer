package store

import "testing"

// Regression test for the shared-average mutation in bm25Rank.
//
// The original guard read:
//
//	if docLens[i] == 0 || avgLen == 0 { avgLen = 1 }
//
// which reassigned the corpus-wide mean from inside the per-doc loop. With an
// empty document anywhere in the corpus, the first documents scored against
// the true mean while every document AFTER the empty one scored against
// avgLen = 1, inflating docNorm to len/1 and collapsing its score. Net effect:
// a single blank row reordered the ranking, and equal documents on either
// side of it received unequal scores.
//
// Symmetry is the sharp assertion here: two identical documents must score
// identically no matter where an empty document sits between them.
func TestBM25RankEmptyDocDoesNotCorruptLaterScores(t *testing.T) {
	const target = "target"
	docs := []string{
		"alpha " + target + " one two three",
		"",                                   // empty doc in the middle
		"alpha " + target + " one two three", // identical to docs[0]
	}

	scores := bm25Rank(target, docs)
	if len(scores) != len(docs) {
		t.Fatalf("len(scores) = %d, want %d", len(scores), len(docs))
	}
	if scores[0] <= 0 {
		t.Fatalf("scores[0] = %v, want > 0", scores[0])
	}
	if scores[0] != scores[2] {
		t.Errorf("identical docs scored differently because of the empty doc: scores[0]=%v scores[2]=%v (avgLen was mutated mid-loop)", scores[0], scores[2])
	}
	// The empty doc contributes no terms, so it must score 0 rather than
	// inherit anything from the guard.
	if scores[1] != 0 {
		t.Errorf("scores[1] = %v, want 0 for an empty doc", scores[1])
	}
}

// An entirely empty corpus must not panic and must leave avgLen handling sane:
// every score is 0 because no document contains any query token.
func TestBM25RankAllEmptyDocsNoPanic(t *testing.T) {
	docs := []string{"", "", ""}
	scores := bm25Rank("anything", docs)
	if len(scores) != len(docs) {
		t.Fatalf("len(scores) = %d, want %d", len(scores), len(docs))
	}
	for i, s := range scores {
		if s != 0 {
			t.Errorf("scores[%d] = %v, want 0", i, s)
		}
	}
}

// Output length must always mirror the input, including the empty corpus,
// because HybridSearchWithScope indexes the score slice back into its
// candidate slice — a short result would panic or silently mislabel rows.
func TestBM25RankLengthMatchesDocs(t *testing.T) {
	if got := bm25Rank("q", nil); len(got) != 0 {
		t.Errorf("bm25Rank(q, nil) returned %d scores, want 0", len(got))
	}
	docs := []string{"some text", "", "more text with tokens"}
	if got := bm25Rank("tokens", docs); len(got) != len(docs) {
		t.Errorf("len(got) = %d, want %d", len(got), len(docs))
	}
}

// IDF must actually differentiate tokens: with raw term frequency (the old
// bm25Score) "alpha" and "target" both appear exactly once in docs[2], so
// they scored identically regardless of how common each token is across the
// corpus. Here "alpha" occurs in all 3 docs (rare idf) while "target"
// occurs in only 1 (rich idf), so the rarer token must win for the same
// document, same tf, same length.
func TestBM25RankRareTokenBeatsCommonToken(t *testing.T) {
	docs := []string{"alpha", "alpha", "alpha target"}

	common := bm25Rank("alpha", docs)[2]
	rare := bm25Rank("target", docs)[2]
	if common <= 0 || rare <= 0 {
		t.Fatalf("expected positive scores, got common=%v rare=%v", common, rare)
	}
	if rare <= common {
		t.Errorf("rare token did not beat common token: target=%v alpha=%v (IDF not applied)", rare, common)
	}
}

// Length normalization: identical tf for the query term, so the SHORTER
// document must score higher. Raw TF would call these a tie, which is how
// a long boilerplate file outranked a short precise one.
func TestBM25RankShortDocBeatsLongDocAtSameTF(t *testing.T) {
	docs := []string{"target", "target a b c d e f g h i j"}
	scores := bm25Rank("target", docs)
	if len(scores) != 2 {
		t.Fatalf("len(scores) = %d, want 2", len(scores))
	}
	if scores[0] <= scores[1] {
		t.Errorf("short doc did not win at equal tf: short=%v long=%v (length normalization not applied)", scores[0], scores[1])
	}
}

// A query matching nothing must produce all zeros - this is the signal
// HybridSearchWithScope uses to fall back to pure-vector results.
func TestBM25RankAbsentTokenScoresZero(t *testing.T) {
	docs := []string{"nothing relevant here", "some other text"}
	for i, s := range bm25Rank("zzzznotpresent", docs) {
		if s != 0 {
			t.Errorf("scores[%d] = %v, want 0 for a token in no doc", i, s)
		}
	}
}
