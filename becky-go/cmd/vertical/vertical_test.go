package main

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"becky-go/internal/vegasctl"
)

func near(a, b float64) bool { return math.Abs(a-b) < 1e-3 }

func face(cx, cy, w float64) box { return box{cx - w/2, cy - w*0.65, w, w * 1.3} }

func TestLayoutIsSingleUnlessThePictureSaysOtherwise(t *testing.T) {
	one := []sample{{0, []box{face(0.5, 0.4, 0.15)}}, {1, []box{face(0.52, 0.4, 0.15)}}}
	two := []sample{{0, []box{face(0.3, 0.4, 0.12), face(0.7, 0.4, 0.12)}}, {1, []box{face(0.3, 0.4, 0.12), face(0.7, 0.42, 0.12)}}, {2, []box{face(0.3, 0.4, 0.12)}}}
	cam := []sample{{0, []box{face(0.88, 0.8, 0.07)}}, {1, []box{face(0.88, 0.8, 0.07)}}}
	for name, c := range map[string]struct {
		ss   []sample
		want string
	}{"one person": {one, single}, "two side by side": {two, split}, "webcam corner": {cam, screencast}, "nobody": {nil, single}} {
		if got, _ := chooseLayout(c.ss); got != c.want {
			t.Errorf("%s: got %s, want %s", name, got, c.want)
		}
	}
}

// Match Output Aspect on 1920x1080: a 607.5 px wide crop of the full height
// (Jordan's own September vertical: 656.3-1263.8 when centred).
func TestSingleCropIsMatchOutputAspectAimedAtTheFace(t *testing.T) {
	ps := placesFor(single, nil, 16.0/9)
	if len(ps) != 1 || !near(ps[0].X0*1920, 656.25) || !near(ps[0].X1*1920, 1263.75) || ps[0].Y0 != 0 || ps[0].Y1 != 1 {
		t.Fatalf("centred crop = %+v", ps)
	}
	ps = placesFor(single, []sample{{0, []box{face(0.3, 0.4, 0.1)}}}, 16.0/9)
	if !near((ps[0].X0+ps[0].X1)/2, 0.3) {
		t.Errorf("crop centre %.3f, want the face at 0.3", (ps[0].X0+ps[0].X1)/2)
	}
	ps = placesFor(single, []sample{{0, []box{face(0.02, 0.4, 0.1)}}}, 16.0/9)
	if ps[0].X0 != 0 {
		t.Errorf("a face at the edge must not pull the crop off the picture: %+v", ps[0])
	}
}

// SPLIT: each half shows a 1215x1080 region (openshorts) - left person on top.
func TestSplitStacksTheLeftPersonOnTop(t *testing.T) {
	ps := placesFor(split, []sample{{0, []box{face(0.75, 0.4, 0.1), face(0.25, 0.4, 0.1)}}}, 16.0/9)
	if len(ps) != 2 || ps[0].Layer != 1 || ps[0].Top != 0 || ps[0].Bottom != 0.5 || ps[1].Bottom != 1 {
		t.Fatalf("split = %+v", ps)
	}
	if !near((ps[0].X1-ps[0].X0)*1920, 1215) || ps[0].X0 > ps[1].X0 {
		t.Errorf("top half %+v, bottom half %+v", ps[0], ps[1])
	}
}

// SCREENCAST: the whole picture on top at full width (608 of 1920 px), the face below.
func TestScreencastKeepsTheWholeScreenOnTop(t *testing.T) {
	ps := placesFor(screencast, []sample{{0, []box{face(0.88, 0.8, 0.07)}}}, 16.0/9)
	if len(ps) != 2 || ps[0].X0 != 0 || ps[0].X1 != 1 || !near(ps[0].Bottom*1920, 607.5) || ps[1].Top != ps[0].Bottom {
		t.Fatalf("screencast = %+v", ps)
	}
	if ps[1].X1 != 1 { // the face is at the right edge: the crop stops at the edge
		t.Errorf("face band %+v", ps[1])
	}
}

func TestFaceSeenThroughTheCrop(t *testing.T) {
	ps := placesFor(single, []sample{{0, []box{face(0.5, 0.4, 0.1)}}}, 16.0/9)
	o, ok := toOutput(face(0.5, 0.4, 0.1), ps)
	if !ok || !near(o.cx(), 0.5) || !near(o.cy(), 0.4) || !near(o.W, 0.1/(ps[0].X1-ps[0].X0)) {
		t.Fatalf("face in the vertical frame = %+v %v", o, ok)
	}
	if _, ok := toOutput(face(0.05, 0.4, 0.04), ps); ok {
		t.Error("a face outside the crop is not in the picture")
	}
}

