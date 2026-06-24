// Package voicerules is becky-voice's SAFETY CORE: the deterministic leash on an
// always-on assistant. It loads one human-editable becky-voice.rules.json and enforces,
// in Go that the model cannot override (SPEC-BECKY-VOICE.md §4 / §8 cloud-item 2):
//   - the GREEN/YELLOW/RED action-tier gate (proactive on reads, ask on writes; default
//     RED for unknown);
//   - the context-STAGING gate (§4.7): captured context is assembled into a StagedSet
//     that is returned for display and is NEVER auto-sent — Send requires an explicit
//     confirm token (the Highlight-bug regression);
//   - the proactive watcher budget + quiet-hours (§4.4);
//   - the cloud-vs-local policy (§4.3): sensitive context forces local-only.
//
// Pure Go, fully unit-testable, value-asserted. The model PROPOSES; this layer DISPOSES.
package voicerules

import (
	"encoding/json"
	"fmt"
	"strings"

	"becky-go/internal/catalog"
)

// Rules is the parsed becky-voice.rules.json — every safety dial Highlight wouldn't let
// Jordan change lives here, not hard-coded.
type Rules struct {
	// AllowProactiveTiers lists the tiers the watcher may run on its own. Default (when
	// the field is absent) is GREEN-only — the safe posture from §4.4.
	AllowProactiveTiers []catalog.Tier `json:"allow_proactive_tiers,omitempty"`
	// TierOverrides lets Jordan re-tier a specific tool (e.g. mark a tool RED).
	TierOverrides map[string]catalog.Tier `json:"tier_overrides,omitempty"`
	// MaxProposalsPerMinute caps the proactive watcher so it can never nag. 0 => the
	// default cap (defaultMaxProposalsPerMinute).
	MaxProposalsPerMinute int `json:"max_proposals_per_minute,omitempty"`
	// QuietHours is a list of [startHour,endHour) windows (24h, local) in which the
	// watcher must not surface any proposal. A window may wrap midnight (start>end).
	QuietHours []QuietWindow `json:"quiet_hours,omitempty"`
	// SensitiveContexts are substrings (case-insensitive) that mark a context as
	// sensitive (e.g. "case", "forensic", "evidence"); a sensitive context forces the
	// realtime layer local-only — it never goes to the cloud model (§4.3).
	SensitiveContexts []string `json:"sensitive_contexts,omitempty"`
	// CloudAllowed is the global switch for using the cloud realtime model at all. When
	// false, everything is local regardless of context.
	CloudAllowed bool `json:"cloud_allowed"`
}

// QuietWindow is a [Start,End) hour range (0..23). When Start>End it wraps midnight.
type QuietWindow struct {
	Start int `json:"start"`
	End   int `json:"end"`
}

const (
	defaultMaxProposalsPerMinute = 2
)

// Default returns the safe default rules used when no file is present: cloud allowed,
// proactive GREEN-only, the default proposal cap, no quiet hours, the canonical
// sensitive markers.
func Default() Rules {
	return Rules{
		AllowProactiveTiers:   []catalog.Tier{catalog.TierGreen},
		MaxProposalsPerMinute: defaultMaxProposalsPerMinute,
		SensitiveContexts:     []string{"case", "forensic", "evidence", "private"},
		CloudAllowed:          true,
	}
}

// Load parses + validates a rules file's bytes, filling safe defaults for absent fields.
func Load(b []byte) (Rules, error) {
	r := Default()
	if len(strings.TrimSpace(string(b))) > 0 {
		if err := json.Unmarshal(b, &r); err != nil {
			return Rules{}, fmt.Errorf("parse becky-voice.rules.json: %w", err)
		}
	}
	if err := r.normalizeAndValidate(); err != nil {
		return Rules{}, err
	}
	return r, nil
}

func (r *Rules) normalizeAndValidate() error {
	if len(r.AllowProactiveTiers) == 0 {
		r.AllowProactiveTiers = []catalog.Tier{catalog.TierGreen}
	}
	for _, tr := range r.AllowProactiveTiers {
		switch tr {
		case catalog.TierGreen, catalog.TierYellow, catalog.TierRed:
		default:
			return fmt.Errorf("invalid proactive tier %q", tr)
		}
	}
	if r.MaxProposalsPerMinute <= 0 {
		r.MaxProposalsPerMinute = defaultMaxProposalsPerMinute
	}
	for _, q := range r.QuietHours {
		if q.Start < 0 || q.Start > 23 || q.End < 0 || q.End > 23 {
			return fmt.Errorf("quiet-hours window out of range: %+v", q)
		}
	}
	if len(r.SensitiveContexts) == 0 {
		r.SensitiveContexts = Default().SensitiveContexts
	}
	return nil
}

// TierFor returns the effective tier of a tool: a rules override wins, else the
// catalog's tier, else RED for an unknown tool.
func (r Rules) TierFor(tool string) catalog.Tier {
	if t, ok := r.TierOverrides[tool]; ok {
		switch t {
		case catalog.TierGreen, catalog.TierYellow, catalog.TierRed:
			return t
		}
	}
	return catalog.TierOf(tool)
}

// Decision is the outcome of gating a tool action.
type Decision struct {
	Allowed     bool
	NeedConfirm bool // true for YELLOW (confirm once) and for any RED done non-proactively
	Tier        catalog.Tier
	Reason      string
}

