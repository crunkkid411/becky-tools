// becky-vst — headless proof that becky's Go brain can host Jordan's VST3
// plugins through the native C++ becky-audio-host sidecar (GUI-RULES.md Phases
// 2-3), driven over the NDJSON-stdio seam.
//
//	becky-vst devices                 # list audio output devices (marks default/ASIO)
//	becky-vst scan [--dir <path>]     # enumerate installed .vst3 plugins
//	becky-vst render --plugin <X.vst3> [--note 60] [--velocity 0.9] [--seconds 2] --out o.wav
//
// It drives the REAL host: scan lists real plugins, render loads a real plugin
// and offline-renders it to a WAV, returning the host's own non-silent
// corroboration (peak/RMS). Audio never crosses the seam — the host writes the
// WAV directly to --out.
//
// Degrade-never-crash: if becky-audio-host.exe is not built/installed, every
// subcommand prints a plain-language message telling Jordan how to build it
// (native/audio-host/scripts/build.ps1) and exits non-zero — it never panics.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"

	"becky-go/internal/audiohost"
	"becky-go/internal/pathx"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	switch os.Args[1] {
	case "devices":
		os.Exit(runDevices(os.Args[2:]))
	case "scan":
		os.Exit(runScan(os.Args[2:]))
	case "render":
		os.Exit(runRender(os.Args[2:]))
	case "save-state":
		os.Exit(runSaveState(os.Args[2:]))
	case "load-state":
		os.Exit(runLoadState(os.Args[2:]))
	case "-h", "--help", "help":
		usage()
		os.Exit(0)
	default:
		fmt.Fprintf(os.Stderr, "becky-vst: unknown subcommand %q\n", os.Args[1])
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage:")
	fmt.Fprintln(os.Stderr, "  becky-vst devices")
	fmt.Fprintln(os.Stderr, "  becky-vst scan [--dir <path>] [--json]")
	fmt.Fprintln(os.Stderr, "  becky-vst render --plugin <X.vst3> [--note 60] [--velocity 0.9] [--seconds 2] --out <wav> [--json]")
	fmt.Fprintln(os.Stderr, "  becky-vst save-state --plugin <X.vst3> --out <file.vstpreset> [--json]")
	fmt.Fprintln(os.Stderr, "  becky-vst load-state --plugin <X.vst3> --state <file.vstpreset> [--note 60] [--seconds 2] --out <wav> [--json]")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "Drives the native becky-audio-host sidecar to host Jordan's VST3 plugins.")
}

// openHost opens the audio host, printing a clear build-it message and returning
// a nil client when the exe is missing. The caller exits non-zero on nil.
func openHost(ctx context.Context) *audiohost.Client {
	c, err := audiohost.Open(ctx)
	if err != nil {
		var nf *audiohost.NotFoundError
		if errors.As(err, &nf) {
			fmt.Fprintln(os.Stderr, "becky-vst: the native audio host is not built yet.")
			fmt.Fprintln(os.Stderr, "  Build it on your PC:  native/audio-host/scripts/build.ps1")
			fmt.Fprintln(os.Stderr, "  (or set BECKY_AUDIO_HOST to a built becky-audio-host.exe)")
		} else {
			fmt.Fprintf(os.Stderr, "becky-vst: cannot start audio host: %v\n", err)
		}
		return nil
	}
	return c
}

func runDevices(argv []string) int {
	fs := flag.NewFlagSet("devices", flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "print the full JSON")
	if err := fs.Parse(argv); err != nil {
		return 2
	}
	ctx := context.Background()
	c := openHost(ctx)
	if c == nil {
		return 1
	}
	defer c.Close()

	res, err := c.Devices(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "becky-vst: devices: %v\n", err)
		return 1
	}
	if *asJSON {
		printJSON(res)
		return 0
	}
	fmt.Printf("Audio output devices (default host API: %s, ASIO available: %v):\n",
		res.DefaultHostAPI, res.ASIOAvailable)
	if len(res.Devices) == 0 {
		fmt.Println("  (none reported)")
	}
	for _, d := range res.Devices {
		mark := " "
		if d.Default {
			mark = "*"
		}
		asio := ""
		if d.ASIO {
			asio = " [ASIO]"
		}
		fmt.Printf("  %s [%d] %s (%s, %dch)%s\n", mark, d.Index, d.Name, d.HostAPI, d.Channels, asio)
	}
	return 0
}

