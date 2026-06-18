package assistant

import (
	"fmt"
	"sort"
	"strings"
)

// util.go holds the small pure helpers the router uses to assemble a Proposal:
// arg extraction, a one-sentence summary, the mutate check, and text truncation.
// Kept separate so router.go reads as the routing logic, not string plumbing.

// argString returns the value of args[key] as a string ("" if absent). Numbers
// from JSON (float64) are formatted without a trailing ".0" when integral, so an
// index=2 arriving as JSON renders as "2", matching the DSL form.
func argString(a Action, key string) string {
	v, ok := a.Args[key]
	if !ok || v == nil {
		return ""
	}
	switch t := v.(type) {
	case string:
		return t
	case float64:
		if t == float64(int64(t)) {
			return fmt.Sprintf("%d", int64(t))
		}
		return fmt.Sprintf("%g", t)
	case int:
		return fmt.Sprintf("%d", t)
	case bool:
		return fmt.Sprintf("%t", t)
	default:
		return fmt.Sprintf("%v", t)
	}
}

// argsSummary renders an action's args as a compact "k=v k=v" string with keys
// sorted, for diffs/logs. Used as the "fixed" value in the corrections log.
func argsSummary(a Action) string {
	keys := make([]string, 0, len(a.Args))
	for k := range a.Args {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, k+"="+argString(a, k))
	}
	return strings.Join(parts, " ")
}

// anyMutating reports whether any action in the list changes the timeline (and so
// the whole proposal must go through ✓/✗).
func anyMutating(acts []Action) bool {
	for _, a := range acts {
		if IsMutating(a.Verb) {
			return true
		}
	}
	return false
}

// summarize produces the one-sentence PreviewText for a set of actions (R-AI
// §2.3: "Add 1 clip and date-label it."). It counts each verb and renders a short
// natural phrase; an empty list yields a neutral message.
func summarize(acts []Action) string {
	if len(acts) == 0 {
		return "Nothing to do."
	}
	counts := map[Verb]int{}
	order := []Verb{}
	for _, a := range acts {
		if counts[a.Verb] == 0 {
			order = append(order, a.Verb)
		}
		counts[a.Verb]++
	}
	parts := make([]string, 0, len(order))
	for _, v := range order {
		parts = append(parts, phraseFor(v, counts[v]))
	}
	return capitalize(joinPhrases(parts)) + "."
}

// phraseFor renders one verb+count as a human phrase, e.g. (add_clip,2) → "add 2
// clips".
func phraseFor(v Verb, n int) string {
	plural := func(singular string) string {
		if n == 1 {
			return fmt.Sprintf("%d %s", n, singular)
		}
		return fmt.Sprintf("%d %ss", n, singular)
	}
	switch v {
	case VerbSearch:
		return "search the transcripts"
	case VerbFindQuotes:
		return "find quotes"
	case VerbPreviewClip:
		return "preview a clip"
	case VerbGrabFrame:
		return "grab a frame"
	case VerbAddClip:
		return "add " + plural("clip")
	case VerbRemoveClip:
		return "remove " + plural("clip")
	case VerbReorder:
		return "reorder " + plural("clip")
	case VerbSetOverlay:
		return "set " + plural("overlay field")
	case VerbSetMarker:
		return "drop " + plural("marker")
	case VerbSetLabel:
		return "label " + plural("clip")
	case VerbExport:
		return "export the compilation"
	default:
		return string(v)
	}
}

// joinPhrases joins phrases with commas and a trailing "and".
func joinPhrases(parts []string) string {
	switch len(parts) {
	case 0:
		return ""
	case 1:
		return parts[0]
	case 2:
		return parts[0] + " and " + parts[1]
	default:
		return strings.Join(parts[:len(parts)-1], ", ") + ", and " + parts[len(parts)-1]
	}
}

func capitalize(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// truncate shortens s to max runes, appending an ellipsis when cut. Used to keep
// clip labels readable.
func truncate(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	if max <= 1 {
		return string(r[:max])
	}
	return string(r[:max-1]) + "…"
}
