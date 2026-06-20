// becky-midi — send LIVE MIDI to an open instrument (Maschine 2 standalone with
// MIDI-mapped pads, a loopMIDI virtual port, or the built-in GS Wavetable synth)
// without cgo. It is the live-output counterpart to becky-compose, which writes
// Standard MIDI Files to disk; becky-midi streams a kick/snare/hat pattern NOW.
//
// It can also CREATE its own virtual MIDI port (via the teVirtualMIDI driver that
// ships with loopMIDI) so the user never has to set up loopMIDI by hand: becky
// makes a port named "becky", holds it open, and streams a beat into it for an app
// like Maschine to receive — zero manual config.
//
// Subcommands:
//
//	becky-midi list                     # JSON list of MIDI output ports
//	becky-midi send [flags]             # stream a drum pattern to an existing port
//	becky-midi create-port [flags]      # CREATE becky's own virtual port + play into it
//	becky-midi --create-port <name>     # same, bare-flag spelling
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
// create-port flags:
//
//	--name  <port>     name of the virtual port becky creates (default "becky")
//	--bpm/--bars/--vel same as send (the demo pattern)
//	--loops <n>        loop the pattern n times through the port (default 4)
//	--hold  <seconds>  keep the port OPEN this long after sending so an app can select it (default 5)
//	--no-send          create + hold the port but send no notes (pure enumeration test)
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
	case "create-port":
		os.Exit(cmdCreatePort(os.Args[2:]))
	case "--create-port":
		// Accept the bare flag spelling too, so `becky-midi --create-port "becky"`
		// works exactly as written in the handoff/verify instructions.
		os.Exit(cmdCreatePort(append([]string{"--name"}, os.Args[2:]...)))
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
	fmt.Fprint(os.Stderr, `becky-midi — live MIDI output (no cgo, Windows winmm + teVirtualMIDI)

usage:
  becky-midi list
  becky-midi send        [--port <substr>] [--index <n>] [--bpm <n>] [--bars <n>]
                         [--vel <n>] [--dry-run] [--note <key>]
  becky-midi create-port [--name <port>] [--bpm <n>] [--bars <n>] [--vel <n>]
                         [--loops <n>] [--hold <seconds>] [--no-send]

examples:
  becky-midi list
  becky-midi send --dry-run                 # preview the schedule, send nothing
  becky-midi send --port "loopMIDI"         # play a 1-bar backbeat into loopMIDI
  becky-midi send --port "GS Wavetable"     # audible on the built-in synth
  becky-midi send --note 36                 # send one kick note (smoke test)

  becky-midi create-port --name "becky"     # becky MAKES its own MIDI port + plays a beat into it
  becky-midi --create-port "becky"          # same (bare-flag spelling)
  becky-midi create-port --name "becky" --hold 30   # keep the port open 30s so an app can select it
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

// cmdCreatePort is the "becky makes its OWN MIDI port" path. It creates a virtual
// MIDI port via the teVirtualMIDI driver (no loopMIDI setup needed from the user),
// holds it open so any MIDI-input app — e.g. NI Maschine — can select it, and
// streams a drum pattern into it. This is the zero-manual-config route: becky owns
// the port end-to-end.
func cmdCreatePort(args []string) int {
	fs := flag.NewFlagSet("create-port", flag.ContinueOnError)
	name := fs.String("name", "becky", "name of the virtual MIDI port to create (what apps see)")
	bpm := fs.Int("bpm", 120, "tempo in BPM for the demo pattern")
	bars := fs.Int("bars", 1, "bars of 4/4 in one pass of the pattern")
	vel := fs.Int("vel", 100, "note velocity 1-127")
	loops := fs.Int("loops", 4, "how many times to loop the pattern through the port")
	hold := fs.Float64("hold", 5, "extra seconds to keep the port OPEN after sending (so an app can find/select it)")
	noSend := fs.Bool("no-send", false, "create + hold the port but send no notes (pure enumeration test)")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	// Create the port. This is the load-bearing step: success means a brand-new
	// MIDI device now exists on the machine that other apps can see.
	vp, err := midilive.CreateVirtualPort(*name)
	if err != nil {
		fmt.Fprintf(os.Stderr, "becky-midi: could not create virtual MIDI port %q: %v\n", *name, err)
		// Explain the most common cause and how to confirm the driver.
		if dll, drv, vErr := midilive.VirtualMIDIVersion(); vErr == nil {
			fmt.Fprintf(os.Stderr, "becky-midi: teVirtualMIDI present (DLL %s, driver %s) — a same-named port may already be open.\n", dll, drv)
		} else {
			fmt.Fprintf(os.Stderr, "becky-midi: teVirtualMIDI driver not usable: %v\n", vErr)
			fmt.Fprintln(os.Stderr, "becky-midi: it ships with loopMIDI (teVirtualMIDI64.dll, normally in C:\\Windows\\System32). Install loopMIDI once.")
		}
		return 1
	}
	defer vp.Close()

	// The port exists now. Report it loudly and prove the driver version.
	fmt.Printf("becky-midi: CREATED virtual MIDI port %q — it is now a MIDI device other apps can select.\n", vp.Name())
	if dll, drv, vErr := midilive.VirtualMIDIVersion(); vErr == nil {
		fmt.Printf("becky-midi: via teVirtualMIDI (client DLL %s, kernel driver %s).\n", dll, drv)
	}

	if *noSend {
		fmt.Printf("becky-midi: --no-send: holding %q OPEN for %.0fs so you can confirm it appears as a MIDI input.\n", vp.Name(), *hold)
		time.Sleep(time.Duration(*hold * float64(time.Second)))
		fmt.Println("becky-midi: closing the port now.")
		return 0
	}

	// Build the deterministic drum pattern (reusing the same pure builder as send).
	sched := midilive.BuildDrumPattern(midilive.DrumPatternOptions{
		BPM:      *bpm,
		Bars:     *bars,
		Velocity: byte(*vel),
	})
	if *loops < 1 {
		*loops = 1
	}

	total := 0
	for i := 0; i < *loops; i++ {
		sent, sendErr := streamScheduleVirtual(vp, sched)
		total += sent
		if sendErr != nil {
			fmt.Fprintf(os.Stderr, "becky-midi: send error after %d messages: %v\n", total, sendErr)
			return 1
		}
	}

	fmt.Printf("becky-midi: sent %d MIDI messages into %q over %d loop(s) of a %d-bar pattern.\n",
		total, vp.Name(), *loops, *bars)
	if *hold > 0 {
		fmt.Printf("becky-midi: holding the port OPEN for %.0fs more so Maschine (or any app) can select %q and receive it.\n", *hold, vp.Name())
		time.Sleep(time.Duration(*hold * float64(time.Second)))
	}
	fmt.Println("becky-midi: done — closing the port. (Bytes were delivered to the OS MIDI port; whether you HEARD")
	fmt.Println("            them depends on an instrument listening on this port with pads mapped to GM kick/snare/hat.)")
	return 0
}

// streamScheduleVirtual is streamSchedule's twin for a VirtualPort: the teVirtualMIDI
// API takes raw MIDI bytes, so each packed message is unpacked via MsgBytes before
// the timed SendBytes. Returns the number of messages actually sent.
func streamScheduleVirtual(vp *midilive.VirtualPort, sched []midilive.ScheduledMessage) (int, error) {
	start := time.Now()
	sent := 0
	for _, m := range sched {
		target := start.Add(time.Duration(m.OffsetMs) * time.Millisecond)
		if d := time.Until(target); d > 0 {
			time.Sleep(d)
		}
		if err := vp.SendBytes(midilive.MsgBytes(m.Msg)); err != nil {
			return sent, err
		}
		sent++
	}
	return sent, nil
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
