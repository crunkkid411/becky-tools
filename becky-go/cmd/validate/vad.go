// vad.go — a thin Silero-VAD (via sherpa-onnx) probe for becky-validate.
//
// It answers one question: how much of the clip's audio is actually speech?
// becky already computes this for becky-cut; here it gates one honesty bug:
//
//   - audio_tone on (near-)silence: when there is essentially no speech, a tone
//     finding like "subdued / deliberate" is a hallucination. We suppress the
//     audio-tone text fed to the synthesis and scrub the audio_tone field on the
//     resulting observations so no tone is asserted on silence.
//
// The whole path degrades gracefully: if the VAD model/helper/python is missing
// or errors, we return speechUnknown and the caller leaves the audio tone alone
// (no regression on clips that do have speech).
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"becky-go/internal/config"
	"becky-go/internal/pyhelpers"
)

// speechUnknown marks "VAD could not run"; callers must not suppress tone on it.
const speechUnknown = -1.0

// A clip is treated as effectively silent for tone purposes — and any asserted
// audio_tone is suppressed — when EITHER too little of it is speech OR too few
// absolute speech-seconds are present. The absolute floor matters because a
// short clip with one tiny utterance (e.g. ~0.6s of speech in 8s = ~7.5%) is
// still too little signal to judge tone, and is exactly where ASR/AV models
// hallucinate "subdued / deliberate". Both floors are conservative so genuine
// conversation (typically well above both) is never stripped.
const (
	minSpeechPctForTone = 12.0 // % of the analyzed window that must be speech
	minSpeechSecForTone = 1.5  // absolute seconds of speech needed to judge tone
)

// vadJSON mirrors the subset of vad_silero.py's stdout we read.
type vadJSON struct {
	Skipped   bool    `json:"skipped"`
	Reason    string  `json:"reason"`
	Duration  float64 `json:"duration"`
	SpeechPct float64 `json:"speech_pct"`
}

// speechStat carries the VAD verdict the tone gate needs: the speech percentage
// of the analyzed window and the absolute speech-seconds. Known reports whether
// VAD actually ran (false => the caller must leave the tone alone, no
// suppression).
type speechStat struct {
	Pct     float64 // % of the analyzed window that is speech
	Seconds float64 // absolute seconds of speech
	Known   bool    // false if VAD could not run
}

// unknownSpeech is the "VAD could not run" verdict.
var unknownSpeech = speechStat{Pct: speechUnknown, Known: false}

// clipSpeechPct extracts the clip's audio and runs Silero VAD over it, returning
// how much of the analyzed window is speech (percentage + absolute seconds). It
// returns unknownSpeech (not an error) on any missing-dependency / failure path
// so the caller degrades to "leave the tone alone" rather than crashing or
// over-suppressing.
//
// windowStart/windowSec bound the analyzed audio to the same window the AV model
// saw, so the speech estimate matches the tone estimate.
func clipSpeechPct(ctx context.Context, cfg config.Config, clip string, windowStart, windowSec float64, logf func(string, ...any)) speechStat {
	if cfg.SileroVADModel == "" {
		logf("vad: no silero model configured; leaving audio tone unchanged")
		return unknownSpeech
	}
	if _, err := os.Stat(cfg.SileroVADModel); err != nil {
		logf("vad: silero model not found (%v); leaving audio tone unchanged", err)
		return unknownSpeech
	}
	script, err := pyhelpers.Materialize("vad_silero.py", pyhelpers.VADSilero)
	if err != nil {
		logf("vad: cannot materialize helper (%v); leaving audio tone unchanged", err)
		return unknownSpeech
	}

	wav, err := extractVADAudio(ctx, cfg.FFmpeg, clip, windowStart, windowSec)
	if err != nil {
		logf("vad: audio extraction degraded (%v); leaving audio tone unchanged", err)
		return unknownSpeech
	}
	defer os.Remove(wav)

	res, err := runValidateVAD(ctx, cfg.Python, script, cfg.SileroVADModel, wav)
	if err != nil {
		logf("vad: probe degraded (%v); leaving audio tone unchanged", err)
		return unknownSpeech
	}
	if res.Skipped {
		logf("vad: helper skipped (%s); leaving audio tone unchanged", res.Reason)
		return unknownSpeech
	}
	secs := res.Duration * res.SpeechPct / 100.0
	logf("vad: %.1f%% (%.2fs) of %.1fs window is speech", res.SpeechPct, secs, res.Duration)
	return speechStat{Pct: res.SpeechPct, Seconds: secs, Known: true}
}

// extractVADAudio writes a 16 kHz mono WAV of [start, start+sec] for VAD.
func extractVADAudio(ctx context.Context, ffmpeg, clip string, start, sec float64) (string, error) {
	tmp, err := os.CreateTemp("", "becky_validate_vad_*.wav")
	if err != nil {
		return "", err
	}
	path := tmp.Name()
	tmp.Close()
	args := []string{"-y", "-ss", fmt.Sprintf("%g", start)}
	if sec > 0 {
		args = append(args, "-t", fmt.Sprintf("%g", sec))
	}
	args = append(args, "-i", clip, "-vn", "-ac", "1", "-ar", "16000",
		"-acodec", "pcm_s16le", "-loglevel", "error", path)
	cmd := exec.CommandContext(ctx, ffmpeg, args...)
	var errBuf strings.Builder
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		os.Remove(path)
		return "", fmt.Errorf("ffmpeg: %v: %s", err, strings.TrimSpace(errBuf.String()))
	}
	if fi, err := os.Stat(path); err != nil || fi.Size() == 0 {
		os.Remove(path)
		return "", fmt.Errorf("vad audio output missing or empty")
	}
	return path, nil
}

// runValidateVAD runs the Silero helper on the WAV and returns the parsed result.
func runValidateVAD(ctx context.Context, python, script, model, wav string) (vadJSON, error) {
	outFile := filepath.Join(os.TempDir(), fmt.Sprintf("becky_validate_vad_%d.json", os.Getpid()))
	defer os.Remove(outFile)
	cmd := exec.CommandContext(ctx, python, script, wav, "--model", model, "--output", outFile)
	var errBuf strings.Builder
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		return vadJSON{}, fmt.Errorf("%v: %s", err, tail(errBuf.String()))
	}
	data, err := os.ReadFile(outFile)
	if err != nil {
		return vadJSON{}, err
	}
	var res vadJSON
	if err := json.Unmarshal(data, &res); err != nil {
		return vadJSON{}, fmt.Errorf("unexpected VAD output: %s", tail(string(data)))
	}
	return res, nil
}
