package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"becky-go/internal/beckyio"
	"becky-go/internal/footage"
)

// runSelftest exercises the real code path with a temp folder (a 0-byte video +
// a real .srt) and asserts the produced reel by value — the one-command offline
// proof. No bytes of the "video" are ever read (becky never opens source media).
func runSelftest() {
	dir, err := os.MkdirTemp("", "becky-hits-selftest")
	if err != nil {
		beckyio.Fatalf("selftest: tempdir: %v", err)
	}
	defer os.RemoveAll(dir)

	// A source video (content irrelevant — only its name/extension is indexed) and
	// its strict same-stem transcript with two cues.
	vid := filepath.Join(dir, "example.mp4")
	srt := filepath.Join(dir, "example.srt")
	_ = os.WriteFile(vid, []byte{}, 0o644)
	srtBody := "1\r\n00:00:10,000 --> 00:00:13,000\r\nthe cat was returned that night\r\n\r\n" +
		"2\r\n00:01:00,000 --> 00:01:05,000\r\nnothing else happened\r\n"
	_ = os.WriteFile(srt, []byte(srtBody), 0o644)

	idx, err := footage.Index(dir)
	if err != nil {
		beckyio.Fatalf("selftest: index: %v", err)
	}

	hits := []hit{
		{SRT: "example.srt", T: "00:00:11", Question: "who is this?"},               // cue 1 + a review question
		{SRT: "example.srt", T: "0:01:02", Q: "custom"},                             // cue 2, explicit label, no question
		{SRT: "example.srt", In: "00:00:30", Out: "0:35", Question: "who is this?"}, // SAME question -> grouped
		{SRT: "missing.srt", T: "00:00:05"},                                         // no source video, skipped
	}
	reel, warnings, cqs := buildReel(idx, hits, "selftest", 0.5, 4.0)

	fail := false
	check := func(name string, ok bool, detail string) {
		status := "PASS"
		if !ok {
			status, fail = "FAIL", true
		}
		fmt.Fprintf(os.Stderr, "[%s] %s %s\n", status, name, detail)
	}

	check("resolve.video", len(idx.Videos) == 1 && idx.Videos[0].HasTranscript,
		fmt.Sprintf("(videos=%d)", len(idx.Videos)))
	check("clip.count", len(reel.Clips) == 3, fmt.Sprintf("(got %d, want 3)", len(reel.Clips)))
	if len(reel.Clips) == 3 {
		c0, c1, c2 := reel.Clips[0], reel.Clips[1], reel.Clips[2]
		check("clip0.window", c0.In == 9.5 && c0.Out == 13.5, fmt.Sprintf("(in=%v out=%v want 9.5/13.5)", c0.In, c0.Out))
		check("clip0.label_from_cue", c0.Label == "the cat was returned that night", "("+c0.Label+")")
		check("clip1.label_override", c1.Label == "custom", "("+c1.Label+")")
		check("clip1.window", c1.In == 59.5 && c1.Out == 65.5, fmt.Sprintf("(in=%v out=%v want 59.5/65.5)", c1.In, c1.Out))
		check("clip2.explicit_window", c2.In == 30 && c2.Out == 35, fmt.Sprintf("(in=%v out=%v want 30/35)", c2.In, c2.Out))
		check("clip.source_resolved", c0.Source == idx.Videos[0].Path, "("+c0.Source+")")
	}
	check("skip.orphan_warned", len(warnings) == 1, fmt.Sprintf("(warnings=%d, want 1)", len(warnings)))
	check("overlay.enabled", reel.Overlay.Enabled, "")

	// Q&A sidecar: two clips share one question -> one grouped entry with both clip IDs.
	check("questions.clip_count", len(cqs) == 2, fmt.Sprintf("(got %d, want 2)", len(cqs)))
	reelPath := filepath.Join(dir, "r.reel.json")
	if err := writeQuestions(reelPath, cqs); err != nil {
		check("questions.write", false, err.Error())
	} else {
		b, _ := os.ReadFile(questionsPathFor(reelPath))
		var qf questionsFile
		_ = json.Unmarshal(b, &qf)
		ok := len(qf.Questions) == 1 && qf.Questions[0].Question == "who is this?" && len(qf.Questions[0].ClipIDs) == 2
		check("questions.grouped", ok, fmt.Sprintf("(entries=%d)", len(qf.Questions)))
	}

	if fail {
		fmt.Fprintln(os.Stderr, "selftest: FAIL")
		os.Exit(1)
	}
	beckyio.PrintJSON(map[string]any{"selftest": "ok", "clips": len(reel.Clips), "skipped": len(warnings)})
}
