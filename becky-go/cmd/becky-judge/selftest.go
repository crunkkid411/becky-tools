package main

// selftest.go is becky-judge's one-command OFFLINE proof: it exercises the real
// plumbing — window building from a .srt, the verdict->hit mapping (hitsFor), and
// writing the becky-hits-shaped hit-list — with NO qmd and NO Claude, so it runs
// anywhere and is measurable evidence the pipeline works end to end. Asserts values,
// exits non-zero on any failure.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const selftestSRT = "1\r\n00:00:01,000 --> 00:00:03,000\r\nwelcome back everyone\r\n\r\n" +
	"2\r\n00:00:04,000 --> 00:00:06,000\r\ngo find the green haired guy and let him know\r\n\r\n" +
	"3\r\n00:00:07,000 --> 00:00:09,000\r\nanyway that is all for tonight\r\n\r\n"

func runSelftest() {
	dir, err := os.MkdirTemp("", "becky-judge-selftest")
	if err != nil {
		selftestFail("mkdir temp: %v", err)
	}
	defer os.RemoveAll(dir)
	srt := filepath.Join(dir, "stream_parakeet_transcription.srt")
	if err := os.WriteFile(srt, []byte(selftestSRT), 0o644); err != nil {
		selftestFail("write srt: %v", err)
	}

	// 1) window building snaps to the .srt and spans +/- the half-window.
	in, out, win, ok := windowAround(srt, 4.0, 1)
	if !ok {
		selftestFail("windowAround returned not-ok")
	}
	if in != 1 || out != 9 {
		selftestFail("window span = [%v,%v], want [1,9] (cue 2 +/- 1)", in, out)
	}
	if !strings.Contains(win, "[00:00:04]") || !strings.Contains(win, "green haired") {
		selftestFail("window text missing timecode/quote: %q", win)
	}

	// 2) the verdict->hit mapping keeps only kept candidates and carries who+reason.
	cands := []candidate{
		{ID: 1, SRT: "stream_parakeet_transcription.srt", Name: "stream.mp4", In: in, Out: out, Window: win},
		{ID: 2, SRT: "stream_parakeet_transcription.srt", Name: "stream.mp4", In: 20, Out: 24, Window: "unrelated"},
	}
	reason := map[int]verdict{
		1: {ID: 1, Keep: true, Who: "Hair Jordan", Reason: "directs viewers to the green-haired man"},
	}
	kept := []candidate{cands[0]} // only id 1 survives
	of := hitsFor(kept, reason, dir, "harass the green haired guy")
	if len(of.Hits) != 1 {
		selftestFail("kept %d hits, want 1", len(of.Hits))
	}
	h := of.Hits[0]
	if h.SRT != "stream_parakeet_transcription.srt" {
		selftestFail("hit srt = %q", h.SRT)
	}
	if f, _ := strconv.ParseFloat(h.In, 64); f != 1 {
		selftestFail("hit in = %q, want 1", h.In)
	}
	if !strings.Contains(h.Q, "Hair Jordan") {
		selftestFail("hit label should carry who/reason, got %q", h.Q)
	}

	// 3) the hit-list round-trips through JSON in the becky-hits input shape.
	out2 := filepath.Join(dir, "_forensic_hits.json")
	if err := writeJSON(out2, of); err != nil {
		selftestFail("write hit-list: %v", err)
	}
	var back outFile
	b, _ := os.ReadFile(out2)
	if err := json.Unmarshal(b, &back); err != nil {
		selftestFail("re-read hit-list: %v", err)
	}
	if len(back.Hits) != 1 || back.Folder != dir {
		selftestFail("round-trip mismatch: %+v", back)
	}

	fmt.Println("becky-judge selftest: PASS (window build + verdict mapping + hit-list write/read)")
}

func selftestFail(format string, a ...any) {
	fmt.Fprintf(os.Stderr, "becky-judge selftest: FAIL - "+format+"\n", a...)
	os.Exit(1)
}
