// becky-diagram — ascii-art -> svgbob -> PNG -> Show Me doc, one dumb call.
// Gives any dumb local model (or agent) a visual language: draw ascii boxes
// and arrows in a text file, hand it to becky-diagram, get back a rendered
// SVG+PNG diagram plus a ready-to-open, high-contrast HTML page
// (AUTOPILOT.md P5; the pipeline itself was hand-proven once at
// hj-mission-control\data\showme\svgbob-test).
//
//	becky-diagram --in diagram.txt --title "Downtime Engine" --out data\showme\downtime-engine
//	becky-diagram --text "+---+\n| x |\n+---+" --title "Quick Box" --out out\
//	becky-diagram --selftest        # offline, no-hardware proof of the pipeline
//	becky-diagram --dry-run --in x.txt --title "..."   # print argv, don't run
//
// becky-shaped: OFFLINE (two local .exe calls: svgbob_cli + rsvg-convert, no
// network), DETERMINISTIC (same ascii text + options -> byte-identical
// SVG/PNG), DEGRADE-NEVER-CRASH (a missing binary prints a plain note and
// exits 0 with degraded:true).
//
// Exit codes: 0 = ran (incl. a clean degrade or a passing selftest), 1 =
// unexpected error / selftest failure, 2 = usage.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"

	"becky-go/internal/diagram"
)

func main() {
	in := flag.String("in", "", "path to an ascii-art source file")
	text := flag.String("text", "", "inline ascii-art text (alternative to --in)")
	title := flag.String("title", "", "REQUIRED (unless --selftest): plain-language title for the Show Me page")
	out := flag.String("out", "", "output directory (default: current directory)")

	svgbobBin := flag.String("svgbob-bin", "", "svgbob_cli binary (default: "+diagram.DefaultSvgbobBin+" on PATH)")
	rsvgBin := flag.String("rsvg-bin", "", "rsvg-convert binary (default: "+diagram.DefaultRsvgBin+" on PATH)")
	fontSize := flag.Int("font-size", 0, "svgbob font size (svgbob's own default when unset)")
	strokeWidth := flag.Int("stroke-width", 0, "svgbob stroke width (svgbob's own default when unset)")
	scale := flag.Float64("scale", 0, "svgbob scale factor (svgbob's own default when unset)")

	asJSON := flag.Bool("json", false, "emit JSON instead of a plain-language report")
	dryRun := flag.Bool("dry-run", false, "print the svgbob_cli/rsvg-convert argv without running them")
	selftest := flag.Bool("selftest", false, "run the offline, no-hardware pipeline proof and exit")
	flag.Parse()

	if *selftest {
		os.Exit(runSelftest())
	}

	if strings.TrimSpace(*title) == "" {
		fmt.Fprintln(os.Stderr, `usage: becky-diagram --in diagram.txt --title "..." [--out dir] [--json]`)
		fmt.Fprintln(os.Stderr, `   or: becky-diagram --text "ascii art" --title "..." [--out dir]`)
		os.Exit(2)
	}
	if strings.TrimSpace(*in) == "" && strings.TrimSpace(*text) == "" {
		fmt.Fprintln(os.Stderr, "usage: becky-diagram needs --in <file> or --text \"...\"")
		os.Exit(2)
	}

	opts := diagram.Options{
		In: *in, Text: *text, Title: *title, OutDir: *out,
		SvgbobBin: *svgbobBin, RsvgBin: *rsvgBin,
		FontSize: *fontSize, StrokeWidth: *strokeWidth, Scale: *scale,
	}

	if *dryRun {
		emit(diagram.Plan(opts), *asJSON)
		return
	}
	emit(diagram.Generate(opts), *asJSON)
}

func emit(res diagram.Result, asJSON bool) {
	if asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(res); err != nil {
			fmt.Fprintln(os.Stderr, "encode:", err)
			os.Exit(1)
		}
		return
	}
	printReport(res)
}

