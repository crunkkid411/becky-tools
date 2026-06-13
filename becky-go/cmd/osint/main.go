// becky-osint — OSINT frame exporter for detective handoff. For each event in a
// becky-events JSON file it extracts the full-resolution frame at the event's
// timestamp, computes a perceptual hash of that frame, and writes the frame plus
// a provenance sidecar into --output-dir. A machine-readable manifest of every
// export is emitted as JSON to stdout (or --output).
//
//	becky-osint <video> --events <events.json> [options]
//
// Options:
//
//	--output-dir <path>   output directory (default: osint-export/)
//	--format jpg|png      image format (default: jpg)
//	--quality <int>       JPEG quality 1-100 (default: 95)
//	--include-audio       also export a short wav snippet around each event
//	--output <file>       write the manifest JSON here instead of stdout
//	--verbose             show progress on stderr
//
// This tool only READS the source video. It does NOT interpret frames (no LLM):
// it exports candidate frames + provenance so detectives can do OSINT matching.
//
// Frame extraction, SHA-256, perceptual hashing, and sidecar writing are reused
// verbatim from internal/osintexport (the same primitives becky-events uses),
// so the provenance format is identical across both tools.
package main

import (
	"flag"
	"fmt"
	"image"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	// Register JPEG and PNG decoders so we can re-read the extracted frame and
	// compute its perceptual hash in-process (stdlib only).
	_ "image/jpeg"
	_ "image/png"

	"becky-go/internal/beckyio"
	"becky-go/internal/config"
	"becky-go/internal/exifmeta"
	"becky-go/internal/mediainfo"
	"becky-go/internal/osintexport"
)

const toolVersion = "becky-osint v1.0.0"

// jpegQualityToFFmpeg maps a 1-100 user quality (higher = better) to ffmpeg's
// -q:v scale (2 = best, 31 = worst). This keeps the CLI flag intuitive while
// feeding osintexport.ExtractFrame the value it forwards to ffmpeg.
func jpegQualityToFFmpeg(quality int) int {
	if quality < 1 {
		quality = 1
	}
	if quality > 100 {
		quality = 100
	}
	// Linear map: 100 -> 2, 1 -> 31.
	q := 31 - (quality-1)*29/99
	if q < 2 {
		q = 2
	}
	if q > 31 {
		q = 31
	}
	return q
}

// audioSnippetSeconds is the half-window (before and after the event) for the
// optional --include-audio wav export.
const audioSnippetSeconds = 2.0

