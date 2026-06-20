// Package audiotrack is the vocal/audio-track side of Becky Canvas (Jordan's
// Cubase-replacement DAW). It is the deterministic, pure-Go logic layer behind a
// multitrack audio timeline: the data model (Track / Region), immutable edits
// (add / move / trim / split / gain / fade), waveform PEAK generation (the min/max
// buckets the UI actually draws), WAV IMPORT (via internal/sampledecode), and an
// OFFLINE MIXDOWN that renders tracks+regions — with per-region gain & fades and
// per-track volume/pan/mute/solo — down to a single WAV.
//
// It is intentionally GUI-agnostic: no Gio, no cmd/canvas. The window draws Peaks
// and applies edits; this package owns the model and the math.
//
// Design rules (CLAUDE.md): pure Go, stdlib + internal/sampledecode only, offline,
// deterministic (same model in -> same mixdown bytes out), and degrade-never-crash
// (out-of-range values are clamped; a missing/short WAV is a wrapped error, never a
// panic). All edits are IMMUTABLE — every mutating method returns a NEW value and
// leaves the receiver untouched (CLAUDE.md coding-style: never mutate in place), so
// the GUI can keep an undo stack of cheap snapshots.
//
// Sample convention: audio is held as interleaved float32 in [-1, 1] at a project
// sample rate, mirroring internal/sampledecode.Audio. Timeline positions and region
// edits are expressed in SAMPLE FRAMES (not seconds) so all arithmetic is exact and
// reproducible; helpers convert to/from seconds at the boundary.
package audiotrack

import (
	"fmt"

	"becky-go/internal/sampledecode"
)

// DefaultSampleRate is the project rate used when none is supplied (matches the
// audio engine's 48 kHz default).
const DefaultSampleRate = 48000

// Clip is a decoded source audio buffer that one or more Regions reference. It is
// the in-memory backing for an imported WAV. Samples is interleaved float32 in
// [-1, 1] (frame0ch0, frame0ch1, frame1ch0, ...), exactly as internal/sampledecode
// returns, so import is a zero-copy adoption of that decoder's output.
type Clip struct {
	// Path is the source file the clip was imported from (for display/relink). It is
	// metadata only — the decoded Samples are authoritative.
	Path string
	// Channels is the source channel count (1 = mono, 2 = stereo).
	Channels int
	// SampleRate is the source sample rate in Hz.
	SampleRate int
	// Samples is the interleaved float32 PCM in [-1, 1].
	Samples []float32
}

// Frames returns the number of sample frames in the clip (Samples/Channels).
func (c *Clip) Frames() int {
	if c == nil || c.Channels <= 0 {
		return 0
	}
	return len(c.Samples) / c.Channels
}

// DurationSec is the clip length in seconds (0 if no/invalid rate).
func (c *Clip) DurationSec() float64 {
	if c == nil || c.SampleRate <= 0 {
		return 0
	}
	return float64(c.Frames()) / float64(c.SampleRate)
}

// Region is a placed slice of a Clip on a track's timeline. SourceIn/SourceOut bound
// the portion of the clip that plays (in source frames); TimelinePos is where frame
// SourceIn lands on the track (in timeline frames). Gain is a linear multiplier;
// FadeInFrames / FadeOutFrames apply a linear amplitude ramp at the head/tail of the
// region (linear, for exact determinism). All edits return a new Region (immutable).
//
// Invariant after Normalize: 0 <= SourceIn <= SourceOut <= clip length; the
// fades never overlap (their sum is clamped to the region length); TimelinePos >= 0.
type Region struct {
	// ID is a stable identifier the GUI assigns so it can address a region across
	// edits (edits preserve the ID). Empty IDs are allowed; the package never
	// generates one.
	ID string
	// Clip is the source buffer this region plays from. A nil Clip is a silent
	// region (renders nothing) — degrade-never-crash, not an error.
	Clip *Clip
	// SourceIn is the first source frame that plays (inclusive).
	SourceIn int
	// SourceOut is one past the last source frame that plays (exclusive), so the
	// region length in frames is SourceOut-SourceIn.
	SourceOut int
	// TimelinePos is the timeline frame where SourceIn is placed.
	TimelinePos int
	// Gain is a linear amplitude multiplier (1 = unity). Clamped to >= 0.
	Gain float64
	// FadeInFrames ramps amplitude 0->1 over the first N frames of the region.
	FadeInFrames int
	// FadeOutFrames ramps amplitude 1->0 over the last N frames of the region.
	FadeOutFrames int
}

