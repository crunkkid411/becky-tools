package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"becky-go/internal/mediainfo"
	"becky-go/internal/subs"
	"becky-go/internal/transcribex"
)

// Captions for a short.
//
// The whole job is one call into internal/subs, because a short IS the
// degenerate case of a reel: one kept span of one source. subs.Build lays
// segments end to end to derive the OUTPUT timeline, so a single segment comes
// back already clip-relative, starting at zero — which is exactly what a burn
// into the rendered clip needs. Nothing here re-times anything by hand.
//
// That also means the cut-snap is free: WordsPerSegment slices the source's
// words to [Start,End], so a word straddling the in-point belongs to whichever
// side actually contains it rather than being duplicated or dropped.

// capWordPad is how far outside the clip a word may sit and still be treated as
// belonging to it - see captionSRT.
const capWordPad = 0.25

// captionSRT writes a frame-aligned .srt for ONE clip window into dir and
// returns its absolute path plus the cue count.
//
// fps must be the media's REAL rate (29.97 = 30000/1001, never 30). A frame at
// 29.97 is 33.3667ms, which is not a whole number of milliseconds, so captions
// quantised at millisecond precision drift — over a long clip that becomes
// visible, and captions are the most visible element of a short.
func captionSRT(video string, in, out, fps float64, dir string, logf transcribex.Logf) (string, int, error) {
	words, _, err := transcribex.EnsureWords(video, logf)
	if err != nil {
		return "", 0, err
	}
	cues := captionCues(words, in, out, fps)
	if len(cues) == 0 {
		return "", 0, nil
	}
	return writeSRT(cues, dir)
}

// captionCues turns a source transcript into cues on the CLIP timeline. Pure -
// no I/O - so the timing rules can be pinned by a test.
func captionCues(words []subs.Word, in, out, fps float64) []subs.Cue {
	// Slice the transcript to THIS window before internal/subs sees it.
	//
	// WordsPerSegment rescues a word that overlaps no segment by retiming it onto
	// the nearest same-source cut - correct for a reel, whose clips tile the
	// source, and badly wrong for a short, which is ONE excerpt from a long
	// video. Unsliced, every word of a five-minute source was rescued onto a
	// 28-second clip: measured here, "before" at 100s and "after" at 200s both
	// landed in the cues for the window [146.2, 174.6]. It renders fine and reads
	// as gibberish.
	//
	// The pad keeps the rescue doing its real job. Parakeet's clock drifts
	// against the cut points - measured at 79-89ms past a kept clip's edge on the
	// 27_walmart footage, still clearly audible inside it - and Jordan's rule is
	// that the cut is ground truth and the transcript is the weaker signal. 0.25s
	// is comfortably past that drift and nowhere near a neighbouring sentence.
	words = subs.WordsInRange(words, in-capWordPad, out+capWordPad)
	if len(words) == 0 {
		return nil
	}

	opt := subs.DefaultOptions()
	// The pause threshold comes from THIS transcript's own word gaps. 49% of
	// Parakeet's words carry end == start, so a constant tuned on another ASR
	// shatters a Parakeet transcript into one caption per word.
	opt.GapSeconds = subs.AutoGapSeconds(words)
	opt.FPS = fps

	return subs.Build([]subs.Segment{{
		Source: "clip", Start: in, End: out, Words: words,
	}}, opt)
}

// captionSidecarPath is where a rendered short's burned captions are saved
// beside the OUTPUT file — same "sidecar beside the video" convention
// transcribex uses for a source's own transcript. This is what lets
// `becky-short --review clip.mp4` find the captions that were actually burned
// with no flag needed; --review-srt still overrides it for a file that was
// not rendered by this tool (or was deliberately doctored for a test).
func captionSidecarPath(dst string) string {
	return strings.TrimSuffix(dst, filepath.Ext(dst)) + ".srt"
}

// saveCaptionSidecar copies the .srt actually burned into dst's ffmpeg run to
// captionSidecarPath(dst), AFTER the render succeeded — so a failed render
// never leaves a sidecar with no matching video.
func saveCaptionSidecar(dst, srtPath string) error {
	data, err := os.ReadFile(srtPath)
	if err != nil {
		return err
	}
	return os.WriteFile(captionSidecarPath(dst), data, 0o644)
}

// writeSRT puts the cues beside the sendcmd script so ffmpeg can find both.
func writeSRT(cues []subs.Cue, dir string) (string, int, error) {
	path := filepath.Join(dir, "captions.srt")
	f, err := os.Create(path)
	if err != nil {
		return "", 0, err
	}
	if err := subs.WriteSRT(f, cues); err != nil {
		f.Close()
		return "", 0, err
	}
	if err := f.Close(); err != nil {
		return "", 0, err
	}
	return path, len(cues), nil
}

// captionFilter is the ffmpeg -vf fragment that burns srtPath in the shipped
// cli-cut style, honouring any height Jordan dragged the captions to in a review
// app (the .capstyle.json sidecar beside the SOURCE transcript).
//
// An absolute path is correct here and would NOT be for the crop path: the
// subtitles filter escapes a Windows drive colon, sendcmd's parser does not.
func captionFilter(srtPath, styleFrom string) string {
	st := subs.DefaultStyle()
	if m := subs.LoadMarginV(styleFrom); m > 0 {
		st.MarginV = m
	}
	return st.SubtitlesFilter(srtPath)
}

// sourceFPS reads the media's true frame rate. A failure is not fatal: 0 means
// "do not quantise", which is worse than frame-aligned but far better than
// refusing to caption, and the caller says so in its note.
func sourceFPS(ffprobe, video string) (float64, error) {
	info, err := mediainfo.Probe(ffprobe, video)
	if err != nil {
		return 0, err
	}
	if info.FPS <= 0 {
		return 0, fmt.Errorf("no frame rate reported for %s", filepath.Base(video))
	}
	return info.FPS, nil
}
