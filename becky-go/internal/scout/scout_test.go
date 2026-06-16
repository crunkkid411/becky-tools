package scout

import (
	"errors"
	"reflect"
	"testing"

	"becky-go/internal/freshness"
)

// testDeps is a tiny slice of the freshness manifest shape (no I/O).
func testDeps() []freshness.Dependency {
	return []freshness.Dependency{
		{
			ID:       "paddleocr-pipeline",
			Name:     "PaddleOCR PP-OCRv6",
			UsedBy:   []string{"becky-ocr"},
			Pinned:   "PP-OCRv6 -> v5",
			Upstream: freshness.Upstream{Type: "github-release", Ref: "PaddlePaddle/PaddleOCR"},
		},
		{
			ID:       "insightface-buffalo_l",
			Name:     "InsightFace buffalo_l face pack",
			UsedBy:   []string{"becky-identify", "becky-cluster"},
			Pinned:   "buffalo_l",
			Upstream: freshness.Upstream{Type: "github-release", Ref: "deepinsight/insightface"},
		},
	}
}

func buildOne(v Video, assessor Assessor) Report {
	src := FakePlaylist{PL: Playlist{ID: "PL1", Title: "Watch later", Videos: []Video{v}}}
	return Build(src, "PL1", testDeps(), nil, nil, assessor)
}

// A dep-match (PaddleOCR) AND a capability hit (ocr) → two independent signals →
// RELEVANT, classified as an "improve" of the tool that uses the dep.
func TestCorroboratedImprove(t *testing.T) {
	v := Video{ID: "v1", Title: "PaddleOCR PP-OCRv6 deep dive: best OCR for documents", Position: 1}
	rep := buildOne(v, nil)
	if len(rep.Relevant) != 1 {
		t.Fatalf("want 1 relevant, got %d (candidates=%d skipped=%d)", len(rep.Relevant), len(rep.Candidates), rep.Skipped)
	}
	it := rep.Relevant[0]
	if it.Kind != "improve" {
		t.Errorf("kind=%q want improve", it.Kind)
	}
	if it.Score < 2 {
		t.Errorf("score=%d want >=2", it.Score)
	}
	if len(it.DepMatches) != 1 || it.DepMatches[0].DependencyID != "paddleocr-pipeline" {
		t.Errorf("dep match wrong: %+v", it.DepMatches)
	}
	if !contains(it.BeckyTools, "becky-ocr") {
		t.Errorf("becky_tools=%v want becky-ocr", it.BeckyTools)
	}
}

// Only a capability keyword (no dep, no model) → a single signal → CANDIDATE
// (extend), never a stated conclusion. This is the corroborate-then-conclude rule.
func TestSingleSignalIsCandidate(t *testing.T) {
	v := Video{ID: "v2", Title: "Building a semantic search engine with embeddings", Position: 1}
	rep := buildOne(v, nil)
	if len(rep.Candidates) != 1 || len(rep.Relevant) != 0 {
		t.Fatalf("want 1 candidate/0 relevant, got cand=%d rel=%d", len(rep.Candidates), len(rep.Relevant))
	}
	if rep.Candidates[0].Kind != "extend" {
		t.Errorf("kind=%q want extend", rep.Candidates[0].Kind)
	}
}

// An off-topic video fires no signal and is silently skipped (counted, not
// listed) — no flood of maybes.
func TestOffTopicSkipped(t *testing.T) {
	v := Video{ID: "v3", Title: "How to bake sourdough bread at home", Position: 1}
	rep := buildOne(v, nil)
	if rep.Skipped != 1 || len(rep.Relevant) != 0 || len(rep.Candidates) != 0 {
		t.Fatalf("want skipped=1, got skipped=%d rel=%d cand=%d", rep.Skipped, len(rep.Relevant), len(rep.Candidates))
	}
}

// The model assessor is a genuinely independent third signal: a capability hit
// PLUS an agreeing assessor promotes a single-signal candidate to RELEVANT.
func TestAssessorCorroborates(t *testing.T) {
	v := Video{ID: "v4", Title: "Semantic search with embeddings", Position: 1}
	withoutModel := buildOne(v, nil)
	if len(withoutModel.Candidates) != 1 {
		t.Fatalf("precondition: want candidate without model, got %+v", withoutModel)
	}
	assessor := FakeAssessor{ByID: map[string]Assessment{
		"v4": {Relevant: true, Tools: []string{"becky-search"}, Ideas: []string{"try a reranker"}, Kind: "extend"},
	}}
	withModel := buildOne(v, assessor)
	if len(withModel.Relevant) != 1 {
		t.Fatalf("want relevant with agreeing model, got rel=%d cand=%d", len(withModel.Relevant), len(withModel.Candidates))
	}
	it := withModel.Relevant[0]
	if !contains(it.Signals, "assessor") || !contains(it.Signals, "capability") {
		t.Errorf("signals=%v want capability+assessor", it.Signals)
	}
	if !contains(it.Ideas, "try a reranker") {
		t.Errorf("ideas=%v want assessor idea merged in", it.Ideas)
	}
}

