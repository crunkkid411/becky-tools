// becky-framematch — FRAME-MATCHING visual-comparison exhibit builder. Given TWO
// sources (two videos, or a video + a folder of reference images) it surfaces
// candidate SAME-LOCATION / same-object frame pairs and produces a labeled
// side-by-side comparison exhibit for HUMAN review.
//
//	becky-framematch <sourceA> <sourceB> [options]
//
// The loop (per FORENSIC-OUTPUT-PHILOSOPHY.md part 2 — iterative, not one-shot):
//  1. Sample + perceptual-hash frames from BOTH sources (reuse osintexport).
//  2. Pair frames across sources by LOW Hamming distance (--threshold), ranked.
//  3. Optionally apply HONEST, logged brightness/contrast/gamma/saturation
//     (ffmpeg eq) to a COPY of a frame to reveal a feature a bad exposure hides.
//  4. Lay out one clear side-by-side comparison per pair (image + HTML exhibit),
//     each labeled with source file, timestamp, and a "what to look for" line.
//  5. Emit a re-runnable JSON manifest. Adjust --threshold/--interval/enhance
//     levels and run again — it is a loop.
//
// Options:
//
//	--output-dir <path>   output directory (default: framematch-out/)
//	--interval <sec>      seconds between video samples (default: 1.0)
//	--fps <n>             samples per second (alternative to --interval)
//	--threshold <bits>    max Hamming distance for a candidate pair (default: 10)
//	--max-pairs <n>       cap on emitted pairs (default: 12; 0 = no cap)
//	--enhance             apply the brightness/contrast/gamma/saturation below
//	--brightness <f>      eq brightness -1..1 (default 0 = none)
//	--contrast <f>        eq contrast (default 1 = none)
//	--gamma <f>           eq gamma (default 1 = none)
//	--saturation <f>      eq saturation (default 1 = none)
//	--enhance-side a|b|both  which side to enhance (default: both)
//	--no-images           skip the per-pair PNG (HTML exhibit + manifest only)
//	--output <file>       write the manifest JSON here instead of stdout
//	--verbose             show progress on stderr
//
// JSON in/JSON out, offline, exit-coded. It only READS the sources; all frames
// and edits are COPIES. Candidate-not-conclusion: it never declares "same place".
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"becky-go/internal/beckyio"
	"becky-go/internal/config"
)

const toolVersion = "becky-framematch v1.0.0"

