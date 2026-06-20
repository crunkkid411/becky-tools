package audiotrack

import (
	"math"
	"os"
	"path/filepath"
	"testing"
)

// approx reports whether a and b are within eps of each other.
func approx(a, b, eps float64) bool { return math.Abs(a-b) <= eps }

// constClip makes a mono clip whose every sample is v (a flat DC level), useful for
// exact gain/fade/pan arithmetic where the source value is a known constant.
func constClip(v float32, frames int) *Clip {
	s := make([]float32, frames)
	for i := range s {
		s[i] = v
	}
	return &Clip{Path: "synthetic:const", Channels: 1, SampleRate: DefaultSampleRate, Samples: s}
}

// stereoConstClip makes a 2-channel clip with L=lv, R=rv on every frame.
func stereoConstClip(lv, rv float32, frames int) *Clip {
	s := make([]float32, frames*2)
	for i := 0; i < frames; i++ {
		s[i*2] = lv
		s[i*2+1] = rv
	}
	return &Clip{Path: "synthetic:stereo", Channels: 2, SampleRate: DefaultSampleRate, Samples: s}
}

// ---------------------------------------------------------------------------
// Model: Clip / Region / Track / Project geometry
// ---------------------------------------------------------------------------

func TestClipFramesAndDuration(t *testing.T) {
	tests := []struct {
		name       string
		clip       *Clip
		wantFrames int
		wantSec    float64
	}{
		{"nil", nil, 0, 0},
		{"mono", &Clip{Channels: 1, SampleRate: 48000, Samples: make([]float32, 48000)}, 48000, 1.0},
		{"stereo", &Clip{Channels: 2, SampleRate: 48000, Samples: make([]float32, 96000)}, 48000, 1.0},
		{"zero-rate", &Clip{Channels: 1, SampleRate: 0, Samples: make([]float32, 100)}, 100, 0},
		{"zero-chan", &Clip{Channels: 0, SampleRate: 48000, Samples: make([]float32, 100)}, 0, 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.clip.Frames(); got != tc.wantFrames {
				t.Errorf("Frames() = %d, want %d", got, tc.wantFrames)
			}
			if got := tc.clip.DurationSec(); !approx(got, tc.wantSec, 1e-9) {
				t.Errorf("DurationSec() = %v, want %v", got, tc.wantSec)
			}
		})
	}
}

func TestRegionLenAndEnd(t *testing.T) {
	tests := []struct {
		name             string
		r                Region
		wantLen, wantEnd int
	}{
		{"normal", Region{SourceIn: 10, SourceOut: 50, TimelinePos: 100}, 40, 140},
		{"inverted-clamps-to-zero", Region{SourceIn: 50, SourceOut: 10, TimelinePos: 5}, 0, 5},
		{"zero", Region{}, 0, 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.r.LenFrames(); got != tc.wantLen {
				t.Errorf("LenFrames() = %d, want %d", got, tc.wantLen)
			}
			if got := tc.r.TimelineEnd(); got != tc.wantEnd {
				t.Errorf("TimelineEnd() = %d, want %d", got, tc.wantEnd)
			}
		})
	}
}

