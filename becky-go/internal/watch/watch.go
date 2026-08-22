// Package watch is the pass where a model WATCHES the video and decides where
// the clip starts and ends.
//
// WHY IT EXISTS. Jordan, 2026-08-20, after becky cut his mouse-trap prank down
// to nineteen unusable seconds:
//
//	"it CHOSE to cut out the setup where i explain and demonstrate the hidden
//	 mouse trap, and it also CHOSE to abruptly edit out the end of the video
//	 (the puchline / payoff everyone was watching for), where my friend Robby
//	 actually reaches in the bag and gets snapped by a mousetrap. Gemma-4 NEEDS
//	 to watch the clip. Literally that's the only fucking thing you need to do
//	 right now because you're giving ME data and signals but nothing usable...
//	 GEMMA needs to decide the final output"
//
// He is right, and the reason becky kept failing this way is structural: every
// signal it had — transcript structure, an LLM reading that transcript, audio
// loudness, face coverage — is BLIND TO PHYSICAL ACTION. The mouse trap snaps
// at about 48 seconds, in the middle of THIRTEEN SECONDS WITH NO SPEECH. To a
// transcript that stretch is empty. It is the entire point of the video.
//
// So this pass does not add another signal. It hands a model the frames and the
// transcript together and takes its answer as the decision.
//
// HOW, and every number here was measured on his own footage 2026-08-20:
//
//   - Frames are sampled into ONE GRID with the timestamp BURNED INTO each tile.
//     That is the trick from TimeLens (arXiv 2512.14698), which found that
//     interleaving raw timestamps with frames beats every positional-encoding
//     scheme they tried; here it is done visually because it costs nothing.
//     Verified the model reads them: asked for the yellow number in the
//     top-left tile, it answered "0".
//   - ONE request, not per-frame. A 3x3 grid answered in 14s where the
//     per-frame path took 92s for the same span.
//   - Gemma-4 E4B, not 12B. The 12B spills off an 8GB card: 141 seconds and an
//     EMPTY response, measured.
//
// THE TRAP THAT COST AN HOUR: Gemma-4 QAT emits a `reasoning_content` channel
// that consumes the token budget BEFORE any answer appears. At max_tokens=100
// the reply came back finish_reason=length with content:"" — a model that looks
// broken and is not. It needs room to think; ask for 2000 and read `content`.
//
// On the prank it answered: start 0, end 63, payoff at 55.4, "The person
// discovers the mouse trap and reacts in shock." That is the clip.
package watch

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"becky-go/internal/config"
)

// Decision is where the clip actually starts and ends, and why.
type Decision struct {
	Start    float64 `json:"start"`
	End      float64 `json:"end"`
	PayoffAt float64 `json:"payoff_at"`
	Payoff   string  `json:"payoff"`
	WhyStart string  `json:"why_start"`
	WhyEnd   string  `json:"why_end"`
	// Changed reports whether the model moved the boundaries it was given.
	Changed bool `json:"changed"`
	// Note is the human-readable summary for becky-short's report.
	Note string `json:"note"`
}

// Options configure one watch.
type Options struct {
	Video string
	// Start/End are the window becky's cheap signals PROPOSED. The model may
	// move them either way — that is the whole point.
	Start, End float64
	// Duration of the whole source, so the model can be shown context outside
	// the proposed window and the answer can be clamped to something real.
	Duration float64
	// Transcript is "12.3-15.0 the words" lines. Optional but it is half the
	// evidence; without it the model is guessing at intent from pictures.
	Transcript string
	// Cuts are shot boundaries (internal/shotcut). The chosen in/out SNAP to
	// the nearest one, because the footage's own cuts are where an edit belongs.
	Cuts []float64
}

