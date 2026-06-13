// becky-pipeline — thin orchestrator that runs the becky forensic chain over a
// single video or a folder of videos with one command, resumable, emitting a
// JSON manifest.
//
//	becky-pipeline <video|folder> [--out <dir>] [--kb <dir>] [--bin <dir>]
//	               [--steps transcribe,diarize,events,osint,embed,identify]
//	               [--db <path>] [--resume] [--force] [--verbose]
//
// For each input video it runs the documented chain, writing each step's JSON
// into <out>/<videostem>/ (transcript.json, diarized.json, events.json,
// osint-manifest.json + osint/ frames, embed.json, identify.json). It is a thin
// driver: it shells out to the existing becky-<tool>.exe binaries (resolved from
// --bin, default = the directory of the running becky-pipeline.exe) and never
// re-implements their logic.
//
// Resumable/idempotent: a step whose output already exists is SKIPPED unless
// --force. --resume continues a partially-done run (same skip behaviour, made
// explicit). Each becky tool is deterministic, so re-runs are safe.
//
// Graceful per-step: if a step fails (embed when the server is down, identify
// with no --kb, etc.) the failure is recorded in the manifest and the pipeline
// CONTINUES the other steps. Source videos are never modified.
//
// A top-level manifest JSON goes to stdout (and <out>/manifest.json); all
// diagnostics go to stderr. Exit 0 on success.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"becky-go/internal/beckyio"
	"becky-go/internal/config"
)

// videoExts is the set of extensions treated as input videos when a folder is
// given. Audio-only inputs (.wav/.mp3) are accepted as a direct file argument
// since several steps run on audio, but folder scanning sticks to video.
var videoExts = map[string]bool{
	".mp4": true, ".mov": true, ".mkv": true, ".avi": true,
	".webm": true, ".m4v": true, ".mpg": true, ".mpeg": true,
}

func main() {
	out := flag.String("out", "pipeline-out", "output root directory")
	kb := flag.String("kb", "", "knowledge-base dir for the identify step (optional)")
	binDir := flag.String("bin", "", "directory holding becky-*.exe (default: dir of this binary)")
	steps := flag.String("steps", "", "comma-separated steps (default: transcribe,diarize,events,osint)")
	db := flag.String("db", "", "SQLite db path for embed (default: <video-out>/forensic.db)")
	resume := flag.Bool("resume", false, "continue a partially-done run (skip completed steps)")
	force := flag.Bool("force", false, "re-run every step even if its output exists")
	forceTranscribe := flag.Bool("force-transcribe", false, "always run Parakeet even when a subtitle sidecar exists (evidence-grade verbatim)")
	lang := flag.String("lang", "en", "transcript language tag recorded in sidecar transcripts")
	verbose := flag.Bool("verbose", false, "show progress on stderr")

	input := parsePositional()
	if input == "" {
		beckyio.Fatalf("usage: becky-pipeline <video|folder> [options]")
	}
	if _, err := os.Stat(input); err != nil {
		beckyio.Fatalf("input not found: %s", input)
	}

	plannedSteps, err := parseSteps(*steps)
	if err != nil {
		beckyio.Fatalf("%v", err)
	}

	resolvedBin, err := resolveBinDir(*binDir)
	if err != nil {
		beckyio.Fatalf("%v", err)
	}
	beckyio.Logf(*verbose, "using becky binaries from: %s", resolvedBin)

	// --resume is an explicit synonym for the default skip-on-existing behaviour;
	// note it for the operator but it does not change the plan (force overrides).
	if *resume && *force {
		beckyio.Logf(*verbose, "note: --resume and --force both set; --force wins (everything re-runs)")
	}

	videos, err := discoverVideos(input)
	if err != nil {
		beckyio.Fatalf("%v", err)
	}
	if len(videos) == 0 {
		beckyio.Fatalf("no input videos found under: %s", input)
	}
	beckyio.Logf(*verbose, "discovered %d input(s)", len(videos))

	if err := os.MkdirAll(*out, 0o755); err != nil {
		beckyio.Fatalf("create output root: %v", err)
	}

	runner := &toolRunner{
		binDir:          resolvedBin,
		verbose:         *verbose,
		cfg:             config.Load(),
		forceTranscribe: *forceTranscribe,
		lang:            *lang,
	}
	manifest := Manifest{
		Tool:      "becky-pipeline v1.0.0",
		StartedAt: time.Now().UTC().Format(time.RFC3339),
		OutRoot:   absOr(*out),
		BinDir:    resolvedBin,
		Steps:     append([]string(nil), plannedSteps...),
		KB:        *kb,
		Videos:    []VideoResult{},
	}

	for _, video := range videos {
		vr := processVideo(runner, video, *out, *kb, *db, plannedSteps, *force, *verbose)
		manifest.Videos = append(manifest.Videos, vr)
	}

	manifest.FinishedAt = time.Now().UTC().Format(time.RFC3339)
	manifest.Totals = computeTotals(manifest.Videos)

	// Persist manifest.json next to outputs, then emit to stdout.
	manifestPath := filepath.Join(*out, "manifest.json")
	if err := writeJSONFile(manifestPath, manifest); err != nil {
		beckyio.Logf(true, "warning: failed to write %s: %v", manifestPath, err)
	} else {
		beckyio.Logf(*verbose, "wrote manifest: %s", manifestPath)
	}
	beckyio.PrintJSON(manifest)
}

