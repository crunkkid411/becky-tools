// becky-daw — the editable arrangement model at the heart of becky-canvas's DAW.
//
//	becky-daw load     --in song.mid [--json]
//	becky-daw drumgrid --in drums.mid --track <id> --clip <name> [--json]
//	becky-daw edit     --in song.mid --out edited.mid --op <op> [op flags] [--json]
//
// becky-daw is HEADLESS for now (no GUI): it loads a Standard MIDI File into the
// editable model (internal/dawmodel), optionally applies ONE pure edit operation,
// and writes the model back out as a byte-stable SMF — proving the round-trip the
// becky-canvas GUI will sit on. It is offline and deterministic: same input + same
// op => same output. Edits that override an auto value append to the corrections
// log (becky's preference-learning substrate); --json surfaces that log.
//
// Exit codes: 0 = ok, 1 = runtime/IO error, 2 = usage error.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"becky-go/internal/dawmodel"
	"becky-go/internal/habits"
	"becky-go/internal/pathx"
)

const (
	exitOK    = 0
	exitErr   = 1
	exitUsage = 2
)

func main() { os.Exit(run(os.Args[1:])) }

// run is the testable entrypoint: it returns the process exit code instead of
// calling os.Exit, so tests can drive the CLI without ending the test process.
func run(args []string) int {
	if len(args) == 0 {
		usage()
		return exitUsage
	}
	cmd, rest := args[0], args[1:]
	switch cmd {
	case "load":
		return cmdLoad(rest)
	case "drumgrid":
		return cmdDrumGrid(rest)
	case "edit":
		return cmdEdit(rest)
	case "ask":
		return cmdAsk(rest)
	case "-h", "--help", "help":
		usage()
		return exitOK
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n", cmd)
		usage()
		return exitUsage
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "becky-daw — editable DAW arrangement model (headless)")
	fmt.Fprintln(os.Stderr, "usage:")
	fmt.Fprintln(os.Stderr, "  becky-daw load     --in song.mid [--json]")
	fmt.Fprintln(os.Stderr, "  becky-daw drumgrid --in drums.mid --track ID --clip NAME [--json]")
	fmt.Fprintln(os.Stderr, "  becky-daw edit     --in song.mid --out edited.mid --op OP [flags] [--json]")
	fmt.Fprintln(os.Stderr, "  ops: addnote move resize setvel delete quantize transpose gain pan mute solo")
	fmt.Fprintln(os.Stderr, "  becky-daw ask      --in project.json --do \"mute the bass\" --do \"set tempo to 128\" --out edited.json")
}

// cmdLoad parses a .mid into the model and reports its structure.
func cmdLoad(args []string) int {
	fs := flag.NewFlagSet("load", flag.ContinueOnError)
	in := fs.String("in", "", "input .mid file")
	asJSON := fs.Bool("json", false, "emit the model as JSON")
	if code, ok := parseFlags(fs, args); !ok {
		return code
	}
	arr, err := loadArrangement(*in)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return exitErr
	}
	if *asJSON {
		return emitJSON(arr)
	}
	printSummary(arr, *in)
	return exitOK
}

// cmdDrumGrid derives and prints the step grid for one clip.
func cmdDrumGrid(args []string) int {
	fs := flag.NewFlagSet("drumgrid", flag.ContinueOnError)
	in := fs.String("in", "", "input .mid file")
	track := fs.String("track", "", "track id")
	clip := fs.String("clip", "", "clip name")
	asJSON := fs.Bool("json", false, "emit the grid as JSON")
	if code, ok := parseFlags(fs, args); !ok {
		return code
	}
	arr, err := loadArrangement(*in)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return exitErr
	}
	track2, clip2 := resolveTarget(arr, *track, *clip)
	g, err := arr.DrumGridOf(track2, clip2, 0)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return exitErr
	}
	if *asJSON {
		return emitJSON(g)
	}
	printGrid(g, track2, clip2)
	return exitOK
}

