// becky-report — deterministic forensic case reporter.
//
// Reads the JSON sidecar files produced by the becky pipeline tools (transcript,
// events, identify, motion) for one clip and emits a structured forensic case
// report: a merged timeline, corroborated identifications, conclusions, and review
// items. Implements "corroborate, then CONCLUDE" from FORENSIC-OUTPUT-PHILOSOPHY.md
// in code — ≥2 independent signals → DOCUMENTED; single-signal → CANDIDATE.
//
// Sidecar inputs can be specified explicitly or auto-discovered from a pipeline
// output directory (the <out>/<stem>/ layout used by becky-pipeline):
//
//	becky-report <pipeline-dir>          # auto-discover from dir/transcript.json etc.
//	becky-report <video>                 # auto-discover sidecars next to the video
//	becky-report --transcript t.json --events e.json --identify i.json
//
// Options:
//
//	--transcript <path>  becky-transcribe JSON
//	--events     <path>  becky-events JSON
//	--identify   <path>  becky-identify JSON
//	--motion     <path>  becky-motion JSON
//	--source     <name>  label for the source (default: inferred from inputs)
//	--format     <mode>  json | markdown | both (default: both)
//	--output     <path>  write the JSON report here (default: stdout)
//	--md-output  <path>  write the markdown report here (default: report.md next to --output)
//	--verbose            progress on stderr
//
// JSON to stdout (or --output), markdown to --md-output; diagnostics to stderr.
// Exit 0 on success. Degrades gracefully (Degraded=true + notes) when sidecars
// are missing or malformed, never panics.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"becky-go/internal/beckyio"
	"becky-go/internal/pathx"
	"becky-go/internal/report"
)

func main() {
	transcriptPath := flag.String("transcript", "", "path to becky-transcribe JSON")
	eventsPath := flag.String("events", "", "path to becky-events JSON")
	identifyPath := flag.String("identify", "", "path to becky-identify JSON")
	motionPath := flag.String("motion", "", "path to becky-motion JSON")
	sourceName := flag.String("source", "", "source label (default: inferred)")
	format := flag.String("format", "both", "output format: json | markdown | both")
	output := flag.String("output", "", "write JSON report here (default: stdout)")
	mdOutput := flag.String("md-output", "", "write markdown report here (default: alongside --output or stdout)")
	verbose := flag.Bool("verbose", false, "show progress on stderr")

	target := parsePositional()
	flag.Parse()

	// Re-parse after flag.Parse() so positional is still there.
	// (parsePositional reads os.Args directly before Parse.)

	*format = strings.ToLower(*format)
	if *format != "json" && *format != "markdown" && *format != "both" {
		beckyio.Fatalf("--format must be json, markdown, or both")
	}

	// If --transcript etc. are not given but a positional arg is, try auto-discovery.
	if target != "" && *transcriptPath == "" && *eventsPath == "" &&
		*identifyPath == "" && *motionPath == "" {

		discovered := discover(target, *verbose)
		if *transcriptPath == "" {
			*transcriptPath = discovered.transcript
		}
		if *eventsPath == "" {
			*eventsPath = discovered.events
		}
		if *identifyPath == "" {
			*identifyPath = discovered.identify
		}
		if *motionPath == "" {
			*motionPath = discovered.motion
		}
		if *sourceName == "" {
			*sourceName = discovered.source
		}
	}

	if *sourceName == "" {
		*sourceName = inferSource(*transcriptPath, *eventsPath, *identifyPath, *motionPath, target)
	}

	beckyio.Logf(*verbose, "building report for %q", *sourceName)
	beckyio.Logf(*verbose, "  transcript : %s", orNone(*transcriptPath))
	beckyio.Logf(*verbose, "  events     : %s", orNone(*eventsPath))
	beckyio.Logf(*verbose, "  identify   : %s", orNone(*identifyPath))
	beckyio.Logf(*verbose, "  motion     : %s", orNone(*motionPath))

	sidecars, loadNotes, err := report.LoadSidecars(
		*transcriptPath, *eventsPath, *identifyPath, *motionPath,
	)
	if err != nil {
		// A hard parse error (file exists but is malformed JSON) is a fatal.
		beckyio.Fatalf("load sidecars: %v", err)
	}

	rep := report.Build(sidecars, *sourceName)
	rep.Notes = append(loadNotes, rep.Notes...)

	// Write JSON.
	if *format == "json" || *format == "both" {
		b, err := json.MarshalIndent(rep, "", "  ")
		if err != nil {
			beckyio.Fatalf("encode report: %v", err)
		}
		b = append(b, '\n')
		if *output == "" {
			os.Stdout.Write(b)
		} else {
			if err := os.WriteFile(*output, b, 0o644); err != nil {
				beckyio.Fatalf("write %s: %v", *output, err)
			}
			beckyio.Logf(*verbose, "JSON report → %s", *output)
		}
	}

	// Write markdown.
	if *format == "markdown" || *format == "both" {
		md := report.Markdown(rep)
		mdPath := resolveMDPath(*mdOutput, *output)
		if mdPath == "" {
			// No --md-output and no --output: print markdown to stdout when JSON
			// also goes to stdout only if format==markdown; in "both" mode emit JSON
			// to stdout and warn the user to redirect.
			if *format == "markdown" {
				fmt.Print(md)
			} else {
				fmt.Fprintln(os.Stderr, "becky-report: pass --md-output <path> to save the markdown (omitting --output sends JSON to stdout)")
			}
		} else {
			if err := os.WriteFile(mdPath, []byte(md), 0o644); err != nil {
				beckyio.Fatalf("write %s: %v", mdPath, err)
			}
			beckyio.Logf(*verbose, "Markdown report → %s", mdPath)
		}
	}

	if rep.Degraded {
		os.Exit(1)
	}
}