func main() {
	eventsPath := flag.String("events", "", "path to becky-events JSON (required unless --metadata-only)")
	outputDir := flag.String("output-dir", "osint-export", "output directory")
	format := flag.String("format", "jpg", "image format: jpg, png")
	quality := flag.Int("quality", 95, "JPEG quality 1-100 (higher = better)")
	includeAudio := flag.Bool("include-audio", false, "also export a short wav snippet around each event")
	output := flag.String("output", "", "write the manifest JSON here instead of stdout")
	metadataOnly := flag.Bool("metadata-only", false, "extract forensic metadata only; skip frame export (no --events needed)")
	exiftoolPath := flag.String("exiftool", "", "path to exiftool (default: auto-detect on PATH / common installs)")
	verbose := flag.Bool("verbose", false, "show progress on stderr")

	input := parsePositional()
	if input == "" {
		beckyio.Fatalf("usage: becky-osint <video> --events <events.json> [options] | becky-osint <video> --metadata-only")
	}
	if _, err := os.Stat(input); err != nil {
		beckyio.Fatalf("input not found: %s", input)
	}
	if !*metadataOnly {
		if *eventsPath == "" {
			beckyio.Fatalf("--events <events.json> is required (or pass --metadata-only)")
		}
		if _, err := os.Stat(*eventsPath); err != nil {
			beckyio.Fatalf("events json not found: %s", *eventsPath)
		}
	}
	fmtLower := strings.ToLower(*format)
	if fmtLower != "jpg" && fmtLower != "jpeg" && fmtLower != "png" {
		beckyio.Fatalf("unknown format: %s (use jpg or png)", *format)
	}

	cfg := config.Load()

	info, err := mediainfo.Probe(cfg.FFprobe, input)
	if err != nil {
		beckyio.Fatalf("%v", err)
	}
	fps := info.FPS
	if fps <= 0 {
		fps = 30
		beckyio.Logf(*verbose, "fps unavailable from probe; assuming %.0f", fps)
	}

	srcSHA, err := osintexport.SHA256File(input)
	if err != nil {
		beckyio.Fatalf("%v", err)
	}
	beckyio.Logf(*verbose, "source sha256: %s", srcSHA)

	if err := os.MkdirAll(*outputDir, 0o755); err != nil {
		beckyio.Fatalf("create output dir: %v", err)
	}

	// Forensic metadata pass (ADDITIVE). Runs regardless of frame export so the
	// provenance block is always present; failures are non-fatal and degrade to
	// the mtime-only fallback inside exifmeta.
	meta := extractMetadata(cfg, *exiftoolPath, input, srcSHA, *outputDir, info, *verbose)

	// Prefer the TRUE capture date for recording_date when a trustworthy capture
	// tag was found; otherwise keep the historic mtime-derived date (which the
	// metadata block now labels untrusted).
	recDate := recordingDate(input)
	if d := trustedCaptureDate(meta); d != "" {
		recDate = d
	}

	manifest := Manifest{
		Tool:          toolVersion,
		SourceFile:    input,
		SourceSHA256:  srcSHA,
		OutputDir:     filepath.ToSlash(*outputDir),
		Format:        fmtLower,
		FPS:           round3(fps),
		Resolution:    info.Resolution(),
		RecordingDate: recDate,
		Metadata:      meta,
		Exports:       []ExportRecord{}, // non-nil so "exports" is [] (not null) on a zero-event input
	}

	if *metadataOnly {
		beckyio.Logf(*verbose, "metadata-only: skipping frame export")
		if err := emit(manifest, *output); err != nil {
			beckyio.Fatalf("%v", err)
		}
		return
	}

	if !info.HasVideo {
		beckyio.Fatalf("input has no video stream; cannot export frames")
	}

	ef, err := loadEvents(*eventsPath)
	if err != nil {
		beckyio.Fatalf("%v", err)
	}
	beckyio.Logf(*verbose, "loaded %d event(s) from %s", len(ef.Events), *eventsPath)

	ffmpegQ := jpegQualityToFFmpeg(*quality)

	for i, ev := range ef.Events {
		rec, serr := exportEvent(cfg, input, *outputDir, srcSHA, info, fps,
			fmtLower, ffmpegQ, recDate, ev, *includeAudio, *verbose)
		if serr != nil {
			beckyio.Logf(true, "warning: event %d (%s @ %.3fs) skipped: %v",
				i, ev.Type, ev.EventTime(), serr)
			manifest.Skipped = append(manifest.Skipped, SkipRecord{
				EventType: ev.Type,
				Timestamp: round3(ev.EventTime()),
				Reason:    serr.Error(),
			})
			continue
		}
		manifest.Exports = append(manifest.Exports, rec)
	}
	manifest.Exported = len(manifest.Exports)

	beckyio.Logf(*verbose, "exported %d frame(s), skipped %d", manifest.Exported, len(manifest.Skipped))
	if err := emit(manifest, *output); err != nil {
		beckyio.Fatalf("%v", err)
	}
}

