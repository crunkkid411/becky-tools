package audiotrack

import (
	"errors"
	"math"
	"testing"
)

// ---------------------------------------------------------------------------
// BuildPeaks: the flat raw-slice min/max downsampler the GUI draws
// ---------------------------------------------------------------------------

func TestBuildPeaksRampIsExact(t *testing.T) {
	// A 0..9 ramp into 5 buckets -> each bucket spans 2 samples; min/max are exact.
	samples := []float32{0, 1, 2, 3, 4, 5, 6, 7, 8, 9}
	pk := BuildPeaks(samples, 5)
	if len(pk) != 5 {
		t.Fatalf("len = %d, want 5", len(pk))
	}
	wantMinMax := [][2]float32{{0, 1}, {2, 3}, {4, 5}, {6, 7}, {8, 9}}
	for i, w := range wantMinMax {
		if pk[i].Min != w[0] || pk[i].Max != w[1] {
			t.Errorf("bucket %d = [%v,%v], want [%v,%v]", i, pk[i].Min, pk[i].Max, w[0], w[1])
		}
	}
}

func TestBuildPeaksAbsTakesLargerMagnitude(t *testing.T) {
	// One bucket spanning -0.9..0.3: Abs should be 0.9 (the larger magnitude).
	pk := BuildPeaks([]float32{-0.9, 0.3}, 1)
	if len(pk) != 1 {
		t.Fatalf("len = %d, want 1", len(pk))
	}
	if !approx(float64(pk[0].Min), -0.9, 1e-6) || !approx(float64(pk[0].Max), 0.3, 1e-6) {
		t.Errorf("min/max = [%v,%v], want [-0.9,0.3]", pk[0].Min, pk[0].Max)
	}
	if !approx(float64(pk[0].Abs), 0.9, 1e-6) {
		t.Errorf("Abs = %v, want 0.9 (larger magnitude)", pk[0].Abs)
	}
}

func TestBuildPeaksDegradeCases(t *testing.T) {
	if pk := BuildPeaks(nil, 10); pk != nil {
		t.Errorf("nil samples -> %v, want nil", pk)
	}
	if pk := BuildPeaks([]float32{1, 2, 3}, 0); pk != nil {
		t.Errorf("zero buckets -> %v, want nil", pk)
	}
	if pk := BuildPeaks([]float32{1, 2, 3}, -4); pk != nil {
		t.Errorf("negative buckets -> %v, want nil", pk)
	}
	if pk := BuildPeaks([]float32{}, 5); pk != nil {
		t.Errorf("empty samples -> %v, want nil", pk)
	}
}

func TestBuildPeaksMoreBucketsThanSamples(t *testing.T) {
	// 3 samples, 8 buckets: no bucket may be left uninitialized; each reads >= 1 sample.
	samples := []float32{0.1, 0.2, 0.3}
	pk := BuildPeaks(samples, 8)
	if len(pk) != 8 {
		t.Fatalf("len = %d, want 8", len(pk))
	}
	for i, c := range pk {
		// Every column must hold an actual sample value (one of 0.1/0.2/0.3), never the
		// zero value of an unread bucket where the data is non-zero everywhere.
		if c.Max < 0.1-1e-6 || c.Max > 0.3+1e-6 {
			t.Errorf("bucket %d max = %v, out of data range (uninitialized?)", i, c.Max)
		}
	}
}

func TestBuildPeaksDeterministic(t *testing.T) {
	samples := make([]float32, 1000)
	for i := range samples {
		samples[i] = float32(math.Sin(float64(i) * 0.07))
	}
	a := BuildPeaks(samples, 64)
	b := BuildPeaks(samples, 64)
	if len(a) != len(b) {
		t.Fatalf("lengths differ: %d vs %d", len(a), len(b))
	}
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("BuildPeaks not deterministic at %d: %+v vs %+v", i, a[i], b[i])
		}
	}
}

