// becky-enroll — build the becky-identify knowledge base straight from the case
// wiki, with zero human clip-making.
//
//	becky-enroll [--wiki <dir>]... [--kb <out-dir>] [--bin <dir>]
//	             [--only "<name>"] [--no-face] [--no-voice]
//	             [--device cpu|cuda] [--verbose]
//
// It crawls the wiki .md files, detects PERSON entities (name + aliases + the raw
// video/image files each references), and for each person auto-builds a clean
// enrollment sample: a ~15-30s single-speaker voice clip (via becky-diarize) into
// KB/voice-prints/<Name>/, and the clearest face frame (via the shared face stack)
// into KB/face-prints/<Name>/. It writes the becky-identify KB layout, an
// enrollment-registry.json, and a human-readable enrollment-report.txt. People
// with no clean sample are SKIPPED with a recorded reason — never fabricated.
//
// JSON summary to stdout; diagnostics to stderr; exit 0 on success. The wiki and
// source videos are read-only.
package main

import (
	"flag"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"becky-go/internal/beckyio"
	"becky-go/internal/config"
)

// defaultWikiRoots are the real CLANCY wiki roots (the spec's verification target).
var defaultWikiRoots = []string{
	`C:\Users\only1\Documents\Obsidian\llm-wiki-CLANCY-TRIAL\wiki`,
	`C:\Users\only1\Documents\Obsidian\llm-wiki-CLANCY-VIDEOS\wiki`,
}

// multiFlag collects repeated --wiki values.
type multiFlag []string

func (m *multiFlag) String() string { return strings.Join(*m, ",") }
func (m *multiFlag) Set(v string) error {
	*m = append(*m, v)
	return nil
}

func main() {
	var wikiRoots multiFlag
	flag.Var(&wikiRoots, "wiki", "wiki root directory (repeatable; default: the CLANCY wiki roots)")
	kb := flag.String("kb", "becky-kb", "output knowledge-base directory")
	binDir := flag.String("bin", "", "directory holding becky-diarize.exe (default: dir of this binary)")
	// Single-clip teach mode (powers `becky learn "<name>" <clip>` and
	// `becky "this is <name>" <clip>`): enroll ONE named person from ONE clip and
	// APPEND to the KB, no wiki involved. --clip + --name together trigger this path.
	clip := flag.String("clip", "", "single clip to learn one named person from (use with --name; appends to the KB)")
	clipName := flag.String("name", "", "person name to learn from --clip")
	only := flag.String("only", "", "enroll only the person whose name/alias/slug matches this (substring)")
	noFace := flag.Bool("no-face", false, "skip face enrollment")
	noVoice := flag.Bool("no-voice", false, "skip voice enrollment")
	includeNonSubject := flag.Bool("include-non-subjects", false, "also enroll legal professionals (attorneys/officers); off by default since their media are case exhibits, not recordings of them")
	device := flag.String("device", "", "device: cpu, cuda (default from config)")
	dryRun := flag.Bool("dry-run", false, "crawl + report detected people and media, but enroll nothing")
	verbose := flag.Bool("verbose", false, "show progress on stderr")
	flag.Parse()

	cfg := config.Load()
	dev := cfg.Device
	if *device != "" {
		dev = *device
	}

	// SINGLE-CLIP TEACH MODE: `--clip <video> --name "<person>"` learns one person
	// from one clip and APPENDS to the KB (no wiki). This is what `becky learn` and
	// `becky "this is <name>"` shell out to. It runs and returns BEFORE the wiki path.
	if *clip != "" || *clipName != "" {
		runLearnClip(cfg, dev, *kb, *clip, *clipName, *binDir, *noFace, *noVoice, *verbose)
		return
	}

	roots := []string(wikiRoots)
	if len(roots) == 0 {
		roots = append([]string(nil), defaultWikiRoots...)
	}
	roots = existingDirs(roots, *verbose)
	if len(roots) == 0 {
		beckyio.Fatalf("no wiki roots exist (checked: %s)", strings.Join([]string(wikiRoots), ", "))
	}

	beckyio.Logf(*verbose, "crawling %d wiki root(s)...", len(roots))
	people, warnings, err := crawlWiki(roots, *verbose)
	if err != nil {
		beckyio.Fatalf("crawl wiki: %v", err)
	}
	beckyio.Logf(*verbose, "detected %d person entit(y/ies)", len(people))

	if *only != "" {
		people = filterPeople(people, *only)
		beckyio.Logf(*verbose, "filtered to %d person(s) matching %q", len(people), *only)
	}

	if *dryRun {
		emitDryRun(roots, people, warnings)
		return
	}

	if err := os.MkdirAll(*kb, 0o755); err != nil {
		beckyio.Fatalf("create KB dir: %v", err)
	}

	opts := enrollOptions{
		diarizeBin:        resolveDiarizeBin(*binDir),
		device:            dev,
		noFace:            *noFace,
		noVoice:           *noVoice,
		includeNonSubject: *includeNonSubject,
		verbose:           *verbose,
	}
	// --only is an explicit operator choice; honour it even for non-subjects.
	if *only != "" {
		opts.includeNonSubject = true
	}
	if !opts.noVoice && opts.diarizeBin == "" {
		beckyio.Logf(true, "warning: becky-diarize.exe not found (pass --bin); voice enrollment will be skipped")
	}

	results := make([]EnrollResult, 0, len(people))
	for i, p := range people {
		beckyio.Logf(*verbose, "[%d/%d] enrolling %s (%d video(s), %d image(s))",
			i+1, len(people), p.Name, len(p.VideoRefs), len(p.ImageRefs))
		results = append(results, enrollPerson(cfg, *kb, p, opts))
	}

	reg, err := writeKB(*kb, roots, results, warnings)
	if err != nil {
		beckyio.Fatalf("write KB: %v", err)
	}
	beckyio.Logf(*verbose, "wrote KB + registry + report to %s", absOr(*kb))
	beckyio.PrintJSON(reg)
}

