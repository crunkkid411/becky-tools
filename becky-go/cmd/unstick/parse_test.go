package main

import (
	"strings"
	"testing"
)

func TestParseActionScalesFaraGridToScreenshot(t *testing.T) {
	reply := "A cookie banner covers the page.\n<tool_call>\n" +
		`{"name": "computer_use", "arguments": {"action": "left_click", "coordinate": [500, 250]}}` +
		"\n</tool_call>"
	got, err := parseAction(reply, 1280, 850)
	if err != nil {
		t.Fatal(err)
	}
	if got["action"] != "left_click" || got["x"] != 640 || got["y"] != 213 {
		t.Fatalf("got action=%v x=%v y=%v, want left_click 640 213", got["action"], got["x"], got["y"])
	}
	if got["thoughts"] != "A cookie banner covers the page." {
		t.Fatalf("thoughts = %q", got["thoughts"])
	}
}

func TestParseActionTerminateHasNoCoordinates(t *testing.T) {
	reply := "<tool_call>\n{\"name\":\"computer_use\",\"arguments\":{\"action\":\"terminate\",\"answer\":\"nothing blocking\"}}\n</tool_call>"
	got, err := parseAction(reply, 1280, 850)
	if err != nil {
		t.Fatal(err)
	}
	if got["action"] != "terminate" || got["answer"] != "nothing blocking" {
		t.Fatalf("got %v", got)
	}
	if _, has := got["x"]; has {
		t.Fatal("terminate must not carry x")
	}
}

func TestParseActionRejectsReplyWithoutToolCall(t *testing.T) {
	_, err := parseAction("I think there is a popup.", 1280, 850)
	if err == nil || !strings.Contains(err.Error(), "no <tool_call>") {
		t.Fatalf("err = %v", err)
	}
}

func TestFaraSystemPromptEmbedded(t *testing.T) {
	if !strings.Contains(faraSystem, "The screen's resolution is 1000x1000") {
		t.Fatal("embedded Fara prompt missing its 1000x1000 grid line")
	}
}
