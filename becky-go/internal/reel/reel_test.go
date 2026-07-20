package reel

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"becky-go/internal/config"
	"becky-go/internal/edl"
)

// ffmpegPath returns an available ffmpeg for gated exec tests, or "".
func ffmpegPath() string {
	if p, err := exec.LookPath("ffmpeg"); err == nil {
		return p
	}
	return ""
}

// ffprobePath returns an available ffprobe for gated exec tests, or "".
func ffprobePath() string {
	if p, err := exec.LookPath("ffprobe"); err == nil {
		return p
	}
	return ""
}

// ffprobeFrameCount returns the EXACT decoded frame count of a video's first
// stream (-count_frames actually decodes, rather than trusting a container's
// possibly-approximate frame-count tag).
func ffprobeFrameCount(ffprobe, path string) (int, error) {
	out, err := exec.Command(ffprobe, "-v", "error", "-select_streams", "v:0",
		"-count_frames", "-show_entries", "stream=nb_read_frames",
		"-of", "default=nw=1:nk=1", path).Output()
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(strings.TrimSpace(string(out)))
}

// resolveOptionsForTest defaults options against a fixed codec so the pure
// arg-builder tests don't depend on the host's ~/.becky/config.json.
func resolveOptionsForTest(r edl.Reel) resolvedOpts {
	return resolveOptions(r, Options{}, config.Config{Codec: "h264_nvenc"})
}

func twoClipReel() edl.Reel {
	return edl.Reel{
		Version: "1",
		Name:    "bake-off",
		Clips: []edl.Clip{
			{ID: "c1", Source: `X:\case\A.mp4`, In: 10.0, Out: 12.0, Label: "first",
				Meta: edl.ClipMeta{Person: "J.DOE", Location: "KITCHEN", Date: "2026-06-18", SourceFPS: 30}},
			{ID: "c2", Source: `X:\case\B.mp4`, In: 3.0, Out: 5.0, Label: "second",
				Meta: edl.ClipMeta{SourceFPS: 25}},
		},
		Overlay: edl.Overlay{Enabled: true, ShowFilename: true, ShowTimecode: true, ShowPerson: true, Position: "bottom"},
	}
}

// fakeFirstClipProbe overrides the probe seam for a test and restores it after.
func fakeFirstClipProbe(t *testing.T, w, h int, fps float64, ok bool) {
	t.Helper()
	orig := firstClipProbe
	t.Cleanup(func() { firstClipProbe = orig })
	firstClipProbe = func(_ /*ffprobe*/, _ /*source*/ string) (int, int, float64, bool) {
		return w, h, fps, ok
	}
}

// TestResolveOptions_AutoMatchesFirstClip is the core of change #3: with no
// explicit Width/Height/FPS, the output dimensions + fps come from the FIRST
// clip's probe, NOT the old fixed 1280x720/30.
func TestResolveOptions_AutoMatchesFirstClip(t *testing.T) {
	fakeFirstClipProbe(t, 1920, 1080, 25, true)
	r := twoClipReel() // clip 0 is the first clip
	ro := resolveOptions(r, Options{}, config.Config{Codec: "h264_nvenc", FFprobe: "ffprobe"})

	if ro.Width != 1920 || ro.Height != 1080 {
		t.Fatalf("output should match first clip 1920x1080, got %dx%d", ro.Width, ro.Height)
	}
	if ro.OutFPS != 25 {
		t.Fatalf("output fps should match first clip (25), got %v", ro.OutFPS)
	}

	// The filter graph normalizes every clip to the first clip's size + fps.
	graph, _, _ := buildFilterComplex(r, ro)
	for _, want := range []string{"scale=1920:1080", "fps=25"} {
		if !strings.Contains(graph, want) {
			t.Fatalf("filter graph should normalize to first clip; missing %q in:\n%s", want, graph)
		}
	}
	// The per-clip ORIGINAL-timecode rate stays the SOURCE's own fps (clip 0 @30),
	// independent of the matched output fps — it's the verification anchor.
	if !strings.Contains(graph, "timecode_rate=30") {
		t.Fatalf("clip0 original-timecode rate must stay the source fps (30):\n%s", graph)
	}
}

// TestResolveOptions_ExplicitOverridesWin confirms --width/--height/--fps still
// beat the auto-match (the power-user escape hatch).
func TestResolveOptions_ExplicitOverridesWin(t *testing.T) {
	fakeFirstClipProbe(t, 1920, 1080, 25, true)
	r := twoClipReel()
	ro := resolveOptions(r, Options{Width: 640, Height: 360, FPS: 60},
		config.Config{Codec: "h264_nvenc", FFprobe: "ffprobe"})
	if ro.Width != 640 || ro.Height != 360 || ro.OutFPS != 60 {
		t.Fatalf("explicit overrides should win, got %dx%d @%v", ro.Width, ro.Height, ro.OutFPS)
	}
}

// TestResolveOptions_FallbackWhenUnprobable confirms that when the first clip
// can't be probed (no ffprobe), the classic 1280x720/30 fallback applies so a
// render still succeeds.
func TestResolveOptions_FallbackWhenUnprobable(t *testing.T) {
	fakeFirstClipProbe(t, 0, 0, 0, false)
	r := twoClipReel()
	ro := resolveOptions(r, Options{}, config.Config{Codec: "h264_nvenc"})
	if ro.Width != defaultWidth || ro.Height != defaultHeight || ro.OutFPS != defaultOutFPS {
		t.Fatalf("unprobable first clip should fall back to %dx%d@%v, got %dx%d@%v",
			defaultWidth, defaultHeight, defaultOutFPS, ro.Width, ro.Height, ro.OutFPS)
	}
}

// TestResolveOptions_PartialOverrideMatchesRest confirms a single override (just
// fps) still lets width/height auto-match the first clip.
func TestResolveOptions_PartialOverrideMatchesRest(t *testing.T) {
	fakeFirstClipProbe(t, 1440, 1080, 30, true)
	r := twoClipReel()
	ro := resolveOptions(r, Options{FPS: 24}, config.Config{Codec: "h264_nvenc", FFprobe: "ffprobe"})
	if ro.Width != 1440 || ro.Height != 1080 {
		t.Fatalf("width/height should still auto-match first clip, got %dx%d", ro.Width, ro.Height)
	}
	if ro.OutFPS != 24 {
		t.Fatalf("explicit fps override should win, got %v", ro.OutFPS)
	}
}

