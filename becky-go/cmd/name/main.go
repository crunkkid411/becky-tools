// becky-name — the high-contrast "who is this?" naming loop. It walks a becky-cluster
// output one card at a time, shows the representative face, takes a typed name, and
// enrolls the WHOLE cluster under that name by shelling out to the EXISTING becky-enroll
// teach path. Anonymous "Person A, seen in 41 clips" becomes a named KB enrollee — so
// becky-identify recognizes that person corpus-wide from then on.
//
//	becky-name --clusters clusters.json --kb kb-final [options]
//
// The colored card is an accessibility AID (ACCESSIBILITY.md): Jordan is SIGHTED with
// impaired vision and reads the screen directly — the card reuses cmd/ask/styles.go's
// high-contrast neon-green/pink/amber/cyan palette with big, uncluttered facts and one
// large name input. It is NOT a screen-reader flow and NOT plain monochrome text.
//
// becky-name is a thin orchestrator: it never re-implements embedding/enroll. The
// decision logic (cluster walk, name capture, clip selection, enroll-argv) lives in
// internal/facenaming and is fully unit-tested headless via fakes. This binary wires
// that logic to a real terminal + a real becky-enroll.
//
// Degrade-never-crash: no TTY -> headless parse/apply, exit 0; --names applies a map
// with no display; --dry-run prints the exact enroll argv per cluster (the offline
// proof); a malformed clusters.json -> a plain-language error, not a stack trace.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"

	"becky-go/internal/beckyio"
	"becky-go/internal/facenaming"

	"golang.org/x/term"
)

// runConfig bundles the parsed flags.
type runConfig struct {
	clustersPath string
	kb           string
	binDir       string
	modality     string
	minClips     int
	device       string
	namesPath    string
	outPath      string
	dryRun       bool
	cap          int
}

func main() {
	clusters := flag.String("clusters", "", "becky-cluster Output JSON (required)")
	kb := flag.String("kb", "", "knowledge base to enroll into (required; appended, never clobbered)")
	binDir := flag.String("bin", "", "dir holding becky-enroll (default: dir of this exe)")
	modality := flag.String("modality", "both", "only review clusters of this modality: face | voice | both")
	minClips := flag.Int("min-clips", 0, "only review clusters with >= N members (0 = no extra filter)")
	device := flag.String("device", "", "device passed through to becky-enroll: cpu | cuda")
	namesPath := flag.String("names", "", "NON-INTERACTIVE: apply a {cluster_id: name} JSON map (no TUI)")
	outPath := flag.String("out", "", "write an audit JSON of which clusters got which names + the skip log")
	dryRun := flag.Bool("dry-run", false, "show the enroll argv per cluster; enroll nothing")
	cap := flag.Int("cap", facenaming.DefaultEnrollCap, "max distinct clips to enroll from per cluster")
	flag.Parse()

	if strings.TrimSpace(*clusters) == "" {
		beckyio.Fatalf("--clusters <clusters.json> is required (a becky-cluster Output JSON)")
	}
	// --kb is required for any real enroll; --dry-run can run without it (it enrolls
	// nothing), so only enforce it outside dry-run.
	if strings.TrimSpace(*kb) == "" && !*dryRun {
		beckyio.Fatalf("--kb <knowledge-base-dir> is required (or use --dry-run)")
	}

	rc := runConfig{
		clustersPath: *clusters, kb: *kb, binDir: *binDir, modality: strings.ToLower(*modality),
		minClips: *minClips, device: *device, namesPath: *namesPath, outPath: *outPath,
		dryRun: *dryRun, cap: *cap,
	}

	data, err := os.ReadFile(rc.clustersPath)
	if err != nil {
		beckyio.Fatalf("read clusters: %v", err)
	}
	cl, err := facenaming.LoadClusters(data)
	if err != nil {
		beckyio.Fatalf("%v", err) // plain-language, no stack trace
	}

	os.Exit(dispatch(rc, cl, term.IsTerminal(int(os.Stdin.Fd()))))
}

// dispatch picks the mode from the flags + whether stdin is a terminal, expressed as a
// pure-ish function so the precedence is unit-testable. Precedence:
//
//	--dry-run             -> print the enroll plan, exit 0          (no display, no enroll)
//	--names <file>        -> apply the map headlessly, exit          (no display)
//	terminal on stdin     -> launch the colored card TUI            (the default)
//	else (no TTY)         -> print the parsed summary, exit 0        (mirrors becky-ask)
func dispatch(rc runConfig, cl facenaming.Clusters, isTTY bool) int {
	switch {
	case rc.dryRun:
		return runDryRun(rc, cl)
	case strings.TrimSpace(rc.namesPath) != "":
		return runNamesFile(rc, cl)
	case isTTY:
		return runTUI(rc, cl)
	default:
		return printNoTTY(rc, cl)
	}
}

