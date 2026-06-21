// becky-arrange — becky's deterministic, stem-aware arrangement builder. It adds
// ONE musical layer at a time to an existing project, each layer written AWARE of
// the stems already there (the bass locks to your actual kick, chords/melody stay
// in key). Pure music-theory math — instant, no model, no tokens — and the output
// is editable MIDI.
//
//	becky-arrange add bass    --project beat.json [--genre house] [--seed 1] [--out out.json]
//	becky-arrange add chords  --project beat.json [--genre crunkcore]
//	becky-arrange add melody  --project beat.json
//	becky-arrange next        --project beat.json     # which layer to build next
//
// The build ORDER (ACE-Step-DAW's three music skills agree; see ARRANGEMENT-RULES.md):
//
//	drums → bass → chords → melody → texture
//
// Input is a dawmodel arrangement project.json, or a becky-compose routing manifest
// (its .mid stems are resolved automatically). Non-destructive: writes a new file.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"becky-go/internal/arrange"
	"becky-go/internal/composearr"
	"becky-go/internal/ctledit"
	"becky-go/internal/dawmodel"
	"becky-go/internal/musictheory"
	"becky-go/internal/pathx"
)

const (
	exitOK    = 0
	exitErr   = 1
	exitUsage = 2
)

func main() { os.Exit(run(os.Args[1:])) }

func run(args []string) int {
	if len(args) == 0 {
		usage()
		return exitUsage
	}
	switch args[0] {
	case "add":
		return runAdd(args[1:])
	case "next":
		return runNext(args[1:])
	case "status", "describe":
		return runStatus(args[1:])
	case "analyze":
		return runAnalyze(args[1:])
	case "jam":
		return runJam(args[1:])
	case "-h", "--help", "help":
		usage()
		return exitOK
	default:
		fmt.Fprintf(os.Stderr, "becky-arrange: unknown command %q\n", args[0])
		usage()
		return exitUsage
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "becky-arrange — build a track one stem-aware layer at a time (deterministic, no model)")
	fmt.Fprintln(os.Stderr, "usage:")
	fmt.Fprintln(os.Stderr, "  becky-arrange add <bass|chords|melody> --project p.json [--genre g] [--seed n] [--out o]")
	fmt.Fprintln(os.Stderr, "  becky-arrange next --project p.json")
	fmt.Fprintln(os.Stderr, "order: drums -> bass -> chords -> melody -> texture (each layer fits the ones before it)")
}

func runAdd(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "becky-arrange add: name a layer (bass|chords|melody)")
		return exitUsage
	}
	role := args[0]
	fs := newFlags("add " + role)
	project := fs.project
	genre := fs.genre
	seed := fs.seed
	out := fs.out
	if err := fs.set.Parse(args[1:]); err != nil {
		return exitUsage
	}
	if strings.TrimSpace(*project) == "" {
		fmt.Fprintln(os.Stderr, "becky-arrange add: --project is required")
		return exitUsage
	}
	arr, err := loadArrangement(*project)
	if err != nil {
		fmt.Fprintln(os.Stderr, "becky-arrange:", err)
		return exitErr
	}
	next, err := arrange.AddLayer(arr, role, arrange.Options{Genre: *genre, Seed: *seed})
	if err != nil {
		fmt.Fprintln(os.Stderr, "becky-arrange:", err)
		return exitErr
	}
	outPath := defaultOut(*project, *out, role)
	if err := writeArrangement(outPath, next); err != nil {
		fmt.Fprintln(os.Stderr, "becky-arrange:", err)
		return exitErr
	}
	fmt.Printf("✓ added %s (%d notes total) — wrote %s\n", role, next.NoteCount(), pathx.Base(outPath))
	// becky checks its OWN output against the universal constraints before trusting it
	// (STANDARDS-MUSIC-RESEARCH.md §7 / the evaluation checklist).
	for _, iss := range musictheory.Evaluate(next) {
		where := iss.Track
		if where == "" {
			where = "arrangement"
		}
		fmt.Printf("  ⚠ %s [%s]: %s\n", iss.Check, where, iss.Note)
	}
	if s := arrange.SuggestNext(next); s != "" {
		fmt.Printf("  next, becky suggests: add %s\n", s)
	}
	return exitOK
}

func runNext(args []string) int {
	fs := newFlags("next")
	project := fs.project
	if err := fs.set.Parse(args); err != nil {
		return exitUsage
	}
	if strings.TrimSpace(*project) == "" {
		fmt.Fprintln(os.Stderr, "becky-arrange next: --project is required")
		return exitUsage
	}
	arr, err := loadArrangement(*project)
	if err != nil {
		fmt.Fprintln(os.Stderr, "becky-arrange:", err)
		return exitErr
	}
	s := arrange.SuggestNext(arr)
	if s == "" {
		fmt.Println("the arrangement has every layer — nothing to add")
		return exitOK
	}
	fmt.Printf("next layer to build: %s\n", s)
	return exitOK
}

// runAnalyze reports the arrangement's gaps + the next step (deterministic opinion).
func runAnalyze(args []string) int {
	fs := newFlags("analyze")
	project := fs.project
	if err := fs.set.Parse(args); err != nil {
		return exitUsage
	}
	if strings.TrimSpace(*project) == "" {
		fmt.Fprintln(os.Stderr, "becky-arrange analyze: --project is required")
		return exitUsage
	}
	arr, err := loadArrangement(*project)
	if err != nil {
		fmt.Fprintln(os.Stderr, "becky-arrange:", err)
		return exitErr
	}
	for _, f := range arrange.Analyze(arr) {
		mark := "•"
		switch f.Kind {
		case "missing_layer":
			mark = "○"
		case "empty_track":
			mark = "⚠"
		case "suggestion":
			mark = "→"
		}
		fmt.Printf("  %s %s\n", mark, f.Note)
	}
	return exitOK
}