func TestRegionNormalizeClamps(t *testing.T) {
	clip := constClip(0.5, 100)
	tests := []struct {
		name string
		in   Region
		want Region // only the fields we assert on
	}{
		{
			name: "negative gain -> 0, negative pos -> 0",
			in:   Region{Clip: clip, SourceIn: 0, SourceOut: 50, TimelinePos: -7, Gain: -3},
			want: Region{SourceIn: 0, SourceOut: 50, TimelinePos: 0, Gain: 0},
		},
		{
			name: "source window clamped to clip length",
			in:   Region{Clip: clip, SourceIn: -5, SourceOut: 200, TimelinePos: 0, Gain: 1},
			want: Region{SourceIn: 0, SourceOut: 100, TimelinePos: 0, Gain: 1},
		},
		{
			name: "inverted source window collapses",
			in:   Region{Clip: clip, SourceIn: 60, SourceOut: 20, TimelinePos: 0, Gain: 1},
			want: Region{SourceIn: 60, SourceOut: 60, TimelinePos: 0, Gain: 1},
		},
		{
			name: "overlapping fades shrunk to fit region length",
			in:   Region{Clip: clip, SourceIn: 0, SourceOut: 10, TimelinePos: 0, Gain: 1, FadeInFrames: 8, FadeOutFrames: 8},
			want: Region{SourceIn: 0, SourceOut: 10, TimelinePos: 0, Gain: 1, FadeInFrames: 8, FadeOutFrames: 2},
		},
		{
			name: "negative fades clamp to 0",
			in:   Region{Clip: clip, SourceIn: 0, SourceOut: 10, TimelinePos: 0, Gain: 1, FadeInFrames: -4, FadeOutFrames: -1},
			want: Region{SourceIn: 0, SourceOut: 10, TimelinePos: 0, Gain: 1, FadeInFrames: 0, FadeOutFrames: 0},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.in.Normalize()
			if got.SourceIn != tc.want.SourceIn || got.SourceOut != tc.want.SourceOut {
				t.Errorf("source window = [%d,%d), want [%d,%d)", got.SourceIn, got.SourceOut, tc.want.SourceIn, tc.want.SourceOut)
			}
			if got.TimelinePos != tc.want.TimelinePos {
				t.Errorf("TimelinePos = %d, want %d", got.TimelinePos, tc.want.TimelinePos)
			}
			if !approx(got.Gain, tc.want.Gain, 1e-9) {
				t.Errorf("Gain = %v, want %v", got.Gain, tc.want.Gain)
			}
			if got.FadeInFrames != tc.want.FadeInFrames || got.FadeOutFrames != tc.want.FadeOutFrames {
				t.Errorf("fades = (%d,%d), want (%d,%d)", got.FadeInFrames, got.FadeOutFrames, tc.want.FadeInFrames, tc.want.FadeOutFrames)
			}
		})
	}
}

func TestNewTrackDefaults(t *testing.T) {
	tr := NewTrack("Lead Vox")
	if tr.Name != "Lead Vox" {
		t.Errorf("Name = %q, want Lead Vox", tr.Name)
	}
	if tr.Volume != 1 {
		t.Errorf("Volume = %v, want unity (1) — a zero-value Track would render silence", tr.Volume)
	}
	if tr.Pan != 0 {
		t.Errorf("Pan = %v, want centered (0)", tr.Pan)
	}
}

func TestProjectFrameSecRoundTrip(t *testing.T) {
	p := NewProject(48000)
	if p.SampleRate != 48000 {
		t.Fatalf("SampleRate = %d", p.SampleRate)
	}
	// 0.5s -> 24000 frames -> 0.5s.
	if f := p.SecToFrames(0.5); f != 24000 {
		t.Errorf("SecToFrames(0.5) = %d, want 24000", f)
	}
	if s := p.FramesToSec(24000); !approx(s, 0.5, 1e-9) {
		t.Errorf("FramesToSec(24000) = %v, want 0.5", s)
	}
	if NewProject(0).SampleRate != DefaultSampleRate {
		t.Errorf("NewProject(0) should default the rate")
	}
}

func TestProjectLenFrames(t *testing.T) {
	clip := constClip(0.5, 100)
	p := NewProject(48000).
		AddTrack(NewTrack("a")).
		AddTrack(NewTrack("b"))
	p = p.AddRegion(0, Region{Clip: clip, SourceIn: 0, SourceOut: 100, TimelinePos: 0, Gain: 1})
	p = p.AddRegion(1, Region{Clip: clip, SourceIn: 0, SourceOut: 50, TimelinePos: 200, Gain: 1})
	// Track b's region ends at 250, the furthest.
	if got := p.LenFrames(); got != 250 {
		t.Errorf("LenFrames() = %d, want 250", got)
	}
	if got := NewProject(48000).LenFrames(); got != 0 {
		t.Errorf("empty project LenFrames() = %d, want 0", got)
	}
}

func TestNewRegionFromClip(t *testing.T) {
	clip := constClip(0.5, 80)
	r := NewRegionFromClip("r1", clip, 1000)
	if r.ID != "r1" || r.SourceIn != 0 || r.SourceOut != 80 || r.TimelinePos != 1000 || r.Gain != 1 {
		t.Errorf("NewRegionFromClip = %+v, want full clip at 1000 unity gain", r)
	}
}

// ---------------------------------------------------------------------------
// Immutable edits: add / move / trim / split / gain / fade
// ---------------------------------------------------------------------------

