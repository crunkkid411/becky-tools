// becky-location — corpus-level room-fingerprint report: "were these clips
// filmed in the same place?" Feed it N clips (files and/or folders); it returns
// the set of DISTINCT rooms, a per-clip room assignment with confidence, and a
// same/different-dwelling verdict — corroborated, then concluded.
//
// SPLIT (SPEC-BECKY-LOCATION.md §5): the clustering/verdict ENGINE (internal/
// location) is pure-Go and fully cloud-tested over abstract fingerprints. This
// CLI's keyframe extraction + the optional cv2 feature signal are the LOCAL
// half — they need ffmpeg/cv2 on real footage and degrade-never-crash when
// absent (the cloud reality), still emitting a valid report.
//
// Conventions (README): JSON to stdout, diagnostics to stderr (silent without
// --verbose), exit 0 on success, sources only READ.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"becky-go/internal/config"
	"becky-go/internal/location"
)

type sampleConfig struct {
	ffmpeg, ffprobe, exiftool string
	interval                  float64
	mask                      location.CropMask
	metadata                  bool
	framesDir                 string
	dedupBits                 int
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr *os.File) int {
	var (
		interval    = 2.0
		cropSpec    = "talking-head"
		fpMethod    = "phash"
		threshold   = location.DefaultThresholds().DecorHamming
		colorThresh = location.DefaultThresholds().ColorChi2
		minSignals  = 2
		metadata    = true
		framesDir   = "location-out"
		outFile     = ""
		summary     = false
		verbose     = false
		pairs       [][2]int
		positional  []string
	)

	// Minimal flag parser (handles --flag value and --flag=value; flags may
	// appear before or after positionals, mirroring becky-scout).
	i := 0
	for i < len(args) {
		a := args[i]
		next := func() (string, bool) {
			if eq := strings.IndexByte(a, '='); eq >= 0 {
				return a[eq+1:], true
			}
			if i+1 < len(args) {
				i++
				return args[i], true
			}
			return "", false
		}
		switch {
		case a == "--summary":
			summary = true
		case a == "--verbose":
			verbose = true
		case a == "--metadata":
			metadata = true
		case a == "--no-metadata":
			metadata = false
		case strings.HasPrefix(a, "--interval"):
			if v, ok := next(); ok {
				if f, err := strconv.ParseFloat(v, 64); err == nil {
					interval = f
				}
			}
		case strings.HasPrefix(a, "--crop"):
			if v, ok := next(); ok {
				cropSpec = v
			}
		case strings.HasPrefix(a, "--fingerprint"):
			if v, ok := next(); ok {
				fpMethod = v
			}
		case strings.HasPrefix(a, "--threshold"):
			if v, ok := next(); ok {
				if n, err := strconv.Atoi(v); err == nil {
					threshold = n
				}
			}
		case strings.HasPrefix(a, "--color-threshold"):
			if v, ok := next(); ok {
				if f, err := strconv.ParseFloat(v, 64); err == nil {
					colorThresh = f
				}
			}
		case strings.HasPrefix(a, "--min-signals"):
			if v, ok := next(); ok {
				if n, err := strconv.Atoi(v); err == nil {
					minSignals = n
				}
			}
		case strings.HasPrefix(a, "--frames-dir"):
			if v, ok := next(); ok {
				framesDir = v
			}
		case strings.HasPrefix(a, "--output"):
			if v, ok := next(); ok {
				outFile = v
			}
		case strings.HasPrefix(a, "--pair"):
			if v, ok := next(); ok {
				if p, ok := parsePair(v); ok {
					pairs = append(pairs, p)
				}
			}
		case a == "-h" || a == "--help":
			fmt.Fprint(stderr, usage)
			return 0
		case strings.HasPrefix(a, "-"):
			fmt.Fprintf(stderr, "unknown flag %q\n", a)
		default:
			positional = append(positional, a)
		}
		i++
	}

	if len(positional) == 0 {
		fmt.Fprint(stderr, usage)
		return 2
	}

	cfg := config.Load()
	mask, cropOK := location.ParseCrop(cropSpec)
	if !cropOK && verbose {
		fmt.Fprintf(stderr, "note: malformed --crop %q, using talking-head default\n", cropSpec)
	}

	// Choose the fingerprinter (degrade-never-crash: features stub falls back).
	var fp location.Fingerprinter
	method := "phash"
	switch fpMethod {
	case "features", "auto":
		ff := newFeatureFingerprinter()
		if ff.Available() {
			fp = ff
			method = "features+phash"
		} else {
			fp = location.NewPhashFingerprinter()
			method = "phash"
			if verbose {
				fmt.Fprintln(stderr, "note: feature helper unavailable; using phash (lower certainty)")
			}
		}
	default:
		fp = location.NewPhashFingerprinter()
	}

	sc := sampleConfig{
		ffmpeg:    cfg.FFmpeg,
		ffprobe:   cfg.FFprobe,
		interval:  interval,
		mask:      mask,
		metadata:  metadata,
		framesDir: framesDir,
		dedupBits: 4,
	}

	clipPaths := expandInputs(positional)
	if len(clipPaths) == 0 {
		fmt.Fprintln(stderr, "no video files found in the given inputs")
		return 0
	}

	clips := make([]location.Clip, 0, len(clipPaths))
	for idx, p := range clipPaths {
		if verbose {
			fmt.Fprintf(stderr, "[%d/%d] %s\n", idx+1, len(clipPaths), p)
		}
		clips = append(clips, sampleClip(idx, p, sc, fp))
	}

	thr := location.Thresholds{
		DecorHamming: threshold,
		ColorChi2:    colorThresh,
		FeatureDist:  location.DefaultThresholds().FeatureDist,
		MinSignals:   minSignals,
	}
	cr := location.Cluster(clips, thr)
	dwellings, verdict := location.GroupDwellings(clips, cr, thr, location.DefaultDwellingParams())

	report := buildReport(clips, cr, dwellings, verdict, method, mask.Name, pairs, nil)

	if summary {
		fmt.Fprint(stdout, renderSummary(report))
		return 0
	}

	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		fmt.Fprintf(stderr, "encode report: %v\n", err)
		return 1
	}
	if outFile != "" {
		if err := os.WriteFile(outFile, data, 0o644); err != nil {
			fmt.Fprintf(stderr, "write %s: %v\n", outFile, err)
			return 1
		}
		if verbose {
			fmt.Fprintf(stderr, "wrote %s\n", outFile)
		}
		return 0
	}
	fmt.Fprintln(stdout, string(data))
	return 0
}

