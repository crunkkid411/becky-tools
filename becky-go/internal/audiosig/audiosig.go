// Package audiosig runs the audio_signals.py helper and answers, for any time
// window, "did anything actually LAND here?"
//
// This exists because of a correction Jordan made. becky-moment ranked windows on
// transcript text plus an LLM's opinion of that text, and I described the fix as
// replacing the transcript. He pushed back, and he was right:
//
//	"I partially agree - it's ONE indicator, but not the end-all-be-all...
//	 that's why becky-tools uses MULTIPLE signals. transcript is CERTAINLY one
//	 of them. but with more context."
//
// So the transcript stays. This adds two signals that are INDEPENDENT of it, both
// named in his own content analysis: "audio spikes on punchlines" and "vocal
// pitch >15% increase = comedic emphasis". They matter because his humour lives in
// DELIVERY - his personality profile puts it plainly: "the tone flip (sincere
// words, sarcastic delivery) is exactly the nuance a transcript-only pipeline
// loses." A deadpan line reads flat on the page and lands out loud.
//
// Measured on his own footage: the loudest moment in a 5-minute clip sits inside a
// 20-second stretch where the transcript has no text at all. No amount of reading
// finds that.
package audiosig

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"becky-go/internal/config"
	"becky-go/internal/pyhelpers"
)

// Event is one detected moment: a loudness spike or a pitch rise.
type Event struct {
	T      float64 `json:"t"`
	RiseDB float64 `json:"rise_db"`
	Ratio  float64 `json:"ratio"`
}

// Signals is the analysed file.
type Signals struct {
	OK         bool    `json:"ok"`
	Reason     string  `json:"reason,omitempty"`
	Spikes     []Event `json:"spikes"`
	PitchRises []Event `json:"pitch_rises"`
	BreathGaps []Event `json:"breath_gaps"`
}

// Run analyses a whole media file. A failure is returned as an error the caller
// turns into "rank without this signal", never a crash.
func Run(cfg config.Config, media string) (Signals, error) {
	script, err := pyhelpers.Materialize("audio_signals.py", pyhelpers.AudioSignals)
	if err != nil {
		return Signals{}, err
	}
	cmd := exec.Command(pythonFor(cfg), script, "--media", media, "--ffmpeg", ffmpegOf(cfg))
	cmd.Env = childEnv(cfg)
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return Signals{}, fmt.Errorf("audio helper failed: %v", err)
	}
	var s Signals
	if err := json.Unmarshal([]byte(lastJSONLine(stdout.String())), &s); err != nil {
		return Signals{}, fmt.Errorf("could not parse audio helper output: %w", err)
	}
	if !s.OK {
		return s, fmt.Errorf("audio helper declined: %s", s.Reason)
	}
	return s, nil
}

// Window is what a window scored on the audio, and why.
type Window struct {
	Spikes     int
	PitchRises int
	// Score is 0..1. It saturates deliberately: three punchlines in thirty
	// seconds is a lively clip, thirty is a car alarm, and the difference between
	// six and eight says nothing useful about which to post.
	Score float64
	Basis string
}

// In scores the window [t0,t1]. Empty is a real answer - "nothing landed here" -
// not a failure.
func (s Signals) In(t0, t1 float64) Window {
	var w Window
	for _, e := range s.Spikes {
		if e.T >= t0 && e.T <= t1 {
			w.Spikes++
		}
	}
	for _, e := range s.PitchRises {
		if e.T >= t0 && e.T <= t1 {
			w.PitchRises++
		}
	}
	// Two independent signals, so agreement counts for more than either alone -
	// becky's corroborate-then-conclude rule at the signal level. A window with
	// both a spike and a pitch rise scores above one with twice as many of either.
	both := 0.0
	if w.Spikes > 0 && w.PitchRises > 0 {
		both = 0.30
	}
	w.Score = clamp01(0.16*float64(w.Spikes) + 0.10*float64(w.PitchRises) + both)
	switch {
	case w.Spikes == 0 && w.PitchRises == 0:
		w.Basis = "nothing lands in the audio here"
	default:
		w.Basis = fmt.Sprintf("%d audio spike(s), %d pitch rise(s)", w.Spikes, w.PitchRises)
	}
	return w
}

func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

func pythonFor(cfg config.Config) string {
	// The face interpreter is the one config already resolves with numpy+scipy on
	// PYTHONPATH; librosa is NOT required (YIN is hand-rolled in the helper).
	if cfg.FacePython != "" {
		return cfg.FacePython
	}
	return cfg.Python
}

func ffmpegOf(cfg config.Config) string {
	if cfg.FFmpeg != "" {
		return cfg.FFmpeg
	}
	return "ffmpeg"
}

func childEnv(cfg config.Config) []string {
	env := os.Environ()
	if cfg.FacePyLib != "" {
		env = append(env, "PYTHONPATH="+cfg.FacePyLib+string(os.PathListSeparator)+os.Getenv("PYTHONPATH"))
	}
	return env
}

func lastJSONLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		l := strings.TrimSpace(lines[i])
		if strings.HasPrefix(l, "{") {
			return l
		}
	}
	return strings.TrimSpace(s)
}