// ---------------------------------------------------------------------------
// Mixdown (single track) via SampleSource — empty / sum / gain / placement / degrade
// ---------------------------------------------------------------------------

func TestSingleTrackMixdownEmptyTrack(t *testing.T) {
	tr := NewTrack("a")
	out, err := Mixdown(tr, 48000, ClipSource{})
	if err != nil {
		t.Errorf("empty track err = %v, want nil", err)
	}
	if out != nil {
		t.Errorf("empty track buffer = %v, want nil (silence)", out)
	}
}

func TestSingleTrackMixdownClipSourceDefaultsWhenNil(t *testing.T) {
	clip := constClip(0.5, 10)
	tr := NewTrack("a")
	tr.Regions = []Region{NewRegionFromClip("r", clip, 0)}
	// Passing a nil SampleSource must default to ClipSource (read the in-memory Clip).
	out, err := Mixdown(tr, 48000, nil)
	if err != nil {
		t.Fatalf("nil-source mixdown err = %v", err)
	}
	wantCh := 0.5 * (math.Sqrt2 / 2) // unity vol, center pan
	if !approx(float64(out[0]), wantCh, 1e-6) {
		t.Errorf("frame0 L = %v, want %v", out[0], wantCh)
	}
}

func TestSingleTrackMixdownGainAndPan(t *testing.T) {
	clip := constClip(0.5, 8)
	tr := NewTrack("a")
	tr.Pan = -1 // hard left
	tr.Regions = []Region{{ID: "r", Clip: clip, SourceIn: 0, SourceOut: 8, TimelinePos: 0, Gain: 1}}
	out, err := Mixdown(tr, 48000, ClipSource{})
	if err != nil {
		t.Fatalf("mixdown err = %v", err)
	}
	for i := 0; i < 8; i++ {
		if !approx(float64(out[i*2]), 0.5, 1e-6) {
			t.Errorf("hard-left frame %d L = %v, want 0.5", i, out[i*2])
		}
		if !approx(float64(out[i*2+1]), 0, 1e-6) {
			t.Errorf("hard-left frame %d R = %v, want 0", i, out[i*2+1])
		}
	}
}

func TestSingleTrackMixdownOverlappingRegionsSum(t *testing.T) {
	clip := constClip(0.5, 10)
	tr := NewTrack("a")
	// Two regions overlapping on [0,10): their contributions sum.
	tr.Regions = []Region{
		{ID: "r1", Clip: clip, SourceIn: 0, SourceOut: 10, TimelinePos: 0, Gain: 1},
		{ID: "r2", Clip: clip, SourceIn: 0, SourceOut: 10, TimelinePos: 0, Gain: 1},
	}
	out, err := Mixdown(tr, 48000, ClipSource{})
	if err != nil {
		t.Fatalf("mixdown err = %v", err)
	}
	one := 0.5 * (math.Sqrt2 / 2)
	if !approx(float64(out[0]), 2*one, 1e-6) {
		t.Errorf("overlapping sum frame0 L = %v, want %v (2x)", out[0], 2*one)
	}
}

func TestSingleTrackMixdownPlacementIsSampleAccurate(t *testing.T) {
	clip := constClip(0.5, 4)
	tr := NewTrack("a")
	// Region placed at timeline frame 3; frames [0,3) must be silence, [3,7) audible.
	tr.Regions = []Region{{ID: "r", Clip: clip, SourceIn: 0, SourceOut: 4, TimelinePos: 3, Gain: 1}}
	out, err := Mixdown(tr, 48000, ClipSource{})
	if err != nil {
		t.Fatalf("mixdown err = %v", err)
	}
	if len(out) != 7*2 {
		t.Fatalf("len = %d, want %d (7 frames stereo)", len(out), 7*2)
	}
	for f := 0; f < 3; f++ {
		if out[f*2] != 0 || out[f*2+1] != 0 {
			t.Errorf("pre-placement frame %d not silent: (%v,%v)", f, out[f*2], out[f*2+1])
		}
	}
	want := 0.5 * (math.Sqrt2 / 2)
	for f := 3; f < 7; f++ {
		if !approx(float64(out[f*2]), want, 1e-6) {
			t.Errorf("placed frame %d L = %v, want %v", f, out[f*2], want)
		}
	}
}

