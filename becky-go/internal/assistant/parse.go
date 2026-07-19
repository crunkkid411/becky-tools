package assistant

// parse.go turns a model's output into a validated []Action. It accepts BOTH
// wire formats the model may emit (R-AI §2.2):
//
//   - a JSON action list:  [{"verb":"add_clip","args":{"in":"00:13:12,640",...}}]
//   - the line DSL:        add_clip source="2026-06-14_ring.mp4" in=00:13:12,640 out=00:13:20,560 label="offers money"
//
// JSON is the canonical internal form; the DSL is the cheap transport. Either way
// the result is validated against the default-deny allowlist (schema.go): an
// unknown verb or a missing required arg never executes — it is returned as an
// Invalid entry so the preview can show "unknown" / "needs a value".

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"becky-go/internal/footage"
)

// requiredArgs lists the arg keys each verb must have to be runnable. A verb
// missing any of these is marked Invalid (shown, not executed). Optional args
// (label, mode, at, preset, …) are not listed. remove_clip / reorder need id OR
// index, which is checked specially in missingArgs.
var requiredArgs = map[Verb][]string{
	VerbSearch:      {"query"},
	VerbFindQuotes:  {"criteria"},
	VerbPreviewClip: {"source", "in", "out"},
	VerbGrabFrame:   {"source", "at"},
	VerbAddClip:     {"source", "in", "out"},
	VerbRemoveClip:  {},
	VerbReorder:     {"to"},
	VerbSetOverlay:  {"field", "value"},
	VerbSetMarker:   {"at"},
	VerbSetLabel:    {"text"},
	VerbExport:      {},
}

// Parse takes raw model output and returns the validated actions plus any invalid
// ones. It auto-detects JSON (leading '[' or '{') vs the line DSL. Surrounding
// markdown code fences (```json … ```) are stripped first, since models often
// wrap structured output. Never panics; malformed input yields zero actions.
func Parse(raw string) (actions []Action, invalid []Invalid) {
	s := stripFences(strings.TrimSpace(raw))
	if s == "" {
		return nil, nil
	}
	var parsed []Action
	if looksJSON(s) {
		parsed = parseJSON(s)
	} else {
		parsed = parseDSL(s)
	}
	return validate(parsed)
}

// ParseActions validates an already-structured action list (e.g. one a backend
// built directly). Same allowlist/required-arg rules as Parse.
func ParseActions(in []Action) (actions []Action, invalid []Invalid) {
	return validate(in)
}

// looksJSON reports whether s begins like a JSON array/object.
func looksJSON(s string) bool {
	return strings.HasPrefix(s, "[") || strings.HasPrefix(s, "{")
}

// stripFences removes a leading/trailing markdown code fence if present.
func stripFences(s string) string {
	if !strings.HasPrefix(s, "```") {
		return s
	}
	// Drop the first fence line (``` or ```json) and a trailing fence.
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[i+1:]
	}
	if j := strings.LastIndex(s, "```"); j >= 0 {
		s = s[:j]
	}
	return strings.TrimSpace(s)
}

// parseJSON decodes either a JSON array of actions or a single action object.
// Unrecognised shapes yield nil (degrade, never crash).
func parseJSON(s string) []Action {
	if strings.HasPrefix(s, "[") {
		var arr []Action
		if err := json.Unmarshal([]byte(s), &arr); err == nil {
			return arr
		}
		return nil
	}
	var one Action
	if err := json.Unmarshal([]byte(s), &one); err == nil && one.Verb != "" {
		return []Action{one}
	}
	return nil
}

// parseDSL parses the line-oriented DSL: one action per line, "verb k=v k=v",
// where a value may be bare, "double-quoted", or 'single-quoted'. Blank lines and
// lines starting with '#' are ignored.
func parseDSL(s string) []Action {
	var out []Action
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := splitDSLFields(line)
		if len(fields) == 0 {
			continue
		}
		act := Action{Verb: Verb(strings.ToLower(fields[0])), Args: map[string]any{}}
		for _, f := range fields[1:] {
			eq := strings.IndexByte(f, '=')
			if eq <= 0 {
				continue // a bare token with no key is ignored
			}
			key := strings.TrimSpace(f[:eq])
			val := unquote(strings.TrimSpace(f[eq+1:]))
			if key != "" {
				act.Args[key] = val
			}
		}
		out = append(out, act)
	}
	return out
}

// splitDSLFields splits a DSL line on whitespace, but keeps quoted substrings
// (single or double) intact so a value like label="offers money for cat" is one
// field.
func splitDSLFields(line string) []string {
	var fields []string
	var cur strings.Builder
	var quote rune
	inQuote := false
	flush := func() {
		if cur.Len() > 0 {
			fields = append(fields, cur.String())
			cur.Reset()
		}
	}
	for _, r := range line {
		switch {
		case inQuote:
			cur.WriteRune(r)
			if r == quote {
				inQuote = false
			}
		case r == '"' || r == '\'':
			inQuote = true
			quote = r
			cur.WriteRune(r)
		case r == ' ' || r == '\t':
			flush()
		default:
			cur.WriteRune(r)
		}
	}
	flush()
	return fields
}

