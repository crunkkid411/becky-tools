package main

import (
	"testing"
)

// TestUsesServer confirms only qwen3-4b routes through the resident server; the
// 0.6B model stays in-process (a different vector space — never mixed).
func TestUsesServer(t *testing.T) {
	if !usesServer("qwen3-4b") {
		t.Error("qwen3-4b should route through the resident server")
	}
	if !usesServer("QWEN3-4B") {
		t.Error("usesServer should be case-insensitive")
	}
	if usesServer("qwen3-0.6b") {
		t.Error("qwen3-0.6b should NOT use the server (in-process path)")
	}
}

func TestSegmentIDDeterministicAndIndexed(t *testing.T) {
	sha := "bdd5a9bc9823e694344237448e4d003bb919c2c446e931407d751e95bdc4a042"
	// Same input -> same id (idempotency depends on this).
	if a, b := segmentID(sha, 2), segmentID(sha, 2); a != b {
		t.Errorf("segmentID not deterministic: %q != %q", a, b)
	}
	// Different index -> different id.
	if segmentID(sha, 2) == segmentID(sha, 3) {
		t.Error("segmentID collides across indices")
	}
	// Prefix is the first 12 hex chars of the source sha.
	if got, want := segmentID(sha, 0), "bdd5a9bc9823:0"; got != want {
		t.Errorf("segmentID = %q, want %q", got, want)
	}
}

func TestSegmentIDShortSHA(t *testing.T) {
	// A short source id (e.g. derived sha12) must not panic on the 12-char slice.
	if got := segmentID("abc", 5); got != "abc:5" {
		t.Errorf("segmentID(short) = %q, want %q", got, "abc:5")
	}
}

func TestSHA12LengthAndStability(t *testing.T) {
	a := sha12("../test.mp4")
	if len(a) != 12 {
		t.Errorf("sha12 length = %d, want 12", len(a))
	}
	if a != sha12("../test.mp4") {
		t.Error("sha12 not stable for the same input")
	}
	if a == sha12("other.mp4") {
		t.Error("sha12 collides for different inputs")
	}
}

func TestVecJSONShape(t *testing.T) {
	got := vecJSON([]float64{0.1, -0.2, 0})
	if got != "[0.1,-0.2,0]" {
		t.Errorf("vecJSON = %q, want %q", got, "[0.1,-0.2,0]")
	}
	if vecJSON([]float64{}) != "[]" {
		t.Error("vecJSON([]) should be []")
	}
}

func TestParseEmbedJSONHappyAndNoise(t *testing.T) {
	// Clean single line.
	clean := `{"model":"Qwen/Qwen3-Embedding-0.6B","dim":1024,"vectors":[[0.1,0.2]]}`
	r, ok := parseEmbedJSON(clean)
	if !ok || r.Dim != 1024 || len(r.Vectors) != 1 {
		t.Fatalf("parseEmbedJSON(clean) = %+v, ok=%v", r, ok)
	}
	// Leading log noise (as sentence-transformers / torch may emit) before JSON.
	noisy := "Loading weights: 100%\nsome warning\n" + clean
	r2, ok2 := parseEmbedJSON(noisy)
	if !ok2 || r2.Dim != 1024 {
		t.Fatalf("parseEmbedJSON(noisy) failed: %+v ok=%v", r2, ok2)
	}
	// Skipped payload is recognized.
	skip := `{"skipped":true,"reason":"ImportError: no torch"}`
	r3, ok3 := parseEmbedJSON(skip)
	if !ok3 || !r3.Skipped {
		t.Fatalf("parseEmbedJSON(skip) = %+v ok=%v", r3, ok3)
	}
	// Garbage -> not ok.
	if _, ok4 := parseEmbedJSON("not json at all"); ok4 {
		t.Error("parseEmbedJSON(garbage) should be !ok")
	}
}
