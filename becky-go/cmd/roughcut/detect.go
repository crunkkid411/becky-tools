package main

// detect.go — the rough-cut detector for quiet-mic raw footage.
//
// WHY NOT becky-cut/auto-editor HERE (measured 2026-08-24 on hj-fbi-recap):
// Jordan's Rode mic records speech at ~-55 dBFS RMS. At that level EVERY
// threshold-based detector fails: auto-editor at its -50 dB floor kept 30% of
// the file and shredded sentences, and Silero VAD on the raw level found
// NOTHING. A single volume threshold cannot tell a word gap from a thinking
// pause anyway - Jordan's delivery has both, and only the DURATION differs.
//
// The recipe that works, all deterministic ffmpeg + the suite's own VAD:
//  1. extract 16k mono WAV;
//  2. loudnorm it (broadcast normalization - Jordan's own "light compression"
//     practice, done for ANALYSIS only; sources are never touched);
//  3. silencedetect on the normalized audio: a silence is only a silence when
//     it lasts >= --pause (0.75s) - word gaps and breaths are shorter, thinking
//     pauses and take gaps are longer. Those are the jump cuts;
//  4. keeps = the complement, with Jordan's margins (0.04s before speech,
//     0.25s after);
//  5. Silero VAD (now on normalized audio, where it works) as a sanity layer:
//     a keep with no speech in it is junk and flips to cut;
//  6. zero-crossing snap (zcross.go) on the ORIGINAL wav makes every boundary
//     pop-free at sample accuracy.

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"becky-go/internal/beckyio"
	"becky-go/internal/config"
	"becky-go/internal/proc"
	"becky-go/internal/pyhelpers"
	"becky-go/internal/quotes"
	"becky-go/internal/sampledecode"
)

const (
	// targetSpeechDB is where the analysis gain puts the speaker on EVERY
	// clip, quiet Rode mic or already-clipping clap test alike.
	targetSpeechDB  = -20.0
	maxBoostDB      = 45.0
	defaultPauseSec = 1.2 // quieter-than-this-for-this-long = jump cut; the margins eat 1.0s of it, so a 1.2s thinking pause still leaves a visible cut
	marginBeforeSec = 0.5 // sentence onsets ramp up to 0.55s below any usable floor (measured); room tone here is trimmable, clipped syllables are not
	marginAfterSec  = 0.5
	minKeepSec      = 0.30 // a keep shorter than this is a detection blip
	envWindowSec    = 0.4  // RMS envelope resolution for calibration
)

// extract16k writes a 16k mono pcm WAV beside the out dir. Degrade, never
// crash: the caller skips the clip when this fails.
func extract16k(c clip, out string) (string, error) {
	wav := out + string(os.PathSeparator) + c.Stem + ".16k.wav"
	cmd := exec.Command(config.Load().FFmpeg, "-hide_banner", "-nostdin", "-y",
		"-i", c.Path, "-vn", "-ac", "1", "-ar", "16000", "-c:a", "pcm_s16le", wav)
	proc.NoWindow(cmd)
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("wav extract: %w", err)
	}
	return wav, nil
}

