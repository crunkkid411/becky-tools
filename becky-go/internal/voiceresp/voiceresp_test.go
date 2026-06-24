package voiceresp

import (
	"strings"
	"testing"

	"becky-go/internal/catalog"
)

// Every catalog tool must have all three outcomes with >=1 line, plus a fix_verb.
func TestGenerate_CoversEveryToolAndOutcome(t *testing.T) {
	m := Generate()
	for _, c := range catalog.All() {
		e, ok := m[c.Verb]
		if !ok {
			t.Errorf("%s: missing from generated response map", c.Verb)
			continue
		}
		if len(e.OK) < 1 {
			t.Errorf("%s: no ok lines", c.Verb)
		}
		if len(e.Partial) < 1 {
			t.Errorf("%s: no partial lines", c.Verb)
		}
		if len(e.Error) < 1 {
			t.Errorf("%s: no error lines", c.Verb)
		}
		if strings.TrimSpace(e.FixVerb) == "" {
			t.Errorf("%s: empty fix_verb", c.Verb)
		}
	}
	if len(m) != len(catalog.All()) {
		t.Errorf("map has %d tools, catalog has %d", len(m), len(catalog.All()))
	}
}

// Round-robin returns each variant before repeating any.
func TestChooser_RoundRobinNoRepeatThenCycles(t *testing.T) {
	m := Generate()
	c := NewChooser(m)
	tool := "becky-transcribe"
	n := len(m[tool].OK)
	if n < 2 {
		t.Fatalf("need >=2 ok lines to test round-robin, got %d", n)
	}
	seen := map[string]bool{}
	for i := 0; i < n; i++ {
		line, ok := c.Choose(tool, OutcomeOK)
		if !ok {
			t.Fatalf("Choose returned not-ok at i=%d", i)
		}
		if seen[line] {
			t.Fatalf("round-robin repeated %q before cycling through all %d variants", line, n)
		}
		seen[line] = true
	}
	// After exhausting all variants, it must cycle back to the first.
	first, _ := NewChooser(m).Choose(tool, OutcomeOK)
	cycled, _ := c.Choose(tool, OutcomeOK)
	if cycled != first {
		t.Errorf("after a full cycle, Choose = %q, want it to wrap to first %q", cycled, first)
	}
}

// Independent (tool, outcome) keys advance independently.
func TestChooser_KeysAreIndependent(t *testing.T) {
	c := NewChooser(Generate())
	okLine, _ := c.Choose("becky-ocr", OutcomeOK)
	errLine, _ := c.Choose("becky-ocr", OutcomeError)
	if okLine == errLine {
		t.Errorf("ok and error lines should differ: %q", okLine)
	}
	// choosing error did not disturb the ok cursor: next ok is the 2nd ok variant.
	okLine2, _ := c.Choose("becky-ocr", OutcomeOK)
	if okLine2 == okLine {
		t.Errorf("ok cursor should have advanced to the 2nd variant, got the same %q", okLine)
	}
}

// "fix it" resolves to the tool's declared fix_verb.
func TestFixVerb_Resolves(t *testing.T) {
	m := Generate()
	if got := m.FixVerb("becky-transcribe"); got != "becky-new-tool" {
		t.Errorf("fix verb = %q, want becky-new-tool", got)
	}
	// custom fix_verb is honored.
	m["becky-transcribe"] = Entry{OK: []string{"x"}, Partial: []string{"x"}, Error: []string{"x"}, FixVerb: "becky-repair-asr"}
	if got := m.FixVerb("becky-transcribe"); got != "becky-repair-asr" {
		t.Errorf("custom fix verb = %q, want becky-repair-asr", got)
	}
	// unknown tool falls back to the default.
	if got := m.FixVerb("becky-unknown"); got != "becky-new-tool" {
		t.Errorf("unknown fix verb = %q, want becky-new-tool", got)
	}
}

// An unknown tool / empty outcome returns ok=false (no panic, no model).
func TestChooser_UnknownToolNotOK(t *testing.T) {
	c := NewChooser(Generate())
	if _, ok := c.Choose("nope", OutcomeOK); ok {
		t.Error("unknown tool should return ok=false")
	}
}

// Generated JSON round-trips through Load and stays complete.
func TestJSONRoundTrip(t *testing.T) {
	m := Generate()
	b, err := m.JSON()
	if err != nil {
		t.Fatalf("JSON: %v", err)
	}
	back, err := Load(b)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(back) != len(m) {
		t.Errorf("round-trip lost tools: %d -> %d", len(m), len(back))
	}
	if len(back["becky-transcribe"].Error) < 1 {
		t.Error("round-trip lost error lines")
	}
	// the error line should name the tool (whoretana-voice default).
	if !strings.Contains(strings.Join(back["becky-transcribe"].Error, " "), "becky-transcribe") {
		t.Error("default error line should mention the tool name")
	}
}
