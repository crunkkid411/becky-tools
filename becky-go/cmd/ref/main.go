// becky-ref — reference matching: measure how YOUR stem differs from a reference
// stem that already sounds the way it should, and tell you the EXACT moves to match
// it. This is the deterministic heavy-lifting a premium reference-matching plugin
// does: it compares tonal balance (fixed log-spaced bands), loudness and dynamics,
// then prints a plain-English MATCH PLAN — "+2.5 dB around 3 kHz", "turn up 2 dB",
// "ease off ~1 dB of compression" — and emits the full structured plan as JSON so it
// can later feed becky-wire / becky-mix.
//
//	becky-ref profile --wav reference.wav [--out ref.profile.json] [--k-weight]
//	becky-ref match --reference ref.wav --mine mine.wav [--out plan.json] [--json] [--k-weight]
//	becky-ref match --profile ref.profile.json --mine mine.wav [--out plan.json] [--json]
//
// SCOPE: mono spectral / loudness / dynamics matching (dsp.DecodeWAV downmixes to
// mono). Stereo width is explicitly out of scope. Loudness is RMS dBFS (or a labelled
// K-weight approximation with --k-weight) — relative, NOT certified LUFS. The output
// says so. Offline + deterministic: same bytes in -> identical plan out.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"becky-go/internal/dsp"
	"becky-go/internal/habits"
	"becky-go/internal/music"
	"becky-go/internal/pathx"
	"becky-go/internal/refmatch"
	"becky-go/internal/stemscan"
)

// newFlagSet builds a ContinueOnError flag set so a parse failure returns exit 2
// (usage error) instead of os.Exit-ing from inside flag.
func newFlagSet(name string) *flag.FlagSet {
	return flag.NewFlagSet(name, flag.ContinueOnError)
}

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	switch os.Args[1] {
	case "profile":
		os.Exit(runProfile(os.Args[2:]))
	case "match":
		os.Exit(runMatch(os.Args[2:]))
	case "apply":
		os.Exit(runApply(os.Args[2:]))
	case "library":
		os.Exit(runLibrary(os.Args[2:]))
	case "-h", "--help", "help":
		usage()
		os.Exit(0)
	default:
		fmt.Fprintf(os.Stderr, "becky-ref: unknown subcommand %q\n", os.Args[1])
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage:")
	fmt.Fprintln(os.Stderr, "  becky-ref profile --wav reference.wav [--out ref.profile.json] [--k-weight]")
	fmt.Fprintln(os.Stderr, "  becky-ref match --reference ref.wav --mine mine.wav [--out plan.json] [--json] [--k-weight]")
	fmt.Fprintln(os.Stderr, "  becky-ref match --profile ref.profile.json --mine mine.wav [--out plan.json] [--json]")
	fmt.Fprintln(os.Stderr, "  becky-ref match --library house.json --mine mine.wav [--out plan.json] [--json] [--remember <role>]")
	fmt.Fprintln(os.Stderr, "  becky-ref apply --plan plan.json --project project.json --bus bus.drums [--output out.json] [--dry-run]")
	fmt.Fprintln(os.Stderr, "  becky-ref library build --dir <folder> [--out house.json] [--recursive]")
}

// --- profile subcommand: extract a reusable target Profile from a reference WAV ---

func runProfile(argv []string) int {
	fs := newFlagSet("profile")
	wav := fs.String("wav", "", "reference audio file to fingerprint")
	out := fs.String("out", "", "output JSON path (default: <wav-base>.profile.json next to source)")
	kw := fs.Bool("k-weight", false, "apply becky's K-weight loudness approximation (NOT certified LUFS)")
	asJSON := fs.Bool("json", false, "print the profile JSON to stdout")
	if err := fs.Parse(argv); err != nil {
		return 2
	}
	if *wav == "" {
		fmt.Fprintln(os.Stderr, "becky-ref profile: need --wav")
		return 2
	}

	a, err := decode(*wav)
	if err != nil {
		fmt.Fprintf(os.Stderr, "becky-ref: %v\n", err)
		return 1 // truly unreadable input
	}
	prof := refmatch.Analyze(a, refmatch.Options{KWeight: *kw})
	prof.Source = pathx.Base(*wav)

	dst := *out
	if dst == "" {
		dst = sidecar(*wav, ".profile.json")
	}
	if err := writeJSON(dst, prof); err != nil {
		fmt.Fprintf(os.Stderr, "becky-ref: %v\n", err)
		return 1
	}

	if *asJSON {
		printJSON(prof)
	} else {
		printProfile(prof, dst)
	}
	if prof.Degraded {
		return 1
	}
	return 0
}

