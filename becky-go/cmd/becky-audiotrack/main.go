// becky-audiotrack — the headless VERIFY tool for internal/audiotrack, the
// multitrack audio-timeline engine behind Becky Canvas (Jordan's Cubase-replacement
// DAW). It proves the engine renders REAL sound: it builds a Project with two
// OVERLAPPING regions on a track, runs the deterministic offline mixdown to a 16-bit
// stereo WAV, and ALSO emits a waveform-peaks JSON overview. The mixdown WAV is meant
// to be checked with ffprobe volumedetect (mean_volume must be well above the -80 dB
// silence floor) — that is the corroborating proof the bounce is non-silent.
//
// Two sources for the audio:
//   - default: a synthesized sine TEST TONE (an explicit color-bar-style test signal,
//     NOT fabricated "recorded" audio — see internal/audiotrack/tone.go), so the tool
//     runs with no external file.
//   - --import <wav>: a real WAV imported via internal/sampledecode.
//
// Usage:
//
//	becky-audiotrack render --out mix.wav [--peaks peaks.json] [--import in.wav]
//	                        [--freq 440] [--seconds 1.0] [--rate 48000] [--width 800]
//	becky-audiotrack peaks  --import in.wav [--out peaks.json] [--width 800]
//
// Offline + deterministic: same flags in -> identical WAV bytes out. Degrade-never-
// crash: a bad path / unreadable WAV is a plain-language error and a non-zero exit, not
// a panic.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"becky-go/internal/audiotrack"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "render":
		err = runRender(os.Args[2:])
	case "peaks":
		err = runPeaks(os.Args[2:])
	case "-h", "--help", "help":
		usage()
		return
	default:
		fmt.Fprintf(os.Stderr, "becky-audiotrack: unknown command %q\n\n", os.Args[1])
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "becky-audiotrack:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `becky-audiotrack — verify the audiotrack mixdown engine produces real sound.

  render  build a 2-overlapping-region project and bounce it to a WAV (+ peaks JSON)
  peaks   emit only a waveform-peaks overview for a clip

  render flags:
    --out <wav>      output mixdown WAV (default mix.wav)
    --peaks <json>   also write a waveform-peaks JSON overview
    --import <wav>   import a real WAV as the source clip (default: a synth test tone)
    --freq <hz>      test-tone frequency when not importing (default 440)
    --seconds <s>    source length in seconds when not importing (default 1.0)
    --rate <hz>      project sample rate (default 48000)
    --width <cols>   waveform-peaks column count (default 800)
    --json           print the result summary as JSON

  peaks flags:
    --import <wav>   the clip to overview (required)
    --out <json>     write the peaks JSON here (default: stdout)
    --width <cols>   column count (default 800)

Verify a render is non-silent (mean_volume must be > -80 dB):
  becky-audiotrack render --out mix.wav
  ffprobe -v error -af volumedetect -f null - -i mix.wav 2>&1 | grep mean_volume
