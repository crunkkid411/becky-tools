// enroll.go — build one person's enrollment sample (voice clip + face frame) from
// a wiki-referenced video, with zero human clip-making.
//
// VOICE: run becky-diarize on a referenced video, choose the span FOR THAT person
// (the .md's "who speaks" note picks the cluster; single-speaker videos are used
// directly), then ffmpeg-clip ~15-30s of that speaker's clean speech into
// KB/voice-prints/<Name>/. FACE: sample frames, embed faces (shared faceembed),
// pick the clearest (highest det_score) and write it to KB/face-prints/<Name>/.
//
// If no clean sample exists, the person is SKIPPED with a recorded reason — never
// fabricated. Source videos are read-only; nothing here writes to them.
package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"becky-go/internal/beckyio"
	"becky-go/internal/config"
	"becky-go/internal/faceembed"
	"becky-go/internal/mediainfo"
)

const (
	minVoiceClipSec = 15.0 // minimum clean-speech clip length to enroll a voice
	maxVoiceClipSec = 30.0 // cap clip length so enrollment stays bounded
	faceSampleEvery = 2.0  // sample one frame every N seconds for the face scan
	faceMaxFrames   = 30   // cap frames sampled per video for face selection
	faceMinDetScore = 0.55 // minimum InsightFace det_score to accept a face frame
	faceJPEGQuality = 2    // ffmpeg -q:v for the saved face frame (lower = better)
)

// EnrollResult is the per-person outcome recorded in the registry.
type EnrollResult struct {
	person      Person
	voiceClip   string // absolute path to the written .wav, or ""
	faceImage   string // absolute path to the written .jpg, or ""
	voiceVideo  string // the video the voice clip came from
	faceVideo   string // the video/image the face came from
	speakerID   string // diarized cluster used for the voice clip
	numSpeakers int    // speaker count in the voice video (>1 => multi-person clip)
	skipVoice   string // reason voice was skipped (empty if enrolled)
	skipFace    string // reason face was skipped (empty if enrolled)
}

// enrollOptions bundles the runtime knobs the enrollment step needs.
type enrollOptions struct {
	diarizeBin        string // path to becky-diarize.exe (for the voice path)
	device            string
	noFace            bool
	noVoice           bool
	includeNonSubject bool // also enroll legal professionals (attorneys/officers)
	verbose           bool
}

// enrollPerson builds the voice + face samples for one person and writes them into
// the KB. It never errors fatally per-person: failures become skip reasons.
func enrollPerson(cfg config.Config, kbDir string, p Person, opts enrollOptions) EnrollResult {
	res := EnrollResult{person: p}

	// Non-subjects (attorneys, officers, etc.) reference case EXHIBITS, not
	// recordings of themselves — auto-enrolling them yields false prints. Skip by
	// default; --include-non-subjects (or --only) overrides.
	if p.NonSubject && !opts.includeNonSubject {
		res.skipVoice = "non-subject role (legal professional); media are case exhibits, not recordings of them — use --only to force"
		res.skipFace = res.skipVoice
		return res
	}

	if !opts.noVoice {
		enrollVoice(cfg, kbDir, p, opts, &res)
	} else {
		res.skipVoice = "voice enrollment disabled (--no-voice)"
	}
	if !opts.noFace {
		enrollFace(cfg, kbDir, p, opts, &res)
	} else {
		res.skipFace = "face enrollment disabled (--no-face)"
	}
	return res
}