func runScan(argv []string) int {
	fs := flag.NewFlagSet("scan", flag.ContinueOnError)
	dir := fs.String("dir", "", "VST3 directory to scan (default: the platform VST3 dir)")
	asJSON := fs.Bool("json", false, "print the full JSON")
	if err := fs.Parse(argv); err != nil {
		return 2
	}
	ctx := context.Background()
	c := openHost(ctx)
	if c == nil {
		return 1
	}
	defer c.Close()

	res, err := c.ScanVST(ctx, *dir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "becky-vst: scan: %v\n", err)
		return 1
	}
	if *asJSON {
		printJSON(res)
		return 0
	}
	fmt.Printf("Scanned %s: %d plugin(s)", res.Dir, res.Count)
	if res.Crashed > 0 {
		fmt.Printf(" (%d faulted on load, skipped)", res.Crashed)
	}
	fmt.Println()
	for _, p := range res.Plugins {
		status := "ok"
		if p.Crashed {
			status = "CRASHED"
		} else if !p.Loadable {
			status = "not-loadable"
		}
		cat := p.Category
		if cat == "" {
			cat = "?"
		}
		fmt.Printf("  [%s] %-28s %-12s %s\n", status, p.Name, cat, pathx.Base(p.Path))
	}
	return 0
}

func runRender(argv []string) int {
	fs := flag.NewFlagSet("render", flag.ContinueOnError)
	plugin := fs.String("plugin", "", "path to the .vst3 plugin to render (required)")
	note := fs.Int("note", 60, "MIDI note number to play (default 60 = C4)")
	velocity := fs.Float64("velocity", 0.9, "note velocity 0..1")
	seconds := fs.Float64("seconds", 2.0, "render duration in seconds")
	out := fs.String("out", "", "output WAV path (required)")
	sampleRate := fs.Int("samplerate", 48000, "render sample rate")
	asJSON := fs.Bool("json", false, "print the full JSON result")
	if err := fs.Parse(argv); err != nil {
		return 2
	}
	if *plugin == "" || *out == "" {
		fmt.Fprintln(os.Stderr, "becky-vst render: --plugin and --out are required")
		return 2
	}
	ctx := context.Background()
	c := openHost(ctx)
	if c == nil {
		return 1
	}
	defer c.Close()

	// A note-on at t=0 and a note-off near the end (held 80% of the duration),
	// so an instrument actually sounds. Effects ignore notes; the host feeds them
	// a test tone.
	events := []audiohost.NoteEvent{
		audiohost.NoteOn(0, *note, *velocity),
		audiohost.NoteOff(*seconds*0.8, *note),
	}
	res, err := c.RenderPath(ctx, *plugin, events, *seconds, *out,
		audiohost.RenderOptions{SampleRate: *sampleRate})
	if err != nil {
		fmt.Fprintf(os.Stderr, "becky-vst: render: %v\n", err)
		return 1
	}
	if *asJSON {
		printJSON(res)
		return 0
	}
	fmt.Printf("Rendered %s -> %s\n", res.Name, res.Out)
	fmt.Printf("  frames=%d  channels=%d  sampleRate=%d\n", res.Frames, res.Channels, res.SampleRate)
	fmt.Printf("  peak=%.2f dB  rms=%.2f dB\n", res.PeakDb, res.RMSDb)
	if res.NonSilent {
		fmt.Println("  result: NON-SILENT audio (the plugin processed and produced sound)")
		return 0
	}
	fmt.Println("  result: SILENT (plugin loaded + processed, but the sampled output was silent)")
	// Silence is plugin-dependent (some need a specific input), not a host
	// failure; exit 0 so a corroboration script can still inspect the WAV.
	return 0
}

