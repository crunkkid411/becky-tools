package audiotrack

// engine.go adds the two flat, GUI-/test-friendly engine entry points the Becky
// Canvas audio panel binds to directly, alongside the richer Project/Track model in
// the rest of the package:
//
//   - BuildPeaks(samples, buckets) []Peak — the bare min/max downsampler the window
//     calls to draw ONE waveform from a raw sample slice, with no Clip/Region wrapper.
//   - Mixdown(track, sampleRate, src) ([]float32, error) — a single-track bounce that
//     sums a Track's regions onto a timeline buffer honoring placement + gain, where
//     the region SOURCE samples come from an injectable SampleSource. This lets the
//     GUI mix straight from in-memory Clips OR from on-disk WAVs (decoded lazily via
//     sampledecode) WITHOUT this layer needing real files in a unit test.
//
// Both are pure Go, deterministic, and degrade-never-crash: bad buckets / empty
// samples yield an empty result; a region whose source is missing or out of range is
// SKIPPED and surfaced as a wrapped error while the rest of the mix still renders
// (partial result + error), never a panic.
//
// NAMING NOTE: the task names the peak helper `Peaks(samples, buckets) []Peak`, but
// `Peaks` is already the richer overview STRUCT this package exports (used by
// cmd/becky-audiotrack and BuildClipPeaks/BuildRegionPeaks). A package-scope function
// and type cannot share an identifier in Go, so the flat helper is exported as
// BuildPeaks — same contract, non-colliding name — and the struct keeps its name.

import (
	"errors"
	"fmt"
)

// BuildPeaks downsamples a raw mono sample slice into `buckets` min/max columns — the
// exact overview the GUI draws as a waveform at a given pixel width. Unlike
// BuildClipPeaks/BuildRegionPeaks it takes a plain []float32 (already mono; the caller
// collapses channels if needed) and returns a bare []Peak, so a panel can draw any
// buffer it has in hand (a recorded take, a region preview, a bus sum) without a Clip.
//
// Determinism + degrade-never-crash:
//   - buckets <= 0 or len(samples) == 0 -> empty (nil) slice.
//   - buckets > len(samples) -> each populated column reads >= 1 sample; no column is
//     left zero/uninitialized (the GUI never sees a gap).
//   - the float stride spreads the remainder evenly; the last column absorbs rounding.
func BuildPeaks(samples []float32, buckets int) []Peak {
	if buckets <= 0 || len(samples) == 0 {
		return nil
	}
	n := len(samples)
	out := make([]Peak, buckets)
	stride := float64(n) / float64(buckets)
	for col := 0; col < buckets; col++ {
		start := int(float64(col) * stride)
		end := int(float64(col+1) * stride)
		if col == buckets-1 {
			end = n // last column absorbs any rounding remainder
		}
		if end <= start {
			end = start + 1 // buckets > n: ensure each column reads >= 1 sample
		}
		if end > n {
			end = n
		}
		mn, mx := samples[start], samples[start]
		for i := start + 1; i < end; i++ {
			v := samples[i]
			if v < mn {
				mn = v
			}
			if v > mx {
				mx = v
			}
		}
		a := mx
		if -mn > a {
			a = -mn
		}
		out[col] = Peak{Min: mn, Max: mx, Abs: a}
	}
	return out
}

// SampleSource resolves the decoded source samples a Region plays from. It is the seam
// that lets Mixdown bounce a Track WITHOUT this package owning file IO: a unit test
// hands back in-memory buffers; the GUI hands back decoded Clips or lazily-decoded WAVs.
//
// SamplesFor returns the interleaved float32 PCM for the region's source plus its
// channel count. A nil/empty return (any error) means "no audio for this region": the
// region is skipped and the miss is reported, the rest of the mix still renders.
type SampleSource interface {
	// SamplesFor returns the source buffer + channel count backing region r. The slice
	// is treated as read-only by the mixer. err != nil (or channels <= 0) skips r.
	SamplesFor(r Region) (samples []float32, channels int, err error)
}

// ClipSource is the default SampleSource: it reads each Region's already-decoded
// in-memory Clip (the common path — the GUI imports WAVs into Clips up front). A region
// with a nil Clip is reported as missing (skipped), never a panic.
type ClipSource struct{}

