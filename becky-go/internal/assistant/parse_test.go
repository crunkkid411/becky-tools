package assistant

import (
	"strings"
	"testing"

	"becky-go/internal/footage"
)

// TestParseJSON covers the JSON action-list wire format + allowlist validation.
func TestParseJSON(t *testing.T) {
	raw := `[
	  {"verb":"add_clip","args":{"source":"a.mp4","in":"00:00:01,000","out":"00:00:03,000","label":"x"}},
	  {"verb":"set_overlay","args":{"index":1,"field":"date","value":"2026-06-14"}}
	]`
	acts, invalid := Parse(raw)
	if len(acts) != 2 || len(invalid) != 0 {
		t.Fatalf("Parse JSON = %d valid, %d invalid; want 2/0", len(acts), len(invalid))
	}
	if acts[0].Verb != VerbAddClip || acts[1].Verb != VerbSetOverlay {
		t.Fatalf("verbs = %v / %v", acts[0].Verb, acts[1].Verb)
	}
	// index arrived as a JSON number; argString must render it as "1".
	if argString(acts[1], "index") != "1" {
		t.Fatalf("index arg = %q, want \"1\"", argString(acts[1], "index"))
	}
}

// TestParseDSL covers the line DSL, including quoted values with spaces.
func TestParseDSL(t *testing.T) {
	raw := `add_clip source="2026-06-14_ring.mp4" in=00:13:12,640 out=00:13:20,560 label="offers money for cat"
set_overlay index=1 field=date value="2026-06-14"`
	acts, invalid := Parse(raw)
	if len(acts) != 2 || len(invalid) != 0 {
		t.Fatalf("Parse DSL = %d valid, %d invalid; want 2/0: %+v / %+v", len(acts), len(invalid), acts, invalid)
	}
	if got := argString(acts[0], "label"); got != "offers money for cat" {
		t.Fatalf("quoted label = %q, want the full phrase", got)
	}
	if got := argString(acts[0], "in"); got != "00:13:12,640" {
		t.Fatalf("in timecode = %q, want verbatim", got)
	}
}

// TestParseAllowlistRejection covers the default-deny posture: unknown verbs and
// missing-required-arg actions become Invalid, never Action.
func TestParseAllowlistRejection(t *testing.T) {
	tests := []struct {
		name        string
		raw         string
		wantValid   int
		wantInvalid int
		reasonHas   string
	}{
		{"unknown verb", `delete_everything target=all`, 0, 1, "unknown verb"},
		{"shell masquerade", `[{"verb":"shell","args":{"cmd":"rm -rf /"}}]`, 0, 1, "unknown verb"},
		{"add_clip missing out", `add_clip source=a.mp4 in=00:00:01,000`, 0, 1, "missing required arg: out"},
		{"remove needs id or index", `remove_clip`, 0, 1, "id or index"},
		{"remove with index ok", `remove_clip index=2`, 1, 0, ""},
		{"search ok", `search query=cat`, 1, 0, ""},
		{"export ok no args", `export`, 1, 0, ""},
		{"set_overlay missing value", `set_overlay index=1 field=date`, 0, 1, "missing required arg: value"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			acts, invalid := Parse(tt.raw)
			if len(acts) != tt.wantValid || len(invalid) != tt.wantInvalid {
				t.Fatalf("got %d valid / %d invalid; want %d / %d (%+v / %+v)",
					len(acts), len(invalid), tt.wantValid, tt.wantInvalid, acts, invalid)
			}
			if tt.reasonHas != "" {
				if len(invalid) == 0 || !strings.Contains(invalid[0].Reason, tt.reasonHas) {
					t.Fatalf("invalid reason = %v, want substring %q", invalid, tt.reasonHas)
				}
			}
		})
	}
}

// TestParseFences confirms a fenced JSON block (```json … ```) is unwrapped.
func TestParseFences(t *testing.T) {
	raw := "```json\n[{\"verb\":\"export\",\"args\":{}}]\n```"
	acts, invalid := Parse(raw)
	if len(acts) != 1 || acts[0].Verb != VerbExport || len(invalid) != 0 {
		t.Fatalf("fenced parse = %+v / %+v", acts, invalid)
	}
}

// TestParseEmpty confirms blank/garbage input yields no VALID actions (degrade,
// no panic).
func TestParseEmpty(t *testing.T) {
	for _, raw := range []string{"", "   ", "\n\n", "not an action at all????"} {
		acts, _ := Parse(raw)
		if len(acts) != 0 {
			t.Fatalf("Parse(%q) produced %d valid actions, want 0", raw, len(acts))
		}
	}
}

// hitFixture returns two search-hit candidates in "as displayed" order, the way
// App.Search stores them — the second is a transcript-only orphan (no source).
func hitFixture() []footage.Candidate {
	return []footage.Candidate{
		{Source: "ring.mp4", Name: "ring.mp4", Timestamp: 10, End: 13, Text: "I will pay you for the cat", Score: 2},
		{Source: "", Name: "orphan.mp4", Timestamp: 5, End: 8, Text: "an orphan quote", Score: 1},
	}
}