// exportEvent extracts the frame, hashes it, writes the provenance sidecar, and
// (optionally) the audio snippet for one event. It reuses osintexport for the
// extract / hash / sidecar primitives and returns the manifest record.
func exportEvent(cfg config.Config, input, outputDir, srcSHA string, info mediainfo.Info,
	fps float64, format string, ffmpegQ int, recDate string, ev Event,
	includeAudio, verbose bool) (ExportRecord, error) {

	ts := ev.EventTime()
	if ts < 0 {
		return ExportRecord{}, fmt.Errorf("negative timestamp %.3f", ts)
	}
	frameIndex := int(math.Round(ts * fps))

	ext := "jpg"
	if format == "png" {
		ext = "png"
	}
	stem := fmt.Sprintf("%s_%ds_frame%d", filePrefix(ev.Type), int(ts), frameIndex)
	framePath := filepath.Join(outputDir, stem+"."+ext)
	sidecarPath := filepath.Join(outputDir, stem+".json")

	if err := osintexport.ExtractFrame(cfg.FFmpeg, input, ts, framePath, format, ffmpegQ); err != nil {
		return ExportRecord{}, fmt.Errorf("extract frame: %w", err)
	}

	hashHex, err := perceptualHash(framePath)
	if err != nil {
		return ExportRecord{}, fmt.Errorf("perceptual hash: %w", err)
	}

	side := osintexport.Sidecar{
		SourceFile:     input,
		SourceSHA256:   srcSHA,
		EventType:      ev.Type,
		Timestamp:      round3(ts),
		FrameIndex:     frameIndex,
		FPS:            round3(fps),
		Resolution:     info.Resolution(),
		PerceptualHash: hashHex,
		Notes:          osintexport.ProvenanceNote,
		ExtractedAt:    time.Now().UTC().Format(time.RFC3339),
		Tool:           toolVersion,
	}
	if err := osintexport.WriteProvenance(sidecarPath, side); err != nil {
		return ExportRecord{}, fmt.Errorf("write provenance: %w", err)
	}

	rec := ExportRecord{
		EventType:      ev.Type,
		Timestamp:      round3(ts),
		FrameIndex:     frameIndex,
		FramePath:      filepath.ToSlash(framePath),
		SidecarPath:    filepath.ToSlash(sidecarPath),
		PerceptualHash: hashHex,
		SHA256:         srcSHA,
	}

	if includeAudio {
		audioPath := filepath.Join(outputDir, stem+".wav")
		if aerr := exportAudioSnippet(cfg, input, audioPath, ts, info); aerr != nil {
			// Best-effort: note the failure but keep the frame export.
			beckyio.Logf(verbose, "  audio snippet @ %.0fs failed: %v", ts, aerr)
		} else {
			rec.AudioPath = filepath.ToSlash(audioPath)
		}
	}

	beckyio.Logf(verbose, "  %s @ %.3fs -> %s (frame %d, phash %s)",
		ev.Type, ts, framePath, frameIndex, hashHex)
	return rec, nil
}

// perceptualHash decodes the extracted frame and computes its 64-bit aHash via
// the shared osintexport helpers, returning the 16-char hex string for the
// sidecar.
func perceptualHash(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("open frame: %w", err)
	}
	defer f.Close()
	img, _, err := image.Decode(f)
	if err != nil {
		return "", fmt.Errorf("decode frame: %w", err)
	}
	return osintexport.HashHex(osintexport.AHashFromImage(img)), nil
}

// exportAudioSnippet writes a short 16k mono wav window centered on the event.
// Best-effort: the source video is only read. Skips cleanly when there is no
// audio stream.
func exportAudioSnippet(cfg config.Config, input, outPath string, ts float64, info mediainfo.Info) error {
	if !info.HasAudio {
		return fmt.Errorf("no audio stream")
	}
	start := ts - audioSnippetSeconds
	if start < 0 {
		start = 0
	}
	dur := audioSnippetSeconds * 2
	cmd := exec.Command(cfg.FFmpeg, "-y",
		"-ss", fmt.Sprintf("%.3f", start), "-i", input,
		"-t", fmt.Sprintf("%.3f", dur),
		"-vn", "-ac", "1", "-ar", "16000", "-acodec", "pcm_s16le",
		"-loglevel", "error", outPath)
	var errBuf strings.Builder
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%v: %s", err, tail(errBuf.String()))
	}
	if _, err := os.Stat(outPath); err != nil {
		return fmt.Errorf("ffmpeg produced no audio at %.3fs", ts)
	}
	return nil
}