// GateAction decides whether a tool may run. proactive=true means the watcher (not a
// direct human ask) is trying to run it.
//   - GREEN: always allowed; proactively allowed only if GREEN is in AllowProactiveTiers
//     (it is, by default).
//   - YELLOW: allowed with confirm-once when asked directly; proactively allowed only if
//     YELLOW is explicitly opted into the proactive tiers.
//   - RED: NEVER allowed proactively; when asked directly it is allowed only with an
//     explicit confirmation (NeedConfirm).
func (r Rules) GateAction(tool string, proactive bool) Decision {
	tier := r.TierFor(tool)
	d := Decision{Tier: tier}
	proactiveOK := false
	for _, t := range r.AllowProactiveTiers {
		if t == tier {
			proactiveOK = true
		}
	}
	switch tier {
	case catalog.TierGreen:
		if proactive && !proactiveOK {
			d.Reason = "green tool not in allowed proactive tiers"
			return d
		}
		d.Allowed = true
		d.Reason = "green: auto"
	case catalog.TierYellow:
		if proactive && !proactiveOK {
			d.Reason = "yellow refused proactively (confirm-once tools are not proactive by default)"
			return d
		}
		d.Allowed = true
		d.NeedConfirm = true
		d.Reason = "yellow: confirm once"
	case catalog.TierRed:
		if proactive {
			d.Reason = "red tool refused proactively (never proactive)"
			return d
		}
		d.Allowed = true
		d.NeedConfirm = true
		d.Reason = "red: explicit confirmation required"
	}
	return d
}

// IsSensitive reports whether a context label/description is sensitive (forces local).
func (r Rules) IsSensitive(context string) bool {
	c := strings.ToLower(context)
	for _, s := range r.SensitiveContexts {
		if s = strings.ToLower(strings.TrimSpace(s)); s != "" && strings.Contains(c, s) {
			return true
		}
	}
	return false
}

// Realtime is the chosen realtime backend for a turn.
type Realtime string

const (
	RealtimeCloud Realtime = "cloud"
	RealtimeLocal Realtime = "local"
)

// RealtimeFor picks cloud vs local for a context: sensitive context ALWAYS forces local;
// otherwise cloud is used only when globally allowed (§4.3).
func (r Rules) RealtimeFor(context string) Realtime {
	if r.IsSensitive(context) {
		return RealtimeLocal
	}
	if r.CloudAllowed {
		return RealtimeCloud
	}
	return RealtimeLocal
}

// InQuietHours reports whether the given 24h hour falls inside any quiet window
// (wrapping windows supported).
func (r Rules) InQuietHours(hour int) bool {
	for _, q := range r.QuietHours {
		if q.Start == q.End {
			continue
		}
		if q.Start < q.End {
			if hour >= q.Start && hour < q.End {
				return true
			}
		} else { // wraps midnight, e.g. 22..6
			if hour >= q.Start || hour < q.End {
				return true
			}
		}
	}
	return false
}

// Budget tracks the proactive proposal rate within the current minute. It is the hard
// cap that keeps the watcher from nagging.
type Budget struct {
	max  int
	used int
}

// NewBudget builds a per-minute budget from the rules.
func (r Rules) NewBudget() *Budget { return &Budget{max: r.MaxProposalsPerMinute} }

// AllowProposal reports whether one more proposal fits in the budget this minute, and
// records it when it does. It must be called per proposed surfacing.
func (b *Budget) AllowProposal() bool {
	if b.used >= b.max {
		return false
	}
	b.used++
	return true
}

// Reset clears the budget at a minute boundary.
func (b *Budget) Reset() { b.used = 0 }

// --- the context-staging gate (the Highlight-bug fix) ---

// StagedSet is captured context (a screenshot, a tab, a window) assembled for display.
// It is NEVER auto-sent: Sent stays false until Send is called with the exact confirm
// token returned by Stage. This is the deterministic guarantee that nothing leaves the
// machine without Jordan's explicit ok (§4.7).
type StagedSet struct {
	Items   []string // descriptions of the staged context (e.g. "screenshot", "tab:foo")
	Sent    bool
	confirm string // the one-time token that must be presented to Send
}

// Stage assembles context into a StagedSet that is shown but NOT sent. It returns the
// set plus the confirm token the caller must echo back to Send. The token is derived
// deterministically from the items (no randomness, so tests are reproducible).
func Stage(items ...string) (*StagedSet, string) {
	token := confirmToken(items)
	return &StagedSet{Items: append([]string(nil), items...), confirm: token}, token
}

// Strip removes a staged item by index (Jordan can strip context before sending, §4.7).
func (s *StagedSet) Strip(i int) {
	if i < 0 || i >= len(s.Items) {
		return
	}
	s.Items = append(s.Items[:i], s.Items[i+1:]...)
	// stripping invalidates the previous token so a stale confirm can't send the
	// now-different set.
	s.confirm = confirmToken(s.Items)
}

// ConfirmToken returns the token currently required to Send this set (updates after a
// Strip). Exposed so a UI can show/re-fetch it; Send still requires the exact value.
func (s *StagedSet) ConfirmToken() string { return s.confirm }

// Send marks the set as sent ONLY if the provided token matches the current confirm
// token exactly. A wrong/empty token never sends — the staged context stays un-sent.
// Returns whether it was sent.
func (s *StagedSet) Send(token string) bool {
	if token == "" || token != s.confirm {
		return false
	}
	s.Sent = true
	return true
}

// confirmToken derives a stable token from the staged items so a confirm is tied to the
// exact set being approved (deterministic; not a security primitive, an intent latch).
func confirmToken(items []string) string {
	var h uint64 = 1469598103934665603 // FNV-1a 64 offset basis
	add := func(s string) {
		for i := 0; i < len(s); i++ {
			h ^= uint64(s[i])
			h *= 1099511628211
		}
	}
	for _, it := range items {
		add(it)
		add("\x00")
	}
	return fmt.Sprintf("confirm-%016x", h)
}