// runJam advances the arrangement by one stem-aware layer (drums → bass → chords →
// melody). Repeatable; --all fills every remaining layer in one go.
func runJam(args []string) int {
	fs := newFlags("jam")
	project := fs.project
	genre := fs.genre
	seed := fs.seed
	out := fs.out
	all := fs.set.Bool("all", false, "fill every remaining layer, not just the next one")
	if err := fs.set.Parse(args); err != nil {
		return exitUsage
	}
	if strings.TrimSpace(*project) == "" {
		fmt.Fprintln(os.Stderr, "becky-arrange jam: --project is required")
		return exitUsage
	}
	arr, err := loadArrangement(*project)
	if err != nil {
		fmt.Fprintln(os.Stderr, "becky-arrange:", err)
		return exitErr
	}
	added := []string{}
	for {
		next, layer, jerr := arrange.Jam(arr, arrange.Options{Genre: *genre, Seed: *seed})
		if jerr != nil {
			fmt.Fprintln(os.Stderr, "becky-arrange:", jerr)
			return exitErr
		}
		if layer == "" {
			break
		}
		arr = next
		added = append(added, layer)
		if !*all {
			break
		}
	}
	if len(added) == 0 {
		fmt.Println("nothing to add — every core layer is present")
		return exitOK
	}
	outPath := defaultOut(*project, *out, "jam")
	if err := writeArrangement(outPath, arr); err != nil {
		fmt.Fprintln(os.Stderr, "becky-arrange:", err)
		return exitErr
	}
	fmt.Printf("✓ jammed: added %s — wrote %s\n", strings.Join(added, " → "), pathx.Base(outPath))
	for _, iss := range musictheory.Evaluate(arr) {
		fmt.Printf("  ⚠ %s [%s]: %s\n", iss.Check, orStr2(iss.Track, "arrangement"), iss.Note)
	}
	if s := arrange.SuggestNext(arr); s != "" {
		fmt.Printf("  next: add %s\n", s)
	}
	return exitOK
}

func orStr2(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

// runStatus prints the addressable scene (tracks/clips/lanes/mix) — the introspection
// an agent uses to discover what it can act on (the dual-operability rule). --json
// emits the machine-readable SceneInfo.
func runStatus(args []string) int {
	fs := newFlags("status")
	project := fs.project
	asJSON := fs.set.Bool("json", false, "emit machine-readable JSON")
	if err := fs.set.Parse(args); err != nil {
		return exitUsage
	}
	if strings.TrimSpace(*project) == "" {
		fmt.Fprintln(os.Stderr, "becky-arrange status: --project is required")
		return exitUsage
	}
	arr, err := loadArrangement(*project)
	if err != nil {
		fmt.Fprintln(os.Stderr, "becky-arrange:", err)
		return exitErr
	}
	scene := ctledit.Describe(arr)
	if *asJSON {
		data, _ := json.MarshalIndent(scene, "", "  ")
		fmt.Println(string(data))
		return exitOK
	}
	key := scene.Root
	if scene.Scale != "" {
		key += " " + scene.Scale
	}
	fmt.Printf("%d BPM · key %s · %d track(s)\n", scene.BPM, strings.TrimSpace(key), len(scene.Tracks))
	for _, tr := range scene.Tracks {
		flags := ""
		if tr.Muted {
			flags += " [muted]"
		}
		if tr.Soloed {
			flags += " [solo]"
		}
		fmt.Printf("  • %-10s %s%s\n", tr.ID, tr.Kind, flags)
		for _, c := range tr.Clips {
			lanes := ""
			if len(c.Lanes) > 0 {
				lanes = "  lanes: " + strings.Join(c.Lanes, ",")
			}
			fmt.Printf("      - %-10s %d notes%s\n", c.Name, c.Notes, lanes)
		}
	}
	if s := arrange.SuggestNext(arr); s != "" {
		fmt.Printf("next layer to build: %s\n", s)
	}
	return exitOK
}

// loadArrangement reads a dawmodel arrangement, transparently resolving a
// becky-compose routing manifest (its .mid stems) the way becky-drum does.
func loadArrangement(path string) (*dawmodel.Arrangement, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", pathx.Base(path), err)
	}
	if isComposeManifest(data) {
		proj, baseDir, perr := composearr.LoadProject(path)
		if perr != nil {
			return nil, fmt.Errorf("parse %s: %w", pathx.Base(path), perr)
		}
		arr, _ := composearr.FromProject(proj, baseDir)
		if arr == nil {
			return nil, fmt.Errorf("parse %s: produced no arrangement", pathx.Base(path))
		}
		return arr, nil
	}
	var arr dawmodel.Arrangement
	if err := json.Unmarshal(data, &arr); err != nil {
		return nil, fmt.Errorf("parse %s: not a valid project (%w)", pathx.Base(path), err)
	}
	return &arr, nil
}

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

func writeArrangement(path string, arr *dawmodel.Arrangement) error {
	data, err := json.MarshalIndent(arr, "", "  ")
	if err != nil {
		return fmt.Errorf("encode: %w", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", pathx.Base(path), err)
	}
	return nil
}

// defaultOut derives "<project>.<role>.json" next to the source when --out is empty.
func defaultOut(project, out, role string) string {
	if strings.TrimSpace(out) != "" {
		return out
	}
	base := pathx.Base(project)
	if i := strings.LastIndex(base, "."); i > 0 {
		base = base[:i]
	}
	dir := pathx.Dir(project)
	name := base + "." + role + ".json"
	if dir == "" || dir == "." {
		return name
	}
	return dir + "/" + name
}