// gridCols/gridRows shape the contact sheet. 5x5 at 320px wide is about
// 1600x900, which Gemma tiles into four of its 896px views — measured working,
// and small enough to leave the context free for the transcript.
const (
	gridCols    = 5
	gridRows    = 5
	tileWidth   = 320
	maxFrames   = gridCols * gridRows
	maxTokens   = 2000 // see the reasoning-channel trap in the package comment
	loadTimeout = 240 * time.Second
)

// contextPad is how far OUTSIDE the proposed window the model is shown, so it
// can pull the start earlier and the end later. becky proposed 22.1-62.0 on the
// prank; the truth was 0-63, so a pass that could only look inside the proposal
// could never have found it.
const contextPad = 25.0

// Runner holds a live Gemma-4 server. Start it, Decide, Close — and close it
// BEFORE the framing pass runs, because Gemma (4.2GB) and Reka (4.7GB) do not
// both fit on an 8GB card. One model at a time is a hardware fact here.
type Runner struct {
	cfg  config.Config
	url  string
	stop func()
	Logf func(string, ...any)
}

// New starts a Gemma-4 E4B multimodal server.
func New(cfg config.Config, logf func(string, ...any)) (*Runner, error) {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	model, mmproj, label := cfg.GemmaAVLM()
	if !fileExists(model) {
		return nil, fmt.Errorf("the watching model is not installed (%s)", model)
	}
	if !fileExists(mmproj) {
		return nil, fmt.Errorf("the watching model's vision projector is missing (%s)", mmproj)
	}
	if !fileExists(cfg.LlamaServer) {
		return nil, fmt.Errorf("llama-server not found (%s)", cfg.LlamaServer)
	}
	port, err := freePort()
	if err != nil {
		return nil, err
	}
	url := fmt.Sprintf("http://127.0.0.1:%d", port)
	cmd := exec.Command(cfg.LlamaServer,
		"-m", model, "--mmproj", mmproj,
		"-ngl", "99", "-c", "16384", "-fa", "on", "--no-warmup",
		"--host", "127.0.0.1", "--port", strconv.Itoa(port))
	logFile, _ := os.CreateTemp("", "becky_watch_server_*.log")
	if logFile != nil {
		cmd.Stdout, cmd.Stderr = logFile, logFile
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("cannot start the watching model: %w", err)
	}
	stop := func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
			_, _ = cmd.Process.Wait()
		}
		if logFile != nil {
			logFile.Close()
		}
	}
	logf("watch: starting %s on %s ...", label, url)
	if err := waitHealthy(url, loadTimeout); err != nil {
		stop()
		return nil, err
	}
	logf("watch: ready")
	return &Runner{cfg: cfg, url: url, stop: stop, Logf: logf}, nil
}

// Close shuts the watching model down, freeing the card for the framing pass.
func (r *Runner) Close() {
	if r != nil && r.stop != nil {
		r.stop()
		r.stop = nil
	}
}

// Decide watches the clip and returns where it should start and end.
func (r *Runner) Decide(opt Options) (Decision, error) {
	lo, hi := windowToShow(opt)
	grid, interval, labelled, err := r.buildGrid(opt.Video, lo, hi)
	if err != nil {
		return Decision{}, err
	}
	defer os.Remove(grid)

	answer, err := r.ask(grid, prompt(opt, lo, hi, interval, labelled))
	if err != nil {
		return Decision{}, err
	}
	d, err := parse(answer)
	if err != nil {
		return Decision{}, fmt.Errorf("%w (model said: %.200s)", err, answer)
	}

	// The model is deciding, not dictating: its answer is clamped to footage
	// that exists and snapped to the footage's own cuts.
	d.Start = clamp(d.Start, 0, opt.Duration)
	d.End = clamp(d.End, 0, opt.Duration)
	if d.End <= d.Start {
		return Decision{}, fmt.Errorf("the model returned an empty window (%.2f to %.2f)", d.Start, d.End)
	}
	d.Start = snap(d.Start, opt.Cuts, snapRadius)
	d.End = snap(d.End, opt.Cuts, snapRadius)

	d.Changed = math.Abs(d.Start-opt.Start) > 0.25 || math.Abs(d.End-opt.End) > 0.25
	d.Note = fmt.Sprintf("the model WATCHED this and cut it %.1fs-%.1fs (was %.1fs-%.1fs): "+
		"payoff at %.1fs is %q; in because %s; out because %s",
		d.Start, d.End, opt.Start, opt.End, d.PayoffAt, d.Payoff, d.WhyStart, d.WhyEnd)
	return d, nil
}

