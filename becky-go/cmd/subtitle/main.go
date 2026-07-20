// becky-subtitle — build TikTok-style captions whose timing is SNAPPED to the
// edit's cut points, and optionally burn them into the rendered video:
//
//	becky-subtitle --reel <reel.json> --transcript <transcript.json> [--out master.srt]
//	becky-subtitle --reel <reel.json> --burn <rendered.mp4>
//	becky-subtitle --selftest
//
// Why this exists: a caption timed off the raw transcript drifts against the
// cut, so at every cut a caption blinks on or off for a few frames. That flash
// is jarring, and it is why these captions were still being done by hand. The
// fix is to snap each caption to the cut it lives in and close every gap
// between captions — the rules ported from cli-cut's build_master_srt, see
// internal/subs.
//
// Chunking is pace-driven: break on the speaker's pauses (--gap) or line length
// (--max-chars), NOT on a fixed word count.
//
// JSON report to stdout, diagnostics to stderr, non-zero exit on fatal error.
// Pure Go + one optional ffmpeg exec for --burn. Source media is never modified:
// --burn writes a NEW file.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"becky-go/internal/beckyio"
	"becky-go/internal/config"
	"becky-go/internal/edl"
	"becky-go/internal/proc"
	"becky-go/internal/subs"
)

type report struct {
	Reel     string   `json:"reel"`
	SRT      string   `json:"srt"`
	Cues     int      `json:"cues"`
	Clips    int      `json:"clips"`
	Duration float64  `json:"duration"`
	PauseGap float64  `json:"pause_gap"` // the pause threshold actually used
	Style    string   `json:"style"`
	Burned   bool     `json:"burned"`
	Output   string   `json:"output,omitempty"`
	Warnings []string `json:"warnings,omitempty"`
}

// transcriptFile is the slice of becky-transcribe's JSON we need. Word-level
// timings are required: segment-level cues are already grouped for reading, and
// re-chunking them cannot recover the pauses caption pacing depends on.
type transcriptFile struct {
	Words []subs.Word `json:"words"`
}