// --- match subcommand: compare mine against a reference (WAV or saved profile) ---

func runMatch(argv []string) int {
	fs := newFlagSet("match")
	reference := fs.String("reference", "", "reference WAV (the stem that sounds right)")
	profile := fs.String("profile", "", "saved reference profile JSON (alternative to --reference)")
	library := fs.String("library", "", "house-sound library JSON (alternative to --reference/--profile); auto-selects the target for YOUR stem's role")
	mine := fs.String("mine", "", "YOUR WAV — the stem to match to the reference")
	out := fs.String("out", "", "output JSON path (default: <mine-base>.matchplan.json next to source)")
	kw := fs.Bool("k-weight", false, "apply becky's K-weight loudness approximation when decoding WAVs")
	asJSON := fs.Bool("json", false, "print the full MatchPlan JSON to stdout")
	remember := fs.String("remember", "", "log this reference as your go-to sound under this name (e.g. 'drums'); after a few uses, `becky-habits usual sound:<name>` recalls it. With --library, recorded role-aware under sound:<role>")
	if err := fs.Parse(argv); err != nil {
		return 2
	}
	if *mine == "" {
		fmt.Fprintln(os.Stderr, "becky-ref match: need --mine")
		return 2
	}
	// Exactly ONE target source: --reference XOR --profile XOR --library.
	targets := 0
	if *reference != "" {
		targets++
	}
	if *profile != "" {
		targets++
	}
	if *library != "" {
		targets++
	}
	if targets != 1 {
		fmt.Fprintln(os.Stderr, "becky-ref match: give exactly one target — --reference <wav>, --profile <json>, or --library <house.json>")
		return 2
	}

	mineAudio, err := decode(*mine)
	if err != nil {
		fmt.Fprintf(os.Stderr, "becky-ref: %v\n", err)
		return 1
	}
	mineProf := refmatch.Analyze(mineAudio, refmatch.Options{KWeight: *kw})
	mineProf.Source = pathx.Base(*mine)

	// Resolve the target. --library is role-aware: classify YOUR stem and auto-pick that
	// role's house sound. The remembered scope and the printed line differ by source.
	var refProf refmatch.Profile
	var matchedRole string
	if *library != "" {
		r, _, _ := stemscan.ClassifyRole(mineAudio.Samples, mineAudio.SampleRate, pathx.Base(*mine), mineProf.CrestDB)
		role := string(r)
		matchedRole = role
		lib, lerr := loadLibrary(*library)
		if lerr != nil {
			fmt.Fprintf(os.Stderr, "becky-ref: %v\n", lerr)
			return 1
		}
		tp, ok := lib.TargetForRole(role)
		if !ok {
			fmt.Fprintf(os.Stderr, "becky-ref: your stem looks like %q, but the house library has no %q target. It has: %s. Add one with `becky-ref library build`.\n",
				role, role, strings.Join(lib.RoleNames(), ", "))
			return 1
		}
		refProf = tp
		fmt.Printf("matching your %s to your house %s sound\n", role, role)
	} else {
		refProf, err = loadTarget(*reference, *profile, *kw)
		if err != nil {
			fmt.Fprintf(os.Stderr, "becky-ref: %v\n", err)
			return 1
		}
	}

	plan := refmatch.Match(refProf, mineProf)

	dst := *out
	if dst == "" {
		dst = sidecar(*mine, ".matchplan.json")
	}
	if err := writeJSON(dst, plan); err != nil {
		fmt.Fprintf(os.Stderr, "becky-ref: %v\n", err)
		return 1
	}

	// Best-effort preference learning: if the producer named this match (--remember
	// drums), log WHICH reference he used for it. Over repeated sessions the same
	// choice corroborates and `becky-habits usual sound:drums` surfaces his go-to
	// reference. The fuzzy spectral target itself is saved explicitly via
	// `becky-ref profile --out`; here we only learn the reach-for habit. Never affects
	// exit code (degrade-never-crash).
	if name := strings.TrimSpace(*remember); name != "" {
		// Role-aware scope when matching against a library: record under the auto-
		// selected role (sound:kick) so `becky-habits usual sound:kick` recalls the
		// go-to. For an explicit --reference/--profile, scope under the given name.
		scope := "sound:" + name
		refDesc := refProf.Source
		if matchedRole != "" {
			scope = "sound:" + matchedRole
			if refDesc == "" {
				refDesc = "house " + matchedRole
			}
		}
		if refDesc != "" {
			logPath := siblingFile(dst, "ref.corrections.jsonl")
			// Structured value (a JSON blob) so becky-habits surfaces it via `usual`.
			fixed, _ := json.Marshal(map[string]string{"reference": refDesc})
			if err := habits.AppendCorrectionLog(logPath, "ref", scope, "reference", "", string(fixed)); err == nil {
				fmt.Printf("  remembered: matching %q to %s (learns after a few uses → `becky-habits usual %s`)\n", name, refDesc, scope)
			}
		}
	}

	if *asJSON {
		printJSON(plan)
	} else {
		printPlan(plan, dst)
	}
	if plan.Degraded {
		return 1
	}
	return 0
}

