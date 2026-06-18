package assistant

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"becky-go/internal/footage"
)

// funnel.go is the 500 GB retrieval funnel (R-AI §3). The HARD invariant: the
// model NEVER ingests the folder — not 500 GB, not 5 GB. Per turn the model sees
// only a fixed system prompt + action catalog + the timeline + ONE WINDOW of
// retrieved candidate cue snippets. Everything else is this deterministic funnel:
//
//	[1] CANDIDATE RETRIEVAL — footage.GrepTranscripts (literal) + caller-supplied
//	    becky-search hits (semantic), merged + capped to top-K.            (Tier 0)
//	[2] MAP — slice candidates into token-bounded windows; per window ONE model
//	    call judges which cue ranges match.                          (mid frontier)
//	[3] REDUCE — dedup + sort the per-window hits.                        (Tier 0)
//	[4] PLAN — ONE final model call over the SMALL reduced set → actions. (deep)
//
// This file owns [1]/[3] (pure Go, tested offline) and the windowing for [2], and
// builds the becky-quotes --select-from-json command for find_quotes. Steps [2]
// and [4]'s model calls go through a Backend (faked in tests).

// Defaults bounding the funnel so the model's context never scales with folder
// size. topK caps candidates after retrieval; windowCues caps cues per map call;
// windowOverlap keeps 1 cue of overlap so a match straddling a window boundary is
// not lost (becky-quotes §4.5 discipline).
const (
	defaultTopK          = 120
	defaultWindowCues    = 60
	defaultWindowOverlap = 1
)

// Funnel runs the retrieval pipeline. The caller execs becky-search and passes
// its hits in (this package stays DB/model-free so tests run offline).
type Funnel struct {
	TopK          int
	WindowCues    int
	WindowOverlap int
}

// NewFunnel builds a Funnel with the default bounds.
func NewFunnel() *Funnel {
	return &Funnel{TopK: defaultTopK, WindowCues: defaultWindowCues, WindowOverlap: defaultWindowOverlap}
}

// Retrieve is step [1]: the deterministic candidate set. It runs footage grep for
// the literal terms, merges the caller-provided semantic hits, de-duplicates by
// (source,timestamp), ranks, and caps to TopK. The result is the ONLY material
// that may reach the model — bounded by construction.
func (f *Funnel) Retrieve(index footage.FolderIndex, terms []string, searchHits []footage.Candidate) []footage.Candidate {
	grep := footage.GrepTranscripts(index, terms)
	merged := mergeCandidates(grep, searchHits)

	topK := f.TopK
	if topK <= 0 {
		topK = defaultTopK
	}
	if len(merged) > topK {
		merged = merged[:topK]
	}
	return merged
}

// Window is one token-bounded slice of candidates for a single map call.
type Window struct {
	Index      int                 `json:"index"`
	Candidates []footage.Candidate `json:"candidates"`
}

// Windows is step [2]'s splitter: slices candidates into WindowCues-sized windows
// with WindowOverlap cues of overlap. A 6-hour transcript reduced to e.g. 600
// candidates is judged in ~10 windowed calls, never one giant prompt.
func (f *Funnel) Windows(candidates []footage.Candidate) []Window {
	size := f.WindowCues
	if size <= 0 {
		size = defaultWindowCues
	}
	overlap := f.WindowOverlap
	if overlap < 0 || overlap >= size {
		overlap = defaultWindowOverlap
	}
	if len(candidates) == 0 {
		return nil
	}

	var windows []Window
	step := size - overlap
	for start := 0; start < len(candidates); start += step {
		end := start + size
		if end > len(candidates) {
			end = len(candidates)
		}
		win := make([]footage.Candidate, end-start)
		copy(win, candidates[start:end])
		windows = append(windows, Window{Index: len(windows), Candidates: win})
		if end == len(candidates) {
			break
		}
	}
	return windows
}

// Reduce is step [3]: merge the per-window selected cues into a single deduped,
// chronologically-sorted set. Selection from the map step is by (source,timestamp)
// identity, so duplicates from overlapping windows collapse cleanly.
func Reduce(selected [][]footage.Candidate) []footage.Candidate {
	seen := map[string]bool{}
	var out []footage.Candidate
	for _, group := range selected {
		for _, c := range group {
			key := c.Source + "|" + fmtFloat(c.Timestamp)
			if seen[key] {
				continue
			}
			seen[key] = true
			out = append(out, c)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Source != out[j].Source {
			return out[i].Source < out[j].Source
		}
		return out[i].Timestamp < out[j].Timestamp
	})
	return out
}

// mergeCandidates combines literal grep hits with semantic search hits, removing
// duplicates by (source,timestamp) and re-ranking by score (grep score and
// search score share a higher-is-better convention). Deterministic order.
func mergeCandidates(a, b []footage.Candidate) []footage.Candidate {
	seen := map[string]int{} // key -> index in out
	out := make([]footage.Candidate, 0, len(a)+len(b))
	add := func(c footage.Candidate) {
		key := c.Source + "|" + fmtFloat(c.Timestamp)
		if i, ok := seen[key]; ok {
			if c.Score > out[i].Score { // keep the stronger signal's score
				out[i].Score = c.Score
			}
			return
		}
		seen[key] = len(out)
		out = append(out, c)
	}
	for _, c := range a {
		add(c)
	}
	for _, c := range b {
		add(c)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		if out[i].Source != out[j].Source {
			return out[i].Source < out[j].Source
		}
		return out[i].Timestamp < out[j].Timestamp
	})
	return out
}