// enrollVoice tries each referenced video until it produces a clean voice clip.
func enrollVoice(cfg config.Config, kbDir string, p Person, opts enrollOptions, res *EnrollResult) {
	if len(p.VideoRefs) == 0 {
		res.skipVoice = "no referenced video files exist on disk"
		return
	}
	var lastReason string
	for _, video := range p.VideoRefs {
		info, err := mediainfo.Probe(cfg.FFprobe, video)
		if err != nil {
			lastReason = "ffprobe " + filepath.Base(video) + ": " + err.Error()
			continue
		}
		if !info.HasAudio {
			lastReason = filepath.Base(video) + ": no audio stream"
			continue
		}
		span, speaker, numSpeakers, reason := pickVoiceSpan(opts.diarizeBin, video, p, opts)
		if reason != "" {
			lastReason = filepath.Base(video) + ": " + reason
			continue
		}
		clip, werr := writeVoiceClip(cfg, kbDir, p.Name, video, span, opts.verbose)
		if werr != nil {
			lastReason = filepath.Base(video) + ": clip failed: " + werr.Error()
			continue
		}
		res.voiceClip = clip
		res.voiceVideo = video
		res.speakerID = speaker
		res.numSpeakers = numSpeakers
		beckyio.Logf(opts.verbose, "  voice: %s -> %s (%s %.1f-%.1fs)", p.Name, filepath.Base(clip), speaker, span.Start, span.End)
		return
	}
	if lastReason == "" {
		lastReason = "no clean single-speaker span found in referenced videos"
	}
	res.skipVoice = lastReason
}

// voiceSpan is a chosen (start,end) window of one speaker's clean speech.
type voiceSpan struct {
	Start float64
	End   float64
}

// pickVoiceSpan diarizes a video (via becky-diarize), chooses the cluster for this
// person, and returns a clean >= minVoiceClipSec window from it. Selection rules:
//   - single speaker -> use it directly.
//   - multiple speakers + a "who speaks" hint that maps to a SPEAKER_NN -> that one.
//   - otherwise -> the dominant (most total speech) speaker, recorded as a guess.
func pickVoiceSpan(diarizeBin, video string, p Person, opts enrollOptions) (voiceSpan, string, int, string) {
	diar, err := runDiarize(diarizeBin, video, opts.device, opts.verbose)
	if err != nil {
		return voiceSpan{}, "", 0, "diarize failed: " + err.Error()
	}
	if len(diar.Speakers) == 0 {
		return voiceSpan{}, "", 0, "diarization found no speakers"
	}
	numSpeakers := len(diar.Speakers)

	speaker := chooseSpeaker(diar, p)
	if speaker == nil {
		return voiceSpan{}, "", numSpeakers, "no speaker cluster selectable"
	}
	span, ok := longestCleanSpan(speaker.Segments)
	if !ok {
		return voiceSpan{}, "", numSpeakers, fmt.Sprintf("%s has no contiguous clean span >= %.0fs", speaker.ID, minVoiceClipSec)
	}
	return span, speaker.ID, numSpeakers, ""
}

// chooseSpeaker selects the diarized cluster to clip for this person. With a single
// cluster it is unambiguous; with multiple, a hint like "SPEAKER_01 = John" routes
// to that cluster, else the dominant (most speech) cluster is used as a best guess.
func chooseSpeaker(diar diarOutput, p Person) *diarSpeaker {
	if len(diar.Speakers) == 1 {
		return &diar.Speakers[0]
	}
	if id := speakerFromHint(p.SpeakerHint, p); id != "" {
		for i := range diar.Speakers {
			if strings.EqualFold(diar.Speakers[i].ID, id) {
				return &diar.Speakers[i]
			}
		}
	}
	return dominantSpeaker(diar.Speakers)
}

// speakerFromHint extracts a SPEAKER_NN id from a "who speaks" note that maps the
// person to a cluster (e.g. "SPEAKER_01 = John" or "John is SPEAKER_01"). It only
// returns an id when the note also references this person's name/alias.
func speakerFromHint(hint string, p Person) string {
	if hint == "" {
		return ""
	}
	if !nameMatches(hint, &p) {
		return ""
	}
	m := reSpeakerID.FindStringSubmatch(hint)
	if len(m) == 2 {
		return strings.ToUpper(m[1])
	}
	return ""
}