// loadTarget resolves the reference target from either a WAV (analyze it now) or a
// saved profile JSON.
func loadTarget(refWAV, profileJSON string, kw bool) (refmatch.Profile, error) {
	if profileJSON != "" {
		b, err := os.ReadFile(profileJSON)
		if err != nil {
			return refmatch.Profile{}, fmt.Errorf("read profile %s: %w", profileJSON, err)
		}
		var p refmatch.Profile
		if err := json.Unmarshal(b, &p); err != nil {
			return refmatch.Profile{}, fmt.Errorf("parse profile %s: %w", profileJSON, err)
		}
		if len(p.Bands) == 0 {
			return refmatch.Profile{}, fmt.Errorf("profile %s has no bands — re-run 'becky-ref profile'", profileJSON)
		}
		return p, nil
	}
	a, err := decode(refWAV)
	if err != nil {
		return refmatch.Profile{}, err
	}
	p := refmatch.Analyze(a, refmatch.Options{KWeight: kw})
	p.Source = pathx.Base(refWAV)
	return p, nil
}

// --- apply subcommand: write a saved match plan onto a routing-graph bus ---

func runApply(argv []string) int {
	fs := newFlagSet("apply")
	planPath := fs.String("plan", "", "saved MatchPlan JSON (from `becky-ref match --out`)")
	projPath := fs.String("project", "", "music.Project routing JSON to edit (e.g. becky-compose's project.json)")
	bus := fs.String("bus", "", "the bus to apply the moves to (e.g. bus.drums)")
	out := fs.String("output", "", "output JSON path (default: <project-base>.refapplied.json next to source)")
	dryRun := fs.Bool("dry-run", false, "show what WOULD change in plain English; write nothing")
	if err := fs.Parse(argv); err != nil {
		return 2
	}
	if *planPath == "" || *projPath == "" || *bus == "" {
		fmt.Fprintln(os.Stderr, "becky-ref apply: need --plan, --project and --bus")
		return 2
	}

	plan, err := loadPlan(*planPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "becky-ref: %v\n", err)
		return 1
	}
	proj, err := loadProject(*projPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "becky-ref: %v\n", err)
		return 1
	}

	if *dryRun {
		fmt.Println("becky-ref apply (dry run — nothing written)")
		fmt.Printf("  Would %s\n", lowerFirst(refmatch.DryRunSummary(*bus, plan)))
		if plan.Note != "" {
			fmt.Printf("  (%s)\n", plan.Note)
		}
		return 0
	}

	res := refmatch.ApplyPlan(proj, *bus, plan)

	dst := *out
	if dst == "" {
		dst = sidecar(*projPath, ".refapplied.json")
	}
	if err := writeJSON(dst, res.Project); err != nil {
		fmt.Fprintf(os.Stderr, "becky-ref: %v\n", err)
		return 1
	}

	// Best-effort preference learning: log the applied move so becky learns the setups
	// Jordan habitually reaches for. Never affects exit code (degrade-never-crash).
	logPath := siblingFile(dst, "ref.corrections.jsonl")
	scope := "bus:" + refmatch.ShortBusID(*bus)
	fixed, _ := json.Marshal(map[string]any{"eq": eqFXType(res.EQNode), "gain": gainFXType(res.GainNode)})
	_ = habits.AppendCorrectionLog(logPath, "ref", scope, "match", "", string(fixed))

	fmt.Println("becky-ref apply")
	if res.NoMoves {
		fmt.Printf("  %s\n", res.Summary)
	} else {
		fmt.Printf("  %s\n", res.Summary)
		if res.EQNode != nil {
			fmt.Printf("  EQ node : %s (%s)\n", res.EQNode.ID, res.EQNode.Type)
		}
		if res.GainNode != nil {
			fmt.Printf("  gain node: %s (%s)\n", res.GainNode.ID, res.GainNode.Type)
		}
	}
	if res.Note != "" {
		fmt.Printf("  (%s)\n", res.Note)
	}
	fmt.Printf("  saved: %s\n", dst)
	return 0
}

