package assistant

// classify.go is the deterministic tier router (R-AI §1.2). classifyTier decides
// the STARTING tier from the utterance alone — with NO model call (deciding the
// tier must never itself cost a token). Order matters: cheapest match wins.
//
//	(a) Tier 0 — explicit command grammar parses the whole utterance → run it now.
//	(b) Tier 0 — literal retrieval ("find <literal>") with no semantic cue.
//	(c) Tier 2 — semantic retrieval ("every time…", paraphrase) OR a multi-step plan.
//	(d) Tier 1 — everything else needing a little intelligence (fuzzy single action).
//
// The predicates are deliberately CONSERVATIVE: when unsure between Tier 1 and
// Tier 2, start at Tier 1 (cheap) and let the fallback chain escalate only if
// Tier 1 can't produce a confident result — this keeps the Max plan from being
// burned on turns a local model could handle.

import (
	"regexp"
	"strconv"
	"strings"
)

// Decision is what classifyTier returns: the tier to TRY first, the parsed
// deterministic actions (non-nil iff Tier 0 fully parsed it), a human reason, and
// the Escalate flag for semantic/plan turns.
type Decision struct {
	Tier     Tier
	Actions  []Action
	Reason   string
	Escalate bool
}

// semanticCues are phrases a 4B model selects poorly over hours of transcript —
// they signal paraphrase/coref/implicature retrieval that needs the frontier tier.
var semanticCues = []string{
	"every time", "everytime", "whenever", "any time", "anytime",
	"any point", "each time", "all the times", "all the time",
	"mentions", "mention", "talks about", "talk about", "brings up",
	"offers", "offer", "implies", "imply", "suggests", "suggest",
	"hints", "alludes", "threatens", "threat", "promises", "about the",
	"references", "refers to", "anything about", "anywhere",
}

// multiStepCues join two actions into one plan ("find … and add … and label …").
var multiStepCues = []string{" and then ", " then ", " and add ", " and label ", " and put ", " and date "}

// hasSemanticCue reports whether u contains a semantic-retrieval phrase.
func hasSemanticCue(u string) bool {
	for _, c := range semanticCues {
		if strings.Contains(u, c) {
			return true
		}
	}
	return false
}

// isMultiStep reports whether u is a conjoined multi-action plan. It fires on an
// explicit chaining phrase OR on ≥2 distinct action-verb keywords appearing.
func isMultiStep(u string) bool {
	for _, c := range multiStepCues {
		if strings.Contains(u, c) {
			return true
		}
	}
	return countActionVerbs(u) >= 2
}

// actionVerbWords maps loose natural-language verb keywords to a canonical bucket,
// used only to COUNT distinct intents for the multi-step predicate.
var actionVerbWords = map[string]string{
	"find": "find", "search": "find", "show": "find", "locate": "find",
	"add": "add", "append": "add", "chuck": "add", "put": "add", "insert": "add",
	"remove": "remove", "delete": "remove", "drop": "remove", "cut": "remove",
	"reorder": "reorder", "move": "reorder", "swap": "reorder",
	"label": "label", "rename": "label", "name": "label",
	"date": "overlay", "overlay": "overlay", "caption": "overlay",
	"marker": "marker", "mark": "marker",
	"export": "export", "render": "export", "compile": "export",
	"grab": "frame", "frame": "frame", "screenshot": "frame", "still": "frame",
}

// countActionVerbs counts distinct canonical action buckets named in u.
func countActionVerbs(u string) int {
	seen := map[string]bool{}
	for _, w := range strings.Fields(u) {
		w = strings.Trim(w, ".,!?;:'\"")
		if b, ok := actionVerbWords[w]; ok {
			seen[b] = true
		}
	}
	return len(seen)
}

