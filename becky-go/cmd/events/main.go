// becky-events — deterministic "20% nuance" detection over a video + its diarized
// transcript. Most footage is one speaker in one room; this tool flags the
// deviations detectives care about.
//
//	becky-events <video> --diarized <diarized.json> [options]
//
// Three event families, deterministic rules only (no LLM):
//  1. second_speaker / phone_call — every non-dominant speaker turn from the
//     diarized JSON. Turns <= --phone-max-duration are phone_call candidates;
//     longer turns are second_speaker.
//  2. location_change — sample keyframes at ~1 fps, aHash each, and flag a jump
//     when the Hamming distance to the previous frame exceeds --location-threshold.
//     Each hit exports a full-res frame + provenance sidecar into --osint-dir.
//  3. multi_face — OPTIONAL. No face detector ships in this environment, so it is
//     skipped gracefully and noted under "notes".
//
// JSON to stdout (or --output); diagnostics to stderr; exit 0 on success.
package main

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"becky-go/internal/beckyio"
	"becky-go/internal/config"
	"becky-go/internal/mediainfo"
	"becky-go/internal/osintexport"
)

const toolVersion = "becky-events v1.0.0"

// gridBytes is one downscaled keyframe = 8x8 grayscale bytes from ffmpeg.
const gridBytes = osintexport.AHashSize * osintexport.AHashSize

func main() {
	out := flag.String("output", "", "output file (default: stdout)")
	format := flag.String("format", "json", "output format: json")
	diarized := flag.String("diarized", "", "path to becky-diarize JSON (required)")
	osintDir := flag.String("osint-dir", "osint-export", "OSINT export directory")
	locThreshold := flag.Int("location-threshold", 14, "Hamming distance for a location change")
	phoneMax := flag.Float64("phone-max-duration", 20.0, "max seconds for a phone_call event")
	device := flag.String("device", "", "device: cuda, cpu (default from config)")
	noMultiFace := flag.Bool("no-multi-face", false, "disable multi_face detection")
	verbose := flag.Bool("verbose", false, "show progress on stderr")

	input := parsePositional()
	if input == "" {
		beckyio.Fatalf("usage: becky-events <video> --diarized <diarized.json> [options]")
	}
	if _, err := os.Stat(input); err != nil {
		beckyio.Fatalf("input not found: %s", input)
	}
	if *diarized == "" {
		beckyio.Fatalf("--diarized <diarized.json> is required")
	}
	if _, err := os.Stat(*diarized); err != nil {
		beckyio.Fatalf("diarized json not found: %s", *diarized)
	}
	if *format != "" && strings.ToLower(*format) != "json" {
		beckyio.Fatalf("unknown format: %s (only json is supported)", *format)
	}

	cfg := config.Load()
	dev := cfg.Device
	if *device != "" {
		dev = *device
	}

	info, err := mediainfo.Probe(cfg.FFprobe, input)
	if err != nil {
		beckyio.Fatalf("%v", err)
	}

	dia, err := loadDiarized(*diarized)
	if err != nil {
		beckyio.Fatalf("%v", err)
	}

	duration := dia.Duration
	if duration <= 0 {
		duration = info.Duration
	}

	events := []Event{} // non-nil so the JSON "events" field is [] (not null) when empty
	notes := map[string]string{}

	// 1. Speaker-derived events (always available from the diarized JSON).
	spkEvents, dominant := speakerEvents(dia, *phoneMax)
	events = append(events, spkEvents...)
	beckyio.Logf(*verbose, "dominant speaker: %s; %d non-dominant turn(s) flagged", dominant, len(spkEvents))

	// 2. Location-change events via 1 fps aHash sampling (requires video).
	if info.HasVideo {
		locEvents, lerr := locationEvents(cfg, info, input, *osintDir, *locThreshold, dev, *verbose)
		if lerr != nil {
			// Sampling failure is not fatal: report it and keep speaker events.
			beckyio.Logf(true, "warning: location sampling failed: %v", lerr)
			notes["location_change_detection"] = fmt.Sprintf("skipped: %v", lerr)
		} else {
			events = append(events, locEvents...)
			beckyio.Logf(*verbose, "%d location change(s) detected", len(locEvents))
		}
	} else {
		beckyio.Logf(*verbose, "no video stream; skipping location-change detection")
		notes["location_change_detection"] = "skipped: input has no video stream"
	}

	// 3. multi_face — default-on but graceful. Sample frames coarsely and detect
	// faces via the shared internal/faceembed runner; a frame with 2+ faces is a
	// multi_face event. Missing models/deps or no-video are recorded as a skip
	// note (never fatal) so speaker + location events still ship.
	switch {
	case *noMultiFace:
		notes["multi_face"] = "skipped: disabled by --no-multi-face"
		beckyio.Logf(*verbose, "multi_face detection disabled (--no-multi-face)")
	case !info.HasVideo:
		notes["multi_face"] = "skipped: input has no video stream"
		beckyio.Logf(*verbose, "no video stream; skipping multi_face detection")
	default:
		mfEvents, merr := multiFaceEvents(cfg, info, input, dev, *verbose)
		if merr != nil {
			// Missing models/deps or sampling failure is not fatal: note it and
			// keep speaker + location events.
			beckyio.Logf(true, "warning: multi_face detection failed: %v", merr)
			notes["multi_face"] = fmt.Sprintf("skipped: %v", merr)
		} else {
			events = append(events, mfEvents...)
			beckyio.Logf(*verbose, "%d multi_face event(s) detected", len(mfEvents))
		}
	}

	sort.SliceStable(events, func(i, j int) bool { return events[i].Start < events[j].Start })

	report := Output{
		File:     input,
		Duration: round3(duration),
		Events:   events,
		Notes:    notes,
	}
	if err := emit(report, *out); err != nil {
		beckyio.Fatalf("%v", err)
	}
}

