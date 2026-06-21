package ctledit

import "testing"

func TestParsePhrase_FourOnTheFloor(t *testing.T) {
	a := makeArrangement(t)
	b, ok := ParsePhrase("give me four on the floor", a)
	if !ok || len(b.Edits) != 1 {
		t.Fatalf("expected a single edit, got ok=%v edits=%d", ok, len(b.Edits))
	}
	ed := b.Edits[0]
	if ed.Op != OpEuclidLane || ed.Lane != "kick" || ed.Pulses != 4 {
		t.Errorf("four-on-the-floor => euclid kick 4, got %+v", ed)
	}
	// And it must actually apply.
	out, res, _ := Apply(a, b, nil)
	if res.Applied != 1 {
		t.Fatalf("phrase batch did not apply: %+v", res.Outcomes)
	}
	if drumNoteSteps(out, "kick", "drums", 36)[8] == false {
		t.Error("E(4,16) kick should hit step 8")
	}
}

func TestParsePhrase_Genre(t *testing.T) {
	a := makeArrangement(t)
	for _, phrase := range []string{"make a house beat", "give me some trap", "dnb please"} {
		b, ok := ParsePhrase(phrase, a)
		if !ok || len(b.Edits) != 1 || b.Edits[0].Op != OpGenerateBeat {
			t.Fatalf("%q should produce a generate_beat edit, got ok=%v %+v", phrase, ok, b.Edits)
		}
		if b.Edits[0].Genre == "" {
			t.Errorf("%q should carry a genre", phrase)
		}
	}
}

func TestParsePhrase_RandomizeVerb(t *testing.T) {
	a := makeArrangement(t)
	b, ok := ParsePhrase("randomize the beat", a)
	if !ok || b.Edits[0].Op != OpGenerateBeat {
		t.Fatalf("randomize => generate_beat, got ok=%v %+v", ok, b.Edits)
	}
}

func TestParsePhrase_EuclidWithLaneAndCount(t *testing.T) {
	a := makeArrangement(t)
	b, ok := ParsePhrase("euclidean snare 3", a)
	if !ok || b.Edits[0].Op != OpEuclidLane {
		t.Fatalf("expected euclid_lane, got ok=%v %+v", ok, b.Edits)
	}
	if b.Edits[0].Lane != "snare" || b.Edits[0].Pulses != 3 {
		t.Errorf("expected snare/3, got lane=%q pulses=%d", b.Edits[0].Lane, b.Edits[0].Pulses)
	}
}

func TestParsePhrase_Deterministic(t *testing.T) {
	a := makeArrangement(t)
	b1, _ := ParsePhrase("make a trap beat", a)
	b2, _ := ParsePhrase("make a trap beat", a)
	if b1.Edits[0].Seed != b2.Edits[0].Seed {
		t.Error("same phrase must yield the same seed")
	}
}

func TestParsePhrase_NoMatch(t *testing.T) {
	a := makeArrangement(t)
	if _, ok := ParsePhrase("what time is it", a); ok {
		t.Error("unrelated phrase should not match")
	}
	if _, ok := ParsePhrase("", a); ok {
		t.Error("empty phrase should not match")
	}
}

func TestParsePhrase_NoDrumClip(t *testing.T) {
	// An arrangement with no drum clip can't host a beat op.
	a := makeArrangement(t)
	// Strip the kick track's notes so no channel-9 clip remains.
	for ti := range a.Tracks {
		for ci := range a.Tracks[ti].Clips {
			a.Tracks[ti].Clips[ci].Notes = nil
		}
	}
	if _, ok := ParsePhrase("randomize the beat", a); ok {
		t.Error("no drum clip => no match")
	}
}