// classifyTier is the pure routing decision (no model call). cx is unused today
// but kept in the signature (R-AI §1.2) so context-aware refinements (e.g. "the
// last clip" needs a non-empty timeline) can be added without churn.
func classifyTier(utt string, cx Context) Decision {
	u := strings.ToLower(strings.TrimSpace(utt))
	if u == "" {
		return Decision{Tier: TierDeterministic, Reason: "empty utterance"}
	}

	// (a) TIER 0 — explicit command grammar.
	if acts, ok := parseCommandGrammar(u, cx); ok {
		return Decision{Tier: TierDeterministic, Actions: acts, Reason: "matched command grammar"}
	}

	// (b) TIER 0 — literal retrieval with no semantic cue.
	if q, ok := parseLiteralSearch(u); ok && !hasSemanticCue(u) {
		return Decision{
			Tier:    TierDeterministic,
			Actions: []Action{{Verb: VerbSearch, Args: map[string]any{"query": q, "mode": "keyword"}}},
			Reason:  "literal retrieval",
		}
	}

	// (c) TIER 2 — semantic retrieval / multi-step plan.
	if hasSemanticCue(u) || isMultiStep(u) {
		return Decision{Tier: TierFrontier, Escalate: true, Reason: "semantic retrieval / multi-step plan"}
	}

	// (d) TIER 1 — fuzzy single-action NL the grammar missed.
	return Decision{Tier: TierLocal, Reason: "fuzzy single-action NL"}
}

// --- Tier-0 command grammar -------------------------------------------------

var (
	// "add clip 3" / "add quote 2" → add_clip by hit selector (the GUI resolves the
	// hit's source/in/out). We emit an add_clip carrying the requested hit.
	reAddClip = regexp.MustCompile(`^add\s+(?:the\s+)?(?:clip|quote|result|hit)\s+(\d+|last)\b`)
	// "add the last clip" / "add the first quote" — adjective-then-noun ordering.
	reAddClipAdj = regexp.MustCompile(`^add\s+the\s+(last|first)\s+(?:clip|quote|result|hit)\b`)
	// "remove clip 2" / "delete clip 2".
	reRemoveClip = regexp.MustCompile(`^(?:remove|delete|drop)\s+(?:the\s+)?(?:clip|quote)\s+(\d+|last)\b`)
	// "remove the last clip" / "delete the first clip".
	reRemoveClipAdj = regexp.MustCompile(`^(?:remove|delete|drop)\s+the\s+(last|first)\s+(?:clip|quote)\b`)
	// "jump to 12:40" / "go to 00:13:12" / "seek to 90" → preview_clip at a point.
	reJump = regexp.MustCompile(`^(?:jump|go|seek|skip)\s+to\s+([0-9:.,]+)\b`)
	// "export" / "export the compilation" / "render it".
	reExport = regexp.MustCompile(`^(?:export|render|compile)\b`)
	// "marker at 12:40" / "set a marker at 00:01:00 [label foo]".
	reMarker = regexp.MustCompile(`^(?:set\s+)?(?:a\s+)?marker\s+at\s+([0-9:.,]+)`)
	// "label clip 2 <text>" / "name clip 1 <text>".
	reLabel = regexp.MustCompile(`^(?:label|name|rename)\s+(?:clip|quote)\s+(\d+|last)\s+(.+)$`)
)

