// becky-drum — the AI drum machine: say what you want in plain English, see the
// beat change, approve it. ("show me, don't do it" — nothing is destructive.)
//
//	becky-drum --project project.json --instruction "make it half-time"
//	becky-drum --project p.json --instruction "humanize the snare" --lane snare
//	becky-drum --project p.json --instruction "give me 3 variations" --dry-run
//
// It reads a project.json (a dawmodel.Arrangement, the same format becky-compose
// writes and becky-daw-engine plays), derives the drum grid from its first
// percussion clip, parses the instruction into a DrumCommand (deterministic
// keyword parser, with a documented local-model path that silent-degrades to it),
// applies the transform, and writes the patched project back next to the source.
//
// --dry-run prints the BEFORE/AFTER grids + a plain-English summary and writes
// nothing. Every applied transform is logged to drum.corrections.jsonl (becky's
// preference-learning substrate) best-effort.
//
// Offline + deterministic: same project + instruction + seed ⇒ byte-identical
// output. Degrade-never-crash: an unrecognised instruction exits 0 with a
// friendly note and the project untouched.
//
// Exit codes: 0 = ok (incl. unknown-instruction degrade), 1 = IO/runtime error,
// 2 = usage error.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"becky-go/internal/composearr"
	"becky-go/internal/dawmodel"
	"becky-go/internal/drumcmd"
	"becky-go/internal/habits"
	"becky-go/internal/pathx"
)

const (
	exitOK    = 0
	exitErr   = 1
	exitUsage = 2
)

func main() { os.Exit(run(os.Args[1:])) }

