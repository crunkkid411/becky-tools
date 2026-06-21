// becky-beat — the generative drum machine, on the command line.
//
// It is the headless front door to internal/beatgen (a Playbeat-class generative
// rhythm engine) so Jordan can MAKE a beat without a DAW and without REAPER, then
// feed it straight into the rest of the suite: the output is a dawmodel
// arrangement project.json that becky-drum tweaks, becky-daw-engine plays, and
// becky-canvas opens.
//
//	becky-beat new      --out beat.json [--genre trap] [--bars 1] [--seed 7] [--density 0.4]
//	becky-beat randomize --project beat.json [--seed 9] [--density 0.5] [--out out.json]
//	becky-beat euclid    --project beat.json --lane kick --pulses 4 --steps 16 [--rotate 0]
//	becky-beat mutate    --project beat.json [--amount 0.2] [--seed 3]
//
// Offline + DETERMINISTIC: same args + same seed => byte-identical output.
// Degrade-never-crash: a bad file/flag exits non-zero with a plain-language note;
// no panics. Nothing is destructive — transforms write a new file (default
// <project>.beat.json next to the source) and never touch the input.
//
// Exit codes: 0 = ok, 1 = IO/runtime error, 2 = usage error.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"becky-go/internal/beatgen"
	"becky-go/internal/dawmodel"
	"becky-go/internal/music"
	"becky-go/internal/pathx"
)

const (
	exitOK    = 0
	exitErr   = 1
	exitUsage = 2
)

// standardKit is the default lane set a fresh beat is generated over — the eight
// voices that cover most genres. Order is stable so output is deterministic.
var standardKit = []string{"kick", "snare", "clap", "hat", "ohat", "rim", "tom", "ride"}

func main() { os.Exit(run(os.Args[1:])) }

// run is the testable entrypoint: returns the exit code instead of calling
// os.Exit, so tests can drive every subcommand.
func run(args []string) int {
	if len(args) == 0 {
		usage()
		return exitUsage
	}
	switch args[0] {
	case "new":
		return runNew(args[1:])
	case "randomize", "random":
		return runTransform(args[1:], "randomize")
	case "euclid", "euclidean":
		return runTransform(args[1:], "euclid")
	case "mutate":
		return runTransform(args[1:], "mutate")
	case "-h", "--help", "help":
		usage()
		return exitOK
	default:
		fmt.Fprintf(os.Stderr, "becky-beat: unknown command %q\n", args[0])
		usage()
		return exitUsage
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "becky-beat — generate and shape drum beats (no DAW required)")
	fmt.Fprintln(os.Stderr, "usage:")
	fmt.Fprintln(os.Stderr, "  becky-beat new       --out beat.json [--genre trap] [--bars 1] [--seed 7] [--density 0.4] [--bpm 140]")
	fmt.Fprintln(os.Stderr, "  becky-beat randomize --project beat.json [--seed 9] [--density 0.5] [--out out.json]")
	fmt.Fprintln(os.Stderr, "  becky-beat euclid    --project beat.json --lane kick --pulses 4 --steps 16 [--rotate 0]")
	fmt.Fprintln(os.Stderr, "  becky-beat mutate    --project beat.json [--amount 0.2] [--seed 3]")
	fmt.Fprintln(os.Stderr, "output is a dawmodel arrangement: feed it to becky-drum / becky-daw-engine / becky-canvas.")
}

// arrangementFromPattern wraps a beatgen pattern into a one-track drum
// arrangement (channel 9, GM percussion) ready to write or chain. bpm<=0 falls
// back to the dawmodel default.
func arrangementFromPattern(p *beatgen.Pattern, bpm int) (*dawmodel.Arrangement, error) {
	grid := gridWithStepTicks(beatgen.ToDrumGrid(p))
	arr := dawmodel.New()
	if bpm > 0 {
		arr.BPM = bpm
	}
	arr = arr.AddTrack("drums", dawmodel.KindMIDI)
	arr.Tracks[0].Clips = append(arr.Tracks[0].Clips, dawmodel.Clip{
		Name: "beat", Channel: 9, Program: -1,
	})
	out, err := arr.ApplyDrumGrid("drums", "beat", grid)
	if err != nil {
		return nil, fmt.Errorf("building arrangement: %w", err)
	}
	return out, nil
}