func TestBuildRenderArgs_InputSeekAndDuration(t *testing.T) {
	r := twoClipReel()
	args, err := buildRenderArgs(r, resolveOptionsForTest(r))
	if err != nil {
		t.Fatalf("buildRenderArgs: %v", err)
	}
	joined := strings.Join(args, " ")

	// Input-seek + read-window BOTH before -i (both are input options), per clip.
	// -t must precede -i, else ffmpeg treats it as an output-duration limit and
	// truncates the whole concat to the last clip (verified live). -t is the
	// clip's exact 2.000s PLUS segmentReadPad (6 frames @30fps = 0.200s) — headroom
	// for the per-clip trim=end_frame step (buildFilterComplex) to cut EXACTLY the
	// target frame count; see framesFor's doc comment.
	wantSeq := []string{
		"-ss", "10.000000", "-t", "2.200000", "-i", `X:\case\A.mp4`,
		"-ss", "3.000000", "-t", "2.200000", "-i", `X:\case\B.mp4`,
	}
	if !containsSubseq(args, wantSeq) {
		t.Fatalf("expected per-clip input-seek+duration sequence, got:\n%v", args)
	}

	for _, want := range []string{"-filter_complex", "-map [vout]", "-c:v", "-pix_fmt yuv420p", "-an"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("args missing %q in:\n%s", want, joined)
		}
	}
	if !strings.HasSuffix(args[len(args)-1], "_reel.mp4") {
		t.Fatalf("expected output mp4 as last arg, got %q", args[len(args)-1])
	}
}

func TestBuildRenderArgs_EmptyReelErrors(t *testing.T) {
	r := edl.Reel{Name: "x"}
	if _, err := buildRenderArgs(r, resolveOptionsForTest(r)); err == nil {
		t.Fatal("expected error for reel with no clips")
	}
}

func TestBuildFilterComplex_ConcatAndNormalize(t *testing.T) {
	r := twoClipReel()
	graph, outLabel, _ := buildFilterComplex(r, resolveOptionsForTest(r))

	if outLabel != "[vout]" {
		t.Fatalf("out label = %q, want [vout]", outLabel)
	}
	for _, want := range []string{"[0:v]", "[1:v]", "[v0]", "[v1]", "concat=n=2:v=1:a=0[vout]"} {
		if !strings.Contains(graph, want) {
			t.Fatalf("filter graph missing %q in:\n%s", want, graph)
		}
	}
	for _, want := range []string{"scale=1280:720", "setsar=1", "fps=30", "format=yuv420p", "setpts=PTS-STARTPTS"} {
		if !strings.Contains(graph, want) {
			t.Fatalf("filter graph missing normalize step %q in:\n%s", want, graph)
		}
	}
}

// TestBuildRenderArgs_AudioMapsAndSilenceFallback: with audio on and one clip
// lacking an audio stream, the argv maps [aout], encodes AAC, drops -an, and adds a
// silent anullsrc input bounded to the audioless clip's duration.
func TestBuildRenderArgs_AudioMapsAndSilenceFallback(t *testing.T) {
	r := twoClipReel()
	ro := resolveOptionsForTest(r)
	ro.Audio = true
	ro.ClipHasAudio = []bool{true, false} // clip 0 has audio; clip 1 does not
	args, err := buildRenderArgs(r, ro)
	if err != nil {
		t.Fatalf("buildRenderArgs: %v", err)
	}
	joined := strings.Join(args, " ")
	for _, want := range []string{"-map [vout]", "-map [aout]", "-c:a aac", "-b:a 192k"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("audio args missing %q in:\n%s", want, joined)
		}
	}
	if strings.Contains(joined, " -an") {
		t.Fatalf("an audio render must NOT pass -an:\n%s", joined)
	}
	// The audioless clip (dur 2.000) gets a silent fill input, -t-bounded before -i.
	if !containsSubseq(args, []string{"-f", "lavfi", "-t", "2.000000", "-i", "anullsrc=channel_layout=stereo:sample_rate=48000"}) {
		t.Fatalf("expected a -t-bounded silent fill input for the audioless clip:\n%v", args)
	}
}

// TestBuildFilterComplex_AudioConcat: with audio on and both clips having audio,
// the graph normalizes each clip's own audio and concatenates v+a interleaved.
func TestBuildFilterComplex_AudioConcat(t *testing.T) {
	r := twoClipReel()
	ro := resolveOptionsForTest(r)
	ro.Audio = true
	ro.ClipHasAudio = []bool{true, true}
	graph, vOut, aOut := buildFilterComplex(r, ro)
	if vOut != "[vout]" || aOut != "[aout]" {
		t.Fatalf("labels = %q,%q, want [vout],[aout]", vOut, aOut)
	}
	for _, want := range []string{"[0:a]aresample", "[1:a]aresample", "[a0]", "[a1]", "concat=n=2:v=1:a=1[vout][aout]"} {
		if !strings.Contains(graph, want) {
			t.Fatalf("audio graph missing %q in:\n%s", want, graph)
		}
	}
}

// TestAudioInputIndices_SilenceNumbering: an audioless clip is routed to a silent
// input appended after the clip inputs; an audioful clip uses its own [i:a].
func TestAudioInputIndices_SilenceNumbering(t *testing.T) {
	r := twoClipReel()
	ro := resolveOptionsForTest(r)
	ro.Audio = true
	ro.ClipHasAudio = []bool{false, true} // clip 0 silent-filled, clip 1 own audio
	idx := audioInputIndices(r, ro)
	if len(idx) != 2 || idx[0] != 2 || idx[1] != 1 {
		t.Fatalf("audioInputIndices = %v, want [2 1] (clip0->silent input #2, clip1->own [1:a])", idx)
	}
}

// --- frame-exact render regression -----------------------------------------
// Direct regression for a bug measured on a real 88-clip forensic reel: the
// render came out 1.27s (38 frames) LONGER than the reel's own duration math,
// sliding burned captions (timed to the reel timeline) increasingly out of sync
// with the picture by the end of the compilation. Root cause: ffmpeg's per-clip
// -ss/-t + the fps filter's own boundary rounding don't reliably land on a whole
// frame count on their own. The fix force-trims every segment to
// framesFor(clip, OutFPS) frames, independent of that internal rounding.

