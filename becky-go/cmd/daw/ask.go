package main

// ask.go — `becky-daw ask`: the HEADLESS natural-language editor. It loads a session
// (a becky-compose project.json, a raw arrangement.json, or a .mid), turns each plain-
// English instruction into a ctledit.BeckyEditBatch via ctlmodel (keyword path offline,
// GBNF-model path when one is wired), applies it to the dawmodel.Arrangement, and writes
// the result back out. This is the runnable twin of the becky-canvas agent box — the
// select→ask→transform loop usable from a script TODAY, with no GUI and no GPU.
//
// It closes the loop with the rest of the suite:
//
//	becky-compose -genre crunkcore -out song/
//	becky-daw ask --in song/project.json --do "set tempo to 128" --do "mute the sfx" --out song/edited.json
//	becky-reaper build --in song/edited.json --out song/song.rpp   # opens + plays in REAPER
//
// Offline + deterministic: the keyword proposer always runs, so the same input + same
// instructions produce the same arrangement.

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"

	"becky-go/internal/composearr"
	"becky-go/internal/ctledit"
	"becky-go/internal/ctlmodel"
	"becky-go/internal/dawmodel"
	"becky-go/internal/pathx"
)

// stringList collects a repeatable flag (--do "x" --do "y").
type stringList []string

func (s *stringList) String() string { return strings.Join(*s, "; ") }
func (s *stringList) Set(v string) error {
	*s = append(*s, v)
	return nil
}

// cmdAsk applies one or more plain-English instructions to a session.
func cmdAsk(args []string) int {
	fs := flag.NewFlagSet("ask", flag.ContinueOnError)
	in := fs.String("in", "", "input session: a becky-compose project.json, an arrangement.json, or a .mid")
	out := fs.String("out", "", "write the edited arrangement here (.json for becky-reaper build, or .mid)")
	dry := fs.Bool("dry-run", false, "show what becky would do without writing")
	asJSON := fs.Bool("json", false, "emit the edited arrangement as JSON on stdout")
	var instr stringList
	fs.Var(&instr, "do", "an instruction in plain English (repeatable), e.g. --do \"mute the bass\"")
	fs.Var(&instr, "instruction", "alias for --do")
	if code, ok := parseFlags(fs, args); !ok {
		return code
	}

	// Allow instructions as trailing args too: becky-daw ask --in x "mute the bass"
	instructions := append([]string(instr), fs.Args()...)
	if len(instructions) == 0 {
		fmt.Fprintln(os.Stderr, "ask: give at least one instruction, e.g. --do \"set tempo to 128\"")
		return exitUsage
	}

	arr, err := loadSession(*in)
	if err != nil {
		fmt.Fprintln(os.Stderr, "ask:", err)
		return exitErr
	}

	proposer := ctlmodel.PickProposer()
	totalApplied, totalSkipped := 0, 0
	for _, raw := range instructions {
		phrase := strings.TrimSpace(raw)
		if phrase == "" {
			continue
		}
		batch := proposer.Propose(phrase, arr)
		if len(batch.Edits) == 0 {
			fmt.Printf("• %q\n  becky: %s\n", phrase, fallbackSummary(batch.Summary))
			continue
		}
		next, res, aerr := ctledit.Apply(arr, batch, nil)
		if aerr != nil {
			fmt.Fprintf(os.Stderr, "ask: apply %q: %v\n", phrase, aerr)
			return exitErr
		}
		arr = next
		totalApplied += res.Applied
		totalSkipped += res.Skipped
		fmt.Printf("• %q\n  becky: %s\n  applied %d, skipped %d\n", phrase, batch.Summary, res.Applied, res.Skipped)
		for _, oc := range res.Outcomes {
			if !oc.Applied {
				fmt.Printf("    - skipped %s: %s\n", oc.Op, oc.Reason)
			}
		}
	}

	fmt.Printf("\nTOTAL: %d edit(s) applied, %d skipped\n", totalApplied, totalSkipped)

	if *asJSON {
		if code := emitJSON(arr); code != exitOK {
			return code
		}
	}

	if *dry {
		fmt.Println("(dry run — nothing written)")
		return exitOK
	}
	if *out != "" {
		if err := writeSession(*out, arr); err != nil {
			fmt.Fprintln(os.Stderr, "ask:", err)
			return exitErr
		}
		fmt.Printf("wrote %s\n", *out)
		if strings.HasSuffix(strings.ToLower(*out), ".json") {
			fmt.Printf("open it in REAPER:  becky-reaper build --in %s --out song.rpp\n", *out)
		}
	}
	return exitOK
}

// loadSession reads a session from a compose project.json, a raw arrangement.json, or a
// .mid — whichever the path is. Degrade-never-crash: a bad file returns a wrapped error.
func loadSession(in string) (*dawmodel.Arrangement, error) {
	if strings.TrimSpace(in) == "" {
		return nil, fmt.Errorf("--in is required (a project.json, arrangement.json, or .mid)")
	}
	low := strings.ToLower(in)
	switch {
	case strings.HasSuffix(low, ".mid"), strings.HasSuffix(low, ".midi"):
		data, err := os.ReadFile(in)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", in, err)
		}
		arr, perr := dawmodel.FromSMF(data)
		if perr != nil {
			return nil, fmt.Errorf("parse %s: %w", pathx.Base(in), perr)
		}
		return arr, nil
	case strings.HasSuffix(low, ".json"):
		return loadJSONSession(in)
	default:
		return nil, fmt.Errorf("unsupported input %q (want .json or .mid)", pathx.Base(in))
	}
}

// loadJSONSession loads a .json session: a becky-compose project (with stems) first,
// falling back to a raw dawmodel.Arrangement when the file is one.
func loadJSONSession(in string) (*dawmodel.Arrangement, error) {
	if proj, baseDir, err := composearr.LoadProject(in); err == nil {
		if arr, ferr := composearr.FromProject(proj, baseDir); ferr == nil && arr != nil && len(arr.Tracks) > 0 {
			return arr, nil
		}
	}
	// Fall back to a raw arrangement JSON.
	data, err := os.ReadFile(in)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", in, err)
	}
	var arr dawmodel.Arrangement
	if err := json.Unmarshal(data, &arr); err != nil {
		return nil, fmt.Errorf("parse %s as project or arrangement: %w", pathx.Base(in), err)
	}
	if len(arr.Tracks) == 0 {
		return nil, fmt.Errorf("%s has no tracks (not a becky-compose project or arrangement)", pathx.Base(in))
	}
	return &arr, nil
}

// writeSession writes the arrangement as an arrangement.json (becky-reaper build input)
// or, when --out ends in .mid, as a byte-stable SMF.
func writeSession(out string, arr *dawmodel.Arrangement) error {
	low := strings.ToLower(out)
	if strings.HasSuffix(low, ".mid") || strings.HasSuffix(low, ".midi") {
		if err := os.WriteFile(out, arr.ToSMF(), 0o644); err != nil {
			return fmt.Errorf("write %s: %w", out, err)
		}
		return nil
	}
	data, err := json.MarshalIndent(arr, "", "  ")
	if err != nil {
		return fmt.Errorf("encode arrangement: %w", err)
	}
	if err := os.WriteFile(out, data, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", out, err)
	}
	return nil
}

func fallbackSummary(s string) string {
	if strings.TrimSpace(s) == "" {
		return "couldn't turn that into an edit"
	}
	return s
}