// windowToShow is the span of video the model is shown: the proposal plus
// context on both sides, because the answer is usually OUTSIDE the proposal.
// A short source is shown whole — there is nothing to gain by hiding any of it.
func windowToShow(opt Options) (lo, hi float64) {
	lo = clamp(opt.Start-contextPad, 0, opt.Duration)
	hi = clamp(opt.End+contextPad, 0, opt.Duration)
	if opt.Duration > 0 && opt.Duration <= float64(maxFrames)*4 {
		return 0, opt.Duration
	}
	if hi <= lo {
		return 0, opt.Duration
	}
	return lo, hi
}

// gridFont is the TTF drawtext writes the tile timestamps with.
//
// THE FONT MUST BE NAMED, and this is not a style preference. With no fontfile=,
// drawtext asks fontconfig for a default; the ffmpeg that `ffmpeg` resolves to
// under the .bat on this machine (C:\Program Files\ffmpeg\...\bin) has no
// fontconfig config, prints
//
//	Fontconfig error: Cannot load default config file: No such file: (null)
//
// and then DIES with 0xc0000005. Measured 2026-08-21 against all five ffmpeg
// builds on this PC: the anaconda one warns and continues, that one hard-crashes.
// exec.LookPath picks whichever is first on PATH, so which ffmpeg becky gets
// depends on how she was launched — and the crash cost the watch pass its entire
// reason to exist. becky reported "the model watched this but its answer was
// unusable" about a clip no model had ever actually seen.
// internal/reel/drawtext.go learned the same lesson; watch.go had not.
func gridFont() string {
	for _, f := range []string{`C:/Windows/Fonts/arial.ttf`, `C:/Windows/Fonts/consola.ttf`,
		`C:/Windows/Fonts/segoeui.ttf`} {
		if _, err := os.Stat(f); err == nil {
			// The drive colon is an option separator inside a filtergraph.
			return strings.ReplaceAll(f, ":", `\:`)
		}
	}
	return ""
}

// buildGrid renders the contact sheet with each tile's timestamp burned in.
//
// labelled reports whether the timestamps actually made it onto the tiles. If
// they did not, the sheet is STILL BUILT and still watched — the labels are how
// the model reads time off the grid, so losing them costs accuracy, but losing
// the sheet costs the whole watch pass. A model that watched the footage without
// a ruler beats a model that never saw it. The prompt is told which it got.
func (r *Runner) buildGrid(video string, lo, hi float64) (path string, interval float64, labelled bool, err error) {
	return r.buildGridN(video, lo, hi, gridCols, gridRows, tileWidth)
}

