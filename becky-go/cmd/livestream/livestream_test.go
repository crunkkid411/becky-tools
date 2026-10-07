package main

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"becky-go/internal/config"
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
		{ID: 7, Label: "meta", Confidence: 95}, {ID: 8, Label: "meta", Confidence: 95}, {ID: 9, Label: "meta", Confidence: 95},
		{ID: 10, Label: "narrative", Keep: false, Confidence: 55},
	}
	reviews := map[int]Review{
		1: {Model: "qwen3.5", Label: "narrative", Topic: 1, Keep: true, Confidence: 70},
		2: {Model: "qwen3.5", Label: "chat_reply", Keep: false, Confidence: 90},
	}
	out := conclude(ds, reviews, "gemma4")
	if !out[0].Keep || out[0].Unsure || !out[0].Said {
		t.Errorf("sure keep changed: %+v", out[0])
	}
	if !out[1].Keep || !out[1].Unsure || out[1].Said || out[1].Note != "gemma4 said cut (not a wanted topic), qwen3.5 said keep (topic 1)" {
		t.Errorf("disagreement = %+v", out[1])
	}
	if out[2].Keep || out[2].Unsure || out[2].Confidence != 90 || out[2].Review == nil {
		t.Errorf("agreement = %+v", out[2])
	}
	// Rules v2: the model itself said keep, so the stray-fragment rule leaves it
	// ("Yes, so there we go" - and the end of the toast - on the 27-livestream).
	if !out[6].Keep || !out[6].Unsure || out[6].Note != "gemma4 was unsure (50%) whether to keep it" {
		t.Errorf("isolated unsure the model kept = %+v", out[6])
	}
	// ...but a lone unsure sentence the model said to cut is still cut.
	if out[10].Keep || !out[10].Unsure || out[10].Note != "gemma4 was unsure (55%) whether to cut it - cut: nothing confidently kept nearby" {
		t.Errorf("isolated unsure the model cut = %+v", out[10])
	}
	if ds[1].Keep || ds[1].Unsure {
		t.Error("conclude changed its input")
	}
}

