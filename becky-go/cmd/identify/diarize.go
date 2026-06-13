// diarize.go — obtain per-speaker segments, either from a supplied becky-diarize
// JSON (--diarized) or by running the shared diarize_sherpa.py helper internally.
//
// Both paths return []speakerAudio with id + segments (no embedding yet). The
// internal path mirrors cmd/diarize exactly: extract 16 kHz mono WAV, run sherpa
// OfflineSpeakerDiarization (pyannote-seg-3.0 + CAM++), group flat spans by
// speaker. Reusing the same helper keeps speaker labeling identical to
// becky-diarize so the two tools agree on SPEAKER_NN ids.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"

	"becky-go/internal/beckyio"
	"becky-go/internal/config"
	"becky-go/internal/mediainfo"
	"becky-go/internal/pyhelpers"
)

// diarizeJSON mirrors the becky-diarize output contract (cmd/diarize Output).
type diarizeJSON struct {
	File     string  `json:"file"`
	Duration float64 `json:"duration"`
	Speakers []struct {
		ID       string `json:"id"`
		Segments []struct {
			Start float64 `json:"start"`
			End   float64 `json:"end"`
		} `json:"segments"`
	} `json:"speakers"`
}

// loadDiarizedSpeakers reads a becky-diarize JSON file into []speakerAudio.
func loadDiarizedSpeakers(path string) ([]speakerAudio, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read diarized json: %w", err)
	}
	var dj diarizeJSON
	if err := json.Unmarshal(data, &dj); err != nil {
		return nil, fmt.Errorf("parse diarized json: %w", err)
	}
	var speakers []speakerAudio
	for _, s := range dj.Speakers {
		spans := make([]SpeakerSpan, 0, len(s.Segments))
		for _, seg := range s.Segments {
			if seg.End > seg.Start {
				spans = append(spans, SpeakerSpan{Start: seg.Start, End: seg.End})
			}
		}
		if len(spans) == 0 {
			continue
		}
		speakers = append(speakers, speakerAudio{id: s.ID, segments: spans})
	}
	sort.Slice(speakers, func(i, j int) bool { return speakers[i].id < speakers[j].id })
	return speakers, nil
}

// flatSegment is one (start,end,speaker) span as diarize_sherpa.py emits it.
type flatSegment struct {
	Start   float64 `json:"start"`
	End     float64 `json:"end"`
	Speaker string  `json:"speaker"`
}

// diarHelperResult mirrors diarize_sherpa.py's stdout.
type diarHelperResult struct {
	Skipped  bool          `json:"skipped"`
	Reason   string        `json:"reason"`
	Segments []flatSegment `json:"segments"`
}

// runDiarization extracts audio and runs the shared diarize helper, returning
// per-speaker spans grouped by SPEAKER_NN.
func runDiarization(cfg config.Config, info mediainfo.Info, input string, opts voiceOptions) ([]speakerAudio, error) {
	if cfg.DiarSegModel == "" || !fileExists(cfg.DiarSegModel) {
		return nil, fmt.Errorf("segmentation model not found: %q", cfg.DiarSegModel)
	}
	if !info.HasAudio {
		return nil, fmt.Errorf("input has no audio stream")
	}

	beckyio.Logf(opts.verbose, "extracting 16kHz mono audio for diarization...")
	wav, err := extractAudio(cfg.FFmpeg, input)
	if err != nil {
		return nil, fmt.Errorf("audio extraction: %w", err)
	}
	if !opts.keepTemp {
		defer os.Remove(wav)
	}

	script, err := pyhelpers.Materialize("diarize_sherpa.py", pyhelpers.DiarizeSherpa)
	if err != nil {
		return nil, fmt.Errorf("materialize diarize helper: %w", err)
	}

	beckyio.Logf(opts.verbose, "running diarization (device=%s)...", opts.device)
	res, err := runDiarHelper(cfg, script, wav, opts)
	if err != nil {
		return nil, err
	}
	if res.Skipped {
		return nil, fmt.Errorf("diarization skipped: %s", res.Reason)
	}
	return groupBySpeaker(res.Segments), nil
}

