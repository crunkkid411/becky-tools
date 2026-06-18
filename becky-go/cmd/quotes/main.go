// becky-quotes — the AI quote-finding brain for becky-clip (SPEC-BECKY-SRT-QUOTES
// + SPEC-BECKY-CLIP §7). It reads a full transcript .srt, lets a Selector pick
// the passages that matter (LLM by default, --exact literal, or external anchors
// via --select-from-json), recursively expands sentence context, snaps every
// region to VERBATIM cue timestamps, and emits a small <video-stem>_QUOTES.srt
// plus a JSON summary.
//
// becky conventions: JSON to stdout, diagnostics to stderr, exit 0/nonzero,
// offline (the only model call is a LOCAL llama-server), the source .srt + video
// are NEVER modified (read-only + sha256 guard), and a missing model degrades to
// a clear note (suggesting --exact / --select-from-json) rather than crashing.
//
// The --select-from-json path is how becky-clip's GUI feeds selection: the
// assistant's frontier tier writes the anchors JSON, this tool only expands +
// snaps + emits, keeping everything offline + deterministic.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"becky-go/internal/beckyio"
	"becky-go/internal/quotes"
)

// defaultQuotesModel mirrors becky-ask's verified on-disk local text model
// (Qwen3.5-4B). BECKY_QUOTES_MODEL or --model overrides; the shared config
// supplies llama-server.exe. We never substitute a different model silently —
// absent model => degrade to a clear note.
const defaultQuotesModel = `X:\HuggingFace\models\unsloth\Qwen3.5-4B-GGUF\Qwen3.5-4B-Q4_K_M.gguf`

// runTimeout bounds the whole tool (selection + expansion). Generous: a chunked
// LLM pass over a long transcript can take minutes; the deterministic modes
// finish in milliseconds.
const runTimeout = 30 * time.Minute

type cliFlags struct {
	srt            string
	video          string
	out            string
	criteria       string
	criteriaFile   string
	exact          string
	selectFromJSON string
	model          string
	maxContextSent int
	maxRegionSec   float64
	mergeGap       float64
	temperature    float64
	logPath        string
	verbose        bool
}

func main() {
	var f cliFlags
	flag.StringVar(&f.srt, "srt", "", "FULL transcript .srt (read-only source of text + timestamps) [required]")
	flag.StringVar(&f.video, "video", "", "video path; used ONLY to derive the output name + provenance (not read for content)")
	flag.StringVar(&f.out, "out", "", "override the output path (default: <video-stem>_QUOTES.srt)")
	flag.StringVar(&f.criteria, "criteria", "", "what 'important' means for this run (selection objective)")
	flag.StringVar(&f.criteriaFile, "criteria-file", "", "read the criteria from a file")
	flag.StringVar(&f.exact, "exact", "", "OPT-IN literal phrase search \"<phrase>|<phrase>...\" (disables the LLM)")
	flag.StringVar(&f.selectFromJSON, "select-from-json", "", "external selection JSON (anchors) — the GUI/frontier path")
	flag.StringVar(&f.model, "model", "", "local LLM GGUF path (default: BECKY_QUOTES_MODEL or the verified Qwen3.5-4B)")
	flag.IntVar(&f.maxContextSent, "max-context-sentences", 4, "expansion cap per side")
	flag.Float64Var(&f.maxRegionSec, "max-region-seconds", 90, "hard cap on a single region's duration")
	flag.Float64Var(&f.mergeGap, "merge-gap", 0, "merge resulting blocks closer than S seconds")
	flag.Float64Var(&f.temperature, "temperature", 0, "model temperature (default 0 for reproducibility)")
	flag.StringVar(&f.logPath, "log", "", "write the selection/expansion rationale sidecar (JSON)")
	flag.BoolVar(&f.verbose, "verbose", false, "stderr progress")
	flag.Parse()

	os.Exit(realMain(f))
}

