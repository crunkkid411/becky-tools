package musictheory

import (
	"testing"

	"becky-go/internal/dawmodel"
	"becky-go/internal/music"
)

func TestClassifyFunction(t *testing.T) {
	cases := map[int]Function{
		0: Tonic, 2: Tonic, 5: Tonic, // I, iii, vi
		1: Subdominant, 3: Subdominant, // ii, IV
		4: Dominant, 6: Dominant, // V, vii
		7: Tonic, 11: Dominant, // wraps (7→0, 11→4)
	}
	for deg, want := range cases {
		if got := ClassifyFunction(deg); got != want {
			t.Errorf("ClassifyFunction(%d) = %s, want %s", deg, got, want)
		}
	}
}

func TestVoiceFromIntervals(t *testing.T) {
	got := VoiceFromIntervals(60, Maj7) // C maj7
	want := []int{60, 64, 67, 71}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("Maj7 voice[%d] = %d, want %d", i, got[i], want[i])
		}
	}
	// Out-of-range intervals are dropped.
	if v := VoiceFromIntervals(125, []int{0, 4, 7}); len(v) != 1 || v[0] != 125 {
		t.Errorf("out-of-range drop failed: %v", v)
	}
}

func TestTransposeAndSemitones(t *testing.T) {
	if d := SemitonesBetween(0, 3); d != 3 { // C → Eb = +3
		t.Errorf("SemitonesBetween(C,Eb) = %d, want 3", d)
	}
	if d := SemitonesBetween(9, 0); d != 3 { // A → C = +3
		t.Errorf("SemitonesBetween(A,C) = %d, want 3", d)
	}
	out := Transpose([]int{60, 64, 67}, 3)
	want := []int{63, 67, 70}
	for i := range want {
		if out[i] != want[i] {
			t.Errorf("Transpose[%d] = %d, want %d", i, out[i], want[i])
		}
	}
	if Transpose([]int{125}, 10)[0] != 127 { // clamps
		t.Error("Transpose should clamp to 127")
	}
}

func TestInScale(t *testing.T) {
	rootPC, _ := music.ParseKey("A minor")
	scale := music.ScaleIntervals("minor")
	if !InScale(60, rootPC, scale) { // C is in A minor
		t.Error("C should be in A minor")
	}
	if InScale(61, rootPC, scale) { // C# is not in A minor
		t.Error("C# should NOT be in A minor")
	}
}

func mtArr() *dawmodel.Arrangement {
	a := dawmodel.New()
	a.Root, a.Scale = "A", "minor"
	a = a.AddTrack("bass", dawmodel.KindMIDI)
	a.Tracks[0].Clips = append(a.Tracks[0].Clips, dawmodel.Clip{Name: "b", Channel: 0, Program: 38})
	return a
}

func TestEvaluate_clean(t *testing.T) {
	a := mtArr()
	// A-minor bass: A(45), C(48), varied velocity, with a rest (steps 0 and 8 only).
	a, _, _ = a.AddNote("bass", "b", dawmodel.Note{Start: 0, Dur: 240, Pitch: 45, Vel: 100, Ch: 0})
	a, _, _ = a.AddNote("bass", "b", dawmodel.Note{Start: 8 * music.StepTicks, Dur: 240, Pitch: 48, Vel: 80, Ch: 0})
	if iss := Evaluate(a); len(iss) != 0 {
		t.Errorf("clean arrangement flagged: %+v", iss)
	}
}

func TestEvaluate_catchesProblems(t *testing.T) {
	a := mtArr()
	// Out of key (A minor has no C#=49), flat velocity, out of bass register (24).
	a, _, _ = a.AddNote("bass", "b", dawmodel.Note{Start: 0, Dur: 120, Pitch: 49, Vel: 90, Ch: 0})
	a, _, _ = a.AddNote("bass", "b", dawmodel.Note{Start: 8 * music.StepTicks, Dur: 120, Pitch: 24, Vel: 90, Ch: 0})
	got := map[string]bool{}
	for _, iss := range Evaluate(a) {
		got[iss.Check] = true
	}
	for _, want := range []string{"key", "velocity", "bass_register"} {
		if !got[want] {
			t.Errorf("Evaluate missed the %q problem (got %v)", want, got)
		}
	}
}

func TestEvaluate_spaceCheck(t *testing.T) {
	a := dawmodel.New()
	a.Root, a.Scale = "A", "minor"
	a = a.AddTrack("melody", dawmodel.KindMIDI)
	a.Tracks[0].Clips = append(a.Tracks[0].Clips, dawmodel.Clip{Name: "m", Channel: 2, Program: 80})
	// Onset on every one of 8 steps — no space.
	for s := 0; s < 8; s++ {
		a, _, _ = a.AddNote("melody", "m", dawmodel.Note{Start: s * music.StepTicks, Dur: 120, Pitch: 69, Vel: 70 + s, Ch: 2})
	}
	found := false
	for _, iss := range Evaluate(a) {
		if iss.Check == "space" {
			found = true
		}
	}
	if !found {
		t.Error("Evaluate should flag a wall-to-wall track for no space")
	}
}

func TestEvaluate_nilSafe(t *testing.T) {
	if iss := Evaluate(nil); len(iss) != 0 {
		t.Errorf("Evaluate(nil) should be empty, got %v", iss)
	}
}