// processVideo runs the planned chain for one video and returns its result row.
func processVideo(r *toolRunner, video, outRoot, kb, db string, steps []string, force, verbose bool) VideoResult {
	stem := stemOf(video)
	videoDir := filepath.Join(outRoot, stem)
	vr := VideoResult{
		Input:  absOr(video),
		Stem:   stem,
		OutDir: absOr(videoDir),
		Steps:  []StepResult{},
		Status: "ok",
	}

	if err := os.MkdirAll(videoDir, 0o755); err != nil {
		vr.Status = "failed"
		vr.Error = fmt.Sprintf("create output dir: %v", err)
		return vr
	}

	paths := newStepPaths(videoDir, db)
	plan := planSteps(steps, paths, force, osStat)

	beckyio.Logf(verbose, "=== %s -> %s ===", video, videoDir)
	for _, ps := range plan {
		sr := r.runStep(ps, video, kb, paths, force)
		vr.Steps = append(vr.Steps, sr)
		if sr.Status == "failed" {
			vr.Status = "partial" // one bad optional step != whole-video failure
		}
	}
	return vr
}

// osStat is the production statFn used by the planner: reports existence and
// non-emptiness of a marker path.
func osStat(path string) (exists bool, nonEmpty bool) {
	fi, err := os.Stat(path)
	if err != nil {
		return false, false
	}
	return true, fi.Size() > 0
}

// discoverVideos returns the list of input videos. A file yields itself; a
// directory yields its video-extension children (non-recursive, sorted).
func discoverVideos(input string) ([]string, error) {
	fi, err := os.Stat(input)
	if err != nil {
		return nil, fmt.Errorf("stat input: %w", err)
	}
	if !fi.IsDir() {
		return []string{input}, nil
	}
	entries, err := os.ReadDir(input)
	if err != nil {
		return nil, fmt.Errorf("read dir: %w", err)
	}
	var videos []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		ext := strings.ToLower(filepath.Ext(e.Name()))
		if videoExts[ext] {
			videos = append(videos, filepath.Join(input, e.Name()))
		}
	}
	sort.Strings(videos)
	return videos, nil
}

// resolveBinDir returns the directory holding the becky-*.exe binaries. If
// --bin is set it is used (after validation); otherwise it defaults to the
// directory of the running becky-pipeline executable (becky-go/bin).
func resolveBinDir(override string) (string, error) {
	if override != "" {
		fi, err := os.Stat(override)
		if err != nil || !fi.IsDir() {
			return "", fmt.Errorf("--bin not a directory: %s", override)
		}
		return absOr(override), nil
	}
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("resolve own path (pass --bin): %w", err)
	}
	return filepath.Dir(exe), nil
}

// stemOf returns the file name without its extension (the per-video subdir name).
func stemOf(path string) string {
	base := filepath.Base(path)
	return strings.TrimSuffix(base, filepath.Ext(base))
}

// absOr returns the absolute form of p, or p unchanged if that fails.
func absOr(p string) string {
	if abs, err := filepath.Abs(p); err == nil {
		return abs
	}
	return p
}

// parsePositional mirrors the other becky tools: positional input first, then
// flags after it (so `becky-pipeline video.mp4 --out x` works).
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