`)
}

// renderResult is the machine-readable summary of a render (printed with --json).
type renderResult struct {
	Out         string  `json:"out"`
	PeaksPath   string  `json:"peaks_path,omitempty"`
	Source      string  `json:"source"`
	SampleRate  int     `json:"sample_rate"`
	Channels    int     `json:"channels"`
	DurationSec float64 `json:"duration_sec"`
	Regions     int     `json:"regions"`
	PeakAbs     float64 `json:"peak_abs"`
	NonSilent   bool    `json:"non_silent"`
}

func runRender(args []string) error {
	fs := flag.NewFlagSet("render", flag.ContinueOnError)
	out := fs.String("out", "mix.wav", "output mixdown WAV path")
	peaksPath := fs.String("peaks", "", "also write a waveform-peaks JSON overview here")
	importPath := fs.String("import", "", "import a real WAV as the source clip")
	freq := fs.Float64("freq", 440, "test-tone frequency (Hz) when not importing")
	seconds := fs.Float64("seconds", 1.0, "source length in seconds when not importing")
	rate := fs.Int("rate", audiotrack.DefaultSampleRate, "project sample rate (Hz)")
	width := fs.Int("width", 800, "waveform-peaks column count")
	asJSON := fs.Bool("json", false, "print the result summary as JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *rate <= 0 {
		return fmt.Errorf("--rate must be positive")
	}

	// 1) Get the source clip: a real import, or a synthesized test tone.
	clip, source, err := sourceClip(*importPath, *freq, *seconds, *rate)
	if err != nil {
		return err
	}
	if clip.Frames() <= 0 {
		return fmt.Errorf("source clip has no audio (frames=0)")
	}

	// 2) Build a project with TWO OVERLAPPING regions on one track. The second region
	// starts at half the clip length, so for the back half of region A both regions
	// sound at once — the mixdown must SUM them. Both regions carry head/tail fades and
	// distinct gains, so the bounce exercises gain, fades, and the overlap sum together.
	clipFrames := clip.Frames()
	half := clipFrames / 2
	fade := clipFrames / 10 // 10% head/tail fades

	track := audiotrack.NewTrack("verify")
	p := audiotrack.NewProject(*rate).AddTrack(track)
	p = p.AddRegion(0, audiotrack.Region{
		ID: "A", Clip: clip,
		SourceIn: 0, SourceOut: clipFrames, TimelinePos: 0,
		Gain: 0.9, FadeInFrames: fade, FadeOutFrames: fade,
	})
	p = p.AddRegion(0, audiotrack.Region{
		ID: "B", Clip: clip,
		SourceIn: 0, SourceOut: clipFrames, TimelinePos: half,
		Gain: 0.7, FadeInFrames: fade, FadeOutFrames: fade,
	})

	// 3) Bounce to a 16-bit stereo WAV (the engine hard-clips to [-1,1] internally).
	if err := p.MixdownWAV(*out); err != nil {
		return err
	}

	// 4) Compute the peak of the (clipped) bounce so we can report non-silence locally
	// too (ffprobe is the independent corroboration; this is the engine's own read).
	peak := audiotrack.PeakAbs(audiotrack.HardClip(p.Mixdown()))

	// 5) Optionally emit a waveform-peaks overview of the whole bounce. We re-import the
	// just-written WAV and build the peaks from it, so the overview reflects the actual
	// rendered file, not the in-memory float buffer.
	if *peaksPath != "" {
		bounce, ierr := audiotrack.ImportWAV(*out)
		if ierr != nil {
			return fmt.Errorf("re-import bounce for peaks: %w", ierr)
		}
		if err := writePeaksJSON(*peaksPath, audiotrack.BuildClipPeaks(bounce, *width)); err != nil {
			return err
		}
	}

	res := renderResult{
		Out:         *out,
		PeaksPath:   *peaksPath,
		Source:      source,
		SampleRate:  *rate,
		Channels:    2,
		DurationSec: p.DurationSec(),
		Regions:     len(p.Tracks[0].Regions),
		PeakAbs:     float64(peak),
		NonSilent:   peak > 0.0001,
	}
	return printRenderResult(res, *asJSON)
}

func printRenderResult(res renderResult, asJSON bool) error {
	if asJSON {
		b, err := json.MarshalIndent(res, "", "  ")
		if err != nil {
			return err
		}
		fmt.Println(string(b))
		return nil
	}
	fmt.Printf("wrote %s — %s, %.3fs, %d region(s), peak %.4f (%s)\n",
		res.Out, res.Source, res.DurationSec, res.Regions, res.PeakAbs, silentLabel(res.NonSilent))
	if res.PeaksPath != "" {
		fmt.Printf("wrote peaks %s\n", res.PeaksPath)
	}
	fmt.Printf("verify non-silence: ffprobe -v error -af volumedetect -f null - -i %s 2>&1 | grep mean_volume\n", res.Out)
	return nil
}

func silentLabel(nonSilent bool) string {
	if nonSilent {
		return "non-silent"
	}
	return "SILENT — engine produced no signal"
}

func runPeaks(args []string) error {
	fs := flag.NewFlagSet("peaks", flag.ContinueOnError)
	importPath := fs.String("import", "", "WAV clip to overview (required)")
	out := fs.String("out", "", "write the peaks JSON here (default: stdout)")
	width := fs.Int("width", 800, "column count")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *importPath == "" {
		return fmt.Errorf("peaks: --import <wav> is required")
	}
	clip, err := audiotrack.ImportWAV(*importPath)
	if err != nil {
		return err
	}
	pk := audiotrack.BuildClipPeaks(clip, *width)
	if *out == "" {
		b, merr := json.MarshalIndent(pk, "", "  ")
		if merr != nil {
			return merr
		}
		fmt.Println(string(b))
		return nil
	}
	return writePeaksJSON(*out, pk)
}

// sourceClip returns the source clip and a human label of where it came from.
func sourceClip(importPath string, freq, seconds float64, rate int) (*audiotrack.Clip, string, error) {
	if importPath != "" {
		clip, err := audiotrack.ImportWAV(importPath)
		if err != nil {
			return nil, "", err
		}
		return clip, fmt.Sprintf("import %q", importPath), nil
	}
	if seconds <= 0 {
		return nil, "", fmt.Errorf("--seconds must be positive when synthesizing a tone")
	}
	frames := int(seconds * float64(rate))
	clip := audiotrack.ToneClip(freq, 0.8, frames, rate)
	return clip, fmt.Sprintf("synth tone %.0f Hz", freq), nil
}

// writePeaksJSON marshals a Peaks overview to path.
func writePeaksJSON(path string, pk audiotrack.Peaks) error {
	b, err := json.MarshalIndent(pk, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, b, 0o644); err != nil {
		return fmt.Errorf("write peaks %q: %w", path, err)
	}
	return nil
}
