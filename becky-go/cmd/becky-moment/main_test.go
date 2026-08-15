package main

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

// --- The becky-hits SEAM ---------------------------------------------------
//
// becky-moment's whole value is that its output flows into becky-hits without a
// human in between. That makes the field names a CONTRACT between two binaries,
// and contracts between becky binaries are exactly what has broken before:
// becky-resolve passed `becky-validate --variant`, a flag that does not exist, so
// the Gemma ladder silently never fired — and every unit test stayed green
// because each tool was tested alone (STATE-OF-MASTER.md; HANDOFF-SHORTS-PIPELINE.md §2).
//
// So this test does not mock becky-hits. It PARSES cmd/becky-hits/main.go and
// asserts against the struct that tool actually reads. Rename a field there and
// this fails here, which is the entire point.

// hitsReaderKeys extracts the json tag names from cmd/becky-hits' `hit` struct.
func hitsReaderKeys(t *testing.T) map[string]bool {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "../becky-hits/main.go", nil, 0)
	if err != nil {
		t.Fatalf("parsing cmd/becky-hits/main.go: %v", err)
	}

	keys := map[string]bool{}
	ast.Inspect(f, func(n ast.Node) bool {
		ts, ok := n.(*ast.TypeSpec)
		if !ok || ts.Name.Name != "hit" {
			return true
		}
		st, ok := ts.Type.(*ast.StructType)
		if !ok {
			return true
		}
		for _, field := range st.Fields.List {
			if field.Tag == nil {
				continue
			}
			tag, err := strconv.Unquote(field.Tag.Value)
			if err != nil {
				continue
			}
			name := strings.Split(reflect.StructTag(tag).Get("json"), ",")[0]
			if name != "" && name != "-" {
				keys[name] = true
			}
		}
		return false
	})
	if len(keys) == 0 {
		t.Fatal("found no json keys on cmd/becky-hits' hit struct — the seam test cannot verify anything")
	}
	return keys
}

func TestSeam_EveryEmittedKeyIsReadByBeckyHits(t *testing.T) {
	readerKeys := hitsReaderKeys(t)

	// Marshal a fully-populated record the way the CLI emits it.
	data, err := json.Marshal(hit{
		SRT:      "clip.srt",
		In:       "00:00:05.000",
		Out:      "00:00:28.000",
		Q:        "a quote",
		Question: "does this matter?",
	})
	if err != nil {
		t.Fatal(err)
	}
	var emitted map[string]any
	if err := json.Unmarshal(data, &emitted); err != nil {
		t.Fatal(err)
	}

	for key := range emitted {
		if !readerKeys[key] {
			t.Errorf("becky-moment emits %q, which cmd/becky-hits does NOT read (it reads %v). "+
				"This is a broken cross-tool seam.", key, sortedKeys(readerKeys))
		}
	}

	// The fields that actually carry the window must be present, or becky-hits
	// would fall back to its default window and silently mistime every clip.
	for _, required := range []string{"srt", "in", "out"} {
		if _, ok := emitted[required]; !ok {
			t.Errorf("becky-moment must emit %q for becky-hits to build a tight window", required)
		}
	}
}

func TestSeam_TimecodeIsTheFormatBeckyHitsParses(t *testing.T) {
	// becky-hits snaps on HH:MM:SS(.mmm). Assert exact strings, not "looks like
	// a timecode" — a truthiness assertion here would pass on garbage.
	cases := map[float64]string{
		0:       "00:00:00.000",
		5:       "00:00:05.000",
		61.5:    "00:01:01.500",
		3661.25: "01:01:01.250",
		-3:      "00:00:00.000", // negatives clamp rather than emitting "-00:00:03"
	}
	for sec, want := range cases {
		if got := formatTC(sec); got != want {
			t.Errorf("formatTC(%v) = %q, want %q", sec, got, want)
		}
	}
}

// --- Spending guards -------------------------------------------------------
//
// CLAUDE.md's never-spend-money invariant is enforced in code, not judgement.
// Jordan authorised his OpenCode Zen key for the content pass, so these tests
// assert the guards that keep that spending DELIBERATE.

func TestZenJudge_RefusesResoldAnthropicEvenWhenPaidIsAllowed(t *testing.T) {
	for _, model := range []string{"claude-opus-5", "claude-sonnet-5", "claude-haiku-4-5", "anthropic/claude-sonnet-5"} {
		_, err := zenJudge(model, true, 4)
		if err == nil {
			t.Errorf("zenJudge(%q, allowPaid=true) returned no error — Claude is already paid for via Max", model)
			continue
		}
		if !strings.Contains(err.Error(), "Max subscription") {
			t.Errorf("zenJudge(%q) error = %q, want it to name the Max subscription", model, err)
		}
	}
}

func TestZenJudge_RefusesMeteredModelWithoutOptIn(t *testing.T) {
	for _, model := range []string{"gpt-5.5", "gemini-3.7-flash", "qwen3.7-max", "typo-model"} {
		if _, err := zenJudge(model, false, 4); err == nil {
			t.Errorf("zenJudge(%q, allowPaid=false) returned no error — a metered call must be opt-in", model)
		}
	}
}

func TestIsFreeZenModel_UnknownIdsAreTreatedAsPaid(t *testing.T) {
	free := []string{"deepseek-v4-flash-free", "mimo-v2.5-free", "big-pickle", "hy3:free"}
	paid := []string{"gpt-5.5", "claude-opus-5", "deepseek-v4-pro", "", "freestyle-model"}

	for _, id := range free {
		if !isFreeZenModel(id) {
			t.Errorf("isFreeZenModel(%q) = false, want true", id)
		}
	}
	for _, id := range paid {
		if isFreeZenModel(id) {
			t.Errorf("isFreeZenModel(%q) = true, want false (unknown ids must default to PAID)", id)
		}
	}
}

func TestZenJudge_FreeModelStillNeedsAKeyAndSaysSo(t *testing.T) {
	t.Setenv("BECKY_ZEN_API_KEY", "")
	t.Setenv("OPENCODE_API_KEY", "")
	t.Setenv("OPENCODE_ZEN_API_KEY", "")
	_, err := zenJudge(defaultZenModel, false, 4)
	if err == nil {
		t.Fatal("expected an error when no API key is set")
	}
	if !strings.Contains(err.Error(), "BECKY_ZEN_API_KEY") {
		t.Errorf("error = %q, want it to name the env var to set", err)
	}
}

// --- Report shaping --------------------------------------------------------

func TestFirstLine_TruncatesOnAWordBoundary(t *testing.T) {
	long := strings.Repeat("word ", 40)
	got := firstLine(long)
	if len([]rune(got)) > 91 {
		t.Errorf("firstLine returned %d runes, want <= 91", len([]rune(got)))
	}
	if !strings.HasSuffix(got, "…") {
		t.Errorf("a truncated label should end with an ellipsis, got %q", got)
	}
	short := "already short"
	if got := firstLine(short); got != short {
		t.Errorf("firstLine(%q) = %q, want it unchanged", short, got)
	}
}

func TestIsTranscript(t *testing.T) {
	yes := []string{"a.srt", "b.VTT", "c.json3", "d.transcript.json", `X:\ev\clip.en.srt`}
	no := []string{"a.mp4", "b.json", "c.txt", "d.info.json", ""}
	for _, p := range yes {
		if !isTranscript(p) {
			t.Errorf("isTranscript(%q) = false, want true", p)
		}
	}
	for _, p := range no {
		if isTranscript(p) {
			t.Errorf("isTranscript(%q) = true, want false", p)
		}
	}
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