// calibrate reads the clip's own context instead of trusting any average:
// the RMS envelope's p90 IS the speaker (a one-second clap is 1 window in
// hundreds; pauses dominate the low end), p10 IS the room (fan on this clip,
// quiet on that one). The gain puts speech at targetSpeechDB - zero for clips
// that already clip - and the silence floor sits halfway between boosted
// speech and boosted room tone, per clip.
func calibrate(wav string) (gain, noiseDB float64, err error) {
	au, err := sampledecode.DecodeWAVFile(wav)
	if err != nil {
		return 0, 0, err
	}
	win := int(envWindowSec * float64(au.SampleRate))
	if win < 1 {
		win = 1
	}
	var env []float64
	for i := 0; i < len(au.Samples); i += win {
		end := i + win
		if end > len(au.Samples) {
			end = len(au.Samples)
		}
		var sum float64
		for j := i; j < end; j++ {
			v := float64(au.Samples[j])
			sum += v * v
		}
		env = append(env, dbfs(math.Sqrt(sum/float64(end-i))))
	}
	sort.Float64s(env)
	p := func(q float64) float64 {
		if len(env) == 0 {
			return -100
		}
		return env[int(q*float64(len(env)-1))]
	}
	speech, room := p(0.90), p(0.10)

	gain = targetSpeechDB - speech
	if gain < 0 {
		gain = 0 // already at or above target: leave the structure as recorded
	}
	if gain > maxBoostDB {
		gain = maxBoostDB
	}
	// Measured (hj-fbi-recap): boosted speech ~-5 dB, room ~-42, but the first
	// and last syllables of a sentence ramp at only 3-8 dB above room tone -
	// a midpoint floor shaves them off. Sit the floor just above the room; the
	// 0.75s minimum duration is what rejects rustles, not the floor height.
	noiseDB = (room + gain) + 3
	if noiseDB < -50 {
		noiseDB = -50
	}
	if noiseDB > -25 {
		noiseDB = -25
	}
	return gain, noiseDB, nil
}

// normalize writes a boosted analysis copy of wav with a fixed LINEAR gain
// (never dynamic normalization: a time-varying loudness pump would raise room
// tone inside the very pauses we are trying to find). The tanh soft-clip is
// only a peak guard for the analysis copy - clap spikes saturate there and
// nowhere else.
func normalize(wav string, gain float64) (string, error) {
	cfg := config.Load()
	norm := strings.TrimSuffix(wav, ".wav") + ".norm.wav"
	cmd := exec.Command(cfg.FFmpeg, "-hide_banner", "-nostdin", "-y",
		"-i", wav, "-af",
		fmt.Sprintf("volume=%.1fdB,asoftclip=type=tanh", gain),
		"-c:a", "pcm_s16le", norm)
	proc.NoWindow(cmd)
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("boost: %w", err)
	}
	return norm, nil
}

var (
	silStartRE = regexp.MustCompile(`silence_start:\s*(-?[0-9.]+)`)
	silEndRE   = regexp.MustCompile(`silence_end:\s*(-?[0-9.]+)`)
)

// silences runs ffmpeg silencedetect over the normalized wav and returns the
// [start,end] spans that are quiet for >= pauseSec.
func silences(norm string, noiseDB, pauseSec float64) ([]span, error) {
	cmd := exec.Command(config.Load().FFmpeg, "-hide_banner", "-nostdin",
		"-i", norm, "-af",
		fmt.Sprintf("silencedetect=noise=%gdB:d=%g", noiseDB, pauseSec),
		"-f", "null", "-")
	proc.NoWindow(cmd)
	var errB strings.Builder
	cmd.Stderr = &errB
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("silencedetect: %w", err)
	}
	var out []span
	var cur *span
	for _, line := range strings.Split(errB.String(), "\n") {
		if m := silStartRE.FindStringSubmatch(line); m != nil {
			t, _ := strconv.ParseFloat(m[1], 64)
			cur = &span{Start: t}
			continue
		}
		if m := silEndRE.FindStringSubmatch(line); m != nil && cur != nil {
			t, _ := strconv.ParseFloat(m[1], 64)
			cur.End = t
			out = append(out, *cur)
			cur = nil
		}
	}
	if cur != nil { // silence running to EOF: no silence_end line is printed
		cur.End = -1 // caller fills with duration
		out = append(out, *cur)
	}
	return out, nil
}