// gridWithStepTicks guarantees a grid has a positive StepTicks before it is
// compiled to notes. beatgen.ToDrumGrid leaves StepTicks at 0 (it is rate-
// agnostic); Compile multiplies the step index by StepTicks, so a zero would
// collapse every hit onto tick 0. We pin it to a 1/16 at the standard PPQ — the
// resolution the arrangement and DrumGridOf both assume.
func gridWithStepTicks(g *dawmodel.DrumGrid) *dawmodel.DrumGrid {
	if g != nil && g.StepTicks <= 0 {
		g.StepTicks = music.StepTicks
	}
	return g
}

// writeArrangement marshals an arrangement to indented JSON at path.
func writeArrangement(path string, arr *dawmodel.Arrangement) error {
	data, err := json.MarshalIndent(arr, "", "  ")
	if err != nil {
		return fmt.Errorf("encode arrangement: %w", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", pathx.Base(path), err)
	}
	return nil
}

// loadArrangement reads a dawmodel arrangement project.json. A bad file or bad
// JSON degrades to a wrapped error, never a panic.
func loadArrangement(path string) (*dawmodel.Arrangement, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", pathx.Base(path), err)
	}
	var arr dawmodel.Arrangement
	if err := json.Unmarshal(data, &arr); err != nil {
		return nil, fmt.Errorf("parse %s: not a valid arrangement (%w)", pathx.Base(path), err)
	}
	return &arr, nil
}

// patternFromArrangement derives a beatgen pattern from the arrangement's drum
// clip (first MIDI clip on channel 9, else the first non-empty MIDI clip). It
// returns the pattern plus the (trackID, clipName) so a transform can be written
// back. An arrangement with no drum clip is a plain-language error.
func patternFromArrangement(arr *dawmodel.Arrangement) (*beatgen.Pattern, string, string, error) {
	trackID, clipName := findDrumClip(arr)
	if trackID == "" {
		return nil, "", "", fmt.Errorf("no drum clip found (need a MIDI clip with notes, ideally on channel 9)")
	}
	grid, err := arr.DrumGridOf(trackID, clipName, 0)
	if err != nil {
		return nil, "", "", fmt.Errorf("reading drum grid: %w", err)
	}
	return beatgen.FromDrumGrid(grid), trackID, clipName, nil
}

// findDrumClip mirrors becky-drum's resolver: prefer a non-empty channel-9 clip,
// then program -1, then any non-empty MIDI clip.
func findDrumClip(arr *dawmodel.Arrangement) (string, string) {
	var ch9, prog, nonEmpty [2]string
	for _, t := range arr.Tracks {
		if t.Kind != "" && t.Kind != dawmodel.KindMIDI {
			continue
		}
		for _, c := range t.Clips {
			if len(c.Notes) == 0 {
				continue
			}
			if c.Channel == 9 && ch9[0] == "" {
				ch9 = [2]string{t.ID, c.Name}
			}
			if c.Program == -1 && prog[0] == "" {
				prog = [2]string{t.ID, c.Name}
			}
			if nonEmpty[0] == "" {
				nonEmpty = [2]string{t.ID, c.Name}
			}
		}
	}
	for _, cand := range [][2]string{ch9, prog, nonEmpty} {
		if cand[0] != "" {
			return cand[0], cand[1]
		}
	}
	return "", ""
}

// defaultOut derives "<project-without-ext>.beat.json" in the source directory
// when --out is empty, using pathx so a Windows path is handled on Linux too.
func defaultOut(project, out string) string {
	if strings.TrimSpace(out) != "" {
		return out
	}
	base := pathx.Base(project)
	if i := strings.LastIndex(base, "."); i > 0 {
		base = base[:i]
	}
	dir := pathx.Dir(project)
	if dir == "" || dir == "." {
		return base + ".beat.json"
	}
	return dir + "/" + base + ".beat.json"
}