// TestFramesFor_QuantizesToWholeFrames pins the exact rounding contract the
// render owes the EDL: whatever ffmpeg's seek/fps rounding does internally, the
// render's frame count for a clip must match this, exactly.
func TestFramesFor_QuantizesToWholeFrames(t *testing.T) {
	c := edl.Clip{In: 0, Out: 2.017} // 2.017*30 = 60.51 -> rounds to 61
	if got := framesFor(c, 30); got != 61 {
		t.Fatalf("framesFor = %d, want 61", got)
	}
	if got := segmentDur(c, 30); got < 2.0333 || got > 2.0334 {
		t.Fatalf("segmentDur = %v, want ~2.0333 (61/30)", got)
	}
	// A degenerate zero/negative-duration clip must never trim to 0 frames — that
	// would starve the concat filter of a segment.
	if got := framesFor(edl.Clip{In: 5, Out: 5}, 30); got != 1 {
		t.Fatalf("framesFor(zero-length clip) = %d, want the 1-frame floor", got)
	}
}

// TestBuildFilterComplex_TrimsEachSegmentToExactFrameCount is the direct
// regression: each clip's chain must force-trim to framesFor(c, OutFPS) frames,
// after the fps filter, independent of ffmpeg's own rounding at that boundary.
func TestBuildFilterComplex_TrimsEachSegmentToExactFrameCount(t *testing.T) {
	r := twoClipReel() // both clips are exactly 2.0s @ OutFPS 30 -> 60 frames each
	graph, _, _ := buildFilterComplex(r, resolveOptionsForTest(r))
	if n := strings.Count(graph, "trim=end_frame=60"); n != 2 {
		t.Fatalf("expected 2 occurrences of trim=end_frame=60 (one per clip), got %d:\n%s", n, graph)
	}
}

// TestBuildFilterComplex_AudioTrimsToQuantizedDuration confirms the audio path
// is bounded to the SAME quantized duration the video was frame-trimmed to, so
// the padded -t read window (segmentReadPad) can never bleed a clip's audio into
// the next one.
func TestBuildFilterComplex_AudioTrimsToQuantizedDuration(t *testing.T) {
	r := twoClipReel()
	ro := resolveOptionsForTest(r)
	ro.Audio = true
	ro.ClipHasAudio = []bool{true, true}
	graph, _, _ := buildFilterComplex(r, ro)
	if n := strings.Count(graph, "atrim=duration=2.000"); n != 2 {
		t.Fatalf("expected 2 occurrences of atrim=duration=2.000 (one per clip), got %d:\n%s", n, graph)
	}
}

func TestBuildFilterComplex_LowerThirdBurned(t *testing.T) {
	r := twoClipReel()
	graph, _, _ := buildFilterComplex(r, resolveOptionsForTest(r))

	// Clip 1: original timecode of In=10s @30fps -> 00:00:10:00, colons escaped.
	if !strings.Contains(graph, `timecode='00\:00\:10\:00'`) {
		t.Fatalf("clip1 original timecode not burned:\n%s", graph)
	}
	if !strings.Contains(graph, "timecode_rate=30") {
		t.Fatalf("clip1 missing timecode_rate=30:\n%s", graph)
	}
	// twoClipReel toggles Filename + Person (NOT date) -> "A.mp4 | J.DOE".
	if !strings.Contains(graph, "A.mp4 | J.DOE") {
		t.Fatalf("clip1 metadata line wrong:\n%s", graph)
	}
	if strings.Contains(graph, "2026-06-18") {
		t.Fatalf("date should be absent (ShowDate not toggled in twoClipReel):\n%s", graph)
	}
}

func TestLowerThirdFilter_Toggles(t *testing.T) {
	clip := edl.Clip{Source: `X:\c\v.mp4`, In: 10, Out: 12,
		Meta: edl.ClipMeta{Person: "P", Location: "L", Date: "2026-06-18", Link: "http://x", SourceFPS: 30}}

	if got := lowerThirdFilter(edl.Overlay{Enabled: false}, clip, "", 30, 1280, 720); got != "" {
		t.Fatalf("disabled overlay should produce empty filter, got %q", got)
	}

	o := edl.Overlay{Enabled: true, ShowTimecode: true}
	got := lowerThirdFilter(o, clip, "", 30, 1280, 720)
	if !strings.Contains(got, "timecode=") {
		t.Fatalf("expected timecode line, got %q", got)
	}
	if strings.Contains(got, "| P") {
		t.Fatalf("metadata should be absent when only timecode is on, got %q", got)
	}

	oAll := edl.Overlay{Enabled: true, ShowFilename: true, ShowPerson: true, ShowLocation: true, ShowDate: true, ShowLink: true}
	gotAll := lowerThirdFilter(oAll, clip, "", 30, 1280, 720)
	// Identity fields stay on one row; Date and Link now get their OWN labeled
	// lines so a long URL can't make the row run past the video (colons escaped).
	if !strings.Contains(gotAll, "v.mp4 | P | L") {
		t.Fatalf("identity line wrong:\n%s", gotAll)
	}
	if strings.Contains(gotAll, "v.mp4 | P | L | 2026-06-18") {
		t.Fatalf("date/link must NOT be joined into the identity row:\n%s", gotAll)
	}
	if !strings.Contains(gotAll, "Date\\: 2026-06-18") {
		t.Fatalf("expected a labeled Date line:\n%s", gotAll)
	}
	if !strings.Contains(gotAll, "text='http\\://x'") {
		t.Fatalf("expected a bare URL line (no \"Link:\" label, colons escaped):\n%s", gotAll)
	}
	if strings.Contains(gotAll, "Link\\:") {
		t.Fatalf("the redundant \"Link:\" label should be gone:\n%s", gotAll)
	}
}

// TestOverlayProvenanceFromFilename: with no sidecar date/link, a yt-dlp file
// name supplies both (Date label + canonical watch URL), each on its own line.
func TestOverlayProvenanceFromFilename(t *testing.T) {
	clip := edl.Clip{Source: `X:\case\2026-06-27_Some Title_[abcdefghijk].mp4`, In: 0, Out: 2,
		Meta: edl.ClipMeta{SourceFPS: 30}}
	o := edl.Overlay{Enabled: true, ShowDate: true, ShowLink: true}
	// Wide canvas so the URL stays on one line (this test checks recovery, not wrap).
	got := lowerThirdFilter(o, clip, "", 30, 1920, 1080)
	if !strings.Contains(got, "Date\\: 2026-06-27") {
		t.Fatalf("date should be recovered from the file name:\n%s", got)
	}
	if !strings.Contains(got, "text='https\\://www.youtube.com/watch?v=abcdefghijk'") {
		t.Fatalf("link should be recovered from the file name (bare URL, no label):\n%s", got)
	}
}

