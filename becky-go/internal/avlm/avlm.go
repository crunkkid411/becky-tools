// Package avlm is a reusable Gemma-4 audio-visual runner for the becky tools.
//
// It turns a short video clip + a prompt into the model's free-text answer by:
//  1. extracting <= 60 s of video as ~1 fps frames and <= 30 s of 16 kHz mono
//     audio via ffmpeg (Gemma 4's documented multimodal limits),
//  2. sending each frame's timestamp as raw text IMMEDIATELY BEFORE that frame
//     (Gemma 4 has no native timestamp awareness; TimeLens, arXiv 2512.14698
//     Table 2, measured this interleaved encoding as the best of four), and
//  3. driving llama.cpp's llama-server (NOT llama-mtmd-cli) with the Gemma-4
//     E4B-it GGUF + BF16 mmproj, GPU-offloaded (-ngl 99), via its OpenAI-
//     compatible /v1/chat/completions endpoint with base64 image + audio parts.
//
// Why llama-server and not llama-mtmd-cli: on this machine's llama.cpp builds
// (prebuilt b9551 AND a from-source 9553 CUDA build) llama-mtmd-cli hard-crashes
// (0xC0000409) the moment it processes a Gemma-4 clip — vision or audio, GPU or
// CPU CLIP. llama-server uses a different multimodal code path that does NOT
// crash and returns correct answers.
//
// The OTHER half of the fix: Gemma-4 "unified" defaults to a THINKING channel
// (enable_thinking=true). With thinking on, the model emits its entire answer
// inside a <|channel>thought block that the server routes to reasoning_content,
// leaving message.content EMPTY — which earlier looked like "NaN logits" but was
// not. We disable thinking (chat_template_kwargs.enable_thinking=false) so the
// real answer lands in message.content. (Open upstream issue #24251 is a
// genuinely separate NaN-on-GPU-CLIP bug seen on Blackwell (compute 12.0); it
// does NOT reproduce on this RTX 3070 / compute 8.6.)
//
// Heavy compute stays in llama.cpp; Go only orchestrates ffmpeg + HTTP. The
// package is deliberately runtime-agnostic about WHAT question is asked so a
// future becky video-editing tool can reuse the same model via a different cmd.
//
// Everything degrades rather than crashes: a missing model, mmproj, llama
// binary, or ffmpeg yields a typed *DegradeError (callers turn it into a JSON
// note + exit 0), never a panic or a partial result.
package avlm

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"becky-go/internal/mediainfo"
	"becky-go/internal/pathx"
)

// Gemma 4 multimodal limits (from Google + llama.cpp). Audio is hard-capped at
// 30 s @ 16 kHz mono; video at 60 s sampled at ~1 fps. We never exceed these in
// a single inference window.
const (
	MaxAudioSeconds = 30.0
	MaxVideoSeconds = 60.0
	MaxFrames       = 40 // capped so frames + audio + prompt fit the 16384-token server context
	audioSampleRate = 16000

	// frameLongEdge downscales every sampled frame before it is sent.
	//
	// 896 is Gemma-4's own vision tile size, so anything larger is resampled
	// away inside the projector: sending a 1920x1080 frame buys nothing and
	// costs real time. MEASURED here on the 8GB RTX 3070 with E4B QAT, cold
	// (no prompt cache), on the rubber-snake clip:
	//
	//	frame size    tokens/frame   seconds/frame   answer
	//	1920x1080          273           17.1        "the coiled yellow object"
	//	 896x504           218           13.5        "the coiled yellow object"
	//	 640x360           113            5.9        "the yellow coiled object"
	//
	// 640 is faster still and gave the same answer, but this is a forensic tool
	// and throwing away half the remaining detail is a quality trade that should
	// not be made silently. 896 is the free part: no information the model can
	// use is lost.
	frameLongEdge = 896

	// secPerFrameEstimate is what one 896px frame costs to prefill, measured as
	// above (13.5s, rounded up for the audio and prompt that ride along). It is
	// only ever used to BUDGET a request against its deadline - being wrong just
	// means asking for a few more or fewer frames.
	secPerFrameEstimate = 14.0

	// generationSlack is time reserved for the model to actually answer once
	// every frame is in, so the budget never spends the whole deadline on input.
	generationSlack = 25 * time.Second

	// serverStartTimeout caps how long we wait for a freshly-spawned llama-server
	// to load the ~5 GB model + mmproj and answer /health.
	serverStartTimeout = 180 * time.Second
)