func TestSingleTrackMixdownMuteIsSilent(t *testing.T) {
	clip := constClip(0.8, 6)
	tr := NewTrack("a")
	tr.Mute = true
	tr.Regions = []Region{NewRegionFromClip("r", clip, 0)}
	out, err := Mixdown(tr, 48000, ClipSource{})
	if err != nil {
		t.Fatalf("mixdown err = %v", err)
	}
	if p := PeakAbs(out); p != 0 {
		t.Errorf("muted track peak = %v, want 0 (silent)", p)
	}
}

func TestSingleTrackMixdownDeterministic(t *testing.T) {
	clip := ToneClip(440, 0.7, 500, 48000)
	build := func() Track {
		tr := NewTrack("a")
		tr.Pan = -0.3
		tr.Regions = []Region{
			{ID: "r1", Clip: clip, SourceIn: 0, SourceOut: 500, TimelinePos: 0, Gain: 0.9, FadeInFrames: 50, FadeOutFrames: 50},
			{ID: "r2", Clip: clip, SourceIn: 0, SourceOut: 500, TimelinePos: 200, Gain: 0.6},
		}
		return tr
	}
	a, _ := Mixdown(build(), 48000, ClipSource{})
	b, _ := Mixdown(build(), 48000, ClipSource{})
	if len(a) != len(b) {
		t.Fatalf("lengths differ: %d vs %d", len(a), len(b))
	}
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("single-track mixdown not deterministic at %d: %v vs %v", i, a[i], b[i])
		}
	}
}

func TestSingleTrackMixdownDefaultsSampleRate(t *testing.T) {
	clip := constClip(0.5, 4)
	tr := NewTrack("a")
	tr.Regions = []Region{NewRegionFromClip("r", clip, 0)}
	// sampleRate <= 0 must not change the rendered SAMPLES (rate only affects metadata),
	// and must not crash. We just assert it renders the same buffer as an explicit rate.
	a, _ := Mixdown(tr, 0, ClipSource{})
	b, _ := Mixdown(tr, DefaultSampleRate, ClipSource{})
	if len(a) != len(b) {
		t.Fatalf("rate-default changed length: %d vs %d", len(a), len(b))
	}
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("rate-default changed sample %d: %v vs %v", i, a[i], b[i])
		}
	}
}

// failSource resolves the named region but reports a miss for all others, to drive the
// degrade (partial-mix + wrapped error) path deterministically.
type failSource struct{ ok string }

func (f failSource) SamplesFor(r Region) ([]float32, int, error) {
	if r.ID == f.ok {
		return r.Clip.Samples, r.Clip.Channels, nil
	}
	return nil, 0, errors.New("synthetic missing source")
}

func TestSingleTrackMixdownPartialDegrade(t *testing.T) {
	clip := constClip(0.5, 10)
	tr := NewTrack("a")
	tr.Regions = []Region{
		{ID: "good", Clip: clip, SourceIn: 0, SourceOut: 10, TimelinePos: 0, Gain: 1},
		{ID: "bad", Clip: clip, SourceIn: 0, SourceOut: 10, TimelinePos: 0, Gain: 1},
	}
	out, err := Mixdown(tr, 48000, failSource{ok: "good"})
	if err == nil {
		t.Fatalf("partial mix should surface a wrapped error naming the skipped region")
	}
	if errors.Is(err, ErrNoRenderableRegions) {
		t.Errorf("partial mix should NOT be ErrNoRenderableRegions (one region rendered)")
	}
	// The good region still rendered: frame0 is its single contribution.
	one := 0.5 * (math.Sqrt2 / 2)
	if !approx(float64(out[0]), one, 1e-6) {
		t.Errorf("partial mix frame0 = %v, want %v (only the good region)", out[0], one)
	}
}