func runDiarHelper(cfg config.Config, script, wav string, opts voiceOptions) (diarHelperResult, error) {
	args := []string{script, wav,
		"--seg-model", cfg.DiarSegModel,
		"--embedding-model", cfg.SpeakerEmbModel,
		"--num-clusters", "-1",
		"--threshold", "0.7",
		// Match becky-diarize's HARDENED over-split guard (0.15) so identify's internal
		// diarization yields the same speaker count becky-diarize would — otherwise a
		// spurious third cluster here would produce a phantom unidentified speaker.
		"--min-speaker-frac", "0.15",
		"--min-speaker-duration", "1.5",
		"--num-threads", fmt.Sprintf("%d", opts.numThreads),
		"--device", opts.device}
	// VAD speech-gating (default ON): strip music/SFX before diarizing so a
	// single talker doesn't fragment into phantom speakers. Mirrors becky-diarize.
	if cfg.SileroVADModel != "" {
		if _, statErr := os.Stat(cfg.SileroVADModel); statErr == nil {
			args = append(args, "--vad-model", cfg.SileroVADModel)
		}
	}
	if opts.verbose {
		args = append(args, "--verbose")
	}
	cmd := exec.Command(cfg.Python, args...)
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	if opts.verbose {
		cmd.Stderr = os.Stderr
	} else {
		cmd.Stderr = &stderr
	}
	if err := cmd.Run(); err != nil {
		return diarHelperResult{}, fmt.Errorf("diarize helper failed: %v\n%s", err, tail(stderr.String()))
	}
	res, ok := parseDiarJSON(stdout.String())
	if !ok {
		return diarHelperResult{}, fmt.Errorf("could not parse diarize helper output:\n%s", tail(stdout.String()))
	}
	return res, nil
}

// groupBySpeaker turns the flat (start,end,speaker) list into []speakerAudio,
// ordered by speaker id with each speaker's spans sorted by start.
func groupBySpeaker(flat []flatSegment) []speakerAudio {
	bySpeaker := map[string][]SpeakerSpan{}
	var order []string
	for _, f := range flat {
		if _, seen := bySpeaker[f.Speaker]; !seen {
			order = append(order, f.Speaker)
		}
		bySpeaker[f.Speaker] = append(bySpeaker[f.Speaker], SpeakerSpan{Start: f.Start, End: f.End})
	}
	sort.Strings(order)
	speakers := make([]speakerAudio, 0, len(order))
	for _, id := range order {
		spans := bySpeaker[id]
		sort.Slice(spans, func(i, j int) bool { return spans[i].Start < spans[j].Start })
		speakers = append(speakers, speakerAudio{id: id, segments: spans})
	}
	return speakers
}

// extractAudio writes a 16 kHz mono PCM WAV of the whole input via ffmpeg.
func extractAudio(ffmpeg, input string) (string, error) {
	tmp, err := os.CreateTemp("", "becky_ident_diar_*.wav")
	if err != nil {
		return "", err
	}
	path := tmp.Name()
	tmp.Close()
	cmd := exec.Command(ffmpeg, "-y", "-i", input,
		"-vn", "-ar", "16000", "-ac", "1", "-acodec", "pcm_s16le",
		"-loglevel", "error", path)
	var errBuf strings.Builder
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		os.Remove(path)
		return "", fmt.Errorf("ffmpeg: %v\n%s", err, tail(errBuf.String()))
	}
	return path, nil
}

// parseDiarJSON tolerates leading C++ log noise by scanning lines bottom-up for
// the first that unmarshals into the expected shape (same trick as cmd/diarize).
func parseDiarJSON(s string) (diarHelperResult, bool) {
	if r, ok := tryDiar(strings.TrimSpace(s)); ok {
		return r, true
	}
	lines := strings.Split(s, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			continue
		}
		if r, ok := tryDiar(line); ok {
			return r, true
		}
	}
	return diarHelperResult{}, false
}

func tryDiar(s string) (diarHelperResult, bool) {
	var r diarHelperResult
	if json.Unmarshal([]byte(s), &r) == nil && (r.Skipped || r.Segments != nil) {
		return r, true
	}
	return diarHelperResult{}, false
}

// parseHelperJSON tolerates leading log noise for the voice_embed.py output.
func parseHelperJSON(s string) (helperResult, bool) {
	if r, ok := tryVoice(strings.TrimSpace(s)); ok {
		return r, true
	}
	lines := strings.Split(s, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			continue
		}
		if r, ok := tryVoice(line); ok {
			return r, true
		}
	}
	return helperResult{}, false
}

func tryVoice(s string) (helperResult, bool) {
	var r helperResult
	if json.Unmarshal([]byte(s), &r) == nil && (r.Skipped || r.Embeddings != nil) {
		return r, true
	}
	return helperResult{}, false
}
