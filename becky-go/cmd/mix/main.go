// becky-mix — turn a becky-compose project into a deterministic mix plan.
//
//	becky-mix project.json [--profile jst] [--breakdown] [--prefs prefs.json]
//	          [--out mix.json] [--json]
//
// Reads a becky-compose project.json and LAYERS a deterministic mix onto it
// (SPEC-BECKY-MIX-JST.md): per-bus FX chains (gate/eq/comp/saturation order as
// data), the Joey Sturgis breakdown kick->low-end sidechain expressed as declared
// {from,to,kind:"sidechain"} edges, and per-bus VST preference slots (default
// "The Odin II" on guitar/lead). It NEVER edits the project (layering, not
// mutation) and is byte-stable: the SAME project + profile + prefs yield an
// identical mix.json. JST plugins are data-only optional VST equivalents — no
// audio is processed here.
//
// Pure-Go + offline. A missing/garbled project.json degrades to a partial plan
// with a plain note (exit 1), never a panic. Bad CLI usage exits 2.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"becky-go/internal/mixplan"
	"becky-go/internal/pathx"
)

func main() {
	profile := flag.String("profile", mixplan.ProfileJST, "mix profile id (currently: jst)")
	breakdown := flag.Bool("breakdown", false, "force the JST breakdown kick->low-end sidechain routine on")
	prefsPath := flag.String("prefs", "", "optional mix-preferences JSON (per-bus VST/preset overrides)")
	out := flag.String("out", "", "write mix.json here (default: alongside the project as mix.json)")
	asJSON := flag.Bool("json", false, "emit the mix plan as JSON to stdout instead of a plain report")
	flag.Parse()

	if flag.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "usage: becky-mix project.json [--profile jst] [--breakdown] [--prefs prefs.json] [--out mix.json] [--json]")
		os.Exit(2)
	}
	projPath := flag.Arg(0)

	raw, readErr := os.ReadFile(projPath)
	if readErr != nil {
		// Keep going with empty bytes so we still emit a (degraded) plan + note.
		fmt.Fprintf(os.Stderr, "warning: could not read %s: %v\n", projPath, readErr)
	}
	prefs, prefsErr := loadPrefs(*prefsPath)
	if prefsErr != nil {
		fmt.Fprintf(os.Stderr, "warning: ignoring --prefs (%v)\n", prefsErr)
	}

	src := mixplan.Load(raw, projPath)
	plan := mixplan.Build(src, mixplan.Options{Profile: *profile, Breakdown: *breakdown, Prefs: prefs})

	if err := emit(plan, projPath, *out, *asJSON); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	if src.Err != nil {
		os.Exit(1) // degraded: a partial plan was produced, but flag it for the caller
	}
}

// loadPrefs reads the optional mix-preferences file into VST overrides.
func loadPrefs(path string) ([]mixplan.VSTPreference, error) {
	if path == "" {
		return nil, nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var f struct {
		BusPreferences map[string]struct {
			VST    string `json:"vst"`
			Preset string `json:"preset"`
		} `json:"busPreferences"`
	}
	if err := json.Unmarshal(b, &f); err != nil {
		return nil, fmt.Errorf("parse prefs: %w", err)
	}
	out := make([]mixplan.VSTPreference, 0, len(f.BusPreferences))
	for bus, p := range f.BusPreferences {
		out = append(out, mixplan.VSTPreference{Bus: bus, VST: p.VST, Preset: p.Preset, FallbackToBuiltin: true})
	}
	return out, nil
}

// emit writes the plan: JSON to stdout, JSON to --out, or a plain report.
func emit(plan *mixplan.MixPlan, projPath, out string, asJSON bool) error {
	data, err := plan.Marshal()
	if err != nil {
		return err
	}
	if asJSON {
		_, err = os.Stdout.Write(data)
		return err
	}
	dest := out
	if dest == "" {
		dest = defaultOutput(projPath)
	}
	if err := os.WriteFile(dest, data, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", dest, err)
	}
	printReport(plan, dest)
	return nil
}

// defaultOutput sits the mix.json next to the source project. pathx.Dir is
// separator-agnostic so a Windows path resolves correctly even on Linux CI.
func defaultOutput(projPath string) string {
	dir := pathx.Dir(projPath)
	if dir == "" {
		return "mix.json"
	}
	return filepath.Join(dir, "mix.json")
}

// printReport writes a plain-language summary for a non-developer.
func printReport(plan *mixplan.MixPlan, dest string) {
	fmt.Println("becky-mix — Joey Sturgis mix plan, layered over your project")
	fmt.Println(strings.Repeat("=", 64))
	fmt.Printf("profile     : %s\n", plan.Profile)
	fmt.Printf("applies to  : %s (%s)\n", plan.AppliesTo, plan.AppliesToHash)
	fmt.Printf("wrote       : %s\n\n", dest)

	fmt.Printf("buses (%d), each with an ordered FX chain:\n", len(plan.Buses))
	for _, b := range plan.Buses {
		fmt.Printf("  - %-14s [%s] -> %s : %s\n", b.Bus, b.Role, b.Out, chainSummary(b.FX))
	}
	fmt.Println()

	if plan.BreakdownDetected {
		fmt.Printf("breakdown sidechain (kick -> low end), %d edges:\n", len(plan.BreakdownRouting))
		for _, e := range plan.BreakdownRouting {
			band := ""
			if e.Band != "" {
				band = " (" + e.Band + " band)"
			}
			fmt.Printf("  - %s -> %s%s: %s\n", e.From, e.To, band, e.Note)
		}
	} else {
		fmt.Println("breakdown sidechain: not engaged (no breakdown signal, or no isolated low-end bus)")
	}
	fmt.Println()

	if len(plan.VSTMap) > 0 {
		fmt.Println("per-bus VST preferences (built-in floor always available as fallback):")
		for _, v := range plan.VSTMap {
			fmt.Printf("  - %-14s : %s\n", v.Bus, v.VST)
		}
		fmt.Println()
	}
	for _, n := range plan.Notes {
		fmt.Printf("note: %s\n", n)
	}
}

// chainSummary renders an FX chain as a compact "gate -> eq -> comp" string.
func chainSummary(fx []mixplan.FXNode) string {
	if len(fx) == 0 {
		return "(no fx)"
	}
	parts := make([]string, 0, len(fx))
	for _, n := range fx {
		parts = append(parts, n.Type)
	}
	return strings.Join(parts, " -> ")
}
