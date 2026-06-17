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
	"strings"

	"becky-go/internal/dsp"
	"becky-go/internal/habits"
	"becky-go/internal/pathx"
	"becky-go/internal/refmatch"
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
	mine := fs.String("mine", "", "YOUR WAV — the stem to match to the reference")
	out := fs.String("out", "", "output JSON path (default: <mine-base>.matchplan.json next to source)")
	kw := fs.Bool("k-weight", false, "apply becky's K-weight loudness approximation when decoding WAVs")
	asJSON := fs.Bool("json", false, "print the full MatchPlan JSON to stdout")
	remember := fs.String("remember", "", "log this reference as your go-to sound under this name (e.g. 'drums'); after a few uses, `becky-habits usual sound:<name>` recalls it")
	if err := fs.Parse(argv); err != nil {
		return 2
	}
	if *mine == "" {
		fmt.Fprintln(os.Stderr, "becky-ref match: need --mine")
		return 2
	}
	if (*reference == "") == (*profile == "") {
		fmt.Fprintln(os.Stderr, "becky-ref match: give exactly one target — either --reference <wav> or --profile <json>")
		return 2
	}

	refProf, err := loadTarget(*reference, *profile, *kw)
	if err != nil {
		fmt.Fprintf(os.Stderr, "becky-ref: %v\n", err)
		return 1
	}

	mineAudio, err := decode(*mine)
	if err != nil {
		fmt.Fprintf(os.Stderr, "becky-ref: %v\n", err)
		return 1
	}
	mineProf := refmatch.Analyze(mineAudio, refmatch.Options{KWeight: *kw})
	mineProf.Source = pathx.Base(*mine)

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
	if name := strings.TrimSpace(*remember); name != "" && refProf.Source != "" {
		logPath := siblingFile(dst, "ref.corrections.jsonl")
		scope := "sound:" + name
		// Structured value (a JSON blob) so becky-habits surfaces it via `usual`.
		fixed, _ := json.Marshal(map[string]string{"reference": refProf.Source})
		if err := habits.AppendCorrectionLog(logPath, "ref", scope, "reference", "", string(fixed)); err == nil {
			fmt.Printf("  remembered: matching %q to %s (learns after a few uses → `becky-habits usual %s`)\n", name, refProf.Source, scope)
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