func main() {
	outputDir := flag.String("output-dir", "framematch-out", "output directory")
	interval := flag.Float64("interval", 1.0, "seconds between video frame samples")
	fps := flag.Float64("fps", 0, "samples per second (alternative to --interval)")
	threshold := flag.Int("threshold", 10, "max Hamming distance for a candidate pair (0-64)")
	maxPairs := flag.Int("max-pairs", 12, "cap on emitted candidate pairs (0 = no cap)")
	enhance := flag.Bool("enhance", false, "apply the honest brightness/contrast/gamma/saturation enhancement")
	brightness := flag.Float64("brightness", 0, "eq brightness -1..1 (0 = none)")
	contrast := flag.Float64("contrast", 1, "eq contrast (1 = none)")
	gamma := flag.Float64("gamma", 1, "eq gamma (1 = none)")
	saturation := flag.Float64("saturation", 1, "eq saturation (1 = none)")
	enhanceSide := flag.String("enhance-side", "both", "which side to enhance: a, b, both")
	roiMode := flag.String("roi", "band", "region hashed for matching: band | corners | full")
	roiTop := flag.Float64("roi-top", 0.0, "ROI top edge as a fraction of height [0,1]")
	roiHeight := flag.Float64("roi-height", 0.35, "ROI height as a fraction of height (0,1]")
	roiLeft := flag.Float64("roi-left", 0.0, "ROI left edge as a fraction of width [0,1]")
	roiWidth := flag.Float64("roi-width", 1.0, "ROI width as a fraction of width (0,1]")
	roiThreshold := flag.Int("roi-threshold", 8, "max ROI-aHash Hamming for an 'agree' (0-64)")
	keypoints := flag.Bool("keypoints", false, "enable static-decor keypoint corroboration (pure-Go)")
	minInliers := flag.Int("min-inliers", 12, "keypoint inliers required for an 'agree'")
	noImages := flag.Bool("no-images", false, "skip per-pair comparison PNGs (HTML + manifest only)")
	output := flag.String("output", "", "write the manifest JSON here instead of stdout")
	verbose := flag.Bool("verbose", false, "show progress on stderr")

	srcA, srcB := parsePositional()
	if srcA == "" || srcB == "" {
		beckyio.Fatalf("usage: becky-framematch <sourceA> <sourceB> [options]")
	}
	if _, err := os.Stat(srcA); err != nil {
		beckyio.Fatalf("source A not found: %s", srcA)
	}
	if _, err := os.Stat(srcB); err != nil {
		beckyio.Fatalf("source B not found: %s", srcB)
	}
	if *threshold < 0 || *threshold > 64 {
		beckyio.Fatalf("--threshold must be 0-64 (Hamming bits), got %d", *threshold)
	}
	side := strings.ToLower(*enhanceSide)
	if side != "a" && side != "b" && side != "both" {
		beckyio.Fatalf("--enhance-side must be a, b, or both")
	}
	roiCfg, rerr := buildROIConfig(strings.ToLower(*roiMode), *roiTop, *roiHeight, *roiLeft, *roiWidth,
		*roiThreshold, *keypoints, *minInliers)
	if rerr != nil {
		beckyio.Fatalf("%v", rerr)
	}

	cfg := config.Load()
	if err := os.MkdirAll(*outputDir, 0o755); err != nil {
		beckyio.Fatalf("create output dir: %v", err)
	}

	// 1. Sample + hash both sources.
	beckyio.Logf(*verbose, "sampling source A: %s", srcA)
	srcInfoA, framesA, err := sampleSource(cfg, srcA, "A", *outputDir, *interval, *fps, roiCfg, *verbose)
	if err != nil {
		beckyio.Fatalf("%v", err)
	}
	beckyio.Logf(*verbose, "sampling source B: %s", srcB)
	srcInfoB, framesB, err := sampleSource(cfg, srcB, "B", *outputDir, *interval, *fps, roiCfg, *verbose)
	if err != nil {
		beckyio.Fatalf("%v", err)
	}
	beckyio.Logf(*verbose, "sampled %d frame(s) from A, %d from B", len(framesA), len(framesB))

	// 2. Pair across sources, ranked on the ROI-aHash (primary signal), with the
	//    whole-frame hash kept as a weak/provenance signal and a corroborated
	//    room call computed per pair.
	pairs := pairFrames(framesA, framesB, *threshold, *maxPairs, roiCfg, cfg)
	beckyio.Logf(*verbose, "%d candidate pair(s) within threshold %d", len(pairs), *threshold)

	opts := enhanceOpts{
		brightness: *brightness,
		contrast:   *contrast,
		gamma:      *gamma,
		saturation: *saturation,
	}
	enhanceActive := *enhance && opts.active()
	if *enhance && !opts.active() {
		beckyio.Logf(true, "warning: --enhance set but all adjustments are neutral (no change applied)")
	}

	// 3 + 4. Per-pair: optional honest enhance, then the side-by-side image.
	for i := range pairs {
		aImg := filepath.FromSlash(pairs[i].A.Path)
		bImg := filepath.FromSlash(pairs[i].B.Path)

		if enhanceActive {
			note := "honest brightness/contrast/gamma/saturation only; reveals detail without altering content"
			if side == "a" || side == "both" {
				if e, eerr := applyEnhance(cfg, "A", aImg, opts, note); eerr == nil {
					pairs[i].Enhancements = append(pairs[i].Enhancements, e)
					aImg = filepath.FromSlash(e.OutputPath)
				} else {
					beckyio.Logf(true, "warning: enhance A pair %d failed: %v", pairs[i].Rank, eerr)
				}
			}
			if side == "b" || side == "both" {
				if e, eerr := applyEnhance(cfg, "B", bImg, opts, note); eerr == nil {
					pairs[i].Enhancements = append(pairs[i].Enhancements, e)
					bImg = filepath.FromSlash(e.OutputPath)
				} else {
					beckyio.Logf(true, "warning: enhance B pair %d failed: %v", pairs[i].Rank, eerr)
				}
			}
		}

		if !*noImages {
			nameA := baseName(srcInfoA.Path, "source A")
			nameB := baseName(srcInfoB.Path, "source B")
			cmp, cerr := buildComparison(cfg, *outputDir, pairs[i], aImg, bImg, nameA, nameB, *verbose)
			if cerr != nil {
				// A failed composite must not lose the pair: the HTML falls back
				// to the two raw frames side by side.
				beckyio.Logf(true, "warning: comparison image pair %d failed: %v", pairs[i].Rank, cerr)
			} else {
				pairs[i].Comparison = cmp
			}
		}
	}

	// 5. Build the manifest + HTML exhibit.
	htmlPath := filepath.Join(*outputDir, "exhibit.html")
	manifest := Manifest{
		Tool:           toolVersion,
		GeneratedAt:    time.Now().UTC().Format(time.RFC3339),
		OutputDir:      filepath.ToSlash(*outputDir),
		ExhibitHTML:    filepath.ToSlash(htmlPath),
		Interval:       *interval,
		FPS:            *fps,
		Threshold:      *threshold,
		ROIMode:        roiCfg.mode,
		ROISpec:        roiCfg.spec(),
		ROIThreshold:   roiCfg.roiThreshold,
		KeypointsOn:    roiCfg.keypoints,
		MinInliers:     roiCfg.minInliers,
		MaxPairs:       *maxPairs,
		EnhanceApplied: enhanceActive,
		SourceA:        srcInfoA,
		SourceB:        srcInfoB,
		PairCount:      len(pairs),
		Pairs:          pairs,
		Notes:          ManifestNote,
	}
	if manifest.Pairs == nil {
		manifest.Pairs = []Pair{}
	}

	if err := writeExhibitHTML(*outputDir, htmlPath, manifest); err != nil {
		beckyio.Fatalf("%v", err)
	}
	beckyio.Logf(*verbose, "wrote exhibit: %s", htmlPath)

	// Always persist the manifest next to the exhibit so the page is rebuildable.
	manifestPath := filepath.Join(*outputDir, "manifest.json")
	if err := writeJSONFile(manifestPath, manifest); err != nil {
		beckyio.Fatalf("%v", err)
	}

	if err := emit(manifest, *output); err != nil {
		beckyio.Fatalf("%v", err)
	}
}

