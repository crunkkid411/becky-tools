// becky-vox — multi-take vocal alignment (Melodyne/VocALign class): match N recorded
// takes to a guide in TIMING (DTW warp of onsets) + PITCH (note-segmented,
// formant-preserving correction — the render is the local-agent stub) and COMP the
// best bits. becky's differentiator is the TRUST MODEL: it ANALYZES (per-syllable
// warp map + per-note pitch decisions + per-phrase confidence) and CONCLUDES only
// where corroborated, FLAGGING the rest for per-phrase approval — never a black-box
// global tightness/retune knob (SPEC-BECKY-VOX.md).
//
//	becky-vox align   --guide lead.wav --alt double.wav --mode stack --out double.aligned.wav --json out.json
//	becky-vox tune    --in lead.wav    --key Aminor --max-shift 2 --out lead.tuned.wav --json out.json
//	becky-vox comp    --takes-json t.json --metric balanced --out comp.wav --json comp.json
//	becky-vox analyze --guide lead.wav --alt double.wav --json out.json   # analysis only, renders nothing
//
// Deterministic floor (cloud-runnable today): pass --features <json> matching the
// vox_align.py contract (internal/vox/features.go) and becky runs the full pure-Go
// pipeline — DTW warp map, pitch decisions, phrases, comp — and writes the analysis
// JSON. The audio path (real WAVs, no --features) is the LOCAL-AGENT boundary:
// vox_align.py extracts features and renders the formant-preserving audio. Until
// it's wired, the audio path degrades cleanly (degrade-never-crash) telling you to
// pass --features.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"becky-go/internal/vox"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	switch os.Args[1] {
	case "align", "analyze", "tune":
		os.Exit(runAlign(os.Args[1], os.Args[2:]))
	case "comp":
		os.Exit(runComp(os.Args[2:]))
	case "-h", "--help", "help":
		usage()
		os.Exit(0)
	default:
		fmt.Fprintf(os.Stderr, "becky-vox: unknown subcommand %q\n", os.Args[1])
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage:")
	fmt.Fprintln(os.Stderr, "  becky-vox align   --guide g.wav --alt a.wav [--mode stack|tune] [--features f.json] [--out a.aligned.wav] [--json out.json]")
	fmt.Fprintln(os.Stderr, "  becky-vox tune    --in v.wav --key Aminor [--max-shift 2] [--features f.json] [--json out.json]")
	fmt.Fprintln(os.Stderr, "  becky-vox analyze --guide g.wav --alt a.wav [--features f.json] [--json out.json]   (renders nothing)")
	fmt.Fprintln(os.Stderr, "  becky-vox comp    --takes-json t.json [--metric balanced|pitch|timing] [--json comp.json]")
}

func runAlign(sub string, argv []string) int {
	fs := flag.NewFlagSet(sub, flag.ContinueOnError)
	guide := fs.String("guide", "", "guide/lead WAV")
	in := fs.String("in", "", "single input WAV (tune mode; alias for --guide)")
	alt := fs.String("alt", "", "alternate take WAV to align onto the guide")
	mode := fs.String("mode", defaultMode(sub), "stack | tune")
	key := fs.String("key", "", "tune-mode target key, e.g. Aminor")
	maxShift := fs.Float64("max-shift", 2.0, "max pitch move in semitones")
	features := fs.String("features", "", "pre-extracted vox features JSON (vox_align.py contract); cloud path")
	out := fs.String("out", "", "rendered aligned/tuned WAV path (recorded for the local renderer)")
	jsonOut := fs.String("json", "", "write the analysis JSON here (default: stdout)")
	if err := fs.Parse(argv); err != nil {
		return 2
	}
	g := firstNonEmpty(*guide, *in)
	if g == "" {
		fmt.Fprintln(os.Stderr, "becky-vox: need --guide (or --in for tune)")
		return 2
	}

	opt := vox.DefaultAlignOptions()
	opt.Mode = *mode
	opt.Key = *key
	opt.MaxShiftSemi = *maxShift

	feats, err := loadFeatures(*features)
	if err != nil {
		fmt.Fprintf(os.Stderr, "becky-vox: %v\n", err)
		return 1
	}
	res := vox.Align(feats, opt, g, *alt)
	if *out != "" {
		// The render is the local-agent stub; record the intended target path so the
		// renderer (vox_align.py) knows where to bake the aligned audio.
		res.AlignedWav = *out
	}
	if err := emit(res, *jsonOut); err != nil {
		fmt.Fprintf(os.Stderr, "becky-vox: %v\n", err)
		return 1
	}
	if res.Degraded {
		return 1
	}
	return 0
}

func runComp(argv []string) int {
	fs := flag.NewFlagSet("comp", flag.ContinueOnError)
	takesJSON := fs.String("takes-json", "", "JSON array of per-take analyses ([{Name,Phrases,Levels}])")
	metric := fs.String("metric", "balanced", "balanced | pitch | timing")
	out := fs.String("out", "", "comp WAV path (recorded for the local renderer)")
	jsonOut := fs.String("json", "", "write the comp decision JSON here (default: stdout)")
	if err := fs.Parse(argv); err != nil {
		return 2
	}
	if *takesJSON == "" {
		fmt.Fprintln(os.Stderr, "becky-vox comp: need --takes-json (per-take analyses; produced by `align`)")
		return 2
	}
	takes, err := loadTakes(*takesJSON)
	if err != nil {
		fmt.Fprintf(os.Stderr, "becky-vox: %v\n", err)
		return 1
	}
	res := vox.Comp(takes, *metric)
	res.Out = *out
	if err := emit(res, *jsonOut); err != nil {
		fmt.Fprintf(os.Stderr, "becky-vox: %v\n", err)
		return 1
	}
	if res.Degraded {
		return 1
	}
	return 0
}

// loadFeatures reads pre-extracted features when --features is given; otherwise the
// audio→features step is the LOCAL-AGENT stub and the cloud build returns a clean
// degrade telling the user to pass --features (the real wiring shells vox_align.py).
func loadFeatures(path string) (vox.VoxFeatures, error) {
	if path != "" {
		b, err := os.ReadFile(path)
		if err != nil {
			return vox.VoxFeatures{}, fmt.Errorf("read features %s: %w", path, err)
		}
		var f vox.VoxFeatures
		if err := json.Unmarshal(b, &f); err != nil {
			return vox.VoxFeatures{}, fmt.Errorf("parse features %s: %w", path, err)
		}
		return f, nil
	}
	return vox.VoxFeatures{
		Skipped: true,
		Reason:  "no DSP backend on this machine — re-run with --features <vox.json> (vox_align.py contract) or wire the local vox pyhelper",
	}, nil
}

// loadTakes reads the per-take analyses for comping (each is {Name,Phrases,Levels}).
func loadTakes(path string) ([]vox.TakeAnalysis, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read takes %s: %w", path, err)
	}
	var takes []vox.TakeAnalysis
	if err := json.Unmarshal(b, &takes); err != nil {
		return nil, fmt.Errorf("parse takes %s: %w", path, err)
	}
	return takes, nil
}

// emit writes v as indented JSON to dest (or stdout when dest is "").
func emit(v interface{}, dest string) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("encode: %w", err)
	}
	b = append(b, '\n')
	if dest == "" {
		os.Stdout.Write(b)
		return nil
	}
	if err := os.WriteFile(dest, b, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", dest, err)
	}
	return nil
}

func defaultMode(sub string) string {
	if sub == "tune" {
		return "tune"
	}
	return "stack"
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
