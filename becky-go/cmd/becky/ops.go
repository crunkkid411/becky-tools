// ops.go — the plain-language named operations. Each chains existing becky tools
// and emits a plain-English headline (stderr) + structured JSON (stdout) with paths
// to the actual clips/frames. Recall-first; candidate-not-conclusion.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"becky-go/internal/beckyio"
)

// --- becky enroll-wiki ---

// runEnrollWiki shells becky-enroll over the case wiki to build the identify KB.
func runEnrollWiki(args []string) error {
	cf, rest := extractCommon(args)
	kb, rest := flagValue(rest, "kb", "becky-kb")
	wikis, rest := flagValues(rest, "wiki")
	device, rest := flagValue(rest, "device", "")
	_ = rest

	toolArgs := []string{"--kb", kb}
	for _, w := range wikis {
		toolArgs = append(toolArgs, "--wiki", w)
	}
	if device != "" {
		toolArgs = append(toolArgs, "--device", device)
	}
	if cf.verbose {
		toolArgs = append(toolArgs, "--verbose")
	}
	// Pass our --bin through so becky-enroll finds becky-diarize.
	if cf.bin != "" {
		toolArgs = append(toolArgs, "--bin", cf.bin)
	}

	headline(cf, "Building the knowledge base from the wiki...")
	out, err := runTool(cf, "enroll", toolArgs)
	if err != nil {
		return err
	}
	var reg struct {
		Summary struct {
			PeopleDetected int `json:"people_detected"`
			VoiceEnrolled  int `json:"voice_enrolled"`
			FaceEnrolled   int `json:"face_enrolled"`
			FullyEnrolled  int `json:"fully_enrolled"`
			Skipped        int `json:"skipped"`
		} `json:"summary"`
	}
	_ = json.Unmarshal(out, &reg)
	s := reg.Summary
	headline(cf, "Enrolled %d/%d people (voice=%d, face=%d); %d skipped. KB: %s",
		s.FullyEnrolled, s.PeopleDetected, s.VoiceEnrolled, s.FaceEnrolled, s.Skipped, absOr(kb))
	os.Stdout.Write(out)
	return nil
}

// --- becky index <corpus-dir> ---

// runIndex runs becky-pipeline (transcribe + embed) over a corpus so it becomes
// searchable. Needs the embedding server; degrades gracefully if it is down.
func runIndex(args []string) error {
	cf, rest := extractCommon(args)
	db, rest := flagValue(rest, "db", "")
	out, rest := flagValue(rest, "out", "pipeline-out")
	kb, rest := flagValue(rest, "kb", "")
	// --force-transcribe re-runs Parakeet even when a YouTube subtitle sidecar
	// exists (evidence-grade verbatim). Default: REUSE the sidecar transcript.
	forceTranscribe := hasFlag(rest, "force-transcribe")
	rest = dropFlag(rest, "force-transcribe")
	corpus := firstPositional(rest)
	if corpus == "" {
		return fmt.Errorf("usage: becky index <corpus-dir> [--db <path>] [--out <dir>] [--kb <dir>] [--force-transcribe]")
	}
	if !dirExists(corpus) && !fileExists(corpus) {
		return fmt.Errorf("corpus not found: %s", corpus)
	}

	// transcribe REUSES yt-dlp .srt/.vtt/.json3 sidecars (skips Parakeet) and
	// metadata ingests .info.json/.live_chat.json — both fall back gracefully when
	// no sidecar is present. embed makes the (sidecar-or-Parakeet) transcript
	// searchable.
	toolArgs := []string{corpus, "--out", out, "--steps", "transcribe,metadata,embed"}
	if db != "" {
		toolArgs = append(toolArgs, "--db", db)
	}
	if kb != "" {
		toolArgs = append(toolArgs, "--kb", kb)
	}
	if forceTranscribe {
		toolArgs = append(toolArgs, "--force-transcribe")
	}
	if cf.bin != "" {
		toolArgs = append(toolArgs, "--bin", cf.bin)
	}
	if cf.verbose {
		toolArgs = append(toolArgs, "--verbose")
	}

	headline(cf, "Indexing corpus %s (reuse yt-dlp subs+metadata where present; transcribe + embed)...", corpus)
	stdout, err := runTool(cf, "pipeline", toolArgs)
	if err != nil {
		return err
	}
	man := parsePipelineManifest(stdout)
	headline(cf, "Indexed %d video(s): %d ok, %d partial. Output: %s",
		man.totalVideos, man.okVideos, man.partialVideos, absOr(out))
	if man.embedFailed {
		headline(cf, "NOTE: embedding failed for some videos — start start-embed-server.bat, then re-run `becky index`.")
	}
	os.Stdout.Write(stdout)
	return nil
}

