// becky-export — render the final output from a becky-cut v1 timeline JSON.
//
//	becky-export <timeline_json> [--output f] [--format mp4|kdenlive]
//	             [--codec h264_nvenc] [--bitrate 12M] [--fps 29.97]
//	             [--hwaccel] [--include-subs] [--subs path] [--verbose]
//
// Two modes (matching becky-cut's rendering approach exactly):
//  1. mp4 (default): auto-editor <timeline.json> -o out.mp4 --progress none -c:v <codec>
//     Optional --include-subs burns an SRT into the result with a best-effort
//     ffmpeg subtitles pass (auto-editor itself has no burn-in option).
//  2. kdenlive: auto-editor <source> --edit audio --margin 0.04s,0.25s
//     --export kdenlive -o out.kdenlive — Jordan's locked margin, like becky-cut.
//
// Video encodes always use cfg.Codec (h264_nvenc) — NEVER libx264.
package main

import (
	"encoding/json"
	"encoding/xml"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"becky-go/internal/beckyio"
	"becky-go/internal/config"
	"becky-go/internal/mediainfo"
)

// timeline mirrors the becky-cut v1 timeline JSON: a source path plus chunks of
// [start, end, speed] (speed 1.0 = keep, large = cut).
type timeline struct {
	Version string  `json:"version"`
	Source  string  `json:"source"`
	Chunks  [][]any `json:"chunks"`
}

const lockedMargin = "0.04s,0.25s" // Jordan's locked auto-editor margin

func main() {
	out := flag.String("output", "", "output file (default: <source-stem>_edited.mp4/.kdenlive)")
	format := flag.String("format", "mp4", "output format: mp4, kdenlive")
	codec := flag.String("codec", "", "video codec (default from config: h264_nvenc)")
	bitrate := flag.String("bitrate", "12M", "video bitrate")
	fps := flag.String("fps", "", "frame rate (default: source/timeline rate)")
	hwaccel := flag.Bool("hwaccel", true, "use CUDA hardware acceleration")
	includeSubs := flag.Bool("include-subs", false, "burn an SRT into the video")
	subs := flag.String("subs", "", "path to SRT file (for --include-subs)")
	verbose := flag.Bool("verbose", false, "show progress on stderr")

	tlPath := parsePositional()
	if tlPath == "" {
		beckyio.Fatalf("usage: becky-export <timeline_json> [options]")
	}

	cfg := config.Load()
	tl, err := loadTimeline(tlPath)
	if err != nil {
		beckyio.Fatalf("%v", err)
	}
	if _, err := os.Stat(tl.Source); err != nil {
		beckyio.Fatalf("timeline source not found: %s", tl.Source)
	}

	vcodec := cfg.Codec
	if *codec != "" {
		vcodec = *codec
	}
	if strings.Contains(strings.ToLower(vcodec), "libx264") {
		beckyio.Fatalf("libx264 is not permitted; use h264_nvenc or hevc_nvenc")
	}

	switch *format {
	case "mp4":
		runMP4(cfg, tl, tlPath, *out, vcodec, *bitrate, *fps, *hwaccel, *includeSubs, *subs, *verbose)
	case "kdenlive":
		runKdenlive(cfg, tl, *out, *verbose)
	default:
		beckyio.Fatalf("unknown --format %q (want mp4 or kdenlive)", *format)
	}
}

// runMP4 renders the timeline through auto-editor, then optionally burns subs.
func runMP4(cfg config.Config, tl timeline, tlPath, out, vcodec, bitrate, fps string, hwaccel, includeSubs bool, subs string, verbose bool) {
	outputPath := out
	if outputPath == "" {
		outputPath = defaultOutput(tl.Source, ".mp4")
	}
	outputPath = mustAbs(outputPath)

	args := []string{mustAbs(tlPath), "-o", outputPath, "--progress", "none", "-c:v", vcodec}
	if bitrate != "" {
		args = append(args, "-b:v", bitrate)
	}
	if fps != "" {
		args = append(args, "--frame-rate", fps)
	}

	beckyio.Logf(verbose, "rendering timeline with %s (bitrate=%s)...", vcodec, bitrate)
	if err := runStream(verbose, cfg.AutoEditor, args...); err != nil {
		beckyio.Fatalf("render failed: %v", err)
	}

	report := map[string]any{
		"input_timeline": mustAbs(tlPath),
		"source":         tl.Source,
		"output":         outputPath,
		"format":         "mp4",
		"codec":          vcodec,
		"bitrate":        bitrate,
		"hwaccel":        hwaccel,
		"chunks":         len(tl.Chunks),
		"rendered":       true,
	}

	// --include-subs: auto-editor has no burn-in option, so do a best-effort
	// ffmpeg subtitles pass on the rendered MP4. Degrade gracefully on failure.
	if includeSubs {
		burned, note := burnSubs(cfg, outputPath, subs, vcodec, verbose)
		report["subs_burned"] = burned
		if note != "" {
			report["subs_note"] = note
		}
	}

	if info, e := mediainfo.Probe(cfg.FFprobe, outputPath); e == nil {
		report["output_duration"] = round3(info.Duration)
		report["output_resolution"] = info.Resolution()
	}
	if fi, e := os.Stat(outputPath); e == nil {
		report["output_mb"] = round3(float64(fi.Size()) / (1024 * 1024))
	}
	beckyio.PrintJSON(report)
}

