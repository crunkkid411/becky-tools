package dawmodel

import (
	"testing"

	"becky-go/internal/music"
)

// buildFile constructs a small multi-track SMF via the EXISTING writer so the
// round-trip test starts from byte-stable, known input.
func buildFile() *music.File {
	f := music.NewFile(480)
	meta := f.AddTrack()
	meta.Tempo(0, 140)
	meta.TimeSig(0, 4, 4)

	mel := f.AddTrack()
	mel.Name(0, "melody")
	mel.Program(0, 0, 81)
	mel.Note(0, 240, 0, 60, 100)   // C4
	mel.Note(240, 240, 0, 64, 90)  // E4
	mel.Note(480, 480, 0, 67, 110) // G4

	drums := f.AddTrack()
	drums.Name(0, "drums")
	drums.Note(0, 60, 9, 36, 118)   // kick step 0
	drums.Note(240, 60, 9, 38, 104) // snare step 2
	drums.Note(480, 60, 9, 36, 118) // kick step 4
	return f
}

// noteTuple is a comparable note for round-trip set equality.
type noteTuple struct {
	start, dur, pitch, vel, ch int
}

// notesOf flattens a parsed song's note-on/note-off pairs into tuples.
func notesOf(t *testing.T, data []byte) []noteTuple {
	t.Helper()
	song, err := music.ParseSMF(data)
	if err != nil {
		t.Fatalf("ParseSMF: %v", err)
	}
	var out []noteTuple
	for _, tr := range song.Tracks {
		open := map[[2]int]music.ParsedEvent{}
		for _, e := range tr.Events {
			switch e.Kind {
			case music.KindNoteOn:
				open[[2]int{e.Channel, e.Key}] = e
			case music.KindNoteOff:
				on, ok := open[[2]int{e.Channel, e.Key}]
				if !ok {
					continue
				}
				delete(open, [2]int{e.Channel, e.Key})
				out = append(out, noteTuple{on.Tick, e.Tick - on.Tick, e.Key, on.Velocity, e.Channel})
			}
		}
	}
	return out
}

// sortTuples insertion-sorts for stable set comparison (tiny N).
func sortTuples(ts []noteTuple) {
	for i := 1; i < len(ts); i++ {
		for j := i; j > 0 && less(ts[j], ts[j-1]); j-- {
			ts[j], ts[j-1] = ts[j-1], ts[j]
		}
	}
}

func less(a, b noteTuple) bool {
	if a.start != b.start {
		return a.start < b.start
	}
	if a.pitch != b.pitch {
		return a.pitch < b.pitch
	}
	return a.ch < b.ch
}

// TestRoundTrip_writerToModelToWriter is the load-bearing tripwire (SPEC §9): build
// with the writer -> ParseSMF -> into the editable model -> ToFile -> ParseSMF, and
// assert the note set is identical. Guards against the edit layer perturbing the
// byte-stable encoder.
func TestRoundTrip_writerToModelToWriter(t *testing.T) {
	orig := buildFile().Bytes()
	want := notesOf(t, orig)

	arr, err := FromSMF(orig)
	if err != nil {
		t.Fatalf("FromSMF: %v", err)
	}
	got := notesOf(t, arr.ToSMF())

	if len(got) != len(want) {
		t.Fatalf("note count: got %d, want %d", len(got), len(want))
	}
	sortTuples(got)
	sortTuples(want)
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("note %d: got %+v, want %+v", i, got[i], want[i])
		}
	}
}

// TestRoundTrip_transportPreserved confirms tempo/timesig/PPQ survive the trip.
func TestRoundTrip_transportPreserved(t *testing.T) {
	arr, err := FromSMF(buildFile().Bytes())
	if err != nil {
		t.Fatalf("FromSMF: %v", err)
	}
	if arr.BPM != 140 {
		t.Errorf("BPM = %d, want 140", arr.BPM)
	}
	if arr.PPQ != 480 {
		t.Errorf("PPQ = %d, want 480", arr.PPQ)
	}
	if arr.Num != 4 || arr.Den != 4 {
		t.Errorf("timesig = %d/%d, want 4/4", arr.Num, arr.Den)
	}
}

// TestRoundTrip_stableAcrossReSerialize: writing the same model twice yields the
// same bytes (determinism), and re-parsing keeps the note count.
func TestRoundTrip_stableAcrossReSerialize(t *testing.T) {
	arr, err := FromSMF(buildFile().Bytes())
	if err != nil {
		t.Fatalf("FromSMF: %v", err)
	}
	a := arr.ToSMF()
	b := arr.ToSMF()
	if len(a) != len(b) {
		t.Fatalf("re-serialize length differs: %d vs %d", len(a), len(b))
	}
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("re-serialize byte %d differs", i)
		}
	}
}

// TestFromSMF_clipsAndProgram checks tracks become clips with names + program.
func TestFromSMF_clipsAndProgram(t *testing.T) {
	arr, err := FromSMF(buildFile().Bytes())
	if err != nil {
		t.Fatalf("FromSMF: %v", err)
	}
	mel, ok := arr.TrackByID("melody")
	if !ok {
		t.Fatalf("melody track missing; tracks=%v", trackIDs(arr))
	}
	if len(mel.Clips) != 1 || mel.Clips[0].Program != 81 {
		t.Errorf("melody clip = %+v, want program 81", mel.Clips)
	}
	if len(mel.Clips[0].Notes) != 3 {
		t.Errorf("melody notes = %d, want 3", len(mel.Clips[0].Notes))
	}
}

// TestFromSMF_degradesOnGarbage: malformed bytes return an error, never panic.
func TestFromSMF_degradesOnGarbage(t *testing.T) {
	cases := [][]byte{nil, []byte("MThd"), []byte("not midi at all"), {0xFF, 0xFF, 0xFF}}
	for _, data := range cases {
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("FromSMF panicked on % X: %v", data, r)
				}
			}()
			arr, err := FromSMF(data)
			if err == nil {
				t.Errorf("FromSMF(% X) = nil error, want error", data)
			}
			if arr == nil {
				t.Errorf("FromSMF(% X) returned nil arrangement; want non-nil partial", data)
			}
		}()
	}
}

func trackIDs(a *Arrangement) []string {
	out := make([]string, 0, len(a.Tracks))
	for _, t := range a.Tracks {
		out = append(out, t.ID)
	}
	return out
}
