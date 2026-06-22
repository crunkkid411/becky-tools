package main

import (
	"math"
	"testing"
)

func TestCosine(t *testing.T) {
	tests := []struct {
		name string
		a, b []float64
		want float64
	}{
		{"identical", []float64{1, 0, 0}, []float64{1, 0, 0}, 1.0},
		{"orthogonal", []float64{1, 0, 0}, []float64{0, 1, 0}, 0.0},
		{"opposite", []float64{1, 0}, []float64{-1, 0}, -1.0},
		{"unnormalized identical", []float64{2, 0}, []float64{5, 0}, 1.0},
		{"empty", []float64{}, []float64{}, 0.0},
		{"mismatched length", []float64{1, 2, 3}, []float64{1, 2}, 0.0},
		{"zero vector", []float64{0, 0}, []float64{1, 1}, 0.0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := cosine(tt.a, tt.b)
			if math.Abs(got-tt.want) > 1e-9 {
				t.Errorf("cosine(%v,%v) = %v, want %v", tt.a, tt.b, got, tt.want)
			}
		})
	}
}

func TestNormalizeIsUnitAndImmutable(t *testing.T) {
	in := []float64{3, 4} // magnitude 5
	out := normalize(in)
	// Magnitude of result must be 1.
	mag := math.Sqrt(out[0]*out[0] + out[1]*out[1])
	if math.Abs(mag-1.0) > 1e-9 {
		t.Errorf("normalized magnitude = %v, want 1.0", mag)
	}
	// Input must not be mutated (immutability rule).
	if in[0] != 3 || in[1] != 4 {
		t.Errorf("normalize mutated input: %v", in)
	}
}

func TestAverageNormalized(t *testing.T) {
	// Two vectors pointing the same way average to that direction (unit length).
	vecs := [][]float64{{1, 0}, {2, 0}}
	got := averageNormalized(vecs)
	if math.Abs(got[0]-1.0) > 1e-9 || math.Abs(got[1]) > 1e-9 {
		t.Errorf("averageNormalized = %v, want [1 0]", got)
	}
	if averageNormalized(nil) != nil {
		t.Errorf("averageNormalized(nil) should be nil")
	}
}

func TestParseHash(t *testing.T) {
	tests := []struct {
		in    string
		want  uint64
		valid bool
	}{
		{"00c0c0c0fcffffff", 0x00c0c0c0fcffffff, true},
		{"0x00c0c0c0fcffffff", 0x00c0c0c0fcffffff, true},
		{"255", 255, true}, // decimal
		{"", 0, false},
		{"   ", 0, false},
		{"not-a-hash", 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			got, ok := parseHash(tt.in)
			if ok != tt.valid {
				t.Fatalf("parseHash(%q) valid=%v, want %v", tt.in, ok, tt.valid)
			}
			if ok && got != tt.want {
				t.Errorf("parseHash(%q) = %d, want %d", tt.in, got, tt.want)
			}
		})
	}
}

func TestParseHashRoundTrip16Hex(t *testing.T) {
	// A 16-char digest must parse to the same uint64 it encodes.
	const h uint64 = 0xdeadbeefcafef00d
	got, ok := parseHash("deadbeefcafef00d")
	if !ok || got != h {
		t.Errorf("round-trip parseHash = %x (ok=%v), want %x", got, ok, h)
	}
}

func TestLocationConfidence(t *testing.T) {
	cases := []struct {
		hamming int
		want    float64
	}{
		{0, 1.0},
		{64, 0.0},
		{32, 0.5},
	}
	for _, c := range cases {
		got := locationConfidence(c.hamming)
		if math.Abs(got-c.want) > 1e-9 {
			t.Errorf("locationConfidence(%d) = %v, want %v", c.hamming, got, c.want)
		}
	}
	// Negative clamp guard (shouldn't happen, but defensive).
	if locationConfidence(100) != 0 {
		t.Errorf("locationConfidence(100) should clamp to 0")
	}
}

func TestMinHamming(t *testing.T) {
	sampled := []uint64{0x0, 0xFF}
	refs := []uint64{0x1} // 0x0 differs by 1 bit
	if d := minHamming(sampled, refs); d != 1 {
		t.Errorf("minHamming = %d, want 1", d)
	}
	// No refs -> max distance (64).
	if d := minHamming(sampled, nil); d != 64 {
		t.Errorf("minHamming(no refs) = %d, want 64", d)
	}
}

func TestBuildConcatFilter(t *testing.T) {
	spans := []SpeakerSpan{{Start: 0.74, End: 6.038}, {Start: 19.032, End: 19.707}}
	got := buildConcatFilter(spans)
	want := "[0:a]atrim=start=0.740:end=6.038,asetpts=PTS-STARTPTS[a0];" +
		"[0:a]atrim=start=19.032:end=19.707,asetpts=PTS-STARTPTS[a1];" +
		"[a0][a1]concat=n=2:v=0:a=1[out]"
	if got != want {
		t.Errorf("buildConcatFilter mismatch:\n got=%s\nwant=%s", got, want)
	}
}

func TestMatchSpeakersThresholdGate(t *testing.T) {
	// One speaker whose embedding equals the enrolled one (cosine 1.0).
	emb := normalize([]float64{1, 2, 3})
	speakers := []speakerAudio{
		{id: "SPEAKER_00", segments: []SpeakerSpan{{0, 1}}, embedding: emb},
		{id: "SPEAKER_01", segments: []SpeakerSpan{{1, 2}}, embedding: normalize([]float64{-1, -2, -3})},
	}
	enrolled := []enrolledVoice{{name: "Defendant", embedding: emb}}

	// SPEAKER_00 is a perfect (cosine 1.0) match: clears detection 0.5, naming 0.75, and
	// the margin (single enrollee -> margin == best == 1.0). SPEAKER_01 is anti-correlated.
	opts := voiceOptions{threshold: 0.5, nameThreshold: 0.75, nameMargin: 0.06}
	ids := matchSpeakers(speakers, enrolled, opts)
	if len(ids) != 1 || ids[0].Name != "Defendant" || ids[0].SpeakerID != "SPEAKER_00" {
		t.Fatalf("expected SPEAKER_00 -> Defendant, got %+v", ids)
	}
	unids := unmatchedDescriptions(speakers, enrolled, opts)
	if len(unids) != 1 || unids[0].SpeakerID != "SPEAKER_01" || unids[0].Confidence != 0.0 {
		t.Fatalf("expected SPEAKER_01 unidentified at conf 0, got %+v", unids)
	}

	// Raising the DETECTION threshold above 1.0 should identify nobody (discrimination).
	hi := voiceOptions{threshold: 1.01, nameThreshold: 0.75, nameMargin: 0.06}
	if got := matchSpeakers(speakers, enrolled, hi); len(got) != 0 {
		t.Errorf("detection threshold 1.01 should match nobody, got %+v", got)
	}
}

func TestTitleize(t *testing.T) {
	cases := map[string]string{
		"defendant":       "Defendant",
		"defendants-home": "Defendants Home",
		"living_room":     "Living Room",
	}
	for in, want := range cases {
		if got := titleize(in); got != want {
			t.Errorf("titleize(%q) = %q, want %q", in, got, want)
		}
	}
}
