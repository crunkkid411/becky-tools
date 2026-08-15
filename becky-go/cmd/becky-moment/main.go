// becky-moment — find the self-contained micro-stories in a long video's
// transcript and emit them as tight [in,out] windows ready to cut.
//
//	becky-moment --transcript <video-or-transcript> [--out hits.json]
//	becky-moment --folder <dir> [--out hits.json]
//	becky-moment --selftest
//
// This is step 1 of the short-form repurposing pipeline (HANDOFF-SHORTS-PIPELINE.md).
// It deliberately depends on NOTHING new: it reads the transcripts becky already
// produces (`internal/sidecar` parses .srt/.vtt/.json3/transcript.json) and writes
// the hit-list shape `becky-hits` already consumes, so the result flows straight
// onto a Becky Review timeline:
//
//	becky-moment --folder E:\Footage --out moments.json
//	becky-hits --hits moments.json --folder E:\Footage
//
// HOW IT DECIDES (and why it is allowed to say "I don't know"):
//   - STRUCTURE is measured deterministically (internal/moment): where a thought
//     starts, whether it completes, whether the opening dangles mid-setup, pace,
//     and length fit. No model, same input -> same output.
//   - CONTENT is judged by an LLM (OpenCode Zen, see zen.go), which is the only
//     genuinely fuzzy call here.
//   - The two are corroborated. Agreement -> a conclusion. Disagreement, or only
//     one signal available -> reported as a CANDIDATE, not a pick. A moment is
//     never promoted on one signal (FORENSIC-OUTPUT-PHILOSOPHY.md).
//
// Without an API key it still works: every moment comes back ranked by structure
// alone and clearly labelled "candidate", with a note saying the content pass did
// not run. Degrade, never crash.
//
// JSON to stdout (or --out); diagnostics to stderr; exit 0 on success. Source
// media is never opened or modified — only transcript sidecars are read.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"becky-go/internal/moment"
	"becky-go/internal/pathx"
	"becky-go/internal/sidecar"
)

const toolVersion = "becky-moment v1.0.0"

// hit is becky-hits' input record. Field names must stay in lockstep with
// cmd/becky-hits/main.go's `hit` — this is a real cross-tool seam, and the seam
// test in main_test.go asserts the JSON keys so a rename there cannot silently
// break the handoff (this is exactly the class of bug HANDOFF-SHORTS-PIPELINE.md
// §2 is about).
type hit struct {
	SRT      string `json:"srt"`
	In       string `json:"in"`
	Out      string `json:"out"`
	Q        string `json:"q"`
	Question string `json:"?,omitempty"`
}

// momentOut is one reported moment: the becky-hits fields plus the evidence.
type momentOut struct {
	Source     string  `json:"source"`
	Start      float64 `json:"start"`
	End        float64 `json:"end"`
	Duration   float64 `json:"duration"`
	Score      float64 `json:"score"`
	Confidence string  `json:"confidence"`
	Basis      string  `json:"basis"`
	Extended   bool    `json:"extended"`
	Text       string  `json:"text"`
}

type report struct {
	Tool        string      `json:"tool"`
	Transcripts int         `json:"transcripts"`
	Candidates  int         `json:"candidates"`
	Moments     []momentOut `json:"moments"`
	Hits        []hit       `json:"hits"`
	JudgeModel  string      `json:"judge_model,omitempty"`
	Notes       []string    `json:"notes,omitempty"`
}

