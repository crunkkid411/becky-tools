// becky-stems — scan a FOLDER of studio stems and tell the producer, per stem, what's
// going on (level, clipping, what each stem probably IS, problems) and propose a sane
// STARTING balance — so he doesn't open twenty tracks and eyeball faders for an hour.
//
//	becky-stems scan --dir <folder> [--out report.json] [--recursive] [--json]
//
// It prints a scannable plain-English table FIRST (role, peak, loudness, CLIPPING!,
// suggested gain), then optionally the full structured JSON with --json. Everything is
// deterministic and offline (pure-Go DSP, no models, no network): the same folder
// produces a byte-identical report. Unreadable / malformed / too-short files are noted
// and skipped, never fatal (degrade-never-crash). Paths may be Windows paths.
//
// HONEST about the heuristic: the peak/RMS/clipping numbers are exact; the per-stem
// ROLE ("kick", "vocal", ...) is a spectral+filename GUESS that says "unknown" when it
// isn't sure rather than flooding you with confident wrong labels (house rule —
// corroborate, then conclude).
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"becky-go/internal/pathx"
	"becky-go/internal/stemscan"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	switch os.Args[1] {
	case "scan":
		os.Exit(runScan(os.Args[2:]))
	case "-h", "--help", "help":
		usage()
		os.Exit(0)
	default:
		fmt.Fprintf(os.Stderr, "becky-stems: unknown subcommand %q\n", os.Args[1])
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: becky-stems scan --dir <folder> [--out report.json] [--recursive] [--json]")
	fmt.Fprintln(os.Stderr, "  scans a folder of WAV stems: per-stem level/clipping/role + a starting balance.")
}

func runScan(argv []string) int {
	fs := flag.NewFlagSet("scan", flag.ContinueOnError)
	dir := fs.String("dir", "", "folder of WAV stems to scan")
	out := fs.String("out", "", "write the full JSON report here (default: <dir>/stems-report.json)")
	recursive := fs.Bool("recursive", false, "also scan subfolders")
	asJSON := fs.Bool("json", false, "also print the full report JSON to stdout")
	if err := fs.Parse(argv); err != nil {
		return 2
	}
	if *dir == "" {
		fmt.Fprintln(os.Stderr, "becky-stems: need --dir <folder>")
		return 2
	}

	files, err := collectWAVs(*dir, *recursive)
	if err != nil {
		// A genuinely missing/unreadable FOLDER is the one hard error (nothing to do).
		fmt.Fprintf(os.Stderr, "becky-stems: %v\n", err)
		return 1
	}

	rep := stemscan.BuildFolderReport(*dir, files)

	outPath := *out
	if outPath == "" {
		outPath = defaultOut(*dir)
	}
	wrote := true
	if err := writeReport(outPath, rep); err != nil {
		// Couldn't write the sidecar — say so but still print the report (degrade).
		fmt.Fprintf(os.Stderr, "becky-stems: couldn't write %s: %v\n", outPath, err)
		wrote = false
	}

	printTable(rep, outPath, wrote)
	if *asJSON {
		printJSON(rep)
	}
	return 0
}