// Options configures one Analyze call. Paths come from config (no hardcoding).
type Options struct {
	Clip         string  // path to the (short, flagged) video clip
	Prompt       string  // the user-facing question / instruction
	SystemPrompt string  // optional system instruction (forensic framing, JSON contract)
	WindowStart  float64 // seconds into the clip to start this window (default 0)
	WindowSec    float64 // window length in seconds (clamped to model limits)
	FPS          float64 // frame sample rate for the video (default 1.0)
	MaxTokens    int     // generation cap (default 512)
	Temperature  float64 // low for determinism (default 0.2)
	Seed         int     // RNG seed for reproducibility (default 42)
	Verbose      bool    // progress to the provided Logf
}

// Result is what Analyze returns on success.
type Result struct {
	Text       string  // the model's raw answer text
	FrameCount int     // frames actually fed to the model
	AudioSec   float64 // audio seconds actually fed (<= 30)
	VideoSec   float64 // video seconds covered by frames
	HadVideo   bool
	HadAudio   bool
}

// DegradeError signals a graceful, non-fatal failure: the caller should emit a
// valid JSON note and exit 0 rather than crash. It is distinct from a hard
// programming error so callers can branch on it.
type DegradeError struct {
	Reason string
	Err    error
}

func (e *DegradeError) Error() string {
	if e.Err != nil {
		return e.Reason + ": " + e.Err.Error()
	}
	return e.Reason
}
func (e *DegradeError) Unwrap() error { return e.Err }

// degrade builds a *DegradeError.
func degrade(reason string, err error) *DegradeError { return &DegradeError{Reason: reason, Err: err} }

// IsDegrade reports whether err is (or wraps) a *DegradeError.
func IsDegrade(err error) bool {
	var de *DegradeError
	return errors.As(err, &de)
}

// Runner holds the resolved paths + a logger. Construct it once and reuse it.
type Runner struct {
	Model     string // Gemma-4 E4B-it GGUF
	MMProj    string // BF16 multimodal projector GGUF
	Server    string // llama-server.exe (spawned when ServerURL is empty)
	ServerURL string // reuse an already-running multimodal llama-server (optional)
	FFmpeg    string // ffmpeg.exe
	FFprobe   string // ffprobe.exe
	NGL       int    // GPU layers to offload (99 = full)
	Logf      func(format string, a ...any)
}

// New builds a Runner with sane defaults. Logf may be nil (no logging).
//
// `server` is the llama-server.exe path (a fresh server is spawned per Analyze
// call when serverURL is empty). `serverURL` optionally points at an
// already-running multimodal llama-server to reuse instead of spawning one.
func New(model, mmproj, server, serverURL, ffmpeg, ffprobe string, logf func(string, ...any)) *Runner {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	return &Runner{
		Model: model, MMProj: mmproj, Server: server, ServerURL: serverURL,
		FFmpeg: ffmpeg, FFprobe: ffprobe, NGL: 99, Logf: logf,
	}
}

// Ready reports whether all required artifacts exist (model, mmproj, server,
// ffmpeg). When a ServerURL is configured we don't require the server binary
// (we'll talk to the running endpoint). It returns a *DegradeError describing
// the first missing piece so a caller can degrade with a precise note.
func (r *Runner) Ready() error {
	checks := []struct {
		path, what string
	}{
		{r.Model, "gemma model GGUF"},
		{r.MMProj, "gemma BF16 mmproj"},
		{r.FFmpeg, "ffmpeg"},
	}
	for _, c := range checks {
		if c.path == "" {
			return degrade(fmt.Sprintf("%s path not configured", c.what), nil)
		}
		if _, err := os.Stat(c.path); err != nil {
			return degrade(fmt.Sprintf("%s not found at %s", c.what, c.path), nil)
		}
	}
	// The server binary is only required when we have to spawn one.
	if r.ServerURL == "" {
		if r.Server == "" {
			return degrade("llama-server path not configured", nil)
		}
		if _, err := os.Stat(r.Server); err != nil {
			if _, lerr := exec.LookPath(r.Server); lerr != nil {
				return degrade(fmt.Sprintf("llama-server not found at %s", r.Server), nil)
			}
		}
	}
	return nil
}

