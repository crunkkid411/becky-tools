// becky-captions — the deterministic caption-acquisition decision tool. Given a
// downloaded video, it decides whether a TRUSTWORTHY official transcript already
// exists (or can be cheaply fetched from YouTube) or whether local ASR is needed,
// and emits that decision as JSON. It is the step becky-clip runs BEFORE local
// transcription (Jordan's hard requirement: prefer the YouTube auto-srt, drop it
// beside the video with the same naming scheme, and fall back to local ASR only
// when there is no official transcript OR the official one is short because the
// stream was YouTube-edited).
//
// Usage:
//
//	becky-captions <video> [--json] [--offline]
//
//	--json     print the decision as JSON (default; flag kept for explicitness +
//	           sibling-tool symmetry)
//	--offline  do NOT contact YouTube — decide using only a local official srt
//	           that is already next to the video (check-only)
//	-v         verbose progress to stderr (id, probe, fetch steps)
//
// JSON (the Decision) goes to stdout; diagnostics go to stderr; exit 0 on a
// successful analysis (including a valid "local_needed" outcome), non-zero only on
// a bad invocation. Offline + deterministic EXCEPT the single, explicit yt-dlp
// fetch (skipped by --offline), exactly like becky-scout. The video bytes are
// never modified; at most an official <stem>.en.srt is written beside the source.
package main

import (
	"fmt"
	"os"

	"becky-go/internal/beckyio"
	"becky-go/internal/captions"
)

func main() {
	video, opt, verbose, err := parseArgs(os.Args[1:])
	if err != nil {
		beckyio.Fatalf("%v\n\nusage: becky-captions <video> [--json] [--offline]", err)
	}
	if verbose {
		opt.Logf = func(format string, args ...any) {
			fmt.Fprintf(os.Stderr, "[becky-captions] "+format+"\n", args...)
		}
	}
	d, err := captions.Analyze(video, opt)
	if err != nil {
		// A truly broken call (empty path). An "expected" outcome is encoded in d,
		// not returned as an error, so we only reach here on a usage-level failure.
		beckyio.Fatalf("%v", err)
	}
	beckyio.PrintJSON(d)
}

// parseArgs parses the CLI: exactly one positional video path plus the optional
// flags. Flags may appear before or after the path (tolerant, like the other
// tools). Returns the video, the captions.Options, the verbose flag, or an error.
func parseArgs(argv []string) (video string, opt captions.Options, verbose bool, err error) {
	for _, a := range argv {
		switch a {
		case "--offline":
			opt.Offline = true
		case "--json":
			// JSON is the only output; accepted for explicitness/symmetry.
		case "-v", "--verbose":
			verbose = true
		case "-h", "--help", "help":
			return "", opt, false, fmt.Errorf("show help")
		default:
			if len(a) > 0 && a[0] == '-' {
				return "", opt, false, fmt.Errorf("unknown flag %q", a)
			}
			if video != "" {
				return "", opt, false, fmt.Errorf("expected one video path, got a second: %q", a)
			}
			video = a
		}
	}
	if video == "" {
		return "", opt, false, fmt.Errorf("no video path given")
	}
	return video, opt, verbose, nil
}
