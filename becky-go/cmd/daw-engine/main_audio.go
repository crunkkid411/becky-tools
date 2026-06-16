//go:build audio

package main

// main_audio.go is the REAL-AUDIO seam, compiled only under `-tags audio`. It
// adds three subcommands on top of the pure-Go device-selection demo, all driven
// by the cgo MiniaudioBackend (native_bridge.c / miniaudio, WASAPI on Windows):
//
//	becky-daw-engine --list-real            list REAL devices + the chosen pair
//	becky-daw-engine --record <secs> <wav>  record from the default/chosen mic
//	becky-daw-engine --play <wav>           play a WAV through the default/chosen out
//
// It uses its own flag.FlagSet so it never collides with main.go's default flags.
// When none of these flags are present it returns handled=false and main() runs
// the existing stub demo unchanged — so `-tags audio` is purely additive.
//
// Exit codes (match main.go's contract): 0 ok; 1 internal/native error; 2 degrade
// (e.g. devices enumerated but no usable pair, or a missing file).

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"becky-go/internal/audioengine"
	"becky-go/internal/dawmodel"
)

// audioFlagNames are the real-audio subcommand flags this seam owns. We only
// activate when one of these is present, so a default-flag invocation (--json,
// --list, --no-interface) falls straight through to the pure-Go demo with no
// noisy usage output.
var audioFlagNames = []string{
	"-list-real", "--list-real",
	"-record", "--record",
	"-play", "--play",
	"-play-pattern-audio", "--play-pattern-audio",
}

// hasAudioFlag reports whether args contain a real-audio subcommand flag (also
// matching the `--play=foo` / `-play=foo` forms).
func hasAudioFlag(args []string) bool {
	for _, a := range args {
		for _, name := range audioFlagNames {
			if a == name || strings.HasPrefix(a, name+"=") {
				return true
			}
		}
	}
	return false
}

// audioModeRun inspects args for a real-audio subcommand and, if present, runs it
// and returns handled=true with an exit code. Otherwise handled=false so main()
// falls through to the pure-Go demo. It never panics: every native failure is a
// typed error turned into a plain-language message + exit code.
func audioModeRun(args []string) (handled bool, exitCode int) {
	if !hasAudioFlag(args) {
		return false, 0 // default-flag invocation — run the pure-Go demo
	}
	fs := flag.NewFlagSet("audio", flag.ContinueOnError)
	fs.SetOutput(io.Discard) // we print our own plain-language errors
	listReal := fs.Bool("list-real", false, "enumerate REAL audio devices via miniaudio and show the chosen pair")
	record := fs.Bool("record", false, "record from the mic: --record <seconds> <out.wav>")
	play := fs.String("play", "", "play a WAV file through the chosen output device")
	playPatternAudio := fs.String("play-pattern-audio", "", "render and play a project.json pattern through the synth")
	if err := fs.Parse(args); err != nil {
		fmt.Fprintln(os.Stderr, "usage: becky-daw-engine [--list-real | --record <seconds> <out.wav> | --play <file.wav> | --play-pattern-audio <project.json>]")
		return true, 2
	}

	switch {
	case *record:
		return true, runRecord(fs.Args())
	case *play != "":
		return true, runPlay(*play)
	case *listReal:
		return true, runListReal()
	case *playPatternAudio != "":
		return true, runPlayPatternAudio(*playPatternAudio)
	default:
		return false, 0
	}
}

// chooseDevices enumerates real devices and applies the existing SelectDefaults
// rule, returning the backend (with its id map populated), the selection, and the
// device list. Any native error is returned for the caller to report + exit 1.
func chooseDevices() (*audioengine.MiniaudioBackend, audioengine.Selection, []audioengine.Device, error) {
	backend := audioengine.NewMiniaudioBackend(48000, 2)
	devices, err := backend.Enumerate()
	if err != nil {
		return backend, audioengine.Selection{}, nil, err
	}
	return backend, audioengine.SelectDefaults(devices), devices, nil
}