// TestLowerThirdFilter_OrderAndLabels pins Jordan's overlay layout: Date (UTC) on
// top, then ORIG TC (a space before the digits), then the filename — matching the
// live preview. Regression for becky-review-user-feedback4 (the burned render had
// lagged the preview: no space, no UTC, filename-first order).
func TestLowerThirdFilter_OrderAndLabels(t *testing.T) {
	clip := edl.Clip{Source: `X:\c\v.mp4`, In: 10, Out: 12,
		Meta: edl.ClipMeta{Date: "2026-06-18", SourceFPS: 30}}
	o := edl.Overlay{Enabled: true, ShowFilename: true, ShowTimecode: true, ShowDate: true}
	got := lowerThirdFilter(o, clip, "", 30, 1280, 720)

	// ORIG TC keeps a trailing space so the burned text reads "ORIG TC 00:00:10:00".
	if !strings.Contains(got, "text='ORIG TC '") {
		t.Fatalf("ORIG TC line must keep a trailing space:\n%s", got)
	}
	// yt-dlp dates are UTC — the label must say so.
	if !strings.Contains(got, "Date\\: 2026-06-18 UTC") {
		t.Fatalf("Date line must be labeled UTC:\n%s", got)
	}
	// Top -> bottom order in the joined filter: Date, then ORIG TC, then filename.
	iDate := strings.Index(got, "Date\\: 2026-06-18")
	iTC := strings.Index(got, "ORIG TC ")
	iName := strings.Index(got, "v.mp4")
	if !(iDate >= 0 && iTC > iDate && iName > iTC) {
		t.Fatalf("overlay order must be Date < ORIG TC < filename (got %d,%d,%d):\n%s", iDate, iTC, iName, got)
	}
}