// locationEvents samples the clip at ~1 fps, hashes each frame, and emits a
// location_change event (with an OSINT export) wherever consecutive frames differ
// by more than threshold bits.
func locationEvents(cfg config.Config, info mediainfo.Info, input, osintDir string, threshold int, dev string, verbose bool) ([]Event, error) {
	hashes, err := sampleHashes(cfg.FFmpeg, input, dev, verbose)
	if err != nil {
		return nil, err
	}
	beckyio.Logf(verbose, "sampled %d keyframes at 1 fps", len(hashes))
	if len(hashes) < 2 {
		return nil, nil
	}

	srcSHA, err := osintexport.SHA256File(input)
	if err != nil {
		return nil, fmt.Errorf("sha256 source: %w", err)
	}
	fps := info.FPS
	if fps <= 0 {
		fps = 30
	}

	var events []Event
	for i := 1; i < len(hashes); i++ {
		ham := osintexport.HammingDistance(hashes[i-1], hashes[i])
		if ham <= threshold {
			continue
		}
		ts := float64(i) // 1 fps sampling -> frame index i is ~i seconds in.
		srcFrame := int(math.Round(ts * fps))
		ev, eerr := exportLocationEvent(cfg, input, osintDir, srcSHA, info, ts, srcFrame, hashes[i], ham, threshold, verbose)
		if eerr != nil {
			// Don't lose the detection if only the export failed; record the
			// event without the file refs and continue.
			beckyio.Logf(true, "warning: location export at %.0fs failed: %v", ts, eerr)
		}
		events = append(events, ev)
	}
	return events, nil
}

