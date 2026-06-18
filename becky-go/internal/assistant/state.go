package assistant

import "becky-go/internal/footage"

// state.go holds the per-turn state types the router and funnel need. The
// timeline is represented by a LOCAL lightweight struct (NOT internal/edl, which
// a parallel agent owns) — the brief's instruction. The GUI maps its real edl.Reel
// into this compact view for the assistant and applies approved actions back into
// the real model itself.

// Tier is the routing decision (R-AI §1.1). The router tries tiers cheap→high and
// degrades high→low on engine failure.
type Tier int

const (
	TierDeterministic Tier = iota // 0 — pure Go: command grammar + literal retrieval
	TierLocal                     // 1 — small local model (llama-server, internal/llmlocal)
	TierFrontier                  // 2 — frontier (claude CLI / Anthropic API); opt-in + logged
)

// String renders the tier for the UI badge / logs.
func (t Tier) String() string {
	switch t {
	case TierDeterministic:
		return "deterministic"
	case TierLocal:
		return "local"
	case TierFrontier:
		return "frontier"
	default:
		return "unknown"
	}
}

// ClipRef is one clip on the assistant's compact timeline view. It mirrors the
// fields of edl.Clip the assistant reasons about (id/source/in/out/label) without
// importing that package.
type ClipRef struct {
	ID     string  `json:"id"`
	Source string  `json:"source"` // absolute path to the source video
	In     float64 `json:"in"`     // seconds into source
	Out    float64 `json:"out"`    // seconds into source
	Label  string  `json:"label,omitempty"`
}

// TimelineState is the assistant's lightweight view of the current compilation:
// the ordered clips + which overlay fields are on. It is small (no media), so it
// goes into every model prompt unchanged.
type TimelineState struct {
	Clips   []ClipRef       `json:"clips"`
	Overlay map[string]bool `json:"overlay,omitempty"` // e.g. {"date":true,"timecode":true}
}

// Context is the per-turn state the funnel + prompts need (all cheap to
// assemble). FolderRoot is the search scope (originals read-only); Index is the
// filename+sidecar map (no media bytes); DB is the optional forensic.db for
// becky-search; Online gates the frontier tier (default false); Budget caps Tier-2
// spend this session.
type Context struct {
	FolderRoot string               `json:"folder_root"`
	Index      *footage.FolderIndex `json:"-"` // assembled once; not serialised into prompts wholesale
	DB         string               `json:"db,omitempty"`
	Timeline   TimelineState        `json:"timeline"`
	Online     bool                 `json:"online"`
	Budget     *Budget              `json:"-"`
}

// Budget is the per-session Tier-2 spend cap. A nil Budget means "no Tier-2
// budget" → frontier turns degrade to local. Exhausted() is the gate the router
// checks before spending a single token.
type Budget struct {
	MaxUSD   float64 // >0 enforces a dollar cap; <=0 means no dollar cap (turn cap may still apply)
	SpentUSD float64
	MaxTurns int // optional hard cap on frontier turns this session (0 = no cap)
	Turns    int
}

// Exhausted reports whether the frontier budget is spent. A nil Budget is
// considered exhausted (no budget configured → never escalate). With a Budget
// set, MaxUSD<=0 means "no dollar cap" (the turn cap may still apply).
func (b *Budget) Exhausted() bool {
	if b == nil {
		return true
	}
	if b.MaxUSD > 0 && b.SpentUSD >= b.MaxUSD {
		return true
	}
	if b.MaxTurns > 0 && b.Turns >= b.MaxTurns {
		return true
	}
	return false
}