// TestCachedScrubProxySegment verifies the non-building cache check TimelineEDL relies
// on: a fresh proxy at the deterministic window path is found; a missing one, a
// different window, or a proxy older than its source are all reported as not-cached
// (so the EDL falls back to the raw source).
func TestCachedScrubProxySegment(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "clip.mp4")
	if err := os.WriteFile(src, []byte("src"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, ok := CachedScrubProxySegment(src, 0.5, 5.2, dir); ok {
		t.Fatal("no proxy on disk yet — must report not-cached")
	}
	pp := SegmentProxyPath(src, dir, 0.5, 5.2)
	if err := os.WriteFile(pp, []byte("proxy-bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, off, ok := CachedScrubProxySegment(src, 0.5, 5.2, dir)
	if !ok || got != pp || off != 0 {
		t.Fatalf("fresh exact-match proxy should be cached at offset 0: got %q off=%v ok=%v (want %q)", got, off, ok, pp)
	}
	if _, _, ok := CachedScrubProxySegment(src, 20.0, 25.0, dir); ok {
		t.Fatal("a window nothing on disk contains must not resolve to an unrelated proxy")
	}
	// source newer than the proxy => stale => not cached
	future := time.Now().Add(2 * time.Hour)
	if err := os.Chtimes(src, future, future); err != nil {
		t.Fatal(err)
	}
	if _, _, ok := CachedScrubProxySegment(src, 0.5, 5.2, dir); ok {
		t.Fatal("a proxy older than its source must be treated as stale")
	}
}

// TestCachedScrubProxySegment_FallsBackToWiderContainingProxy is the
// regression for Jordan's real bug: a small trim-handle drag (SRT cut points
// aren't frame-exact) changes a clip's [in,out) just slightly, which used to
// miss the EXACT-window cache and force a fresh raw-source encode every time —
// "dragging clips around... breaks the whole program". A WIDER proxy (built
// with ScrubProxyPadSec margin) must now be found and used, with the correct
// offset into it.
func TestCachedScrubProxySegment_FallsBackToWiderContainingProxy(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "clip.mp4")
	if err := os.WriteFile(src, []byte("src"), 0o644); err != nil {
		t.Fatal(err)
	}
	// A padded proxy covering [10,50) already exists (as ScrubSegment would build).
	wide := SegmentProxyPath(src, dir, 10, 50)
	if err := os.WriteFile(wide, []byte("wide-proxy-bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	// A minor drag narrows the clip to [12,45) — no exact-match proxy for THIS
	// window, but it's fully inside the padded one.
	got, off, ok := CachedScrubProxySegment(src, 12, 45, dir)
	if !ok || got != wide {
		t.Fatalf("a window inside a wider cached proxy should resolve to it: got %q ok=%v (want %q)", got, ok, wide)
	}
	if off != 2 { // 12 - 10 = 2s into the wide proxy
		t.Fatalf("offset into the wider proxy = %v, want 2", off)
	}
	// A window NOT fully inside the wide proxy must still miss.
	if _, _, ok := CachedScrubProxySegment(src, 5, 45, dir); ok {
		t.Fatal("a window extending before the cached proxy's start must not match")
	}
}

func TestWrapToWidth(t *testing.T) {
	// A short string is returned as a single line.
	if got := wrapToWidth("short.mp4", 26, 1280); len(got) != 1 {
		t.Fatalf("short text should be one line, got %d: %v", len(got), got)
	}
	// A long no-space token hard-breaks into multiple lines that each fit.
	long := strings.Repeat("a", 300)
	fontSize, width := 26, 640
	got := wrapToWidth(long, fontSize, width)
	if len(got) < 2 {
		t.Fatalf("a 300-char token at width 640 must wrap, got %d lines", len(got))
	}
	maxChars := int(float64(width-2*ltMarginX) / (float64(fontSize) * 0.55))
	for _, ln := range got {
		if len([]rune(ln)) > maxChars {
			t.Fatalf("wrapped line %q exceeds %d chars", ln, maxChars)
		}
	}
	// No characters are lost (critical: the whole filename must survive).
	if joined := strings.Join(got, ""); joined != long {
		t.Fatalf("wrap dropped characters: got %d, want %d", len(joined), len(long))
	}
	// Width unknown (0) disables wrapping.
	if got := wrapToWidth(long, 26, 0); len(got) != 1 {
		t.Fatalf("width 0 should disable wrapping, got %d lines", len(got))
	}
}

// TestLowerThirdFilter_WrapsLongFilename: a very long filename produces more than
// one drawtext line (so it is never clipped off the right edge of the video).
func TestLowerThirdFilter_WrapsLongFilename(t *testing.T) {
	long := strings.Repeat("verylongname_", 12) + "[abcdefghijk].mp4"
	clip := edl.Clip{Source: `X:\case\` + long, In: 0, Out: 2, Meta: edl.ClipMeta{SourceFPS: 30}}
	o := edl.Overlay{Enabled: true, ShowFilename: true}
	got := lowerThirdFilter(o, clip, "", 30, 640, 360)
	if n := strings.Count(got, "drawtext="); n < 2 {
		t.Fatalf("a long filename should wrap to >=2 drawtext lines, got %d:\n%s", n, got)
	}
}

func TestMetaLine_SkipsEmptyAndUntoggled(t *testing.T) {
	clip := edl.Clip{Source: `X:\c\v.mp4`, Meta: edl.ClipMeta{Person: "", Location: "L"}}
	o := edl.Overlay{Enabled: true, ShowFilename: true, ShowPerson: true, ShowLocation: true}
	if got := metaLine(o, clip); got != "v.mp4 | L" {
		t.Fatalf("metaLine = %q, want %q", got, "v.mp4 | L")
	}
}

func TestLineYExpr(t *testing.T) {
	// Bottom (default): the LAST line sits ltBottomPad (61) off the bottom; earlier
	// lines step up by ltLineH (58). With 4 lines: i=3 -> h-61, i=0 -> h-235.
	if got := lineYExpr("bottom", 3, 4); got != "h-61" {
		t.Fatalf("bottom last line y = %q, want h-61", got)
	}
	if got := lineYExpr("bottom", 0, 4); got != "h-235" {
		t.Fatalf("bottom top line y = %q, want h-235", got)
	}
	// Top: the FIRST line sits ltTopPad (20) off the top; later lines step down by 58.
	if got := lineYExpr("top", 0, 4); got != "20" {
		t.Fatalf("top first line y = %q, want 20", got)
	}
	if got := lineYExpr("top", 2, 4); got != "136" {
		t.Fatalf("top third line y = %q, want 136", got)
	}
	// Unknown position defaults to bottom-anchored.
	if got := lineYExpr("middle", 0, 1); got != "h-61" {
		t.Fatalf("unknown position should default bottom, got %q", got)
	}
}

func TestCodecQualityArgs(t *testing.T) {
	tests := []struct {
		name string
		opts resolvedOpts
		want []string
	}{
		{"explicit bitrate wins", resolvedOpts{Codec: "h264_nvenc", Bitrate: "12M"}, []string{"-b:v", "12M"}},
		{"nvenc cq", resolvedOpts{Codec: "h264_nvenc"}, []string{"-rc", "vbr", "-cq", "19"}},
		{"libx264 crf", resolvedOpts{Codec: "libx264"}, []string{"-crf", "18", "-preset", "medium"}},
		{"unknown codec none", resolvedOpts{Codec: "mpeg4"}, nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := codecQualityArgs(tc.opts); strings.Join(got, " ") != strings.Join(tc.want, " ") {
				t.Fatalf("codecQualityArgs = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestWriteFilterScriptIfNeeded is the regression for a real failure the
// frame-quantization fix exposed: a real 88-clip forensic reel's assembled
// ffmpeg command line came to 34,663 chars (each clip's lower-third + trim/
// atrim filter chain adds up fast) — over Windows' ~32,767-char CreateProcess
// limit, so ffmpeg never even launches ("The filename or extension is too
// long"). A short command line must pass through untouched; a long one must
// have "-filter_complex <graph>" swapped for "-filter_complex_script <file>"
// whose contents are the exact same graph.
func TestWriteFilterScriptIfNeeded(t *testing.T) {
	t.Run("short command line untouched", func(t *testing.T) {
		args := []string{"-y", "-filter_complex", "short graph", "-map", "[vout]", "out.mp4"}
		got, cleanup, err := writeFilterScriptIfNeeded(args)
		defer cleanup()
		if err != nil {
			t.Fatalf("writeFilterScriptIfNeeded: %v", err)
		}
		if strings.Join(got, "|") != strings.Join(args, "|") {
			t.Fatalf("short args should pass through unchanged, got %v", got)
		}
	})

	t.Run("long command line rewritten to a script file", func(t *testing.T) {
		graph := strings.Repeat("[0:v]scale=1280:720,trim=end_frame=60[v0];", 1000)
		args := []string{"-y", "-filter_complex", graph, "-map", "[vout]", "out.mp4"}
		got, cleanup, err := writeFilterScriptIfNeeded(args)
		defer cleanup()
		if err != nil {
			t.Fatalf("writeFilterScriptIfNeeded: %v", err)
		}
		if !containsSubseq(got, []string{"-filter_complex_script"}) {
			t.Fatalf("expected -filter_complex_script in rewritten args: %v", got)
		}
		if strings.Contains(strings.Join(got, " "), graph) {
			t.Fatal("the raw graph must NOT still be inline once rewritten")
		}
		idx := indexOf(got, "-filter_complex_script")
		if idx < 0 || idx+1 >= len(got) {
			t.Fatalf("-filter_complex_script missing its path arg: %v", got)
		}
		contents, err := os.ReadFile(got[idx+1])
		if err != nil {
			t.Fatalf("reading the script file: %v", err)
		}
		if string(contents) != graph {
			t.Fatal("script file contents must be the EXACT same graph that was rewritten")
		}
		if _, statErr := os.Stat(got[idx+1]); statErr != nil {
			t.Fatal("script file should still exist before cleanup runs")
		}
		cleanup()
		if _, statErr := os.Stat(got[idx+1]); statErr == nil {
			t.Fatal("cleanup should have removed the temp script file")
		}
	})
}

func TestShouldFallbackToLibx264(t *testing.T) {
	if !shouldFallbackToLibx264("h264_nvenc") {
		t.Fatal("nvenc should be eligible for libx264 fallback")
	}
	if shouldFallbackToLibx264("libx264") {
		t.Fatal("libx264 should not fall back to itself")
	}
	if !shouldFallbackToLibx264("hevc_nvenc") {
		t.Fatal("hevc_nvenc should be eligible for fallback")
	}
}

func TestGrabFrameArgs(t *testing.T) {
	args := grabFrameArgs(`X:\c\v.mp4`, 14.567, `X:\out\still.png`)
	joined := strings.Join(args, " ")
	for _, want := range []string{"-ss 14.567000", `-i X:\c\v.mp4`, "-frames:v 1", "-update 1", `X:\out\still.png`} {
		if !strings.Contains(joined, want) {
			t.Fatalf("grabFrameArgs missing %q in:\n%s", want, joined)
		}
	}
	if indexOf(args, "-ss") > indexOf(args, "-i") {
		t.Fatal("-ss must come before -i for accurate seek")
	}
}

func TestGrabThumbArgs(t *testing.T) {
	args := grabThumbArgs(`X:\c\v.mp4`, 8179.792, `X:\out\t.jpg`, 160)
	joined := strings.Join(args, " ")
	for _, want := range []string{"-noaccurate_seek", "-ss 8179.792000", `-i X:\c\v.mp4`, "-frames:v 1", "scale=160:-2", "-q:v 6", `X:\out\t.jpg`} {
		if !strings.Contains(joined, want) {
			t.Fatalf("grabThumbArgs missing %q in:\n%s", want, joined)
		}
	}
	if indexOf(args, "-ss") > indexOf(args, "-i") {
		t.Fatal("-ss must come before -i for a fast keyframe seek")
	}
}

func TestGrabThumbTailArgs(t *testing.T) {
	args := grabThumbTailArgs(`X:\c\v.mp4`, `X:\out\t.jpg`, 120)
	joined := strings.Join(args, " ")
	for _, want := range []string{"-sseof -1", `-i X:\c\v.mp4`, "-frames:v 1", "scale=120:-2", `X:\out\t.jpg`} {
		if !strings.Contains(joined, want) {
			t.Fatalf("grabThumbTailArgs missing %q in:\n%s", want, joined)
		}
	}
}

func TestProxyArgs(t *testing.T) {
	args := proxyArgs(`X:\c\exotic.mkv`, `X:\out\exotic.proxy.mp4`)
	joined := strings.Join(args, " ")
	for _, want := range []string{"-c:v libx264", "-preset veryfast", "-movflags +faststart", "-c:a aac"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("proxyArgs missing %q in:\n%s", want, joined)
		}
	}
}

func TestNeedsProxy(t *testing.T) {
	tests := []struct {
		codec string
		want  bool
	}{
		{"h264", false}, {"H264", false}, {"vp9", false}, {"av1", false},
		{"hevc", true}, {"prores", true}, {"mpeg2video", true}, {"", true},
	}
	for _, tc := range tests {
		if got := needsProxy(tc.codec); got != tc.want {
			t.Fatalf("needsProxy(%q) = %v, want %v", tc.codec, got, tc.want)
		}
	}
}

func TestProxyPath(t *testing.T) {
	if got := proxyPath(`X:\c\exotic clip.mkv`, `X:\out`); !strings.HasSuffix(got, "exotic clip.proxy.mp4") {
		t.Fatalf("proxyPath = %q, want suffix exotic clip.proxy.mp4", got)
	}
}

// TestScrubProxyArgs asserts the scrub proxy is INTRA-FRAME (every frame a
// keyframe) and CONSTANT-frame-rate — the actual fix for laggy scrubbing. It
// checks VALUES (the GOP/scene-cut/fps flags), not that the slice is non-empty.
func TestScrubProxyArgs(t *testing.T) {
	t.Setenv("BECKY_PROXY_CODEC", "")
	t.Setenv("BECKY_PROXY_RES", "")
	joined := strings.Join(scrubProxyArgs(`X:\c\longgop.mp4`, `X:\out\longgop.scrub.mp4`), " ")
	for _, want := range []string{
		"-g 1",                // every frame is a keyframe
		"-keyint_min 1",       // ...minimum too, so no encoder GOP coalescing
		"-sc_threshold 0",     // no scene-cut GOPs
		"scale=-2:540,fps=30", // downscale + CONSTANT 30fps (kills VFR frame-step lag)
		"-c:v libx264", "-crf 20", "-movflags +faststart", "-c:a aac",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("scrubProxyArgs missing %q in:\n%s", want, joined)
		}
	}
}

// TestScrubProxyArgsEnv covers the env-tunable codec/resolution paths so the
// dnxhr/mjpeg recipes and BECKY_PROXY_RES override don't silently regress.
func TestScrubProxyArgsEnv(t *testing.T) {
	t.Run("dnxhr", func(t *testing.T) {
		t.Setenv("BECKY_PROXY_CODEC", "dnxhr")
		t.Setenv("BECKY_PROXY_RES", "")
		joined := strings.Join(scrubProxyArgs(`X:\c\src.mp4`, `X:\out\src.scrub.mov`), " ")
		for _, want := range []string{"-c:v dnxhd", "-profile:v dnxhr_lb", "-pix_fmt yuv422p", "-c:a pcm_s16le", "scale=-2:540,fps=30"} {
			if !strings.Contains(joined, want) {
				t.Fatalf("dnxhr scrubProxyArgs missing %q in:\n%s", want, joined)
			}
		}
	})
	t.Run("mjpeg", func(t *testing.T) {
		t.Setenv("BECKY_PROXY_CODEC", "mjpeg")
		t.Setenv("BECKY_PROXY_RES", "")
		joined := strings.Join(scrubProxyArgs(`X:\c\src.mp4`, `X:\out\src.scrub.mov`), " ")
		for _, want := range []string{"-c:v mjpeg", "-q:v 5", "-pix_fmt yuvj420p"} {
			if !strings.Contains(joined, want) {
				t.Fatalf("mjpeg scrubProxyArgs missing %q in:\n%s", want, joined)
			}
		}
	})
	t.Run("res override", func(t *testing.T) {
		t.Setenv("BECKY_PROXY_CODEC", "")
		t.Setenv("BECKY_PROXY_RES", "720")
		joined := strings.Join(scrubProxyArgs(`X:\c\src.mp4`, `X:\out\src.scrub.mp4`), " ")
		if !strings.Contains(joined, "scale=-2:720,fps=30") {
			t.Fatalf("BECKY_PROXY_RES=720 not honored in:\n%s", joined)
		}
	})
	t.Run("garbage res falls back to 540", func(t *testing.T) {
		t.Setenv("BECKY_PROXY_CODEC", "")
		t.Setenv("BECKY_PROXY_RES", "notanumber")
		joined := strings.Join(scrubProxyArgs(`X:\c\src.mp4`, `X:\out\src.scrub.mp4`), " ")
		if !strings.Contains(joined, "scale=-2:540,fps=30") {
			t.Fatalf("garbage BECKY_PROXY_RES should fall back to 540 in:\n%s", joined)
		}
	})
}

// TestScrubProxySegmentArgs asserts the WINDOWED scrub proxy brackets the
// requested span with an accurate input seek (-ss before -i) and a duration
// limit (-t after -i), while keeping the same intra-frame recipe as the
// whole-file scrub proxy. Checks VALUES, not just non-empty.
func TestScrubProxySegmentArgs(t *testing.T) {
	t.Setenv("BECKY_PROXY_CODEC", "")
	t.Setenv("BECKY_PROXY_RES", "")
	args := scrubProxySegmentArgs(`X:\c\longgop.mp4`, `X:\out\longgop.12000-17500.scrub.mp4`, 12.0, 17.5)
	joined := strings.Join(args, " ")
	for _, want := range []string{
		"-ss 12.000000",
		"-i " + `X:\c\longgop.mp4`,
		"-t 5.500000",
		"-g 1", "-keyint_min 1", "-sc_threshold 0", // intra-frame, same as scrubProxyArgs
		"scale=-2:540,fps=30",
		"-c:v libx264", "-crf 20", "-movflags +faststart", "-c:a aac",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("scrubProxySegmentArgs missing %q in:\n%s", want, joined)
		}
	}
	if indexOf(args, "-ss") > indexOf(args, "-i") {
		t.Fatal("-ss must come before -i for an accurate windowed seek")
	}
	if indexOf(args, "-i") > indexOf(args, "-t") {
		t.Fatal("-t must come after -i to bound the window's duration")
	}
}

// TestScrubProxySegmentPath asserts the "<stem>.<inMs>-<outMs>.scrub.<ext>"
// cache naming so distinct timeline windows of one source never collide.
func TestScrubProxySegmentPath(t *testing.T) {
	t.Setenv("BECKY_PROXY_CODEC", "")
	got := scrubProxySegmentPath(`X:\c\long gop.mp4`, `X:\out`, 12.0, 17.5)
	if !strings.HasSuffix(got, "long gop.12000-17500.scrub.mp4") {
		t.Fatalf("scrubProxySegmentPath = %q, want suffix long gop.12000-17500.scrub.mp4", got)
	}
}

// TestScrubProxyPath asserts the .scrub stem and the codec-driven extension.
func TestScrubProxyPath(t *testing.T) {
	t.Run("h264 default -> .mp4", func(t *testing.T) {
		t.Setenv("BECKY_PROXY_CODEC", "")
		if got := scrubProxyPath(`X:\c\long gop.mp4`, `X:\out`); !strings.HasSuffix(got, "long gop.scrub.mp4") {
			t.Fatalf("scrubProxyPath = %q, want suffix long gop.scrub.mp4", got)
		}
	})
	t.Run("dnxhr -> .mov", func(t *testing.T) {
		t.Setenv("BECKY_PROXY_CODEC", "dnxhr")
		if got := scrubProxyPath(`X:\c\long gop.mp4`, `X:\out`); !strings.HasSuffix(got, "long gop.scrub.mov") {
			t.Fatalf("scrubProxyPath = %q, want suffix long gop.scrub.mov", got)
		}
	})
}

func TestEscapeHelpers(t *testing.T) {
	if got := escapeFontPath(`C:\Windows\Fonts\consola.ttf`); got != `'C\:/Windows/Fonts/consola.ttf'` {
		t.Fatalf("escapeFontPath = %q", got)
	}
	if got := escapeColons("00:00:10:00"); got != `00\:00\:10\:00` {
		t.Fatalf("escapeColons = %q", got)
	}
	if got := escapeDrawtextText("a'b:c%d\\e"); got != `a\'b\:c\%d\\e` {
		t.Fatalf("escapeDrawtextText = %q", got)
	}
}

// TestFormatRate_NTSCUsesExactRational is the regression for a real measured
// bug: formatRate used to truncate the NTSC family to 3 decimals ("29.970" =
// 2997/100 exactly), which is a DIFFERENT rate than true NTSC 30000/1001 — a
// rendered file's own r_frame_rate came back 2997/100 because of this, silently
// re-introducing the container-vs-edit grid mismatch edl.NormalizeRate exists to
// remove. Any value within ntscTolerance of a family member (as NormalizeRate
// itself would produce) must emit the exact fraction.
func TestFormatRate_NTSCUsesExactRational(t *testing.T) {
	tests := []struct {
		in   float64
		want string
	}{
		{30, "30"}, {25, "25"}, {60, "60"},
		{24000.0 / 1001.0, "24000/1001"},
		{30000.0 / 1001.0, "30000/1001"},
		{60000.0 / 1001.0, "60000/1001"},
		{29.97, "30000/1001"},  // decimal approximation of the same rate
		{23.976, "24000/1001"}, // decimal approximation of the same rate
		{24.5, "24.5"},         // genuinely not NTSC — stays a plain decimal
	}
	for _, tc := range tests {
		if got := formatRate(tc.in); got != tc.want {
			t.Fatalf("formatRate(%v) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestFormatSeconds_MicrosecondPrecision is the regression for a real measured
// bug: at millisecond precision, a true-NTSC frame boundary like 3.336666...s
// (frame 100 @ 30000/1001) rounds UP to 3.337 — PAST the frame's actual PTS —
// which makes ffmpeg's accurate -ss seek skip that frame and land one frame
// late. Measured: a synthetic 150-frame/3-clip render came out 149 frames.
// Microsecond precision (6 decimals) is far finer than any real container's
// frame-time tick, so it can no longer cross a frame boundary.
func TestFormatSeconds_MicrosecondPrecision(t *testing.T) {
	if got := formatSeconds(2); got != "2.000000" {
		t.Fatalf("formatSeconds(2) = %q", got)
	}
	if got := formatSeconds(-5); got != "0.000000" {
		t.Fatalf("negative seconds should clamp to 0.000000, got %q", got)
	}
	// The actual regression case: frame 100 at true NTSC.
	frame100 := 100.0 * 1001.0 / 30000.0
	if got := formatSeconds(frame100); got != "3.336667" {
		t.Fatalf("formatSeconds(frame 100 @ NTSC) = %q, want 3.336667 (rounds to the frame's OWN boundary, not past it)", got)
	}
}

func TestSlug(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"penguin bounty", "penguin-bounty"},
		{"Case #42: Threats!!!", "case-42-threats"},
		{"   ", "becky"},
		{"already-slug", "already-slug"},
	}
	for _, tc := range tests {
		if got := slug(tc.in); got != tc.want {
			t.Fatalf("slug(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestRender_FrameCountMatchesReelExactly is the end-to-end regression for the
// measured drift bug: it actually RENDERS a multi-clip reel through real ffmpeg
// (skipped when ffmpeg isn't on this box — same gate as the tests below; CI's
// free runners have no ffmpeg, this only runs locally) and asserts the output's
// video stream has EXACTLY the frame count the clips' math says it should —
// not "close enough". On a real 88-clip forensic reel this bug cost 38 frames
// (1.27s) of drift, sliding burned captions out of sync with the picture.
func TestRender_FrameCountMatchesReelExactly(t *testing.T) {
	ffmpeg := ffmpegPath()
	if ffmpeg == "" {
		t.Skip("ffmpeg not on PATH; frame-accuracy is verified locally, not in CI")
	}
	ffprobe := ffprobePath()
	if ffprobe == "" {
		t.Skip("ffprobe not on PATH")
	}
	dir := t.TempDir()
	src := filepath.Join(dir, "src.mp4")
	const rate = 30000.0 / 1001.0 // true NTSC — the grid this whole fix is about
	// A tiny synthetic source with an EXACT, known frame count (-frames:v pins it,
	// sidestepping any ambiguity in how long a lavfi source "actually" runs).
	genArgs := []string{"-y", "-hide_banner", "-loglevel", "error",
		"-f", "lavfi", "-i", "color=c=blue:s=64x64:rate=30000/1001",
		"-frames:v", "150", "-pix_fmt", "yuv420p", src}
	if out, err := exec.Command(ffmpeg, genArgs...).CombinedOutput(); err != nil {
		t.Fatalf("generating synthetic source: %v\n%s", err, out)
	}

	// Three clips cut from the SAME 150-frame source at frame-exact boundaries
	// (0-50, 50-100, 100-150) — mirrors a real multi-cut reel's per-segment math.
	frameTime := func(f int) float64 { return float64(f) * 1001.0 / 30000.0 }
	r := edl.Reel{Version: "1", Name: "frame-exact-check", Clips: []edl.Clip{
		{ID: "c1", Source: src, In: frameTime(0), Out: frameTime(50), Meta: edl.ClipMeta{SourceFPS: rate}},
		{ID: "c2", Source: src, In: frameTime(50), Out: frameTime(100), Meta: edl.ClipMeta{SourceFPS: rate}},
		{ID: "c3", Source: src, In: frameTime(100), Out: frameTime(150), Meta: edl.ClipMeta{SourceFPS: rate}},
	}}

	out := filepath.Join(dir, "out.mp4")
	res, err := Render(r, Options{Output: out, Codec: "libx264", Width: 64, Height: 64, FPS: rate})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if res.Clips != 3 {
		t.Fatalf("res.Clips = %d, want 3", res.Clips)
	}

	got, err := ffprobeFrameCount(ffprobe, out)
	if err != nil {
		t.Fatalf("ffprobe the render: %v", err)
	}
	const wantFrames = 150 // 50+50+50, exactly what the reel's own math says
	if got != wantFrames {
		t.Fatalf("rendered video has %d frames, want EXACTLY %d (reel duration=%.6fs, drift=%.6fs = %d frames)",
			got, wantFrames, r.Duration(), float64(got-wantFrames)/rate, got-wantFrames)
	}
}

func TestRenderDegradesWithoutFFmpeg(t *testing.T) {
	// With ffmpeg absent, Render must return a clean error, not panic.
	if ffmpegPath() != "" {
		t.Skip("ffmpeg present; the no-ffmpeg degrade path is not exercised here")
	}
	if _, err := Render(twoClipReel(), Options{}); err == nil {
		t.Fatal("expected an error when ffmpeg is unavailable")
	}
}

// --- helpers used only by tests ---

func indexOf(s []string, v string) int {
	for i, x := range s {
		if x == v {
			return i
		}
	}
	return -1
}

// containsSubseq reports whether want appears as a contiguous run in s.
func containsSubseq(s, want []string) bool {
	if len(want) == 0 {
		return true
	}
	for i := 0; i+len(want) <= len(s); i++ {
		ok := true
		for j := range want {
			if s[i+j] != want[j] {
				ok = false
				break
			}
		}
		if ok {
			return true
		}
	}
	return false
}

// --- caption burn-in -------------------------------------------------------
// Regression guard for the bug that cost Jordan a whole day: captions showed in
// the preview but the RENDERED file had none, because the render never saw the
// .srt. If the burn ever falls out of the graph again, these fail.

func TestBuildFilterComplex_BurnsCaptionsAfterConcat(t *testing.T) {
	r := twoClipReel()
	opts := resolveOptions(r, Options{SubtitleSRT: `X:\case\reel.srt`, SubtitleMarginV: 72},
		config.Config{Codec: "h264_nvenc"})
	graph, vOut, _ := buildFilterComplex(r, opts)

	if vOut != "[vsub]" {
		t.Fatalf("video output label = %q, want [vsub] (the render must -map the CAPTIONED video)", vOut)
	}
	// The burn hangs off the concat's output, not off a clip.
	if !strings.Contains(graph, "[vout]subtitles=") {
		t.Fatalf("captions not burned onto the concat output:\n%s", graph)
	}
	if !strings.Contains(graph, "reel.srt") {
		t.Fatalf("the .srt is not in the filter graph:\n%s", graph)
	}
	// The height Jordan dragged to must survive into the burn.
	if !strings.Contains(graph, "MarginV=72") {
		t.Fatalf("MarginV=72 (the placement set in the review app) missing:\n%s", graph)
	}
	if !strings.HasSuffix(graph, "[vsub]") {
		t.Fatalf("burn chain must be last and end at [vsub]:\n%s", graph)
	}
}

func TestBuildRenderArgs_MapsCaptionedVideo(t *testing.T) {
	r := twoClipReel()
	opts := resolveOptions(r, Options{SubtitleSRT: `X:\case\reel.srt`},
		config.Config{Codec: "h264_nvenc"})
	opts.Audio = true
	opts.ClipHasAudio = []bool{true, true}
	args, err := buildRenderArgs(r, opts)
	if err != nil {
		t.Fatalf("buildRenderArgs: %v", err)
	}
	// -map must point at the captioned label; mapping [vout] would ship an
	// uncaptioned render with the burn silently unused.
	var mapped []string
	for i, a := range args {
		if a == "-map" && i+1 < len(args) {
			mapped = append(mapped, args[i+1])
		}
	}
	if len(mapped) != 2 || mapped[0] != "[vsub]" {
		t.Fatalf("-map targets = %v, want [vsub] first", mapped)
	}
	if opts.SubtitleMarginV != 0 {
		t.Fatalf("unset MarginV must stay 0 so DefaultStyle applies, got %d", opts.SubtitleMarginV)
	}
}

// No .srt must leave the graph EXACTLY as it was — the burn is opt-in.
func TestBuildFilterComplex_NoCaptionsLeavesGraphUnchanged(t *testing.T) {
	r := twoClipReel()
	opts := resolveOptionsForTest(r)
	graph, vOut, _ := buildFilterComplex(r, opts)
	if vOut != "[vout]" {
		t.Fatalf("video output label = %q, want [vout]", vOut)
	}
	if strings.Contains(graph, "subtitles=") {
		t.Fatalf("no .srt was given but a subtitles filter appeared:\n%s", graph)
	}
}