func main() {
	fs := flag.NewFlagSet("becky-subtitle", flag.ExitOnError)
	reelPath := fs.String("reel", "", "path to a Reel JSON (the edit: what was kept, in order)")
	editPath := fs.String("edit", "", "instead of --reel: an already-cut edit exported from your NLE (Vegas 'EDL TXT' .txt or Final Cut 7 .xml). Imported in place, no separate step")
	transcript := fs.String("transcript", "", "becky-transcribe JSON for the source media (word-level). Omit to look for a sidecar beside each source")
	autoTranscribe := fs.Bool("transcribe", true, "when a source has no transcript, run becky-transcribe to make one (this is the slow step)")
	out := fs.String("out", "", "output .srt path (default: <reel name>.srt beside the reel)")
	burn := fs.String("burn", "", "also burn the captions into this rendered video (writes a NEW file)")
	burnOut := fs.String("burn-out", "", "output path for --burn (default: <video>.captioned.mp4)")
	vcodec := fs.String("vcodec", "libx264", "video codec for the --burn re-encode")
	crf := fs.String("crf", "18", "quality for the --burn re-encode (lower is better)")

	maxChars := fs.Int("max-chars", 22, "break a caption once the line would exceed this many characters")
	gap := fs.Float64("gap", -1, "break a caption when the speaker pauses longer than this (seconds). -1 = derive it from the transcript's own timing, which is what you want unless you are matching an old render")
	minDur := fs.Float64("min-dur", 0.10, "minimum seconds a caption stays on screen")
	hold := fs.Float64("hold", 0.35, "carry the last caption across an inter-cut gap no longer than this")
	lower := fs.Bool("lower", false, "lowercase the captions and drop trailing punctuation (the old cli-cut look). Default keeps sentence case, matching the captions you actually publish")

	font := fs.String("font", "ProximaNova-Semibold", "caption font FAMILY name (libass matches the family, not the filename)")
	fontSize := fs.Int("font-size", 12, "caption font size")
	bold := fs.Int("bold", 0, "1 to force bold")
	marginV := fs.Int("margin-v", 90, "how far the captions sit above the bottom edge")
	outline := fs.Int("outline", 2, "black outline thickness")

	verbose := fs.Bool("verbose", false, "log progress to stderr")
	selftest := fs.Bool("selftest", false, "run the offline self-test (no files needed) and exit")
	_ = fs.Parse(os.Args[1:])

	if *selftest {
		runSelftest()
		return
	}
	if *reelPath == "" && *editPath == "" {
		beckyio.Fatalf("--reel <reel.json> or --edit <vegas .txt|.xml> is required (or use --selftest)")
	}

	// One call does the whole job: an NLE edit is imported in place, so there is
	// no separate import step to remember.
	var reel edl.Reel
	var preWarnings []string
	basePath := *reelPath
	if *editPath != "" {
		res, err := edl.ImportTimeline(*editPath)
		if err != nil {
			beckyio.Fatalf("import %s: %v", filepath.Base(*editPath), err)
		}
		reel = res.Reel
		basePath = *editPath
		beckyio.Logf(*verbose, "imported %d cuts from %s (%s)", len(reel.Clips), filepath.Base(*editPath), res.Format)
		for _, u := range res.Unresolved {
			preWarnings = append(preWarnings, "source not found on disk: "+u)
		}
	} else {
		r, err := loadReel(*reelPath)
		if err != nil {
			beckyio.Fatalf("%v", err)
		}
		reel = r
	}
	if len(reel.Clips) == 0 {
		beckyio.Fatalf("edit has no clips: %s", basePath)
	}

	opt := subs.Options{
		MaxChars:       *maxChars,
		GapSeconds:     *gap,
		MinDuration:    *minDur,
		PostSpeechHold: *hold,
		Lowercase:      *lower,
	}
	style := subs.Style{
		FontName: *font,
		FontSize: *fontSize,
		Bold:     *bold,
		MarginV:  *marginV,
		Outline:  *outline,
	}

	segments, allWords, warnings := segmentsFor(reel, *transcript, *autoTranscribe, *verbose)
	warnings = append(preWarnings, warnings...)
	if *gap < 0 {
		opt.GapSeconds = subs.AutoGapSeconds(allWords)
		beckyio.Logf(*verbose, "auto pause threshold: %.3fs (from %d words)", opt.GapSeconds, len(allWords))
	}
	cues := subs.Build(segments, opt)
	if len(cues) == 0 {
		warnings = append(warnings, "no captions produced — check that the transcript covers this edit's source ranges")
	}

	srtPath := *out
	if srtPath == "" {
		srtPath = filepath.Join(filepath.Dir(basePath), reelName(reel, basePath)+".srt")
	}
	f, err := os.Create(srtPath)
	if err != nil {
		beckyio.Fatalf("write %s: %v", srtPath, err)
	}
	if err := subs.WriteSRT(f, cues); err != nil {
		f.Close()
		beckyio.Fatalf("write %s: %v", srtPath, err)
	}
	f.Close()
	beckyio.Logf(*verbose, "wrote %d captions -> %s", len(cues), srtPath)

	rep := report{
		Reel:     mustAbs(basePath),
		SRT:      mustAbs(srtPath),
		Cues:     len(cues),
		Clips:    len(reel.Clips),
		Duration: round3(reel.Duration()),
		PauseGap: round3(opt.GapSeconds),
		Style:    style.ForceStyle(),
		Warnings: warnings,
	}

	if *burn != "" {
		dst := *burnOut
		if dst == "" {
			dst = strings.TrimSuffix(*burn, filepath.Ext(*burn)) + ".captioned.mp4"
		}
		if err := burnIn(*burn, srtPath, dst, style, *vcodec, *crf, *verbose); err != nil {
			rep.Warnings = append(rep.Warnings, "burn: "+err.Error())
		} else {
			rep.Burned = true
			rep.Output = mustAbs(dst)
		}
	}

	beckyio.PrintJSON(rep)
}

// segmentsFor maps every clip of the reel onto its source's word timings. The
// reel's clip order IS the output timeline, so the segments come back in that
// order and internal/subs lays them end to end.
//
// It also returns every distinct word loaded, so the caller can derive the
// pause threshold from the real transcript rather than a constant.
func segmentsFor(reel edl.Reel, explicit string, autoTranscribe, verbose bool) ([]subs.Segment, []subs.Word, []string) {
	var warnings []string
	var allWords []subs.Word
	cache := map[string][]subs.Word{}
	missing := map[string]bool{}

	var explicitWords []subs.Word
	if explicit != "" {
		w, err := loadTranscript(explicit)
		if err != nil {
			beckyio.Fatalf("%v", err)
		}
		explicitWords = w
		allWords = append(allWords, w...)
		beckyio.Logf(verbose, "loaded %d words from %s", len(w), explicit)
	}

	segs := make([]subs.Segment, 0, len(reel.Clips))
	for _, c := range reel.Clips {
		words := explicitWords
		if explicit == "" {
			if w, ok := cache[c.Source]; ok {
				words = w
			} else {
				path := findTranscript(c.Source)
				if path == "" && autoTranscribe && !missing[c.Source] {
					// No transcript yet: make one. This is the slow step, so say
					// so on stderr rather than looking hung.
					sidecar := defaultSidecar(c.Source)
					fmt.Fprintf(os.Stderr, "transcribing %s (one-time, this is the slow part)...\n", filepath.Base(c.Source))
					if err := runTranscribe(c.Source, sidecar, verbose); err != nil {
						warnings = append(warnings, "auto-transcribe "+filepath.Base(c.Source)+": "+err.Error())
					} else {
						path = sidecar
					}
				}
				if path == "" {
					if !missing[c.Source] {
						missing[c.Source] = true
						warnings = append(warnings, "no transcript found for "+filepath.Base(c.Source)+
							" — run: becky-transcribe \""+c.Source+"\" --format json --output \""+
							defaultSidecar(c.Source)+"\"")
					}
				} else if w, err := loadTranscript(path); err != nil {
					warnings = append(warnings, "transcript "+filepath.Base(path)+": "+err.Error())
				} else {
					beckyio.Logf(verbose, "loaded %d words from %s", len(w), path)
					words = w
					allWords = append(allWords, w...)
				}
				cache[c.Source] = words
			}
		}
		segs = append(segs, subs.Segment{Start: c.In, End: c.Out, Words: words})
	}
	return segs, allWords, warnings
}

