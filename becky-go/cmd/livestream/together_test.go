package main

import "testing"

// The 27-livestream, 2026-10-08: "There's no excuse for me to look this way on
// stream" - System One kept it alone (50%), Gemma and Qwen cut it. The vote cuts it.
func TestVoteMajorityAndPosture(t *testing.T) {
	ss := []Sentence{{ID: 0, Start: 10, End: 12}, {ID: 1, Start: 20, End: 22}, {ID: 2, Start: 30, End: 32}}
	all := map[string][]Decision{
		"systemone": {{Said: true}, {Said: true}, {Said: true}},
		"gemma4":    {{Label: "break"}, {Label: "narrative", Topic: 1}, {Label: "narrative", Topic: 1}},
		"qwen3.5":   {{Label: "narrative"}, {Label: "narrative", Topic: 1}, {Label: "chat_reply"}},
	}
	// he looks down at his screen from 27 s on (sentence 2, with its 3 s lead)
	var rows []picFrame
	for t := 0.0; t < 40; t += 0.5 {
		f := picFrame{T: t, Face: []float64{0.5, 0.5, 0.3}, Head: []float64{-5, 0}}
		if t >= 27 {
			f.Head = []float64{-25, 0}
		}
		rows = append(rows, f)
	}
	ds := vote3(ss, all, newPosture(rows), map[int]string{0: "@a: hi"})
	if ds[0].Keep {
		t.Errorf("1 of 3 keep was kept: %+v", ds[0])
	}
	if !ds[1].Keep || ds[1].Posture != "looking at the camera" {
		t.Errorf("3 of 3 keep: %+v", ds[1])
	}
	if ds[2].Keep {
		t.Errorf("a split keep while he reads his screen was kept: %+v (posture %q)", ds[2], ds[2].Posture)
	}
}

func TestPostureLooks(t *testing.T) {
	p := newPosture([]picFrame{
		{Face: []float64{0.5, 0.5, 0.3}, Head: []float64{-20, 0}},
		{Face: []float64{0.5, 0.52, 0.3}, Head: []float64{-21, 0}},
		{Face: []float64{0.5, 0.48, 0.3}, Head: []float64{-19, 0}},
	})
	for _, c := range []struct {
		f    picFrame
		want string
	}{
		{picFrame{Face: []float64{0.5, 0.5, 0.3}, Head: []float64{-22, 0}}, "camera"},
		{picFrame{Face: []float64{0.5, 0.5, 0.3}, Head: []float64{-33, 0}}, "reading"}, // 13 deg down
		{picFrame{Face: []float64{0.5, 0.6, 0.3}}, "reading"},                          // face low
		{picFrame{Body: map[string][]float64{"ls": {0.6, 0.7, 1}}}, "reading"},         // head dropped out
		{picFrame{}, "gone"},
	} {
		if got := p.look(c.f); got != c.want {
			t.Errorf("look(%+v) = %s, want %s", c.f, got, c.want)
		}
	}
}