func TestEditsAreImmutable(t *testing.T) {
	clip := constClip(0.5, 100)
	orig := NewProject(48000).AddTrack(NewTrack("a"))
	orig = orig.AddRegion(0, Region{Clip: clip, SourceIn: 0, SourceOut: 100, TimelinePos: 0, Gain: 1})

	// A move must not touch the original.
	moved := orig.MoveRegion(0, 0, 500)
	if orig.Tracks[0].Regions[0].TimelinePos != 0 {
		t.Errorf("original mutated by MoveRegion: pos = %d", orig.Tracks[0].Regions[0].TimelinePos)
	}
	if moved.Tracks[0].Regions[0].TimelinePos != 500 {
		t.Errorf("moved copy pos = %d, want 500", moved.Tracks[0].Regions[0].TimelinePos)
	}

	// A gain change must not touch the original.
	louder := orig.SetRegionGain(0, 0, 2)
	if orig.Tracks[0].Regions[0].Gain != 1 {
		t.Errorf("original mutated by SetRegionGain: gain = %v", orig.Tracks[0].Regions[0].Gain)
	}
	if louder.Tracks[0].Regions[0].Gain != 2 {
		t.Errorf("louder copy gain = %v, want 2", louder.Tracks[0].Regions[0].Gain)
	}
}

func TestInvalidIndicesAreNoOps(t *testing.T) {
	clip := constClip(0.5, 100)
	p := NewProject(48000).AddTrack(NewTrack("a"))
	p = p.AddRegion(0, NewRegionFromClip("r", clip, 0))

	// None of these should panic; each returns an unchanged-shape copy.
	cases := []Project{
		p.MoveRegion(9, 0, 100),
		p.MoveRegion(0, 9, 100),
		p.SetRegionGain(0, 9, 2),
		p.RemoveRegion(9, 0),
		p.RemoveTrack(9),
		p.TrimRegionStart(5, 5, 10),
		p.SplitRegion(0, 9, 50, "-b"),
		p.MoveRegionToTrack(0, 9, 0, 0),
	}
	for i, c := range cases {
		if len(c.Tracks) != 1 || len(c.Tracks[0].Regions) != 1 {
			t.Errorf("case %d: shape changed unexpectedly: %d tracks", i, len(c.Tracks))
		}
	}
}

func TestMoveRegionToTrack(t *testing.T) {
	clip := constClip(0.5, 100)
	p := NewProject(48000).AddTrack(NewTrack("a")).AddTrack(NewTrack("b"))
	p = p.AddRegion(0, NewRegionFromClip("r", clip, 0))
	out := p.MoveRegionToTrack(0, 0, 1, 300)
	if len(out.Tracks[0].Regions) != 0 {
		t.Errorf("source track should be empty, has %d", len(out.Tracks[0].Regions))
	}
	if len(out.Tracks[1].Regions) != 1 {
		t.Fatalf("dest track should have 1 region, has %d", len(out.Tracks[1].Regions))
	}
	if out.Tracks[1].Regions[0].TimelinePos != 300 {
		t.Errorf("moved region pos = %d, want 300", out.Tracks[1].Regions[0].TimelinePos)
	}
}

func TestTrimStartKeepsAudioAnchored(t *testing.T) {
	clip := constClip(0.5, 100)
	p := NewProject(48000).AddTrack(NewTrack("a"))
	// Region plays source [0,100) placed at timeline 1000.
	p = p.AddRegion(0, Region{Clip: clip, SourceIn: 0, SourceOut: 100, TimelinePos: 1000, Gain: 1})
	// Trim 20 frames off the left in source frames; kept audio stays anchored on timeline.
	out := p.TrimRegionStart(0, 0, 20)
	r := out.Tracks[0].Regions[0]
	if r.SourceIn != 20 {
		t.Errorf("SourceIn = %d, want 20", r.SourceIn)
	}
	if r.TimelinePos != 1020 {
		t.Errorf("TimelinePos = %d, want 1020 (kept audio anchored)", r.TimelinePos)
	}
	if r.LenFrames() != 80 {
		t.Errorf("LenFrames = %d, want 80", r.LenFrames())
	}
}

func TestTrimEndFixedLeftEdge(t *testing.T) {
	clip := constClip(0.5, 100)
	p := NewProject(48000).AddTrack(NewTrack("a"))
	p = p.AddRegion(0, Region{Clip: clip, SourceIn: 0, SourceOut: 100, TimelinePos: 500, Gain: 1})
	// Shorten from the right by 30 frames.
	out := p.TrimRegionEnd(0, 0, -30)
	r := out.Tracks[0].Regions[0]
	if r.SourceOut != 70 || r.TimelinePos != 500 {
		t.Errorf("after trim end: out=%d pos=%d, want out=70 pos=500", r.SourceOut, r.TimelinePos)
	}
	// Extending past the clip is clamped by Normalize.
	out2 := p.TrimRegionEnd(0, 0, 9999)
	if out2.Tracks[0].Regions[0].SourceOut != 100 {
		t.Errorf("SourceOut = %d, want clamped to 100", out2.Tracks[0].Regions[0].SourceOut)
	}
}

