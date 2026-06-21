package arrange

import (
	"testing"

	"becky-go/internal/dawmodel"
	"becky-go/internal/music"
)

// drumArr builds a drums-only arrangement with the kick firing on the given step
// indices (0..15) of a single bar, in A minor.
func drumArr(t *testing.T, kickSteps ...int) *dawmodel.Arrangement {
	t.Helper()
	a := dawmodel.New()
	a.Root, a.Scale = "A", "minor"
	a = a.AddTrack("drums", dawmodel.KindMIDI)
	a.Tracks[0].Clips = append(a.Tracks[0].Clips, dawmodel.Clip{Name: "beat", Channel: 9, Program: -1})
	var err error
	for _, s := range kickSteps {
		a, _, err = a.AddNote("drums", "beat", dawmodel.Note{
			Start: s * music.StepTicks, Dur: music.StepTicks, Pitch: 36, Vel: 110, Ch: 9,
		})
		if err != nil {
			t.Fatalf("add kick: %v", err)
		}
	}
	return a
}

// bassNotes returns the bass track's notes (sorted by start).
func bassNotes(a *dawmodel.Arrangement) []dawmodel.Note {
	for _, t := range a.Tracks {
		if t.ID != "bass" {
			continue
		}
		for _, c := range t.Clips {
			if c.Name == "bass" {
				return c.Notes
			}
		}
	}
	return nil
}

func TestAddBass_locksToKick(t *testing.T) {
	a := drumArr(t, 0, 4, 8, 12) // four-on-the-floor kick
	out, err := AddBass(a, Options{Genre: "house", Seed: 1})
	if err != nil {
		t.Fatalf("AddBass: %v", err)
	}
	notes := bassNotes(out)
	if len(notes) == 0 {
		t.Fatal("no bass notes written")
	}
	// Every kick onset must have a bass note at the same tick.
	kickTicks := map[int]bool{}
	for _, s := range []int{0, 4, 8, 12} {
		kickTicks[s*music.StepTicks] = true
	}
	bassStarts := map[int]bool{}
	for _, n := range notes {
		bassStarts[n.Start] = true
	}
	for tick := range kickTicks {
		if !bassStarts[tick] {
			t.Errorf("bass does not lock to the kick at tick %d", tick)
		}
	}
}

func TestAddBass_anchorsDownbeatWithoutKick(t *testing.T) {
	// Kick only off the downbeat — bass must STILL place a root on beat 1.
	a := drumArr(t, 2, 6, 10)
	out, _ := AddBass(a, Options{Seed: 1})
	notes := bassNotes(out)
	if len(notes) == 0 || notes[0].Start != 0 {
		t.Errorf("bass must anchor the downbeat (tick 0); first note start = %v", notes)
	}
}

func TestAddBass_inRegisterAndKey(t *testing.T) {
	a := drumArr(t, 0, 8)
	out, _ := AddBass(a, Options{Genre: "house", Seed: 3})
	rootPC, scale, _ := resolveKey(a)
	scalePCs := map[int]bool{}
	for _, iv := range scale {
		scalePCs[(rootPC+iv)%12] = true
	}
	for _, n := range bassNotes(out) {
		if n.Pitch < BassMidiLo || n.Pitch > BassMidiHi {
			t.Errorf("bass note %d out of register [%d,%d]", n.Pitch, BassMidiLo, BassMidiHi)
		}
		if !scalePCs[n.Pitch%12] {
			t.Errorf("bass note %d is not in the key (pitch class %d)", n.Pitch, n.Pitch%12)
		}
	}
}

func TestAddBass_velocityNotFlat(t *testing.T) {
	a := drumArr(t, 0, 2, 4, 6, 8, 10, 12, 14)
	out, _ := AddBass(a, Options{Seed: 7})
	notes := bassNotes(out)
	first := notes[0].Vel
	varied := false
	for _, n := range notes {
		if n.Vel < BassVelLo || n.Vel > BassVelHi {
			t.Errorf("bass velocity %d out of range [%d,%d]", n.Vel, BassVelLo, BassVelHi)
		}
		if n.Vel != first {
			varied = true
		}
	}
	if !varied {
		t.Error("bass velocity is flat — the no-flat-velocity rule was not applied")
	}
}

func TestAddBass_deterministic(t *testing.T) {
	a := drumArr(t, 0, 4, 8, 12)
	o1, _ := AddBass(a, Options{Genre: "house", Seed: 9})
	o2, _ := AddBass(a, Options{Genre: "house", Seed: 9})
	n1, n2 := bassNotes(o1), bassNotes(o2)
	if len(n1) != len(n2) {
		t.Fatalf("non-deterministic note count: %d vs %d", len(n1), len(n2))
	}
	for i := range n1 {
		if n1[i] != n2[i] {
			t.Errorf("note %d differs: %+v vs %+v", i, n1[i], n2[i])
		}
	}
}

