package main

// machine.go — the `--play-machine` / `--play-pad` CLI seam for the 16-pad
// drummachine. NEW FILE: it does not touch main.go's existing flags. It hooks in
// via an init() that scans os.Args BEFORE main()/flag.Parse runs, so a stray
// drummachine flag never collides with the device-selection FlagSet in main.go.
//
// Two modes (the canvas GUI execs the engine for sound — the proven becky-canvas
// pattern):
//
//	becky-daw-engine --play-machine <machine.json> [--loops N] [--kit DIR]
//	    ▶ a pattern: load the kit, render the pattern, play it (looped if N>1).
//	becky-daw-engine --play-pad <machine.json> --pad N [--vel V] [--kit DIR]
//	    audition pad N once (instant feedback on a pad click).
//
// Sound itself requires the `-tags audio` build (machine.go calls into
// playMachineAudio / playPadAudio, which are real only under //go:build audio; the
// no-audio build prints a rebuild hint and exits 2 — same contract as
// --play-pattern-audio).
//
// Offline schedule dump (no audio build, no hardware): with --schedule it prints
// the computed []MachineEvent as JSON so the GUI/tests can inspect timing without
// sound. Exit codes match main.go: 0 ok; 1 error; 2 degrade.

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"becky-go/internal/audioengine"
	"becky-go/internal/drummachine"
)

// machineFlagNames are the drummachine subcommand flags this seam owns.
var machineFlagNames = []string{
	"-play-machine", "--play-machine",
	"-play-pad", "--play-pad",
}

// hasMachineFlag reports whether args contain one of our subcommand flags (also
// matching the `--play-machine=foo` form).
func hasMachineFlag(args []string) bool {
	for _, a := range args {
		for _, name := range machineFlagNames {
			if a == name || strings.HasPrefix(a, name+"=") {
				return true
			}
		}
	}
	return false
}

// init runs before main(): if a drummachine subcommand is present we handle it and
// exit, so main.go's default FlagSet never sees (and rejects) our flags. When none
// is present this is a no-op and main() proceeds unchanged.
func init() {
	args := os.Args[1:]
	if !hasMachineFlag(args) {
		return
	}
	os.Exit(machineModeRun(args))
}

