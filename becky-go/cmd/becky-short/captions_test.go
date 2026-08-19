package main

import (
	"os"
	"strings"
	"testing"

	"becky-go/internal/subs"
)

// THE bug this file exists to catch: captions timed to the SOURCE instead of to
// the clip. A short cut from 146.2s would then carry cues starting at 146.2s,
// ffmpeg would burn nothing at all onto a 28-second clip, and the render would
// still exit 0 with a file that plays. Exactly the §2.1 failure mode - silent,
// plausible, and invisible to every unit test.
func TestCaptionCues_AreClipRelativeNotSourceRelative(t *testing.T) {
	words := []subs.Word{
		{Word: "before", Start: 100.0, End: 100.3},
		{Word: "hello", Start: 146.5, End: 146.8},
		{Word: "there", Start: 147.0, End: 147.4},
		{Word: "after", Start: 200.0, End: 200.4},
	}
	cues := captionCues(words, 146.2, 174.6, 30000.0/1001.0)

	if len(cues) == 0 {
		t.Fatal("no cues built for a window that contains speech")
	}
	if cues[0].Start > 1.0 {
		t.Errorf("first cue starts at %.2fs — captions are on the SOURCE timeline, not the clip's; "+
			"burned into a 28.4s clip nothing would appear", cues[0].Start)
	}
	// Words outside the window must not appear: "before" and "after" are in the
	// same transcript but not in this clip.
	all := ""
	for _, c := range cues {
		all += " " + strings.ToLower(c.Text)
	}
	if strings.Contains(all, "before") || strings.Contains(all, "after") {
		t.Errorf("cues carry words from outside [146.2,174.6]: %q", strings.TrimSpace(all))
	}
	if !strings.Contains(all, "hello") {
		t.Errorf("cues dropped a word that IS inside the window: %q", strings.TrimSpace(all))
	}
}

// Captions go on the finished 9:16 frame, so the burn must come AFTER the crop
// and the scale. Appended before them, libass would draw at source resolution
// and the scale would then shrink the text with the picture.
func TestCaptionFilter_IsAppendedAfterTheScale(t *testing.T) {
	chain := "sendcmd=f=crop.cmds,crop=608:1080:100:0,scale=1080:1920:flags=lanczos"
	chain += "," + captionFilter("C:/tmp/captions.srt", "C:/tmp/clip.mp4")

	scale := strings.Index(chain, "scale=")
	burn := strings.Index(chain, "subtitles=")
	if burn < 0 {
		t.Fatalf("no subtitles filter in the chain: %s", chain)
	}
	if burn < scale {
		t.Errorf("subtitles at %d precedes scale at %d — captions would be scaled with the source", burn, scale)
	}
}

// The subtitles filter parses its own argument string, so a Windows drive colon
// has to survive as an escaped colon rather than being read as a separator.
func TestCaptionFilter_EscapesTheWindowsDriveColon(t *testing.T) {
	got := captionFilter("C:"+string(os.PathSeparator)+"tmp"+string(os.PathSeparator)+"captions.srt", "")
	if strings.Contains(got, string(os.PathSeparator)+"tmp") {
		t.Errorf("a path separator survived; ffmpeg needs forward slashes: %s", got)
	}
	// The escaped form puts a backslash BEFORE the colon.
	if !strings.Contains(got, "C"+`\`+":/") {
		t.Errorf("drive colon is not escaped, ffmpeg will read it as a separator: %s", got)
	}
}

// The caption height Jordan drags to in a review app is written to a
// .capstyle.json beside the transcript, and every surface that burns captions is
// expected to honour it. A short that ignored it would put his captions
// somewhere he did not choose.
func TestCaptionFilter_UsesTheShippedStyle(t *testing.T) {
	got := captionFilter("C:/tmp/captions.srt", "")
	st := subs.DefaultStyle()
	for _, want := range []string{
		"FontName=" + st.FontName,
		"PrimaryColour=&H00FFFFFF", // opaque white fill
		"OutlineColour=&H00000000", // opaque black outline
		"Alignment=2",              // bottom-centre
	} {
		if !strings.Contains(got, want) {
			t.Errorf("caption style lost %q: %s", want, got)
		}
	}
}