// parseCommandGrammar tries to map the whole utterance to a complete action list
// with zero ambiguity. Returns (actions, true) only on a full, confident parse.
func parseCommandGrammar(u string, cx Context) ([]Action, bool) {
	u = strings.TrimSpace(u)

	if m := reAddClip.FindStringSubmatch(u); m != nil {
		return []Action{{Verb: VerbAddClip, Args: map[string]any{"hit": m[1]}}}, true
	}
	if m := reAddClipAdj.FindStringSubmatch(u); m != nil {
		return []Action{{Verb: VerbAddClip, Args: map[string]any{"hit": m[1]}}}, true
	}
	if m := reRemoveClip.FindStringSubmatch(u); m != nil {
		return []Action{{Verb: VerbRemoveClip, Args: map[string]any{"index": m[1]}}}, true
	}
	if m := reRemoveClipAdj.FindStringSubmatch(u); m != nil {
		return []Action{{Verb: VerbRemoveClip, Args: map[string]any{"index": m[1]}}}, true
	}
	if m := reJump.FindStringSubmatch(u); m != nil {
		return []Action{{Verb: VerbPreviewClip, Args: map[string]any{"at": m[1]}}}, true
	}
	if m := reMarker.FindStringSubmatch(u); m != nil {
		args := map[string]any{"at": m[1]}
		if lbl := markerLabel(u); lbl != "" {
			args["label"] = lbl
		}
		return []Action{{Verb: VerbSetMarker, Args: args}}, true
	}
	if m := reLabel.FindStringSubmatch(u); m != nil {
		return []Action{{Verb: VerbSetLabel, Args: map[string]any{"index": m[1], "text": strings.TrimSpace(m[2])}}}, true
	}
	if reExport.MatchString(u) {
		return []Action{{Verb: VerbExport, Args: map[string]any{}}}, true
	}
	return nil, false
}

// markerLabel extracts a trailing "label <text>" / "called <text>" from a marker
// command, or "".
func markerLabel(u string) string {
	for _, kw := range []string{" label ", " called ", " named "} {
		if i := strings.Index(u, kw); i >= 0 {
			return strings.TrimSpace(u[i+len(kw):])
		}
	}
	return ""
}

// --- Tier-0 literal search --------------------------------------------------

// literalLeadRE matches the literal-search lead-in ("find the word 'cat'",
// "search for penguin", "show me 'pay you'").
var literalLeadRE = regexp.MustCompile(`^(?:find|search|show\s+me|show|look\s+for|grep)\s+(?:for\s+)?(?:the\s+)?(?:word\s+|phrase\s+|term\s+)?(.+)$`)

// parseLiteralSearch extracts a literal query from a search-style utterance. It
// returns (query, true) only when the remainder is a concrete literal (quoted, or
// a short bare phrase) — NOT when it carries a semantic cue (the caller also
// checks hasSemanticCue, but quoted literals are accepted regardless).
func parseLiteralSearch(u string) (string, bool) {
	m := literalLeadRE.FindStringSubmatch(u)
	if m == nil {
		return "", false
	}
	rest := strings.TrimSpace(m[1])
	if rest == "" {
		return "", false
	}
	// A quoted literal is unambiguous — accept it as the exact query.
	if q := quotedInner(rest); q != "" {
		return q, true
	}
	// A short bare phrase (≤ 4 words) with no semantic cue is a literal search.
	if !hasSemanticCue(rest) && len(strings.Fields(rest)) <= 4 {
		return strings.Trim(rest, ".,!?"), true
	}
	return "", false
}

// quotedInner returns the inside of a leading quoted span, or "".
func quotedInner(s string) string {
	if len(s) < 2 {
		return ""
	}
	q := s[0]
	if q != '"' && q != '\'' {
		return ""
	}
	if j := strings.IndexByte(s[1:], q); j >= 0 {
		return s[1 : 1+j]
	}
	return ""
}

// secondsFromTimecode parses "HH:MM:SS", "MM:SS", "SS", or "SS.mmm" into seconds.
// Used by handlers that turn a grammar "at" into a numeric preview point. (Kept
// here next to the grammar that produces those strings.)
func secondsFromTimecode(tc string) (float64, bool) {
	tc = strings.TrimSpace(strings.ReplaceAll(tc, ",", "."))
	if tc == "" {
		return 0, false
	}
	parts := strings.Split(tc, ":")
	var total float64
	for _, p := range parts {
		f, err := strconv.ParseFloat(p, 64)
		if err != nil {
			return 0, false
		}
		total = total*60 + f
	}
	return total, true
}