// buildGridN is buildGrid with the sheet's shape spelled out, so the critic pass
// can read a denser sheet of a SHORT file than the watch pass reads of a long
// source. Same filter chain, same font fix, same unlabelled fallback.
func (r *Runner) buildGridN(video string, lo, hi float64, cols, rows, tileW int) (path string, interval float64, labelled bool, err error) {
	span := hi - lo
	if span <= 0 {
		return "", 0, false, fmt.Errorf("nothing to watch")
	}
	frames := cols * rows
	interval = span / float64(frames)
	if interval < 0.5 {
		interval = 0.5
	}
	gridCols, gridRows, tileWidth := cols, rows, tileW
	f, err := os.CreateTemp("", "becky-watch-*.png")
	if err != nil {
		return "", 0, false, err
	}
	path = f.Name()
	f.Close()

	// drawtext runs AFTER fps+scale on purpose. Before fps, `t` is the source
	// clock and the label is drawn at full resolution then shrunk to nothing;
	// after, `t` is already the sampled frame's own source time and the label is
	// drawn at the size it will be read at. Both were tried; only this reads.
	sample := fmt.Sprintf("fps=1/%s,scale=%d:-1", trimF(interval), tileWidth)
	tiles := fmt.Sprintf("tile=%dx%d:padding=2:color=white", gridCols, gridRows)
	label := ""
	if font := gridFont(); font != "" {
		label = fmt.Sprintf(",drawtext=fontfile='%s':text='%%{eif\\:trunc(t+%s)\\:d}s'"+
			":x=4:y=4:fontsize=28:fontcolor=yellow:box=1:boxcolor=black@0.9", font, trimF(lo))
	}

	var lastErr error
	for _, vf := range []string{sample + label + "," + tiles, sample + "," + tiles} {
		cmd := exec.Command(r.cfg.FFmpeg, "-y", "-v", "error",
			"-ss", trimF(lo), "-t", trimF(span), "-i", video,
			"-vf", vf, "-frames:v", "1", path)
		var stderr strings.Builder
		cmd.Stderr = &stderr
		err := cmd.Run()
		if err == nil {
			if st, serr := os.Stat(path); serr == nil && st.Size() > 0 {
				return path, interval, strings.Contains(vf, "drawtext"), nil
			}
			err = fmt.Errorf("the contact sheet came out empty")
		} else {
			err = fmt.Errorf("%v (%s)", err, tail(stderr.String()))
		}
		lastErr = err
		if label == "" {
			break // both passes are the same command; do not run it twice
		}
		if r.Logf != nil {
			r.Logf("  the timestamp labels would not render (%v) - watching the frames without them", err)
		}
		label = ""
	}
	os.Remove(path)
	return "", 0, false, fmt.Errorf("could not build the contact sheet: %v", lastErr)
}

func prompt(opt Options, lo, hi, interval float64, labelled bool) string {
	var b strings.Builder
	fmt.Fprintf(&b, "You are choosing the in and out points for a short-form clip cut from this video.\n\n")
	fmt.Fprintf(&b, "The image is a grid of frames, read left-to-right then top-to-bottom, sampled every "+
		"%.1f seconds from %.0fs to %.0fs. ", interval, lo, hi)
	if labelled {
		fmt.Fprintf(&b, "The YELLOW NUMBER in each tile is that frame's timestamp in seconds. ")
	} else {
		// No ruler on the tiles, so say how to compute one. Counting tiles is
		// less reliable than reading a burnt-in number, which is why the label
		// is the preferred path — but it is far better than guessing.
		fmt.Fprintf(&b, "The tiles are NOT labelled: the first tile is at %.0fs and each following tile "+
			"is %.1f seconds later, so tile number N (counting from 1) is at %.0f + (N-1) x %.1f seconds. ",
			lo, interval, lo, interval)
	}
	fmt.Fprintf(&b, "The whole video is %.0f seconds long.\n\n", opt.Duration)
	if strings.TrimSpace(opt.Transcript) != "" {
		fmt.Fprintf(&b, "Transcript with timestamps:\n%s\n\n", strings.TrimSpace(opt.Transcript))
	}
	// The three-part rule is the whole instruction, and the "no speech" line is
	// the one that matters: on the prank the payoff is thirteen seconds with no
	// words in it, which is exactly what every transcript-based pass missed.
	fmt.Fprintf(&b, "A watchable clip must contain ALL THREE of:\n"+
		"  SETUP   - what the viewer must see to understand the joke\n"+
		"  TENSION - the wait\n"+
		"  PAYOFF  - the thing everyone is watching for, INCLUDING the physical action AND the reaction to it\n\n"+
		"The physical action often happens where there is NO SPEECH. Look at the frames for it.\n"+
		"Do not cut before the setup. Do not cut before the payoff has landed.\n\n")
	fmt.Fprintf(&b, "Reply with JSON only:\n"+
		`{"start": <seconds>, "end": <seconds>, "payoff_at": <seconds>, "payoff": "<one short sentence>", `+
		`"why_start": "<short>", "why_end": "<short>"}`)
	return b.String()
}