// Analyze runs one audio-visual inference window and returns the model text.
// All failures are returned as *DegradeError so the caller never crashes.
func (r *Runner) Analyze(ctx context.Context, opts Options) (Result, error) {
	if err := r.Ready(); err != nil {
		return Result{}, err
	}
	if _, err := os.Stat(opts.Clip); err != nil {
		return Result{}, degrade("clip not found", err)
	}

	// Probe so we only ask ffmpeg for streams that exist (graceful for
	// audio-only / video-only inputs).
	info, err := mediainfo.Probe(r.FFprobe, opts.Clip)
	if err != nil {
		// Probe failure is not fatal — assume both streams and let ffmpeg skip
		// what isn't there.
		r.Logf("avlm: ffprobe failed (%v); assuming video+audio", err)
		info = mediainfo.Info{HasVideo: true, HasAudio: true}
	}

	defaults(&opts)
	window := clampWindow(opts.WindowSec)

	work, err := os.MkdirTemp("", "becky_avlm_*")
	if err != nil {
		return Result{}, degrade("cannot create work dir", err)
	}
	defer os.RemoveAll(work)

	var res Result

	// --- Frames (video) ---------------------------------------------------
	var frames []string
	if info.HasVideo && opts.FPS > 0 {
		frames, err = r.extractFrames(ctx, opts.Clip, work, opts.WindowStart, window, opts.FPS)
		if err != nil {
			r.Logf("avlm: frame extraction degraded: %v", err)
		}
		res.FrameCount = len(frames)
		res.HadVideo = len(frames) > 0
		res.VideoSec = window
	}

	// --- Audio ------------------------------------------------------------
	var audioPath string
	if info.HasAudio {
		audioSec := window
		if audioSec > MaxAudioSeconds {
			audioSec = MaxAudioSeconds
		}
		audioPath, err = r.extractAudio(ctx, opts.Clip, work, opts.WindowStart, audioSec)
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

	// --- Prompt with [t.s] timestamp captions, frames-first --------------
	prompt := buildPrompt(opts, frames, audioPath, window)

	text, err := r.invokeServer(ctx, prompt, opts, frames, audioPath)
	if err != nil {
		return res, err // already a *DegradeError
	}
	res.Text = strings.TrimSpace(text)
	if res.Text == "" {
		// Empty content with thinking disabled is unexpected; treat as a graceful
		// degradation, not a success, so the caller emits a clear note.
		return res, degrade("model returned empty output", nil)
	}
	return res, nil
}

// defaults fills unset Options with safe values.
func defaults(o *Options) {
	if o.FPS <= 0 {
		o.FPS = 1.0
	}
	if o.WindowSec <= 0 {
		o.WindowSec = 30.0
	}
	if o.MaxTokens <= 0 {
		o.MaxTokens = 512
	}
	if o.Temperature <= 0 {
		o.Temperature = 0.2
	}
	if o.Seed == 0 {
		o.Seed = 42
	}
}

// clampWindow caps a requested window at the model's video limit.
func clampWindow(sec float64) float64 {
	if sec <= 0 {
		return 30.0
	}
	if sec > MaxVideoSeconds {
		return MaxVideoSeconds
	}
	return sec
}

// frameBudget is how many frames this request can afford before ctx's deadline.
//
// It exists because the old behaviour was a guaranteed silent failure at the
// shipped defaults: becky-validate asks for a 30s window at 1 fps = 30 frames,
// with a 240s timeout, and one frame costs ~14s on this card. The request could
// never finish. What Jordan saw was a four-minute wait and then zero
// observations with a note blaming the model.
//
// Fewer frames is a real answer. No answer is not. The caller logs the cap so
// the number of frames actually looked at is never silently smaller than asked.
func frameBudget(ctx context.Context, want int) (int, bool) {
	dl, ok := ctx.Deadline()
	if !ok {
		return want, false
	}
	usable := time.Until(dl) - generationSlack
	if usable <= 0 {
		return 1, true // something is better than a certain timeout
	}
	afford := int(usable.Seconds() / secPerFrameEstimate)
	if afford < 1 {
		afford = 1
	}
	if afford >= want {
		return want, false
	}
	return afford, true
}

// extractFrames samples frames at fps within [start, start+window], downscaled
// to frameLongEdge, and returns their paths sorted by time. The count is capped
// at MaxFrames and again at whatever ctx's deadline can actually afford.
func (r *Runner) extractFrames(ctx context.Context, clip, work string, start, window, fps float64) ([]string, error) {
	pat := filepath.Join(work, "frame_%04d.jpg")
	want, capped := frameBudget(ctx, MaxFrames)
	if capped {
		r.Logf("avlm: deadline allows %d frame(s) at ~%.0fs each, not %d — asking for fewer "+
			"rather than timing out with nothing", want, secPerFrameEstimate, MaxFrames)
	}
	// -vf fps=N samples at N fps then scales the long edge down to what the
	// vision encoder can actually use; -frames:v caps the count.
	args := []string{
		"-y", "-ss", ftoa(start), "-t", ftoa(window),
		"-i", clip,
		"-vf", fmt.Sprintf("fps=%s,scale='min(%d,iw)':-2", ftoa(fps), frameLongEdge),
		"-frames:v", itoa(want),
		"-q:v", "3",
		"-loglevel", "error",
		pat,
	}
	if err := r.runFFmpeg(ctx, args); err != nil {
		return nil, err
	}
	// pathx.FilesIn, NOT filepath.Glob: `work` is a LITERAL directory and it is
	// routinely named after the clip, so any glob metacharacter in the clip name
	// gets reinterpreted. Jordan's files are yt-dlp output and nearly all of them
	// carry the video id in brackets, which glob reads as a character class. See
	// pathx.FilesIn's comment for exactly how that failed.
	matches, err := pathx.FilesIn(work, "frame_", ".jpg")
	if err != nil {
		return nil, degrade("cannot list extracted frames", err)
	}
	if len(matches) > want {
		matches = matches[:want]
	}
	// ffmpeg exiting 0 while writing nothing is not a shrug. It used to return an
	// empty slice here and the run continued to an audio-only answer with zero
	// observations and a note that blamed the model.
	if len(matches) == 0 {
		return nil, degrade("ffmpeg reported success but wrote no frames to "+work, nil)
	}
	r.Logf("avlm: extracted %d frame(s) at %.2f fps", len(matches), fps)
	return matches, nil
}

// extractAudio writes a 16 kHz mono PCM WAV of [start, start+sec].
func (r *Runner) extractAudio(ctx context.Context, clip, work string, start, sec float64) (string, error) {
	out := filepath.Join(work, "audio.wav")
	args := []string{
		"-y", "-ss", ftoa(start), "-t", ftoa(sec),
		"-i", clip,
		"-vn", "-ac", "1", "-ar", itoa(audioSampleRate),
		"-acodec", "pcm_s16le",
		"-loglevel", "error",
		out,
	}
	if err := r.runFFmpeg(ctx, args); err != nil {
		return "", err
	}
	if fi, err := os.Stat(out); err != nil || fi.Size() == 0 {
		return "", fmt.Errorf("audio output missing or empty")
	}
	r.Logf("avlm: extracted %.1fs of 16kHz mono audio", sec)
	return out, nil
}

// runFFmpeg executes ffmpeg, capturing stderr for diagnostics.
func (r *Runner) runFFmpeg(ctx context.Context, args []string) error {
	cmd := exec.CommandContext(ctx, r.FFmpeg, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("ffmpeg: %w: %s", err, tail(stderr.String()))
	}
	return nil
}

// buildPrompt assembles the text prompt. Per Gemma 4's requirement, frame
// captions (with baked-in [t.s] timestamps) come BEFORE the question. The
// actual media bytes are attached as chat content parts by invokeServer; this
// text tells the model the time of each frame so it can reason temporally.
func buildPrompt(opts Options, frames []string, audioPath string, window float64) string {
	var b strings.Builder
	if len(frames) > 0 {
		b.WriteString("You are given ")
		fmt.Fprintf(&b, "%d video frame(s) sampled at %.2f fps", len(frames), opts.FPS)
		if audioPath != "" {
			b.WriteString(" and the clip's audio")
		}
		b.WriteString(". Each frame is preceded by its clip-absolute timestamp.\n")
	} else if audioPath != "" {
		fmt.Fprintf(&b, "You are given the clip's audio (%.0fs, 16kHz mono).\n", window)
	}
	b.WriteString("\n")
	b.WriteString(strings.TrimSpace(opts.Prompt))
	return b.String()
}

// --- tiny formatting helpers (avoid strconv churn at call sites) ---

func ftoa(f float64) string { return fmt.Sprintf("%g", f) }
func itoa(i int) string     { return fmt.Sprintf("%d", i) }

func tail(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 500 {
		return s[len(s)-500:]
	}
	return s
}

// readBase64 reads a file and returns its base64-encoded contents.
func readBase64(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(data), nil
}

// freePort asks the OS for an unused TCP port on localhost.
func freePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}
