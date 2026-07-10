package main

import "testing"

// The original positional contract must keep working unchanged.
func TestParseArgs_PositionalImageStillWorks(t *testing.T) {
	img, phrase, _, _, err := parseArgs([]string{"photo.png", "the red button"})
	if err != nil {
		t.Fatalf("parseArgs error: %v", err)
	}
	if img != "photo.png" || phrase != "the red button" {
		t.Errorf("got image=%q phrase=%q, want image=photo.png phrase=%q", img, phrase, "the red button")
	}
}

// --image is the convention every image-taking becky tool now shares
// (becky-AI-Agent-review-1.md acceptance criterion 8) — becky-perceive only
// took a bare positional before.
func TestParseArgs_ImageFlag(t *testing.T) {
	img, phrase, _, _, err := parseArgs([]string{"--image", "photo.png", "the", "red", "button"})
	if err != nil {
		t.Fatalf("parseArgs error: %v", err)
	}
	if img != "photo.png" {
		t.Errorf("image = %q, want photo.png", img)
	}
	if phrase != "the red button" {
		t.Errorf("phrase = %q, want %q", phrase, "the red button")
	}
}

func TestParseArgs_ImageFlagEqualsForm(t *testing.T) {
	img, phrase, _, _, err := parseArgs([]string{"--image=photo.png", "target phrase"})
	if err != nil {
		t.Fatalf("parseArgs error: %v", err)
	}
	if img != "photo.png" || phrase != "target phrase" {
		t.Errorf("got image=%q phrase=%q", img, phrase)
	}
}

// --json must be recognized (not swallowed into the phrase) since
// becky-perceive's default output is already the JSON envelope.
func TestParseArgs_JSONFlagNotSwallowedIntoPhrase(t *testing.T) {
	img, phrase, _, pretty, err := parseArgs([]string{"photo.png", "the button", "--json"})
	if err != nil {
		t.Fatalf("parseArgs error: %v", err)
	}
	if pretty {
		t.Error("--json must not set pretty")
	}
	if img != "photo.png" || phrase != "the button" {
		t.Errorf("got image=%q phrase=%q, want image=photo.png phrase=%q (--json must not leak in)", img, phrase, "the button")
	}
}

func TestParseArgs_MissingImageFlagValue(t *testing.T) {
	if _, _, _, _, err := parseArgs([]string{"--image"}); err == nil {
		t.Error("expected an error when --image has no value")
	}
}