// LenFrames is the region's length on the timeline in frames (SourceOut-SourceIn,
// never negative).
func (r Region) LenFrames() int {
	n := r.SourceOut - r.SourceIn
	if n < 0 {
		return 0
	}
	return n
}

// TimelineEnd is one past the region's last timeline frame (TimelinePos+LenFrames).
func (r Region) TimelineEnd() int { return r.TimelinePos + r.LenFrames() }

// Normalize returns a clamped, internally-consistent copy of the region. It never
// errors: out-of-range source bounds are clamped to the clip, a negative gain
// becomes 0, and overlapping fades are shrunk to fit. This is the degrade-never-crash
// guard every edit funnels through so the model can't reach an invalid state.
func (r Region) Normalize() Region {
	out := r
	if out.Gain < 0 {
		out.Gain = 0
	}
	if out.TimelinePos < 0 {
		out.TimelinePos = 0
	}
	// Clamp the source window to [0, clipFrames].
	clipFrames := out.Clip.Frames()
	if out.SourceIn < 0 {
		out.SourceIn = 0
	}
	if out.SourceOut < out.SourceIn {
		out.SourceOut = out.SourceIn
	}
	if clipFrames > 0 {
		if out.SourceIn > clipFrames {
			out.SourceIn = clipFrames
		}
		if out.SourceOut > clipFrames {
			out.SourceOut = clipFrames
		}
	}
	// Fades are non-negative and cannot jointly exceed the region length.
	if out.FadeInFrames < 0 {
		out.FadeInFrames = 0
	}
	if out.FadeOutFrames < 0 {
		out.FadeOutFrames = 0
	}
	if l := out.LenFrames(); out.FadeInFrames+out.FadeOutFrames > l {
		// Shrink proportionally toward the available length, fade-in first.
		if out.FadeInFrames > l {
			out.FadeInFrames = l
		}
		out.FadeOutFrames = l - out.FadeInFrames
	}
	return out
}

// gainAt returns the region's amplitude multiplier at local frame i (0-based into the
// region, 0 <= i < LenFrames), combining the constant Gain with any fade-in/out ramp.
// Linear ramps are used (not equal-power) so the mixdown is bit-for-bit reproducible
// and trivially testable. Out-of-range i clamps to the nearest edge.
func (r Region) gainAt(i int) float64 {
	l := r.LenFrames()
	if l <= 0 {
		return 0
	}
	if i < 0 {
		i = 0
	}
	if i >= l {
		i = l - 1
	}
	g := r.Gain
	if r.FadeInFrames > 0 && i < r.FadeInFrames {
		// 0 at i==0 .. ~1 approaching FadeInFrames. Divide by FadeInFrames (not -1)
		// so a 1-frame fade is a clean 0->silence-to-full edge.
		g *= float64(i) / float64(r.FadeInFrames)
	}
	if r.FadeOutFrames > 0 {
		// Frames from the end: l-1-i ranges 0 (last frame) .. up.
		fromEnd := l - 1 - i
		if fromEnd < r.FadeOutFrames {
			g *= float64(fromEnd) / float64(r.FadeOutFrames)
		}
	}
	return g
}

