package research

import (
	"reflect"
	"testing"
)

func TestPlanQuery_deterministicAndCapped(t *testing.T) {
	cases := []struct {
		name    string
		q       string
		maxSub  int
		maxPer  int
		wantLen int
	}{
		{"basic", "best local diarization model", 5, 3, 5},
		{"capped to two", "x", 2, 1, 2},
		{"empty question yields nil", "", 5, 3, 0},
		{"zero cap yields nil", "x", 0, 3, 0},
		{"more than framings clamps", "x", 99, 3, 5},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := PlanQuery(c.q, c.maxSub, c.maxPer)
			if len(got) != c.wantLen {
				t.Fatalf("len=%d want %d (%+v)", len(got), c.wantLen, got)
			}
			// Determinism: a second call must equal the first.
			if !reflect.DeepEqual(got, PlanQuery(c.q, c.maxSub, c.maxPer)) {
				t.Error("PlanQuery is not deterministic")
			}
			for _, sq := range got {
				if len(sq.Queries) > c.maxPer {
					t.Errorf("sub-question exceeded maxPer: %+v", sq)
				}
			}
		})
	}
	// The first sub-question is the verbatim question (overview framing).
	got := PlanQuery("topic", 1, 1)
	if got[0].Q != "topic" {
		t.Errorf("first framing should be the bare question, got %q", got[0].Q)
	}
}

func TestCanonicalURL(t *testing.T) {
	cases := []struct{ in, want string }{
		{"https://Example.com/Path/", "https://example.com/Path"},
		{"https://example.com/p#frag", "https://example.com/p"},
		{"https://example.com/p?utm_source=x&a=1", "https://example.com/p?a=1"},
		{"https://example.com:443/p", "https://example.com/p"},
		{"  https://example.com/  ", "https://example.com/"},
		{"", ""},
		{"not a url", "not a url"},
	}
	for _, c := range cases {
		if got := CanonicalURL(c.in); got != c.want {
			t.Errorf("CanonicalURL(%q)=%q want %q", c.in, got, c.want)
		}
	}
	// Two links differing only by tracking param canonicalize equal.
	if CanonicalURL("https://e.com/a?gclid=z") != CanonicalURL("https://e.com/a") {
		t.Error("tracking-param links should canonicalize equal")
	}
}

func TestFuseRRF_mathAndDedup(t *testing.T) {
	// listA ranks A,B; listB ranks B,A. With k=60:
	//   A: 1/61 + 1/62; B: 1/62 + 1/61  → identical scores → tie broken by canon URL.
	listA := []SearchResult{{URL: "https://e.com/a", Rank: 1}, {URL: "https://e.com/b", Rank: 2}}
	listB := []SearchResult{{URL: "https://e.com/b", Rank: 1}, {URL: "https://e.com/a", Rank: 2}}
	got := FuseRRF([][]SearchResult{listA, listB}, 60)
	if len(got) != 2 {
		t.Fatalf("expected 2 deduped results, got %d: %+v", len(got), got)
	}
	// Tie on score → canonical URL ascending → /a before /b.
	if got[0].URL != "https://e.com/a" || got[1].URL != "https://e.com/b" {
		t.Errorf("tie-break order wrong: %+v", got)
	}
	if got[0].Rank != 1 || got[1].Rank != 2 {
		t.Errorf("results should be re-numbered 1..n, got %+v", got)
	}
}

func TestFuseRRF_higherRankWins(t *testing.T) {
	// C appears once at rank 1 (score 1/61); D appears twice at ranks 3,3
	// (score 2/63). 2/63 ≈ 0.0317 > 1/61 ≈ 0.0164, so D should outrank C.
	lists := [][]SearchResult{
		{{URL: "https://e.com/c", Rank: 1}, {URL: "https://e.com/d", Rank: 3}},
		{{URL: "https://e.com/d", Rank: 3}},
	}
	got := FuseRRF(lists, 60)
	if got[0].URL != "https://e.com/d" {
		t.Errorf("doc in two lists should win: %+v", got)
	}
}

func TestFuseRRF_dedupKeepsBestRankRepresentative(t *testing.T) {
	// Same canonical URL, different titles/ranks across lists — representative is
	// the best (lowest) original rank.
	lists := [][]SearchResult{
		{{URL: "https://e.com/x?utm_source=a", Rank: 5, Title: "rank5"}},
		{{URL: "https://e.com/x", Rank: 1, Title: "rank1"}},
	}
	got := FuseRRF(lists, 60)
	if len(got) != 1 {
		t.Fatalf("tracking-param variant should dedup to 1: %+v", got)
	}
	if got[0].Title != "rank1" {
		t.Errorf("representative should be best-ranked occurrence, got %q", got[0].Title)
	}
}

func TestFuseRRF_emptyAndDefaultK(t *testing.T) {
	if got := FuseRRF(nil, 0); len(got) != 0 {
		t.Errorf("empty input should yield empty, got %+v", got)
	}
	// k<=0 must fall back to defaultRRFK without panicking.
	got := FuseRRF([][]SearchResult{{{URL: "https://e.com/a", Rank: 1}}}, 0)
	if len(got) != 1 {
		t.Errorf("default-k fuse failed: %+v", got)
	}
}

func TestCollapseNearDups(t *testing.T) {
	base := "the quick brown fox jumps over the lazy dog every single morning"
	caps := []Capture{
		{URL: "https://e.com/1", Text: base, ContentSHA256: "h1"},
		{URL: "https://e.com/2", Text: base, ContentSHA256: "h1"},                  // exact dup (same hash)
		{URL: "https://e.com/3", Text: base + " and again", ContentSHA256: "h3"},   // near-dup (high shingle overlap)
		{URL: "https://e.com/4", Text: "completely different unrelated content x"}, // distinct
	}
	got := CollapseNearDups(caps, 0.8)
	if len(got) != 2 {
		t.Fatalf("expected 2 survivors (rep + distinct), got %d: %+v", len(got), urlsOf(got))
	}
	if got[0].URL != "https://e.com/1" {
		t.Errorf("highest-ranked representative should survive, got %q", got[0].URL)
	}
	if got[1].URL != "https://e.com/4" {
		t.Errorf("distinct capture should survive, got %q", got[1].URL)
	}
	// Determinism.
	if !reflect.DeepEqual(got, CollapseNearDups(caps, 0.8)) {
		t.Error("CollapseNearDups not deterministic")
	}
}

func urlsOf(cs []Capture) []string {
	out := make([]string, len(cs))
	for i, c := range cs {
		out[i] = c.URL
	}
	return out
}
