package main

import (
	"testing"

	"becky-go/internal/faceembed"
)

func TestClassifyDualModel(t *testing.T) {
	cases := []struct {
		name string
		r    faceRead
		want faceVerdict
	}{
		{"both agree and clear", faceRead{arcName: "J", lvName: "J", arc: 0.70, lv: 0.55, lvMargin: 0.40, hasLV: true}, verdictYes},
		{"models name different people", faceRead{arcName: "J", lvName: "C", arc: 0.70, lv: 0.55, lvMargin: 0.40, hasLV: true}, verdictGray},
		{"ArcFace sure, LVFace under its bar", faceRead{arcName: "J", lvName: "J", arc: 0.70, lv: 0.20, lvMargin: 0.15, hasLV: true}, verdictGray},
		{"LVFace too close to its runner-up", faceRead{arcName: "J", lvName: "J", arc: 0.70, lv: 0.45, lvMargin: 0.05, hasLV: true}, verdictGray},
		{"ArcFace under the name bar", faceRead{arcName: "J", lvName: "J", arc: 0.50, lv: 0.45, lvMargin: 0.30, hasLV: true}, verdictGray},
		{"both far below", faceRead{arcName: "J", lvName: "C", arc: 0.30, lv: 0.08, lvMargin: 0.02, hasLV: true}, verdictNo},
		{"ArcFace low but LVFace sees someone", faceRead{arcName: "J", lvName: "J", arc: 0.30, lv: 0.25, lvMargin: 0.2, hasLV: true}, verdictGray},
		{"ArcFace only, clear", faceRead{arcName: "J", arc: 0.60}, verdictYes},
		{"ArcFace only, gray", faceRead{arcName: "J", arc: 0.45}, verdictGray},
		{"ArcFace only, nobody", faceRead{arcName: "J", arc: 0.20}, verdictNo},
	}
	for _, c := range cases {
		if got := classify(c.r, 0.55); got != c.want {
			t.Errorf("%s: got %d want %d", c.name, got, c.want)
		}
	}
}

func TestReadScoresBothModels(t *testing.T) {
	enrolled := []enrolledFace{
		{name: "J", vector: []float64{1, 0}, vector2: []float64{1, 0, 0}},
		{name: "C", vector: []float64{0, 1}, vector2: []float64{0, 1, 0}},
	}
	r := read(faceembed.Face{Vector: []float64{0.8, 0.6}, Vector2: []float64{0.6, 0.8, 0}}, enrolled)
	if r.arcName != "J" || r.lvName != "C" || !r.hasLV {
		t.Fatalf("got %+v", r)
	}
	if r.arc < 0.799 || r.arc > 0.801 || r.lv < 0.799 || r.lv > 0.801 || r.lvMargin < 0.199 || r.lvMargin > 0.201 {
		t.Fatalf("scores %+v", r)
	}
	if r := read(faceembed.Face{Vector: []float64{1, 0}}, enrolled); r.hasLV || r.lv != 0 {
		t.Fatalf("no second vector must mean no LVFace read: %+v", r)
	}
}

func TestGemmaWord(t *testing.T) {
	for in, want := range map[string]string{"Yes.": "yes", "no": "no", " **No** they differ": "no", "Unclear": "unclear", "": "unclear", "maybe yes": "unclear"} {
		if got := gemmaWord(in); got != want {
			t.Errorf("%q: got %q want %q", in, got, want)
		}
	}
}

func TestVoiceHintStaysUnderSoloFloor(t *testing.T) {
	e := &escalator{}
	id := e.voiceHint(grayFace{name: "J", read: faceRead{arc: 0.91}, ts: 2}, 30)
	if id.Confidence >= faceSoloFloor || id.Match != "cosine (needs voice)" || id.Frames[0].Frame != 60 {
		t.Fatalf("got %+v", id)
	}
}

func TestSecondOpinion(t *testing.T) {
	camEnrolled := []enrolledVoice{{name: "J", embedding: []float64{1, 0}}, {name: "C", embedding: []float64{0, 1}}}
	cam := []speakerAudio{
		{id: "s1", embedding: []float64{1, 0}},       // CAM++ sure it is J
		{id: "s2", embedding: []float64{0, 1}},       // CAM++ sure it is C
		{id: "s3", embedding: []float64{0.71, 0.70}}, // CAM++ torn: no vote
	}
	ids := []Identification{{SpeakerID: "s1", Name: "J"}, {SpeakerID: "s2", Name: "J"}, {SpeakerID: "s3", Name: "C"}}
	opts := voiceOptions{threshold: 0.45, nameThreshold: 0.75, nameMargin: 0.06}
	got := secondOpinion(ids, cam, camEnrolled, opts)
	if got[0].Match != "redimnet+campp" || got[1].Match != "redimnet-disputed (CAM++ heard C at 1.00)" || got[2].Match != "redimnet" {
		t.Fatalf("got %q / %q / %q", got[0].Match, got[1].Match, got[2].Match)
	}
}