// collectWAVs reads the WAV files in dir (non-recursive unless asked) into FileInputs.
// A per-file read error is captured on the FileInput (so it's reported as skipped),
// NOT returned — only a folder that can't be listed at all is a hard error.
func collectWAVs(dir string, recursive bool) ([]stemscan.FileInput, error) {
	info, err := os.Stat(dir)
	if err != nil {
		return nil, fmt.Errorf("can't open folder %s: %w", dir, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("%s is not a folder", dir)
	}

	var paths []string
	if recursive {
		err = filepath.WalkDir(dir, func(p string, d os.DirEntry, e error) error {
			if e != nil || d.IsDir() {
				return nil // skip unreadable entries, keep going
			}
			if isWAV(p) {
				paths = append(paths, p)
			}
			return nil
		})
		if err != nil {
			return nil, fmt.Errorf("walk %s: %w", dir, err)
		}
	} else {
		entries, e := os.ReadDir(dir)
		if e != nil {
			return nil, fmt.Errorf("list %s: %w", dir, e)
		}
		for _, ent := range entries {
			if ent.IsDir() {
				continue
			}
			p := filepath.Join(dir, ent.Name())
			if isWAV(p) {
				paths = append(paths, p)
			}
		}
	}
	sort.Strings(paths) // deterministic read order (BuildFolderReport re-sorts too)

	files := make([]stemscan.FileInput, 0, len(paths))
	for _, p := range paths {
		data, e := os.ReadFile(p)
		files = append(files, stemscan.FileInput{Path: p, Data: data, Err: e})
	}
	return files, nil
}

func isWAV(p string) bool {
	return strings.EqualFold(filepath.Ext(p), ".wav")
}

func defaultOut(dir string) string {
	// Sidecar next to the folder. Use the OS join here (real local FS path).
	return filepath.Join(dir, "stems-report.json")
}

func writeReport(path string, rep stemscan.FolderReport) error {
	b, err := json.MarshalIndent(rep, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0o644)
}

func printJSON(rep stemscan.FolderReport) {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	_ = enc.Encode(rep)
}

// printTable writes the human-first summary: headline, then one aligned line per stem.
func printTable(rep stemscan.FolderReport, outPath string, wrote bool) {
	fmt.Println(rep.Headline)
	fmt.Println()

	if len(rep.Stems) == 0 {
		fmt.Println("(nothing to analyze)")
		return
	}

	// Column header. Kept short so it fits a normal terminal.
	fmt.Printf("  %-22s  %-15s  %7s  %8s  %5s  %s\n",
		"STEM", "ROLE", "PEAK", "LOUDNESS", "GAIN", "NOTES")
	fmt.Printf("  %-22s  %-15s  %7s  %8s  %5s  %s\n",
		strings.Repeat("-", 22), strings.Repeat("-", 15), "-------", "--------", "-----", "-----")

	for _, s := range rep.Stems {
		if s.Skipped {
			fmt.Printf("  %-22s  %-15s  %7s  %8s  %5s  skipped: %s\n",
				trunc(s.Name, 22), "-", "-", "-", "-", s.Reason)
			continue
		}
		fmt.Printf("  %-22s  %-15s  %6.1f  %7.1f  %+5.1f  %s\n",
			trunc(s.Name, 22),
			roleLabel(s),
			s.PeakDBFS,
			s.LoudnessDBFS,
			s.SuggestGainDB,
			stemNotes(s),
		)
	}

	fmt.Println()
	fmt.Printf("Levels in dBFS. LOUDNESS is honest RMS (not certified LUFS). GAIN is a starting trim toward %.0f dBFS RMS.\n", rep.TargetDBFS)
	fmt.Println("Role is a spectral+filename guess; \"unknown\" means becky won't guess rather than guess wrong.")
	if wrote {
		fmt.Printf("Full report: %s\n", outPath)
	}
}

// roleLabel renders the role with an honesty marker: a low-confidence named role gets a
// trailing "?" so the producer reads it as a guess, not a fact.
func roleLabel(s stemscan.StemReport) string {
	if s.Role == stemscan.RoleUnknown {
		return "unknown"
	}
	if s.RoleConfidence < 0.55 {
		return string(s.Role) + "?"
	}
	return string(s.Role)
}

// stemNotes builds the per-stem flags a producer triages on, most important first.
func stemNotes(s stemscan.StemReport) string {
	var notes []string
	if s.Clipping {
		notes = append(notes, fmt.Sprintf("⚠ CLIPPING! (%.2f%% of samples)", s.ClippedFrac*100))
	}
	if s.NearSilent {
		notes = append(notes, "near-silent (muted/empty?)")
	}
	if absf(s.DCOffset) > 0.003 {
		notes = append(notes, fmt.Sprintf("DC offset %.3f", s.DCOffset))
	}
	if s.CrestDB > 0 && s.CrestDB < 6 && !s.NearSilent {
		notes = append(notes, fmt.Sprintf("very squashed (crest %.0f dB)", s.CrestDB))
	}
	if s.SuggestNote != "" && s.SuggestNote != "toward a -18 dBFS RMS starting balance" {
		notes = append(notes, s.SuggestNote)
	}
	return strings.Join(notes, "; ")
}

func absf(v float64) float64 {
	if v < 0 {
		return -v
	}
	return v
}

func trunc(s string, n int) string {
	// pathx.Base in case a full path slipped through; then truncate for the column.
	s = pathx.Base(s)
	if len(s) <= n {
		return s
	}
	if n <= 1 {
		return s[:n]
	}
	return s[:n-1] + "…"
}
