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
	"path/filepath"
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
	defaultPauseSec = 0.6 // quieter-than-this-for-this-long = jump cut; Jordan jump-cuts frequently, even mid-sentence
	marginBeforeSec = 0.2 // measured: detector onsets run up to 0.2s late vs the true word; 0.2s of room tone is inaudible, a clipped consonant is not
	marginAfterSec  = 0.25
	rescueBeforeSec = 0.6 // rescue padding: only fires when a cue's words are at risk, so it can afford to be generous
	rescueAfterSec  = 0.5
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
		fmt.Sprintf("highpass=f=80,volume=%.1fdB,asoftclip=type=tanh", gain),
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
		if k.End-k.Start < minKeepSec || vadPct <= 0 || speechPctOf(raw.FullSegments, k) >= vadPct {
			out = append(out, k)
		}
	}
	return out
}

// loadWords reads becky-transcribe's word-level timestamps for a clip
// (buttercut proposal 2.1 - the suite already had them; roughcut was cue-level
// only). Missing file degrades to nil: cue edges stand.
func loadWords(out, stem string) []span {
	b, err := os.ReadFile(filepath.Join(out, stem+".words.json"))
	if err != nil {
		return nil
	}
	var w struct {
		Words []struct {
			Start float64 `json:"start"`
			End   float64 `json:"end"`
		} `json:"words"`
	}
	if json.Unmarshal(b, &w) != nil {
		return nil
	}
	out2 := make([]span, len(w.Words))
	for i, x := range w.Words {
		out2[i] = span{x.Start, x.End}
	}
	return out2
}

// refineWordEdges trims non-speech lead-ins and tails at the WORD: a human
// editor cuts where the first word starts, not where the transcript cue
// happened to open (Jordan's WE_TRIED edit: speaker adjusting himself before
// the line is exactly what must not survive - and L2199, 2026-08-24: "the
// timeline is littered with clips that are mostly room noise where I'm just
// adjusting myself preparing to deliver the line").
//
// Anchors to the FIRST/LAST word that actually overlaps the keep, not a
// fixed s+0.15..s+0.8 window: the old window silently gave up on anything
// longer than 0.8s of lead-in (exactly the multi-second "adjusting myself"
// case Jordan named), and worse, could skip past the true first word - if it
// started at s+0.06 (just under the window) - onto a LATER word in the
// window, deleting real speech (measured 2026-08-24: a 0.79s "You can see"
// keep collapsed to a 0.06s fragment this way). capSec is a sanity valve
// against corrupt/misaligned word data, not a real ceiling on how much
// non-speech is allowed to precede a line.
//
// Overlap tests are INCLUSIVE (>=/<=, not >/<): a meaningful fraction of
// Parakeet's word timestamps are genuine zero-duration points (measured,
// this session's words.json: {"word":"And","start":132,"end":132}) - a
// strict `w.End > s` treats a word sitting exactly at the boundary as not
// touching it, so the search silently skips the true first/last word and
// locks onto the NEXT one instead, clipping into real speech.
func refineWordEdges(keeps, words []span) []span {
	if len(words) == 0 {
		return keeps
	}
	const (
		minGap = 0.1 // gaps smaller than this are the margins doing their job
		capSec = 4.0 // never trim more than this off one edge - protects against bad word data
	)
	out := make([]span, len(keeps))
	for i, k := range keeps {
		s, e := k.Start, k.End
		for _, w := range words {
			if w.End < s || w.Start > e {
				continue // doesn't touch this keep
			}
			if gap := w.Start - s; gap >= minGap && gap <= capSec {
				s = w.Start - 0.06
			}
			break // first word overlapping the keep - head trim decided
		}
		for j := len(words) - 1; j >= 0; j-- {
			w := words[j]
			if w.End < s || w.Start > e {
				continue // doesn't touch this keep
			}
			if gap := e - w.End; gap >= minGap && gap <= capSec {
				e = w.End + 0.18
			}
			break // last word overlapping the keep - tail trim decided
		}
		if e-s < minKeepSec {
			s, e = k.Start, k.End
		}
		out[i] = span{s, e}
	}
	return out
}