// runDryRun prints the exact enroll argv the real run WOULD execute (the offline,
// measurable proof). In dry-run with no --names, EVERY reviewable cluster is planned
// with a literal <name> placeholder so the plan is visible without a map.
func runDryRun(rc runConfig, cl facenaming.Clusters) int {
	names := loadNamesOrPlaceholder(rc, cl)
	kb := rc.kb
	if kb == "" {
		kb = "<kb>"
	}
	plan := facenaming.DryRunPlan(cl, names, kb, rc.device, rc.modality, rc.minClips, rc.cap)
	if len(plan) == 0 {
		fmt.Println("dry-run: no clusters to enroll (check --modality / --min-clips / --names)")
		return 0
	}
	fmt.Printf("dry-run: %d enroll command(s) that WOULD run (%s):\n", len(plan), facenaming.ModalitySummary(cl))
	for _, argv := range plan {
		fmt.Println("  " + shellJoin(argv))
	}
	return 0
}

// runNamesFile applies a {cluster_id: name} map with no display (headless/CI).
func runNamesFile(rc runConfig, cl facenaming.Clusters) int {
	raw, err := os.ReadFile(rc.namesPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "becky-name: read --names: %v\n", err)
		return 1
	}
	var m map[string]string
	if err := json.Unmarshal(raw, &m); err != nil {
		fmt.Fprintf(os.Stderr, "becky-name: --names must be a {cluster_id: name} JSON object: %v\n", err)
		return 1
	}
	names := facenaming.ParseNamesMap(m)
	en := newExecEnroller(rc)
	res := facenaming.ApplyNames(cl, names, en, rc.modality, rc.minClips, rc.cap)
	res.KB = rc.kb
	for _, o := range res.Outcomes {
		fmt.Println(o.Summary())
	}
	if len(res.SkippedID) > 0 {
		fmt.Printf("skipped (left unnamed): %s\n", strings.Join(res.SkippedID, ", "))
	}
	writeAudit(rc.outPath, res)
	return 0
}

// printNoTTY prints what was parsed (cluster count + modalities) and exits 0, so the
// loop is crash-free off a terminal and scriptable (mirrors becky-ask's no-TTY path).
func printNoTTY(rc runConfig, cl facenaming.Clusters) int {
	order := facenaming.WalkOrder(cl, rc.modality, rc.minClips)
	fmt.Fprintf(os.Stderr, "becky-name: %d cluster(s) to review (%s)\n", len(order), facenaming.ModalitySummary(cl))
	for _, c := range order {
		fmt.Fprintf(os.Stderr, "  %s — %d clip(s), %d file(s), cohesion %.2f [%s]\n",
			c.ClusterID, c.MemberCount, c.DistinctSourceFiles, c.Cohesion, c.Modality)
	}
	fmt.Fprintln(os.Stderr, "becky-name is an interactive colored review window — run it in a terminal, or pass --names <map.json> to apply names headlessly, or --dry-run to preview the enroll commands.")
	return 0
}

// loadNamesOrPlaceholder returns the --names map if given, else a placeholder map
// naming every reviewable cluster "<name>" so --dry-run shows the full plan.
func loadNamesOrPlaceholder(rc runConfig, cl facenaming.Clusters) map[string]string {
	if strings.TrimSpace(rc.namesPath) != "" {
		if raw, err := os.ReadFile(rc.namesPath); err == nil {
			var m map[string]string
			if json.Unmarshal(raw, &m) == nil {
				return facenaming.ParseNamesMap(m)
			}
		}
	}
	m := map[string]string{}
	for _, c := range facenaming.WalkOrder(cl, rc.modality, rc.minClips) {
		m[c.ClusterID] = "<name>"
	}
	return m
}

// writeAudit writes the apply result to outPath when set (best-effort, never fatal).
func writeAudit(outPath string, res facenaming.ApplyResult) {
	if strings.TrimSpace(outPath) == "" {
		return
	}
	b, err := json.MarshalIndent(res, "", "  ")
	if err != nil {
		return
	}
	if err := os.WriteFile(outPath, append(b, '\n'), 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "becky-name: could not write audit %s: %v\n", outPath, err)
		return
	}
	fmt.Fprintf(os.Stderr, "Saved audit: %s\n", outPath)
}

// shellJoin renders an argv as a copy-pasteable command line, quoting tokens with
// spaces. Used only for human display in --dry-run.
func shellJoin(argv []string) string {
	parts := make([]string, len(argv))
	for i, a := range argv {
		if strings.ContainsAny(a, " \t") {
			parts[i] = `"` + a + `"`
		} else {
			parts[i] = a
		}
	}
	return strings.Join(parts, " ")
}