// dominantSpeaker returns the cluster with the most total speech time.
func dominantSpeaker(speakers []diarSpeaker) *diarSpeaker {
	bestIdx, bestDur := -1, -1.0
	for i := range speakers {
		dur := totalSpeech(speakers[i].Segments)
		if dur > bestDur {
			bestDur, bestIdx = dur, i
		}
	}
	if bestIdx < 0 {
		return nil
	}
	return &speakers[bestIdx]
}

// longestCleanSpan returns the longest single diarized segment for the speaker,
// clamped to maxVoiceClipSec, provided it reaches minVoiceClipSec. A single
// contiguous segment is "clean" speech for that speaker (no other talker in it).
func longestCleanSpan(segs []diarSegment) (voiceSpan, bool) {
	best := voiceSpan{}
	bestDur := 0.0
	for _, s := range segs {
		d := s.End - s.Start
		if d > bestDur {
			bestDur = d
			best = voiceSpan{Start: s.Start, End: s.End}
		}
	}
	if bestDur < minVoiceClipSec {
		return voiceSpan{}, false
	}
	if best.End-best.Start > maxVoiceClipSec {
		best.End = best.Start + maxVoiceClipSec
	}
	return best, true
}

func totalSpeech(segs []diarSegment) float64 {
	var sum float64
	for _, s := range segs {
		sum += s.End - s.Start
	}
	return sum
}

// writeVoiceClip extracts the chosen span as a 16 kHz mono WAV into the KB.
func writeVoiceClip(cfg config.Config, kbDir, name, video string, span voiceSpan, verbose bool) (string, error) {
	dir := filepath.Join(kbDir, "voice-prints", name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	clip := filepath.Join(dir, voiceClipName(video, span))
	args := []string{"-y", "-ss", fmt.Sprintf("%.3f", span.Start),
		"-to", fmt.Sprintf("%.3f", span.End), "-i", video,
		"-vn", "-ac", "1", "-ar", "16000", "-acodec", "pcm_s16le",
		"-loglevel", "error", clip}
	cmd := exec.Command(cfg.FFmpeg, args...)
	var errBuf strings.Builder
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		os.Remove(clip)
		return "", fmt.Errorf("ffmpeg: %v: %s", err, tail(errBuf.String()))
	}
	return clip, nil
}

// voiceClipName builds a descriptive clip filename: <stem>_<start>-<end>.wav.
func voiceClipName(video string, span voiceSpan) string {
	stem := strings.TrimSuffix(filepath.Base(video), filepath.Ext(video))
	stem = sanitizeStem(stem)
	return fmt.Sprintf("%s_%s-%s.wav", stem, secLabel(span.Start), secLabel(span.End))
}

// enrollFace samples frames from referenced videos (and tries referenced images),
// embeds faces, and writes the clearest face frame to the KB.
func enrollFace(cfg config.Config, kbDir string, p Person, opts enrollOptions, res *EnrollResult) {
	if cfg.FaceModelRoot == "" {
		res.skipFace = "face model root not configured"
		return
	}
	// Prefer a video (more frames to pick from); fall back to referenced images.
	var lastReason string
	for _, video := range p.VideoRefs {
		info, err := mediainfo.Probe(cfg.FFprobe, video)
		if err != nil || !info.HasVideo {
			lastReason = filepath.Base(video) + ": no video stream"
			continue
		}
		img, src, reason := bestFaceFromVideo(cfg, kbDir, p.Name, video, info, opts)
		if reason != "" {
			lastReason = filepath.Base(video) + ": " + reason
			continue
		}
		res.faceImage = img
		res.faceVideo = src
		beckyio.Logf(opts.verbose, "  face: %s -> %s", p.Name, filepath.Base(img))
		return
	}
	for _, image := range p.ImageRefs {
		img, reason := bestFaceFromImage(cfg, kbDir, p.Name, image, opts)
		if reason != "" {
			lastReason = filepath.Base(image) + ": " + reason
			continue
		}
		res.faceImage = img
		res.faceVideo = image
		beckyio.Logf(opts.verbose, "  face: %s -> %s (from still)", p.Name, filepath.Base(img))
		return
	}
	if lastReason == "" {
		lastReason = "no clear face frame in referenced media"
	}
	res.skipFace = lastReason
}