// "Oh my yeah, I exist": Claude said chat_reply, cut, 60% - Jordan: "just me
// responding to chat". An unsure chat reply the model cut stays cut, even right
// next to kept sentences; an unsure narrative cut is still kept for him to judge.
func TestConcludeUnsureChatReplyTheModelCutStaysCut(t *testing.T) {
	ds := []Decision{
		{ID: 0, Label: "narrative", Keep: true, Confidence: 90},
		{ID: 1, Label: "chat_reply", Keep: false, Confidence: 60, Note: "could still be part of the topic"},
		{ID: 2, Label: "super_chat", Keep: false, Confidence: 40},
		{ID: 3, Label: "chat_reply", Keep: true, Confidence: 60},
		{ID: 4, Label: "narrative", Keep: false, Confidence: 60},
		{ID: 5, Label: "narrative", Keep: true, Confidence: 90},
	}
	out := conclude(ds, nil, "claude")
	for _, i := range []int{1, 2} {
		if out[i].Keep || !out[i].Unsure || !chatCut(out[i]) || !strings.HasSuffix(out[i].Note, " - cut: a chat reply, as claude said") {
			t.Errorf("unsure chat cut %d = %+v", i, out[i])
		}
	}
	if !out[3].Keep || chatCut(out[3]) {
		t.Errorf("a chat reply the model kept stays kept: %+v", out[3])
	}
	if !out[4].Keep || !out[4].Unsure || chatCut(out[4]) {
		t.Errorf("an unsure narrative cut is kept for Jordan to judge: %+v", out[4])
	}
	if out[1].Note != "could still be part of the topic - cut: a chat reply, as claude said" {
		t.Errorf("the model's own note comes first: %q", out[1].Note)
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

// sedWith is n sound-labeler frames (40 ms each) from set(f).
func sedWith(n int, set func(f int) (br, voice, vocal float64)) *sedFrames {
	s := &sedFrames{breath: make([]float64, n), voice: make([]float64, n), vocal: make([]float64, n),
		vocalK: make([]int, n), have: make([]bool, n), names: []string{"Laughter"}}
	for f := range n {
		s.breath[f], s.voice[f], s.vocal[f] = set(f)
		s.have[f] = true
	}
	return s
}

// A breath is Breathing/Pant 0.25+ with no voice over it, 0.12 s or longer; a
// dip of up to two frames is the same breath; it never reaches past the piece.
func TestBreathEventsNeedBreathWithoutVoice(t *testing.T) {
	s := sedWith(100, func(f int) (float64, float64, float64) {
		switch {
		case f >= 10 && f <= 12: // 0.12 s: just long enough
			return 0.3, 0, 0
		case f >= 20 && f <= 21: // 0.08 s: too short
			return 0.9, 0, 0
		case f >= 30 && f <= 33, f >= 36 && f <= 38: // a two-frame dip: one breath
			return 0.4, 0, 0
		case f >= 50 && f <= 60: // breathing under his voice
			return 0.8, 0.6, 0
		case f >= 70 && f <= 80: // just under the breath score
			return 0.24, 0, 0
		}
		return 0, 0, 0
	})
	got := breathEvents(s, 0, 4)
	if len(got) != 2 || !near(got[0].A, 0.40) || !near(got[0].B, 0.52) || !near(got[1].A, 1.20) || !near(got[1].B, 1.56) {
		t.Fatalf("events = %+v", got)
	}
	if got := breathEvents(s, 1.3, 4); len(got) != 1 || !near(got[0].A, 1.32) {
		t.Errorf("events inside the piece [1.3, 4] = %+v", got)
	}
}

// One breath between his voice (ends 1.5 s) and his next word (2.3 s), as the
// labeler hears it (1.72-2.12 s).
func breathFixture() (*audio, *sedFrames, span) {
	au := pcmWith(16000, 8000, 100, span{1.5, 2.3}) // -12 dBFS voice, -50 dBFS breath
	s := sedWith(100, func(f int) (float64, float64, float64) {
		switch {
		case f <= 37 || f >= 57:
			return 0, 0.9, 0
		case f >= 43 && f <= 52:
			return 0.6, 0, 0.1
		}
		return 0, 0, 0
	})
	return au, s, span{1.72, 2.12}
}

// Jordan: "the end of that region cuts off the beginning of the word in the
// next segment". The region starts after his voice and ends before his next
// word, both edges on the 30 fps grid.
func TestBreathRegionKeepsClearOfHisWords(t *testing.T) {
	au, s, ev := breathFixture()
	lo, hi, ok := breathRegion(au, s, -43, 30, 0, 4, ev)
	if !ok || !near(lo*30, 48) || !near(hi*30, 67) {
		t.Fatalf("region = %v-%v (frames %v-%v), want frames 48-67", lo, hi, lo*30, hi*30)
	}
	if lo < 1.5+afterVoice-1e-9 || hi > 2.3-beforeWord+1e-9 {
		t.Errorf("region %v-%v touches a word (voice ends 1.5, next word 2.3)", lo, hi)
	}
	// No word before the piece ends: the region runs to the piece's edge.
	if lo, hi, ok := breathRegion(au, s, -43, 30, 0, 2.2, ev); !ok || !near(lo*30, 48) || !near(hi*30, 66) {
		t.Errorf("region to the piece end = %v-%v (frames %v-%v), want 48-66", lo, hi, lo*30, hi*30)
	}
}

// The 1.2 s region at 14:12 ran to the cut over 0.5 s of him handling the phone
// (loud, no voice). A region runs to a piece edge through quiet only: a loud
// sound that is not the breath stops it, on either side.
func TestBreathRegionStopsShortOfALoudSoundAtThePieceEdge(t *testing.T) {
	au := pcmWith(16000, 8000, 100, span{1.5, 1.6}, span{1.65, 2.6}, span{2.8, 3.0})
	s := sedWith(100, func(f int) (float64, float64, float64) {
		switch {
		case f <= 37:
			return 0, 0.9, 0 // his voice, ending at 1.5 - before the piece starts
		case f >= 43 && f <= 52:
			return 0.6, 0, 0.1 // the breath, 1.72-2.12
		}
		return 0, 0, 0 // 1.60-1.65 and 2.60-2.80 are loud, but no voice
	})
	lo, hi, ok := breathRegion(au, s, -43, 30, 1.55, 3.0, span{1.72, 2.12})
	if !ok || !near(lo*30, 52) || !near(hi*30, 68) {
		t.Fatalf("region = frames %v-%v, want 52-68 (clear of both loud sounds)", lo*30, hi*30)
	}
}

func TestGridRegionSnapsInwardTakesSliversDropsTiny(t *testing.T) {
	cases := []struct {
		name           string
		lo, hi, a, b   float64
		toEnd, ok      bool
		wantF0, wantF1 float64
	}{
		{"edges round inward", 1.21, 2.49, 0, 4, false, true, 37, 74},
		{"a 2-frame sliver at the piece start goes too", 1.04, 2.0, 1, 3, false, true, 30, 60},
		{"a 2-frame sliver at the piece end goes too", 1.2, 2.95, 1, 3, true, true, 36, 90},
		{"...but not when his next word is in the piece", 1.2, 2.95, 1, 3, false, true, 36, 88},
		{"3 frames is too short", 1.0, 1.12, 0, 3, false, false, 0, 0},
	}
	for _, c := range cases {
		lo, hi, ok := gridRegion(c.lo, c.hi, c.a, c.b, 30, c.toEnd)
		if ok != c.ok || (ok && (!near(lo*30, c.wantF0) || !near(hi*30, c.wantF1))) {
			t.Errorf("%s: got %v %v-%v (frames %v-%v), want frames %v-%v", c.name, ok, lo, hi, lo*30, hi*30, c.wantF0, c.wantF1)
		}
	}
}

// Two breaths close together in one piece become one region, never two that
// overlap; the strongest breath and mouth sound inside are reported.
func TestFindBreathsMergesCloseRegionsAndReportsTheSound(t *testing.T) {
	au := pcmWith(16000, 100, 100) // all quiet: no words anywhere
	s := sedWith(100, func(f int) (float64, float64, float64) {
		switch {
		case f >= 20 && f <= 25:
			return 0.5, 0, 0.35 // a laugh in it
		case f >= 30 && f <= 35:
			return 0.7, 0, 0
		}
		return 0, 0, 0
	})
	got := findBreaths(au, s, -43, 30, []span{{0, 4}})
	if len(got) != 1 || !near(got[0].A, 0) || !near(got[0].B, 4) {
		t.Fatalf("breaths = %+v", got)
	}
	if !near(got[0].Breath, 0.7) || !near(got[0].Vocal, 0.35) || got[0].VocalLabel != "Laughter" {
		t.Errorf("sound = %v / %v %q", got[0].Breath, got[0].Vocal, got[0].VocalLabel)
	}
	// Separate pieces keep separate regions.
	if got := findBreaths(au, s, -43, 30, []span{{0, 1.1}, {1.2, 4}}); len(got) != 2 {
		t.Errorf("one region per piece, got %+v", got)
	}
}

// The verdicts the 27-livestream settled: any one model seeing the raised drink
// or the held-open mouth places no region; hands never do.
func TestPicLookVerdict(t *testing.T) {
	still := picLook{Frames: 5, FaceSeen: 1, FaceSize: 1.05, Shoulders: 0.05}
	with := func(f func(*picLook)) picLook { l := still; f(&l); return l }
	cases := []struct {
		name string
		l    picLook
		want string
	}{
		{"still breath", still, ""},
		{"hands up (arms spread, hands to head) is still a breath", with(func(l *picLook) { l.HandsUp = 5 }), ""},
		{"too few frames", with(func(l *picLook) { l.Frames = 2 }), vUnclear},
		{"the toast", with(func(l *picLook) { l.Raised = 3; l.RaisedBox = picObj{"bottle", 0.48, 0.19, 0.37} }), vHeld},
		{"insightface: mouth held open", with(func(l *picLook) { l.MouthHeld = 6 }), vFace},
		{"MediaPipe: jaw held open", with(func(l *picLook) { l.JawHeld = 3 }), vFace},
		{"jaw open only 2 frames", with(func(l *picLook) { l.JawHeld = 2; l.MouthHeld = 3 }), ""},
		{"face out of view", with(func(l *picLook) { l.FaceSeen = 0.6 }), vMovement},
		{"leaning in", with(func(l *picLook) { l.FaceSize = 1.4 }), vMovement},
		{"shoulders move", with(func(l *picLook) { l.Shoulders = 0.25 }), vMovement},
	}
	for _, c := range cases {
		if got, why := c.l.verdict(); got != c.want || (got != "" && why == "") {
			t.Errorf("%s: verdict = %q (%q), want %q", c.name, got, why, c.want)
		}
	}
	l := with(func(l *picLook) { l.MouthHeld = 6; l.JawHeld = 7 })
	if _, why := l.verdict(); why != "insightface: mouth open 0.6 s; MediaPipe: jaw open 0.7 s" {
		t.Errorf("both face models named: %q", why)
	}

	// With a hand up only a confident breath gets a region (27-livestream values).
	hands := with(func(l *picLook) { l.HandsUp = 3 })
	for _, c := range []struct {
		name string
		l    picLook
		br   float64
		want string
	}{
		{"hands to head, clear breath (he wants it cut)", hands, 0.57, ""},
		{"hands in his hair, faint breath", hands, 0.43, vMovement},
		{"hands down, faint breath", still, 0.29, ""},
		{"the toast still wins over the hands rule", with(func(l *picLook) { l.HandsUp = 4; l.Raised = 1 }), 0.42, vHeld},
	} {
		if got, why := pictureVerdict(c.l, c.br); got != c.want || (got != "" && why == "") {
			t.Errorf("%s: pictureVerdict = %q (%q), want %q", c.name, got, why, c.want)
		}
	}
	if _, why := pictureVerdict(hands, 0.32); why != "MediaPipe: a hand above his shoulders (0.3 s) and only a faint breath (0.32)" {
		t.Errorf("hands rule wording: %q", why)
	}
}

func TestPicturesLookSumsTheFrames(t *testing.T) {
	body := func(y float64) map[string][]float64 {
		return map[string][]float64{"ls": {0.4, y, 0.9}, "rs": {0.6, y, 0.9}, "lw": {0.4, 0.9, 0.9}, "rw": {0.6, y - 0.1, 0.9}}
	}
	p := &pictures{frames: map[int]picFrame{}}
	for i, f := range []picFrame{
		{Face: []float64{0.5, 0.3, 0.20}, Mouth: 0.25, Jaw: 0.7, Body: body(0.50)},
		{Face: []float64{0.5, 0.3, 0.22}, Mouth: 0.30, Jaw: 0.8, Body: body(0.52),
			Objs: []picObj{{"cup", 0.55, 0.05, 0.86}, {"bottle", 0.45, 0.12, 0.33}}},
		{Mouth: 0.40, Jaw: 0.9}, // no face found
		{Face: []float64{0.5, 0.3, 0.24}, Mouth: 0.26, Jaw: 0.65, Body: body(0.50)},
	} {
		f.T = 10 + float64(i)/10
		p.frames[picKey(f.T)] = f
	}
	l := p.look(10, 10.3)
	if l.Frames != 4 || !near(l.FaceSeen, 0.75) || !near(l.FaceSize, 1.2) || !near(l.Shoulders, 0.1) {
		t.Errorf("face/body = %+v", l)
	}
	// insightface's run breaks where no face was found; MediaPipe's does not
	if l.MouthHeld != 2 || l.JawHeld != 4 || !near(l.Mouth, 0.30) || !near(l.Jaw, 0.9) {
		t.Errorf("mouth/jaw = %+v", l)
	}
	if l.Raised != 1 || l.RaisedBox.What != "bottle" || l.HandsUp != 3 {
		t.Errorf("raised/hands = %+v", l)
	}
}

func TestMissingKeysAreRunsOfUnmeasuredFrames(t *testing.T) {
	have := map[int]picFrame{100: {}, 101: {}, 103: {}}
	got := missingKeys(have, []span{{10.0, 10.5}, {10.4, 10.6}})
	if want := [][2]int{{102, 102}, {104, 106}}; !slices.Equal(got, want) {
		t.Errorf("missing = %v, want %v", got, want)
	}
}

func TestBreathRegionsOnlyForBreathsOnTheTimeline(t *testing.T) {
	ps := []piece{{In: 10, Out: 20, TL: 0}, {In: 30, Out: 40, TL: 10}}
	ms := breathRegions([]breath{
		{A: 12, B: 12.8, Verdict: vBreath},
		{A: 15, B: 16, Verdict: vFace},
		{A: 31.5, B: 32, Verdict: vBreath},
	}, ps)
	if len(ms) != 2 || !near(ms[0].At, 2) || !near(ms[0].Len, 0.8) || !near(ms[1].At, 11.5) || !near(ms[1].Len, 0.5) {
		t.Fatalf("regions = %+v", ms)
	}
	if ms[1].Label != "Breath check 2 of 2 (0.5 s)" {
		t.Errorf("label = %q", ms[1].Label)
	}
}

func TestBreathSummaryCountsEveryVerdict(t *testing.T) {
	spots := []breath{
		{A: 60, B: 60.8, Verdict: vBreath, Breath: 0.6},
		{A: 70, B: 70.5, Verdict: vBreath, Breath: 0.4},
		{A: 80, B: 81, Verdict: vMovement, Why: "his shoulders move 0.30 shoulder widths"},
		{A: 90, B: 90.4, Verdict: vFace, Why: "MediaPipe: jaw open 0.6 s"},
		{A: 95, B: 95.4, Verdict: vSound, Why: "Laughter 0.40"},
		{A: 99, B: 99.5, Verdict: vHeld, Why: "Falcon: a bottle"},
	}
	line, details := breathSummary(spots, "", []string{"Gemma did not run: x"})
	if want := "- **Breath check:** 2 breath region(s), 1.3 s, every edge on a frame; nothing was cut. Heard but left alone: 1 movement, 1 facial expression, 1 holding something up, 1 laugh or cough, 0 not checked. (Gemma did not run: x)"; line != want {
		t.Errorf("line = %q\nwant   %q", line, want)
	}
	if len(details) != 6 || details[0] != "- stream 1:00: 0.80 s - breath (breathing 0.60; picture still)" ||
		details[3] != "- stream 1:30: 0.40 s - facial expression (MediaPipe: jaw open 0.6 s)" {
		t.Errorf("details = %q", details)
	}
	if line, details := breathSummary(spots, "the sound check did not run: x", nil); !strings.Contains(line, "skipped - the sound check did not run: x") || details != nil {
		t.Errorf("skipped line = %q, details %q", line, details)
	}
}

// Without the sound labeler nothing may be called a breath - never back to
// loudness alone, which is what put the wrong markers on his timeline.
func TestRunBreathCheckPlacesNothingWithoutTheLabeler(t *testing.T) {
	cfg := config.Config{SoundLabelPython: filepath.Join(t.TempDir(), "no-python.exe")}
	res := runBreathCheck(cfg, "x.mp4", t.TempDir(), "x", "x.wav", nil, -43, 30, []span{{1, 2}}, func(string, ...any) {})
	if res.Note == "" || len(res.Spots) != 0 || len(breathRegions(res.Spots, []piece{{In: 0, Out: 9}})) != 0 {
		t.Errorf("result = %+v", res)
	}
}

// The stretches the edit cuts next to his words: a pause inside a section, the
// 2.5 s after one (thumbs up after "still allowed to livestream"), the 2 s
// before one - never past a word the content decision left out.
func TestMomentCandidates(t *testing.T) {
	words := []Word{{Word: "a", Start: 0.5, End: 1}, {Word: "b", Start: 1.2, End: 2}, {Word: "cut", Start: 3, End: 3.5}, {Word: "c", Start: 10.2, End: 11}, {Word: "d", Start: 11.5, End: 12}}
	ranges := []Range{{In: 0.4, Out: 2.1, W0: 0, W1: 1}, {In: 10.1, Out: 12.1, W0: 3, W1: 4}}
	pieces := []span{{0.4, 1.05}, {1.15, 2.1}, {10.1, 12.1}}
	got := momentCandidates(ranges, pieces, words)
	want := []moment{{A: 0, B: 0.4, Kind: "before"}, {A: 1.05, B: 1.15, Kind: "pause"}, {A: 2.1, B: 2.96, Kind: "after"},
		{A: 8.1, B: 10.1, Kind: "before"}, {A: 12.1, B: 14.6, Kind: "after"}}
	if len(got) != len(want) {
		t.Fatalf("got %+v", got)
	}
	for i := range want {
		if got[i].Kind != want[i].Kind || !near(got[i].A, want[i].A) || !near(got[i].B, want[i].B) {
			t.Errorf("candidate %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

// What the small models see: the wave (fast movement + an open palm) wakes
// Gemma; one twitch alone does not; a gesture alone does.
func TestMomentSignals(t *testing.T) {
	body := func(x float64) map[string][]float64 {
		return map[string][]float64{"n": {0.5, 0.3, 1}, "ls": {0.3, 0.5, 1}, "rs": {0.7, 0.5, 1}, "lw": {x, 0.8, 1}, "rw": {0.7, 0.8, 1}}
	}
	head := []float64{-8, 0}
	p := &pictures{frames: map[int]picFrame{}}
	for k, x := range []float64{0.3, 0.3, 0.5, 0.3, 0.3} { // 0.2 / 0.4 shoulder widths = 0.5 each
		p.frames[100+k] = picFrame{T: float64(100+k) / 10, Body: body(x), Head: head}
	}
	f := p.frames[103]
	f.Gest = [][]any{{"Open_Palm", 0.57}}
	p.frames[103] = f
	got := momentSignals(p, 10.0, 10.4)
	if len(got) != 2 || got[0] != "fast movement (MediaPipe pose) x2" || got[1] != "Open Palm (MediaPipe gesture) x1" {
		t.Errorf("wave signals = %q", got)
	}
	q := &pictures{frames: map[int]picFrame{}}
	for k, x := range []float64{0.3, 0.5, 0.5} {
		q.frames[200+k] = picFrame{Body: body(x), Head: head}
	}
	if got := momentSignals(q, 20.0, 20.2); got != nil {
		t.Errorf("one twitch woke Gemma: %q", got)
	}
	f = q.frames[202]
	f.Gest = [][]any{{"Thumb_Up", 0.61}}
	q.frames[202] = f
	if got := momentSignals(q, 20.0, 20.2); len(got) != 2 {
		t.Errorf("a thumbs up must wake Gemma: %q", got)
	}
}

// What goes back: Gemma's span padded and on the grid, inside the stretch,
// slivers absorbed; the 27-livestream wave inside becky-cut's 896.27-896.9.
func TestPutBack(t *testing.T) {
	lo, hi, ok := putBack(896.2667, 896.9, 896.2, 896.8, 30)
	if !ok || !near(lo, 896.2667) || !near(hi, 896.9) {
		t.Errorf("wave = %.4f-%.4f %v (whole stretch: what is left is a sliver)", lo, hi, ok)
	}
	lo, hi, ok = putBack(202.2667, 204.7667, 202.2, 203.0, 30)
	if !ok || !near(lo, 202.2667) || !near(hi, 203.1) {
		t.Errorf("thumbs up = %.4f-%.4f %v", lo, hi, ok)
	}
	if _, _, ok := putBack(10, 10.1, 10, 10.1, 30); ok {
		t.Error("3 frames is too short to put back")
	}
	got := addPieces([]span{{0, 2}, {3, 5}}, []span{{2, 2.5}, {6, 7}})
	if !slices.Equal(got, []span{{0, 2.5}, {3, 5}, {6, 7}}) {
		t.Errorf("addPieces = %+v", got)
	}
}

// Every checked breath is cut; unchecked ones stay.
func TestCutBreaths(t *testing.T) {
	got := cutBreaths([]span{{0, 5}, {6, 9}}, []breath{{A: 1, B: 1.5, Verdict: vBreath}, {A: 4.5, B: 5, Verdict: vBreath},
		{A: 7, B: 7.5, Verdict: vMovement}})
	if !slices.Equal(got, []span{{0, 1}, {1.5, 4.5}, {6, 9}}) {
		t.Errorf("cutBreaths = %+v", got)
	}
}

// Every position goes to VEGAS in whole frames of the timeline.
func TestMarkJobIsInFrames(t *testing.T) {
	got := markJob([]mark{
		{At: 1.6, Len: 2.2333 - 1.6, Label: "Breath check 1 of 2 (0.6 s)"},
		{At: 2.0, Label: "a\tb\nc"},
		{At: 3.0, Len: 0.001, Label: "tiny"},
	}, 30)
	want := "region\t48\t19\tBreath check 1 of 2 (0.6 s)\nmarker\t60\ta b c\nregion\t90\t1\ttiny\n"
	if got != want {
		t.Errorf("job =\n%q\nwant\n%q", got, want)
	}
}

func TestCutPointLandsInsideOrAtTheNextPiece(t *testing.T) {
	ps := []piece{{In: 30, Out: 40, TL: 10}, {In: 10, Out: 20, TL: 0}}
	for _, c := range []struct {
		t, want float64
		ok      bool
	}{{15, 5, true}, {25, 10, true}, {5, 0, true}, {45, 0, false}} {
		if got, ok := cutPoint(ps, c.t); ok != c.ok || !near(got, c.want) {
			t.Errorf("cutPoint(%v) = %v %v, want %v %v", c.t, got, ok, c.want, c.ok)
		}
	}
}

// A second version of the project never overwrites the first one's report.
func TestReportPathFollowsTheProjectName(t *testing.T) {
	dir := filepath.Join("X", "27-livestream")
	media := filepath.Join(dir, "v.mp4")
	work := filepath.Join(dir, "becky-edit")
	for veg, want := range map[string]string{
		filepath.Join(dir, "27-livestream-claude (2).veg"): "report-claude (2).md",
		filepath.Join(dir, "27-livestream-claude.veg"):     "report-claude.md",
		"": "report-claude.md",
	} {
		if got := reportPath(work, "claude", media, veg); got != filepath.Join(work, want) {
			t.Errorf("reportPath(%q) = %q, want %q", veg, got, want)
		}
	}
}

// --breaths-only rewrites the breath lines of a finished report (the 10-05
// format) and nothing else; a second run over its own output changes nothing.
func TestRewriteBreathReportSwapsOnlyTheBreathLines(t *testing.T) {
	old := "# Livestream edit - X\n\n## On the timeline for you to look at (12)\n\n" +
		"- **Loud cuts:** 1 marker(s) where a cut sits inside speech\n" +
		"- **Breath examples:** 5 marker(s); nothing was cut. In this edit, wordless loud gaps add up to 41 s.\n" +
		"- **Planned words not heard:** 0 region(s)\n\n## Details\n\n### Loud cuts\n\n- x\n\n" +
		"### Breath examples\n\n- stream 3:21: 0.9 s, -30 dB\n- stream 12:13: 1.0 s, -33 dB\n\n## Time\n\ntotal 5m\n"
	details := []string{"- stream 14:07: 1.2 s - breath (breathing 0.52, picture still 1.1x)"}
	md, err := rewriteBreathReport(old, "- **Breath check:** NEW", details, 5, 2)
	want := "# Livestream edit - X\n\n## On the timeline for you to look at (9)\n\n" +
		"- **Loud cuts:** 1 marker(s) where a cut sits inside speech\n" +
		"- **Breath check:** NEW\n" +
		"- **Planned words not heard:** 0 region(s)\n\n## Details\n\n### Loud cuts\n\n- x\n\n" +
		"### Breath check\n\n- stream 14:07: 1.2 s - breath (breathing 0.52, picture still 1.1x)\n\n## Time\n\ntotal 5m\n"
	if err != nil || md != want {
		t.Fatalf("err %v\ngot:\n%s\nwant:\n%s", err, md, want)
	}
	if again, err := rewriteBreathReport(md, "- **Breath check:** NEW", details, 2, 2); err != nil || again != md {
		t.Errorf("a re-run changed the report: err %v\n%s", err, again)
	}
	if _, err := rewriteBreathReport("# no breath here\n", "x", nil, 0, 0); err == nil {
		t.Error("a report without breath lines must be refused, not guessed at")
	}
}

func TestLastJSONLineSkipsLogLines(t *testing.T) {
	if got := lastJSONLine("Loading pretrained checkpoint\n{\"ok\": true}\n"); got != `{"ok": true}` {
		t.Errorf("got %q", got)
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

// Gemma writes times as numbers, strings or "185.2s".
func TestSecondsParse(t *testing.T) {
	var v struct{ A, B, C, D seconds }
	if err := json.Unmarshal([]byte(`{"A": 185.2, "B": "185.2", "C": "185.2s", "D": "soon"}`), &v); err != nil {
		t.Fatal(err)
	}
	if v.A != 185.2 || v.B != 185.2 || v.C != 185.2 || v.D != 0 {
		t.Errorf("parsed %+v", v)
	}
}

// The thumbs up and THEN the water bottle: only the thumbs up goes back.
func TestJudgeActions(t *testing.T) {
	text := "[202.3s] head level, thumbs up\n[203.4s] reaching for bottle\n" +
		`{"actions": [{"label": "Gesture", "what": "thumbs up", "fits_line": true, "from": "202.2s", "to": 203.0}, {"label": "object", "what": "reaches for water", "fits_line": false, "from": 203.0, "to": 204.2}]}`
	acts, err := parseActions(text, 200.77, 204.77)
	if err != nil || len(acts) != 2 {
		t.Fatalf("parse: %v %+v", err, acts)
	}
	c := moment{A: 202.2667, B: 204.2667}
	judgeActions(&c, acts, 30)
	if !c.Keep || len(c.Back) != 1 || !near(c.Back[0].A, 202.2667) || !near(c.Back[0].B, 203.1) ||
		c.Label != "gesture, object" || c.Gemma != "thumbs up; then reaches for water" {
		t.Errorf("judged %+v", c)
	}
	// a "gesture" that has nothing to do with his words (hands to his hair) stays cut
	c = moment{A: 185.15, B: 186.87}
	judgeActions(&c, []action{{Label: "gesture", What: "hands near his head", From: 185.2, To: 185.9}}, 30)
	if c.Keep {
		t.Errorf("an action that does not fit the line was put back: %+v", c)
	}
	// a kept action Gemma could not place, next to other actions: none of it goes back
	c = moment{A: 185.15, B: 186.87}
	judgeActions(&c, []action{{Label: "gesture", Fits: true}, {Label: "grooming"}}, 30)
	if c.Keep {
		t.Errorf("an unplaced action took the whole stretch back: %+v", c)
	}
	if _, err := parseActions("no json here", 0, 1); err == nil {
		t.Error("an answer without actions must be an error")
	}
}