func fmtFloat(f float64) string {
	return fmt.Sprintf("%.3f", f)
}

// --- find_quotes: anchors file + becky-quotes --select-from-json command -----

// QuoteAnchor is one selection the frontier backend produces for becky-quotes'
// --select-from-json mode (SPEC §7): a source + in/out timecodes copied VERBATIM
// from cue boundaries (never invented) + the matched text. becky-quotes only
// expands+emits from these — the hard selection stays with the frontier model
// while the tool stays deterministic.
type QuoteAnchor struct {
	Source string `json:"source"` // source video (or its transcript) the cue is in
	In     string `json:"in"`     // cue-boundary timecode, e.g. "00:13:12,640"
	Out    string `json:"out"`    // cue-boundary timecode, e.g. "00:13:20,560"
	Text   string `json:"text"`   // the matched cue text (verbatim)
}

// WriteAnchors writes the anchors JSON to dir and returns its path. The file is
// the bridge to becky-quotes --select-from-json. Caller-owned work dir (never the
// case folder); the originals are untouched.
func WriteAnchors(dir string, anchors []QuoteAnchor) (string, error) {
	if dir == "" {
		dir = os.TempDir()
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("create anchors dir %s: %w", dir, err)
	}
	data, err := json.MarshalIndent(anchors, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal anchors: %w", err)
	}
	path := filepath.Join(dir, "quote_anchors.json")
	if err := os.WriteFile(path, append(data, '\n'), 0o644); err != nil {
		return "", fmt.Errorf("write anchors %s: %w", path, err)
	}
	return path, nil
}

// QuotesCommand builds the deterministic becky-quotes --select-from-json command
// for an anchors file over a given source SRT. The GUI runs it on ✓; this package
// only FORMS the argv, so unit tests can assert the command shape without the
// binary present (the brief's requirement).
func QuotesCommand(srtPath, anchorsPath string) ExecCommand {
	return ExecCommand{
		Bin:  "becky-quotes",
		Args: []string{"--srt", srtPath, "--select-from-json", anchorsPath},
		Note: "expand frontier-selected anchors into a verbatim quote SRT (deterministic)",
	}
}

// AnchorsFromCandidates converts reduced candidates into QuoteAnchors, formatting
// the in/out seconds back into SRT timecodes. Used when the funnel itself (not the
// model) supplies the anchors for a literal find_quotes.
func AnchorsFromCandidates(cands []footage.Candidate) []QuoteAnchor {
	out := make([]QuoteAnchor, 0, len(cands))
	for _, c := range cands {
		out = append(out, QuoteAnchor{
			Source: c.Source,
			In:     secondsToTimecode(c.Timestamp),
			Out:    secondsToTimecode(c.End),
			Text:   c.Text,
		})
	}
	return out
}

// secondsToTimecode formats seconds as the SRT "HH:MM:SS,mmm" form.
func secondsToTimecode(sec float64) string {
	if sec < 0 {
		sec = 0
	}
	totalMs := int64(sec*1000 + 0.5)
	ms := totalMs % 1000
	totalS := totalMs / 1000
	s := totalS % 60
	totalM := totalS / 60
	m := totalM % 60
	h := totalM / 60
	return fmt.Sprintf("%02d:%02d:%02d,%03d", h, m, s, ms)
}

// candidatesBlock renders a window's candidates as a compact, numbered text block
// for the model prompt — the ONLY variable part of the per-turn context, and it is
// window-bounded by construction. Kept here so the prompt builder and the funnel
// share one format.
func candidatesBlock(cands []footage.Candidate) string {
	var b strings.Builder
	for i, c := range cands {
		fmt.Fprintf(&b, "%d) [%s @ %s] %s\n", i+1, filepath.Base(c.Source), secondsToTimecode(c.Timestamp), c.Text)
	}
	return b.String()
}

// runMap runs one map-window model call (step [2]) and returns the candidates the
// model selected from that window. The model is asked for the 1-based indices of
// matching cues; anything it returns that is out of range is ignored. Degrades to
// an empty selection on any backend error (the window simply contributes nothing).
func runMap(ctx context.Context, be Backend, criteria string, win Window) []footage.Candidate {
	if be == nil || be.Available() != nil {
		return nil
	}
	req := Request{
		System: mapSystemPrompt,
		User:   mapUserPrompt(criteria, win.Candidates),
		Tier:   TierFrontier,
	}
	out, err := be.Complete(ctx, req)
	if err != nil {
		return nil
	}
	idxs := parseIndexList(out)
	var sel []footage.Candidate
	for _, n := range idxs {
		if n >= 1 && n <= len(win.Candidates) {
			sel = append(sel, win.Candidates[n-1])
		}
	}
	return sel
}

// parseIndexList extracts 1-based integers from a model reply, accepting a JSON
// array ([1,4,7]) or a loose comma/space list. Non-numbers are skipped.
func parseIndexList(s string) []int {
	s = strings.TrimSpace(stripFences(s))
	// Try JSON array first.
	if strings.HasPrefix(s, "[") {
		var arr []int
		if err := json.Unmarshal([]byte(s), &arr); err == nil {
			return arr
		}
	}
	var out []int
	for _, tok := range strings.FieldsFunc(s, func(r rune) bool {
		return r < '0' || r > '9'
	}) {
		var n int
		if _, err := fmt.Sscanf(tok, "%d", &n); err == nil {
			out = append(out, n)
		}
	}
	return out
}
