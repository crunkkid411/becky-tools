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

// genreDensity holds a per-role onset density (0..1) for a few genres. These are
// honest, simple priors — not a trained model — that bias how busy each voice is
// at generation time. An unknown genre falls back to "default".
var genreDensity = map[string]map[string]float64{
	"default": {"kick": 0.30, "snare": 0.20, "clap": 0.12, "hat": 0.55, "ohat": 0.15, "rim": 0.05, "tom": 0.05, "ride": 0.05},
	"trap":    {"kick": 0.28, "snare": 0.13, "clap": 0.13, "hat": 0.80, "ohat": 0.12, "rim": 0.06, "tom": 0.06, "ride": 0.04},
	"house":   {"kick": 1.00, "snare": 0.00, "clap": 0.25, "hat": 0.50, "ohat": 0.50, "rim": 0.04, "tom": 0.04, "ride": 0.10},
	"techno":  {"kick": 1.00, "snare": 0.00, "clap": 0.13, "hat": 0.60, "ohat": 0.30, "rim": 0.10, "tom": 0.06, "ride": 0.06},
	"dnb":     {"kick": 0.22, "snare": 0.16, "clap": 0.06, "hat": 0.70, "ohat": 0.20, "rim": 0.10, "tom": 0.08, "ride": 0.10},
	"rock":    {"kick": 0.30, "snare": 0.25, "clap": 0.00, "hat": 0.50, "ohat": 0.05, "rim": 0.05, "tom": 0.08, "ride": 0.20},
}

// genreNames lists the known genres in stable order for the usage hint.
var genreNames = []string{"default", "trap", "house", "techno", "dnb", "rock"}

// runNew generates a fresh beat over the standard kit and writes it.
func runNew(args []string) int {
	fs := flag.NewFlagSet("new", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	out := fs.String("out", "", "output arrangement project.json (required)")
	genre := fs.String("genre", "default", "genre prior ("+strings.Join(genreNames, "/")+")")
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
	priors := genreDensity[strings.ToLower(strings.TrimSpace(*genre))]
	if priors == nil {
		priors = genreDensity["default"]
	}
	lanes := make([]beatgen.Lane, 0, len(standardKit))
	for _, role := range standardKit {
		d := priors[role]
		if *density > 0 {
			d = clamp01(*density)
		}
		lanes = append(lanes, beatgen.Lane{Name: role, Role: role, Density: d})
	}
	p := beatgen.NewPattern(steps, lanes...).Generate(beatgen.DefaultGenerateOptions(), *seed)

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

func normGenre(g string) string {
	g = strings.ToLower(strings.TrimSpace(g))
	if _, ok := genreDensity[g]; ok {
		return g
	}
	return "default"
}
