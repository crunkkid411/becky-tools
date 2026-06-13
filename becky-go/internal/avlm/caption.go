// caption.go — the TWO-STAGE audio-visual flow for forensic nuance extraction.
//
// Why two stages instead of one multimodal request:
//   Gemma-4-E4B, given MANY frames in a single chat request, collapses to one
//   averaged scene description and replicates it across timestamps — it does NOT
//   attend to each frame, so subtle, partially-occluded physical contact is
//   silently dropped (measured: zero contact recall on the test clip's documented
//   0:06-0:23 contact sequence). Given ONE frame per request, its per-image
//   grounding is far better and it recovers the contact signal (hip / lateral
//   waist / upper thigh / hands-on-torso, plus look-down body language).
//
// AnalyzeFrameByFrame therefore:
//  1. extracts frames (and optional audio) once,
//  2. captions EACH frame in its own single-image request (Stage 1), with a
//     neutral clinical/recall prompt that names body regions and notes any
//     contact, possible (occluded) contact, proximity, and the receiving
//     person's body language,
//  3. optionally analyzes the audio once for tone (Stage 1b),
//  4. feeds the per-frame captions (text) + optional context to the model with
//     the JSON observation contract to consolidate them into a timestamped
//     incident log (Stage 2).
//
// Everything degrades rather than crashes (typed *DegradeError), exactly like
// the single-shot Analyze path.
package avlm

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"becky-go/internal/mediainfo"
)

// FrameCaption is one frame's clip-absolute timestamp + the model's neutral
// per-frame description + the path to the frame image that produced it. The
// Frame path is load-bearing: it is what lets a downstream contact observation
// link to a real, human-openable image, and what the synthesis cites so any
// physical_contact claim can be verified against the exact frame.
type FrameCaption struct {
	Timestamp float64 `json:"timestamp"`
	Text      string  `json:"text"`
	Frame     string  `json:"frame"` // path to the extracted frame image (may be persisted for review)
}

// TwoStageOptions configures AnalyzeFrameByFrame.
type TwoStageOptions struct {
	Clip          string // path to the (short, flagged) video clip
	CaptionSystem string // Stage 1 system prompt (neutral clinical/recall framing)
	CaptionPrompt string // Stage 1 per-frame user instruction
	SynthSystem   string // Stage 2 system prompt (JSON observation contract)
	SynthPrompt   string // Stage 2 user instruction prefix (the captions are appended)
	AudioPrompt   string // optional Stage 1b audio-tone instruction; "" skips audio analysis
	AudioSystem   string // optional Stage 1b audio system prompt

	WindowStart float64 // seconds into the clip to start (default 0)
	WindowSec   float64 // window length in seconds (clamped to model limits)
	FPS         float64 // frame sample rate (default 1.0)

	// FramesDir, when set, persists the extracted frames here (instead of a
	// temp dir that is deleted) so contact observations can link to a real
	// image a human can open. Each FrameCaption.Frame then points into this dir.
	FramesDir string

	CaptionMaxTokens int     // per-frame caption cap (default 320)
	SynthMaxTokens   int     // synthesis cap (default 3072)
	Temperature      float64 // default 0.2
	Seed             int     // default 42
	Verbose          bool
}

// TwoStageResult is what AnalyzeFrameByFrame returns on success. SynthesisText is
// the raw model text the caller parses into observations.
type TwoStageResult struct {
	Captions      []FrameCaption
	AudioTone     string // raw audio-tone text (empty if audio analysis skipped/failed)
	SynthesisText string
	FrameCount    int
	AudioSec      float64
	HadVideo      bool
	HadAudio      bool
}

