// becky-judge — Stage 2 (JUDGE) of the forensic evidence pipeline.
//
//	Stage 1 RECALL  : internal/qmd hybrid search casts a wide net over the transcripts.
//	Stage 2 JUDGE   : a large LLM (Claude, via internal/agentrun) reads ONLY the Stage-1
//	                  candidate windows with the deputy's rubric + the case alias map,
//	                  resolves coded language ("green hair" = the man my ex lives with =
//	                  Hair Jordan), rejects satire shields / "I'm-the-victim" inversions /
//	                  gaming-stream noise, and keeps only genuine hits. (this tool)
//	Stage 3 VERIFY  : becky-tools confirms WHO is on screen/voice for the survivors.
//
// Output is a hit-list `_forensic_hits.json` in becky-hits' input shape, so the survivors
// flow straight into Becky Review:  becky-hits --hits _forensic_hits.json --folder <dir>
//
// Usage:
//
//	becky-judge --folder E:\TakingBack2007 --query "asking people to harass Shelby or Hair Jordan" \
//	            --rubric rubric.txt --aliases aliases.txt [--out _forensic_hits.json] [--limit 40]
//	becky-judge ... --dry-run     # Stage 1 only: emit every candidate, skip the LLM judge
//	becky-judge --selftest        # offline proof (no qmd, no LLM): synthetic judge round
//
// Read-only over evidence: it reads .srt sidecars + the qmd index and writes only the
// small hit-list JSON. Degrade-never-crash: no Claude (or an LLM error) falls back to
// emitting all candidates with a clear note, so Stage 1 is never lost.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"becky-go/internal/agentrun"
	"becky-go/internal/footage"
	"becky-go/internal/qmd"
	"becky-go/internal/sidecar"
)

// candidate is one Stage-1 hit expanded into a context window the judge can reason over.
type candidate struct {
	ID       int     `json:"id"`
	SRT      string  `json:"srt"`   // transcript basename (for the hit-list)
	Video    string  `json:"video"` // resolved source video path
	Name     string  `json:"name"`  // video basename
	In       float64 `json:"in"`    // window start (sec, from the .srt)
	Out      float64 `json:"out"`   // window end (sec, from the .srt)
	Window   string  `json:"window"`
	QmdScore float64 `json:"qmd_score"`
}

// verdict is the judge's decision for one candidate (the LLM's JSON-schema output).
type verdict struct {
	ID         int     `json:"id"`
	Keep       bool    `json:"keep"`
	Who        string  `json:"who"`
	Confidence float64 `json:"confidence"`
	Reason     string  `json:"reason"`
}

type outHit struct {
	SRT string `json:"srt"`
	In  string `json:"in"`
	Out string `json:"out"`
	Q   string `json:"q"`
}

type outFile struct {
	Name   string   `json:"name"`
	Folder string   `json:"folder"`
	Query  string   `json:"query"`
	Hits   []outHit `json:"hits"`
}

type report struct {
	Out        string `json:"out"`
	Query      string `json:"query"`
	Mode       string `json:"recall_mode"` // qmd: hybrid | keyword | unavailable
	Candidates int    `json:"candidates"`
	Kept       int    `json:"kept"`
	Judged     bool   `json:"judged"` // false = dry-run / judge unavailable (all kept)
	Note       string `json:"note,omitempty"`
}