// run is the testable entrypoint: returns the exit code instead of os.Exit.
func run(args []string) int {
	fs := flag.NewFlagSet("becky-drum", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	project := fs.String("project", "", "input project.json (a dawmodel arrangement)")
	instruction := fs.String("instruction", "", "plain-English drum instruction, e.g. \"make it half-time\"")
	lane := fs.String("lane", "", "override the target lane (snare/hat/kick/…); empty = let the instruction decide")
	output := fs.String("output", "", "output project.json (default: <project>.drum.json next to the source)")
	dryRun := fs.Bool("dry-run", false, "preview the before/after + summary without writing")
	seed := fs.Int64("seed", drumcmd.DefaultSeed, "RNG seed for humanize/variations (deterministic)")
	asJSON := fs.Bool("json", false, "emit the full Result (before/after/variants) as JSON")
	fs.Usage = func() { usage(fs) }

	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	if strings.TrimSpace(*project) == "" {
		fmt.Fprintln(os.Stderr, "becky-drum: --project is required")
		usage(fs)
		return exitUsage
	}
	if strings.TrimSpace(*instruction) == "" {
		fmt.Fprintln(os.Stderr, "becky-drum: --instruction is required (what should I do to the beat?)")
		usage(fs)
		return exitUsage
	}

	// 1. Load the project.
	arr, err := loadProject(*project)
	if err != nil {
		fmt.Fprintln(os.Stderr, "becky-drum:", err)
		return exitErr
	}

	// 2. Locate the drum grid (first percussion-ish MIDI clip).
	trackID, clipName, grid, err := deriveDrumGrid(arr)
	if err != nil {
		fmt.Fprintln(os.Stderr, "becky-drum:", err)
		return exitErr
	}

	// 3. Parse the instruction → DrumCommand (model path degrades to keywords).
	cmd := drumcmd.PickParser().Parse(*instruction, drumcmd.SummarizeGrid(grid), *seed)
	if strings.TrimSpace(*lane) != "" {
		cmd.Lane = strings.ToLower(strings.TrimSpace(*lane)) // CLI override wins
	}

	// 4. Apply (immutable; never panics).
	res, err := drumcmd.Apply(grid, cmd)
	if err != nil {
		fmt.Fprintln(os.Stderr, "becky-drum:", err)
		return exitErr
	}

	if *asJSON {
		return emitJSON(res)
	}

	// 5. Always SHOW (before/after + summary) — "show me, don't do it".
	printPreview(res)

	// Unknown instruction: degrade gracefully, write nothing, exit 0.
	if res.Action == drumcmd.Unknown {
		return exitOK
	}

	if *dryRun {
		fmt.Printf("(dry run — nothing written. Drop --dry-run to apply.)\n")
		return exitOK
	}

	if !res.Changed {
		fmt.Printf("✓ Nothing to change — the beat already matches that. (no file written)\n")
		return exitOK
	}

	// 6. Apply the grid back into the arrangement and write the patched project.
	patched, err := arr.ApplyDrumGrid(trackID, clipName, res.After)
	if err != nil {
		fmt.Fprintln(os.Stderr, "becky-drum: applying grid to project:", err)
		return exitErr
	}
	outPath := resolveOutput(*project, *output)
	if err := writeProject(outPath, patched); err != nil {
		fmt.Fprintln(os.Stderr, "becky-drum:", err)
		return exitErr
	}

	// 7. Log the transform as a correction (preference learning), best-effort.
	logTransform(*project, outPath, clipName, res, cmd)

	fmt.Printf("✓ %s — wrote %s\n", capitalize(res.Summary), pathx.Base(outPath))
	return exitOK
}

func usage(fs *flag.FlagSet) {
	fmt.Fprintln(os.Stderr, "becky-drum — the AI drum machine (plain-English beat edits, with preview)")
	fmt.Fprintln(os.Stderr, "usage:")
	fmt.Fprintln(os.Stderr, "  becky-drum --project p.json --instruction \"make it half-time\" [--lane snare] [--output out.json] [--dry-run] [--seed 42]")
	fmt.Fprintln(os.Stderr, "examples of instructions becky understands:")
	fmt.Fprintln(os.Stderr, "  make it half-time / double-time it")
	fmt.Fprintln(os.Stderr, "  humanize the snare / humanize the drums")
	fmt.Fprintln(os.Stderr, "  add a hi-hat roll into beat 4 / add a fill")
	fmt.Fprintln(os.Stderr, "  swing it / more swing")
	fmt.Fprintln(os.Stderr, "  give me 3 variations")
	fmt.Fprintln(os.Stderr, "  make it busier / strip it back")
	fmt.Fprintln(os.Stderr, "  tighten it to the grid")
	fmt.Fprintln(os.Stderr, "flags:")
	fs.PrintDefaults()
}

// loadProject reads a project.json into a dawmodel.Arrangement. A bad file or bad
// JSON degrades to a wrapped error (never a panic).
//
// It transparently accepts TWO shapes:
//   - a dawmodel.Arrangement with inline notes (what becky-daw writes), and
//   - a becky-compose ROUTING MANIFEST (tracks that reference external .mid
//     stems). A compose manifest has no inline notes, so it is routed through
//     internal/composearr, which loads each stem into a real Arrangement. This is
//     what makes "compose a beat, then tweak it in plain English" actually work.
func loadProject(path string) (*dawmodel.Arrangement, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", pathx.Base(path), err)
	}
	if isComposeManifest(data) {
		proj, baseDir, perr := composearr.LoadProject(path)
		if perr != nil {
			return nil, fmt.Errorf("parse %s: not a valid compose project (%w)", pathx.Base(path), perr)
		}
		// FromProject degrades on missing stems (partial arrangement + wrapped
		// error). A drum tweak only needs the percussion stem, so a missing
		// melodic stem must not be fatal — we keep the arrangement and ignore the
		// partial-load note here; deriveDrumGrid reports if the drum clip is gone.
		arr, _ := composearr.FromProject(proj, baseDir)
		if arr == nil {
			return nil, fmt.Errorf("parse %s: compose project produced no arrangement", pathx.Base(path))
		}
		return arr, nil
	}
	var arr dawmodel.Arrangement
	if err := json.Unmarshal(data, &arr); err != nil {
		return nil, fmt.Errorf("parse %s: not a valid project.json (%w)", pathx.Base(path), err)
	}
	return &arr, nil
}