func printReport(res diagram.Result) {
	if res.Degraded {
		fmt.Println("becky-diagram could not render the diagram.")
		fmt.Println("  reason:", res.Error)
		fmt.Println("  (this is a graceful degrade — svgbob_cli or rsvg-convert was missing/failed, not a crash.)")
		return
	}
	fmt.Printf("Rendered %q -> %s\n", res.Title, res.HTMLPath)
	fmt.Println("  source:", res.SourcePath)
	fmt.Println("  svg:   ", res.SVGPath)
	fmt.Println("  png:   ", res.PNGPath)
}

// runSelftest is the one-command, OFFLINE, no-hardware proof of the real code
// path: it exercises slug resolution, argv construction, and HTML rendering
// with no svgbob_cli/rsvg-convert binary required. This is becky's "provable
// handoff" gate.
func runSelftest() int {
	plan := diagram.Plan(diagram.Options{
		Text: "+---+\n| x |\n+---+", Title: "Selftest Diagram!", OutDir: "out",
	})
	svgArgs := diagram.BuildSvgbobArgs(diagram.Options{FontSize: 16}, plan.SourcePath, plan.SVGPath)
	rsvgArgs := diagram.BuildRsvgArgs(plan.SVGPath, plan.PNGPath)
	htmlBody := diagram.RenderHTML("Selftest <Diagram>", "+---+\n| x |\n+---+", "d.svg", "d.png")

	type check struct {
		name string
		ok   bool
	}
	checks := []check{
		{"Plan never degrades", !plan.Degraded},
		{"slug strips punctuation/spaces", plan.Slug == "selftest-diagram"},
		{"source/svg/png/html paths derive from OutDir+slug", plan.SourcePath == "out\\selftest-diagram.txt" || plan.SourcePath == "out/selftest-diagram.txt"},
		{"svgbob argv carries the source file first", len(svgArgs) > 0 && svgArgs[0] == plan.SourcePath},
		{"svgbob argv carries -o <svg>", argVal(svgArgs, "-o") == plan.SVGPath},
		{"svgbob argv carries --font-size when set", argVal(svgArgs, "--font-size") == "16"},
		{"rsvg argv is <svg> -o <png>", len(rsvgArgs) == 3 && rsvgArgs[0] == plan.SVGPath && argVal(rsvgArgs, "-o") == plan.PNGPath},
		{"HTML escapes the title", !strings.Contains(htmlBody, "<Diagram>")},
		{"HTML has no font-size below 28px", minFontSize(htmlBody) >= 28},
		{"HTML embeds the image filenames", strings.Contains(htmlBody, "d.svg") && strings.Contains(htmlBody, "d.png")},
	}

	failed := 0
	for _, c := range checks {
		status := "PASS"
		if !c.ok {
			status = "FAIL"
			failed++
		}
		fmt.Printf("[%s] %s\n", status, c.name)
	}
	fmt.Println()
	if failed == 0 {
		fmt.Printf("becky-diagram selftest: PASS (%d/%d checks)\n", len(checks), len(checks))
		return 0
	}
	fmt.Printf("becky-diagram selftest: FAIL (%d/%d checks failed)\n", failed, len(checks))
	return 1
}

func argVal(args []string, flag string) string {
	for i, a := range args {
		if a == flag && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}

// minFontSize scans an HTML string for every "font-size: Npx" and returns the
// smallest N found (or -1 if none). No regex import needed for a one-shot
// scan of a small, known-shape template.
func minFontSize(htmlBody string) int {
	const needle = "font-size:"
	min := -1
	for i := 0; i+len(needle) <= len(htmlBody); i++ {
		if htmlBody[i:i+len(needle)] != needle {
			continue
		}
		j := i + len(needle)
		for j < len(htmlBody) && htmlBody[j] == ' ' {
			j++
		}
		start := j
		for j < len(htmlBody) && htmlBody[j] >= '0' && htmlBody[j] <= '9' {
			j++
		}
		if j == start {
			continue
		}
		n := 0
		for _, c := range htmlBody[start:j] {
			n = n*10 + int(c-'0')
		}
		if min == -1 || n < min {
			min = n
		}
	}
	return min
}