// recordingDate returns the source file's modification date as YYYY-MM-DD. This
// is the UNTRUSTED fallback only: the metadata pass surfaces the true capture
// date (and labels mtime as untrusted) — see trustedCaptureDate. An empty string
// is returned if the mtime cannot be read (the sidecar then omits the field).
func recordingDate(path string) string {
	fi, err := os.Stat(path)
	if err != nil {
		return ""
	}
	return fi.ModTime().Format("2006-01-02")
}

// extractMetadata runs the forensic metadata pass and writes its provenance
// sidecar. It is best-effort: a probe failure logs a warning and returns nil so
// the frame-export behavior is never affected (the manifest then omits the
// metadata block but still reports frames + SHA-256 provenance).
func extractMetadata(cfg config.Config, exiftoolPath, input, srcSHA, outputDir string,
	info mediainfo.Info, verbose bool) *exifmeta.Metadata {

	ex := exifmeta.NewExtractor(exiftoolPath, cfg.FFprobe)
	if ex.Exiftool == "" {
		beckyio.Logf(verbose, "exiftool not found; using ffprobe-only metadata (reduced tag coverage)")
	} else {
		beckyio.Logf(verbose, "metadata source: exiftool (%s)", ex.Exiftool)
	}

	md, err := ex.Extract(input)
	if err != nil {
		beckyio.Logf(true, "warning: metadata pass failed: %v", err)
		return nil
	}
	// Backfill resolution from the basic probe when neither tool surfaced it.
	if md.Resolution == "" {
		md.Resolution = info.Resolution()
	}

	beckyio.Logf(verbose, "capture_time=%s (%s) device=%s %s rotation=%d",
		md.CaptureTimeLocal, md.CaptureTimeSource, md.DeviceMake, md.DeviceModel, md.Rotation)

	sidecar := filepath.Join(outputDir, "osint-metadata.json")
	if werr := writeMetadataSidecar(sidecar, &md, input, srcSHA); werr != nil {
		beckyio.Logf(verbose, "  metadata sidecar write failed: %v", werr)
	} else {
		beckyio.Logf(verbose, "  metadata sidecar -> %s", sidecar)
	}
	return &md
}

// trustedCaptureDate returns the capture date (YYYY-MM-DD, in the device-local
// zone) ONLY when the metadata came from a real capture tag — never from the
// untrusted mtime. Returns "" otherwise so the caller keeps the mtime fallback.
func trustedCaptureDate(md *exifmeta.Metadata) string {
	if md == nil || md.CaptureTimeSource == exifmeta.SourceMTime || md.CaptureTimeLocal == "" {
		return ""
	}
	t, err := time.Parse(time.RFC3339, md.CaptureTimeLocal)
	if err != nil {
		return ""
	}
	return t.Format("2006-01-02")
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

func emit(m Manifest, outPath string) error {
	if outPath == "" {
		beckyio.PrintJSON(m)
		return nil
	}
	b, err := marshalIndent(m)
	if err != nil {
		return err
	}
	if err := os.WriteFile(outPath, b, 0o644); err != nil {
		return fmt.Errorf("write output: %w", err)
	}
	return nil
}

func round3(f float64) float64 { return float64(int(f*1000+0.5)) / 1000 }

// nowRFC3339 returns the current UTC time as an RFC3339 string (used to stamp the
// metadata sidecar's extracted_at field).
func nowRFC3339() string { return time.Now().UTC().Format(time.RFC3339) }

func tail(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 800 {
		return s[len(s)-800:]
	}
	return s
}
