package quotes

import (
	"context"
	"strings"
	"testing"
)

func TestSliceJSONArray(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{`[{"quote":"x"}]`, `[{"quote":"x"}]`},
		{"prose before [1,2] and after", "[1,2]"},
		{"```json\n[\"a\"]\n```", `["a"]`},
		{"no array here", ""},
		{"][ mismatched", ""},
	}
	for _, tt := range tests {
		if got := sliceJSONArray(tt.in); got != tt.want {
			t.Errorf("sliceJSONArray(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestParseSelectionJSON(t *testing.T) {
	raw := `Here are the quotes:
	[
	  {"quote":"press charges","because":"threat"},
	  {"quote":"","because":"empty dropped"},
	  {"quote":"two restraining orders","because":"prior context"}
	]`
	anchors := parseSelectionJSON(raw)
	if len(anchors) != 2 {
		t.Fatalf("expected 2 anchors (empty-quote dropped), got %d", len(anchors))
	}
	if anchors[0].Quote != "press charges" || anchors[1].Quote != "two restraining orders" {
		t.Errorf("anchors parsed wrong: %+v", anchors)
	}
}

func TestParseSelectionJSON_GarbageYieldsNone(t *testing.T) {
	if a := parseSelectionJSON("the model rambled with no json"); len(a) != 0 {
		t.Errorf("expected no anchors from non-JSON reply, got %d", len(a))
	}
	if a := parseSelectionJSON("[not valid json}"); len(a) != 0 {
		t.Errorf("expected no anchors from malformed JSON, got %d", len(a))
	}
}

func TestLocalClient_AvailableDegrades(t *testing.T) {
	// no model + no server -> Available returns a descriptive error, never panics.
	c := NewLocalClient(`X:\nope\missing-model.gguf`, `X:\nope\llama-server.exe`, 0, nil)
	err := c.Available()
	if err == nil {
		t.Fatal("expected Available() error when model+server are absent")
	}
	if !strings.Contains(err.Error(), "model") {
		t.Errorf("error should name the missing model, got: %v", err)
	}
}

func TestLocalClient_AvailableWithBaseURL(t *testing.T) {
	// a BaseURL bypasses the model/binary checks (assume an external server).
	c := NewLocalClient("", "", 0, nil)
	c.BaseURL = "http://127.0.0.1:9999"
	if err := c.Available(); err != nil {
		t.Errorf("Available() with BaseURL should be nil, got: %v", err)
	}
}

func TestLocalSelector_DegradesWhenUnavailable(t *testing.T) {
	sel := &LocalSelector{Client: NewLocalClient(`X:\nope\m.gguf`, `X:\nope\s.exe`, 0, nil)}
	_, err := sel.Select(context.Background(), "some transcript", "find threats")
	if err == nil {
		t.Fatal("LocalSelector should return an error (not crash) when the model is unavailable")
	}
}

func TestLLMExpander_DegradesWhenUnavailable(t *testing.T) {
	exp := &LLMExpander{Client: NewLocalClient(`X:\nope\m.gguf`, `X:\nope\s.exe`, 0, nil)}
	ok, err := exp.NeedsContext(context.Background(), "block", "neighbor")
	if err == nil {
		t.Fatal("LLMExpander should return an error when the model is unavailable")
	}
	if ok {
		t.Error("NeedsContext should be false on degrade")
	}
}

func TestLocalSelector_NilClient(t *testing.T) {
	sel := &LocalSelector{}
	if _, err := sel.Select(context.Background(), "t", "c"); err == nil {
		t.Error("expected error for a LocalSelector with no client")
	}
}