// --- becky find "<query>" ---

// runFind runs becky-search (hybrid keyword+vector) over the embedded corpus.
func runFind(args []string) error {
	cf, rest := extractCommon(args)
	db, rest := flagValue(rest, "db", "")
	limit, rest := flagValue(rest, "limit", "10")
	query := firstPositional(rest)
	if query == "" {
		return fmt.Errorf("usage: becky find \"<query>\" [--db <path>] [--limit N]")
	}
	if db == "" {
		db = defaultDB()
	}
	if db == "" || !fileExists(db) {
		return fmt.Errorf("no search index found (pass --db <path>; build one with `becky index <corpus>`)")
	}

	toolArgs := []string{query, "--db", db, "--limit", limit}
	if cf.verbose {
		toolArgs = append(toolArgs, "--verbose")
	}
	headline(cf, "Searching the corpus for: %q", query)
	stdout, err := runTool(cf, "search", toolArgs)
	if err != nil {
		return err
	}
	res := parseSearchResults(stdout)
	headline(cf, "Found %d match(es). Top hits:", len(res.results))
	for i, r := range res.results {
		if i >= 5 {
			break
		}
		headline(cf, "  #%d [%s @ %.1fs] %s (sim %.3f)", r.Rank, filepath.Base(r.SourceFile), r.Timestamp, truncate(r.Text, 70), r.Similarity)
	}
	os.Stdout.Write(stdout)
	return nil
}

// --- becky appearances "<name>" ---

// runAppearances identifies a person across the corpus and reports coverage with
// the actual clips/frames. Chains: (KB) becky-identify per video -> aggregate.
func runAppearances(args []string) error {
	cf, rest := extractCommon(args)
	corpus, rest := flagValue(rest, "corpus", "")
	kb, rest := flagValue(rest, "kb", "becky-kb")
	threshold, rest := flagValue(rest, "voice-threshold", "0.45")
	name := firstPositional(rest)
	if name == "" {
		return fmt.Errorf("usage: becky appearances \"<name>\" --corpus <dir> [--kb <dir>]")
	}
	if !dirExists(kb) {
		return fmt.Errorf("knowledge base not found: %s (run `becky enroll-wiki` first)", kb)
	}
	if corpus == "" {
		corpus = defaultCorpus()
	}
	if corpus == "" || (!dirExists(corpus) && !fileExists(corpus)) {
		return fmt.Errorf("corpus not found: %q (pass --corpus <dir|video>)", corpus)
	}

	videos := discoverVideos(corpus)
	if len(videos) == 0 {
		return fmt.Errorf("no videos found under %s", corpus)
	}
	headline(cf, "Looking for %q across %d video(s)...", name, len(videos))

	report := newAppearanceReport(name, kb, corpus)
	for _, v := range videos {
		idArgs := []string{v, "--kb", kb, "--voice-threshold", threshold}
		if cf.verbose {
			idArgs = append(idArgs, "--verbose")
		}
		out, err := runTool(cf, "identify", idArgs)
		if err != nil {
			report.addError(v, err.Error())
			continue
		}
		report.ingest(v, out)
	}

	report.finalize()
	headline(cf, "%s recognized in %d/%d video(s) (voice=%d, face=%d).",
		name, report.MatchedVideos, report.TotalVideos, report.VoiceMatches, report.FaceMatches)
	for _, a := range report.Appearances {
		headline(cf, "  - %s: %s (conf %.3f)", filepath.Base(a.Video), a.Modality, a.Confidence)
	}
	emitJSON(report)
	return nil
}

// --- becky profile "<name>" ---