func main() {
	fs := flag.NewFlagSet("becky-judge", flag.ExitOnError)
	folder := fs.String("folder", "", "case folder the transcripts live in (the folder Becky Review opens)")
	query := fs.String("query", "", "the forensic ask, e.g. \"asking people to harass Shelby or Hair Jordan\"")
	rubricArg := fs.String("rubric", "", "judge rubric: a file path OR inline text (default: built-in forensic rubric)")
	aliasArg := fs.String("aliases", "", "case alias map: a file path OR inline text (coded reference -> who it really is)")
	out := fs.String("out", "", "output hit-list path (default: <folder>\\_forensic_hits.json)")
	limit := fs.Int("limit", 40, "max Stage-1 candidates to judge")
	window := fs.Int("window", 4, "context cues on EACH side of a hit (more context = better referent resolution)")
	model := fs.String("model", "", "Claude model for the judge (default: CLI default)")
	dryRun := fs.Bool("dry-run", false, "Stage 1 only: emit every candidate, skip the LLM judge")
	jsonOut := fs.Bool("json", false, "emit the machine report as JSON to stdout")
	selftest := fs.Bool("selftest", false, "run the offline self-test (no qmd, no LLM) and exit")
	_ = fs.Parse(os.Args[1:])

	if *selftest {
		runSelftest()
		return
	}
	if strings.TrimSpace(*folder) == "" || strings.TrimSpace(*query) == "" {
		fatalf("--folder and --query are required (or --selftest)")
	}
	if fi, err := os.Stat(*folder); err != nil || !fi.IsDir() {
		fatalf("not a folder: %s", *folder)
	}

	idx, err := footage.Index(*folder)
	if err != nil {
		fatalf("index folder: %v", err)
	}

	// Stage 1: RECALL.
	cands, mode := recall(*query, idx, *limit, *window)
	if len(cands) == 0 {
		fatalf("no candidates from Stage-1 recall (qmd mode=%s) — try a different query", mode)
	}

	// Stage 2: JUDGE (or dry-run / degrade).
	var kept []candidate
	keepReason := map[int]verdict{}
	judged := false
	note := ""
	switch {
	case *dryRun:
		kept = cands
		note = "dry-run: every candidate kept (no LLM judge)"
	default:
		verdicts, jerr := judge(cands, loadText(*rubricArg, defaultRubric), loadText(*aliasArg, ""), *query, *model)
		if jerr != nil {
			kept = cands
			note = "judge unavailable (" + firstLine(jerr) + ") — every candidate kept; re-run when Claude is available"
		} else {
			judged = true
			for _, v := range verdicts {
				if v.Keep {
					keepReason[v.ID] = v
				}
			}
			for _, c := range cands {
				if _, ok := keepReason[c.ID]; ok {
					kept = append(kept, c)
				}
			}
		}
	}

	of := hitsFor(kept, keepReason, *folder, *query)

	outPath := *out
	if outPath == "" {
		outPath = filepath.Join(*folder, "_forensic_hits.json")
	}
	if err := writeJSON(outPath, of); err != nil {
		fatalf("write hit-list: %v", err)
	}

	rep := report{Out: outPath, Query: *query, Mode: mode, Candidates: len(cands), Kept: len(of.Hits), Judged: judged, Note: note}
	if *jsonOut {
		_ = json.NewEncoder(os.Stdout).Encode(rep)
	} else {
		fmt.Printf("Stage 1 (qmd %s): %d candidates\n", rep.Mode, rep.Candidates)
		if judged {
			fmt.Printf("Stage 2 (judge): kept %d of %d\n", rep.Kept, rep.Candidates)
		} else {
			fmt.Printf("Stage 2: SKIPPED — %s\n", note)
		}
		fmt.Printf("Wrote %d hits -> %s\n", rep.Kept, rep.Out)
		fmt.Printf("Next: becky-hits --hits %q --folder %q   (or double-click \"Open Forensic Hits\")\n", outPath, *folder)
	}
}

// ---- Stage 1: RECALL --------------------------------------------------------

func recall(query string, idx footage.FolderIndex, limit, halfWindow int) ([]candidate, string) {
	hits, mode, _ := qmd.Search(query)
	var cands []candidate
	seen := map[string]bool{}
	id := 0
	for _, h := range hits {
		if len(cands) >= limit {
			break
		}
		srtName := qmd.SourceName(h)
		if srtName == "" {
			continue
		}
		v, ok := videoByTranscript(idx, srtName)
		if !ok {
			continue // no source video in the folder -> can't make a reviewable clip
		}
		in, out, win, ok := windowAround(v.TranscriptPath, qmd.FirstTimecode(h.Snippet), halfWindow)
		if !ok {
			continue
		}
		key := v.Path + "|" + strconv.FormatFloat(in, 'f', 1, 64)
		if seen[key] {
			continue
		}
		seen[key] = true
		id++
		cands = append(cands, candidate{
			ID: id, SRT: baseName(v.TranscriptPath), Video: v.Path, Name: v.Name,
			In: in, Out: out, Window: win, QmdScore: h.Score,
		})
	}
	return cands, mode
}

// windowAround parses the .srt, finds the cue nearest t (or the first when t<0), and
// returns the [in,out] span + a timecoded text window of +/- halfWindow cues.
func windowAround(srtPath string, t float64, half int) (float64, float64, string, bool) {
	sub, err := sidecar.ParseSubtitle(srtPath)
	if err != nil || len(sub.Segments) == 0 {
		return 0, 0, "", false
	}
	idx := 0
	if t >= 0 {
		best := absf(sub.Segments[0].Start - t)
		for i, s := range sub.Segments {
			if d := absf(s.Start - t); d < best {
				best, idx = d, i
			}
		}
	}
	lo, hi := idx-half, idx+half
	if lo < 0 {
		lo = 0
	}
	if hi >= len(sub.Segments) {
		hi = len(sub.Segments) - 1
	}
	var b strings.Builder
	for i := lo; i <= hi; i++ {
		s := sub.Segments[i]
		fmt.Fprintf(&b, "[%s] %s\n", hms(s.Start), strings.TrimSpace(s.Text))
	}
	return sub.Segments[lo].Start, sub.Segments[hi].End, strings.TrimSpace(b.String()), true
}

// ---- Stage 2: JUDGE ---------------------------------------------------------