func TestAddBass_immutableAndNoDuplicate(t *testing.T) {
	a := drumArr(t, 0, 8)
	before := len(a.Tracks)
	out, err := AddBass(a, Options{Seed: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(a.Tracks) != before {
		t.Error("AddBass mutated the input arrangement")
	}
	if _, err := AddBass(out, Options{Seed: 1}); err == nil {
		t.Error("AddBass should refuse to add a second bass track")
	}
}

// drumArrBars builds a drums-only A-minor arrangement of `bars` bars, four-on-the-
// floor kick in each bar.
func drumArrBars(t *testing.T, bars int) *dawmodel.Arrangement {
	t.Helper()
	a := dawmodel.New()
	a.Root, a.Scale = "A", "minor"
	a = a.AddTrack("drums", dawmodel.KindMIDI)
	a.Tracks[0].Clips = append(a.Tracks[0].Clips, dawmodel.Clip{Name: "beat", Channel: 9, Program: -1})
	var err error
	for b := 0; b < bars; b++ {
		for _, s := range []int{0, 4, 8, 12} {
			a, _, err = a.AddNote("drums", "beat", dawmodel.Note{
				Start: (b*16 + s) * music.StepTicks, Dur: music.StepTicks, Pitch: 36, Vel: 110, Ch: 9,
			})
			if err != nil {
				t.Fatalf("add kick: %v", err)
			}
		}
	}
	return a
}

func TestAddChords_minorVIsMajor(t *testing.T) {
	// In A minor with a 4-bar progression containing V (degree 4 at bar 3), the V
	// triad's third must be raised (major V) — the leading tone.
	a := drumArrBars(t, 4)
	out, err := AddChords(a, Options{Genre: "crunkcore", Seed: 1}) // i bVII bVI V → bar 3 is V
	if err != nil {
		t.Fatalf("AddChords: %v", err)
	}
	// Bar index 3 is the V chord (degree 4). Its third should be a major third
	// above the chord root (4 semitones), not a minor third (3).
	rootPC, scale, _ := resolveKey(a)
	vRoot := music.ScaleMidi(rootPC, scale, 4, 4)
	var chord []int
	for _, t := range out.Tracks {
		if t.ID != "chords" {
			continue
		}
		for _, n := range t.Clips[0].Notes {
			if n.Start == 3*stepsPerBar*music.StepTicks {
				chord = append(chord, n.Pitch)
			}
		}
	}
	if len(chord) < 2 {
		t.Fatalf("V chord not found / too few tones: %v", chord)
	}
	// The third (second-lowest tone) minus the root pitch-class should be 4 (major).
	thirdInterval := ((chord[1]-vRoot)%12 + 12) % 12
	if thirdInterval != 4 {
		t.Errorf("minor-key V chord third interval = %d, want 4 (major V / raised leading tone)", thirdInterval)
	}
}

func TestAddLayer_dispatchAndUnknown(t *testing.T) {
	a := drumArr(t, 0, 8)
	if _, err := AddLayer(a, "bass", Options{Seed: 1}); err != nil {
		t.Errorf("AddLayer bass: %v", err)
	}
	if _, err := AddLayer(a, "kazoo", Options{Seed: 1}); err == nil {
		t.Error("unknown layer should error")
	}
}

func TestSuggestNext(t *testing.T) {
	a := drumArr(t, 0, 8) // drums only
	if got := SuggestNext(a); got != "bass" {
		t.Errorf("after drums, SuggestNext = %q, want bass", got)
	}
	withBass, _ := AddBass(a, Options{Seed: 1})
	if got := SuggestNext(withBass); got != "chords" {
		t.Errorf("after drums+bass, SuggestNext = %q, want chords", got)
	}
}

func TestNextLayer_order(t *testing.T) {
	if got := NextLayer(map[string]bool{"drums": true}); got != "bass" {
		t.Errorf("NextLayer(drums) = %q, want bass", got)
	}
	all := map[string]bool{"drums": true, "bass": true, "chords": true, "melody": true, "texture": true}
	if got := NextLayer(all); got != "" {
		t.Errorf("NextLayer(all) = %q, want empty", got)
	}
}

func TestAnalyze_reportsGapsAndNext(t *testing.T) {
	a := drumArr(t, 0, 4, 8, 12) // drums only
	kinds := map[string]int{}
	var suggestion string
	for _, f := range Analyze(a) {
		kinds[f.Kind]++
		if f.Kind == "suggestion" {
			suggestion = f.Track
		}
	}
	if kinds["missing_layer"] < 3 {
		t.Errorf("expected missing bass/chords/melody, got kinds=%+v", kinds)
	}
	if suggestion != "bass" {
		t.Errorf("next suggestion should be bass, got %q", suggestion)
	}
}

func TestAnalyze_emptyTrack(t *testing.T) {
	a := drumArr(t, 0, 8)
	a = a.AddTrack("lead", dawmodel.KindMIDI)
	found := false
	for _, f := range Analyze(a) {
		if f.Kind == "empty_track" && f.Track == "lead" {
			found = true
		}
	}
	if !found {
		t.Error("empty 'lead' track should be flagged")
	}
}

func TestJam_buildsNextLayer(t *testing.T) {
	a := drumArr(t, 0, 4, 8, 12)
	out, added, err := Jam(a, Options{Genre: "house", Seed: 1})
	if err != nil {
		t.Fatal(err)
	}
	if added != "bass" {
		t.Errorf("first jam step should add bass, got %q", added)
	}
	if _, ok := out.TrackByID("bass"); !ok {
		t.Error("jam should have built the bass track")
	}
	steps := []string{}
	cur := out
	for i := 0; i < 6; i++ {
		nxt, a2, _ := Jam(cur, Options{Seed: 1})
		if a2 == "" {
			break
		}
		steps = append(steps, a2)
		cur = nxt
	}
	if len(steps) < 2 || steps[0] != "chords" || steps[1] != "melody" {
		t.Errorf("jam order after bass = %v, want [chords melody ...]", steps)
	}
}

func TestAnalyze_nilSafe(t *testing.T) {
	if len(Analyze(nil)) != 0 {
		t.Error("Analyze(nil) should be empty")
	}
}