// runProfile is appearances + a person summary card (KB metadata + coverage).
func runProfile(args []string) error {
	cf, rest := extractCommon(args)
	corpus, rest := flagValue(rest, "corpus", "")
	kb, rest := flagValue(rest, "kb", "becky-kb")
	threshold, rest := flagValue(rest, "voice-threshold", "0.45")
	name := firstPositional(rest)
	if name == "" {
		return fmt.Errorf("usage: becky profile \"<name>\" --corpus <dir> [--kb <dir>]")
	}
	if !dirExists(kb) {
		return fmt.Errorf("knowledge base not found: %s (run `becky enroll-wiki` first)", kb)
	}

	enrolled, card := loadEntityCard(kb, name)
	if !enrolled {
		headline(cf, "WARNING: %q is not enrolled in the KB — run `becky enroll-wiki` (profile will show no appearances).", name)
	}

	// Coverage via the appearances machinery (best-effort; may be empty without a corpus).
	if corpus == "" {
		corpus = defaultCorpus()
	}
	report := newAppearanceReport(name, kb, corpus)
	if corpus != "" && (dirExists(corpus) || fileExists(corpus)) {
		videos := discoverVideos(corpus)
		headline(cf, "Profiling %q across %d video(s)...", name, len(videos))
		for _, v := range videos {
			idArgs := []string{v, "--kb", kb, "--voice-threshold", threshold}
			if cf.verbose {
				idArgs = append(idArgs, "--verbose")
			}
			out, err := runTool(cf, "identify", idArgs)
			if err != nil {
				report.addError(v, err.Error())
				continue
			}
			report.ingest(v, out)
		}
	} else {
		headline(cf, "No corpus given; profile shows KB enrollment only (pass --corpus <dir> for appearances).")
	}
	report.finalize()

	profile := map[string]any{
		"name":           name,
		"enrolled":       enrolled,
		"card":           card,
		"coverage":       report,
		"recall_warning": "candidate identifications for human review; becky does not conclude",
	}
	headline(cf, "Profile: %s — enrolled=%v, recognized in %d/%d video(s).",
		name, enrolled, report.MatchedVideos, report.TotalVideos)
	if len(card.Aliases) > 0 {
		headline(cf, "  aliases: %s", strings.Join(card.Aliases, ", "))
	}
	emitJSON(profile)
	return nil
}

// --- becky corroborate "<claim>" ---

// runCorroborate cross-references a free-text claim: keyword/semantic search for
// supporting transcript moments. It surfaces the supporting data points for human
// review — never a verdict.
func runCorroborate(args []string) error {
	cf, rest := extractCommon(args)
	db, rest := flagValue(rest, "db", "")
	limit, rest := flagValue(rest, "limit", "10")
	claim := firstPositional(rest)
	if claim == "" {
		return fmt.Errorf("usage: becky corroborate \"<claim>\" [--db <path>] [--limit N]")
	}
	if db == "" {
		db = defaultDB()
	}
	result := map[string]any{
		"claim":      claim,
		"supporting": []any{},
		"disclaimer": "candidate supporting moments for human review; not a conclusion of truth",
		"reviewed":   false,
	}
	if db == "" || !fileExists(db) {
		headline(cf, "No search index found — build one with `becky index <corpus>` first.")
		result["note"] = "no search index; run `becky index <corpus>`"
		emitJSON(result)
		return nil
	}

	headline(cf, "Corroborating claim: %q", claim)
	toolArgs := []string{claim, "--db", db, "--limit", limit, "--mode", "hybrid"}
	if cf.verbose {
		toolArgs = append(toolArgs, "--verbose")
	}
	stdout, err := runTool(cf, "search", toolArgs)
	if err != nil {
		return err
	}
	res := parseSearchResults(stdout)
	support := make([]map[string]any, 0, len(res.results))
	for _, r := range res.results {
		support = append(support, map[string]any{
			"source_file": r.SourceFile,
			"timestamp":   r.Timestamp,
			"text":        r.Text,
			"speaker":     r.Speaker,
			"similarity":  r.Similarity,
			"matched":     r.Matched,
		})
	}
	result["supporting"] = support
	result["support_count"] = len(support)
	headline(cf, "Surfaced %d candidate supporting moment(s) for review:", len(support))
	for i, r := range res.results {
		if i >= 5 {
			break
		}
		headline(cf, "  - [%s @ %.1fs] %s", filepath.Base(r.SourceFile), r.Timestamp, truncate(r.Text, 70))
	}
	emitJSON(result)
	return nil
}

// --- shared helpers ---

// emitJSON writes v to stdout as indented JSON (the underlying structured result).
func emitJSON(v any) {
	beckyio.PrintJSON(v)
}

// discoverVideos returns the videos under a dir (non-recursive) or the file itself.
func discoverVideos(input string) []string {
	if fileExists(input) {
		return []string{input}
	}
	entries, err := os.ReadDir(input)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		switch strings.ToLower(filepath.Ext(e.Name())) {
		case ".mp4", ".mov", ".mkv", ".avi", ".webm", ".m4v", ".mpg", ".mpeg":
			out = append(out, filepath.Join(input, e.Name()))
		}
	}
	sort.Strings(out)
	return out
}

// defaultDB returns a conventional forensic DB path if one exists, else "".
func defaultDB() string {
	for _, p := range []string{"pipeline-out/forensic.db", "forensic.db"} {
		if fileExists(p) {
			return p
		}
	}
	return ""
}

// defaultCorpus returns a conventional corpus dir if one exists, else "".
func defaultCorpus() string {
	for _, p := range []string{"corpus", "videos", "raw"} {
		if dirExists(p) {
			return p
		}
	}
	return ""
}

func truncate(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}