// runListReal prints the real device list and the chosen pair.
func runListReal() int {
	_, sel, devices, err := chooseDevices()
	if err != nil {
		fmt.Fprintln(os.Stderr, "could not list audio devices:", err)
		return 1
	}
	fmt.Println("becky-daw-engine — REAL audio devices (miniaudio/WASAPI)")
	fmt.Println("=======================================================")
	fmt.Printf("Found %d device(s):\n", len(devices))
	for _, d := range devices {
		tag := "[built-in]"
		if d.IsInterface {
			tag = "[pro interface]"
		}
		def := ""
		if d.IsDefault {
			def = " (OS default)"
		}
		fmt.Printf("    - [%s] %s %s%s, %dch @ %dHz\n",
			d.Kind, d.DisplayName(), tag, def, d.Channels, d.SampleRate)
	}
	fmt.Println()
	fmt.Println("Chosen (prefer the pro interface for in+out):")
	fmt.Printf("    note: %s\n", sel.Note)
	if sel.Input == nil || sel.Output == nil {
		return 2 // degrade: no usable pair
	}
	return 0
}

// runRecord parses "<seconds> <out.wav>" and records from the chosen mic.
func runRecord(rest []string) int {
	if len(rest) < 2 {
		fmt.Fprintln(os.Stderr, "usage: becky-daw-engine --record <seconds> <out.wav>")
		return 2
	}
	seconds, err := strconv.ParseFloat(rest[0], 64)
	if err != nil || seconds <= 0 {
		fmt.Fprintf(os.Stderr, "bad seconds %q: want a positive number\n", rest[0])
		return 2
	}
	outPath := rest[1]

	backend, sel, _, err := chooseDevices()
	if err != nil {
		fmt.Fprintln(os.Stderr, "could not open audio devices:", err)
		return 1
	}
	fmt.Printf("Recording %.1fs from %s -> %s ...\n", seconds, sideName(sel.Input), outPath)
	if err := backend.RecordWAV(sel.Input, outPath, seconds, 48000, 1); err != nil {
		fmt.Fprintln(os.Stderr, "record failed:", err)
		return 1
	}
	fmt.Println("Done. Wrote", outPath)
	return 0
}

// runPlay plays the given WAV through the chosen output device.
func runPlay(path string) int {
	backend, sel, _, err := chooseDevices()
	if err != nil {
		fmt.Fprintln(os.Stderr, "could not open audio devices:", err)
		return 1
	}
	fmt.Printf("Playing %s through %s ...\n", path, sideName(sel.Output))
	if err := backend.PlayWAV(sel.Output, path); err != nil {
		fmt.Fprintln(os.Stderr, "play failed:", err)
		return 1
	}
	fmt.Println("Done.")
	return 0
}

// sideName is a short label for a chosen device, or "(default device)" when the
// selection is nil (the backend will fall back to the OS default).
func sideName(d *audioengine.Device) string {
	if d == nil {
		return "(default device)"
	}
	return d.DisplayName()
}

// runPlayPatternAudio reads a project.json, sequences all MIDI notes, renders
// the pattern with the pure-Go polyphonic synth, and plays it through the
// chosen output device. This is the audible entry point for
// `becky-daw-engine --play-pattern-audio <project.json>`.
//
// Exit codes: 0 ok; 1 internal error; 2 degrade (no MIDI notes / bad file).
func runPlayPatternAudio(path string) int {
	data, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintln(os.Stderr, "play-pattern-audio: cannot read file:", err)
		return 1
	}
	var arr dawmodel.Arrangement
	if err := json.Unmarshal(data, &arr); err != nil {
		fmt.Fprintln(os.Stderr, "play-pattern-audio: cannot parse JSON:", err)
		return 1
	}

	// Enumerate real devices so we can pass the pro-interface output to the synth.
	_, sel, _, enumErr := chooseDevices()
	if enumErr != nil {
		fmt.Fprintln(os.Stderr, "play-pattern-audio: device enumeration failed (will use OS default):", enumErr)
		sel.Output = nil
	}

	bpm := arr.BPM
	if bpm <= 0 {
		bpm = 120
	}
	fmt.Printf("Playing pattern from %s | BPM %d | output: %s\n",
		path, bpm, sideName(sel.Output))
	fmt.Println("Rendering audio ... (this may take a moment for long patterns)")

	if err := audioengine.PlayPatternAudio(&arr, sel.Output, 48000); err != nil {
		fmt.Fprintln(os.Stderr, "play-pattern-audio:", err)
		if strings.Contains(err.Error(), "no MIDI notes") {
			return 2 // degrade: empty pattern
		}
		return 1
	}
	fmt.Println("Done.")
	return 0
}
