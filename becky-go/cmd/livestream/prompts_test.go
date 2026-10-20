package main

import (
	"os"
	"path/filepath"
	"testing"
)

// An edited prompt throws away only the answers asked with the old words.
func TestStampPromptsRedoesOnlyAfterAnEdit(t *testing.T) {
	cache := filepath.Join(t.TempDir(), "coarse-video.json")
	write := func() { _ = os.WriteFile(cache, []byte("{}"), 0o644) }
	exists := func() bool { _, err := os.Stat(cache); return err == nil }

	write()
	stampPrompts(cache, "built-in words") // first run with fingerprints: old answers kept
	if !exists() {
		t.Fatal("answers saved before fingerprints were thrown away")
	}
	stampPrompts(cache, "built-in words") // same words: kept
	if !exists() {
		t.Fatal("unchanged prompt threw the answers away")
	}
	stampPrompts(cache, "Jordan's words") // edited: asked again
	if exists() {
		t.Fatal("edited prompt kept the old answers")
	}

	write()
	cache2 := cache + "2"
	_ = os.WriteFile(cache2, []byte("{}"), 0o644)
	edited["edited before the first run"] = true
	stampPrompts(cache2, "edited before the first run") // no fingerprint yet, but the file was edited
	if _, err := os.Stat(cache2); err == nil {
		t.Fatal("a prompt edited before its first run kept the old answers")
	}
}

func TestFillPutsDataIntoThePrompt(t *testing.T) {
	got := fill("A {X} b {X} {Y}%d", "{X}", "1%s", "{Y}", "{X}")
	if got != "A 1%s b 1%s {X}%d" {
		t.Fatalf("got %q", got)
	}
}
