// becky-hum — the INPUT side of becky-compose: a hummed/sung melody becomes a
// {key, tempo, monophonic MIDI} decision sheet with key-aware, per-note suggestions
// (SPEC-BECKY-HUM.md). becky detects the key (Krumhansl-Schmuckler) + tempo (onset
// autocorrelation), transcribes the melody to MIDI, and tells you per note where
// you went off-key and what you probably meant — it SUGGESTS, never silently
// autotunes. The melody.mid feeds straight into becky-compose --melody/--key/--bpm.
//
//	becky-hum analyze --wav hum.wav [--out dir] [--key-hint F#m] [--genre crunkcore]
//	                  [--engine basic-pitch|pyin] [--quantize 1/16|off]
//	                  [--apply-suggestions] [--features pitch.json] [--json]
//
// Deterministic floor (cloud-runnable today): give --features <json> matching the
// pitch-helper contract (internal/hum/features.go) and becky runs the full pure-Go
// pipeline — key/tempo/segment/suggest — and writes melody.mid + hum.json. The
// audio path (--wav with no --features) is the LOCAL-AGENT boundary: it shells
// ffmpeg (16 kHz mono) + the pitch pyhelper to produce those features. Until that
// helper is wired, --wav alone degrades cleanly (degrade-never-crash) and tells you
// to pass --features.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"becky-go/internal/hum"
	"becky-go/internal/pathx"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	switch os.Args[1] {
	case "analyze", "record":
		os.Exit(runAnalyze(os.Args[2:]))
	case "-h", "--help", "help":
		usage()
		os.Exit(0)
	default:
		fmt.Fprintf(os.Stderr, "becky-hum: unknown subcommand %q\n", os.Args[1])
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: becky-hum analyze --wav hum.wav [--features pitch.json] [--out dir]")
	fmt.Fprintln(os.Stderr, "         [--key-hint F#m] [--genre crunkcore] [--engine basic-pitch|pyin]")
	fmt.Fprintln(os.Stderr, "         [--quantize 1/16|off] [--apply-suggestions] [--json]")
}

func runAnalyze(argv []string) int {
	fs := flag.NewFlagSet("analyze", flag.ContinueOnError)
	wav := fs.String("wav", "", "input audio file (16 kHz mono is produced by ffmpeg)")
	features := fs.String("features", "", "pre-extracted pitch features JSON (pitch-helper contract); cloud path")
	out := fs.String("out", "", "output directory (default: ./<wav-base>-hum)")
	keyHint := fs.String("key-hint", "", "skip key detection, e.g. F#m (still reported)")
	genre := fs.String("genre", "", "genre for tempo-octave + chord context, e.g. crunkcore")
	engine := fs.String("engine", "basic-pitch", "pitch engine label: basic-pitch | pyin")
	device := fs.String("device", "auto", "device label for the pitch helper: auto | cpu | cuda")
	quantize := fs.String("quantize", "off", "rhythmic quantization: 1/16, 1/8, ... or off")
	apply := fs.Bool("apply-suggestions", false, "also write melody.corrected.mid with suggested pitches")
	asJSON := fs.Bool("json", false, "print the result JSON to stdout")
	if err := fs.Parse(argv); err != nil {
		return 2
	}
	if *wav == "" && *features == "" {
		fmt.Fprintln(os.Stderr, "becky-hum: need --wav or --features")
		return 2
	}

	opt := hum.DefaultOptions()
	opt.Wav = *wav
	opt.KeyHint = *keyHint
	opt.Genre = *genre
	opt.Engine = *engine
	opt.QuantDiv = parseQuantize(*quantize)

	feats, err := loadFeatures(*features, *wav, *engine, *device)
	if err != nil {
		// Degrade-never-crash: report plainly and exit 1 (no panic).
		fmt.Fprintf(os.Stderr, "becky-hum: %v\n", err)
		return 1
	}

	res := hum.Analyze(feats, opt)
	dir := outDir(*out, *wav, *features)
	if err := writeArtifacts(dir, res, *apply); err != nil {
		fmt.Fprintf(os.Stderr, "becky-hum: %v\n", err)
		return 1
	}

	if *asJSON {
		printJSON(res)
	} else {
		printReport(res, dir)
	}
	if res.Degraded {
		return 1
	}
	return 0
}

