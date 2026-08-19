package audiosig

import "testing"

// A cue boundary is where the ASR decided a line ended, not where the sound
// stops. Parakeet quantises to 0.08s and 49% of its words carry end == start, so
// a cue edge routinely lands part-way through a consonant. These tests pin the
// rule that keeps an edge off a word: nudge only TOWARD quiet, never into speech.

func sig(gaps ...[2]float64) Signals {
	s := Signals{OK: true}
	for _, g := range gaps {
		s.BreathGaps = append(s.BreathGaps, Event{T0: g[0], T1: g[1], T: g[0]})
	}
	return s
}

func TestSnapEdges_StartLandsOnTheEndOfSilenceAndEndOnItsStart(t *testing.T) {
	// Silence 9.8-10.1 before the clip, and 39.9-40.4 after it.
	s := sig([2]float64{9.8, 10.1}, [2]float64{39.9, 40.4})

	gotS, gotE, moved := s.SnapEdges(10.0, 40.0)

	if !moved {
		t.Fatal("nothing moved; both edges sat mid-silence and should have snapped")
	}
	// Start goes FORWARD to 10.1 - the last moment of quiet. Anything earlier
	// would add dead air; anything later would clip the first word's attack.
	if gotS != 10.1 {
		t.Errorf("start = %.3f, want 10.100 (the end of the gap, where speech begins)", gotS)
	}
	// End goes BACK to 39.9 - the first moment of quiet after the last word.
	if gotE != 39.9 {
		t.Errorf("end = %.3f, want 39.900 (the start of the gap, where speech stopped)", gotE)
	}
}

// The whole point of "only toward quiet": an edge must never be pulled into
// speech, whichever direction that happens to be.
func TestSnapEdges_NeverMovesAnEdgeIntoSpeech(t *testing.T) {
	// One gap 20.0-20.2. A clip starting at 20.25 is already 50ms into a word.
	s := sig([2]float64{20.0, 20.2})
	gotS, _, _ := s.SnapEdges(20.25, 30.0)
	if gotS > 20.2 {
		t.Errorf("start = %.3f, want <= 20.200: it was moved further into the word, not out of it", gotS)
	}
	if gotS != 20.2 {
		t.Errorf("start = %.3f, want 20.200 (back out to the last moment of quiet)", gotS)
	}
}

// The window was chosen for editorial reasons. This pass tidies its edges; it
// does not get to relocate the moment.
func TestSnapEdges_LeavesAnEdgeAloneBeyondTheBudget(t *testing.T) {
	s := sig([2]float64{5.0, 5.4}) // nowhere near either edge
	gotS, gotE, moved := s.SnapEdges(30.0, 60.0)
	if moved || gotS != 30.0 || gotE != 60.0 {
		t.Errorf("got %.3f-%.3f moved=%v; want the edges untouched — the nearest silence is %.1fs away, well past the %.2fs budget",
			gotS, gotE, moved, 30.0-5.4, SnapBudget)
	}
}

func TestSnapEdges_PicksTheNEARESTGapNotTheFirstOne(t *testing.T) {
	// Two gaps inside the budget of the start. 10.05 is nearer than 9.75.
	s := sig([2]float64{9.7, 9.75}, [2]float64{10.0, 10.05})
	gotS, _, _ := s.SnapEdges(10.1, 40.0)
	if gotS != 10.05 {
		t.Errorf("start = %.3f, want 10.050 — the nearest gap, not the first one in the list", gotS)
	}
}

// Degrade, never crash, and never silently ruin a clip.
func TestSnapEdges_RefusesRatherThanCollapseAMoment(t *testing.T) {
	// A gap straddling almost the whole clip would snap 10.0-10.6 down to nothing.
	s := sig([2]float64{10.05, 10.55})
	gotS, gotE, moved := s.SnapEdges(10.0, 10.6)
	if moved {
		t.Errorf("snapped to %.3f-%.3f (%.3fs) — a snap that collapses the moment must be refused",
			gotS, gotE, gotE-gotS)
	}
}

func TestSnapEdges_NoSignalIsNotAFailure(t *testing.T) {
	for _, s := range []Signals{{}, {OK: true}, {OK: false, BreathGaps: []Event{{T0: 1, T1: 2}}}} {
		gotS, gotE, moved := s.SnapEdges(10, 40)
		if moved || gotS != 10 || gotE != 40 {
			t.Errorf("got %.1f-%.1f moved=%v; with no usable audio signal the edges must pass through untouched",
				gotS, gotE, moved)
		}
	}
}