// unquote strips a single matching pair of surrounding quotes.
func unquote(s string) string {
	if len(s) >= 2 {
		if (s[0] == '"' && s[len(s)-1] == '"') || (s[0] == '\'' && s[len(s)-1] == '\'') {
			return s[1 : len(s)-1]
		}
	}
	return s
}

// validate partitions actions into runnable + invalid, enforcing the allowlist
// and per-verb required args (the default-deny posture). The order of valid
// actions is preserved (handlers run in order on ✓).
func validate(in []Action) (valid []Action, invalid []Invalid) {
	for _, a := range in {
		if a.Args == nil {
			a.Args = map[string]any{}
		}
		if !IsAllowed(a.Verb) {
			invalid = append(invalid, Invalid{Action: a, Reason: "unknown verb (not in allowlist)"})
			continue
		}
		if missing := missingArgs(a); missing != "" {
			invalid = append(invalid, Invalid{Action: a, Reason: "missing required arg: " + missing})
			continue
		}
		valid = append(valid, a)
	}
	return valid, invalid
}

// missingArgs returns the name of the first missing/empty required arg for a, or
// "" if all are present. remove_clip / reorder are special-cased: they need id OR
// index.
func missingArgs(a Action) string {
	if a.Verb == VerbRemoveClip {
		if !hasArg(a, "id") && !hasArg(a, "index") {
			return "id or index"
		}
		return ""
	}
	if a.Verb == VerbReorder {
		if !hasArg(a, "id") && !hasArg(a, "index") {
			return "id or index"
		}
	}
	// add_clip by GUI-resolved hit selector ("add clip 3") carries `hit` instead
	// of source/in/out — the GUI fills those from the referenced search hit before
	// applying. Such an action is complete as a directive.
	if a.Verb == VerbAddClip && hasArg(a, "hit") {
		return ""
	}
	for _, key := range requiredArgs[a.Verb] {
		if !hasArg(a, key) {
			return key
		}
	}
	return ""
}

// hasArg reports whether a has a non-empty value for key. A value counts as
// present if it is a non-empty string or any non-nil non-string scalar.
func hasArg(a Action, key string) bool {
	v, ok := a.Args[key]
	if !ok || v == nil {
		return false
	}
	if s, isStr := v.(string); isStr {
		return strings.TrimSpace(s) != ""
	}
	return true
}

// resolveHitActions turns each Tier-0 "add clip N" placeholder (a VerbAddClip
// carrying only a "hit" arg — see classify.go's reAddClip/reAddClipAdj) into a
// real add_clip with source/in/out/label, by indexing into hits: the caller's
// most recent search results, in the same order the GUI showed them (1-based;
// "last" means the final result). Every other action passes through unchanged.
//
// An out-of-range index, an empty hits list, or a transcript-only orphan (no
// playable video) demotes the action to Invalid with a plain-language reason
// instead of silently doing nothing or shipping an add_clip with an empty
// source — "add clip 3" must either really add clip 3 or say why it can't.
func resolveHitActions(acts []Action, hits []footage.Candidate) (valid []Action, invalid []Invalid) {
	for _, a := range acts {
		if a.Verb != VerbAddClip || !hasArg(a, "hit") {
			valid = append(valid, a)
			continue
		}
		sel, _ := a.Args["hit"].(string)
		idx, ok := resolveHitIndex(sel, len(hits))
		if !ok {
			invalid = append(invalid, Invalid{Action: a, Reason: hitRangeReason(sel, len(hits))})
			continue
		}
		h := hits[idx]
		if h.Source == "" {
			invalid = append(invalid, Invalid{Action: a, Reason: "that result has no playable video (transcript-only)"})
			continue
		}
		valid = append(valid, Action{Verb: VerbAddClip, Args: map[string]any{
			"source": h.Source,
			"in":     secondsToTimecode(h.Timestamp),
			"out":    secondsToTimecode(h.End),
			"label":  truncate(h.Text, 60),
		}})
	}
	return valid, invalid
}

// resolveHitIndex turns "3" (1-based) or "last" into a 0-based index into a
// list of n hits. ok is false when n is 0 or the selector is out of range.
func resolveHitIndex(sel string, n int) (idx int, ok bool) {
	if n == 0 {
		return 0, false
	}
	if sel == "last" {
		return n - 1, true
	}
	i, err := strconv.Atoi(sel)
	if err != nil || i < 1 || i > n {
		return 0, false
	}
	return i - 1, true
}

// hitRangeReason renders the plain-language reason a hit selector didn't
// resolve — no results yet vs. an index past the end of a real result list.
func hitRangeReason(sel string, n int) string {
	if n == 0 {
		return `no search results yet -- run a search first, then "add clip ` + sel + `"`
	}
	return fmt.Sprintf(`only %d search result(s) -- "clip %s" is out of range`, n, sel)
}