// SamplesFor returns the region's Clip samples + channel count, or an error if the
// region has no clip to play from.
func (ClipSource) SamplesFor(r Region) ([]float32, int, error) {
	if r.Clip == nil {
		return nil, 0, fmt.Errorf("region %q: nil clip", r.ID)
	}
	if r.Clip.Channels <= 0 {
		return nil, 0, fmt.Errorf("region %q: clip has zero channels", r.ID)
	}
	return r.Clip.Samples, r.Clip.Channels, nil
}

// FileSource is a SampleSource that decodes a Region's backing WAV from disk on demand
// (via ImportWAV/sampledecode), caching by path so a file is decoded once even when
// several regions share it. It is the on-disk counterpart of ClipSource for a Track
// whose regions were placed by path without preloading a Clip. A region with a nil Clip
// AND no resolvable path is reported as missing (skipped).
//
// Construct with NewFileSource; the zero value is usable but won't cache.
type FileSource struct {
	cache map[string]*Clip
}

// NewFileSource returns a FileSource with an initialized decode cache.
func NewFileSource() *FileSource { return &FileSource{cache: map[string]*Clip{}} }

// SamplesFor returns the region's source samples, preferring an already-decoded
// in-memory Clip and otherwise decoding the region's Clip.Path WAV from disk (cached).
func (f *FileSource) SamplesFor(r Region) ([]float32, int, error) {
	// Already-decoded clip with real samples: use it directly.
	if r.Clip != nil && r.Clip.Channels > 0 && len(r.Clip.Samples) > 0 {
		return r.Clip.Samples, r.Clip.Channels, nil
	}
	path := ""
	if r.Clip != nil {
		path = r.Clip.Path
	}
	if path == "" {
		return nil, 0, fmt.Errorf("region %q: no in-memory samples and no source path", r.ID)
	}
	if f.cache != nil {
		if c, ok := f.cache[path]; ok {
			return c.Samples, c.Channels, nil
		}
	}
	c, err := ImportWAV(path)
	if err != nil {
		return nil, 0, fmt.Errorf("region %q: %w", r.ID, err)
	}
	if f.cache != nil {
		f.cache[path] = c
	}
	return c.Samples, c.Channels, nil
}

// Mixdown renders a SINGLE track to interleaved STEREO float32 (L,R,L,R,...) at
// sampleRate, summing the track's regions onto a timeline buffer with sample-accurate
// region boundaries and honoring each region's gain + fade envelope and the track's
// volume + pan (a muted track renders silence; solo is a project-level concept and is
// ignored here — this is the per-track bounce). Source samples come from src; if src is
// nil, ClipSource is used (read each region's in-memory Clip).
//
// This is the flat engine entry the GUI calls to audition or bounce one lane. The whole
// Project bounce stays Project.Mixdown().
//
// Degrade-never-crash: a region whose source is missing/out-of-range is SKIPPED and the
// reason is collected; Mixdown still returns the partial mix of the renderable regions
// PLUS a wrapped error naming the skipped regions (callers may ignore the error and use
// the partial buffer). sampleRate <= 0 falls back to DefaultSampleRate. A track with no
// renderable content returns (nil, nil) for an empty track / (nil, err) if every region
// was skipped.
func Mixdown(track Track, sampleRate int, src SampleSource) ([]float32, error) {
	if sampleRate <= 0 {
		sampleRate = DefaultSampleRate
	}
	if src == nil {
		src = ClipSource{}
	}

	// Project length for this single track = furthest region end.
	frames := 0
	for _, r := range track.Regions {
		if e := r.TimelineEnd(); e > frames {
			frames = e
		}
	}
	if frames <= 0 {
		return nil, nil // empty track -> silence, no error
	}

	out := make([]float32, frames*2) // stereo

	// Track strip: a muted track is silent; volume clamped >= 0; constant-power pan.
	lGain, rGain := panGains(track.Pan)
	vol := track.Volume
	if vol < 0 {
		vol = 0
	}
	if track.Mute {
		vol = 0
	}
	lGain *= vol
	rGain *= vol

	var skipped []string
	rendered := 0
	for _, r := range track.Regions {
		samples, ch, err := src.SamplesFor(r)
		if err != nil || ch <= 0 || len(samples) == 0 {
			skipped = append(skipped, regionSkipLabel(r, err))
			continue
		}
		// Normalize the source window against the RESOLVED buffer (its real frame count),
		// not the region's placeholder Clip: with FileSource the decoded length is only
		// known here, so a path-only region would otherwise collapse to zero length.
		mixRegionFrom(out, frames, normalizeAgainst(r, len(samples)/ch), samples, ch, float32(lGain), float32(rGain))
		rendered++
	}

	if len(skipped) > 0 {
		// If EVERY region was skipped, wrap the sentinel so callers can distinguish a
		// track of only-broken regions from an empty track (which returns nil error).
		if rendered == 0 {
			err := fmt.Errorf("%w: skipped %d region(s): %v", ErrNoRenderableRegions, len(skipped), skipped)
			return out, err
		}
		// Otherwise it's a partial mix: return the renderable regions PLUS a wrapped
		// error naming the skipped ones (callers may ignore the error and use the buffer).
		return out, fmt.Errorf("audiotrack: mixdown skipped %d region(s): %v", len(skipped), skipped)
	}
	return out, nil
}