func eqFXType(n *music.ProjFX) string {
	if n == nil {
		return ""
	}
	return n.Type
}

func gainFXType(n *music.ProjFX) string {
	if n == nil {
		return ""
	}
	return n.Type
}

// loadPlan reads a saved MatchPlan JSON.
func loadPlan(path string) (refmatch.MatchPlan, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return refmatch.MatchPlan{}, fmt.Errorf("read plan %s: %w", path, err)
	}
	var p refmatch.MatchPlan
	if err := json.Unmarshal(b, &p); err != nil {
		return refmatch.MatchPlan{}, fmt.Errorf("parse plan %s: %w (is it a `becky-ref match --out` file?)", path, err)
	}
	return p, nil
}

// loadProject reads a music.Project routing JSON.
func loadProject(path string) (music.Project, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return music.Project{}, fmt.Errorf("read project %s: %w", path, err)
	}
	var p music.Project
	if err := json.Unmarshal(b, &p); err != nil {
		return music.Project{}, fmt.Errorf("parse project %s: %w (is it a becky routing project.json?)", path, err)
	}
	return p, nil
}

// --- library subcommand: build a per-role house-sound target from a folder of stems ---

func runLibrary(argv []string) int {
	if len(argv) < 1 || argv[0] != "build" {
		fmt.Fprintln(os.Stderr, "usage: becky-ref library build --dir <folder> [--out house.json] [--recursive]")
		return 2
	}
	fs := newFlagSet("library build")
	dir := fs.String("dir", "", "folder of good-sounding reference WAV stems")
	out := fs.String("out", "", "output JSON path (default: <dir>/house.json)")
	recursive := fs.Bool("recursive", false, "also scan subfolders")
	kw := fs.Bool("k-weight", false, "apply becky's K-weight loudness approximation when profiling")
	asJSON := fs.Bool("json", false, "print the full HouseSound JSON to stdout")
	if err := fs.Parse(argv[1:]); err != nil {
		return 2
	}
	if *dir == "" {
		fmt.Fprintln(os.Stderr, "becky-ref library build: need --dir")
		return 2
	}

	stems, err := readStems(*dir, *recursive)
	if err != nil {
		fmt.Fprintf(os.Stderr, "becky-ref: %v\n", err)
		return 1
	}
	if len(stems) == 0 {
		fmt.Fprintf(os.Stderr, "becky-ref: no .wav stems found in %s\n", *dir)
		return 1
	}

	lib := refmatch.BuildLibrary(*dir, stems, refmatch.Options{KWeight: *kw})

	dst := *out
	if dst == "" {
		dst = filepath.Join(*dir, "house.json")
	}
	if err := writeJSON(dst, lib); err != nil {
		fmt.Fprintf(os.Stderr, "becky-ref: %v\n", err)
		return 1
	}

	if *asJSON {
		printJSON(lib)
	} else {
		printLibrary(lib, dst)
	}
	return 0
}

