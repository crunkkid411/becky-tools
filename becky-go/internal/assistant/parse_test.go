package assistant

import (
	"strings"
	"testing"
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