// AnalyzeFrameByFrame runs the two-stage forensic flow. All failures are
// *DegradeError so the caller never crashes.
func (r *Runner) AnalyzeFrameByFrame(ctx context.Context, o TwoStageOptions) (TwoStageResult, error) {
	var res TwoStageResult
	if err := r.Ready(); err != nil {
		return res, err
	}
	if _, err := os.Stat(o.Clip); err != nil {
		return res, degrade("clip not found", err)
	}
	twoStageDefaults(&o)

	info, err := mediainfo.Probe(r.FFprobe, o.Clip)
	if err != nil {
		r.Logf("avlm: ffprobe failed (%v); assuming video+audio", err)
		info = mediainfo.Info{HasVideo: true, HasAudio: true}
	}

	window := clampWindow(o.WindowSec)
	work, err := os.MkdirTemp("", "becky_avlm2_*")
	if err != nil {
		return res, degrade("cannot create work dir", err)
	}
	defer os.RemoveAll(work)

	// Frames go into FramesDir when set (persisted for human review of any
	// contact observation), otherwise into the temp work dir. The persisted dir
	// is cleared first so STALE frames from a prior run can never contaminate
	// this run's extraction (extractFrames globs the dir).
	frameDir := work
	if o.FramesDir != "" {
		_ = os.RemoveAll(o.FramesDir)
		if mkErr := os.MkdirAll(o.FramesDir, 0o755); mkErr != nil {
			r.Logf("avlm: cannot create frames dir %s (%v); using temp dir", o.FramesDir, mkErr)
		} else {
			frameDir = o.FramesDir
		}
	}

	// --- extract media (once) -------------------------------------------------
	var frames []string
	if info.HasVideo && o.FPS > 0 {
		frames, err = r.extractFrames(ctx, o.Clip, frameDir, o.WindowStart, window, o.FPS)
		if err != nil {
			r.Logf("avlm: frame extraction degraded: %v", err)
		}
		res.FrameCount = len(frames)
		res.HadVideo = len(frames) > 0
	}
	var audioPath string
	if info.HasAudio && o.AudioPrompt != "" {
		audioSec := window
		if audioSec > MaxAudioSeconds {
			audioSec = MaxAudioSeconds
		}
		audioPath, err = r.extractAudio(ctx, o.Clip, work, o.WindowStart, audioSec)
		if err != nil {
			r.Logf("avlm: audio extraction degraded: %v", err)
			audioPath = ""
		}
		res.HadAudio = audioPath != ""
		if res.HadAudio {
			res.AudioSec = audioSec
		}
	}
	if len(frames) == 0 && audioPath == "" {
		return res, degrade("no media extracted (clip has no usable audio or video)", nil)
	}

	// --- one warm server for all requests ------------------------------------
	baseURL, cleanup, err := r.ensureServer(ctx)
	if err != nil {
		return res, err
	}
	defer cleanup()

	// --- Stage 1: caption each frame individually ----------------------------
	res.Captions = r.captionFrames(ctx, baseURL, frames, o)

	// --- Stage 1b: audio tone (optional, once) -------------------------------
	if audioPath != "" {
		if b64, rerr := readBase64(audioPath); rerr == nil {
			parts := []contentPart{
				{Type: "text", Text: o.AudioPrompt},
				{Type: "input_audio", InputAudio: &inputAudio{Data: b64, Format: "wav"}},
			}
			tone, terr := r.chat(ctx, baseURL, o.AudioSystem, parts, o.Temperature, o.Seed, 256)
			if terr != nil {
				r.Logf("avlm: audio-tone analysis degraded: %v", terr)
			} else {
				res.AudioTone = strings.TrimSpace(tone)
			}
		}
	}

	// --- Stage 2: synthesize captions into the JSON incident log -------------
	if len(res.Captions) == 0 {
		// No frame captions: degrade with whatever audio tone we have.
		return res, degrade("no frame captions produced (Stage 1 yielded nothing)", nil)
	}
	synthUser := buildSynthUser(o.SynthPrompt, res.Captions, res.AudioTone)
	parts := []contentPart{{Type: "text", Text: synthUser}}
	r.Logf("avlm: synthesizing %d caption(s) into observations...", len(res.Captions))
	text, err := r.chat(ctx, baseURL, o.SynthSystem, parts, o.Temperature, o.Seed, o.SynthMaxTokens)
	if err != nil {
		return res, err
	}
	res.SynthesisText = strings.TrimSpace(text)
	if res.SynthesisText == "" {
		return res, degrade("synthesis returned empty output", nil)
	}
	return res, nil
}