// Jordan, 2026-10-14: the first verticals came from his RAW project (OBS sound
// not synced, silences in, his instructions to the editor not followed). Shorts
// come only from the edited video the YouTube edit recorded.
func TestShortsOnlyFromTheEditedVideo(t *testing.T) {
	dir := t.TempDir()
	raw, edited := filepath.Join(dir, "goodbye-youtube-update.veg"), filepath.Join(dir, "goodbye-youtube-update-video.veg")
	for _, p := range []string{raw, edited} {
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := editedProject(raw); err == nil {
		t.Fatal("no edit recorded yet: the raw project must be refused")
	}
	if err := os.MkdirAll(filepath.Join(dir, "becky-edit"), 0o755); err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(map[string]string{"path": edited})
	if err := os.WriteFile(filepath.Join(dir, "becky-edit", "project-stamp.json"), b, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := editedProject(raw); err == nil || !strings.Contains(err.Error(), "goodbye-youtube-update-video.veg") {
		t.Errorf("the raw project must be refused and the edited one named, got %v", err)
	}
	for _, arg := range []string{edited, dir} {
		if got, err := editedProject(arg); err != nil || got != edited {
			t.Errorf("editedProject(%s) = %s, %v; want the edited project", arg, got, err)
		}
	}
	if got := verticalName(edited); got != filepath.Join(dir, "goodbye-youtube-update-VERTICAL.veg") {
		t.Errorf("vertical name = %s", got)
	}
}

func TestCaptionsMoveDownOnlyWhenAHeadCoversThem(t *testing.T) {
	high, low := headBox(box{0.3, 0.15, 0.4, 0.3}), headBox(box{0.3, 0.45, 0.4, 0.3})
	if y := captionY(single, []box{high}, 1); y != 0.60 {
		t.Errorf("his usual caption line is free, got %.2f", y)
	}
	if y := captionY(single, []box{low}, 1); y != 0.79 {
		t.Errorf("a head over the caption line moves them down, got %.2f", y)
	}
	if y := captionY(single, []box{high, high, high, high, low}, 5); y != 0.60 {
		t.Errorf("one lean toward the camera is not enough to move them, got %.2f", y)
	}
	if captionY(split, nil, 0) != 0.50 {
		t.Error("split captions go on the seam")
	}
}

// The goodbye update at 0:50: he leans right for several seconds and the fixed
// crop loses half his face. A hard cut to a new framing follows him; a single
// stray sample does not.
func TestCropReaimsWhenHeMovesAndHolds(t *testing.T) {
	var ss []sample
	for i := range 20 {
		cx := 0.5
		if i >= 8 {
			cx = 0.68 // leaned right and stayed there
		}
		if i == 4 {
			cx = 0.7 // one stray sample
		}
		ss = append(ss, sample{float64(i), []box{face(cx, 0.4, 0.1)}})
	}
	spans, groups := reaim(ss, 0, 20, 16.0/9)
	if len(spans) != 2 || spans[1][0] != 8 || spans[1][1] != 20 || len(groups[1]) != 12 {
		t.Fatalf("spans = %v", spans)
	}
	if ps := placesFor(single, groups[1], 16.0/9); !near((ps[0].X0+ps[0].X1)/2, 0.68) {
		t.Errorf("the new framing aims at him: %+v", ps[0])
	}
	if spans, _ := reaim(ss[:8], 0, 8, 16.0/9); len(spans) != 1 {
		t.Errorf("standing still is one framing, got %v", spans)
	}
}

func TestBeatsAreLoudAndFarApart(t *testing.T) {
	env := make([]float64, 300) // 60 s
	for i := range env {
		env[i] = 0.1
	}
	env[10], env[50], env[150], env[290] = 1, 1, 0.9, 1 // 2 s, 10 s (too close), 30 s, 58 s (too late)
	got := beats(env, 60)
	if len(got) != 2 || !near(got[0], 2) || !near(got[1], 30) {
		t.Fatalf("beats = %v, want [2 30]", got)
	}
	if beats(make([]float64, 100), 20) != nil {
		t.Error("silence has no beats")
	}
}

// The OBS recording's sound sits 5 frames after its picture: words follow the sound.
func TestWordsFollowTheAudioEvent(t *testing.T) {
	evs := []vegasctl.Event{
		{Kind: "video", Source: "a.mp4", In: 0, Out: 100, Timeline: 0, TimelineEnd: 100},
		{Kind: "audio", Source: "a.mp4", In: 0.002, Out: 99.9, Timeline: 0.1667, TimelineEnd: 100, Track: 1},
		{Kind: "audio", Source: "a.mp4", In: 0.002, Out: 99.9, Timeline: 0.1667, TimelineEnd: 100, Track: 2},
	}
	ws := timelineWords(evs, func(string) []word { return []word{{"hi", 1, 1.2}, {"there", 1.3, 1.5}} })
	if len(ws) != 2 || !near(ws[0].Start, 1.1647) {
		t.Fatalf("words = %+v", ws)
	}
}

// The model picks; code keeps the limits: never in the first second or the last
// two, 8 s apart (the model's first choice wins a clash), at most one per 12 s.
func TestEffectLimitsAreCode(t *testing.T) {
	picks := []effect{
		{T: 20, Len: 1.5, Kind: fxFaceZoom},
		{T: 0.5, Kind: fxPunch},           // the hook is reading
		{T: 24, Kind: fxPunch},            // too close to the zoom at 20
		{T: 5, Kind: fxPunch},             // fine
		{T: 30, Len: 9, Kind: fxFaceZoom}, // held 3 s at most
		{T: 34, Kind: fxPunch},            // over the cap (36 s short: 3)
		{T: 35, Kind: fxPunch},            // runs into the last two seconds
	}
	got := limitEffects(picks, 36, 3)
	if len(got) != 3 || got[0].T != 5 || got[1].T != 20 || got[2].T != 30 {
		t.Fatalf("kept %+v", got)
	}
	if got[0].Len != punchRise+punchHold || got[2].Len != fxZoomMax {
		t.Errorf("lengths %v %v", got[0].Len, got[2].Len)
	}
}

// The goodbye update's three shorts as becky-moment cut them (2026-10-14).
func TestShortsAreWholeSentences(t *testing.T) {
	segs := []segment{
		{282.37, 284.53, "That's kind of what it comes down to."},
		{288.93, 293.41, "So on September 30th, I did a live stream where I apologized."},
		{293.49, 296.05, "That was my apology video, my apology live stream."},
		{296.45, 297.89, "And I hyped it up."},
		{348.6, 351.7, "Well, he did not like that apology video."},
		{351.9, 357.4, "Of course he didn't, because I was telling the truth."},
		{547.2, 549.9, "Very much so stalking and harassing people."},
		{550.3, 551.8, "And then he deletes them right away."},
		{551.9, 553.2, "And threatening people."},
		{554.3, 563.4, "Especially PopTart, who he's not supposed to do that to."},
		{603.5, 608.4, "Said he was gonna make a website dedicated to Kickflip, which is my kid"},
		{611.1, 617.9, "Said footage of my kid is going to be in his movie"},
		{956.0, 958.4, "Um so he's chilled out a bit."},
		{958.6, 962.5, "He's even said some neutral things about me since then on his live streams that he"},
		{963.5, 967.4, "you know it accused me of things before"},
		{1017.2, 1020.5, "And I'm simply calling them out."},
		{1022.3, 1024.8, "Um Yeah, so that's a channel update."},
	}
	for _, c := range []struct{ in, want [2]float64 }{
		{[2]float64{551.885, 611.885}, [2]float64{546.95, 608.4}}, // back over two "And"s (+ lead-in); not "Said footage of my-"
		{[2]float64{963.2, 1022.8}, [2]float64{958.4, 1020.5}},    // a lowercase start walks back; lead-in stops at "bit."; not "Um Yeah, so-"
		{[2]float64{554.3, 608.4}, [2]float64{554.05, 608.4}},     // already whole: only the lead-in
		{[2]float64{296.3, 356.2}, [2]float64{288.68, 351.7}},     // "And", then "apologized. That" with no gap: back to "So on September 30th"
		{[2]float64{0, 30}, [2]float64{0, 30}},                    // no words there (silent footage): untouched
	} {
		s := wholeSentences(short{Start: c.in[0], End: c.in[1]}, segs)
		if !near(s.Start, c.want[0]) || !near(s.End, c.want[1]) {
			t.Errorf("%v -> %.1f-%.1f, want %v", c.in, s.Start, s.End, c.want)
		}
	}
}

func TestJordansListAndNames(t *testing.T) {
	if v, _ := seconds("1:23.5"); v != 83.5 {
		t.Errorf("1:23.5 = %v", v)
	}
	if v, _ := seconds("0:01:23"); v != 83 {
		t.Errorf("0:01:23 = %v", v)
	}
	m := listLine.FindStringSubmatch("6:04 - 6:58 He threatened my kid")
	if m == nil || m[1] != "6:04" || m[2] != "6:58" || m[3] != "He threatened my kid" {
		t.Fatalf("list line = %q", m)
	}
	if k := kebab("He Threatened My Kid?! (part 2)"); k != "he-threatened-my-kid-part-2" {
		t.Errorf("kebab = %q", k)
	}
}