// runKdenlive produces a valid .kdenlive project from the timeline's source
// video via auto-editor's own exporter, using Jordan's locked margin.
func runKdenlive(cfg config.Config, tl timeline, out string, verbose bool) {
	outputPath := out
	if outputPath == "" {
		outputPath = defaultOutput(tl.Source, ".kdenlive")
	}
	outputPath = mustAbs(outputPath)

	beckyio.Logf(verbose, "auto-editor kdenlive export (margin=%s)...", lockedMargin)
	if err := runStream(verbose, cfg.AutoEditor, mustAbs(tl.Source),
		"--edit", "audio", "--margin", lockedMargin, "--export", "kdenlive",
		"-o", outputPath, "--progress", "none"); err != nil {
		beckyio.Fatalf("kdenlive export failed: %v", err)
	}

	// Confirm the result is parseable XML before reporting success.
	valid, note := validateXML(outputPath)
	report := map[string]any{
		"input_timeline": mustAbs(tl.Source) + " (timeline source)",
		"source":         tl.Source,
		"output":         outputPath,
		"format":         "kdenlive",
		"margin":         lockedMargin,
		"chunks":         len(tl.Chunks),
		"rendered":       true,
		"valid_xml":      valid,
		"note": "kdenlive project generated from source via auto-editor " +
			"(--export kdenlive); chunk modifications are auto-editor's own audio " +
			"detection, not the timeline JSON's cut spans",
	}
	if note != "" {
		report["xml_note"] = note
	}
	if fi, e := os.Stat(outputPath); e == nil {
		report["output_mb"] = round3(float64(fi.Size()) / (1024 * 1024))
	}
	beckyio.PrintJSON(report)
}

// burnSubs runs a best-effort ffmpeg subtitles filter pass to hard-burn an SRT.
// It re-encodes in place to a temp file, then swaps. Returns whether subs were
// burned and a human-readable note on any degradation.
func burnSubs(cfg config.Config, video, subs, vcodec string, verbose bool) (bool, string) {
	if subs == "" {
		return false, "skipped: --include-subs given without --subs <path>"
	}
	if _, err := os.Stat(subs); err != nil {
		return false, fmt.Sprintf("skipped: subtitle file not found: %s", subs)
	}
	tmp := video + ".subs.tmp" + filepath.Ext(video)
	// ffmpeg's subtitles filter needs a forward-slash, escaped-colon path on
	// Windows (drive letter colon must be escaped inside the filter graph).
	filterPath := escapeSubsPath(subs)
	beckyio.Logf(verbose, "burning subtitles from %s...", subs)
	args := []string{"-y", "-i", video,
		"-vf", "subtitles=" + filterPath,
		"-c:v", vcodec, "-c:a", "copy", "-loglevel", "error", tmp}
	if err := runStream(verbose, cfg.FFmpeg, args...); err != nil {
		os.Remove(tmp)
		return false, fmt.Sprintf("skipped: ffmpeg subtitles pass failed: %v", err)
	}
	if err := os.Rename(tmp, video); err != nil {
		// Fall back to remove-then-rename if the OS won't overwrite.
		if rmErr := os.Remove(video); rmErr == nil {
			if e := os.Rename(tmp, video); e == nil {
				return true, ""
			}
		}
		os.Remove(tmp)
		return false, fmt.Sprintf("skipped: could not replace output with burned copy: %v", err)
	}
	return true, ""
}

// escapeSubsPath converts a Windows path into the form ffmpeg's subtitles
// filter expects: forward slashes with the drive-letter colon escaped.
func escapeSubsPath(p string) string {
	p = mustAbs(p)
	p = strings.ReplaceAll(p, "\\", "/")
	p = strings.ReplaceAll(p, ":", "\\:")
	return "'" + p + "'"
}

// validateXML confirms a file parses as XML (kdenlive projects are MLT XML).
func validateXML(path string) (bool, string) {
	f, err := os.Open(path)
	if err != nil {
		return false, err.Error()
	}
	defer f.Close()
	dec := xml.NewDecoder(f)
	for {
		_, e := dec.Token()
		if e == io.EOF {
			return true, ""
		}
		if e != nil {
			return false, e.Error()
		}
	}
}

// loadTimeline reads and validates a becky-cut v1 timeline JSON.
func loadTimeline(path string) (timeline, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return timeline{}, fmt.Errorf("read timeline: %w", err)
	}
	var tl timeline
	if err := json.Unmarshal(data, &tl); err != nil {
		return timeline{}, fmt.Errorf("parse timeline JSON: %w", err)
	}
	if tl.Source == "" {
		return timeline{}, fmt.Errorf("timeline missing \"source\" field")
	}
	if len(tl.Chunks) == 0 {
		return timeline{}, fmt.Errorf("timeline has no chunks")
	}
	return tl, nil
}

// parsePositional pulls the single positional timeline path, then re-parses the
// remaining flags (same pattern as becky-cut).
func parsePositional() string {
	flag.Parse()
	rest := flag.Args()
	if len(rest) == 0 {
		return ""
	}
	first := rest[0]
	if len(rest) > 1 {
		_ = flag.CommandLine.Parse(rest[1:])
	}
	return first
}

// defaultOutput builds <source-stem>_edited<ext> next to the source video.
func defaultOutput(source, ext string) string {
	stem := strings.TrimSuffix(filepath.Base(source), filepath.Ext(source))
	return filepath.Join(filepath.Dir(source), stem+"_edited"+ext)
}

func runStream(verbose bool, name string, args ...string) error {
	cmd := exec.Command(name, args...)
	var errBuf strings.Builder
	if verbose {
		cmd.Stderr = io.MultiWriter(os.Stderr, &errBuf)
	} else {
		cmd.Stderr = &errBuf
	}
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s: %v\n%s", filepath.Base(name), err, tail(errBuf.String()))
	}
	return nil
}

func mustAbs(p string) string {
	if abs, err := filepath.Abs(p); err == nil {
		return abs
	}
	return p
}

func round3(f float64) float64 { return float64(int(f*1000+0.5)) / 1000 }

func tail(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 800 {
		return s[len(s)-800:]
	}
	return s
}