// bestFaceFromVideo samples frames, embeds faces, and copies the highest-det_score
// single-face frame into KB/face-prints/<Name>/. Frames with >1 face are skipped so
// the enrolled print is unambiguous (which face is the person?).
func bestFaceFromVideo(cfg config.Config, kbDir, name, video string, info mediainfo.Info, opts enrollOptions) (string, string, string) {
	frames, times, err := sampleFrames(cfg, video, info, opts.verbose)
	if err != nil {
		return "", "", "frame sampling failed: " + err.Error()
	}
	if len(frames) == 0 {
		return "", "", "no frames sampled"
	}
	defer os.RemoveAll(filepath.Dir(frames[0]))

	recs, err := faceembed.Embed(cfg, frames, opts.device, opts.verbose)
	if err != nil {
		return "", "", "face embed: " + err.Error()
	}
	bestIdx := bestSingleFace(recs)
	if bestIdx < 0 {
		return "", "", fmt.Sprintf("no single clear face (det_score >= %.2f)", faceMinDetScore)
	}
	ts := 0.0
	if bestIdx < len(times) {
		ts = times[bestIdx]
	}
	dst := filepath.Join(kbDir, "face-prints", name, faceFrameName(video, ts))
	if err := copyFile(frames[bestIdx], dst); err != nil {
		return "", "", "save face frame: " + err.Error()
	}
	return dst, video, ""
}

// bestFaceFromImage embeds a single referenced still image and, if it holds exactly
// one clear face, copies it into the KB.
func bestFaceFromImage(cfg config.Config, kbDir, name, image string, opts enrollOptions) (string, string) {
	recs, err := faceembed.Embed(cfg, []string{image}, opts.device, opts.verbose)
	if err != nil {
		return "", "face embed: " + err.Error()
	}
	if bestSingleFace(recs) != 0 {
		return "", "no single clear face in still"
	}
	dst := filepath.Join(kbDir, "face-prints", name, faceImageName(image))
	if err := copyFile(image, dst); err != nil {
		return "", "save face image: " + err.Error()
	}
	return dst, ""
}

// bestSingleFace returns the index of the frame with exactly one face above the
// det-score floor, picking the highest det_score among such frames; -1 if none.
func bestSingleFace(recs []faceembed.Face) int {
	bestIdx, bestScore := -1, faceMinDetScore
	for i, f := range recs {
		if !f.Found || f.NFaces != 1 {
			continue
		}
		if f.DetScore >= bestScore {
			bestScore = f.DetScore
			bestIdx = i
		}
	}
	return bestIdx
}

func faceFrameName(video string, ts float64) string {
	stem := sanitizeStem(strings.TrimSuffix(filepath.Base(video), filepath.Ext(video)))
	return fmt.Sprintf("%s_%s.jpg", stem, secLabel(ts))
}

func faceImageName(image string) string {
	base := sanitizeStem(strings.TrimSuffix(filepath.Base(image), filepath.Ext(image)))
	return base + ".jpg"
}

func tail(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 400 {
		return s[len(s)-400:]
	}
	return s
}

// secLabel formats seconds as a filename-safe "MmSSs" label (e.g. 12.5 -> 0m12s).
func secLabel(sec float64) string {
	if sec < 0 {
		sec = 0
	}
	total := int(sec + 0.5)
	return fmt.Sprintf("%dm%02ds", total/60, total%60)
}

// sanitizeStem makes a filename component safe (no spaces/odd chars).
func sanitizeStem(s string) string {
	repl := func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			return r
		default:
			return '_'
		}
	}
	out := strings.Map(repl, s)
	if len(out) > 60 {
		out = out[:60]
	}
	return out
}