// readStems walks dir (optionally recursively) and reads every .wav file into a
// StemInput. A file that can't be read is carried with Err set so BuildLibrary reports
// it skipped-with-reason (degrade-never-crash) rather than aborting the whole scan.
func readStems(dir string, recursive bool) ([]refmatch.StemInput, error) {
	var paths []string
	if recursive {
		err := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
			if err != nil {
				return nil // skip unreadable entries, keep walking
			}
			if !d.IsDir() && isWAV(p) {
				paths = append(paths, p)
			}
			return nil
		})
		if err != nil {
			return nil, fmt.Errorf("scan %s: %w", dir, err)
		}
	} else {
		entries, err := os.ReadDir(dir)
		if err != nil {
			return nil, fmt.Errorf("read folder %s: %w", dir, err)
		}
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			p := filepath.Join(dir, e.Name())
			if isWAV(p) {
				paths = append(paths, p)
			}
		}
	}
	sort.Strings(paths) // determinism (BuildLibrary re-sorts by name too)

	out := make([]refmatch.StemInput, 0, len(paths))
	for _, p := range paths {
		si := refmatch.StemInput{Name: pathx.Base(p), Path: p}
		b, err := os.ReadFile(p)
		if err != nil {
			si.Err = err
		} else {
			si.Data = b
		}
		out = append(out, si)
	}
	return out, nil
}

func isWAV(p string) bool {
	return strings.EqualFold(filepath.Ext(p), ".wav")
}

// loadLibrary reads a saved HouseSound JSON.
func loadLibrary(path string) (refmatch.HouseSound, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return refmatch.HouseSound{}, fmt.Errorf("read library %s: %w", path, err)
	}
	var h refmatch.HouseSound
	if err := json.Unmarshal(b, &h); err != nil {
		return refmatch.HouseSound{}, fmt.Errorf("parse library %s: %w (is it a `becky-ref library build` file?)", path, err)
	}
	if len(h.Roles) == 0 {
		return refmatch.HouseSound{}, fmt.Errorf("library %s has no role targets — re-run `becky-ref library build`", path)
	}
	return h, nil
}

func printLibrary(h refmatch.HouseSound, dst string) {
	fmt.Println("becky-ref library")
	fmt.Printf("  house sound from %d role(s):\n", len(h.Roles))
	for _, rt := range h.Roles {
		fmt.Printf("    %-16s from %d stem(s): %s\n", rt.Role, rt.StemCount, strings.Join(rt.ContributingStems, ", "))
		fmt.Printf("      loudness %.1f dBFS, crest %.1f dB, brightness %s\n", rt.Profile.LoudnessDB, rt.Profile.CrestDB, hz(rt.Profile.CentroidHz))
		if rt.Degraded {
			fmt.Printf("      DEGRADED: %s\n", rt.Note)
		}
	}
	if len(h.Skipped) > 0 {
		fmt.Printf("  skipped %d file(s):\n", len(h.Skipped))
		for _, s := range h.Skipped {
			fmt.Printf("    %s — %s\n", s.Name, s.Reason)
		}
	}
	fmt.Printf("  (%s)\n", h.Note)
	fmt.Printf("  saved: %s\n", dst)
	fmt.Println("  now: becky-ref match --library " + dst + " --mine yourstem.wav")
}

// lowerFirst lowercases the first rune of s (so "Set the ..." reads as "Would set the
// ..." in the dry-run line).
func lowerFirst(s string) string {
	if s == "" {
		return s
	}
	return strings.ToLower(s[:1]) + s[1:]
}

// --- shared helpers ---

