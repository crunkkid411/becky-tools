package ctledit

import (
	"testing"

	"becky-go/internal/dawmodel"
)

// makeRouteArrangement builds a 4-track arrangement suitable for autoroute testing:
// drums, bass, chords, melody — the exact tracks the DefaultRuleset routes.
func makeRouteArrangement(t *testing.T) *dawmodel.Arrangement {
	t.Helper()
	a := dawmodel.New()
	a.BPM = 120
	for _, id := range []string{"drums", "bass", "chords", "melody"} {
		a = a.AddTrack(id, dawmodel.KindMIDI)
		a = mustAddClip(t, a, id, id+"-clip1")
	}
	return a
}

// trackBus returns the Strip.Bus for a track, or "" when the track is not found.
func trackBus(a *dawmodel.Arrangement, trackID string) string {
	for _, tr := range a.Tracks {
		if tr.ID == trackID {
			return tr.Strip.Bus
		}
	}
	return ""
}

// TestOpRoute_ApplyRoutesAllTracks verifies that OpRoute calls autoroute.Apply and
// assigns the buses proven by the offline `becky-route` smoke-test:
//
//	drums -> DRUMS, bass -> BASS, chords -> SYNTH, melody -> SYNTH
func TestOpRoute_ApplyRoutesAllTracks(t *testing.T) {
	a := makeRouteArrangement(t)

	batch := BeckyEditBatch{Edits: []BeckyEdit{{Op: OpRoute}}}
	got, result, err := Apply(a, batch, nil)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	// Assert values, not truthiness (STANDARDS-ENGINEERING).
	if result.Applied < 1 {
		t.Errorf("result.Applied = %d, want >= 1", result.Applied)
	}
	if result.Skipped != 0 {
		t.Errorf("result.Skipped = %d, want 0", result.Skipped)
	}

	cases := []struct {
		track   string
		wantBus string
	}{
		{"drums", "DRUMS"},
		{"bass", "BASS"},
		{"chords", "SYNTH"},
		{"melody", "SYNTH"},
	}
	for _, tc := range cases {
		gotBus := trackBus(got, tc.track)
		if gotBus != tc.wantBus {
			t.Errorf("track %q: Strip.Bus = %q, want %q", tc.track, gotBus, tc.wantBus)
		}
	}
}

// TestOpRoute_ImmutableInput verifies that Apply never mutates the input arrangement.
func TestOpRoute_ImmutableInput(t *testing.T) {
	a := makeRouteArrangement(t)

	// Snapshot the input buses before Apply (dawmodel may pre-assign buses on AddTrack).
	type busSnap struct{ id, bus string }
	before := make([]busSnap, len(a.Tracks))
	for i, tr := range a.Tracks {
		before[i] = busSnap{tr.ID, tr.Strip.Bus}
	}

	batch := BeckyEditBatch{Edits: []BeckyEdit{{Op: OpRoute}}}
	got, _, _ := Apply(a, batch, nil)

	// Input buses are unchanged (same values as snapshot).
	for i, tr := range a.Tracks {
		if tr.Strip.Bus != before[i].bus {
			t.Errorf("input mutated: track %q Strip.Bus changed from %q to %q after Apply",
				tr.ID, before[i].bus, tr.Strip.Bus)
		}
	}
	// Output is a new pointer.
	if got == a {
		t.Error("Apply returned the same *Arrangement pointer -- expected a new copy")
	}
}

// TestOpRoute_EmptyArrangement verifies degrade-never-crash: an arrangement with no
// tracks skips (not applied) and returns the original arrangement intact.
func TestOpRoute_EmptyArrangement(t *testing.T) {
	a := dawmodel.New()

	batch := BeckyEditBatch{Edits: []BeckyEdit{{Op: OpRoute}}}
	got, result, _ := Apply(a, batch, nil)

	if result.Applied != 0 {
		t.Errorf("result.Applied = %d, want 0 on empty arrangement", result.Applied)
	}
	if result.Skipped != 1 {
		t.Errorf("result.Skipped = %d, want 1 on empty arrangement", result.Skipped)
	}
	// Returned arrangement is still valid (not nil).
	if got == nil {
		t.Fatal("Apply returned nil arrangement")
	}
	if len(got.Tracks) != 0 {
		t.Errorf("got.Tracks length = %d, want 0", len(got.Tracks))
	}
}

// TestOpRoute_JSONRoundTrip verifies OpRoute survives JSON encode -> ParseBatch ->
// Apply, matching the real agent-box path (model emits JSON -> ParseBatch -> Apply).
func TestOpRoute_JSONRoundTrip(t *testing.T) {
	a := makeRouteArrangement(t)

	raw := `{"summary":"route everything","edits":[{"op":"route"}]}`
	batch, err := ParseBatch([]byte(raw))
	if err != nil {
		t.Fatalf("ParseBatch: %v", err)
	}
	if len(batch.Edits) != 1 {
		t.Fatalf("len(batch.Edits) = %d, want 1", len(batch.Edits))
	}
	if batch.Edits[0].Op != OpRoute {
		t.Fatalf("batch.Edits[0].Op = %q, want %q", batch.Edits[0].Op, OpRoute)
	}

	got, result, err := Apply(a, batch, nil)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if result.Applied < 1 {
		t.Errorf("result.Applied = %d, want >= 1", result.Applied)
	}
	if bus := trackBus(got, "drums"); bus != "DRUMS" {
		t.Errorf("drums Strip.Bus = %q, want DRUMS after JSON round-trip", bus)
	}
}

// TestParsePhrase_RouteVariants verifies that the deterministic keyword parser
// returns an OpRoute batch for common routing phrases.
func TestParsePhrase_RouteVariants(t *testing.T) {
	a := makeRouteArrangement(t)

	phrases := []string{
		"route the tracks",
		"route everything",
		"auto route",
		"set up the buses",
		"route to buses",
	}

	for _, phrase := range phrases {
		t.Run(phrase, func(t *testing.T) {
			b, ok := ParsePhrase(phrase, a)
			if !ok {
				t.Fatalf("ParsePhrase(%q) ok = false, want true", phrase)
			}
			if len(b.Edits) != 1 {
				t.Fatalf("ParsePhrase(%q): len(Edits) = %d, want 1", phrase, len(b.Edits))
			}
			if b.Edits[0].Op != OpRoute {
				t.Errorf("ParsePhrase(%q): Edits[0].Op = %q, want %q", phrase, b.Edits[0].Op, OpRoute)
			}
			if b.Summary == "" {
				t.Errorf("ParsePhrase(%q): Summary is empty", phrase)
			}
		})
	}
}

// TestParsePhrase_RouteWorksWithoutDrumClip verifies that OpRoute phrase detection
// fires even when no drum clip exists (the routing check must be BEFORE findDrumClipRef).
func TestParsePhrase_RouteWorksWithoutDrumClip(t *testing.T) {
	// Arrangement with only a melodic track -- no channel-9 / GM percussion.
	a := dawmodel.New()
	a = a.AddTrack("bass", dawmodel.KindMIDI)
	a = mustAddClip(t, a, "bass", "clip1")

	b, ok := ParsePhrase("route the tracks", a)
	if !ok {
		t.Fatalf("ParsePhrase without drum clip: ok = false, want true")
	}
	if len(b.Edits) != 1 {
		t.Fatalf("len(Edits) = %d, want 1", len(b.Edits))
	}
	if b.Edits[0].Op != OpRoute {
		t.Errorf("Edits[0].Op = %q, want %q", b.Edits[0].Op, OpRoute)
	}
}
