package main

import (
	"flag"
	"fmt"
	"strconv"
	"strings"

	"becky-go/internal/dawmodel"
)

// ops.go binds the `edit` subcommand flags and dispatches one pure edit op against
// the model, plus the plain-language report printers. Each op maps to a single
// dawmodel mutator that returns a NEW arrangement (immutability), so applying the
// same op to the same input is deterministic.

// editOpts holds the parsed flags for `becky-daw edit`.
type editOpts struct {
	in, out     string
	op          string
	track, clip string
	ids         string // comma-separated note IDs
	dticks      int
	dpitch      int
	ddur        int
	vel         int
	pitch       int
	start       int
	dur         int
	ch          int
	grid        int
	strength    float64
	swing       float64
	semis       int
	gain, pan   float64
	flag        bool // mute/solo on/off
	bus         string
	asJSON      bool
}

// bindEditFlags wires editOpts onto a flag set and returns it.
func bindEditFlags(fs *flag.FlagSet) *editOpts {
	o := &editOpts{}
	fs.StringVar(&o.in, "in", "", "input .mid file")
	fs.StringVar(&o.out, "out", "", "output .mid file (omit to only report)")
	fs.StringVar(&o.op, "op", "", "edit op")
	fs.StringVar(&o.track, "track", "", "track id")
	fs.StringVar(&o.clip, "clip", "", "clip name")
	fs.StringVar(&o.ids, "ids", "", "comma-separated note IDs")
	fs.IntVar(&o.dticks, "dticks", 0, "move: delta ticks")
	fs.IntVar(&o.dpitch, "dpitch", 0, "move: delta semitones")
	fs.IntVar(&o.ddur, "ddur", 0, "resize: delta ticks")
	fs.IntVar(&o.vel, "vel", 0, "velocity")
	fs.IntVar(&o.pitch, "pitch", 60, "addnote: pitch")
	fs.IntVar(&o.start, "start", 0, "addnote: start tick")
	fs.IntVar(&o.dur, "dur", 240, "addnote: duration ticks")
	fs.IntVar(&o.ch, "channel", 0, "addnote: channel")
	fs.IntVar(&o.grid, "grid", 120, "quantize: grid ticks")
	fs.Float64Var(&o.strength, "strength", 1, "quantize: strength 0..1")
	fs.Float64Var(&o.swing, "swing", 0.5, "quantize: swing 0.5..0.75")
	fs.IntVar(&o.semis, "semis", 0, "transpose: semitones")
	fs.Float64Var(&o.gain, "gain", 1, "gain 0..2")
	fs.Float64Var(&o.pan, "pan", 0, "pan -1..1")
	fs.BoolVar(&o.flag, "on", false, "mute/solo: on")
	fs.StringVar(&o.bus, "bus", "", "sidechain: bus id (source via --ids first token)")
	fs.BoolVar(&o.asJSON, "json", false, "emit the edited model as JSON")
	return o
}

// applyOp dispatches one edit op and returns the resulting arrangement.
func applyOp(arr *dawmodel.Arrangement, track, clip string, o *editOpts) (*dawmodel.Arrangement, error) {
	ids := parseIDs(o.ids)
	switch strings.ToLower(o.op) {
	case "addnote":
		out, _, err := arr.AddNote(track, clip, dawmodel.Note{
			Start: o.start, Dur: o.dur, Pitch: o.pitch, Vel: o.vel, Ch: o.ch,
		})
		return out, err
	case "move":
		return arr.MoveNotes(track, clip, ids, o.dticks, o.dpitch)
	case "resize":
		return arr.ResizeNotes(track, clip, ids, o.ddur)
	case "setvel":
		return arr.SetVelocity(track, clip, ids, o.vel)
	case "delete":
		return arr.DeleteNotes(track, clip, ids)
	case "quantize":
		return arr.Quantize(track, clip, ids, o.grid, o.strength, o.swing)
	case "transpose":
		return arr.Transpose(track, clip, o.semis)
	case "gain":
		return arr.SetGain(track, o.gain)
	case "pan":
		return arr.SetPan(track, o.pan)
	case "mute":
		return arr.SetMute(track, o.flag)
	case "solo":
		return arr.SetSolo(track, o.flag)
	case "sidechain":
		return arr.AddSidechain(o.bus, firstID(o.ids))
	default:
		return arr, fmt.Errorf("unknown op %q", o.op)
	}
}

// parseIDs turns "1,3,7" into a slice of note IDs (bad tokens are skipped).
func parseIDs(s string) []uint64 {
	var out []uint64
	for _, tok := range strings.Split(s, ",") {
		tok = strings.TrimSpace(tok)
		if tok == "" {
			continue
		}
		if n, err := strconv.ParseUint(tok, 10, 64); err == nil {
			out = append(out, n)
		}
	}
	return out
}

// firstID returns the first comma-separated token (the sidechain source node id).
func firstID(s string) string {
	for _, tok := range strings.Split(s, ",") {
		if t := strings.TrimSpace(tok); t != "" {
			return t
		}
	}
	return ""
}

// printSummary writes a plain-language model overview for a non-developer.
func printSummary(arr *dawmodel.Arrangement, in string) {
	fmt.Printf("becky-daw — loaded %s\n", in)
	fmt.Printf("  %s %s %s | %d BPM | %d/%d | PPQ %d\n",
		orDash(arr.Genre), orDash(arr.Root), orDash(arr.Scale), arr.BPM, arr.Num, arr.Den, arr.PPQ)
	fmt.Printf("  tracks: %d | notes: %d\n", len(arr.Tracks), arr.NoteCount())
	for _, t := range arr.Tracks {
		for _, c := range t.Clips {
			fmt.Printf("    - %-16s clip %-16s ch %2d prog %3d notes %d\n",
				t.ID, c.Name, c.Channel, c.Program, len(c.Notes))
		}
	}
	if n := arr.CorrectionCount(); n > 0 {
		fmt.Printf("  corrections logged: %d\n", n)
	}
}

// printGrid renders the step grid as ASCII (# = hit, . = empty) per lane.
func printGrid(g *dawmodel.DrumGrid, track, clip string) {
	fmt.Printf("becky-daw drum grid — %s/%s | %d bars x %d steps | step %dt | ch %d\n",
		track, clip, g.Bars, g.Steps, g.StepTicks, g.Channel)
	for _, ln := range g.Lanes {
		var b strings.Builder
		for _, on := range ln.On {
			if on {
				b.WriteByte('#')
			} else {
				b.WriteByte('.')
			}
		}
		fmt.Printf("  %-8s [%s]\n", ln.Name, b.String())
	}
}

// printEditReport states what the edit did, in plain language.
func printEditReport(arr *dawmodel.Arrangement, op, out string) {
	fmt.Printf("becky-daw — applied op %q\n", op)
	fmt.Printf("  tracks: %d | notes: %d\n", len(arr.Tracks), arr.NoteCount())
	if n := arr.CorrectionCount(); n > 0 {
		fmt.Printf("  corrections logged this edit (becky learns from these): %d\n", n)
		for _, c := range arr.Corrections {
			fmt.Printf("    %-9s %-14s %s -> %s\n", c.Kind, c.Clip, c.Auto, c.Fixed)
		}
	}
	if out != "" {
		fmt.Printf("  wrote %s\n", out)
	} else {
		fmt.Println("  (no --out: model not written; report only)")
	}
}

func orDash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "-"
	}
	return s
}
