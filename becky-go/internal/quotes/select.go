// select.go — the Selector seam (SPEC §4, SPEC-BECKY-CLIP §7) and the two
// model-free selectors.
//
// A Selector turns (transcript, criteria) into a set of Anchors — the passages
// that matter. Everything downstream (expansion, snapping, merge, emit) is
// deterministic and identical regardless of which Selector chose the anchors.
// This is THE integration point with becky-clip's assistant: the GUI's frontier
// AI produces anchors as JSON and feeds them via --select-from-json, so the tool
// stays offline/deterministic while a stronger model does the hard selection.
//
// Three selectors ship:
//   - ExactSelector   — literal OR-separated phrase match, no model (SPEC §4.6).
//   - JSONSelector    — load anchors from --select-from-json (SPEC §4.3); the
//     MAIN GUI path.
//   - LocalSelector   — a local llama-server semantic selection (model.go).
package quotes

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// Anchor is one selected passage. Quote is the verbatim words of the passage;
// Cue is an optional direct cue index (>=0) if the selector addressed a cue
// rather than a string; Because is a one-line rationale for the audit log/JSON.
// At least one of Quote or Cue (>=0) must be usable to resolve a location.
type Anchor struct {
	Quote   string // verbatim spoken words to snap against the transcript
	Cue     int    // direct cue index (0-based); <0 means "use Quote instead"
	Because string // one-line rationale (selected_because in the JSON summary)

	// hint is an optional source timecode ("HH:MM:SS") used ONLY to disambiguate
	// a recurring opening phrase when resolving Quote. It never sets a region
	// boundary. Unexported: it is an internal resolution aid, not part of the
	// public summary shape.
	hint string
}

// Selector chooses the important passages. transcript is the full cleaned text
// (joined cue text); criteria is the selection objective (--criteria). Returns
// anchors in any order; the engine de-dups + sorts after snapping.
//
// Small interface, defined where it's used — becky-clip's assistant implements
// the frontier tier by writing JSON that JSONSelector reads, so it never needs to
// import this package to participate.
type Selector interface {
	Select(ctx context.Context, transcript, criteria string) ([]Anchor, error)
}

// Expander is the optional model judgment for recursive context expansion
// (SPEC §5): does adding this neighbor sentence give necessary context to
// understand the block? Selectors that wrap a model also implement this; the
// model-free selectors do not, so expansion is OFF for them unless an Expander is
// supplied separately (SPEC §5: "For Exact/JSON modes, expansion is off unless a
// model is available").
type Expander interface {
	// NeedsContext reports whether neighbor adds necessary context to block.
	// Implementations MUST be deterministic at temperature 0 and degrade to
	// (false, error) rather than panic when the model is unavailable.
	NeedsContext(ctx context.Context, block, neighbor string) (bool, error)
}

// ---- ExactSelector -------------------------------------------------------

// ExactSelector matches literal phrases (the only non-LLM selection path,
// opt-in via --exact). Phrases is the OR-separated list; an anchor is produced
// for every phrase that occurs verbatim (normalized) in the transcript. No model
// is ever consulted.
type ExactSelector struct {
	Phrases []string
	index   *cueIndex
}

// NewExactSelector builds an ExactSelector over the given cues from a raw
// "<phrase>|<phrase>" string (SPEC §4.6).
func NewExactSelector(cues []Cue, orExpr string) *ExactSelector {
	return &ExactSelector{Phrases: splitPhrases(orExpr), index: buildIndex(cues)}
}

// Select returns one anchor per phrase found verbatim in the transcript. The
// transcript/criteria args are ignored (matching is over the cue index captured
// at construction); they exist to satisfy the Selector contract.
func (s *ExactSelector) Select(_ context.Context, _ string, _ string) ([]Anchor, error) {
	var anchors []Anchor
	for _, p := range s.Phrases {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if _, _, ok := s.index.locate(p, 0, -1); ok {
			anchors = append(anchors, Anchor{Quote: p, Cue: -1, Because: "exact phrase: " + p})
		}
	}
	return anchors, nil
}

// splitPhrases splits an OR-separated phrase expression on "|", trimming blanks.
func splitPhrases(expr string) []string {
	var out []string
	for _, p := range strings.Split(expr, "|") {
		if t := strings.TrimSpace(p); t != "" {
			out = append(out, t)
		}
	}
	return out
}

// ---- JSONSelector --------------------------------------------------------

// jsonAnchorFile is the --select-from-json schema (SPEC §4.3):
//
//	{"anchors":[{"quote":"<verbatim words>","hint":"00:13:14","because":"..."},
//	            {"cue":412,"because":"..."}]}
//
// Either "quote" or "cue" identifies the passage; "hint" (a timecode) and
// "because" are optional. Unknown fields are ignored.
type jsonAnchorFile struct {
	Anchors []jsonAnchor `json:"anchors"`
}

type jsonAnchor struct {
	Quote   string `json:"quote"`
	Cue     *int   `json:"cue"`
	Hint    string `json:"hint"`
	Because string `json:"because"`
}

// JSONSelector loads anchors a stronger external model already chose. This is the
// main becky-clip GUI path: the assistant's frontier tier writes this file, the
// tool only expands + snaps + emits. Fully offline.
type JSONSelector struct {
	anchors []Anchor
}

// NewJSONSelectorFromFile reads and validates a --select-from-json file. A
// missing/invalid file is a clear error (degrade-never-crash) — the CLI turns it
// into a stderr note + nonzero exit. An empty anchors list is allowed (yields no
// regions) but reported by the caller.
func NewJSONSelectorFromFile(path string) (*JSONSelector, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read --select-from-json %s: %w", path, err)
	}
	return NewJSONSelector(data)
}

// NewJSONSelector parses anchors from raw JSON bytes (split out for testing).
func NewJSONSelector(data []byte) (*JSONSelector, error) {
	var f jsonAnchorFile
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("parse anchors JSON: %w", err)
	}
	anchors := make([]Anchor, 0, len(f.Anchors))
	for _, a := range f.Anchors {
		cue := -1
		if a.Cue != nil {
			cue = *a.Cue
		}
		quote := strings.TrimSpace(a.Quote)
		if quote == "" && cue < 0 {
			continue // an anchor with neither quote nor cue is unusable
		}
		anchors = append(anchors, Anchor{Quote: quote, Cue: cue, Because: a.Because, hint: a.Hint})
	}
	return &JSONSelector{anchors: anchors}, nil
}

// Select returns the pre-loaded anchors (transcript/criteria ignored).
func (s *JSONSelector) Select(_ context.Context, _ string, _ string) ([]Anchor, error) {
	return s.anchors, nil
}
