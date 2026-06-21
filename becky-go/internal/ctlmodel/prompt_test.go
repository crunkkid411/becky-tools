package ctlmodel

import (
	"strings"
	"testing"

	"becky-go/internal/ctledit"
)

func TestSnapshot(t *testing.T) {
	if got := Snapshot(nil); !strings.Contains(got, "empty") {
		t.Errorf("Snapshot(nil) = %q, want an 'empty' note", got)
	}
	s := Snapshot(testArr())
	for _, want := range []string{`id="bass"`, `id="lead"`, `id="drums"`, "bpm=140", "bus=bus.808", "genre=crunkcore"} {
		if !strings.Contains(s, want) {
			t.Errorf("Snapshot missing %q in:\n%s", want, s)
		}
	}
}

func TestBuildPrompt(t *testing.T) {
	p := BuildPrompt("mute the bass", Snapshot(testArr()))
	for _, want := range []string{"mute the bass", "BeckyEditBatch", `id="bass"`, "JSON:", ctledit.OpSetTempo} {
		if !strings.Contains(p, want) {
			t.Errorf("prompt missing %q", want)
		}
	}
}

func TestDecodeBatch_Clean(t *testing.T) {
	b, err := DecodeBatch(`{"summary":"x","edits":[{"op":"set_tempo","bpm":128}]}`)
	if err != nil {
		t.Fatalf("DecodeBatch: %v", err)
	}
	if len(b.Edits) != 1 || b.Edits[0].Op != ctledit.OpSetTempo || b.Edits[0].BPM != 128 {
		t.Errorf("decoded = %+v, want one set_tempo 128", b)
	}
}

func TestDecodeBatch_WithChatter(t *testing.T) {
	out := "Sure! Here is the edit:\n{\"summary\":\"mute\",\"edits\":[{\"op\":\"mute\",\"target\":\"bass\",\"muted\":true}]}\nDone."
	b, err := DecodeBatch(out)
	if err != nil {
		t.Fatalf("DecodeBatch: %v", err)
	}
	if len(b.Edits) != 1 || b.Edits[0].Op != ctledit.OpMute {
		t.Errorf("decoded = %+v, want one mute edit", b)
	}
}

func TestDecodeBatch_NoJSON(t *testing.T) {
	if _, err := DecodeBatch("there is no json here"); err == nil {
		t.Error("expected error for input with no JSON object")
	}
}

func TestExtractFirstJSONObject(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{`{"a":1}`, `{"a":1}`},
		{`prefix {"a":{"b":2}} suffix`, `{"a":{"b":2}}`},
		{`{"s":"has } brace"}`, `{"s":"has } brace"}`},
		{`{"s":"esc \" quote } "}`, `{"s":"esc \" quote } "}`},
		{`no object`, ``},
		{`{unterminated`, ``},
	}
	for _, c := range cases {
		if got := extractFirstJSONObject(c.in); got != c.want {
			t.Errorf("extractFirstJSONObject(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