// machineModeRun parses and dispatches the drummachine subcommands. It uses its own
// FlagSet (ContinueOnError, output discarded) so usage never leaks into main.go.
func machineModeRun(args []string) int {
	fs := flag.NewFlagSet("machine", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	playMachine := fs.String("play-machine", "", "render and play a machine.json drum pattern")
	playPad := fs.String("play-pad", "", "audition a single pad of a machine.json")
	pad := fs.Int("pad", 0, "with --play-pad: the pad index 0..15 to audition")
	vel := fs.Int("vel", 100, "with --play-pad: velocity 1..127")
	loops := fs.Int("loops", 1, "with --play-machine: tile the pattern N times for a seamless loop")
	kitDir := fs.String("kit", "", "kit directory to resolve relative pad SamplePaths (default: machine.json's folder)")
	schedule := fs.Bool("schedule", false, "print the computed event schedule as JSON instead of playing (offline, no audio)")
	if err := fs.Parse(args); err != nil {
		fmt.Fprintln(os.Stderr, "usage: becky-daw-engine [--play-machine <machine.json> [--loops N] | --play-pad <machine.json> --pad N [--vel V]] [--kit DIR] [--schedule]")
		return 2
	}

	switch {
	case *playPad != "":
		return runPlayPad(*playPad, *kitDir, *pad, *vel, *schedule)
	case *playMachine != "":
		return runPlayMachine(*playMachine, *kitDir, *loops, *schedule)
	default:
		fmt.Fprintln(os.Stderr, "machine: no subcommand value (use --play-machine <file> or --play-pad <file> --pad N)")
		return 2
	}
}

// loadMachineFile reads + normalises a machine.json. Returns (machine, kitDir, code);
// code != -1 means the caller should return code (error/degrade).
func loadMachineFile(path, kitDirFlag string) (*drummachine.Machine, string, int) {
	f, err := os.Open(path)
	if err != nil {
		fmt.Fprintln(os.Stderr, "machine: cannot read file:", err)
		return nil, "", 1
	}
	defer func() { _ = f.Close() }()
	m, err := drummachine.Load(f)
	if err != nil {
		fmt.Fprintln(os.Stderr, "machine: cannot parse machine.json:", err)
		return nil, "", 1
	}
	kitDir := kitDirFlag
	if kitDir == "" {
		kitDir = dirOf(path) // default: resolve relative samples next to the file
	}
	return m, kitDir, -1
}

// runPlayMachine loads the machine + kit and either prints the schedule (--schedule)
// or plays it (real only under -tags audio; no-audio build prints a rebuild hint).
func runPlayMachine(path, kitDirFlag string, loops int, scheduleOnly bool) int {
	m, kitDir, code := loadMachineFile(path, kitDirFlag)
	if code != -1 {
		return code
	}
	const sr = 48000
	pat, ok := m.PatternForScene(0)
	if !ok {
		fmt.Fprintln(os.Stderr, "machine: no playable pattern (degrade)")
		return 2
	}
	events := audioengine.SequenceMachinePattern(m, pat, sr)

	if scheduleOnly {
		return emitMachineSchedule(m, events)
	}
	if len(events) == 0 {
		fmt.Fprintln(os.Stderr, "machine: pattern is empty (no audible hits)")
		return 2
	}
	kit := audioengine.LoadMachineKitAt(kitDir, m, sr)
	fmt.Printf("Playing %s | tempo %.0f BPM | %d hits | %d pad sample(s) loaded | loops %d\n",
		path, m.Tempo, len(events), kit.Len(), loops)
	return playMachineAudio(m, kit, sr, loops)
}

// runPlayPad loads the machine + kit and auditions one pad (or prints its 1-event
// schedule under --schedule).
func runPlayPad(path, kitDirFlag string, pad, vel int, scheduleOnly bool) int {
	m, kitDir, code := loadMachineFile(path, kitDirFlag)
	if code != -1 {
		return code
	}
	if pad < 0 || pad >= drummachine.PadCount {
		fmt.Fprintf(os.Stderr, "machine: pad index %d out of range [0,%d)\n", pad, drummachine.PadCount)
		return 2
	}
	const sr = 48000
	if scheduleOnly {
		p := m.Kit.Pads[pad]
		ev := audioengine.MachineEvent{
			Pad: pad, Velocity: vel, Level: p.Level, Pan: p.Pan,
			PitchSemis: p.PitchSemitones, DecaySec: p.Decay, Note: p.MidiNote,
		}
		return emitMachineSchedule(m, []audioengine.MachineEvent{ev})
	}
	kit := audioengine.LoadMachineKitAt(kitDir, m, sr)
	fmt.Printf("Auditioning pad %d (%s) of %s | vel %d | %s\n",
		pad, m.Kit.Pads[pad].Name, path, vel, sampleOrSine(kit, pad))
	return playPadAudio(m, kit, pad, vel, sr)
}

// machineScheduleReport is the JSON shape emitted by --schedule.
type machineScheduleReport struct {
	Tempo  float64                    `json:"tempo"`
	Events []audioengine.MachineEvent `json:"events"`
}

// emitMachineSchedule prints the computed schedule as JSON. Exit 2 (degrade) when
// the schedule is empty so the GUI knows nothing would sound.
func emitMachineSchedule(m *drummachine.Machine, events []audioengine.MachineEvent) int {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(machineScheduleReport{Tempo: m.Tempo, Events: events}); err != nil {
		fmt.Fprintln(os.Stderr, "machine: encode error:", err)
		return 1
	}
	if len(events) == 0 {
		fmt.Fprintln(os.Stderr, "machine: schedule is empty (no audible hits)")
		return 2
	}
	return 0
}

// sampleOrSine reports whether a pad will play its sample or the sine fallback.
func sampleOrSine(kit *audioengine.MachineKit, pad int) string {
	if kit != nil && kit.PadHasSample(pad) {
		return "sample"
	}
	return "sine fallback (no sample)"
}

// dirOf returns the directory portion of a path, treating both '/' and '\' as
// separators so a Windows-style machine.json path resolves on Linux/CI too.
func dirOf(p string) string {
	if i := strings.LastIndexAny(p, `/\`); i >= 0 {
		return p[:i]
	}
	return "."
}
