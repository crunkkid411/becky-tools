package main

import (
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// synth builds 16 kHz audio whose level, block by block, is the given dB list,
// starting at t0: a 160 Hz square wave (a voice; its RMS is exactly the level),
// or, for the blocks listed in hiss, noise at that level (an "f").
func synth(t0, block float64, dbs []float64, total float64, hiss ...int) *audio {
	a := &audio{sr: 16000, pcm: make([]int16, int(total*16000))}
	seed := uint32(1)
	for k, db := range dbs {
		amp := math.Pow(10, db/20) * 32767
		noisy := false
		for _, h := range hiss {
			noisy = noisy || h == k
		}
		lo := int(math.Round((t0 + float64(k)*block) * 16000))
		for i := lo; i < lo+int(math.Round(block*16000)) && i < len(a.pcm); i++ {
			v := amp
			if noisy {
				seed = seed*1664525 + 1013904223
				v = amp * math.Sqrt(3) * (float64(seed>>8)/float64(1<<24)*2 - 1) // uniform noise, RMS amp
			} else if i/50%2 == 1 {
				v = -amp
			}
			a.pcm[i] = int16(v)
		}
	}
	return a
}

// Jordan's zero-crossing-errors.JPG (goodbye update, source 375.267): becky-cut's
// edge sat 4 frames early on crackle; his voice starts at 375.42 and he put the
// cut on the frame at 375.400. Levels measured every 5 ms from 375.250 (shifted
// by 375 s here).
func TestCutStartsOnTheFrameBeforeHisVoice(t *testing.T) {
	crackle := []float64{-50.7, -48.5, -45.8, -46.6, -44.5, -38.9, -43.3, -40.7, -36.5, -43.6, -39.3, -38.7, -48.2, -40.1, -42.8, -48.8,
		-43.3, -45.2, -40.4, -38.3, -41.6, -36.4, -44.5, -44.1, -43.8, -39.3, -46.9}
	env := append(append([]float64{}, crackle...), -33.8, -35.2, -38.4, -32.3, -38.2, -35.0, -34.7, -17.8, -8.2, -14.8, -15.6, -16.3)
	for i := 0; i < 200; i++ { // he keeps talking (-9 dB, his level on this recording)
		env = append(env, -9)
	}
	env = append(append(env, crackle...), crackle...) // then the same crackle until the piece ends
	au := synth(0.25, 0.005, env, 2.0)
	fps := 30.0
	got, _ := tighten([]span{{8 / fps, 50 / fps}}, au, fps)
	if math.Abs(got[0].A-0.4) > 1e-6 {
		t.Errorf("start = %.4f, want 0.4000 (the frame before his voice, as Jordan cut it)", got[0].A)
	}
	// the end: his voice stops at 0.25 + 239*0.005 = 1.445 -> the next frame, 1.4667
	if math.Abs(got[0].B-44/fps) > 1e-6 {
		t.Errorf("end = %.4f, want %.4f (the first frame after his voice)", got[0].B, 44/fps)
	}
	// an edge inside his voice (a cut in running speech) stays put
	if got, _ := tighten([]span{{13 / fps, 30 / fps}}, au, fps); got[0].A != 13/fps || got[0].B != 30/fps {
		t.Errorf("a cut inside speech moved: %+v", got[0])
	}
}

// A word's soft first and last sounds are the word: crackle, then a 40 ms "f"
// under the voice, the vowel, a 60 ms "s", crackle. The cut moves off the
// crackle but keeps both; a 300 ms out-breath after the voice still goes.
func TestCutKeepsTheFAndTheS(t *testing.T) {
	var env []float64
	add := func(db float64, n int) {
		for i := 0; i < n; i++ {
			env = append(env, db)
		}
	}
	add(-45, 30) // 0.25-0.40 crackle
	add(-35, 8)  // 0.40-0.44 "f"
	add(-9, 100) // 0.44-0.94 the vowel
	add(-42, 12) // 0.94-1.00 "s"
	add(-45, 30) // 1.00-1.15 crackle
	var hiss []int
	for i := 30; i < 38; i++ {
		hiss = append(hiss, i)
	}
	for i := 138; i < 150; i++ {
		hiss = append(hiss, i)
	}
	fps := 30.0
	got, _ := tighten([]span{{6 / fps, 36 / fps}}, synth(0.25, 0.005, env, 2.0, hiss...), fps)
	if math.Round(got[0].A*fps) != 12 || math.Round(got[0].B*fps) != 30 {
		t.Errorf("piece = frames %.0f-%.0f, want 12-30 (from the \"f\" to the end of the \"s\")", got[0].A*fps, got[0].B*fps)
	}
	env = env[:138] // the "s" is a 300 ms out-breath instead
	add(-42, 60)
	add(-45, 30)
	for i := 150; i < 198; i++ {
		hiss = append(hiss, i)
	}
	got, _ = tighten([]span{{6 / fps, 36 / fps}}, synth(0.25, 0.005, env, 2.0, hiss...), fps)
	if math.Round(got[0].B*fps) != 29 {
		t.Errorf("end = frame %.0f, want 29 (the breath goes)", got[0].B*fps)
	}
}

// Jordan's censor-example.JPG: "fucking" in "I don't know how fucking long"
// (goodbye update, source 194.01 in the transcript, 5 frames early). He marked
// the word 194.167-194.367 and the censor 194.200-194.333: 4 frames. Levels
// every 10 ms from 193.85 (shifted by 193.8 s, a whole number of frames); its
// "f" hisses at 194.12-194.17, the "k" bursts for 20 ms at 194.30.
func TestCensorSitsOneFrameInsideTheWord(t *testing.T) {
	env := []float64{-13.6, -15.3, -13.9, -15.3, -13.3, -13.3, -13.9, -17.3, -17.7, -18.0, -18.6, -18.4, -16.8, -16.0, -15.4,
		-13.9, -12.7, -12.0, -11.8, -13.1, -14.3, -14.8, -15.3, -19.9, -23.1, -30.4, -32.1, -37.3, -40.9, -41.9, -42.2, -37.4, -32.7,
		-23.6, -13.3, -12.8, -13.4, -13.9, -15.7, -19.3, -23.9, -26.1, -31.5, -34.6, -39.0, -34.7, -32.3, -20.6, -14.2, -13.2, -13.1,
		-17.5, -15.5, -14.5, -13.8, -14.1, -14.4, -14.7, -15.0, -14.3, -13.7, -13.5, -13.6, -13.9, -14.5, -14.9, -15.2, -15.7, -16.2, -16.4}
	au := synth(0.05, 0.01, env, 1.0, 27, 28, 29, 30, 31, 32, 45, 46)
	fps := 30.0
	w := Word{Word: "fucking", Start: 0.21, End: 0.53}
	a, b, ok := wordEdges(au, w, -60, fps)
	if !ok || math.Abs(a-11/fps) > 1e-6 || math.Abs(b-17/fps) > 1e-6 {
		t.Fatalf("word = %.4f-%.4f (%v), want %.4f-%.4f as Jordan marked it", a, b, ok, 11/fps, 17/fps)
	}
	words := []Word{{Word: "how", Start: 0.05, End: 0.21}, w, {Word: "long", Start: 0.53, End: 0.69}}
	bs := censorJob(words, []bool{true, true, true}, au, []piece{{In: 0, Out: 1, TL: 10}}, fps)
	if len(bs) != 1 || bs[0].F0 != 312 || bs[0].Len != 4 {
		t.Errorf("censor = %+v, want frame 312, 4 frames long (his example)", bs)
	}
	if bs := censorJob(words, []bool{true, false, true}, au, []piece{{In: 0, Out: 1, TL: 10}}, fps); len(bs) != 0 {
		t.Errorf("a word that is cut out of the edit needs no censor: %+v", bs)
	}
}

// "Go fuck yourself" (goodbye update 313.75-314.25, shifted by 313.7 s): the
// transcript put "fuck" at 313.90, which is still "Go" (313.83-313.92, its
// "G" bursts for 20 ms). The "f" hisses at 313.95-313.98, the vowel starts at
// 313.99 and stops on the "k" at 314.10: the word is frames 9419-9423.
func TestCensorFindsTheFNotTheWordBefore(t *testing.T) {
	env := []float64{-53, -53, -57, -57, -55, -57, -55, -54, -30, -29, -18, -18, -16, -14, -15, -18, -19, -20, -25, -26, -29, -30, -26,
		-25, -20, -15, -13, -13, -13, -16, -18, -16, -18, -21, -27, -35, -34, -41, -55, -33, -32, -41, -28, -21, -19, -19, -20, -20}
	au := synth(0.05, 0.01, env, 1.0, 6, 8, 9, 20, 21, 22, 23, 39, 40, 41)
	a, b, ok := wordEdges(au, Word{Word: "fuck", Start: 0.2, End: 0.6}, -49, 30)
	if !ok || math.Round(a*30) != 8 || math.Round(b*30) != 12 {
		t.Errorf("word = frames %.0f-%.0f (%v), want 8-12", a*30, b*30, ok)
	}
}

// "so fucked up" (657.90-658.38, shifted by 657.8 s): the long "s" of "so"
// hisses before its own vowel too; the "f" is the one at 658.19-658.25.
// "want to fucking live" (1084.55-1085.13, shifted by 1084.5 s): a soft "f"
// with no hiss, a near-silence at 1084.88-1084.90, and no stop on the "k":
// the word runs on to 1085.12.
func TestCensorSkipsTheSOfSoAndHearsASoftF(t *testing.T) {
	so := []float64{-20, -21, -22, -21, -20, -20, -21, -21, -20, -20, -19, -19, -20, -20, -20, -21, -17, -10, -11, -12, -11, -12, -14,
		-14, -13, -14, -18, -22, -28, -29, -36, -40, -37, -33, -33, -31, -25, -16, -13, -13, -13, -13, -15, -19, -21, -28, -40, -37, -47}
	hiss := []int{29, 30, 31, 32, 33, 34, 35}
	for i := 0; i < 16; i++ {
		hiss = append(hiss, i)
	}
	a, b, ok := wordEdges(synth(0.10, 0.01, so, 1.0, hiss...), Word{Word: "fucked", Start: 0.34, End: 0.90}, -49, 30)
	if !ok || math.Round(a*30) != 13 || math.Round(b*30) != 17 {
		t.Errorf("so fucked: frames %.0f-%.0f (%v), want 13-17", a*30, b*30, ok)
	}
	soft := []float64{-19, -22, -21, -21, -21, -18, -15, -14, -13, -13, -13, -13, -14, -15, -15,
		-14, -15, -18, -22, -28, -26, -31, -28, -23, -18, -16, -15, -17, -15, -16, -19, -27, -32, -42, -40, -44, -23, -17,
		-18, -20, -22, -21, -19, -19, -18, -19, -18, -19, -18, -16, -16, -18, -21, -26, -24, -25, -27, -42, -29}
	a, b, ok = wordEdges(synth(0.05, 0.01, soft, 1.0), Word{Word: "fucking", Start: 0.21, End: 0.53}, -49, 30)
	if !ok || math.Round(a*30) != 12 || math.Round(b*30) != 19 {
		t.Errorf("soft f: frames %.0f-%.0f (%v), want 12-19", a*30, b*30, ok)
	}
}

func TestSwearSyllables(t *testing.T) {
	for w, n := range map[string]int{"fuck": 1, "Fucking": 2, "fucked.": 1, "fucker": 2, "motherfucker": 4, "fuckin'": 2, "fucks": 1} {
		if got := syllables(w); got != n {
			t.Errorf("syllables(%q) = %d, want %d", w, got, n)
		}
	}
}

// The goodbye update at 6:36 (Jordan: "I say 'Whatever', the second clip I say
// 'here's'"): both are islands for the model; "And um" is filler the words
// alone take out; "Yeah." after his own question is never one.
func TestIslands(t *testing.T) {
	ws := []Word{
		{Word: "striking", Start: 393.0, End: 393.6}, {Word: "them.", Start: 393.6, End: 394.37},
		{Word: "Whatever.", Start: 396.26, End: 397.22},
		{Word: "Here's", Start: 397.78, End: 398.42},
		{Word: "In", Start: 399.38, End: 399.54}, {Word: "fact,", Start: 399.54, End: 399.78}, {Word: "it's", Start: 399.86, End: 400.1},
		{Word: "And", Start: 401.0, End: 401.2}, {Word: "um", Start: 401.2, End: 401.4},
		{Word: "Am", Start: 402.0, End: 402.1}, {Word: "I", Start: 402.1, End: 402.2}, {Word: "grateful?", Start: 402.2, End: 402.5},
		{Word: "Yeah.", Start: 403.2, End: 403.5},
		{Word: "it", Start: 404.2, End: 404.4}, {Word: "is.", Start: 404.4, End: 404.7},
	}
	keep := make([]bool, len(ws))
	for i := range keep {
		keep[i] = true
	}
	var got []string
	for _, is := range islands(ws, keep) {
		got = append(got, is.Text)
	}
	if strings.Join(got, "|") != "Whatever.|Here's|And um" {
		t.Errorf("islands = %q", got)
	}
	if allFiller("Whatever.") || allFiller("Here's") || !allFiller("And um") || !allFiller("So uh") {
		t.Error("only filler words are cut without the model")
	}
	// pauses INSIDE his sentence are emphasis (goodbye update 23:41, 9:22), a
	// lone "and" between his words is meant; "another note" + a 7.8 s stop is not
	at := func(text []string, starts []float64) []Word {
		var ws []Word
		for i, s := range text {
			ws = append(ws, Word{Word: s, Start: starts[i], End: starts[i] + 0.2})
		}
		return ws
	}
	ws = at([]string{"on", "this", "platform", "or", "to", "violate", "multiple", "protection", "orders", "against", "him", "for", "her.", "So", "kids", "and", "wife.", "Um", "another", "note", "I", "know."},
		[]float64{0, 0.2, 0.4, 1.6, 1.8, 2.8, 3.0, 4.0, 4.2, 4.4, 4.6, 5.6, 5.8, 8.5, 8.7, 9.5, 10.3, 10.5, 12.5, 12.7, 20.7, 20.9})
	keep = make([]bool, len(ws))
	for i := range keep {
		keep[i] = true
	}
	got = nil
	for _, is := range islands(ws, keep) {
		got = append(got, is.Text)
	}
	if strings.Join(got, "|") != "another note" {
		t.Errorf("islands = %q, want only \"another note\"", got)
	}
}

func TestProjectNameHasNoDay(t *testing.T) {
	for in, want := range map[string]string{`X:\Videos\2026\10_oct\12_goodbye-youtube-update`: "goodbye-youtube-update", `X:\v\livestream`: "livestream"} {
		if got := projectBase(in); got != want {
			t.Errorf("projectBase(%s) = %s, want %s", in, got, want)
		}
	}
}

// A provided file goes on the timeline; anything else is a short marker, and
// an insert moves every marker after it.
// The goodbye update: the 12B flagged all of these; he points at the first three.
func TestMarkersOnlyWhereHePoints(t *testing.T) {
	var ss []Sentence
	var ls []showLine
	for i, s := range []string{
		"In fact, he blackmailed me into an apology saying this.",
		"Here's this short, and he tagged me and PopTart in, which fucking no contact order, right?",
		"Like this is the stuff he's been threatening.",
		"Cause he made this documentary that I didn't want to watch.",
		"So on September 30th, I did a live stream where I apologized.",
		"I'm also gonna link all the videos if you do want to like watch them in depth, but I'm just gonna summarize here real quick.",
		"Unfortunately, all the drama has caused, you know, this other guy's live streams to like get better numbers.",
		"we should not be incentivizing people to threaten children on this platform",
	} {
		ss = append(ss, Sentence{Text: s})
		ls = append(ls, showLine{ID: i})
	}
	if got := pointed(ss, ls); len(got) != 3 || got[2].ID != 2 {
		t.Errorf("pointed = %+v, want sentences 0-2", got)
	}
}

func TestAssetsAndMarkers(t *testing.T) {
	dir := t.TempDir()
	rec := filepath.Join(dir, "2026-10-12_16-38-12.mp4")
	for _, f := range []string{rec, filepath.Join(dir, "2026-10-12_16-38-12.mkv"), filepath.Join(dir, "kickflip site.png"), filepath.Join(dir, "elsewhere.mp4")} {
		_ = os.WriteFile(f, []byte("x"), 0o644)
	}
	_ = os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("use elsewhere.mp4 for the apology\nSept 27 blackmail"), 0o644)
	shot := filepath.Join(t.TempDir(), "censor-example.JPG") // his screenshot for becky, outside the folder
	_ = os.WriteFile(shot, []byte("x"), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "user-feedback.md"), []byte("see \""+shot+"\""), 0o644)
	got := providedAssets(dir, rec)
	if len(got) != 2 || filepath.Base(got[0]) != "elsewhere.mp4" || filepath.Base(got[1]) != "kickflip site.png" {
		t.Errorf("provided = %q", got)
	}
	ss := []Sentence{{ID: 0, Start: 0, End: 5, Text: "he blackmailed me into an apology saying this."}, {ID: 1, Start: 6, End: 9, Text: "Look at his website."}, {ID: 2, Start: 10, End: 12, Text: "Crazy."}}
	ps := []piece{{In: 0, Out: 5.5, TL: 0}, {In: 6, Out: 9.5, TL: 5.5}, {In: 10, Out: 12.5, TL: 9}}
	lines := []showLine{{ID: 0, Label: "blackmail apology", What: "the clip of him blackmailing", Asset: "clip.mp4"}, {ID: 1, Label: "kickflip website", What: "his website"}}
	pl, ms, marked := placeAssets(lines, ss, ps, 30, func(string) (float64, bool) { return 10, true })
	if len(pl) != 1 || !pl[0].Insert || pl[0].Frame != 165 || pl[0].Frames != 300 {
		t.Fatalf("placements = %+v", pl)
	}
	if len(ms) != 1 || ms[0].Label != "kickflip website" || math.Abs(ms[0].At-19) > 1e-6 || marked[0].ID != 1 {
		t.Fatalf("markers = %+v (want the website at 9 s + the 10 s insert)", ms)
	}
	if got := frameClock(121.5, 30); got != "2:01.15" {
		t.Errorf("frameClock = %s", got)
	}
	if got := shortLabel("The Blackmail Apology Clip From September"); got != "the blackmail apology clip" {
		t.Errorf("label = %q", got)
	}
	path := filepath.Join(dir, "MARKERS.txt")
	if err := writeMarkers(path, marked, ss, ms, 30); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	if !strings.Contains(string(b), "0:19.00  kickflip website") || !strings.Contains(string(b), "Look at his website.") {
		t.Errorf("MARKERS.txt:\n%s", b)
	}
}

func TestLooksTakeTurnsOnCuts(t *testing.T) {
	var ps []piece
	tl := 0.0
	for i := 0; i < 60; i++ { // 3 s pieces, 3 minutes
		ps = append(ps, piece{In: float64(i) * 4, Out: float64(i)*4 + 3, TL: tl})
		tl += 3
	}
	job, n := looksJob(ps, 30)
	lines := strings.Split(strings.TrimSpace(job), "\n")
	// a 12 s creep from 21 s, a 6 s crop a minute after it ends, then a creep again
	if n != 3 || !strings.HasPrefix(lines[0], "pipzoom\t630\t360\t1.11") || !strings.HasPrefix(lines[1], "zoom\t2790\t180\t1.12") || !strings.HasPrefix(lines[2], "pipzoom\t4770\t360\t") {
		t.Errorf("looks (%d):\n%s", n, job)
	}
	if s := starterJob(`X:\v\a.mp4`); s != "trackfx\t0\t0\tX:\\v\\a.mp4\tBeauty Box\tPOLISH\tAutoLooks\tpolish\tColor Corrector\thj-simple\tColor Curves\thair-help\tColor Curves\tPOLISH-subtle\n" {
		t.Errorf("starter job = %q", s)
	}
}