// Track is an ordered list of Regions plus the channel-strip controls the mixdown
// applies once per track: Volume (linear), Pan (-1 left .. +1 right), Mute, Solo.
// Regions need not be sorted by the caller; the mixdown reads TimelinePos directly,
// and SortedRegions is offered for the UI.
type Track struct {
	// Name is a human label ("Lead Vox", "Gtr DI").
	Name string
	// Regions placed on this track's timeline.
	Regions []Region
	// Volume is the track fader as a linear multiplier (1 = unity). Clamped >= 0.
	Volume float64
	// Pan is the stereo position: -1 hard left, 0 center, +1 hard right.
	Pan float64
	// Mute silences the track in the mixdown.
	Mute bool
	// Solo, when set on ANY track in the project, restricts the mixdown to soloed
	// tracks only (standard DAW solo semantics).
	Solo bool
}

// NewTrack returns a track with unity volume, centered pan, and the given name. Use
// this rather than a zero-value Track, whose Volume=0 would render silence.
func NewTrack(name string) Track {
	return Track{Name: name, Volume: 1, Pan: 0}
}

// Project is the whole multitrack timeline at a single sample rate. The mixdown and
// every edit operate on a Project; all edits are immutable (return a new Project).
type Project struct {
	// SampleRate is the project rate every track renders at (Hz).
	SampleRate int
	// Tracks in display/render order.
	Tracks []Track
}

// NewProject returns an empty project at the given sample rate (<=0 -> DefaultSampleRate).
func NewProject(sampleRate int) Project {
	if sampleRate <= 0 {
		sampleRate = DefaultSampleRate
	}
	return Project{SampleRate: sampleRate}
}

// FramesToSec converts a frame count to seconds at the project rate.
func (p Project) FramesToSec(frames int) float64 {
	if p.SampleRate <= 0 {
		return 0
	}
	return float64(frames) / float64(p.SampleRate)
}

// SecToFrames converts seconds to a (rounded) frame count at the project rate.
func (p Project) SecToFrames(sec float64) int {
	if p.SampleRate <= 0 {
		return 0
	}
	return int(sec*float64(p.SampleRate) + 0.5)
}

// LenFrames is the project length in frames: the furthest TimelineEnd across all
// tracks/regions (0 for an empty project).
func (p Project) LenFrames() int {
	max := 0
	for _, t := range p.Tracks {
		for _, r := range t.Regions {
			if e := r.TimelineEnd(); e > max {
				max = e
			}
		}
	}
	return max
}

// DurationSec is the project length in seconds.
func (p Project) DurationSec() float64 { return p.FramesToSec(p.LenFrames()) }

// ImportWAV decodes the WAV at path via internal/sampledecode and returns a Clip
// ready to place on a track. The decoder handles PCM 8/16/24/32-bit, IEEE float32,
// and WAVE_FORMAT_EXTENSIBLE, normalizing to interleaved float32 in [-1, 1] — so the
// 32-bit-float bug in go-audio/wav cannot recur here. A malformed/truncated/
// unsupported file returns a wrapped error (degrade-never-crash), never a panic.
func ImportWAV(path string) (*Clip, error) {
	a, err := sampledecode.DecodeWAVFile(path)
	if err != nil {
		return nil, fmt.Errorf("audiotrack: import %q: %w", path, err)
	}
	if a.Channels <= 0 {
		return nil, fmt.Errorf("audiotrack: import %q: zero channels", path)
	}
	return &Clip{
		Path:       path,
		Channels:   a.Channels,
		SampleRate: a.SampleRate,
		Samples:    a.Samples,
	}, nil
}

// NewRegionFromClip places an entire clip on a track timeline at timelinePos, with
// unity gain and no fades. It is the common "drop this WAV here" constructor. The
// returned region is normalized.
func NewRegionFromClip(id string, c *Clip, timelinePos int) Region {
	return Region{
		ID:          id,
		Clip:        c,
		SourceIn:    0,
		SourceOut:   c.Frames(),
		TimelinePos: timelinePos,
		Gain:        1,
	}.Normalize()
}
