//go:build !gui

// becky-canvas — the deterministic FOUNDATION for Jordan's AI-friendly Cubase
// replacement: a native creative GUI (becky-ask + video/DAW/MIDI/drum on one
// canvas, SPEC-BECKY-CANVAS.md). This binary is the HEADLESS scene-model layer the
// later native GUI renders. It does NOT open a window: the giu/Dear-ImGui (cgo)
// window + DrawList waveform/pitch rendering are an explicit Phase-2 native step.
//
//	becky-canvas [--mode ask|video|daw|midi|drum] [--json] [project.json]
//
// With a project.json it loads a becky-compose project into a DAW-mode SCENE (track
// lanes, clips on a timeline, a viewport/transport, pitch/waveform lane placeholders
// the GUI fills, the routing DAG, and a corrections-log hook). With no project it
// emits an empty scene for the chosen mode. Default output is scene.json on stdout
// (deterministic, sorted); --json is accepted as an explicit synonym. A plain-English
// note goes to stderr.
//
// Invariants (CLAUDE.md §2): deterministic (same project -> byte-identical
// scene.json); degrade, never crash (a bad/missing project still prints a usable
// empty DAW scene + a non-zero "degraded" code, never a panic).
//
// Exit codes: 0 = scene emitted; 1 = degraded (project couldn't be read/parsed, but
// an empty scene was still emitted); 2 = bad invocation (unknown flag / bad --mode).
package main

import (
	"flag"
	"fmt"
	"os"

	"becky-go/internal/canvas"
)

const (
	exitOK       = 0
	exitDegraded = 1
	exitBadArgs  = 2
)

func main() { os.Exit(run(os.Args[1:])) }

// run is the testable entry point: it returns the exit code instead of calling
// os.Exit, so the CLI surface can be unit-tested.
func run(args []string) int {
	fs := flag.NewFlagSet("becky-canvas", flag.ContinueOnError)
	modeFlag := fs.String("mode", string(canvas.ModeAsk),
		"canvas mode: "+joinModes())
	_ = fs.Bool("json", false, "emit scene JSON (default; accepted as an explicit synonym)")
	if err := fs.Parse(args); err != nil {
		return exitBadArgs
	}

	mode, ok := canvas.ParseMode(*modeFlag)
	if !ok {
		fmt.Fprintf(os.Stderr, "becky-canvas: unknown mode %q (want one of: %s)\n",
			*modeFlag, joinModes())
		return exitBadArgs
	}

	path := fs.Arg(0)
	scene, degraded := buildScene(path, mode)

	if err := emit(scene); err != nil {
		fmt.Fprintln(os.Stderr, "becky-canvas: encode scene:", err)
		return exitDegraded
	}
	if degraded {
		return exitDegraded
	}
	return exitOK
}

// buildScene loads the project at path (DAW mode) or returns an empty scene for the
// chosen mode when no path is given. A load failure degrades: it returns the empty
// DAW scene Load produced AND degraded=true (the canvas still opens). The plain
// note to stderr explains what happened in non-developer language.
func buildScene(path string, mode canvas.Mode) (canvas.Scene, bool) {
	if path == "" {
		fmt.Fprintf(os.Stderr, "becky-canvas: empty %s scene (no project given)\n", mode)
		return canvas.NewScene(mode), false
	}
	scene, err := canvas.Load(path)
	if err != nil {
		fmt.Fprintln(os.Stderr, "becky-canvas: couldn't load the project, opening an empty DAW canvas instead —", err)
		return scene, true
	}
	fmt.Fprintf(os.Stderr, "becky-canvas: loaded %d track lane(s) into a DAW scene from %s\n",
		len(scene.Tracks), scene.Title)
	return scene, false
}

// emit writes the scene as deterministic scene.json to stdout.
func emit(s canvas.Scene) error {
	data, err := s.JSON()
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(os.Stdout, string(data))
	return err
}

// joinModes renders the planned mode names for help/error text.
func joinModes() string {
	names := canvas.ModeNames()
	out := ""
	for i, n := range names {
		if i > 0 {
			out += "|"
		}
		out += n
	}
	return out
}
