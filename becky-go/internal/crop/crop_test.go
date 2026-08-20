package crop

import (
	"strings"
	"testing"
)

// Every platform normalises playback to roughly -14 LUFS, so a short that lands
// quiet gets turned UP along with its noise floor, and one that lands hot gets
// turned down and sounds flat beside everything around it.
//
// Measured on a real render of test-for-clips.mp4: **-24.0 LUFS before, -19.2
// after**, and the true peak moved from -0.5 dBFS to exactly the -1.5 ceiling
// asked for - real headroom for the lossy encode.
//
// It does NOT reach -14, and that is not a bug to chase with a two-pass filter:
// loudnorm's own analysis of that file reports input_tp -0.53 dBTP, i.e. the
// source is already peak-limited, so the remaining 5 dB could only be bought by
// squashing the dynamics. It took the gain that was actually available.
func TestRenderArgs_NormalisesLoudness(t *testing.T) {
	args := RenderArgs("in.mp4", 1, 2, "crop=1:2:3:4", "out.mp4")

	af := -1
	for i, a := range args {
		if a == "-af" {
			af = i
		}
	}
	if af < 0 || af+1 >= len(args) {
		t.Fatalf("no -af in the render args, so nothing normalises the audio: %v", args)
	}
	if args[af+1] != LoudnormFilter {
		t.Errorf("-af = %q, want %q", args[af+1], LoudnormFilter)
	}
	for _, want := range []string{"I=-14", "TP=-1.5"} {
		if !strings.Contains(LoudnormFilter, want) {
			t.Errorf("loudness filter lost %q: %s", want, LoudnormFilter)
		}
	}
}

// The audio filter must not displace the video filter chain - they are separate
// flags and a short with normalised audio but no crop is not a short.
func TestRenderArgs_KeepsTheVideoChainAlongsideTheAudioFilter(t *testing.T) {
	chain := "sendcmd=f=crop0.cmds,crop=608:1080:0:0,scale=1080:1920:flags=lanczos"
	args := RenderArgs("in.mp4", 1, 2, chain, "out.mp4")

	var gotVF string
	for i, a := range args {
		if a == "-vf" && i+1 < len(args) {
			gotVF = args[i+1]
		}
	}
	if gotVF != chain {
		t.Errorf("-vf = %q, want the crop chain %q", gotVF, chain)
	}
}

// StaticAt is what points a face-less shot at its subject instead of at the
// middle of nothing. The values matter: an aim of 0.75 on a 1920x1080 source
// must actually put the crop in the right-hand side of the frame.
func TestStaticAtAimsTheCropAndStaysInFrame(t *testing.T) {
	const w, h = 1920, 1080
	asp := 9.0 / 16.0
	centre := StaticCenter(w, h, asp)

	right := StaticAt(w, h, asp, 0.75)
	if right.W != centre.W || right.H != centre.H {
		t.Errorf("StaticAt changed the crop SIZE: %+v vs centre %+v", right, centre)
	}
	if right.X <= centre.X {
		t.Errorf("an aim of 0.75 did not move the crop right: X=%d, centre X=%d", right.X, centre.X)
	}
	// 0.75 * 1920 = 1440 is the wanted centre; the rect is 606 wide.
	if got := right.X + right.W/2; got < 1400 || got > 1480 {
		t.Errorf("crop centre landed at %d, want ~1440", got)
	}

	// An aim at the very edge must clamp INSIDE the frame, not hang off it.
	for _, xf := range []float64{-1, 0, 0.02, 0.98, 1, 2} {
		r := StaticAt(w, h, asp, xf)
		if r.X < 0 || r.X+r.W > w {
			t.Errorf("aim %.2f produced an out-of-frame rect %+v (source width %d)", xf, r, w)
		}
		if r.X%2 != 0 {
			t.Errorf("aim %.2f produced an odd X (%d); encoders want even", xf, r.X)
		}
	}

	// Dead centre must reproduce StaticCenter exactly, so the fallback path is
	// unchanged when focal has nothing better to say.
	if mid := StaticAt(w, h, asp, 0.5); mid != centre {
		t.Errorf("StaticAt(0.5) = %+v, want the same rect as StaticCenter %+v", mid, centre)
	}
}