// keepsFromSilences inverts the silence spans into keep spans with Jordan's
// margins: start 0.04s before speech onset, end 0.25s after it stops.
func keepsFromSilences(sils []span, duration float64) []span {
	var keeps []span
	prev := 0.0
	for _, s := range sils {
		end := s.End
		if end < 0 {
			end = duration
		}
		if k := (span{prev - marginBeforeSec, s.Start + marginAfterSec}); k.End > k.Start {
			keeps = append(keeps, k)
		}
		prev = end
	}
	if k := (span{prev - marginBeforeSec, duration}); k.End > k.Start {
		keeps = append(keeps, k)
	}
	var out []span
	for _, k := range keeps {
		if k.Start < 0 {
			k.Start = 0
		}
		if k.End > duration {
			k.End = duration
		}
		if k.End-k.Start >= minKeepSec {
			out = append(out, k)
		}
	}
	return out
}

// vadSanity flips keeps that contain no speech (junk cues, camera handling) to
// cuts, using Silero on the NORMALIZED audio where it can actually hear. Any
// VAD failure keeps everything - degrade, never delete.
func vadSanity(norm string, keeps []span, vadThreshold, vadPct float64, verbose bool) []span {
	cfg := config.Load()
	script, err := pyhelpers.Materialize("vad_silero.py", pyhelpers.VADSilero)
	if err != nil {
		beckyio.Logf(true, "vad materialize failed (%v) - keeps unchanged", err)
		return keeps
	}
	jsonOut := norm + ".vad.json"
	cmd := exec.Command(cfg.Python, script, norm, "--model", cfg.SileroVADModel,
		"--threshold", fmt.Sprintf("%.3f", vadThreshold), "--full-segments", "--output", jsonOut)
	proc.NoWindow(cmd)
	if err := cmd.Run(); err != nil {
		beckyio.Logf(true, "vad failed (%v) - keeps unchanged", err)
		return keeps
	}
	defer os.Remove(jsonOut)
	var raw struct {
		FullSegments []span `json:"full_segments"`
	}
	if b, err := os.ReadFile(jsonOut); err == nil {
		if jerr := json.Unmarshal(b, &raw); jerr != nil {
			beckyio.Logf(true, "vad output unparsable (%v) - keeps unchanged", jerr)
			return keeps
		}
	}
	if len(raw.FullSegments) == 0 {
		beckyio.Logf(verbose, "vad found no speech spans - keeps unchanged")
		return keeps
	}
	var out []span
	for _, k := range keeps {
		if k.End-k.Start < minKeepSec || speechPctOf(raw.FullSegments, k) >= vadPct {
			out = append(out, k)
		}
	}
	return out
}

// rescueMissedCues is the transcript-driven layer (WE_TRIED canon: the
// transcript drives, the audio arbitrates). A 3+ word cue whose words the
// audio detector left uncovered is a quiet delivery below the floor - Parakeet
// heard it, a human editor would keep it - so it comes back as a keep with
// padding. Abandoned retakes are subtracted AFTER this step and stay cut.
func rescueMissedCues(cues []quotes.Cue, keeps []span) []span {
	for _, cue := range cues {
		if len(strings.Fields(cue.Text)) < 3 {
			continue
		}
		if wordsCovered(cue, keeps) {
			continue
		}
		keeps = append(keeps, span{cue.Start - marginBeforeSec, cue.End + marginAfterSec})
	}
	sort.SliceStable(keeps, func(i, j int) bool { return keeps[i].Start < keeps[j].Start })
	var merged []span
	for _, k := range keeps {
		if k.Start < 0 {
			k.Start = 0
		}
		if len(merged) > 0 && k.Start <= merged[len(merged)-1].End+0.05 {
			if k.End > merged[len(merged)-1].End {
				merged[len(merged)-1].End = k.End
			}
			continue
		}
		merged = append(merged, k)
	}
	return merged
}

func speechPctOf(segs []span, k span) float64 {
	var acc float64
	for _, s := range segs {
		lo := maxF(s.Start, k.Start)
		hi := minF(s.End, k.End)
		if hi > lo {
			acc += hi - lo
		}
	}
	return 100 * acc / (k.End - k.Start)
}

func maxF(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}

func minF(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}