// TestResolveHitActionsByIndex covers "add clip N": a 1-based hit selector
// resolves to the real source/in/out/label of that search result.
func TestResolveHitActionsByIndex(t *testing.T) {
	acts := []Action{{Verb: VerbAddClip, Args: map[string]any{"hit": "1"}}}
	valid, invalid := resolveHitActions(acts, hitFixture())
	if len(invalid) != 0 {
		t.Fatalf("invalid = %+v, want none", invalid)
	}
	if len(valid) != 1 {
		t.Fatalf("valid = %+v, want 1", valid)
	}
	if got := argString(valid[0], "source"); got != "ring.mp4" {
		t.Fatalf("source = %q, want ring.mp4", got)
	}
	if hasArg(valid[0], "hit") {
		t.Fatal("resolved action must not still carry the raw hit selector")
	}
}

// TestResolveHitActionsLast covers "add the last clip".
func TestResolveHitActionsLast(t *testing.T) {
	acts := []Action{{Verb: VerbAddClip, Args: map[string]any{"hit": "last"}}}
	valid, invalid := resolveHitActions(acts, []footage.Candidate{
		{Source: "a.mp4", Timestamp: 1, End: 2, Text: "x"},
		{Source: "b.mp4", Timestamp: 3, End: 4, Text: "y"},
	})
	if len(invalid) != 0 || len(valid) != 1 {
		t.Fatalf("valid/invalid = %+v / %+v", valid, invalid)
	}
	if got := argString(valid[0], "source"); got != "b.mp4" {
		t.Fatalf("\"last\" resolved to %q, want b.mp4 (the final hit)", got)
	}
}

// TestResolveHitActionsNoSearchYet: with zero search hits, "add clip 1" degrades
// to Invalid with an honest "run a search first" reason — never a crash, never a
// silently-dropped add_clip with an empty source.
func TestResolveHitActionsNoSearchYet(t *testing.T) {
	acts := []Action{{Verb: VerbAddClip, Args: map[string]any{"hit": "1"}}}
	valid, invalid := resolveHitActions(acts, nil)
	if len(valid) != 0 {
		t.Fatalf("valid = %+v, want none (no hits to resolve against)", valid)
	}
	if len(invalid) != 1 || !strings.Contains(invalid[0].Reason, "search first") {
		t.Fatalf("invalid = %+v, want a \"run a search first\" reason", invalid)
	}
}

// TestResolveHitActionsOutOfRange: "clip 5" with only 2 real results degrades to
// Invalid naming the actual count, not a silent no-op or a crash.
func TestResolveHitActionsOutOfRange(t *testing.T) {
	acts := []Action{{Verb: VerbAddClip, Args: map[string]any{"hit": "5"}}}
	valid, invalid := resolveHitActions(acts, hitFixture())
	if len(valid) != 0 {
		t.Fatalf("valid = %+v, want none (index 5 is out of range for 2 hits)", valid)
	}
	if len(invalid) != 1 || !strings.Contains(invalid[0].Reason, "out of range") {
		t.Fatalf("invalid = %+v, want an \"out of range\" reason", invalid)
	}
}

// TestResolveHitActionsOrphanHasNoVideo: a hit selector pointing at a
// transcript-only orphan (no playable video) degrades to Invalid rather than
// producing an add_clip with an empty source.
func TestResolveHitActionsOrphanHasNoVideo(t *testing.T) {
	acts := []Action{{Verb: VerbAddClip, Args: map[string]any{"hit": "2"}}}
	valid, invalid := resolveHitActions(acts, hitFixture())
	if len(valid) != 0 {
		t.Fatalf("valid = %+v, want none (hit 2 is a transcript-only orphan)", valid)
	}
	if len(invalid) != 1 || !strings.Contains(invalid[0].Reason, "no playable video") {
		t.Fatalf("invalid = %+v, want a \"no playable video\" reason", invalid)
	}
}

// TestResolveHitActionsPassesThroughOthers: actions that aren't a hit-carrying
// add_clip (an already-complete add_clip, or an unrelated verb) pass through
// untouched.
func TestResolveHitActionsPassesThroughOthers(t *testing.T) {
	acts := []Action{
		{Verb: VerbExport, Args: map[string]any{}},
		{Verb: VerbAddClip, Args: map[string]any{"source": "z.mp4", "in": "00:00:01,000", "out": "00:00:02,000"}},
	}
	valid, invalid := resolveHitActions(acts, hitFixture())
	if len(invalid) != 0 || len(valid) != 2 {
		t.Fatalf("valid/invalid = %+v / %+v, want both actions passed through unchanged", valid, invalid)
	}
	if argString(valid[1], "source") != "z.mp4" {
		t.Fatal("an already-complete add_clip must not be touched")
	}
}