// emitDryRun prints the detected people + media without enrolling anything.
func emitDryRun(roots []string, people []Person, warnings []string) {
	type dryEnt struct {
		Name      string   `json:"name"`
		Aliases   []string `json:"aliases"`
		MDSource  string   `json:"md_source"`
		VideoRefs []string `json:"video_refs"`
		ImageRefs []string `json:"image_refs"`
	}
	ents := make([]dryEnt, 0, len(people))
	for _, p := range people {
		ents = append(ents, dryEnt{
			Name:      p.Name,
			Aliases:   nonNil(p.Aliases),
			MDSource:  p.MDSource,
			VideoRefs: relList(p.VideoRefs),
			ImageRefs: relList(p.ImageRefs),
		})
	}
	beckyio.PrintJSON(map[string]any{
		"dry_run":         true,
		"wiki_roots":      roots,
		"people_detected": len(people),
		"entities":        ents,
		"warnings":        warnings,
	})
}

// filterPeople keeps people whose name, alias, or slug contains the query.
func filterPeople(people []Person, query string) []Person {
	q := strings.ToLower(strings.TrimSpace(query))
	var out []Person
	for _, p := range people {
		if strings.Contains(strings.ToLower(p.Name), q) || strings.Contains(p.Slug, q) {
			out = append(out, p)
			continue
		}
		for _, a := range p.Aliases {
			if strings.Contains(strings.ToLower(a), q) {
				out = append(out, p)
				break
			}
		}
	}
	return out
}

// existingDirs keeps only roots that are real directories.
func existingDirs(roots []string, verbose bool) []string {
	var out []string
	for _, r := range roots {
		fi, err := os.Stat(r)
		if err == nil && fi.IsDir() {
			out = append(out, r)
		} else {
			beckyio.Logf(verbose, "skipping missing wiki root: %s", r)
		}
	}
	return out
}

// resolveDiarizeBin finds becky-diarize.exe: --bin dir if given, else next to this
// executable (the becky-go/bin layout), else "" (voice enrollment degrades).
func resolveDiarizeBin(override string) string {
	name := "becky-diarize"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	if override != "" {
		cand := filepath.Join(override, name)
		if fileExists(cand) {
			return cand
		}
	}
	if exe, err := os.Executable(); err == nil {
		cand := filepath.Join(filepath.Dir(exe), name)
		if fileExists(cand) {
			return cand
		}
	}
	return ""
}