// parsePositional pulls the two leading positional source paths, then re-parses
// the remaining args for flags (mirrors the becky-osint/becky-events pattern but
// for two positionals).
func parsePositional() (string, string) {
	flag.Parse()
	rest := flag.Args()
	if len(rest) < 2 {
		if len(rest) == 1 {
			return rest[0], ""
		}
		return "", ""
	}
	a, b := rest[0], rest[1]
	if len(rest) > 2 {
		_ = flag.CommandLine.Parse(rest[2:])
	}
	return a, b
}

// emit prints the manifest to stdout or writes it to outPath.
func emit(m Manifest, outPath string) error {
	if outPath == "" {
		beckyio.PrintJSON(m)
		return nil
	}
	return writeJSONFile(outPath, m)
}

// writeJSONFile writes the manifest as indented JSON with a trailing newline.
func writeJSONFile(path string, m Manifest) error {
	b, err := marshalIndent(m)
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, b, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

// logf is the verbose-gated stderr logger (wraps beckyio.Logf so the per-source
// helpers can stay package-local).
func logf(verbose bool, format string, a ...any) {
	beckyio.Logf(verbose, format, a...)
}

// round3 rounds to 3 decimals for stable JSON timestamps/scores.
func round3(f float64) float64 { return float64(int(f*1000+0.5)) / 1000 }
