//go:build !gui

// becky-drummachine — THE WINDOW: a real, GUI-first 16-pad drum machine (a
// Maschine-2-class groovebox) where the AI works the controls. The actual window
// lives in gui.go (`//go:build gui`); THIS file is the headless, testable stub the
// default `go build ./...` compiles, so CI stays green without the GUI system libs.
//
// Headless usage (what CI exercises):
//
//	becky-drummachine [--machine machine.json] [--do "<plain-English instruction>"] [--json]
//
// With --do it runs the SAME words→edits path the window's AI box uses
// (machinectl.Parse + machinectl.Apply) against the loaded (or default) machine,
// prints the plain-English summary to stderr, and prints the resulting machine.json
// to stdout. This is the deterministic surface the unit tests cover. Without --do it
// just prints the loaded/default machine (a quick "what would the window open with").
//
// Run the real window with the gui tag:
//
//	go run -tags gui ./cmd/drummachine
//	go build -tags gui -o bin/becky-drummachine.exe ./cmd/drummachine
//
// Invariants (CLAUDE.md §2): deterministic (same machine + same instruction ->
// byte-identical machine.json + summary); degrade, never crash (a bad/missing
// machine still opens a default; an unrecognised instruction prints a friendly note
// and leaves the machine unchanged, exit 0).
//
// Exit codes: 0 = ok (incl. an unrecognised instruction — that's a friendly note,
// not a failure); 1 = degraded (the --machine file couldn't be read/parsed, a
// default was used instead); 2 = bad invocation (unknown flag).
package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"becky-go/internal/drummachine"
	"becky-go/internal/machinectl"
)

const (
	exitOK       = 0
	exitDegraded = 1
	exitBadArgs  = 2
)

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

// run is the testable entry point: it returns an exit code instead of calling
// os.Exit, and writes to the supplied streams so the CLI surface is unit-testable.
func run(args []string, stdout, stderr *os.File) int {
	fs := flag.NewFlagSet("becky-drummachine", flag.ContinueOnError)
	fs.SetOutput(stderr)
	machinePath := fs.String("machine", "", "path to a machine.json to load (default: a fresh default machine)")
	do := fs.String("do", "", "run one plain-English instruction (the AI box, headless) and print the result")
	_ = fs.Bool("json", false, "emit the resulting machine.json (default; accepted as an explicit synonym)")
	if err := fs.Parse(args); err != nil {
		return exitBadArgs
	}

	m, degraded := loadOrDefault(*machinePath, stderr)

	// --do: run the SAME parse+apply path the window's AI box uses.
	if strings.TrimSpace(*do) != "" {
		next, summary := applyInstruction(m, *do)
		fmt.Fprintln(stderr, "becky-drummachine:", summary)
		m = next
	}

	if err := emit(m, stdout); err != nil {
		fmt.Fprintln(stderr, "becky-drummachine: encode machine:", err)
		return exitDegraded
	}
	if degraded {
		return exitDegraded
	}
	return exitOK
}

// applyInstruction runs the deterministic words→edits pipeline (the headless mirror
// of the GUI AI box): PickParser().Parse -> machinectl.Apply. It returns the new
// machine and the plain-English summary. Degrade-never-crash: a nil parser result or
// an apply error still yields a usable machine (the input, unchanged) and a note.
func applyInstruction(m *drummachine.Machine, instruction string) (*drummachine.Machine, string) {
	parser := machinectl.PickParser() // deterministic on a machine with no model present
	intent, err := parser.Parse(instruction, m)
	if err != nil {
		// Parser errors are not expected (the deterministic parser never errors), but
		// degrade rather than crash if a wired model path returns one.
		return m, "couldn't read that instruction: " + err.Error()
	}
	next, summary, aerr := machinectl.Apply(m, intent)
	if aerr != nil {
		// Apply still returns a safe machine on a typed edit error.
		return next, summary
	}
	return next, summary
}

// loadOrDefault loads a machine.json from path, or returns a fresh default machine.
// A missing path is normal (default). A path that fails to read/parse degrades to a
// default AND flags degraded=true (the window still opens). A plain note explains it.
func loadOrDefault(path string, stderr *os.File) (m *drummachine.Machine, degraded bool) {
	path = strings.TrimSpace(path)
	if path == "" {
		return drummachine.NewMachine(), false
	}
	f, err := os.Open(path)
	if err != nil {
		fmt.Fprintln(stderr, "becky-drummachine: couldn't open the machine, opening a default one instead —", err)
		return drummachine.NewMachine(), true
	}
	defer f.Close()
	loaded, err := drummachine.Load(f)
	if err != nil {
		fmt.Fprintln(stderr, "becky-drummachine: couldn't read the machine, opening a default one instead —", err)
		return drummachine.NewMachine(), true
	}
	fmt.Fprintf(stderr, "becky-drummachine: loaded %q (%d pattern(s), tempo %g)\n",
		loaded.Name, loaded.PatternCount(), loaded.Tempo)
	return loaded, false
}

// emit writes m as deterministic machine.json to stdout.
func emit(m *drummachine.Machine, stdout *os.File) error {
	data, err := m.MarshalBytes()
	if err != nil {
		return err
	}
	_, err = stdout.Write(data)
	return err
}