// discovered holds the auto-discovered sidecar paths for one target.
type discovered struct {
	transcript string
	events     string
	identify   string
	motion     string
	source     string
}

// discover tries to find sidecar JSONs for target. Target may be:
//   - a directory: looks for transcript.json / diarized.json / events.json /
//     identify.json / motion.json directly inside it (becky-pipeline output layout)
//   - a video file: looks for same-named JSON files next to it
func discover(target string, verbose bool) discovered {
	info, err := os.Stat(target)
	if err != nil {
		return discovered{}
	}

	var dir, base string
	if info.IsDir() {
		dir = target
		base = pathx.Base(target)
	} else {
		dir = filepath.Dir(target)
		stem := pathx.Base(target)
		if dot := strings.LastIndex(stem, "."); dot > 0 {
			stem = stem[:dot]
		}
		base = stem
	}

	find := func(names ...string) string {
		for _, name := range names {
			p := filepath.Join(dir, name)
			if _, err := os.Stat(p); err == nil {
				beckyio.Logf(verbose, "found sidecar: %s", p)
				return p
			}
		}
		return ""
	}

	return discovered{
		transcript: find("transcript.json"),
		events:     find("events.json"),
		identify:   find("identify.json"),
		motion:     find("motion.json"),
		source:     base,
	}
}

// inferSource picks a human-readable name from the available paths.
func inferSource(transcript, events, identify, motion, target string) string {
	if target != "" {
		return pathx.Base(target)
	}
	for _, p := range []string{transcript, events, identify, motion} {
		if p != "" {
			dir := filepath.Dir(p)
			return pathx.Base(dir)
		}
	}
	return "unknown"
}

// resolveMDPath decides where to write the markdown report.
func resolveMDPath(mdOutput, jsonOutput string) string {
	if mdOutput != "" {
		return mdOutput
	}
	if jsonOutput != "" {
		base := jsonOutput
		if strings.HasSuffix(base, ".json") {
			base = base[:len(base)-5]
		}
		return base + ".report.md"
	}
	return ""
}

// parsePositional reads the first non-flag argument from os.Args (before Parse).
func parsePositional() string {
	for _, arg := range os.Args[1:] {
		if !strings.HasPrefix(arg, "-") {
			return arg
		}
	}
	return ""
}

func orNone(s string) string {
	if s == "" {
		return "(not provided)"
	}
	return s
}