func main() {
	var (
		transcript = flag.String("transcript", "", "one video or transcript file")
		folder     = flag.String("folder", "", "a folder of videos/transcripts")
		out        = flag.String("out", "", "write JSON here (default: stdout)")
		top        = flag.Int("top", 10, "keep the N best moments")
		minDur     = flag.Float64("min", 12, "minimum moment length (seconds)")
		maxDur     = flag.Float64("max", 60, "maximum moment length (seconds)")
		extend     = flag.Float64("extend", 8, "seconds past --max the ending may reach to complete a thought")
		judge      = flag.Bool("judge", true, "run the LLM content pass (needs BECKY_ZEN_API_KEY)")
		model      = flag.String("model", defaultZenModel, "judge model id (OpenCode Zen)")
		allowPaid  = flag.Bool("allow-paid", false, "permit a METERED judge model (Zen auto-reloads $20 below $5)")
		selftest   = flag.Bool("selftest", false, "run the offline proof and exit")
		verbose    = flag.Bool("verbose", false, "progress to stderr")
	)
	flag.Parse()

	if *selftest {
		os.Exit(runSelftest())
	}

	target := *transcript
	if target == "" && flag.NArg() > 0 {
		target = flag.Arg(0)
	}
	if target == "" && *folder == "" {
		fmt.Fprintln(os.Stderr, "usage: becky-moment --transcript <file> | --folder <dir> | --selftest")
		os.Exit(2)
	}

	opt := moment.Options{
		MinDuration:  *minDur,
		MaxDuration:  *maxDur,
		ExtendBudget: *extend,
	}

	sources, err := collectTranscripts(target, *folder)
	if err != nil {
		fmt.Fprintf(os.Stderr, "becky-moment: %v\n", err)
		os.Exit(1)
	}
	if len(sources) == 0 {
		// Not a crash: an unindexed folder is a normal state, and the caller
		// deserves a valid empty result with the reason.
		emit(report{
			Tool:  toolVersion,
			Notes: []string{"no transcript sidecars found — run becky-transcribe first"},
		}, *out)
		return
	}

	rep := report{Tool: toolVersion, Transcripts: len(sources)}
	var allCands []moment.Candidate
	var owner []string // parallel to allCands: which transcript each came from

	for _, src := range sources {
		sub, err := sidecar.ParseSubtitle(src)
		if err != nil {
			rep.Notes = append(rep.Notes, fmt.Sprintf("%s: %v", pathx.Base(src), err))
			continue
		}
		segs := make([]moment.Segment, 0, len(sub.Segments))
		for _, s := range sub.Segments {
			segs = append(segs, moment.Segment{Start: s.Start, End: s.End, Text: s.Text})
		}
		cands := moment.Find(segs, opt)
		if *verbose {
			fmt.Fprintf(os.Stderr, "%s: %d cues -> %d candidates\n", pathx.Base(src), len(segs), len(cands))
		}
		for _, c := range cands {
			allCands = append(allCands, c)
			owner = append(owner, src)
		}
	}
	rep.Candidates = len(allCands)

	// The content pass. Any failure here is a DEGRADE: we keep every candidate
	// and say plainly that only one signal is behind the ranking.
	var verdicts []moment.Judgement
	if *judge && len(allCands) > 0 {
		jf, err := zenJudge(*model, *allowPaid, 12)
		if err != nil {
			rep.Notes = append(rep.Notes, "content pass skipped: "+err.Error())
		} else {
			rep.JudgeModel = *model
			verdicts, err = jf(context.Background(), allCands)
			if err != nil {
				rep.Notes = append(rep.Notes, "content pass failed: "+err.Error())
			}
		}
	} else if !*judge {
		rep.Notes = append(rep.Notes, "content pass disabled (--judge=false): ranking is structure-only")
	}

	ranked := moment.Rank(allCands, verdicts)
	if *top > 0 && len(ranked) > *top {
		ranked = ranked[:*top]
	}

	for _, r := range ranked {
		src := ""
		for i := range allCands {
			if allCands[i].Start == r.Start && allCands[i].End == r.End {
				src = owner[i]
				break
			}
		}
		rep.Moments = append(rep.Moments, momentOut{
			Source:     src,
			Start:      r.Start,
			End:        r.End,
			Duration:   r.Dur(),
			Score:      r.Final,
			Confidence: string(r.Confidence),
			Basis:      r.Basis,
			Extended:   r.Extended,
			Text:       r.Text,
		})
		rep.Hits = append(rep.Hits, hit{
			SRT: pathx.Base(src),
			In:  formatTC(r.Start),
			Out: formatTC(r.End),
			Q:   firstLine(r.Text),
		})
	}

	if len(verdicts) == 0 && len(ranked) > 0 {
		rep.Notes = append(rep.Notes,
			"every moment is CANDIDATE-only: the content pass did not run, so these are ranked on structure alone")
	}
	emit(rep, *out)
}

// collectTranscripts resolves the CLI target(s) to transcript sidecar paths. A
// video path resolves through sidecar.FindSubtitle; a transcript path is used
// directly; a folder is scanned.
func collectTranscripts(target, folder string) ([]string, error) {
	var out []string
	if target != "" {
		if isTranscript(target) {
			out = append(out, target)
		} else if s := sidecar.FindSubtitle(target); s != "" {
			out = append(out, s)
		} else {
			return nil, fmt.Errorf("no transcript sidecar beside %s", pathx.Base(target))
		}
	}
	if folder != "" {
		entries, err := os.ReadDir(folder)
		if err != nil {
			return nil, err
		}
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			p := filepath.Join(folder, e.Name())
			if isTranscript(p) {
				out = append(out, p)
			}
		}
	}
	sort.Strings(out)
	return dedupe(out), nil
}

func isTranscript(p string) bool {
	l := strings.ToLower(p)
	for _, ext := range []string{".srt", ".vtt", ".json3"} {
		if strings.HasSuffix(l, ext) {
			return true
		}
	}
	return strings.HasSuffix(l, ".transcript.json")
}

func dedupe(in []string) []string {
	seen := map[string]bool{}
	out := in[:0]
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

// formatTC renders seconds as HH:MM:SS.mmm — the timecode form becky-hits parses.
func formatTC(sec float64) string {
	if sec < 0 {
		sec = 0
	}
	h := int(sec) / 3600
	m := (int(sec) % 3600) / 60
	s := sec - float64(h*3600+m*60)
	return fmt.Sprintf("%02d:%02d:%06.3f", h, m, s)
}

// firstLine trims a moment's text down to a usable clip label.
func firstLine(text string) string {
	t := strings.TrimSpace(strings.ReplaceAll(text, "\n", " "))
	const max = 90
	if len(t) <= max {
		return t
	}
	if i := strings.LastIndex(t[:max], " "); i > 20 {
		return t[:i] + "…"
	}
	return t[:max] + "…"
}

func emit(rep report, out string) {
	data, err := json.MarshalIndent(rep, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "becky-moment: %v\n", err)
		os.Exit(1)
	}
	data = append(data, '\n')
	if out == "" {
		os.Stdout.Write(data)
		return
	}
	if err := os.WriteFile(out, data, 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "becky-moment: %v\n", err)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "wrote %s\n", out)
}