// realMain is the testable entry point: it returns the process exit code instead
// of calling os.Exit, so behavior can be asserted in tests.
func realMain(f cliFlags) int {
	if strings.TrimSpace(f.srt) == "" {
		fmt.Fprintln(os.Stderr, "error: --srt is required")
		return 2
	}

	// 1) parse the transcript (read-only).
	cues, err := quotes.ParseSRTFile(f.srt)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}

	// record source hashes BEFORE the run for the integrity guard.
	srtHashBefore := fileSHA256(f.srt)
	videoHashBefore := fileSHA256(f.video)

	// 2) resolve criteria text.
	criteria := strings.TrimSpace(f.criteria)
	if criteria == "" && strings.TrimSpace(f.criteriaFile) != "" {
		data, rerr := os.ReadFile(f.criteriaFile)
		if rerr != nil {
			fmt.Fprintf(os.Stderr, "error: read --criteria-file: %v\n", rerr)
			return 1
		}
		criteria = strings.TrimSpace(string(data))
	}

	// 3) choose the selection mode + (optional) expander.
	mode, selector, expander, err := buildSelector(f, cues, criteria)
	if err != nil {
		// degrade-never-crash: clear note + nonzero exit, suggest the offline paths.
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		fmt.Fprintln(os.Stderr, "hint: use --exact \"<phrase>|...\" or --select-from-json <anchors.json> for an offline run.")
		return 1
	}
	if criteria == "" && mode == "local" {
		fmt.Fprintln(os.Stderr, "warning: no --criteria given; using a generic-salience objective (SPEC §4.2).")
	}

	// 4) run the deterministic pipeline.
	ctx, cancel := context.WithTimeout(context.Background(), runTimeout)
	defer cancel()
	opts := quotes.Options{
		Selector: selector,
		Expander: expander,
		Criteria: criteria,
		Caps: quotes.ExpandCaps{
			MaxSentencesPerSide: f.maxContextSent,
			MaxRegionSeconds:    f.maxRegionSec,
		},
		MergeGap:     f.mergeGap,
		ChunkCues:    chunkCuesFor(mode),
		ChunkOverlap: 2,
	}
	summary, err := quotes.Run(ctx, cues, opts)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: selection failed: %v\n", err)
		fmt.Fprintln(os.Stderr, "hint: the local model is the only model path; use --exact or --select-from-json to run fully offline.")
		return 1
	}

	// 5) write the _QUOTES.srt.
	outPath := deriveOutPath(f.out, f.video, f.srt)
	if werr := os.WriteFile(outPath, []byte(summary.SRT()), 0o644); werr != nil {
		fmt.Fprintf(os.Stderr, "error: write %s: %v\n", outPath, werr)
		return 1
	}
	beckyio.Logf(f.verbose, "becky-quotes: wrote %d regions -> %s", summary.AfterMerge, outPath)

	// 6) optional --log rationale sidecar.
	if strings.TrimSpace(f.logPath) != "" {
		if lerr := writeLog(f.logPath, f, mode, outPath, summary); lerr != nil {
			fmt.Fprintf(os.Stderr, "warning: could not write --log %s: %v\n", f.logPath, lerr)
		}
	}

	// 7) integrity guard: the source .srt + video must be byte-identical.
	if srtHashBefore != "" && srtHashBefore != fileSHA256(f.srt) {
		fmt.Fprintln(os.Stderr, "error: source .srt changed during the run (integrity violation)")
		return 1
	}
	if videoHashBefore != "" && videoHashBefore != fileSHA256(f.video) {
		fmt.Fprintln(os.Stderr, "error: source video changed during the run (integrity violation)")
		return 1
	}

	// 8) JSON summary to stdout.
	beckyio.PrintJSON(buildPayload(f, mode, outPath, srtHashBefore, videoHashBefore, criteria, summary))
	return 0
}

// chunkCuesFor returns the map-reduce window size (SPEC §4.5). Only the local LLM
// mode chunks; exact/json resolve over the whole transcript at once. ~700 cues is
// a conservative window that fits the 8K context with the system prompt.
func chunkCuesFor(mode string) int {
	if mode == "local" {
		return 700
	}
	return 0
}
