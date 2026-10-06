package main

import (
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func near(a, b float64) bool { return math.Abs(a-b) < 1e-6 }

func TestSplitSentencesAtPunctuationAndPauses(t *testing.T) {
	ws := []Word{
		{Word: "Hello", Start: 0, End: 0.3}, {Word: "world.", Start: 0.35, End: 0.7},
		{Word: "This", Start: 0.8, End: 1.0}, {Word: "is", Start: 1.05, End: 1.2}, {Word: "fine", Start: 1.25, End: 1.5},
		{Word: "and", Start: 3.0, End: 3.2}, {Word: "more?\"", Start: 3.3, End: 3.6},
	}
	ss := splitSentences(ws)
	want := []Sentence{
		{ID: 0, W0: 0, W1: 1, Start: 0, End: 0.7, Text: "Hello world."},
		{ID: 1, W0: 2, W1: 4, Start: 0.8, End: 1.5, Text: "This is fine"},
		{ID: 2, W0: 5, W1: 6, Start: 3.0, End: 3.6, Text: "and more?\""},
	}
	if len(ss) != len(want) {
		t.Fatalf("got %d sentences, want %d: %+v", len(ss), len(want), ss)
	}
	for i := range want {
		if ss[i] != want[i] {
			t.Errorf("sentence %d = %+v, want %+v", i, ss[i], want[i])
		}
	}
}

func TestWindowsCapSentencesAndWords(t *testing.T) {
	mk := func(n, words int) []Sentence {
		ss := make([]Sentence, n)
		for i := range ss {
			ss[i] = Sentence{ID: i, W0: i * words, W1: i*words + words - 1}
		}
		return ss
	}
	got := windows(mk(100, 10))
	if len(got) != 3 || got[0] != [2]int{0, 40} || got[2] != [2]int{80, 100} {
		t.Errorf("10-word sentences: %v", got)
	}
	got = windows(mk(50, 30)) // 24 sentences pass 700 words
	if len(got) != 3 || got[0] != [2]int{0, 24} || got[1] != [2]int{24, 48} || got[2] != [2]int{48, 50} {
		t.Errorf("30-word sentences: %v", got)
	}
}

func TestParseDecisionsLinesUpByPosition(t *testing.T) {
	raw := `noise {"decisions":[{"id":0,"label":"chatter","topic":1,"confidence":150},{"id":9,"label":"meta","topic":2,"confidence":-3},{"id":4,"label":"narrative","topic":7,"confidence":80}]}`
	ds, err := parseDecisions(raw, []int{5, 6, 7}, 2)
	if err != nil {
		t.Fatal(err)
	}
	// unknown label reads as narrative; wanted topic + narrative = keep
	if ds[0].ID != 5 || ds[0].Label != "narrative" || ds[0].Topic != 1 || ds[0].Confidence != 100 || !ds[0].Keep {
		t.Errorf("first = %+v", ds[0])
	}
	// wanted topic but not narrative = cut
	if ds[1].ID != 6 || ds[1].Label != "meta" || ds[1].Topic != 2 || ds[1].Confidence != 0 || ds[1].Keep {
		t.Errorf("second = %+v", ds[1])
	}
	// a topic number past the list = no topic = cut
	if ds[2].ID != 7 || ds[2].Topic != 0 || ds[2].Keep {
		t.Errorf("third = %+v", ds[2])
	}
	if _, err := parseDecisions(raw, []int{1, 2, 3, 4}, 2); err == nil || !strings.Contains(err.Error(), "answered 3 of 4") {
		t.Errorf("short answer error = %v", err)
	}
}

func TestParseTopicsDropsBlanks(t *testing.T) {
	got, err := parseTopics(`Sure: {"topics":["  the channel update ", "", "baldness"]}`)
	if err != nil || !slices.Equal(got, []string{"the channel update", "baldness"}) {
		t.Errorf("topics = %q, %v", got, err)
	}
	if _, err := parseTopics(`{"topics":[" "]}`); err == nil {
		t.Error("a list of blanks must be an error")
	}
}

func TestReviewTargetsUnsureBoundariesAndNearKeeps(t *testing.T) {
	ds := []Decision{{Keep: true, Confidence: 100}, {Keep: true, Confidence: 100}}
	for range 8 {
		ds = append(ds, Decision{Keep: false, Confidence: 100})
	}
	ds[8].Confidence = 40 // far from any keep, but unsure
	got := reviewTargets(ds)
	if want := []int{1, 2, 3, 4, 5, 8}; !slices.Equal(got, want) {
		t.Errorf("targets = %v, want %v (boundary 1-2, cuts within 4 of a keep, the unsure one)", got, want)
	}
}

func TestConcludeAgreeDisagreeAndIsolated(t *testing.T) {
	ds := []Decision{
		{ID: 0, Label: "narrative", Keep: true, Confidence: 95},
		{ID: 1, Label: "narrative", Keep: false, Confidence: 60},
		{ID: 2, Label: "chat_reply", Keep: false, Confidence: 80},
		{ID: 3, Label: "meta", Confidence: 95}, {ID: 4, Label: "meta", Confidence: 95}, {ID: 5, Label: "meta", Confidence: 95},
		{ID: 6, Label: "narrative", Keep: true, Confidence: 50},
	}
	reviews := map[int]Review{
		1: {Model: "qwen3.5", Label: "narrative", Topic: 1, Keep: true, Confidence: 70},
		2: {Model: "qwen3.5", Label: "chat_reply", Keep: false, Confidence: 90},
	}
	out := conclude(ds, reviews, "gemma4")
	if !out[0].Keep || out[0].Unsure {
		t.Errorf("sure keep changed: %+v", out[0])
	}
	if !out[1].Keep || !out[1].Unsure || out[1].Note != "gemma4 said cut (not a wanted topic), qwen3.5 said keep (topic 1)" {
		t.Errorf("disagreement = %+v", out[1])
	}
	if out[2].Keep || out[2].Unsure || out[2].Confidence != 90 || out[2].Review == nil {
		t.Errorf("agreement = %+v", out[2])
	}
	if out[6].Keep || !out[6].Unsure || out[6].Note != "gemma4 was unsure (50%) whether to keep it - cut: nothing confidently kept nearby" {
		t.Errorf("isolated unsure = %+v", out[6])
	}
	if ds[1].Keep || ds[1].Unsure {
		t.Error("conclude changed its input")
	}
}

func TestParseClaudeNeedsEveryID(t *testing.T) {
	p := filepath.Join(t.TempDir(), "d.txt")
	body := "Here are the decisions:\n0 meta cut 95\n1 narrative keep 90%\n- 2 chat_reply cut 60 could be part of it\n7 narrative keep 90\n"
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	ds, err := parseClaude(p, 3)
	if err != nil {
		t.Fatal(err)
	}
	if ds[1].Confidence != 90 || !ds[1].Keep || ds[2].Note != "could be part of it" || ds[2].Confidence != 60 || ds[2].Keep {
		t.Errorf("decisions = %+v", ds)
	}
	if _, err := parseClaude(p, 4); err == nil || !strings.Contains(err.Error(), "missing 1 of 4") {
		t.Errorf("missing-id error = %v", err)
	}
}

// pcmWith is 4 s of loud audio with quiet stretches.
func pcmWith(sr int, loud, quiet int16, quietSpans ...span) *audio {
	a := &audio{sr: sr, pcm: make([]int16, 4*sr)}
	for i := range a.pcm {
		a.pcm[i] = loud
		t := float64(i) / float64(sr)
		for _, q := range quietSpans {
			if t >= q.A && t < q.B {
				a.pcm[i] = quiet
			}
		}
	}
	return a
}

func TestFrameCutPicksQuietestFrame(t *testing.T) {
	au := pcmWith(16000, 10000, 10, span{0.95, 1.25})
	at, how, db := au.frameCut(0.8, 1.4, 30, false)
	if !near(at, 29.0/30) || how != "speech" || db > -70 {
		t.Errorf("earliest quiet frame = %.4f %s %.1f, want %.4f", at, how, db, 29.0/30)
	}
	if at, _, _ = au.frameCut(0.8, 1.4, 30, true); !near(at, 37.0/30) {
		t.Errorf("latest quiet frame = %.4f, want %.4f", at, 37.0/30)
	}
	if at, how, _ = au.frameCut(1.01, 1.02, 30, false); how != "mid" || !near(at, 1.0) { // midpoint, snapped
		t.Errorf("no frame in range = %.4f %s", at, how)
	}
}

func TestContentRangesUseBeckyCutEdgeOrQuietFrame(t *testing.T) {
	ws := []Word{
		{Word: "Hello", Start: 0, End: 0.4}, {Word: "there.", Start: 0.5, End: 0.9},
		{Word: "Topic", Start: 2.0, End: 2.4}, {Word: "starts.", Start: 2.5, End: 3.0},
		{Word: "Chat", Start: 3.05, End: 3.4}, {Word: "reply.", Start: 3.5, End: 3.9},
	}
	keep := []bool{false, false, true, true, false, false}
	keeps := []span{{0, 1.0}, {1.9, 3.95}}
	au := pcmWith(16000, 8000, 30, span{3.0, 3.05})
	rs := contentRanges(ws, keep, keeps, au, 30, 4.0)
	if len(rs) != 1 {
		t.Fatalf("ranges = %+v", rs)
	}
	r := rs[0]
	if !near(r.In, 1.9) || r.InHow != "becky-cut" || !near(r.Out, 91.0/30) || r.OutHow != "speech" || r.W0 != 2 || r.W1 != 3 {
		t.Errorf("range = %+v", r)
	}
	if len(loudEdges(rs, -50)) != 0 || len(loudEdges(rs, -70)) != 1 {
		t.Errorf("loud edges: quiet cut at %.1f dB misjudged", r.OutDB)
	}
	ps := finalPieces(rs, keeps, 30)
	if len(ps) != 1 || !near(ps[0].A, 1.9) || !near(ps[0].B, 91.0/30) {
		t.Errorf("final pieces = %+v", ps)
	}
}

func TestTimelineMapping(t *testing.T) {
	ps := []piece{{In: 10, Out: 12, TL: 0}, {In: 20, Out: 25, TL: 2}}
	t0, t1, ok := toTimeline(ps, 11, 21)
	if !ok || !near(t0, 1) || !near(t1, 3) {
		t.Errorf("toTimeline = %v %v %v", t0, t1, ok)
	}
	if _, _, ok := toTimeline(ps, 13, 14); ok {
		t.Error("a span that was cut mapped onto the timeline")
	}
	src, join := fromTimeline(ps, 2.5)
	if !near(src, 20.5) || !near(join, 0.5) {
		t.Errorf("fromTimeline = %v %v", src, join)
	}
	if tl, on := tlOf(ps, 11.5, 12.5); !on || !near(tl, 1.5) {
		t.Errorf("tlOf partly kept = %v %v", tl, on)
	}
	if tl, on := tlOf(ps, 13, 14); on || !near(tl, 2) {
		t.Errorf("tlOf all cut = %v %v (want the cut at 2)", tl, on)
	}
	if tl, on := tlOf(ps, 26, 27); on || !near(tl, 7) {
		t.Errorf("tlOf after the end = %v %v", tl, on)
	}
}

// Regression (2026-10-05): "I have hair" was reported lost from all three edits.
// Parakeet put "I" 0.3 s early, in the silence BeckyCut removed; the edit plays it.
func TestEarlyWordAtACutIsHeardNotLost(t *testing.T) {
	ps := []piece{{In: 788.733, Out: 789.9, TL: 80}, {In: 791.0, Out: 794.1, TL: 81.167}}
	tlI, onI := tlOf(ps, 790.70, 791.02) // span mostly in the removed silence
	tlUh, onUh := tlOf(ps, 790.2, 790.5) // span entirely in it
	if !onI || !near(tlI, 81.167) || onUh || !near(tlUh, 81.167) {
		t.Fatalf("tlOf: I %v %v, uh %v %v", tlI, onI, tlUh, onUh)
	}
	planned := []tword{{norm: "uh", tl: tlUh}, {norm: "i", tl: tlI}, {norm: "have", tl: 81.19}}
	heard := []tword{{norm: "i", tl: 80.95}, {norm: "uh", tl: 81.0}, {norm: "have", tl: 81.27}}
	if missing, _, matched := matchByTime(planned, heard); matched != 3 || len(missing) != 0 {
		t.Errorf("missing %v matched %d", missing, matched)
	}
}

func TestWAVRoundTrip(t *testing.T) {
	p := filepath.Join(t.TempDir(), "a.wav")
	in := []int16{0, 1, -1, 32767, -32768}
	if err := writeWAV16(p, 16000, in); err != nil {
		t.Fatal(err)
	}
	a, err := readWAV16(p)
	if err != nil {
		t.Fatal(err)
	}
	if a.sr != 16000 || len(a.pcm) != len(in) {
		t.Fatalf("read %d Hz, %d samples", a.sr, len(a.pcm))
	}
	for i := range in {
		if a.pcm[i] != in[i] {
			t.Errorf("sample %d = %d, want %d", i, a.pcm[i], in[i])
		}
	}
	if got := rebuildAudio(a, []piece{{In: 1.0 / 16000, Out: 3.0 / 16000}}); len(got) != 2 || got[0] != 1 || got[1] != -1 {
		t.Errorf("rebuildAudio = %v", got)
	}
}

func TestMatchByTime(t *testing.T) {
	planned := []tword{{norm: "hello", tl: 1}, {norm: "big", tl: 2}, {norm: "colour", tl: 3}}
	heard := []tword{{norm: "hello", tl: 1.1}, {norm: "color", tl: 3.2}, {norm: "extra", tl: 5}}
	missing, extra, matched := matchByTime(planned, heard)
	if matched != 2 || len(missing) != 1 || missing[0] != 1 || len(extra) != 1 || extra[0] != 2 {
		t.Errorf("missing %v extra %v matched %d", missing, extra, matched)
	}
	sp := spots([]tword{{text: "a"}, {text: "b"}, {text: "c"}, {text: "d"}, {text: "e"}, {text: "f"}}, []int{1, 2, 5}, 2)
	if len(sp) != 1 || sp[0].Text != "b c" || sp[0].Words != 2 {
		t.Errorf("spots = %+v", sp)
	}
}

func TestMergeFindingsRegionsNeedTwoSignals(t *testing.T) {
	frames := []*frameInfo{
		{Src: 10, OCR: []string{"call 555-123-4567 now"}},
		{Src: 12, OCR: []string{"555-123-4567"}},
		{Src: 30, Vision: &visionReply{OtherCreator: true, What: "a TikTok"}},
		{Src: 50, Vision: &visionReply{Document: true, What: "police report"}},
		{Src: 52, Vision: &visionReply{Document: true, What: "police report"}},
	}
	fs := mergeFindings(frames, nil)
	if len(fs) != 3 {
		t.Fatalf("findings = %+v", fs)
	}
	if fs[0].Kind != "Readable phone number" || !near(fs[0].Src0, 9) || !near(fs[0].Src1, 13) || !fs[0].Region || fs[0].Evidence != "phone number: 555-123-4567" {
		t.Errorf("OCR finding = %+v", fs[0])
	}
	if fs[1].Kind != "Another creator's content on screen" || fs[1].Region {
		t.Errorf("one-frame vision finding must not be a region: %+v", fs[1])
	}
	if fs[2].Kind != "A document with names on screen" || !fs[2].Region || !near(fs[2].Src0, 49) || !near(fs[2].Src1, 53) {
		t.Errorf("two-frame vision finding = %+v", fs[2])
	}
}

func TestGridFramesOnGlobalGrid(t *testing.T) {
	got := gridFrames([]span{{0.5, 4.1}, {9.0, 10.0}, {9.5, 10.5}})
	want := []float64{2, 4, 10}
	if len(got) != len(want) {
		t.Fatalf("grid = %v, want %v", got, want)
	}
	for i := range want {
		if !near(got[i], want[i]) {
			t.Errorf("grid = %v, want %v", got, want)
		}
	}
}

func TestBreathSpotsOnlyLoudGapsInsideOnePiece(t *testing.T) {
	ws := []Word{{Start: 0, End: 1.0}, {Start: 1.5, End: 2.0}, {Start: 2.6, End: 3.0}, {Start: 3.1, End: 3.5}}
	au := pcmWith(16000, 2000, 5, span{2.0, 2.6})
	got := breathSpots(ws, []span{{0, 4}}, au, -45)
	if len(got) != 1 || !near(got[0].A, 1.0) || !near(got[0].B, 1.5) {
		t.Errorf("spots = %+v", got)
	}
	if got := breathSpots(ws, []span{{0, 1.2}, {1.3, 4}}, au, -45); len(got) != 0 {
		t.Errorf("a gap becky-cut already cut was reported: %+v", got)
	}
}

func TestBreathMarksSpreadAcrossTheEdit(t *testing.T) {
	lens := []float64{0.4, 0.9, 0.5, 0.6, 0.7, 0.4, 0.5, 1.0, 0.45, 0.8}
	var spots []breath
	for i, l := range lens {
		a := 10 * float64(i)
		spots = append(spots, breath{A: a, B: a + l})
	}
	picks, total := breathMarks(spots)
	want := []float64{10, 30, 40, 70, 90}
	if len(picks) != len(want) || !near(total, 6.25) {
		t.Fatalf("picks %+v total %v", picks, total)
	}
	for i := range want {
		if !near(picks[i].A, want[i]) {
			t.Errorf("pick %d at %v, want %v", i, picks[i].A, want[i])
		}
	}
}

func TestPickMessageBoxIgnoresProgressWindow(t *testing.T) {
	progress := `{"title":"Becky Cut","text":"","class":"WindowsForms10.Window.8.app.0.1","buttons":[]}`
	box := `{"title":"Becky Cut","text":"The cut worked, but rebuilding the clip grouping did not.","class":"#32770","buttons":["OK"]}`
	if got := pickMessageBox([]byte("[" + progress + "]")); got != "" {
		t.Errorf("progress window taken for a message box: %q", got)
	}
	if got := pickMessageBox([]byte("[" + progress + "," + box + "]")); got != "Becky Cut: The cut worked, but rebuilding the clip grouping did not." {
		t.Errorf("message box = %q", got)
	}
}