// A model that calls a video relevant does NOT, on its own, make a conclusion:
// assessor-only is a single signal → candidate.
func TestAssessorAloneIsCandidate(t *testing.T) {
	v := Video{ID: "v5", Title: "An unrelated talk", Position: 1}
	assessor := FakeAssessor{ByID: map[string]Assessment{
		"v5": {Relevant: true, Tools: []string{"becky-ask"}},
	}}
	rep := buildOne(v, assessor)
	if len(rep.Candidates) != 1 || len(rep.Relevant) != 0 {
		t.Fatalf("want 1 candidate, got cand=%d rel=%d", len(rep.Candidates), len(rep.Relevant))
	}
}

// A bad playlist source degrades to an empty, noted report — never a crash.
func TestDegradeOnSourceError(t *testing.T) {
	src := FakePlaylist{Err: errors.New("yt-dlp not found")}
	rep := Build(src, "PLx", testDeps(), nil, nil, nil)
	if !rep.Degraded || rep.Note == "" {
		t.Fatalf("want degraded with note, got %+v", rep)
	}
	if rep.Assessed != 0 || len(rep.Relevant) != 0 {
		t.Errorf("degraded report should be empty, got %+v", rep)
	}
}

// Output ordering is deterministic: by score desc, then playlist position asc.
func TestDeterministicOrder(t *testing.T) {
	vids := []Video{
		{ID: "b", Title: "Semantic search embeddings tutorial", Position: 3},             // 1 signal (cap)
		{ID: "a", Title: "PaddleOCR PP-OCRv6 OCR document parsing", Position: 2},         // 2 signals
		{ID: "c", Title: "InsightFace face recognition arcface embeddings", Position: 1}, // 2 signals
	}
	src := FakePlaylist{PL: Playlist{ID: "PL", Videos: vids}}
	rep := Build(src, "PL", testDeps(), nil, nil, nil)
	if len(rep.Relevant) != 2 {
		t.Fatalf("want 2 relevant, got %d", len(rep.Relevant))
	}
	// Both relevant have score 2; tie broken by position asc → c (pos1) before a (pos2).
	gotOrder := []string{rep.Relevant[0].ID, rep.Relevant[1].ID}
	if !reflect.DeepEqual(gotOrder, []string{"c", "a"}) {
		t.Errorf("relevant order=%v want [c a]", gotOrder)
	}
	if len(rep.Candidates) != 1 || rep.Candidates[0].ID != "b" {
		t.Errorf("candidate=%v want [b]", rep.Candidates)
	}
}

// The capability catalog matches across all readable fields (here: transcript).
func TestCatalogMatchesTranscript(t *testing.T) {
	v := Video{ID: "t", Title: "My channel update", Transcript: "today we explore speaker diarization with pyannote", Position: 1}
	rep := buildOne(v, nil)
	// "diariz" (capability) + "pyannote" is not in testDeps, so only the cap signal fires → candidate.
	if len(rep.Candidates) != 1 {
		t.Fatalf("want 1 candidate from transcript match, got %+v", rep)
	}
	if !contains(rep.Candidates[0].BeckyTools, "becky-diarize") {
		t.Errorf("tools=%v want becky-diarize", rep.Candidates[0].BeckyTools)
	}
}

// A video with no becky signal but a personal-interest hit lands in the "useful
// to you" lane (not skipped) — the lower-stakes lens Jordan asked for.
func TestUsefulLane(t *testing.T) {
	v := Video{ID: "u", Title: "No-code automation workflow to save time", Position: 1}
	rep := buildOne(v, nil)
	if len(rep.Useful) != 1 {
		t.Fatalf("want 1 useful, got useful=%d skipped=%d rel=%d cand=%d", len(rep.Useful), rep.Skipped, len(rep.Relevant), len(rep.Candidates))
	}
	it := rep.Useful[0]
	if it.Score != 0 {
		t.Errorf("useful item should have becky score 0, got %d", it.Score)
	}
	if !contains(it.Interests, "productivity & automation") {
		t.Errorf("interests=%v want productivity & automation", it.Interests)
	}
}

// A becky match takes precedence over the useful lane: a video that is BOTH a
// becky candidate and personally useful is surfaced as the becky candidate (its
// interests are still recorded for context), not duplicated into "useful".
func TestBeckyMatchNotDuplicatedAsUseful(t *testing.T) {
	v := Video{ID: "p", Title: "Local LLM with llama.cpp and GGUF quantization tutorial", Position: 1}
	rep := buildOne(v, nil)
	if len(rep.Useful) != 0 {
		t.Errorf("becky-matched video should not appear in useful, got %d", len(rep.Useful))
	}
	if len(rep.Candidates)+len(rep.Relevant) != 1 {
		t.Fatalf("want the video in a becky lane, got cand=%d rel=%d", len(rep.Candidates), len(rep.Relevant))
	}
}

func contains(s []string, want string) bool {
	for _, e := range s {
		if e == want {
			return true
		}
	}
	return false
}
