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
)

// report is the JSON shape emitted by --json: the chosen selection plus the full
// device list (handy for the Phase-2 native layer to diff against).
type report struct {
	Selection audioengine.Selection `json:"selection"`
	Devices   []audioengine.Device  `json:"devices"`
}

func main() {
	asJSON := flag.Bool("json", false, "emit JSON instead of a plain-language report")
	list := flag.Bool("list", false, "list all enumerated devices, not just the chosen pair")
	noIface := flag.Bool("no-interface", false, "simulate the pro interface being unplugged (demo the fallback)")
	flag.Parse()

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