// splitOnWordGaps further splits a keep wherever two consecutive words that
// overlap it are more than pause seconds apart - the word-timing equivalent
// of keepsFromTranscript's own mid-sentence pause split, and one that can
// catch a long non-speech stretch the dB-threshold silencedetect pass misses
// (calibrated for zero-crossing-snap tolerance, not for telling room noise
// from speech). A Parakeet CUE's own [start,end] can span a silence far
// longer than the cue-to-cue gap the merge step sees (measured,
// buttercut_proposal.md: "a '13s cue' that contained a 6s silence"). Overlap
// is inclusive (see refineWordEdges) for the same zero-duration-word reason.
func splitOnWordGaps(keeps, words []span, pause float64) []span {
	if len(words) == 0 {
		return keeps
	}
	var out []span
	for _, k := range keeps {
		var inside []span
		for _, w := range words {
			if w.End < k.Start || w.Start > k.End {
				continue
			}
			inside = append(inside, w)
		}
		if len(inside) < 2 {
			out = append(out, k)
			continue
		}
		start := k.Start
		for i := 0; i < len(inside)-1; i++ {
			gapStart, gapEnd := inside[i].End, inside[i+1].Start
			if gapEnd-gapStart < pause {
				continue
			}
			cutAt := gapStart + marginAfterSec
			if cutAt-start >= minKeepSec {
				out = append(out, span{start, cutAt})
			}
			start = gapEnd - marginBeforeSec
		}
		if k.End-start >= minKeepSec {
			out = append(out, span{start, k.End})
		}
	}
	return out
}

// keepsFromTranscript is the PRIMARY detector (WE_TRIED canon: the transcript
// drives, the audio arbitrates). Keeps are exactly the transcript cue spans -
// a stretch of room noise where Jordan adjusts himself has no cue and is
// never kept, which is what an audio-only detector could not manage. Cues
// closer together than the jump-cut pause bridge into one keep; a silence of
// >= pause length INSIDE a merged span (measured on the boosted audio) splits
// it - the excessive mid-sentence pause becomes a jump cut. Zero-crossing
// snaps later land on the edges of SPEECH, not of random noises, because the
// boundaries ARE the speech edges.
func keepsFromTranscript(cues []quotes.Cue, sils []span, pause float64) []span {
	type pad struct{ lo, hi float64 }
	pads := make([]pad, len(cues))
	for i, c := range cues {
		lo, hi := marginBeforeSec, marginAfterSec
		if n := len(strings.Fields(c.Text)); n >= 1 && n <= 2 {
			lo, hi = 0.5, 0.7 // ad-libs: delivered with visual emphasis on purpose
		}
		pads[i] = pad{c.Start - lo, c.End + hi}
	}
	// bridge cues across conversational gaps
	var merged []span
	for i := range cues {
		s := span{pads[i].lo, pads[i].hi}
		if len(merged) > 0 && s.Start-merged[len(merged)-1].End < pause {
			if s.End > merged[len(merged)-1].End {
				merged[len(merged)-1].End = s.End
			}
			continue
		}
		merged = append(merged, s)
	}
	// split merged spans at measured excessive pauses
	var keeps []span
	for _, m := range merged {
		start := m.Start
		for _, s := range sils {
			if s.End-s.Start < pause {
				continue
			}
			if s.Start <= start+0.3 || s.End >= m.End-0.3 {
				continue // hugging a speech edge: not a mid-sentence pause
			}
			if s.Start < start || s.End > m.End {
				continue
			}
			if s.Start-start >= minKeepSec {
				keeps = append(keeps, span{start, s.Start + marginAfterSec})
			}
			start = s.End - marginBeforeSec
		}
		if m.End-start >= minKeepSec {
			keeps = append(keeps, span{start, m.End})
		}
	}
	return keeps
}

// rescueMissedCues is the transcript-driven layer (WE_TRIED canon: the
// transcript drives, the audio arbitrates). A 3+ word cue whose words the
// audio detector left uncovered is a quiet delivery below the floor - Parakeet
// heard it, a human editor would keep it - so it comes back as a keep with
// padding. Abandoned retakes are subtracted AFTER this step and stay cut.
// words is used ONLY to tighten a newly-rescued span's own edges (measured
// 2026-08-24: an untrimmed rescue carried a 4.7s lead-in and 4.2s tail past
// its actual words on LTXZ8562's opening cue) - it is never re-applied to
// keeps that already covered their cue, so an already-fine boundary can't be
// disturbed by a second pass interacting badly with the later zero-crossing
// snap (regression measured the same day: re-refining the WHOLE keeps list
// here dropped QA coverage on 6 cues that needed no rescue at all).
func rescueMissedCues(cues []quotes.Cue, keeps []span, words []span) []span {
	for _, cue := range cues {
		if len(strings.Fields(cue.Text)) < 3 {
			continue
		}
		if wordsCovered(cue, keeps) {
			continue
		}
		rescued := span{cue.Start - rescueBeforeSec, cue.End + rescueAfterSec}
		if trimmed := refineWordEdges([]span{rescued}, words); len(trimmed) == 1 {
			rescued = trimmed[0]
		}
		keeps = append(keeps, rescued)
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