// captionFrames sends each frame as its own single-image request and returns the
// per-frame captions in time order. A failed frame is skipped (logged), never
// fatal — partial captions still feed a useful synthesis.
func (r *Runner) captionFrames(ctx context.Context, baseURL string, frames []string, o TwoStageOptions) []FrameCaption {
	caps := make([]FrameCaption, 0, len(frames))
	for i, f := range frames {
		if ctx.Err() != nil {
			r.Logf("avlm: caption loop stopped (%v) after %d frame(s)", ctx.Err(), len(caps))
			break
		}
		t := o.WindowStart + float64(i)/o.FPS
		b64, err := readBase64(f)
		if err != nil {
			r.Logf("avlm: skipping frame at %.1fs (%v)", t, err)
			continue
		}
		// The frame file name is given to the captioner so the same name can be
		// cited by the synthesis and resolved to a real, openable image.
		frameName := filepath.Base(f)
		text := fmt.Sprintf("This frame is at clip timestamp [%.1fs] (frame file: %s).\n\n%s", t, frameName, o.CaptionPrompt)
		parts := []contentPart{
			{Type: "text", Text: text},
			{Type: "image_url", ImageURL: &imageURL{URL: "data:image/jpeg;base64," + b64}},
		}
		capText, err := r.chat(ctx, baseURL, o.CaptionSystem, parts, o.Temperature, o.Seed, o.CaptionMaxTokens)
		if err != nil {
			r.Logf("avlm: caption degraded at %.1fs: %v", t, err)
			continue
		}
		capText = strings.TrimSpace(capText)
		if capText == "" {
			continue
		}
		// Surface each per-frame caption at verbose level: these are the Stage-1
		// ground truth the synthesis reads, so seeing them is essential when
		// tuning the prompts (otherwise a synthesis miss is indistinguishable
		// from a caption miss).
		r.Logf("avlm: caption [%.1fs] %s", t, oneLine(capText))
		caps = append(caps, FrameCaption{Timestamp: t, Text: capText, Frame: f})
	}
	r.Logf("avlm: captioned %d/%d frame(s)", len(caps), len(frames))
	return caps
}

// oneLine collapses a multi-line caption to a single log-friendly line.
func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// buildSynthUser assembles the Stage-2 user message: the synthesis instruction,
// the per-frame captions as a labeled block (each tagged with its frame file so
// the model can cite it for any contact observation), and the optional audio
// tone.
func buildSynthUser(prompt string, caps []FrameCaption, audioTone string) string {
	var b strings.Builder
	b.WriteString(strings.TrimSpace(prompt))
	b.WriteString("\n\n=== PER-FRAME VISUAL DESCRIPTIONS (one per sampled frame) ===\n")
	for _, c := range caps {
		frameName := filepath.Base(c.Frame)
		if frameName == "" || frameName == "." {
			fmt.Fprintf(&b, "[%.1fs] %s\n", c.Timestamp, c.Text)
			continue
		}
		fmt.Fprintf(&b, "[%.1fs] (frame file: %s) %s\n", c.Timestamp, frameName, c.Text)
	}
	if audioTone != "" {
		b.WriteString("\n=== AUDIO TONE / PROSODY (whole window) ===\n")
		b.WriteString(audioTone)
		b.WriteString("\n")
	}
	b.WriteString("\nReturn ONLY the JSON array of observations now.")
	return b.String()
}

// twoStageDefaults fills unset two-stage options with safe values.
func twoStageDefaults(o *TwoStageOptions) {
	if o.FPS <= 0 {
		o.FPS = 1.0
	}
	if o.WindowSec <= 0 {
		o.WindowSec = 30.0
	}
	if o.CaptionMaxTokens <= 0 {
		o.CaptionMaxTokens = 320
	}
	if o.SynthMaxTokens <= 0 {
		// Big enough that a full multi-observation array for a 30+ frame window
		// completes instead of truncating mid-JSON (finish=length => unparseable).
		o.SynthMaxTokens = 8192
	}
	if o.Temperature <= 0 {
		o.Temperature = 0.2
	}
	if o.Seed == 0 {
		o.Seed = 42
	}
}
