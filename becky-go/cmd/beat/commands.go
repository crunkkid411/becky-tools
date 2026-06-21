// commands.go — the becky-beat subcommand handlers (new / randomize / euclid /
// mutate). Each parses its own flag set, drives internal/beatgen, and writes a
// dawmodel arrangement. Kept separate from main.go's plumbing for readability.
package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"becky-go/internal/beatgen"
	"becky-go/internal/pathx"
)

// runNew generates a fresh beat over the standard kit and writes it.
func runNew(args []string) int {
	fs := flag.NewFlagSet("new", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	out := fs.String("out", "", "output arrangement project.json (required)")
	genre := fs.String("genre", "straight", "genre prior ("+strings.Join(beatgen.GenreNames(), "/")+")")
	bars := fs.Int("bars", 1, "number of bars (each 16 steps)")
	seed := fs.Int64("seed", 7, "RNG seed (deterministic)")
	density := fs.Float64("density", 0, "override every lane's density (0..1); 0 = use the genre priors")
	bpm := fs.Int("bpm", 0, "tempo BPM (0 = dawmodel default)")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	if strings.TrimSpace(*out) == "" {
		fmt.Fprintln(os.Stderr, "becky-beat new: --out is required")
		return exitUsage
	}

	steps := 16 * clampBars(*bars)
	lanes := make([]beatgen.Lane, 0, len(standardKit))
	for _, role := range standardKit {
		lanes = append(lanes, beatgen.Lane{Name: role, Role: role})
	}
	pat := beatgen.NewPattern(steps, lanes...)
	var p *beatgen.Pattern
	if *density > 0 {
		// Explicit density override: set every lane and role-aware generate.
		for _, role := range standardKit {
			pat = pat.SetDensity(role, clamp01(*density))
		}
		p = pat.Generate(beatgen.DefaultGenerateOptions(), *seed)
	} else {
		// Genre priors drive per-lane density + onset placement.
		p = pat.GenerateGenre(strings.ToLower(strings.TrimSpace(*genre)), *seed)
	}

	arr, err := arrangementFromPattern(p, *bpm)
	if err != nil {
		fmt.Fprintln(os.Stderr, "becky-beat new:", err)
		return exitErr
	}
	if err := writeArrangement(*out, arr); err != nil {
		fmt.Fprintln(os.Stderr, "becky-beat new:", err)
		return exitErr
	}
	fmt.Printf("✓ generated a %d-bar %s beat (seed %d) — %d hits across %d lanes\n",
		clampBars(*bars), normGenre(*genre), *seed, arr.NoteCount(), len(p.Lanes))
	fmt.Printf("  wrote %s — open it in becky-canvas, tweak it with becky-drum, or play it with becky-daw-engine\n", *out)
	return exitOK
}

// runTransform loads an arrangement, applies a beatgen op to its drum pattern, and
// writes the result. kind selects the operation.
func runTransform(args []string, kind string) int {
	fs := flag.NewFlagSet(kind, flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	project := fs.String("project", "", "input arrangement project.json (required)")
	out := fs.String("out", "", "output (default <project>.beat.json next to source)")
	seed := fs.Int64("seed", 7, "RNG seed (deterministic)")
	density := fs.Float64("density", 0, "randomize: target density for every lane (0..1); 0 = keep current")
	amount := fs.Float64("amount", 0.2, "mutate: variation amount (0..1)")
	lane := fs.String("lane", "", "euclid: target lane (kick/snare/hat/...)")
	pulses := fs.Int("pulses", 0, "euclid: number of onsets to spread")
	stepsFlag := fs.Int("steps", 0, "euclid: lane length (0 = keep current)")
	rotate := fs.Int("rotate", 0, "euclid: rotation offset")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	if strings.TrimSpace(*project) == "" {
		fmt.Fprintf(os.Stderr, "becky-beat %s: --project is required\n", kind)
		return exitUsage
	}

	arr, err := loadArrangement(*project)
	if err != nil {
		fmt.Fprintln(os.Stderr, "becky-beat:", err)
		return exitErr
	}
	p, trackID, clipName, err := patternFromArrangement(arr)
	if err != nil {
		fmt.Fprintln(os.Stderr, "becky-beat:", err)
		return exitErr
	}

	var summary string
	switch kind {
	case "randomize":
		if *density > 0 {
			for _, ln := range p.Lanes {
				p = p.SetDensity(ln.Name, clamp01(*density))
			}
		}
		p = p.Generate(beatgen.DefaultGenerateOptions(), *seed)
		summary = fmt.Sprintf("randomized (seed %d)", *seed)
	case "mutate":
		p = p.Mutate(clamp01(*amount), *seed)
		summary = fmt.Sprintf("mutated by %.0f%% (seed %d)", clamp01(*amount)*100, *seed)
	case "remix":
		p = p.Remix(clamp01(*amount), *seed)
		summary = fmt.Sprintf("remixed (kept the vibe, %.0f%% nudge, seed %d)", clamp01(*amount)*100, *seed)
	case "euclid":
		if strings.TrimSpace(*lane) == "" || *pulses <= 0 {
			fmt.Fprintln(os.Stderr, "becky-beat euclid: --lane and --pulses are required")
			return exitUsage
		}
		if *stepsFlag > 0 {
			p = p.SetDensity(*lane, 0) // clear the lane to the requested length cleanly
		}
		p = p.ApplyEuclidean(*lane, *pulses, *rotate)
		summary = fmt.Sprintf("euclid %s: %d pulses (rotate %d)", *lane, *pulses, *rotate)
	default:
		fmt.Fprintf(os.Stderr, "becky-beat: unknown transform %q\n", kind)
		return exitUsage
	}

	grid := gridWithStepTicks(beatgen.ToDrumGrid(p))
	patched, err := arr.ApplyDrumGrid(trackID, clipName, grid)
	if err != nil {
		fmt.Fprintln(os.Stderr, "becky-beat: applying pattern:", err)
		return exitErr
	}
	outPath := defaultOut(*project, *out)
	if err := writeArrangement(outPath, patched); err != nil {
		fmt.Fprintln(os.Stderr, "becky-beat:", err)
		return exitErr
	}
	fmt.Printf("✓ %s — %d hits — wrote %s\n", summary, patched.NoteCount(), pathx.Base(outPath))
	return exitOK
}

// runVary writes N Remix variations of a beat (Playbeat's "give me variations"):
// each is a vibe-preserving nudge of the SAME source pattern at a distinct seed,
// so they're siblings rather than a drift. Files land as <base>.varN.json.
func runVary(args []string) int {
	fs := flag.NewFlagSet("vary", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	project := fs.String("project", "", "input arrangement project.json (required)")
	outdir := fs.String("outdir", "", "directory for the variations (default: next to the source)")
	count := fs.Int("count", 3, "how many variations to write (1..24)")
	seed := fs.Int64("seed", 7, "base RNG seed (deterministic)")
	amount := fs.Float64("amount", 0.25, "remix nudge amount (0..1)")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	if strings.TrimSpace(*project) == "" {
		fmt.Fprintln(os.Stderr, "becky-beat vary: --project is required")
		return exitUsage
	}
	n := *count
	if n < 1 {
		n = 1
	}
	if n > 24 {
		n = 24 // Playbeat maps 24 variations to 24 keys; cap to match
	}

	arr, err := loadArrangement(*project)
	if err != nil {
		fmt.Fprintln(os.Stderr, "becky-beat:", err)
		return exitErr
	}
	base, trackID, clipName, err := patternFromArrangement(arr)
	if err != nil {
		fmt.Fprintln(os.Stderr, "becky-beat:", err)
		return exitErr
	}

	dir := strings.TrimSpace(*outdir)
	if dir == "" {
		dir = pathx.Dir(*project)
	}
	if dir != "" && dir != "." {
		if mkErr := os.MkdirAll(dir, 0o755); mkErr != nil {
			fmt.Fprintln(os.Stderr, "becky-beat vary: create outdir:", mkErr)
			return exitErr
		}
	}
	stem := pathx.Base(*project)
	if i := strings.LastIndex(stem, "."); i > 0 {
		stem = stem[:i]
	}

	written := 0
	for i := 1; i <= n; i++ {
		// Each variation remixes the ORIGINAL pattern at a distinct seed so the
		// set is a fan of siblings, not a cumulative drift.
		v := base.Remix(clamp01(*amount), *seed+int64(i))
		grid := gridWithStepTicks(beatgen.ToDrumGrid(v))
		patched, aerr := arr.ApplyDrumGrid(trackID, clipName, grid)
		if aerr != nil {
			fmt.Fprintf(os.Stderr, "becky-beat vary: variation %d: %v\n", i, aerr)
			continue
		}
		outPath := joinPath(dir, fmt.Sprintf("%s.var%d.json", stem, i))
		if werr := writeArrangement(outPath, patched); werr != nil {
			fmt.Fprintln(os.Stderr, "becky-beat vary:", werr)
			continue
		}
		written++
		fmt.Printf("  var%d → %s (%d hits)\n", i, pathx.Base(outPath), patched.NoteCount())
	}
	if written == 0 {
		fmt.Fprintln(os.Stderr, "becky-beat vary: no variations written")
		return exitErr
	}
	fmt.Printf("✓ wrote %d variations of %s\n", written, pathx.Base(*project))
	return exitOK
}

// joinPath joins a directory and filename, tolerating an empty/"." directory.
func joinPath(dir, name string) string {
	if dir == "" || dir == "." {
		return name
	}
	return dir + "/" + name
}

func clampBars(b int) int {
	if b < 1 {
		return 1
	}
	if b > 64 {
		return 64
	}
	return b
}

func clamp01(f float64) float64 {
	if f < 0 {
		return 0
	}
	if f > 1 {
		return 1
	}
	return f
}

// normGenre lowercases a genre and falls back to the default ("straight") when
// beatgen does not recognise it — matching GenerateGenre's own degrade behavior so
// the printed summary never claims a genre the engine didn't actually use.
func normGenre(g string) string {
	g = strings.ToLower(strings.TrimSpace(g))
	for _, known := range beatgen.GenreNames() {
		if known == g {
			return g
		}
	}
	return "straight"
}