// isComposeManifest reports whether raw JSON is a becky-compose routing manifest
// (as opposed to a dawmodel.Arrangement). The tell is the "becky-compose" tool
// stamp or tracks that reference an external .mid stem — a dawmodel arrangement
// carries inline notes instead. Best-effort: unparseable JSON returns false so
// the caller falls through to the normal arrangement decode + its error.
func isComposeManifest(data []byte) bool {
	var probe struct {
		Tool   string `json:"tool"`
		Tracks []struct {
			Midi string `json:"midi"`
		} `json:"tracks"`
	}
	if err := json.Unmarshal(data, &probe); err != nil {
		return false
	}
	if strings.EqualFold(strings.TrimSpace(probe.Tool), "becky-compose") {
		return true
	}
	for _, t := range probe.Tracks {
		if strings.TrimSpace(t.Midi) != "" {
			return true
		}
	}
	return false
}

// deriveDrumGrid finds the drum clip in the arrangement and derives its grid.
// It prefers a clip on MIDI channel 9 (GM percussion) or program -1; otherwise it
// falls back to the first MIDI clip — mirroring becky-daw's resolveTarget logic.
func deriveDrumGrid(arr *dawmodel.Arrangement) (trackID, clipName string, grid *dawmodel.DrumGrid, err error) {
	trackID, clipName = findDrumClip(arr)
	if trackID == "" || clipName == "" {
		return "", "", nil, fmt.Errorf("no MIDI drum clip found in the project — nothing to change")
	}
	g, gerr := arr.DrumGridOf(trackID, clipName, 0)
	if gerr != nil {
		return "", "", nil, fmt.Errorf("reading the drum grid: %w", gerr)
	}
	return trackID, clipName, g, nil
}

// findDrumClip returns the (track, clip) of the best drum candidate. It scans in
// priority order so a real GM-percussion pattern always wins over a weak signal:
//  1. a non-empty clip on MIDI channel 9 (GM percussion) — the strongest signal;
//  2. a non-empty clip with program -1 (percussion/unknown);
//  3. any non-empty MIDI clip;
//  4. as a last resort, the first MIDI clip even if empty.
//
// Channel 9 is checked before program -1 because program -1 also marks tracks
// whose instrument is simply unknown (e.g. a melodic track loaded from a bare
// SMF), which would otherwise shadow the actual drum clip.
func findDrumClip(arr *dawmodel.Arrangement) (string, string) {
	var ch9, prog, nonEmpty, anyClip [2]string
	for _, t := range arr.Tracks {
		if t.Kind != "" && t.Kind != dawmodel.KindMIDI {
			continue
		}
		for _, c := range t.Clips {
			hasNotes := len(c.Notes) > 0
			if c.Channel == 9 && hasNotes && ch9[0] == "" {
				ch9 = [2]string{t.ID, c.Name}
			}
			if c.Program == -1 && hasNotes && prog[0] == "" {
				prog = [2]string{t.ID, c.Name}
			}
			if hasNotes && nonEmpty[0] == "" {
				nonEmpty = [2]string{t.ID, c.Name}
			}
			if anyClip[0] == "" {
				anyClip = [2]string{t.ID, c.Name}
			}
		}
	}
	for _, cand := range [][2]string{ch9, prog, nonEmpty, anyClip} {
		if cand[0] != "" {
			return cand[0], cand[1]
		}
	}
	return "", ""
}