func TestSplitRegion(t *testing.T) {
	clip := constClip(0.5, 100)
	p := NewProject(48000).AddTrack(NewTrack("a"))
	// Region source [0,100) at timeline 0 -> timeline [0,100).
	p = p.AddRegion(0, Region{ID: "r", Clip: clip, SourceIn: 0, SourceOut: 100, TimelinePos: 0, Gain: 1, FadeInFrames: 5})
	out := p.SplitRegion(0, 0, 40, "-b")
	regions := out.Tracks[0].Regions
	if len(regions) != 2 {
		t.Fatalf("split produced %d regions, want 2", len(regions))
	}
	left, right := regions[0], regions[1]
	if left.SourceIn != 0 || left.SourceOut != 40 || left.TimelinePos != 0 {
		t.Errorf("left = src[%d,%d)@%d, want src[0,40)@0", left.SourceIn, left.SourceOut, left.TimelinePos)
	}
	if right.SourceIn != 40 || right.SourceOut != 100 || right.TimelinePos != 40 {
		t.Errorf("right = src[%d,%d)@%d, want src[40,100)@40", right.SourceIn, right.SourceOut, right.TimelinePos)
	}
	if right.ID != "r-b" {
		t.Errorf("right ID = %q, want r-b", right.ID)
	}
	if right.FadeInFrames != 0 {
		t.Errorf("right fade-in = %d, want 0 (head fade belongs to left)", right.FadeInFrames)
	}
	// The two halves cover the original timeline span contiguously.
	if left.TimelineEnd() != right.TimelinePos {
		t.Errorf("split is not contiguous: leftEnd=%d rightPos=%d", left.TimelineEnd(), right.TimelinePos)
	}

	// A cut outside the region is a no-op.
	noop := p.SplitRegion(0, 0, 9999, "-b")
	if len(noop.Tracks[0].Regions) != 1 {
		t.Errorf("out-of-range split should be a no-op, got %d regions", len(noop.Tracks[0].Regions))
	}
}

func TestSetRegionFadesLeaveUnchangedWithNegativeOne(t *testing.T) {
	clip := constClip(0.5, 100)
	p := NewProject(48000).AddTrack(NewTrack("a"))
	p = p.AddRegion(0, Region{Clip: clip, SourceIn: 0, SourceOut: 100, TimelinePos: 0, Gain: 1, FadeInFrames: 10, FadeOutFrames: 20})
	// -1 means "leave that fade alone".
	out := p.SetRegionFades(0, 0, 5, -1)
	r := out.Tracks[0].Regions[0]
	if r.FadeInFrames != 5 {
		t.Errorf("FadeInFrames = %d, want 5", r.FadeInFrames)
	}
	if r.FadeOutFrames != 20 {
		t.Errorf("FadeOutFrames = %d, want 20 (unchanged)", r.FadeOutFrames)
	}
}

// ---------------------------------------------------------------------------
// gainAt: the per-frame gain + fade math (the heart of the envelope)
// ---------------------------------------------------------------------------

func TestGainAtFadeMath(t *testing.T) {
	clip := constClip(1, 100)
	// Region length 100, gain 1, fade-in 10, fade-out 10.
	r := Region{Clip: clip, SourceIn: 0, SourceOut: 100, Gain: 1, FadeInFrames: 10, FadeOutFrames: 10}.Normalize()

	if g := r.gainAt(0); !approx(g, 0, 1e-9) {
		t.Errorf("gainAt(0) = %v, want 0 (start of fade-in)", g)
	}
	if g := r.gainAt(5); !approx(g, 0.5, 1e-9) {
		t.Errorf("gainAt(5) = %v, want 0.5 (mid fade-in)", g)
	}
	if g := r.gainAt(50); !approx(g, 1, 1e-9) {
		t.Errorf("gainAt(50) = %v, want 1 (full gain in the middle)", g)
	}
	if g := r.gainAt(99); !approx(g, 0, 1e-9) {
		t.Errorf("gainAt(99) = %v, want 0 (last frame of fade-out)", g)
	}
	// Region with constant gain 0.5 and no fades is flat at 0.5.
	flat := Region{Clip: clip, SourceIn: 0, SourceOut: 10, Gain: 0.5}.Normalize()
	for i := 0; i < 10; i++ {
		if g := flat.gainAt(i); !approx(g, 0.5, 1e-9) {
			t.Errorf("flat gainAt(%d) = %v, want 0.5", i, g)
		}
	}
}

