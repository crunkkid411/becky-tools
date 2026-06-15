package palantir

import "testing"

func TestQuery_whoCoOccursWith(t *testing.T) {
	g := Build(ingestTwoSharedClips(), Options{Engine: EngineCooccurOnly})
	ans := Query(g, "who co-occurs with John")
	if ans.Matched != "person:john" {
		t.Fatalf("matched = %q, want person:john", ans.Matched)
	}
	if len(ans.Neighbors) == 0 {
		t.Fatal("John should have at least one connection (Person A)")
	}
	if ans.Neighbors[0].NodeID != "person:a" {
		t.Errorf("nearest neighbour = %q, want person:a", ans.Neighbors[0].NodeID)
	}
}

func TestQuery_unknownEntityIsAPlainNote(t *testing.T) {
	g := Build(ingestTwoSharedClips(), Options{Engine: EngineCooccurOnly})
	ans := Query(g, "who co-occurs with Nobody")
	if len(ans.Neighbors) != 0 {
		t.Error("an unknown entity should return no neighbours")
	}
	if ans.Note == "" {
		t.Error("an unknown entity should return a plain-language note, not an error")
	}
}

func TestSanitizeID(t *testing.T) {
	cases := map[string]string{
		"John Clancy":     "john-clancy",
		"  JC!! ":         "jc",
		"Apple iPhone 13": "apple-iphone-13",
		"":                "unknown",
		"---":             "unknown",
	}
	for in, want := range cases {
		if got := sanitizeID(in); got != want {
			t.Errorf("sanitizeID(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestPairEdgeID_orderIndependent(t *testing.T) {
	if pairEdgeID("co_occurrence", "b", "a") != pairEdgeID("co_occurrence", "a", "b") {
		t.Error("undirected edge id must be independent of endpoint order")
	}
}