// resolveOutput returns the output path. When --output is empty it derives
// "<source-without-ext>.drum.json" in the SOURCE directory, using pathx so a
// Windows-style project path is handled correctly even on Linux/CI.
func resolveOutput(project, output string) string {
	if strings.TrimSpace(output) != "" {
		return output
	}
	dir := pathx.Dir(project)
	base := pathx.Base(project)
	if i := strings.LastIndex(base, "."); i > 0 {
		base = base[:i]
	}
	name := base + ".drum.json"
	if dir == "" {
		return name
	}
	return filepath.Join(dir, name)
}

// writeProject marshals the arrangement back to indented JSON and writes it.
func writeProject(path string, arr *dawmodel.Arrangement) error {
	data, err := json.MarshalIndent(arr, "", "  ")
	if err != nil {
		return fmt.Errorf("encode project: %w", err)
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", pathx.Base(path), err)
	}
	return nil
}

// emitJSON prints the full Result as indented JSON (for the GUI / scripting).
func emitJSON(v any) int {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		fmt.Fprintln(os.Stderr, "becky-drum: encode:", err)
		return exitErr
	}
	return exitOK
}

// printPreview renders the "show me, don't do it" before/after, plain-text. For
// variations it lists each variant's hit count so Jordan can eyeball the spread.
func printPreview(res *drumcmd.Result) {
	fmt.Printf("becky-drum: %s\n", res.Summary)
	if res.Before == nil {
		return
	}
	fmt.Println("\nBEFORE:")
	printGrid(res.Before)
	if res.Action == drumcmd.Variations && len(res.Variants) > 0 {
		for i, v := range res.Variants {
			label := "AFTER"
			if i == 0 {
				label = "VARIANT 1 (as-is)"
			} else {
				label = fmt.Sprintf("VARIANT %d", i+1)
			}
			fmt.Printf("\n%s:\n", label)
			printGrid(v)
		}
		return
	}
	if res.After != nil {
		fmt.Println("\nAFTER:")
		printGrid(res.After)
	}
}

// printGrid prints one lane per row as a 'x'/'.' step string — colours & shapes
// over text is the canvas's job; the CLI keeps it scannable for a human.
func printGrid(g *dawmodel.DrumGrid) {
	for _, ln := range g.Lanes {
		var b strings.Builder
		for s, on := range ln.On {
			if s > 0 && s%4 == 0 {
				b.WriteByte(' ')
			}
			if on {
				b.WriteByte('x')
			} else {
				b.WriteByte('.')
			}
		}
		fmt.Printf("  %-6s %s\n", ln.Name, b.String())
	}
}

// logTransform appends a drum.corrections.jsonl line so becky learns Jordan's
// preferences (the existing internal/habits contract). Best-effort: a write
// failure prints a warning and never affects the exit code or the result.
//
//	scope = clip name (which beat he was editing)
//	field = the action (which transform he chose)
//	auto  = "" (becky proposed nothing; this is his explicit instruction)
//	fixed = the action + any params (what he asked for)
func logTransform(project, out, clip string, res *drumcmd.Result, cmd drumcmd.DrumCommand) {
	dir := pathx.Dir(out)
	if dir == "" {
		dir = pathx.Dir(project)
	}
	logPath := "drum.corrections.jsonl"
	if dir != "" {
		logPath = filepath.Join(dir, "drum.corrections.jsonl")
	}
	fixed := res.Action.String()
	if cmd.Lane != "" {
		fixed += ":" + cmd.Lane
	}
	if cmd.Beat > 0 {
		fixed += fmt.Sprintf(":beat%d", cmd.Beat)
	}
	if err := habits.AppendCorrectionLog(logPath, "drum", clip, res.Action.String(), "", fixed); err != nil {
		fmt.Fprintf(os.Stderr, "warn: correction log: %v\n", err)
	}
}

// capitalize upper-cases the first rune of s (for the success line).
func capitalize(s string) string {
	if s == "" {
		return s
	}
	r := []rune(s)
	if r[0] >= 'a' && r[0] <= 'z' {
		r[0] -= 'a' - 'A'
	}
	return string(r)
}
