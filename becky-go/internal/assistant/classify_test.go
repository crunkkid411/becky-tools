package assistant

import "testing"

// TestClassifyTier covers the deterministic routing decision (R-AI §1.2): which
// tier each utterance STARTS at, with no model call. This is the load-bearing
// "don't burn the Max plan" logic, so it is exercised hard.
func TestClassifyTier(t *testing.T) {
	tests := []struct {
		name       string
		utt        string
		wantTier   Tier
		wantVerb   Verb // when Tier 0 parsed actions; "" otherwise
		wantEscala bool
	}{
		// (a) Tier 0 — explicit command grammar.
		{"add clip N", "add clip 3", TierDeterministic, VerbAddClip, false},
		{"add the last clip", "add the last clip", TierDeterministic, VerbAddClip, false},
		{"remove clip", "remove clip 2", TierDeterministic, VerbRemoveClip, false},
		{"jump to time", "jump to 12:40", TierDeterministic, VerbPreviewClip, false},
		{"export", "export the compilation", TierDeterministic, VerbExport, false},
		{"marker", "set a marker at 00:01:00 label intro", TierDeterministic, VerbSetMarker, false},
		{"label clip", "label clip 2 the cat threat", TierDeterministic, VerbSetLabel, false},

		// (b) Tier 0 — literal retrieval (no semantic cue).
		{"find quoted literal", `find the word "cat"`, TierDeterministic, VerbSearch, false},
		{"search short phrase", "search for penguin", TierDeterministic, VerbSearch, false},

		// (c) Tier 2 — semantic retrieval / multi-step.
		{"every time semantic", "find every time he offered money for the cat", TierFrontier, "", true},
		{"whenever semantic", "whenever he threatens the host family", TierFrontier, "", true},
		{"mentions semantic", "show me where he mentions the reward", TierFrontier, "", true},
		{"multi-step chain", "find the threats and add them to the timeline", TierFrontier, "", true},

		// (d) Tier 1 — fuzzy single-action NL the grammar missed.
		{"fuzzy add", "chuck that angry bit onto the end", TierLocal, "", false},
		{"fuzzy trim", "tighten up the second clip a little", TierLocal, "", false},

		// edge: empty.
		{"empty", "   ", TierDeterministic, "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := classifyTier(tt.utt, Context{})
			if d.Tier != tt.wantTier {
				t.Fatalf("classifyTier(%q).Tier = %v (%s), want %v", tt.utt, d.Tier, d.Reason, tt.wantTier)
			}
			if d.Escalate != tt.wantEscala {
				t.Fatalf("classifyTier(%q).Escalate = %v, want %v", tt.utt, d.Escalate, tt.wantEscala)
			}
			if tt.wantVerb != "" {
				if len(d.Actions) == 0 || d.Actions[0].Verb != tt.wantVerb {
					t.Fatalf("classifyTier(%q) actions = %+v, want first verb %v", tt.utt, d.Actions, tt.wantVerb)
				}
			}
			if tt.wantTier == TierDeterministic && tt.wantVerb == "" && len(d.Actions) != 0 {
				// an empty/no-grammar-match utterance shouldn't fabricate actions.
				t.Fatalf("classifyTier(%q) should have no actions, got %+v", tt.utt, d.Actions)
			}
		})
	}
}

// TestAddClipListGrammar covers the multi-selector "add clips 1, 3 and 5"
// grammar (reAddClipList): one Tier-0 add_clip{hit:N} action per selector, in
// order, in a SINGLE Decision — this is what lets applyActions batch every
// resolved clip into one apply_edit_batch call (H-4/H-6) with zero model
// tokens. A single selector still yields exactly one action (no regression),
// and a sentence with unrelated trailing text does NOT get swept into the
// list (the $ anchor keeps this narrow).
func TestAddClipListGrammar(t *testing.T) {
	tests := []struct {
		name     string
		utt      string
		wantTier Tier
		wantSels []string // hit selector per action, in order; nil means "don't check"
	}{
		{"comma and list", "add clips 1, 3 and 5", TierDeterministic, []string{"1", "3", "5"}},
		{"bare and, no comma", "add clip 2 and 4", TierDeterministic, []string{"2", "4"}},
		{"comma only, no and", "add clips 1,2,3", TierDeterministic, []string{"1", "2", "3"}},
		{"last in a list", "add clips 1 and last", TierDeterministic, []string{"1", "last"}},
		{"single stays single", "add clip 7", TierDeterministic, []string{"7"}},
		{"trailing text not swept in", "add clip 1 to the reel", TierDeterministic, []string{"1"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := classifyTier(tt.utt, Context{})
			if d.Tier != tt.wantTier {
				t.Fatalf("classifyTier(%q).Tier = %v (%s), want %v", tt.utt, d.Tier, d.Reason, tt.wantTier)
			}
			if len(d.Actions) != len(tt.wantSels) {
				t.Fatalf("classifyTier(%q) actions = %+v, want %d selectors %v", tt.utt, d.Actions, len(tt.wantSels), tt.wantSels)
			}
			for i, want := range tt.wantSels {
				a := d.Actions[i]
				if a.Verb != VerbAddClip {
					t.Fatalf("action[%d] verb = %v, want add_clip", i, a.Verb)
				}
				if got, _ := a.Args["hit"].(string); got != want {
					t.Fatalf("action[%d] hit = %q, want %q", i, got, want)
				}
			}
		})
	}
}

// TestSemanticPredicate isolates hasSemanticCue / isMultiStep so the routing
// thresholds are pinned.
func TestSemanticPredicate(t *testing.T) {
	if !hasSemanticCue("every time he does it") {
		t.Fatal("`every time` must be a semantic cue")
	}
	if hasSemanticCue("add clip 2") {
		t.Fatal("`add clip 2` is a plain command, not semantic")
	}
	if !isMultiStep("find it and add it") {
		t.Fatal("`find … and add …` must be multi-step")
	}
	if isMultiStep("preview the clip") {
		t.Fatal("a single action must not be multi-step")
	}
}

// TestSecondsFromTimecode pins the grammar's timecode→seconds helper.
func TestSecondsFromTimecode(t *testing.T) {
	tests := []struct {
		in   string
		want float64
		ok   bool
	}{
		{"12:40", 760, true},
		{"00:13:12", 792, true},
		{"90", 90, true},
		{"00:00:01,500", 1.5, true},
		{"", 0, false},
		{"abc", 0, false},
	}
	for _, tt := range tests {
		got, ok := secondsFromTimecode(tt.in)
		if ok != tt.ok || (ok && got != tt.want) {
			t.Fatalf("secondsFromTimecode(%q) = %v,%v want %v,%v", tt.in, got, ok, tt.want, tt.ok)
		}
	}
}