// regionSkipLabel makes a stable, human-readable label for a skipped region (its ID, or
// a placeholder when the ID is empty), optionally with the underlying reason.
func regionSkipLabel(r Region, err error) string {
	id := r.ID
	if id == "" {
		id = "<unnamed>"
	}
	if err != nil {
		return id + " (" + err.Error() + ")"
	}
	return id
}

// normalizeAgainst returns a clamped copy of r whose source window is bounded by
// srcFrames (the resolved buffer's real frame count) rather than r.Clip — so a region
// that referenced its source by path (placeholder Clip with no decoded samples) is
// clamped to the file's actual length once decoded. Gain/pos/fade clamping matches
// Region.Normalize; only the source-bound reference differs. Never panics.
func normalizeAgainst(r Region, srcFrames int) Region {
	out := r
	if out.Gain < 0 {
		out.Gain = 0
	}
	if out.TimelinePos < 0 {
		out.TimelinePos = 0
	}
	if srcFrames < 0 {
		srcFrames = 0
	}
	if out.SourceIn < 0 {
		out.SourceIn = 0
	}
	if out.SourceOut < out.SourceIn {
		out.SourceOut = out.SourceIn
	}
	if out.SourceIn > srcFrames {
		out.SourceIn = srcFrames
	}
	if out.SourceOut > srcFrames {
		out.SourceOut = srcFrames
	}
	if out.FadeInFrames < 0 {
		out.FadeInFrames = 0
	}
	if out.FadeOutFrames < 0 {
		out.FadeOutFrames = 0
	}
	if l := out.LenFrames(); out.FadeInFrames+out.FadeOutFrames > l {
		if out.FadeInFrames > l {
			out.FadeInFrames = l
		}
		out.FadeOutFrames = l - out.FadeInFrames
	}
	return out
}

// mixRegionFrom adds one region's contribution into the stereo bus `out` using an
// EXTERNALLY-supplied source buffer (so the source can come from a Clip or a freshly
// decoded file). It mirrors the in-package mixRegion exactly — mono-collapse the source
// frame, apply the region gain+fade envelope, then split to L/R by the pre-combined
// track volume*pan gains — but reads from `samples` instead of r.Clip. Sample-accurate:
// region material outside the project window is clipped, not wrapped.
func mixRegionFrom(out []float32, frames int, r Region, samples []float32, ch int, lGain, rGain float32) {
	if ch <= 0 {
		return
	}
	n := r.LenFrames()
	for i := 0; i < n; i++ {
		tlFrame := r.TimelinePos + i
		if tlFrame < 0 || tlFrame >= frames {
			continue // clipped to the project window
		}
		srcFrame := r.SourceIn + i
		var sum float32
		base := srcFrame * ch
		for c := 0; c < ch; c++ {
			idx := base + c
			if idx >= 0 && idx < len(samples) {
				sum += samples[idx]
			}
		}
		mono := (sum / float32(ch)) * float32(r.gainAt(i))
		out[tlFrame*2] += mono * lGain
		out[tlFrame*2+1] += mono * rGain
	}
}

// ErrNoRenderableRegions is returned (wrapped) when a Mixdown call has regions but every
// one was skipped (no source resolved). Callers that want to distinguish "track was
// empty" (nil error) from "track had only broken regions" can errors.Is against it.
var ErrNoRenderableRegions = errors.New("audiotrack: no renderable regions")