// ---------------------------------------------------------------------------
// panGains: constant-power pan law
// ---------------------------------------------------------------------------

func TestPanGains(t *testing.T) {
	tests := []struct {
		name         string
		pan          float64
		wantL, wantR float64
	}{
		{"center -3dB each", 0, math.Sqrt2 / 2, math.Sqrt2 / 2},
		{"hard left", -1, 1, 0},
		{"hard right", 1, 0, 1},
		{"over-left clamps", -5, 1, 0},
		{"over-right clamps", 5, 0, 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			l, r := panGains(tc.pan)
			if !approx(l, tc.wantL, 1e-9) || !approx(r, tc.wantR, 1e-9) {
				t.Errorf("panGains(%v) = (%v,%v), want (%v,%v)", tc.pan, l, r, tc.wantL, tc.wantR)
			}
			// Constant-power invariant: L^2 + R^2 == 1.
			if !approx(l*l+r*r, 1, 1e-9) {
				t.Errorf("panGains(%v): L^2+R^2 = %v, want 1 (constant power)", tc.pan, l*l+r*r)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Mixdown: deterministic, gain/pan/fade applied, solo/mute honored
// ---------------------------------------------------------------------------

func TestMixdownEmptyProject(t *testing.T) {
	if got := NewProject(48000).Mixdown(); got != nil {
		t.Errorf("empty project Mixdown() = %v, want nil", got)
	}
}

func TestMixdownGainAndPanArithmetic(t *testing.T) {
	// A flat DC clip of 0.5, full unity track, center pan: each output channel should
	// be 0.5 * (sqrt2/2) on every frame.
	clip := constClip(0.5, 10)
	p := NewProject(48000).AddTrack(NewTrack("a"))
	p = p.AddRegion(0, Region{Clip: clip, SourceIn: 0, SourceOut: 10, TimelinePos: 0, Gain: 1})
	out := p.Mixdown()
	if len(out) != 10*2 {
		t.Fatalf("mixdown length = %d, want 20 (stereo)", len(out))
	}
	wantCh := 0.5 * (math.Sqrt2 / 2)
	for i := 0; i < 10; i++ {
		if !approx(float64(out[i*2]), wantCh, 1e-6) || !approx(float64(out[i*2+1]), wantCh, 1e-6) {
			t.Fatalf("frame %d = (%v,%v), want (%v,%v)", i, out[i*2], out[i*2+1], wantCh, wantCh)
		}
	}

	// Hard-left pan: L = full level, R = 0.
	pl := p.SetTrackPan(0, -1)
	outL := pl.Mixdown()
	for i := 0; i < 10; i++ {
		if !approx(float64(outL[i*2]), 0.5, 1e-6) {
			t.Errorf("hard-left frame %d L = %v, want 0.5", i, outL[i*2])
		}
		if !approx(float64(outL[i*2+1]), 0, 1e-6) {
			t.Errorf("hard-left frame %d R = %v, want 0", i, outL[i*2+1])
		}
	}

	// Track volume halves the level.
	pv := p.SetTrackVolume(0, 0.5)
	outV := pv.Mixdown()
	wantHalf := 0.5 * 0.5 * (math.Sqrt2 / 2)
	if !approx(float64(outV[0]), wantHalf, 1e-6) {
		t.Errorf("half-volume frame 0 L = %v, want %v", outV[0], wantHalf)
	}
}

func TestMixdownStereoSourceCollapsedToMono(t *testing.T) {
	// Stereo source L=0.4 R=0.8 -> mono 0.6; center pan -> each out channel 0.6*sqrt2/2.
	clip := stereoConstClip(0.4, 0.8, 8)
	p := NewProject(48000).AddTrack(NewTrack("a"))
	p = p.AddRegion(0, Region{Clip: clip, SourceIn: 0, SourceOut: 8, TimelinePos: 0, Gain: 1})
	out := p.Mixdown()
	wantCh := 0.6 * (math.Sqrt2 / 2)
	if !approx(float64(out[0]), wantCh, 1e-6) {
		t.Errorf("stereo->mono frame 0 = %v, want %v", out[0], wantCh)
	}
}

func TestMixdownMuteAndSolo(t *testing.T) {
	clip := constClip(0.5, 10)
	base := NewProject(48000).AddTrack(NewTrack("a")).AddTrack(NewTrack("b"))
	base = base.AddRegion(0, Region{Clip: clip, SourceIn: 0, SourceOut: 10, TimelinePos: 0, Gain: 1})
	base = base.AddRegion(1, Region{Clip: clip, SourceIn: 0, SourceOut: 10, TimelinePos: 0, Gain: 1})

	// Both tracks audible: sum of two equal contributions.
	both := base.Mixdown()
	one := 0.5 * (math.Sqrt2 / 2)
	if !approx(float64(both[0]), 2*one, 1e-6) {
		t.Errorf("both tracks frame 0 = %v, want %v", both[0], 2*one)
	}

	// Mute track b -> only one contribution.
	muted := base.SetTrackMute(1, true).Mixdown()
	if !approx(float64(muted[0]), one, 1e-6) {
		t.Errorf("muted-b frame 0 = %v, want %v (one track)", muted[0], one)
	}

	// Solo track a -> only a renders even though b is not muted.
	soloed := base.SetTrackSolo(0, true).Mixdown()
	if !approx(float64(soloed[0]), one, 1e-6) {
		t.Errorf("solo-a frame 0 = %v, want %v (one track)", soloed[0], one)
	}
}

func TestMixdownDeterministic(t *testing.T) {
	clip := ToneClip(440, 0.7, 2000, 48000)
	build := func() Project {
		p := NewProject(48000).AddTrack(NewTrack("a")).AddTrack(NewTrack("b"))
		p = p.AddRegion(0, Region{Clip: clip, SourceIn: 0, SourceOut: 2000, TimelinePos: 0, Gain: 0.9, FadeInFrames: 100, FadeOutFrames: 100})
		p = p.AddRegion(1, Region{Clip: clip, SourceIn: 0, SourceOut: 2000, TimelinePos: 500, Gain: 0.5})
		return p.SetTrackPan(0, -0.5).SetTrackPan(1, 0.5)
	}
	a := build().Mixdown()
	b := build().Mixdown()
	if len(a) != len(b) {
		t.Fatalf("lengths differ: %d vs %d", len(a), len(b))
	}
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("mixdown not deterministic at sample %d: %v vs %v", i, a[i], b[i])
		}
	}
}

func TestMixdownIsNonSilentForToneProject(t *testing.T) {
	clip := ToneClip(440, 0.8, 4800, 48000)
	p := NewProject(48000).AddTrack(NewTrack("a"))
	p = p.AddRegion(0, Region{Clip: clip, SourceIn: 0, SourceOut: 4800, TimelinePos: 0, Gain: 1})
	peak := PeakAbs(p.Mixdown())
	if peak < 0.1 {
		t.Errorf("tone mixdown peak = %v, want a clearly non-silent signal (>0.1)", peak)
	}
}

func TestHardClipAndPeakAbs(t *testing.T) {
	buf := []float32{0, 1.5, -2, 0.25, -0.5}
	clipped := HardClip(buf)
	want := []float32{0, 1, -1, 0.25, -0.5}
	for i := range want {
		if clipped[i] != want[i] {
			t.Errorf("HardClip[%d] = %v, want %v", i, clipped[i], want[i])
		}
	}
	// HardClip must not mutate the input.
	if buf[1] != 1.5 {
		t.Errorf("HardClip mutated its input: buf[1] = %v", buf[1])
	}
	if got := PeakAbs(buf); !approx(float64(got), 2, 1e-9) {
		t.Errorf("PeakAbs = %v, want 2", got)
	}
	if got := PeakAbs(nil); got != 0 {
		t.Errorf("PeakAbs(nil) = %v, want 0", got)
	}
}

// ---------------------------------------------------------------------------
// WAV write + round-trip via ImportWAV (proves the bounce is a real, readable file)
// ---------------------------------------------------------------------------

func TestMixdownWAVRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bounce.wav")

	clip := ToneClip(440, 0.8, 4800, 48000) // 0.1s of a loud A4
	p := NewProject(48000).AddTrack(NewTrack("a"))
	p = p.AddRegion(0, Region{Clip: clip, SourceIn: 0, SourceOut: 4800, TimelinePos: 0, Gain: 1})

	if err := p.MixdownWAV(path); err != nil {
		t.Fatalf("MixdownWAV: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat written wav: %v", err)
	}
	// Header (44) + 4800 frames * 2 ch * 2 bytes = 44 + 19200.
	if info.Size() != 44+4800*2*2 {
		t.Errorf("wav size = %d, want %d", info.Size(), 44+4800*2*2)
	}

	// Re-import and confirm it decoded as non-silent stereo at 48k.
	got, err := ImportWAV(path)
	if err != nil {
		t.Fatalf("ImportWAV: %v", err)
	}
	if got.Channels != 2 || got.SampleRate != 48000 {
		t.Errorf("re-imported clip = %d ch @ %d Hz, want 2 ch @ 48000", got.Channels, got.SampleRate)
	}
	if got.Frames() != 4800 {
		t.Errorf("re-imported frames = %d, want 4800", got.Frames())
	}
	if peak := PeakAbs(got.Samples); peak < 0.1 {
		t.Errorf("re-imported peak = %v, want a non-silent signal", peak)
	}
}

func TestEmptyProjectWritesValidSilentWAV(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "silent.wav")
	if err := NewProject(48000).MixdownWAV(path); err != nil {
		t.Fatalf("empty MixdownWAV should still write a valid file: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Size() != 44 {
		t.Errorf("silent wav size = %d, want 44 (header only)", info.Size())
	}
}

// ---------------------------------------------------------------------------
// Peaks: bucketing, region windows, shaped (post-fader) overview
// ---------------------------------------------------------------------------

func TestBuildClipPeaks(t *testing.T) {
	// A 100-frame tone, 10 columns -> 10 frames per bucket.
	clip := ToneClip(1000, 0.9, 100, 48000)
	pk := BuildClipPeaks(clip, 10)
	if pk.Width != 10 || len(pk.Columns) != 10 {
		t.Fatalf("Peaks width = %d (%d cols), want 10", pk.Width, len(pk.Columns))
	}
	if !approx(pk.FramesPerBucket, 10, 1e-9) {
		t.Errorf("FramesPerBucket = %v, want 10", pk.FramesPerBucket)
	}
	// Every column of a real tone should have a non-zero span (max > min).
	for i, c := range pk.Columns {
		if c.Max <= c.Min {
			t.Errorf("column %d has no span: min=%v max=%v", i, c.Min, c.Max)
		}
		if c.Abs < 0 {
			t.Errorf("column %d Abs negative: %v", i, c.Abs)
		}
	}
}

func TestPeaksDegradeCases(t *testing.T) {
	clip := constClip(0.5, 100)
	if pk := BuildClipPeaks(nil, 10); pk.Width != 0 {
		t.Errorf("nil clip should give empty Peaks, got width %d", pk.Width)
	}
	if pk := BuildClipPeaks(clip, 0); pk.Width != 0 {
		t.Errorf("zero width should give empty Peaks, got width %d", pk.Width)
	}
	if pk := BuildClipPeaks(clip, -5); pk.Width != 0 {
		t.Errorf("negative width should give empty Peaks, got width %d", pk.Width)
	}
	// Region with nil clip.
	if pk := BuildRegionPeaks(Region{}, 10); pk.Width != 0 {
		t.Errorf("nil-clip region should give empty Peaks, got width %d", pk.Width)
	}
}

func TestPeaksWidthExceedsFrames(t *testing.T) {
	// 5 frames, 20 columns: no column may be left uninitialized (each reads >= 1 frame).
	clip := constClip(0.5, 5)
	pk := BuildClipPeaks(clip, 20)
	if pk.Width != 20 {
		t.Fatalf("width = %d, want 20", pk.Width)
	}
	for i, c := range pk.Columns {
		// A constant 0.5 clip: every populated column has min==max==0.5.
		if !approx(float64(c.Max), 0.5, 1e-6) {
			t.Errorf("column %d max = %v, want 0.5 (no empty/uninitialized column)", i, c.Max)
		}
	}
}

func TestBuildRegionPeaksWindow(t *testing.T) {
	clip := ToneClip(1000, 0.9, 200, 48000)
	// A region that plays only [50,150) of the clip.
	r := Region{Clip: clip, SourceIn: 50, SourceOut: 150, Gain: 1}
	pk := BuildRegionPeaks(r, 10)
	if pk.Width != 10 {
		t.Fatalf("region peaks width = %d, want 10", pk.Width)
	}
	if !approx(pk.FramesPerBucket, 10, 1e-9) { // 100 frames / 10 cols
		t.Errorf("FramesPerBucket = %v, want 10", pk.FramesPerBucket)
	}
}

func TestBuildRegionPeaksShapedAppliesFade(t *testing.T) {
	// A flat DC clip of 1.0 with a long fade-in: the shaped overview's first column
	// should be quieter than a later (full-gain) column.
	clip := constClip(1, 100)
	r := Region{Clip: clip, SourceIn: 0, SourceOut: 100, Gain: 1, FadeInFrames: 50}
	shaped := BuildRegionPeaksShaped(r, 10)
	plain := BuildRegionPeaks(r, 10)
	if shaped.Width != 10 || plain.Width != 10 {
		t.Fatalf("widths: shaped=%d plain=%d", shaped.Width, plain.Width)
	}
	// First column of the shaped overview is in the fade-in -> smaller magnitude than
	// the plain (un-shaped) overview's first column.
	if shaped.Columns[0].Abs >= plain.Columns[0].Abs {
		t.Errorf("shaped fade-in column0 Abs=%v should be < plain Abs=%v", shaped.Columns[0].Abs, plain.Columns[0].Abs)
	}
	// A late column (past the 50-frame fade) should be at full level ~1.0.
	if !approx(float64(shaped.Columns[9].Abs), 1, 1e-3) {
		t.Errorf("shaped late column Abs = %v, want ~1.0 (past fade-in)", shaped.Columns[9].Abs)
	}
}

func TestFrameAtColumn(t *testing.T) {
	clip := constClip(0.5, 100)
	pk := BuildClipPeaks(clip, 10) // 10 frames per bucket
	if got := pk.FrameAtColumn(0); got != 0 {
		t.Errorf("FrameAtColumn(0) = %d, want 0", got)
	}
	if got := pk.FrameAtColumn(5); got != 50 {
		t.Errorf("FrameAtColumn(5) = %d, want 50", got)
	}
	// Clamps out-of-range indices.
	if got := pk.FrameAtColumn(999); got != 90 {
		t.Errorf("FrameAtColumn(999) = %d, want 90 (clamped to last column)", got)
	}
	if got := pk.FrameAtColumn(-3); got != 0 {
		t.Errorf("FrameAtColumn(-3) = %d, want 0 (clamped)", got)
	}
	if got := (Peaks{}).FrameAtColumn(2); got != 0 {
		t.Errorf("empty Peaks FrameAtColumn = %d, want 0", got)
	}
}

// ---------------------------------------------------------------------------
// SortedRegions
// ---------------------------------------------------------------------------

func TestSortedRegions(t *testing.T) {
	tr := Track{Regions: []Region{
		{ID: "c", TimelinePos: 300},
		{ID: "a", TimelinePos: 100},
		{ID: "b2", TimelinePos: 100},
		{ID: "b1", TimelinePos: 100},
	}}
	sorted := tr.SortedRegions()
	wantIDs := []string{"a", "b1", "b2", "c"} // by pos, then ID
	for i, w := range wantIDs {
		if sorted[i].ID != w {
			t.Errorf("sorted[%d] = %q, want %q", i, sorted[i].ID, w)
		}
	}
	// Original slice is untouched (immutability).
	if tr.Regions[0].ID != "c" {
		t.Errorf("SortedRegions mutated the original slice: [0]=%q", tr.Regions[0].ID)
	}
}

// ---------------------------------------------------------------------------
// Recording stub honesty (default !audio build)
// ---------------------------------------------------------------------------

func TestRecordToWAVStubIsHonest(t *testing.T) {
	// In the default (non-audio) build this must return the typed unavailable error and
	// must NOT create a file (never fabricate captured audio).
	dir := t.TempDir()
	path := filepath.Join(dir, "should-not-exist.wav")
	clip, err := RecordToWAV(path, 1.0, 48000, 1)
	if err == nil {
		t.Skip("audio build present: real capture path is hardware-only, not unit-tested here")
	}
	if clip != nil {
		t.Errorf("stub returned a non-nil clip: %+v", clip)
	}
	if _, statErr := os.Stat(path); statErr == nil {
		t.Errorf("stub fabricated a file at %q — it must never write audio", path)
	}
}
