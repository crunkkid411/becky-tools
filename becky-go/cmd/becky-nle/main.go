//go:build !gui

// becky-nle — Jordan's AI-integrated, Vegas-fast NLE (GUI-RULES.md Wave-2). The real
// WINDOW lives in gui*.go (//go:build gui): open a video, see a timeline, scrub a
// frame-accurate GPU-decoded preview, mark in/out, export the range. THIS file is the
// headless, testable stub the default `go build ./...` compiles, so CI stays green
// with ONLY the Go toolchain (no Gio system libs, no GPU).
//
// Headless usage (what CI / a script exercises — drives the SAME videopreview client
// + reel export the window uses):
//
//	becky-nle --probe  <video>                         # print width/height/fps/duration/frames as JSON
//	becky-nle --export-range <video> --in S --out S [--out-file f.mp4]  # cut [in,out] to a new MP4 next to the source
//
// Run the real window with the gui tag:
//
//	go run   -tags gui ./cmd/becky-nle [video]
//	go build -tags gui -o bin/becky-nle.exe ./cmd/becky-nle
//
// Invariants (CLAUDE.md §2): degrade, never crash — a missing sidecar / ffmpeg / video
// surfaces as a typed error + a friendly line, never a panic. --probe is deterministic
// for a given file (it just reports the sidecar's ffprobe metadata).
//
// Exit codes: 0 = ok; 1 = degraded/failed (sidecar missing, probe/export error);
// 2 = bad invocation (unknown flag / missing required arg).
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	"becky-go/internal/videopreview"
)

const (
	exitOK       = 0
	exitDegraded = 1
	exitBadArgs  = 2
)

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

// run is the testable entry point: returns an exit code instead of calling os.Exit,
// and writes to the supplied streams so the CLI surface is unit-testable.
func run(args []string, stdout, stderr *os.File) int {
	fs := flag.NewFlagSet("becky-nle", flag.ContinueOnError)
	fs.SetOutput(stderr)
	probe := fs.String("probe", "", "print metadata (width/height/fps/duration/frames) for a video as JSON, then exit")
	exportSrc := fs.String("export-range", "", "source video to cut a marked range from")
	inSec := fs.Float64("in", 0, "in-mark seconds (with --export-range)")
	outSec := fs.Float64("out", 0, "out-mark seconds (with --export-range)")
	outFile := fs.String("out-file", "", "output MP4 path (default: <source-dir>/<stem>_range.mp4)")
	if err := fs.Parse(args); err != nil {
		return exitBadArgs
	}

	switch {
	case *probe != "":
		return doProbe(*probe, stdout, stderr)
	case *exportSrc != "":
		return doExport(*exportSrc, *inSec, *outSec, *outFile, stdout, stderr)
	default:
		fmt.Fprintln(stderr, "becky-nle: nothing to do.")
		fmt.Fprintln(stderr, "  becky-nle --probe <video>")
		fmt.Fprintln(stderr, "  becky-nle --export-range <video> --in S --out S [--out-file f.mp4]")
		fmt.Fprintln(stderr, "  (the real window: go build -tags gui ./cmd/becky-nle)")
		return exitBadArgs
	}
}

// doProbe opens the video through the videopreview client (which spawns the sidecar)
// and prints its Info as JSON. Degrades clearly if the sidecar is missing or the open
// fails — it never panics.
func doProbe(path string, stdout, stderr *os.File) int {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	client, err := videopreview.Start(ctx, "")
	if err != nil {
		fmt.Fprintln(stderr, "becky-nle:", friendlySidecarErr(err))
		return exitDegraded
	}
	defer client.Close()

	info, err := client.Open(ctx, path)
	if err != nil {
		fmt.Fprintln(stderr, "becky-nle: couldn't open the video —", err)
		return exitDegraded
	}

	b, err := json.MarshalIndent(info, "", "  ")
	if err != nil {
		fmt.Fprintln(stderr, "becky-nle: encode info:", err)
		return exitDegraded
	}
	fmt.Fprintln(stdout, string(b))
	return exitOK
}

// doExport probes the source (for fps/duration) then renders [in,out] to a new MP4 via
// the shared exportRange (internal/reel). Both the probe and the render degrade with a
// plain message rather than crashing.
func doExport(src string, in, out float64, outFile string, stdout, stderr *os.File) int {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	p := NewProject()

	// Probe via the sidecar when available so the export inherits the real fps/duration.
	// If the sidecar is missing we still export: reel auto-matches the source itself.
	if client, err := videopreview.Start(ctx, ""); err == nil {
		defer client.Close()
		if info, oerr := client.Open(ctx, src); oerr == nil {
			p.LoadInfo(src, info)
		} else {
			fmt.Fprintln(stderr, "becky-nle: probe degraded (", oerr, ") — exporting anyway")
			p.Source = src
		}
	} else {
		fmt.Fprintln(stderr, "becky-nle: preview sidecar unavailable (", friendlySidecarErr(err), ") — exporting via ffmpeg directly")
		p.Source = src
	}

	// Apply the requested marks.
	p.In = in
	p.Out = out
	if p.MarkDur() <= 0 {
		fmt.Fprintln(stderr, "becky-nle: empty range — pass --in and --out (seconds), out > in")
		return exitBadArgs
	}

	res, err := exportRange(p, outFile)
	if err != nil {
		fmt.Fprintln(stderr, "becky-nle: export failed —", err)
		return exitDegraded
	}

	b, _ := json.MarshalIndent(res, "", "  ")
	fmt.Fprintln(stdout, string(b))
	fmt.Fprintf(stderr, "becky-nle: wrote %s (%s, %s)\n", res.Output, formatTC(res.DurationSec), res.Codec)
	return exitOK
}