func TestReadCutWeighsEverySignal(t *testing.T) {
	keepAll := Decision{Votes: []vote{{Keep: true}, {Keep: true}, {Keep: true}}}
	split := Decision{Votes: []vote{{Keep: true}, {Keep: true}, {Keep: false}}}
	r := func(cuts int) []vote {
		vs := []vote{{Keep: true}, {Keep: true}, {Keep: true}}
		for i := 0; i < cuts; i++ {
			vs[i].Keep = false
		}
		return vs
	}
	for _, c := range []struct {
		name    string
		d       Decision
		reads   []vote
		reading bool
		want    bool
	}{
		{"two of three readers (the to-do list lines, 2026-10-08)", keepAll, r(2), false, true},
		{"one reader against three voters", keepAll, r(1), false, false},
		{"all three readers", keepAll, r(3), false, true},
		{"two readers and a voter", split, r(2), false, true},
		{"two readers and his posture", keepAll, r(2), true, true},
		{"one reader, a voter and his posture", split, r(1), true, false},
		{"nobody", split, r(0), true, false},
	} {
		if got := readCut(c.d, c.reads, c.reading); got != c.want {
			t.Errorf("%s: cut = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestKeptLinesMarkCuts(t *testing.T) {
	ss := []Sentence{{ID: 0, Text: "a"}, {ID: 1, Text: "b"}, {ID: 2, Text: "c"}, {ID: 3, Text: "d"}}
	ds := []Decision{{Keep: true}, {Keep: true}, {}, {Keep: true, Posture: "looking at the camera"}}
	ls := keptLines(ss, ds)
	if len(ls) != 3 || ls[2].idx != 3 || !ls[2].gap || ls[1].gap || ls[2].text != "0:00 | looking at the camera | d" || ls[1].text != "0:00 | not measured | b" {
		t.Errorf("kept lines = %+v", ls)
	}
}

func TestFinalPiecesDropWordlessSound(t *testing.T) {
	ws := []Word{{Word: "held", Start: 1.1, End: 1.5}, {Word: "for", Start: 1.6, End: 1.8}}
	rs := []Range{{In: 0, Out: 2}}
	keeps := []span{{0, 0.8}, {1.0, 2}} // 0-0.8 s: him fixing his hair, no words
	ps := finalPieces(rs, keeps, ws, 30)
	if len(ps) != 1 || !near(ps[0].A, 1.0) {
		t.Errorf("pieces = %+v", ps)
	}
}

func TestIsFiller(t *testing.T) {
	for text, want := range map[string]bool{
		"Like": true, "Yeah.": true, "So just you know um": true, "Okay, and": true,
		"No.": false, "What": false, "Like I don't understand this logic": false, "": false,
	} {
		if got := isFiller(text); got != want {
			t.Errorf("isFiller(%q) = %v, want %v", text, got, want)
		}
	}
}

// "I don't know, but cheers" starts at 219.43, a moment before "Maybe they're
// weird." ends: the answer belongs to the line it starts.
func TestLineAtPicksTheLineItStarts(t *testing.T) {
	ss := []Sentence{{Start: 218.0, End: 219.6}, {Start: 219.43, End: 222}}
	if got := lineAt(ss, 219.43); got != 1 {
		t.Errorf("lineAt = %d, want 1", got)
	}
	if got := lineAt(ss, 300); got != -1 {
		t.Errorf("an answer far from any line matched line %d", got)
	}
}

// Face zooms go only where Gemma saw a reaction AND MediaPipe measured it
// (27-livestream: the surprised face at 3:10 yes, the hair fixing at 3:06 no).
func TestFaceZoomNeedsTwoModels(t *testing.T) {
	face := []float64{0.5, 0.4}
	for _, c := range []struct {
		m    moment
		want bool
	}{
		{moment{Keep: true, Face: face, Label: "reaction", Signals: []string{"big facial expression (MediaPipe) x5"}}, true},
		{moment{Keep: true, Face: face, Label: "reaction", Signals: []string{"mouth held open (insightface and MediaPipe) x1"}}, true},
		{moment{Keep: true, Face: face, Label: "reaction", Signals: []string{"big facial expression (MediaPipe) x1"}}, false},
		{moment{Keep: true, Face: face, Label: "acting", Signals: []string{"big facial expression (MediaPipe) x5"}}, false},
		{moment{Keep: true, Face: face, Label: "grooming, looking away, reaction", Signals: []string{"big facial expression (MediaPipe) x4"}}, false},
		{moment{Keep: true, Face: face, Label: "reaction, reaction", Signals: []string{"big facial expression (MediaPipe) x4"}}, true},
		{moment{Keep: true, Label: "reaction", Signals: []string{"big facial expression (MediaPipe) x5"}}, false},
		{moment{Keep: false, Face: face, Label: "reaction", Signals: []string{"big facial expression (MediaPipe) x5"}}, false},
	} {
		if got := c.m.faceZoom(); got != c.want {
			t.Errorf("%s %v: face zoom %v, want %v", c.m.Label, c.m.Signals, got, c.want)
		}
	}
}