// cmdEdit applies one pure op and writes the model back to --out.
func cmdEdit(args []string) int {
	fs := flag.NewFlagSet("edit", flag.ContinueOnError)
	o := bindEditFlags(fs)
	if code, ok := parseFlags(fs, args); !ok {
		return code
	}
	if o.op == "" {
		fmt.Fprintln(os.Stderr, "edit: --op is required")
		return exitUsage
	}
	arr, err := loadArrangement(o.in)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return exitErr
	}
	track, clip := resolveTarget(arr, o.track, o.clip)
	edited, err := applyOp(arr, track, clip, o)
	if err != nil {
		fmt.Fprintln(os.Stderr, "edit:", err)
		return exitErr
	}
	if o.out != "" {
		if err := os.WriteFile(o.out, edited.ToSMF(), 0o644); err != nil {
			fmt.Fprintf(os.Stderr, "write %s: %v\n", o.out, err)
			return exitErr
		}
	}
	if o.asJSON {
		return emitJSON(edited)
	}
	printEditReport(edited, o.op, o.out)
	emitCorrectionLog(edited, o.in, o.out)
	return exitOK
}

// loadArrangement reads a .mid file into the editable model. Bad MIDI degrades to a
// wrapped error (the model never panics); a partial parse still errors here so the
// caller knows the input was imperfect.
func loadArrangement(in string) (*dawmodel.Arrangement, error) {
	if strings.TrimSpace(in) == "" {
		return nil, fmt.Errorf("--in is required")
	}
	data, err := os.ReadFile(in)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", in, err)
	}
	arr, perr := dawmodel.FromSMF(data)
	if perr != nil {
		return nil, fmt.Errorf("parse %s: %w", pathx.Base(in), perr)
	}
	return arr, nil
}

// resolveTarget falls back to the first MIDI clip when track/clip are unset, so the
// common single-stem case needs no flags.
func resolveTarget(arr *dawmodel.Arrangement, track, clip string) (string, string) {
	if track != "" && clip != "" {
		return track, clip
	}
	for _, t := range arr.Tracks {
		for _, c := range t.Clips {
			ft, fc := track, clip
			if ft == "" {
				ft = t.ID
			}
			if fc == "" {
				fc = c.Name
			}
			return ft, fc
		}
	}
	return track, clip
}

// parseFlags runs fs.Parse and maps flag errors to the usage exit code.
func parseFlags(fs *flag.FlagSet, args []string) (int, bool) {
	fs.SetOutput(os.Stderr)
	if err := fs.Parse(args); err != nil {
		return exitUsage, false
	}
	return exitOK, true
}

func emitJSON(v any) int {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		fmt.Fprintln(os.Stderr, "encode:", err)
		return exitErr
	}
	return exitOK
}

// emitCorrectionLog writes the arrangement's in-memory corrections to a JSONL
// sidecar (daw.corrections.jsonl) alongside the output file — or alongside the
// input when no output file was written (report-only mode). Best-effort: a
// write failure prints a warning and does not affect the exit code.
//
// Scope/field mapping (habits contract):
//
//	scope = c.Clip  — which clip Jordan was editing (the instrument bucket)
//	field = c.Kind  — which knob was adjusted (gain, quantize, velocity, …)
//
// This mirrors the examples in internal/habits/sources.go: scope="kick",
// field="gain_db". The clip identifies what was edited; the kind names the knob.
func emitCorrectionLog(arr *dawmodel.Arrangement, in, out string) {
	if arr.CorrectionCount() == 0 {
		return
	}
	logPath := correctionLogPath(in, out)
	for _, c := range arr.Corrections {
		if err := habits.AppendCorrectionLog(logPath, "daw", c.Clip, c.Kind, c.Auto, c.Fixed); err != nil {
			fmt.Fprintf(os.Stderr, "warn: correction log: %v\n", err)
		}
	}
}

// correctionLogPath returns the path for the daw.corrections.jsonl sidecar.
// It sits in the directory of the output file when one was written, or in the
// directory of the input file otherwise — always next to becky's work products.
func correctionLogPath(in, out string) string {
	dir := ""
	if out != "" {
		dir = pathx.Dir(out)
	}
	if dir == "" && in != "" {
		dir = pathx.Dir(in)
	}
	if dir == "" {
		return "daw.corrections.jsonl"
	}
	return filepath.Join(dir, "daw.corrections.jsonl")
}
