// becky-midi — send LIVE MIDI to an open instrument (Maschine 2 standalone with
// MIDI-mapped pads, a loopMIDI virtual port, or the built-in GS Wavetable synth)
// without cgo. It is the live-output counterpart to becky-compose, which writes
// Standard MIDI Files to disk; becky-midi streams a kick/snare/hat pattern NOW.
//
// Subcommands:
//
//	becky-midi list                     # JSON list of MIDI output ports
//	becky-midi send [flags]             # stream a drum pattern to a port
//
// send flags:
//
//	--port  <substr>   open the first port whose name contains this (default: first port)
//	--index <n>        open by numeric device index instead of name
//	--bpm   <n>        tempo (default 120)
//	--bars  <n>        bars of 4/4 to play (default 1)
//	--vel   <n>        note velocity 1-127 (default 100)
//	--dry-run          print the schedule as JSON and send NOTHING (propose-then-apply)
//	--note  <key>      ignore the pattern; send one note (key 0-127) and exit (smoke test)
//
// Honesty: on success the program reports exactly how many MIDI messages it sent
// and to which port. It cannot assert that sound was *heard* — that depends on
// the receiving instrument being open and routed — and it says so. On a non-
// Windows build every send degrades with ErrUnsupportedOS (becky's
// degrade-never-crash rule); use --dry-run to inspect the schedule anywhere.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	"becky-go/internal/midilive"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	switch os.Args[1] {
	case "list":
		os.Exit(cmdList())
	case "send":
		os.Exit(cmdSend(os.Args[2:]))
	case "-h", "--help", "help":
		usage()
		os.Exit(0)
	default:
		fmt.Fprintf(os.Stderr, "becky-midi: unknown subcommand %q\n\n", os.Args[1])
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `becky-midi — live MIDI output (no cgo, Windows winmm)

usage:
  becky-midi list
  becky-midi send [--port <substr>] [--index <n>] [--bpm <n>] [--bars <n>]
                  [--vel <n>] [--dry-run] [--note <key>]

examples:
  becky-midi list
  becky-midi send --dry-run                 # preview the schedule, send nothing
  becky-midi send --port "loopMIDI"         # play a 1-bar backbeat into loopMIDI
  becky-midi send --port "GS Wavetable"     # audible on the built-in synth
  becky-midi send --note 36                 # send one kick note (smoke test)
`)
}

func cmdList() int {
	ports, err := midilive.ListPorts()
	if err != nil {
		// Degrade: still print valid JSON (an empty array) so a pipeline doesn't
		// break, but surface the reason on stderr and exit non-zero.
		fmt.Fprintf(os.Stderr, "becky-midi: %v\n", err)
		fmt.Println("[]")
		return 1
	}
	if ports == nil {
		ports = []midilive.Port{}
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(ports); err != nil {
		fmt.Fprintf(os.Stderr, "becky-midi: json encode: %v\n", err)
		return 1
	}
	return 0
}

func cmdSend(args []string) int {
	fs := flag.NewFlagSet("send", flag.ContinueOnError)
	port := fs.String("port", "", "open the first port whose name contains this substring")
	index := fs.Int("index", -1, "open by numeric device index (overrides --port)")
	bpm := fs.Int("bpm", 120, "tempo in BPM")
	bars := fs.Int("bars", 1, "bars of 4/4 to play")
	vel := fs.Int("vel", 100, "note velocity 1-127")
	dryRun := fs.Bool("dry-run", false, "print the schedule as JSON and send nothing")
	note := fs.Int("note", -1, "send a single note (key 0-127) instead of the pattern")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	// Build the schedule first so --dry-run can show it without touching hardware.
	var sched []midilive.ScheduledMessage
	if *note >= 0 {
		k := byte(*note & 0x7F)
		sched = []midilive.ScheduledMessage{
			{OffsetMs: 0, Msg: midilive.NoteOnMsg(midilive.DrumChannel, k, byte(*vel)), Label: "note on"},
			{OffsetMs: 300, Msg: midilive.NoteOffMsg(midilive.DrumChannel, k), Label: "note off"},
		}
	} else {
		sched = midilive.BuildDrumPattern(midilive.DrumPatternOptions{
			BPM:      *bpm,
			Bars:     *bars,
			Velocity: byte(*vel),
		})
	}

	if *dryRun {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(sched)
		fmt.Fprintf(os.Stderr, "becky-midi: dry-run — %d messages over %d ms, NOTHING sent\n",
			len(sched), midilive.TotalDurationMs(sched))
		return 0
	}

	// Open the port.
	var (
		out *midilive.OutPort
		err error
	)
	if *index >= 0 {
		out, err = midilive.Open(*index)
	} else {
		out, err = midilive.OpenNamed(*port) // empty substring opens the first port
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "becky-midi: could not open MIDI port: %v\n", err)
		fmt.Fprintln(os.Stderr, "becky-midi: tip — `becky-midi list` shows available ports; `--dry-run` previews the schedule with no device.")
		return 1
	}
	defer out.Close()

	sent, sendErr := streamSchedule(out, sched)
	if sendErr != nil {
		fmt.Fprintf(os.Stderr, "becky-midi: send error after %d messages: %v\n", sent, sendErr)
		return 1
	}

	fmt.Printf("becky-midi: sent %d MIDI messages to %q (index %d) over %d ms.\n",
		sent, out.Port().Name, out.Port().Index, midilive.TotalDurationMs(sched))
	fmt.Println("becky-midi: bytes were sent to the OS MIDI port. Whether you HEARD it depends on the receiving")
	fmt.Println("            instrument being open and routed (e.g. Maschine pads mapped to GM kick/snare/hat,")
	fmt.Println("            or send to the \"GS Wavetable\" port for an always-audible test).")
	return 0
}

// streamSchedule sends each message at its scheduled offset relative to start.
// It sleeps between messages so timing is honoured by the wall clock. Returns the
// number of messages actually sent.
func streamSchedule(out *midilive.OutPort, sched []midilive.ScheduledMessage) (int, error) {
	start := time.Now()
	sent := 0
	for _, m := range sched {
		target := start.Add(time.Duration(m.OffsetMs) * time.Millisecond)
		if d := time.Until(target); d > 0 {
			time.Sleep(d)
		}
		if err := out.Send(m.Msg); err != nil {
			return sent, err
		}
		sent++
	}
	return sent, nil
}