func decode(path string) (dsp.Audio, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return dsp.Audio{}, fmt.Errorf("read %s: %w", path, err)
	}
	a, err := dsp.DecodeWAV(b)
	if err != nil {
		return dsp.Audio{}, fmt.Errorf("decode %s: %w", path, err)
	}
	return a, nil
}

// sidecar builds an output path next to the source: dir(src)/<base-without-ext><suffix>.
// Uses pathx (separator-agnostic) because the source may be a Windows path even on Linux.
func sidecar(src, suffix string) string {
	base := pathx.Base(src)
	if dot := strings.LastIndex(base, "."); dot > 0 {
		base = base[:dot]
	}
	if base == "" {
		base = "ref"
	}
	dir := pathx.Dir(src)
	name := base + suffix
	if dir == "" {
		return name
	}
	// Preserve the source's separator style so a C:\... source yields a C:\... sidecar.
	sep := "/"
	if strings.ContainsRune(src, '\\') && !strings.ContainsRune(src, '/') {
		sep = "\\"
	}
	return dir + sep + name
}

// siblingFile returns a path to name in the same directory as src, preserving src's
// separator style (so a C:\... source yields a C:\... sibling). Used for the shared
// corrections log that feeds becky-habits.
func siblingFile(src, name string) string {
	dir := pathx.Dir(src)
	if dir == "" {
		return name
	}
	sep := "/"
	if strings.ContainsRune(src, '\\') && !strings.ContainsRune(src, '/') {
		sep = "\\"
	}
	return dir + sep + name
}

func writeJSON(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("encode JSON: %w", err)
	}
	if err := os.WriteFile(path, append(b, '\n'), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

func printJSON(v any) {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}

func printProfile(p refmatch.Profile, dst string) {
	fmt.Println("becky-ref profile")
	if p.Degraded {
		fmt.Printf("  DEGRADED: %s\n", p.Note)
	}
	fmt.Printf("  loudness : %.1f dBFS%s\n", p.LoudnessDB, kwTag(p.KWeighted))
	fmt.Printf("  dynamics : crest %.1f dB (peak %.1f)\n", p.CrestDB, p.PeakDB)
	fmt.Printf("  brightness: centroid %s\n", hz(p.CentroidHz))
	fmt.Println("  tonal balance (dBFS):")
	for _, b := range p.Bands {
		fmt.Printf("    %-11s %6.1f\n", b.Name, b.EnergyDB)
	}
	fmt.Printf("  saved: %s\n", dst)
}

func printPlan(p refmatch.MatchPlan, dst string) {
	// Headline FIRST — the one line a non-dev reads (FORENSIC-OUTPUT-PHILOSOPHY).
	fmt.Printf("\xf0\x9f\x91\x80 %s\n\n", p.Headline) // 👀
	if p.MoveCount == 0 {
		fmt.Printf("  %s\n", p.Verdict)
	} else {
		fmt.Println("  Moves to match the reference:")
		n := 1
		if p.GainText != "" {
			fmt.Printf("   %d. %s\n", n, p.GainText)
			n++
		}
		for _, m := range p.EQMoves {
			fmt.Printf("   %d. EQ: %s\n", n, m.Text)
			n++
		}
		if p.CompText != "" {
			fmt.Printf("   %d. %s\n", n, p.CompText)
			n++
		}
	}
	if p.BrightnessNote != "" {
		fmt.Printf("  note: %s\n", p.BrightnessNote)
	}
	if p.Degraded {
		fmt.Printf("  DEGRADED: %s\n", p.Note)
	} else if p.Note != "" {
		fmt.Printf("  (%s)\n", p.Note)
	}
	fmt.Printf("  full plan saved: %s\n", dst)
}

func kwTag(k bool) string {
	if k {
		return " (K-weight approx, NOT certified LUFS)"
	}
	return " (RMS, relative — NOT certified LUFS)"
}

func hz(h float64) string {
	if h >= 1000 {
		return fmt.Sprintf("%.1f kHz", h/1000)
	}
	return fmt.Sprintf("%.0f Hz", h)
}
