package quotes

import "testing"

func TestEndsSentence(t *testing.T) {
	tests := []struct {
		name string
		word string
		next string
		want bool
	}{
		{"period", "tonight.", "", true},
		{"question", "really?", "", true},
		{"bang", "stop!", "", true},
		{"midword", "however", "", false},
		{"abbrev Mr", "Mr.", "Smith", false},
		{"abbrev Dr", "Dr.", "Jones", false},
		{"decimal", "3.", "14", false},          // "3.14" — internal dot
		{"version continues", "1.", "2", false}, // "1.2"
		{"single initial", "J.", "Smith", false},
		{"quoted period", `done."`, "", true},
		{"double dot ends", "wait..", "", true}, // ends on '.', not abbrev
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := endsSentence(tt.word, tt.next); got != tt.want {
				t.Errorf("endsSentence(%q,%q) = %v, want %v", tt.word, tt.next, got, tt.want)
			}
		})
	}
}

func TestSegment_MapsSentencesToCueRanges(t *testing.T) {
	cues := ParseSRT(goldenSRT)
	sents := Segment(cues)
	// 6 cues, each one clean sentence -> 6 sentences, each spanning one cue.
	if len(sents) != 6 {
		t.Fatalf("expected 6 sentences, got %d: %+v", len(sents), sents)
	}
	for i, s := range sents {
		if s.FirstCue != i || s.LastCue != i {
			t.Errorf("sentence %d should map to cue %d, got [%d,%d]", i, i, s.FirstCue, s.LastCue)
		}
	}
}

func TestSegment_SentenceSpanningMultipleCues(t *testing.T) {
	// A single sentence split across two cues (no terminal in cue 1).
	srt := `1
00:00:01,000 --> 00:00:02,000
So I told her that I would

2
00:00:02,000 --> 00:00:03,500
press charges if she kept it up.
`
	cues := ParseSRT(srt)
	sents := Segment(cues)
	if len(sents) != 1 {
		t.Fatalf("expected 1 sentence across 2 cues, got %d", len(sents))
	}
	if sents[0].FirstCue != 0 || sents[0].LastCue != 1 {
		t.Errorf("sentence should span cues [0,1], got [%d,%d]", sents[0].FirstCue, sents[0].LastCue)
	}
}

func TestSegmentationPoor_TriggersFallback(t *testing.T) {
	// Unpunctuated run-on transcript: many words, ~no terminal punctuation.
	var b string
	for i := 0; i < 30; i++ {
		d1 := string(rune('0' + i%10))
		d2 := string(rune('0' + (i+1)%10))
		b += "1\n00:00:0" + d1 + ",000 --> 00:00:0" + d2 + ",500\nword word word word word word word\n\n"
	}
	cues := ParseSRT(b)
	sents := Segment(cues)
	if !SegmentationPoor(cues, sents) {
		t.Error("expected SegmentationPoor=true for an unpunctuated run-on transcript")
	}
	// the fallback unit set has one sentence per cue.
	fb := cueLevelSentences(cues)
	if len(fb) != len(cues) {
		t.Errorf("cue-level fallback should yield one sentence per cue: got %d for %d cues", len(fb), len(cues))
	}
}

func TestSegmentationPoor_FalseForCleanText(t *testing.T) {
	cues := ParseSRT(goldenSRT)
	sents := Segment(cues)
	if SegmentationPoor(cues, sents) {
		t.Error("clean punctuated transcript should NOT be flagged as poor")
	}
}