// loadFeatures reads pre-extracted features when --features is given; otherwise it
// runs the pure-Go DSP extractor over the WAV (offline: no Python, no model, no
// ffmpeg). The DSP path is the deterministic FLOOR — it recovers a clear hummed
// melody's key/tempo/notes. Precise f0 (scoops, vibrato, low SNR) stays the
// model-helper boundary, reachable by passing --features <pitch.json>. Never panics.
func loadFeatures(featuresPath, wav, engine, device string) (hum.Features, error) {
	if featuresPath != "" {
		b, err := os.ReadFile(featuresPath)
		if err != nil {
			return hum.Features{}, fmt.Errorf("read features %s: %w", featuresPath, err)
		}
		var f hum.Features
		if err := json.Unmarshal(b, &f); err != nil {
			return hum.Features{}, fmt.Errorf("parse features %s: %w", featuresPath, err)
		}
		return f, nil
	}
	// Audio path: decode + analyze the WAV with internal/dsp (ported from dawbase).
	// A genuine I/O failure (missing file) is an error; an undecodable/silent take
	// returns Skipped features so the run degrades cleanly rather than crashing.
	return hum.DSPExtractor{}.Extract(wav, engine, device)
}

func writeArtifacts(dir string, res hum.Result, apply bool) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create %s: %w", dir, err)
	}
	melody := hum.MelodySMF(res.Notes, res.Tempo.BPM, 480)
	if err := os.WriteFile(filepath.Join(dir, "melody.mid"), melody.Bytes(), 0o644); err != nil {
		return fmt.Errorf("write melody.mid: %w", err)
	}
	if apply {
		corrected := hum.CorrectedSMF(res.Notes, res.Tempo.BPM, 480)
		if err := os.WriteFile(filepath.Join(dir, "melody.corrected.mid"), corrected.Bytes(), 0o644); err != nil {
			return fmt.Errorf("write melody.corrected.mid: %w", err)
		}
	}
	b, err := json.MarshalIndent(res, "", "  ")
	if err != nil {
		return fmt.Errorf("encode result: %w", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "hum.json"), append(b, '\n'), 0o644); err != nil {
		return fmt.Errorf("write hum.json: %w", err)
	}
	return nil
}

// parseQuantize turns "1/16", "16", "off", "" into a subdivision count (0 = off).
func parseQuantize(q string) int {
	q = strings.TrimSpace(strings.ToLower(q))
	if q == "" || q == "off" || q == "none" || q == "0" {
		return 0
	}
	if i := strings.LastIndex(q, "/"); i >= 0 {
		q = q[i+1:]
	}
	n := 0
	for _, c := range q {
		if c < '0' || c > '9' {
			return 0
		}
		n = n*10 + int(c-'0')
	}
	return n
}

func outDir(out, wav, features string) string {
	if out != "" {
		return out
	}
	src := wav
	if src == "" {
		src = features
	}
	base := pathx.Base(src)
	if dot := strings.LastIndex(base, "."); dot > 0 {
		base = base[:dot]
	}
	if base == "" {
		base = "hum"
	}
	return base + "-hum"
}

func printJSON(res hum.Result) {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	_ = enc.Encode(res)
}

func printReport(res hum.Result, dir string) {
	fmt.Printf("becky-hum: %s\n", res.Engine)
	if res.Degraded {
		fmt.Printf("  DEGRADED: %s\n", res.Reason)
	}
	fmt.Printf("  key  : %s (%s, confidence %.2f)", res.Key.Compose, res.Key.Method, res.Key.Confidence)
	if res.Key.Ambiguous {
		fmt.Printf("  <-- ambiguous, runner-up %s (gap %.3f)", res.Key.RunnerUp, res.Key.CorrGap)
	}
	fmt.Println()
	fmt.Printf("  tempo: %d BPM (%s, %s)\n", res.Tempo.BPM, res.Tempo.Method, res.Tempo.ResolvedBy)
	review := 0
	for _, n := range res.Notes {
		if n.NeedsReview {
			review++
		}
	}
	fmt.Printf("  notes: %d transcribed, %d flagged for review (off/ambiguous key)\n", len(res.Notes), review)
	fmt.Printf("  files: %s/melody.mid + hum.json\n", dir)
	fmt.Printf("  next : %s\n", res.Compose)
}
