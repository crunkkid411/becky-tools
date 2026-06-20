package main

import (
	"fmt"
	"os"

	"becky-go/internal/composearr"
	"becky-go/internal/reaper"
)

// cmdCompose turns a becky-compose project (project.json + its per-track .mid
// stems) into a REAPER .rpp: each stem becomes a MIDI track routed to its declared
// bus, so the session opens with the Cubase-style bus tree (DRUMS/BASS/MUSIC/FX).
// This is the "becky-compose -> becky-reaper" pipe — generated music lands in the
// DAW as a real, routed session instead of a folder of loose .mid files.
//
//	becky-reaper compose --in <dir>/project.json --out song.rpp [--render]
func cmdCompose(args []string) error {
	var in, out string
	render := false
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--in":
			i++
			if i < len(args) {
				in = args[i]
			}
		case "--out":
			i++
			if i < len(args) {
				out = args[i]
			}
		case "--render":
			render = true
		}
	}
	if in == "" || out == "" {
		return fmt.Errorf("compose needs --in <project.json> and --out <song.rpp>")
	}

	proj, baseDir, err := composearr.LoadProject(in)
	if err != nil {
		return err
	}
	a, cerr := composearr.FromProject(proj, baseDir)
	if cerr != nil {
		// Partial conversion (some stems missing/unreadable): warn but still write
		// whatever converted, per degrade-never-crash.
		fmt.Fprintf(os.Stderr, "warning: %v\n", cerr)
	}
	if len(a.Tracks) == 0 {
		return fmt.Errorf("no stems converted from %s (nothing to write)", in)
	}

	p := reaper.FromArrangement(a, renderTarget(out, render))
	return writeAndMaybeRender(p, out, render)
}