// exportLocationEvent writes the full-res frame + provenance sidecar and returns
// the populated location_change Event.
func exportLocationEvent(cfg config.Config, input, osintDir, srcSHA string, info mediainfo.Info,
	ts float64, srcFrame int, hash uint64, ham, threshold int, verbose bool) (Event, error) {

	stem := fmt.Sprintf("location_%ds_frame%d", int(ts), srcFrame)
	jpgPath := filepath.Join(osintDir, stem+".jpg")
	jsonPath := filepath.Join(osintDir, stem+".json")
	hashHex := osintexport.HashHex(hash)

	ev := Event{
		Type:        "location_change",
		Start:       round3(ts),
		End:         round3(ts),
		Frame:       srcFrame,
		Timestamp:   round3(ts),
		Hamming:     ham,
		Confidence:  locationConfidence(ham, threshold),
		Description: "Background changed significantly",
	}

	if err := osintexport.ExtractFrame(cfg.FFmpeg, input, ts, jpgPath, "jpg", 2); err != nil {
		return ev, fmt.Errorf("extract frame: %w", err)
	}
	side := osintexport.Sidecar{
		SourceFile:     input,
		SourceSHA256:   srcSHA,
		EventType:      "location_change",
		Timestamp:      round3(ts),
		FrameIndex:     srcFrame,
		FPS:            round3(info.FPS),
		Resolution:     info.Resolution(),
		PerceptualHash: hashHex,
		Notes:          osintexport.ProvenanceNote,
		ExtractedAt:    time.Now().UTC().Format(time.RFC3339),
		Tool:           toolVersion,
	}
	if err := osintexport.WriteProvenance(jsonPath, side); err != nil {
		return ev, fmt.Errorf("write provenance: %w", err)
	}
	ev.OSINTExport = filepath.ToSlash(jpgPath)
	ev.Provenance = filepath.ToSlash(jsonPath)
	beckyio.Logf(verbose, "  location change @ %.0fs (hamming=%d) -> %s", ts, ham, jpgPath)
	return ev, nil
}

// sampleHashes runs ffmpeg once to produce a 1 fps, 8x8 grayscale raw stream and
// computes an aHash per 64-byte frame. CUDA decode is best-effort; on failure we
// retry on the CPU so sampling still runs.
func sampleHashes(ffmpeg, input, dev string, verbose bool) ([]uint64, error) {
	tryCUDA := strings.EqualFold(dev, "cuda")
	hashes, err := runSample(ffmpeg, input, tryCUDA)
	if err != nil && tryCUDA {
		beckyio.Logf(verbose, "cuda decode failed (%v); retrying on cpu", err)
		hashes, err = runSample(ffmpeg, input, false)
	}
	return hashes, err
}

func runSample(ffmpeg, input string, hwaccel bool) ([]uint64, error) {
	args := []string{"-y"}
	if hwaccel {
		args = append(args, "-hwaccel", "cuda")
	}
	args = append(args,
		"-i", input,
		"-vf", fmt.Sprintf("fps=1,scale=%d:%d,format=gray", osintexport.AHashSize, osintexport.AHashSize),
		"-f", "rawvideo", "-pix_fmt", "gray",
		"-loglevel", "error", "-")

	cmd := exec.Command(ffmpeg, args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	var errBuf strings.Builder
	cmd.Stderr = &errBuf
	if err := cmd.Start(); err != nil {
		return nil, err
	}

	hashes, readErr := readGrayFrames(stdout)
	waitErr := cmd.Wait()
	if waitErr != nil {
		return nil, fmt.Errorf("ffmpeg sample: %v: %s", waitErr, tail(errBuf.String()))
	}
	if readErr != nil {
		return nil, readErr
	}
	return hashes, nil
}

// readGrayFrames consumes the raw gray stream 64 bytes at a time, hashing each
// frame. A trailing partial frame (shorter than 64 bytes) is ignored.
func readGrayFrames(r io.Reader) ([]uint64, error) {
	br := bufio.NewReader(r)
	buf := make([]byte, gridBytes)
	var hashes []uint64
	for {
		_, err := io.ReadFull(br, buf)
		if err == io.EOF || err == io.ErrUnexpectedEOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("read gray frame: %w", err)
		}
		h, herr := osintexport.AHashFromGray64(buf)
		if herr != nil {
			return nil, herr
		}
		hashes = append(hashes, h)
	}
	return hashes, nil
}

func parsePositional() string {
	flag.Parse()
	rest := flag.Args()
	if len(rest) == 0 {
		return ""
	}
	input := rest[0]
	if len(rest) > 1 {
		_ = flag.CommandLine.Parse(rest[1:])
	}
	return input
}

func emit(o Output, outPath string) error {
	if outPath == "" {
		beckyio.PrintJSON(o)
		return nil
	}
	b, err := marshalIndent(o)
	if err != nil {
		return err
	}
	if err := os.WriteFile(outPath, b, 0o644); err != nil {
		return fmt.Errorf("write output: %w", err)
	}
	return nil
}

func round3(f float64) float64 { return float64(int(f*1000+0.5)) / 1000 }

func tail(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 800 {
		return s[len(s)-800:]
	}
	return s
}