// expandInputs turns the positional args (files and/or folders) into a sorted,
// deduplicated list of video file paths.
func expandInputs(inputs []string) []string {
	seen := map[string]bool{}
	var out []string
	add := func(p string) {
		if !seen[p] && isVideo(p) {
			seen[p] = true
			out = append(out, p)
		}
	}
	for _, in := range inputs {
		fi, err := os.Stat(in)
		if err != nil {
			continue
		}
		if fi.IsDir() {
			entries, _ := os.ReadDir(in)
			for _, e := range entries {
				if !e.IsDir() {
					add(filepath.Join(in, e.Name()))
				}
			}
		} else {
			add(in)
		}
	}
	return out
}

func isVideo(p string) bool {
	switch strings.ToLower(filepath.Ext(p)) {
	case ".mp4", ".mov", ".mkv", ".avi", ".webm", ".m4v", ".wmv", ".flv", ".mpg", ".mpeg", ".3gp":
		return true
	}
	return false
}

func parsePair(s string) ([2]int, bool) {
	parts := strings.Split(s, ",")
	if len(parts) != 2 {
		return [2]int{}, false
	}
	a, e1 := strconv.Atoi(strings.TrimSpace(parts[0]))
	b, e2 := strconv.Atoi(strings.TrimSpace(parts[1]))
	if e1 != nil || e2 != nil {
		return [2]int{}, false
	}
	return [2]int{a, b}, true
}

const usage = `becky-location — were these clips filmed in the same place?

Usage:
  becky-location <video-or-folder...> [options]

Options:
  --interval <sec>        seconds between keyframe samples per clip   (default 2.0)
  --crop <preset|spec>    decor crop mask: talking-head|top|full|"T,L,R,B"  (default talking-head)
  --fingerprint <m>       phash | features | auto                     (default phash)
  --threshold <bits>      same-room aHash Hamming cutoff (0-64)        (default 10)
  --color-threshold <f>   same-room color chi-square cutoff (0-1)      (default 0.25)
  --min-signals <n>       independent signals required to MERGE (1-3)  (default 2)
  --no-metadata           do NOT use EXIF/QuickTime GPS + capture-time
  --pair A,B              restrict pair verdicts to clip indices (repeatable)
  --frames-dir <path>     where extracted keyframes go                (default location-out/)
  --output <file>         write JSON here instead of stdout
  --summary               print a concise human block instead of JSON
  --verbose               progress to stderr

JSON to stdout; one or more videos and/or folders as input. Sources are only read.
`