// runSaveState loads a plugin and writes its current plugin state (component +
// controller) to a .vstpreset-format file via vst.state.save.
func runSaveState(argv []string) int {
	fs := flag.NewFlagSet("save-state", flag.ContinueOnError)
	plugin := fs.String("plugin", "", "path to the .vst3 plugin (required)")
	out := fs.String("out", "", "output .vstpreset path (required)")
	sampleRate := fs.Int("samplerate", 48000, "sample rate to load at")
	asJSON := fs.Bool("json", false, "print the full JSON result")
	if err := fs.Parse(argv); err != nil {
		return 2
	}
	if *plugin == "" || *out == "" {
		fmt.Fprintln(os.Stderr, "becky-vst save-state: --plugin and --out are required")
		return 2
	}
	ctx := context.Background()
	c := openHost(ctx)
	if c == nil {
		return 1
	}
	defer c.Close()

	inst, err := c.LoadVSTOptions(ctx, *plugin, *sampleRate, 0)
	if err != nil {
		fmt.Fprintf(os.Stderr, "becky-vst: save-state: load: %v\n", err)
		return 1
	}
	res, err := c.SaveState(ctx, inst.InstanceID, *out)
	if err != nil {
		fmt.Fprintf(os.Stderr, "becky-vst: save-state: %v\n", err)
		return 1
	}
	if *asJSON {
		printJSON(res)
		return 0
	}
	fmt.Printf("Saved %s state -> %s\n", res.Name, res.Out)
	fmt.Printf("  classId=%s  saved=%v\n", res.ClassID, res.Saved)
	return 0
}

// runLoadState loads a plugin, applies a saved .vstpreset to it (vst.state.load),
// then renders it — proving the saved sound is restored and audible.
func runLoadState(argv []string) int {
	fs := flag.NewFlagSet("load-state", flag.ContinueOnError)
	plugin := fs.String("plugin", "", "path to the .vst3 plugin (required)")
	state := fs.String("state", "", "path to the .vstpreset to apply (required)")
	out := fs.String("out", "", "output WAV path (required)")
	note := fs.Int("note", 60, "MIDI note number to play (default 60 = C4)")
	velocity := fs.Float64("velocity", 0.9, "note velocity 0..1")
	seconds := fs.Float64("seconds", 2.0, "render duration in seconds")
	sampleRate := fs.Int("samplerate", 48000, "render sample rate")
	asJSON := fs.Bool("json", false, "print the full JSON result")
	if err := fs.Parse(argv); err != nil {
		return 2
	}
	if *plugin == "" || *state == "" || *out == "" {
		fmt.Fprintln(os.Stderr, "becky-vst load-state: --plugin, --state and --out are required")
		return 2
	}
	ctx := context.Background()
	c := openHost(ctx)
	if c == nil {
		return 1
	}
	defer c.Close()

	ls, err := c.LoadStatePath(ctx, *plugin, *state, audiohost.RenderOptions{SampleRate: *sampleRate})
	if err != nil {
		fmt.Fprintf(os.Stderr, "becky-vst: load-state: %v\n", err)
		return 1
	}
	events := []audiohost.NoteEvent{
		audiohost.NoteOn(0, *note, *velocity),
		audiohost.NoteOff(*seconds*0.8, *note),
	}
	res, err := c.Render(ctx, ls.InstanceID, events, *seconds, *out,
		audiohost.RenderOptions{SampleRate: *sampleRate})
	if err != nil {
		fmt.Fprintf(os.Stderr, "becky-vst: load-state: render: %v\n", err)
		return 1
	}
	if *asJSON {
		printJSON(struct {
			Load   audiohost.StateLoadResult `json:"load"`
			Render audiohost.RenderResult    `json:"render"`
		}{ls, res})
		return 0
	}
	fmt.Printf("Applied %s state from %s\n", ls.Name, *state)
	fmt.Printf("Rendered -> %s  (peak=%.2f dB, rms=%.2f dB, nonSilent=%v)\n",
		res.Out, res.PeakDb, res.RMSDb, res.NonSilent)
	return 0
}

func printJSON(v interface{}) {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}
