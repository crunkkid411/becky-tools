package hum

import "testing"

// noteAt builds a held note of the given MIDI pitch + duration for PCP fixtures.
func noteAt(midi int, dur float64) Note { return Note{Midi: midi, DurSec: dur} }

func TestDetectKey_CMajorProfile(t *testing.T) {
	// A C-major melody (C D E F G A B, tonic/dominant emphasized) must detect C major.
	notes := []Note{
		noteAt(60, 2.0), // C (tonic, long)
		noteAt(67, 1.5), // G (dominant)
		noteAt(64, 1.0), // E
		noteAt(62, 0.5), // D
		noteAt(65, 0.5), // F
		noteAt(69, 0.5), // A
		noteAt(71, 0.5), // B
	}
	got := DetectKey(PitchClassProfile(notes))
	if got.Compose != "C" {
		t.Fatalf("C-major profile detected %q (root %s scale %s), want C", got.Compose, got.Root, got.Scale)
	}
	if got.Scale != "major" {
		t.Errorf("scale = %q, want major", got.Scale)
	}
}

func TestDetectKey_AMinorProfile(t *testing.T) {
	// A natural-minor melody emphasizing A (tonic) and E (dominant) -> A minor.
	notes := []Note{
		noteAt(69, 2.0), // A
		noteAt(76, 1.5), // E
		noteAt(72, 1.0), // C
		noteAt(71, 0.5), // B
		noteAt(74, 0.5), // D
		noteAt(77, 0.5), // F
		noteAt(79, 0.5), // G
	}
	got := DetectKey(PitchClassProfile(notes))
	if got.Compose != "Am" {
		t.Fatalf("A-minor profile detected %q, want Am", got.Compose)
	}
}

func TestDetectKey_FSharpMinor(t *testing.T) {
	// F#-minor emphasis (F#, C# dominant) -> F#m, the SPEC's example key.
	notes := []Note{
		noteAt(66, 2.0), // F#
		noteAt(73, 1.5), // C#
		noteAt(69, 1.0), // A
		noteAt(71, 0.5), // B
	}
	got := DetectKey(PitchClassProfile(notes))
	if got.Compose != "F#m" {
		t.Fatalf("F#-minor profile detected %q, want F#m", got.Compose)
	}
}

func TestDetectKey_EmptyDegrades(t *testing.T) {
	got := DetectKey([12]float64{})
	if got.Compose != "Am" || !got.Ambiguous || got.Confidence != 0 {
		t.Errorf("empty PCP should degrade to ambiguous Am @ 0 confidence, got %+v", got)
	}
}

func TestDetectKey_Deterministic(t *testing.T) {
	notes := []Note{noteAt(60, 2.0), noteAt(67, 1.0), noteAt(64, 1.0)}
	a := DetectKey(PitchClassProfile(notes))
	b := DetectKey(PitchClassProfile(notes))
	if a != b {
		t.Errorf("DetectKey not deterministic: %+v vs %+v", a, b)
	}
}

func TestPitchClassProfile_DurationWeighted(t *testing.T) {
	// A long C should out-weight a short C# in its own bin.
	pcp := PitchClassProfile([]Note{noteAt(60, 4.0), noteAt(61, 0.5)})
	if pcp[0] <= pcp[1] {
		t.Errorf("duration weighting failed: C=%.2f C#=%.2f", pcp[0], pcp[1])
	}
	if pcp[0] != 4.0 {
		t.Errorf("C bin = %.2f, want 4.0", pcp[0])
	}
}

func TestScaleTonesPC_CMajor(t *testing.T) {
	got := ScaleTonesPC("C")
	want := map[int]bool{0: true, 2: true, 4: true, 5: true, 7: true, 9: true, 11: true}
	if len(got) != 7 {
		t.Fatalf("C major has 7 tones, got %d (%v)", len(got), got)
	}
	for _, pc := range got {
		if !want[pc] {
			t.Errorf("unexpected pitch class %d in C major", pc)
		}
	}
}

func TestPearson_PerfectCorrelation(t *testing.T) {
	v := [12]float64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12}
	if c := pearson(v, v); c < 0.999 {
		t.Errorf("self-correlation = %.4f, want ~1", c)
	}
}