func TestSingleTrackMixdownAllSkippedIsSentinel(t *testing.T) {
	clip := constClip(0.5, 10)
	tr := NewTrack("a")
	tr.Regions = []Region{
		{ID: "x", Clip: clip, SourceIn: 0, SourceOut: 10, TimelinePos: 0, Gain: 1},
		{ID: "y", Clip: clip, SourceIn: 0, SourceOut: 10, TimelinePos: 0, Gain: 1},
	}
	out, err := Mixdown(tr, 48000, failSource{ok: "none"})
	if !errors.Is(err, ErrNoRenderableRegions) {
		t.Fatalf("all-skipped err = %v, want wrapped ErrNoRenderableRegions", err)
	}
	// A valid (silent) buffer is still returned so the caller never gets a nil + len.
	if len(out) != 10*2 {
		t.Errorf("all-skipped buffer len = %d, want %d (valid silent buffer)", len(out), 10*2)
	}
	if PeakAbs(out) != 0 {
		t.Errorf("all-skipped buffer should be silent, peak = %v", PeakAbs(out))
	}
}

// ---------------------------------------------------------------------------
// ClipSource / FileSource resolvers
// ---------------------------------------------------------------------------

func TestClipSourceNilClipIsError(t *testing.T) {
	if _, _, err := (ClipSource{}).SamplesFor(Region{ID: "r"}); err == nil {
		t.Errorf("ClipSource.SamplesFor(nil clip) should error, got nil")
	}
	clip := constClip(0.5, 4)
	s, ch, err := (ClipSource{}).SamplesFor(Region{ID: "r", Clip: clip})
	if err != nil || ch != 1 || len(s) != 4 {
		t.Errorf("ClipSource on a real clip = (%d samples, %d ch, %v), want (4,1,nil)", len(s), ch, err)
	}
}

func TestFileSourcePrefersInMemoryAndCaches(t *testing.T) {
	clip := constClip(0.5, 4)
	fs := NewFileSource()
	// An in-memory clip with samples is used directly (no file IO, no path needed).
	s, ch, err := fs.SamplesFor(Region{ID: "r", Clip: clip})
	if err != nil || ch != 1 || len(s) != 4 {
		t.Errorf("FileSource in-memory = (%d,%d,%v), want (4,1,nil)", len(s), ch, err)
	}
	// No samples and no path -> a clean error, not a panic.
	if _, _, err := fs.SamplesFor(Region{ID: "r2", Clip: &Clip{Channels: 1}}); err == nil {
		t.Errorf("FileSource with no samples/path should error")
	}
	if _, _, err := fs.SamplesFor(Region{ID: "r3"}); err == nil {
		t.Errorf("FileSource with nil clip should error")
	}
}

func TestFileSourceMixdownRoundTrip(t *testing.T) {
	// Write a real WAV, place a region that references it BY PATH only (no preloaded
	// samples), and confirm FileSource decodes it so the bounce is non-silent. This is
	// the on-disk path the GUI uses for a Track built from file references.
	dir := t.TempDir()
	path := dir + "/take.wav"
	tone := ToneClip(440, 0.8, 2400, 48000)
	if err := WritePCM16WAV(path, tone.Samples, 48000, 1); err != nil {
		t.Fatalf("write wav: %v", err)
	}
	tr := NewTrack("vox")
	// Region with a Clip that carries ONLY the path (no decoded samples).
	tr.Regions = []Region{{
		ID:        "take",
		Clip:      &Clip{Path: path, Channels: 1, SampleRate: 48000},
		SourceIn:  0,
		SourceOut: 2400,
		Gain:      1,
	}}
	out, err := Mixdown(tr, 48000, NewFileSource())
	if err != nil {
		t.Fatalf("file-source mixdown err = %v", err)
	}
	if PeakAbs(out) < 0.1 {
		t.Errorf("file-source bounce peak = %v, want non-silent (>0.1)", PeakAbs(out))
	}
}
