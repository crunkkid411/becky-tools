// becky-daw-engine — the headless FOUNDATION of becky's DAW audio engine.
//
//	becky-daw-engine [--json] [--list] [--no-interface]
//
// This is the pure-Go, deterministic foundation (SPEC-BECKY-DAW-ENGINE.md). It
// enumerates audio devices through a STUB enumerator (the real native miniaudio/
// WASAPI enumerator + the real-time audio callback + VST3/CLAP hosting are an
// explicit later "native Phase-2" cgo step — NOT built here, see internal/
// audioengine/host.go), applies the device-default selection rule, and prints
// the chosen input + output.
//
// The device-default rule (SPEC): when Jordan's pro AUDIO INTERFACE is plugged
// in, DEFAULT to it for BOTH input and output; fall back to the laptop built-in
// only when the interface is absent.
//
//	--json          emit JSON instead of the plain-language report
//	--list          list ALL enumerated devices, not just the chosen pair
//	--no-interface  simulate the interface being UNPLUGGED (demo the fallback)
//
// Exit codes: 0 ok; 1 internal error (enumeration failed); 2 degrade (devices
// enumerated but no usable input/output could be selected).
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"becky-go/internal/audioengine"
	"becky-go/internal/dawmodel"
)

// report is the JSON shape emitted by --json: the chosen selection plus the full
// device list (handy for the Phase-2 native layer to diff against).
type report struct {
	Selection audioengine.Selection `json:"selection"`
	Devices   []audioengine.Device  `json:"devices"`
}

func main() {
	// Real-audio seam: under `-tags audio` the native backend handles --list-real,
	// --record, and --play (see main_audio.go). The default (no-tag) build's seam
	// is a no-op (main_noaudio.go), so the pure-Go device-selection demo below is
	// unchanged. Keeping main() identical across builds preserves CI-green default
	// behavior (CLAUDE.md §3).
	if handled, code := audioModeRun(os.Args[1:]); handled {
		os.Exit(code)
	}

	asJSON := flag.Bool("json", false, "emit JSON instead of a plain-language report")
	list := flag.Bool("list", false, "list all enumerated devices, not just the chosen pair")
	noIface := flag.Bool("no-interface", false, "simulate the pro interface being unplugged (demo the fallback)")
	playPattern := flag.String("play-pattern", "", "read a project.json and print the computed event schedule as JSON (offline, no audio)")
	flag.Parse()

	// --play-pattern: pure-Go offline schedule dump — no audio device needed.
	if *playPattern != "" {
		os.Exit(playPatternRun(*playPattern))
	}

	// Headless foundation: enumerate via the deterministic stub. The native
	// Phase-2 AudioBackend.Enumerate replaces this with real WASAPI devices.
	enum := audioengine.StubEnumerator{WithInterface: !*noIface}
	devices, err := enum.Enumerate()
	if err != nil {
		fmt.Fprintln(os.Stderr, "enumerate error:", err)
		os.Exit(1)
	}

	sel := audioengine.SelectDefaults(devices)

	if *asJSON {
		if err := emitJSON(report{Selection: sel, Devices: devices}); err != nil {
			fmt.Fprintln(os.Stderr, "encode:", err)
			os.Exit(1)
		}
	} else {
		printReport(sel, devices, *list)
	}

	// Degrade exit code: enumeration worked but nothing usable was selectable.
	if sel.Input == nil || sel.Output == nil {
		os.Exit(2)
	}
}

// emitJSON writes the report as indented JSON to stdout.
func emitJSON(r report) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(r)
}

// printReport writes the plain-language report for a non-developer.
func printReport(sel audioengine.Selection, devices []audioengine.Device, list bool) {
	fmt.Println("becky-daw-engine — audio device selection (foundation)")
	fmt.Println("======================================================")
	fmt.Println()

	fmt.Println("Chosen devices (default rule: prefer the pro interface for in+out):")
	fmt.Printf("    input  : %s\n", sideLabel(sel.Input))
	fmt.Printf("    output : %s\n", sideLabel(sel.Output))
	fmt.Printf("    note   : %s\n", sel.Note)
	fmt.Println()

	if list {
		fmt.Printf("All %d enumerated device(s):\n", len(devices))
		for _, d := range devices {
			fmt.Printf("    - [%s] %s (%s)%s, %dch @ %dHz\n",
				d.Kind, d.DisplayName(), d.ID, ifaceTag(d), d.Channels, d.SampleRate)
		}
		fmt.Println()
	}

	fmt.Println("(foundation only — no sound yet: the real-time audio backend and")
	fmt.Println(" VST3/CLAP hosting are the native Phase-2 step, see SPEC-BECKY-DAW-ENGINE.md)")
}

// sideLabel renders a chosen device, or a clear "(none)" for the degrade case.
func sideLabel(d *audioengine.Device) string {
	if d == nil {
		return "(none)"
	}
	return fmt.Sprintf("%s (%s)%s", d.DisplayName(), d.ID, ifaceTag(*d))
}

// ifaceTag marks the pro interface vs the built-in in the listing.
func ifaceTag(d audioengine.Device) string {
	if d.IsInterface {
		return " [pro interface]"
	}
	return " [built-in]"
}

// scheduleReport is the JSON shape emitted by --play-pattern: the transport
// parameters used and the full flat event list so callers can inspect the
// computed schedule without any audio hardware.
type scheduleReport struct {
	BPM    int                          `json:"bpm"`
	PPQ    int                          `json:"ppq"`
	Events []audioengine.ScheduledEvent `json:"events"`
}

// playPatternRun reads a project.json (dawmodel.Arrangement), sequences every
// MIDI clip across all tracks using the arrangement's own BPM/PPQ, and prints
// the merged, sorted schedule as JSON. It is fully offline — no audio device,
// no cgo. Exit codes: 0 ok; 1 error; 2 degrade (no MIDI notes found).
func playPatternRun(path string) int {
	data, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintln(os.Stderr, "play-pattern: cannot read file:", err)
		return 1
	}
	var arr dawmodel.Arrangement
	if err := json.Unmarshal(data, &arr); err != nil {
		fmt.Fprintln(os.Stderr, "play-pattern: cannot parse JSON:", err)
		return 1
	}

	bpm := arr.BPM
	if bpm <= 0 {
		bpm = 120
	}
	ppq := arr.PPQ
	if ppq <= 0 {
		ppq = audioengine.PPQDefault
	}
	// Use a standard 48 kHz sample rate for the offline schedule dump.
	const offlineSR = 48000
	tr, err := audioengine.NewTransport(float64(bpm), ppq, offlineSR)
	if err != nil {
		fmt.Fprintln(os.Stderr, "play-pattern: bad transport params:", err)
		return 1
	}

	// Collect all notes from all MIDI clips across all tracks.
	var allNotes []dawmodel.Note
	for _, track := range arr.Tracks {
		if track.Kind != dawmodel.KindMIDI {
			continue
		}
		for _, clip := range track.Clips {
			allNotes = append(allNotes, clip.Notes...)
		}
	}

	evs, err := audioengine.SequenceNotes(allNotes, tr)
	if err != nil {
		fmt.Fprintln(os.Stderr, "play-pattern: sequencing error:", err)
		return 1
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(scheduleReport{BPM: bpm, PPQ: ppq, Events: evs}); err != nil {
		fmt.Fprintln(os.Stderr, "play-pattern: encode error:", err)
		return 1
	}

	if len(evs) == 0 {
		fmt.Fprintln(os.Stderr, "play-pattern: no MIDI notes found in project (degrade)")
		return 2
	}
	return 0
}