func TestFusionHonoursVoiceModels(t *testing.T) {
	cases := []struct {
		match string
		conf  float64
		named bool
	}{
		{"redimnet+campp", 0.66, true},                            // ReDimNet scale: 0.66 is a sure same-person
		{"redimnet", 0.55, true},                                  // CAM++ had no opinion
		{"redimnet-disputed (CAM++ heard C at 0.80)", 0.9, false}, // never alone
		{"cosine", 0.70, false},                                   // old CAM++ floor 0.75 still applies
		{"cosine", 0.80, true},
	}
	for _, c := range cases {
		ids, _ := fuseIdentifications([]Identification{{Type: "voice", Name: "J", Confidence: c.conf, Match: c.match, SpeakerID: "s1"}}, nil)
		if (len(ids) == 1) != c.named {
			t.Errorf("%s %.2f: named=%v want %v", c.match, c.conf, len(ids) == 1, c.named)
		}
	}
	// a disputed voice plus a face hint for the same person is corroborated
	ids, _ := fuseIdentifications([]Identification{
		{Type: "voice", Name: "J", Confidence: 0.6, Match: "redimnet-disputed (CAM++ heard C at 0.80)", SpeakerID: "s1"},
		{Type: "face", Name: "J", Confidence: 0.6, Match: "cosine (needs voice)"},
	}, nil)
	if len(ids) != 1 || ids[0].Type != "corroborated" {
		t.Fatalf("got %+v", ids)
	}
}

func TestFuseEnrolledKeepsSecondModel(t *testing.T) {
	recs := []faceembed.Face{
		{Path: "a.jpg", Found: true, Vector: []float64{1, 0}, Vector2: []float64{0, 1}},
		{Path: "b.jpg", Found: true, Vector: []float64{1, 0}, Vector2: []float64{0, 1}},
	}
	got := fuseEnrolled(recs, map[string]string{"a.jpg": "J", "b.jpg": "J"})
	if len(got) != 1 || len(got[0].vector2) != 2 || got[0].vector2[1] < 0.999 {
		t.Fatalf("got %+v", got)
	}
}

func TestFusionHonoursFaceModels(t *testing.T) {
	cases := []struct {
		match string
		conf  float64
		named bool
	}{
		{"cosine+lvface", 0.62, true}, // both face models agreed
		{"cosine+gemma", 0.52, true},  // Gemma confirmed a gray face
		{"cosine+web", 0.51, true},    // same-face pages online name them
		{"cosine+jordan", 0.51, true}, // Jordan answered the who-is-this window
		{"cosine", 0.70, false},       // ArcFace alone: lookalike floor 0.78
		{"cosine", 0.80, true},
		{"cosine (needs voice)", 0.77, false}, // a hint never stands alone
	}
	for _, c := range cases {
		ids, _ := fuseIdentifications([]Identification{{Type: "face", Name: "J", Confidence: c.conf, Match: c.match}}, nil)
		if (len(ids) == 1) != c.named {
			t.Errorf("%s %.2f: named=%v want %v", c.match, c.conf, len(ids) == 1, c.named)
		}
		if len(ids) == 1 && ids[0].Match != c.match {
			t.Errorf("%s: final match %q", c.match, ids[0].Match)
		}
	}
}

// The face says yes and the whole person does not object -> yes; any face "no" -> no;
// a face yes that the whole person contradicts (tattoos, sex, build) -> unclear.
func TestGemmaVerdict(t *testing.T) {
	cases := []struct{ face, person, want string }{
		{"yes", "yes", "yes"}, {"yes", "unclear", "yes"}, {"yes", "no", "unclear"},
		{"no", "yes", "no"}, {"unclear", "yes", "unclear"}, {"unclear", "unclear", "unclear"},
	}
	for _, c := range cases {
		if got := gemmaVerdict(c.face, c.person); got != c.want {
			t.Errorf("gemmaVerdict(%s,%s) = %s, want %s", c.face, c.person, got, c.want)
		}
	}
	if got := personWord("face: same\nhair: different\nsame person: **No**."); got != "no" {
		t.Errorf("personWord = %s", got)
	}
	if got := personWord("face: same"); got != "unclear" {
		t.Errorf("personWord without a verdict line = %s", got)
	}
}

// Reverse search names the candidate only when more same-face pages name them than anyone else.
func TestReadRevSearch(t *testing.T) {
	o := revOut{NamesSeen: map[string]int{"Hair Jordan": 15}, Summary: "s"}
	if a, _, _ := readRevSearch(o, "Hair Jordan"); a != "yes" {
		t.Errorf("15 pages naming the candidate: %s", a)
	}
	if a, who, _ := readRevSearch(o, "Shelby"); a != "no" || who != "Hair Jordan" {
		t.Errorf("pages naming someone else: %s %s", a, who)
	}
	if a, _, _ := readRevSearch(revOut{NamesSeen: map[string]int{}}, "Shelby"); a != "unclear" {
		t.Errorf("no names: %s", a)
	}
	if a, _, _ := readRevSearch(revOut{NamesSeen: map[string]int{"A": 2, "B": 2}}, "A"); a != "unclear" {
		t.Errorf("a tie must not name anyone: %s", a)
	}
}

// A scrub view of a different face (a cut, someone stepping in) never stands in for the one we follow.
func TestSameFaceViewsDropsOtherPeople(t *testing.T) {
	res := faceembed.ScrubResult{
		Views: []faceembed.Face{{Vector: []float64{0, 1}}, {Vector: []float64{0.8, 0.6}}},
		Times: []float64{3.0, 2.1},
	}
	got := sameFaceViews(res, []float64{1, 0})
	if len(got.Views) != 1 || got.Times[0] != 2.1 {
		t.Fatalf("kept %+v", got)
	}
}