// findTranscript looks for a word-level transcript beside the source video,
// newest convention first. Returns "" when there is none.
func findTranscript(source string) string {
	dir := filepath.Dir(source)
	stem := strings.TrimSuffix(filepath.Base(source), filepath.Ext(source))
	for _, cand := range []string{
		filepath.Join(dir, stem+".transcript.json"),
		source + ".transcript.json",
		filepath.Join(dir, "transcripts", stem+".json"),
	} {
		if fi, err := os.Stat(cand); err == nil && !fi.IsDir() {
			return cand
		}
	}
	return ""
}

// runTranscribe shells out to becky-transcribe to produce the word-level JSON
// sidecar. Resolved from PATH first, then from this binary's own folder, so it
// works whether becky-subtitle was launched from bin\ or from a shortcut.
func runTranscribe(source, out string, verbose bool) error {
	exe, err := exec.LookPath("becky-transcribe")
	if err != nil {
		if self, e := os.Executable(); e == nil {
			cand := filepath.Join(filepath.Dir(self), "becky-transcribe.exe")
			if _, e := os.Stat(cand); e == nil {
				exe = cand
			}
		}
	}
	if exe == "" {
		return fmt.Errorf("becky-transcribe not found on PATH")
	}
	cmd := exec.Command(exe, source, "--format", "json", "--output", out)
	proc.NoWindow(cmd)
	if verbose {
		cmd.Stderr = os.Stderr
	}
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("becky-transcribe failed: %w", err)
	}
	if _, err := os.Stat(out); err != nil {
		return fmt.Errorf("becky-transcribe wrote no transcript to %s", out)
	}
	return nil
}

func defaultSidecar(source string) string {
	dir := filepath.Dir(source)
	stem := strings.TrimSuffix(filepath.Base(source), filepath.Ext(source))
	return filepath.Join(dir, stem+".transcript.json")
}

func loadTranscript(path string) ([]subs.Word, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read transcript %s: %w", path, err)
	}
	var t transcriptFile
	if err := json.Unmarshal(b, &t); err != nil {
		return nil, fmt.Errorf("parse transcript %s: %w", path, err)
	}
	if len(t.Words) == 0 {
		return nil, fmt.Errorf("transcript %s has no word-level timings (re-run becky-transcribe with --format json)", filepath.Base(path))
	}
	return t.Words, nil
}

func loadReel(path string) (edl.Reel, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return edl.Reel{}, fmt.Errorf("read reel %s: %w", path, err)
	}
	var r edl.Reel
	if err := json.Unmarshal(b, &r); err != nil {
		return edl.Reel{}, fmt.Errorf("parse reel %s: %w", path, err)
	}
	return r, nil
}

func reelName(r edl.Reel, path string) string {
	if r.Name != "" {
		return r.Name
	}
	base := filepath.Base(path)
	return strings.TrimSuffix(base, filepath.Ext(base))
}

// burnIn hard-burns the captions into a copy of the video. The source video is
// never modified — the result is written to dst.
func burnIn(video, srt, dst string, style subs.Style, vcodec, crf string, verbose bool) error {
	if _, err := os.Stat(video); err != nil {
		return fmt.Errorf("video not found: %s", video)
	}
	cfg := config.Load()
	args := []string{"-y", "-i", video,
		"-vf", style.SubtitlesFilter(srt),
		"-c:v", vcodec, "-crf", crf, "-c:a", "copy",
		"-loglevel", "error", dst}
	beckyio.Logf(verbose, "burning captions: %s %s", cfg.FFmpeg, strings.Join(args, " "))

	cmd := exec.Command(cfg.FFmpeg, args...)
	proc.NoWindow(cmd)
	cmd.Stderr = os.Stderr
	if verbose {
		cmd.Stdout = os.Stderr
	}
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("ffmpeg failed: %w", err)
	}
	if fi, err := os.Stat(dst); err != nil || fi.Size() == 0 {
		return fmt.Errorf("ffmpeg produced no output at %s", dst)
	}
	return nil
}

func mustAbs(p string) string {
	if abs, err := filepath.Abs(p); err == nil {
		return abs
	}
	return p
}

func round3(f float64) float64 { return float64(int(f*1000+0.5)) / 1000 }