func parse(s string) (Decision, error) {
	// The model wraps JSON in a fenced block; find the object rather than
	// trusting the fence to be there.
	i, j := strings.Index(s, "{"), strings.LastIndex(s, "}")
	if i < 0 || j <= i {
		return Decision{}, fmt.Errorf("no JSON in the model's answer")
	}
	var d Decision
	if err := json.Unmarshal([]byte(s[i:j+1]), &d); err != nil {
		return Decision{}, fmt.Errorf("the model's JSON did not parse: %w", err)
	}
	return d, nil
}

// snapRadius is how far a chosen boundary may move to land on a real cut.
// Wider than this and the model's intent is being overridden rather than
// tidied.
const snapRadius = 0.6

func snap(t float64, cuts []float64, radius float64) float64 {
	best, bestD := t, radius
	for _, c := range cuts {
		if d := math.Abs(c - t); d < bestD {
			best, bestD = c, d
		}
	}
	return best
}

func clamp(v, lo, hi float64) float64 {
	if hi > 0 && v > hi {
		v = hi
	}
	if v < lo {
		v = lo
	}
	return v
}

// --- small local helpers, so this package stands alone ---

func (r *Runner) ask(imagePath, text string) (string, error) {
	raw, err := os.ReadFile(imagePath)
	if err != nil {
		return "", err
	}
	body, err := json.Marshal(map[string]any{
		"model": "gemma",
		"messages": []any{map[string]any{"role": "user", "content": []any{
			map[string]any{"type": "text", "text": text},
			map[string]any{"type": "image_url", "image_url": map[string]any{
				"url": "data:image/png;base64," + base64.StdEncoding.EncodeToString(raw)}},
		}}},
		"temperature": 0.0,
		"max_tokens":  maxTokens,
	})
	if err != nil {
		return "", err
	}
	client := &http.Client{Timeout: 15 * time.Minute}
	resp, err := client.Post(r.url+"/v1/chat/completions", "application/json", strings.NewReader(string(body)))
	if err != nil {
		return "", fmt.Errorf("the watching model did not answer: %w", err)
	}
	defer resp.Body.Close()
	var out struct {
		Choices []struct {
			FinishReason string `json:"finish_reason"`
			Message      struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", err
	}
	if len(out.Choices) == 0 {
		return "", fmt.Errorf("the watching model returned nothing")
	}
	c := out.Choices[0]
	if strings.TrimSpace(c.Message.Content) == "" {
		// The reasoning-channel trap: the budget went on thinking and no answer
		// was left. Say so plainly rather than reporting an empty result.
		return "", fmt.Errorf("the watching model spent its whole answer on reasoning "+
			"(finish=%s) — raise max_tokens", c.FinishReason)
	}
	return c.Message.Content, nil
}

func fileExists(p string) bool {
	if p == "" {
		return false
	}
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}

func freePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}

func waitHealthy(url string, limit time.Duration) error {
	deadline := time.Now().Add(limit)
	client := &http.Client{Timeout: 3 * time.Second}
	for time.Now().Before(deadline) {
		if resp, err := client.Get(url + "/health"); err == nil {
			resp.Body.Close()
			if resp.StatusCode == 200 {
				return nil
			}
		}
		time.Sleep(time.Second)
	}
	return fmt.Errorf("the watching model did not become ready within %s", limit)
}

func trimF(v float64) string { return strconv.FormatFloat(v, 'f', -1, 64) }

func tail(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 400 {
		return s[len(s)-400:]
	}
	return s
}