func judge(cands []candidate, rubric, aliases, query, model string) ([]verdict, error) {
	if agentrun.ResolveBin() == "" {
		return nil, fmt.Errorf("claude CLI not found on PATH")
	}
	prompt := buildPrompt(cands, rubric, aliases, query)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	res, err := agentrun.Run(ctx, agentrun.AgentSpec{
		PromptStdin:  prompt,
		JSONSchema:   verdictSchema,
		Model:        model,
		MaxTurns:     1,
		MaxBudgetUSD: 3.0,
		WorkDir:      os.TempDir(), // neutral dir: no project CLAUDE.md, stays on-task
	})
	if err != nil {
		return nil, err
	}
	if res.IsError {
		return nil, fmt.Errorf("judge returned an error result")
	}
	raw := res.StructuredOutput
	if len(raw) == 0 {
		raw = []byte(res.Result) // fallback if the CLI put JSON in result
	}
	var parsed struct {
		Verdicts []verdict `json:"verdicts"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, fmt.Errorf("parse judge output: %w", err)
	}
	return parsed.Verdicts, nil
}

// verdictSchema MUST be a single line with no spaces: it is passed as a --json-schema
// argv to the Windows claude.cmd shim, which mangles multi-line / spaced args.
const verdictSchema = `{"type":"object","properties":{"verdicts":{"type":"array","items":{"type":"object","properties":{"id":{"type":"integer"},"keep":{"type":"boolean"},"who":{"type":"string"},"confidence":{"type":"number"},"reason":{"type":"string"}},"required":["id","keep","reason"]}}},"required":["verdicts"]}`

const defaultRubric = `You are a careful forensic evidence judge for a harassment investigation.
Decide whether each transcript window is a GENUINE hit for the deputy's request.
- Resolve coded / indirect references using the alias map; a hit counts even if the
  target is named only by an alias, nickname, or description.
- KEEP only windows that genuinely match the request.
- REJECT: satire/"just joking" shields used to disguise a real directive; the speaker
  casting himself as the victim to invert who is harassing whom; ordinary gaming-stream
  banter with no real target; windows where you cannot determine WHO is meant.
- When unsure who the referent is, set keep=false and say why.`

func buildPrompt(cands []candidate, rubric, aliases, query string) string {
	var b strings.Builder
	b.WriteString(rubric)
	b.WriteString("\n\n")
	if strings.TrimSpace(aliases) != "" {
		b.WriteString("ALIAS MAP (coded reference -> who it really is):\n")
		b.WriteString(strings.TrimSpace(aliases))
		b.WriteString("\n\n")
	}
	b.WriteString("DEPUTY'S REQUEST: ")
	b.WriteString(query)
	b.WriteString("\n\nFor EACH candidate window below, return a verdict {id, keep, who, confidence, reason}.\n")
	b.WriteString("Judge ONLY the text shown; do not invent context.\n\n")
	for _, c := range cands {
		fmt.Fprintf(&b, "=== candidate %d  (file: %s) ===\n%s\n\n", c.ID, c.Name, c.Window)
	}
	return b.String()
}

// hitsFor builds the becky-hits-shaped hit-list from the kept candidates, labelling
// each with the judge's who+reason (else the video name). Shared by main + the selftest.
func hitsFor(kept []candidate, reason map[int]verdict, folder, query string) outFile {
	of := outFile{Name: "Forensic: " + query, Folder: folder, Query: query}
	for _, c := range kept {
		q := c.Name
		if v, ok := reason[c.ID]; ok && strings.TrimSpace(v.Reason) != "" {
			q = strings.TrimSpace(strings.TrimPrefix(v.Who+": "+v.Reason, ": "))
		}
		of.Hits = append(of.Hits, outHit{
			SRT: c.SRT,
			In:  strconv.FormatFloat(c.In, 'f', 3, 64),
			Out: strconv.FormatFloat(c.Out, 'f', 3, 64),
			Q:   q,
		})
	}
	return of
}

// ---- helpers ----------------------------------------------------------------

// videoByTranscript finds the indexed video whose transcript sidecar basename matches
// srtName (case-insensitive) — the same mapping Becky Review uses for qmd hits.
func videoByTranscript(idx footage.FolderIndex, srtName string) (footage.Video, bool) {
	want := strings.ToLower(baseName(srtName))
	for _, v := range idx.Videos {
		if v.HasTranscript && strings.ToLower(baseName(v.TranscriptPath)) == want {
			return v, true
		}
	}
	return footage.Video{}, false
}

// loadText returns the contents of arg when it is a path to an existing file, else arg
// itself (inline text), else def.
func loadText(arg, def string) string {
	arg = strings.TrimSpace(arg)
	if arg == "" {
		return def
	}
	if b, err := os.ReadFile(arg); err == nil {
		return string(b)
	}
	return arg
}

func writeJSON(path string, v any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o644)
}

func baseName(p string) string {
	p = strings.ReplaceAll(p, "\\", "/")
	if i := strings.LastIndexByte(p, '/'); i >= 0 {
		return p[i+1:]
	}
	return p
}

func hms(sec float64) string {
	if sec < 0 {
		sec = 0
	}
	t := int(sec)
	return fmt.Sprintf("%02d:%02d:%02d", t/3600, (t%3600)/60, t%60)
}

func absf(f float64) float64 {
	if f < 0 {
		return -f
	}
	return f
}

func firstLine(err error) string {
	if err == nil {
		return ""
	}
	s := err.Error()
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

func fatalf(format string, a ...any) {
	fmt.Fprintf(os.Stderr, "becky-judge: "+format+"\n", a...)
	os.Exit(1)
}
