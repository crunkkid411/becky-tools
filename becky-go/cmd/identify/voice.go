// voice.go — the core voice-identification path.
//
// Pipeline:
//  1. Get per-speaker segments — load --diarized (becky-diarize JSON) if given,
//     else run diarization internally via the shared diarize_sherpa.py helper
//     (the exact same recipe becky-diarize uses).
//  2. For each detected speaker, concat that speaker's audio spans into one 16 kHz
//     mono WAV (ffmpeg), then extract a CAM++ 192-dim embedding (voice_embed.py).
//  3. For each enrolled <kb>/voice-prints/<name>, extract+average its clips'
//     embeddings.
//  4. Cosine-match each speaker vs each enrolled name (math done here, in Go).
//     Best >= threshold -> named identification; else -> unidentified[].
package main

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"becky-go/internal/beckyio"
	"becky-go/internal/config"
	"becky-go/internal/mediainfo"
	"becky-go/internal/pyhelpers"
)

// voiceOptions bundles the knobs for the voice path.
type voiceOptions struct {
	diarizedPath string
	threshold    float64
	device       string
	numThreads   int
	keepTemp     bool
	verbose      bool
}

// speakerAudio holds one diarized speaker, its spans, and its embedding.
type speakerAudio struct {
	id        string
	segments  []SpeakerSpan
	embedding []float64
}

// enrolledVoice is one enrolled name with its averaged (L2-normalized) embedding.
type enrolledVoice struct {
	name      string
	embedding []float64
}

// identifyVoices runs the full voice path and returns named + unidentified entries.
func identifyVoices(cfg config.Config, info mediainfo.Info, input string, kb Knowledge, opts voiceOptions) ([]Identification, []Unidentified, error) {
	if cfg.SpeakerEmbModel == "" || !fileExists(cfg.SpeakerEmbModel) {
		return nil, nil, fmt.Errorf("speaker embedding model not found: %q", cfg.SpeakerEmbModel)
	}

	speakers, err := resolveSpeakers(cfg, info, input, opts)
	if err != nil {
		return nil, nil, err
	}
	if len(speakers) == 0 {
		beckyio.Logf(opts.verbose, "no speakers detected")
		return nil, nil, nil
	}

	embedModel := cfg.SpeakerEmbModel
	script, err := pyhelpers.Materialize("voice_embed.py", pyhelpers.VoiceEmbed)
	if err != nil {
		return nil, nil, fmt.Errorf("materialize voice helper: %w", err)
	}

	// Extract one embedding per detected speaker from its concatenated audio.
	if err := embedSpeakers(cfg, script, embedModel, input, speakers, opts); err != nil {
		return nil, nil, err
	}

	// Build enrolled embeddings (averaged across each name's clips).
	enrolled, err := embedEnrolled(cfg, script, embedModel, kb.Voices, opts)
	if err != nil {
		return nil, nil, err
	}
	if len(enrolled) == 0 {
		beckyio.Logf(opts.verbose, "no voice-prints enrolled; all speakers -> unidentified")
	}

	ids := matchSpeakers(speakers, enrolled, opts.threshold, opts.verbose)
	unids := unmatchedDescriptions(speakers, enrolled, opts.threshold)
	return ids, unids, nil
}

// resolveSpeakers returns each speaker's id + segments, either from the supplied
// diarized JSON or by running diarization internally.
func resolveSpeakers(cfg config.Config, info mediainfo.Info, input string, opts voiceOptions) ([]speakerAudio, error) {
	if opts.diarizedPath != "" {
		beckyio.Logf(opts.verbose, "loading speakers from %s", opts.diarizedPath)
		return loadDiarizedSpeakers(opts.diarizedPath)
	}
	beckyio.Logf(opts.verbose, "no --diarized given; running diarization internally")
	return runDiarization(cfg, info, input, opts)
}

// embedSpeakers concatenates each speaker's audio and fills in its embedding.
// Speakers whose audio cannot be embedded keep a nil embedding (-> unidentified).
func embedSpeakers(cfg config.Config, script, model, input string, speakers []speakerAudio, opts voiceOptions) error {
	for i := range speakers {
		sp := &speakers[i]
		wav, err := concatSpeakerAudio(cfg.FFmpeg, input, sp.segments)
		if err != nil {
			return fmt.Errorf("concat audio for %s: %w", sp.id, err)
		}
		if !opts.keepTemp {
			defer os.Remove(wav)
		}
		vecs, err := extractEmbeddings(cfg, script, model, []string{wav}, opts)
		if err != nil {
			return fmt.Errorf("embed %s: %w", sp.id, err)
		}
		if len(vecs) == 1 {
			sp.embedding = normalize(vecs[0])
		}
		beckyio.Logf(opts.verbose, "  %s: embedded (%d segment(s))", sp.id, len(sp.segments))
	}
	return nil
}

// embedEnrolled extracts and averages each enrolled name's clip embeddings.
func embedEnrolled(cfg config.Config, script, model string, voices []VoicePrint, opts voiceOptions) ([]enrolledVoice, error) {
	var out []enrolledVoice
	for _, v := range voices {
		vecs, err := extractEmbeddings(cfg, script, model, v.Clips, opts)
		if err != nil {
			return nil, fmt.Errorf("embed enrolled %q: %w", v.Name, err)
		}
		if len(vecs) == 0 {
			beckyio.Logf(opts.verbose, "  enrolled %q: no usable clips, skipping", v.Name)
			continue
		}
		out = append(out, enrolledVoice{name: v.Name, embedding: averageNormalized(vecs)})
		beckyio.Logf(opts.verbose, "  enrolled %q: averaged %d clip(s)", v.Name, len(vecs))
	}
	return out, nil
}

// matchSpeakers cosine-matches each embedded speaker against enrolled names and
// returns identifications for those that clear the threshold.
func matchSpeakers(speakers []speakerAudio, enrolled []enrolledVoice, threshold float64, verbose bool) []Identification {
	var ids []Identification
	for _, sp := range speakers {
		if sp.embedding == nil {
			continue
		}
		bestName, bestSim := bestMatch(sp.embedding, enrolled)
		if bestName == "" || bestSim < threshold {
			continue // handled by unmatchedDescriptions
		}
		beckyio.Logf(verbose, "  %s -> %q (cosine=%.4f >= %.2f)", sp.id, bestName, bestSim, threshold)
		ids = append(ids, Identification{
			Type:       "voice",
			SpeakerID:  sp.id,
			Name:       bestName,
			Confidence: round4(bestSim),
			Match:      "cosine",
			Segments:   sp.segments,
		})
	}
	return ids
}

// unmatchedDescriptions returns an unidentified[] entry for each speaker that did
// not clear the threshold (or had no embedding), with confidence 0.0.
func unmatchedDescriptions(speakers []speakerAudio, enrolled []enrolledVoice, threshold float64) []Unidentified {
	var unids []Unidentified
	for _, sp := range speakers {
		if sp.embedding != nil {
			if name, sim := bestMatch(sp.embedding, enrolled); name != "" && sim >= threshold {
				continue // identified
			}
		}
		unids = append(unids, Unidentified{
			Type:        "voice",
			SpeakerID:   sp.id,
			Description: "unidentified speaker, unknown identity",
			Confidence:  0.0,
		})
	}
	return unids
}

// bestMatch returns the highest-cosine enrolled name and its similarity.
func bestMatch(emb []float64, enrolled []enrolledVoice) (string, float64) {
	bestName := ""
	bestSim := -1.0
	for _, e := range enrolled {
		sim := cosine(emb, e.embedding)
		if sim > bestSim {
			bestSim = sim
			bestName = e.name
		}
	}
	return bestName, bestSim
}

// helperResult mirrors voice_embed.py's stdout.
type helperResult struct {
	Skipped    bool   `json:"skipped"`
	Reason     string `json:"reason"`
	Dim        int    `json:"dim"`
	Embeddings []struct {
		Path   string    `json:"path"`
		Vector []float64 `json:"vector"`
	} `json:"embeddings"`
}

// extractEmbeddings runs voice_embed.py over one or more WAVs and returns vectors
// in input order.
func extractEmbeddings(cfg config.Config, script, model string, wavs []string, opts voiceOptions) ([][]float64, error) {
	if len(wavs) == 0 {
		return nil, nil
	}
	args := []string{script}
	args = append(args, wavs...)
	args = append(args,
		"--model", model,
		"--num-threads", fmt.Sprintf("%d", opts.numThreads),
		"--device", opts.device)
	// Gate every clip to speech-only before the CAM++ embedding so a clean
	// speaker concat and a raw enrollment clip (intro music/SFX) compare fairly.
	if cfg.SileroVADModel != "" {
		if _, statErr := os.Stat(cfg.SileroVADModel); statErr == nil {
			args = append(args, "--vad-model", cfg.SileroVADModel)
		}
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
		return nil, fmt.Errorf("voice helper failed: %v\n%s", err, tail(stderr.String()))
	}
	res, ok := parseHelperJSON(stdout.String())
	if !ok {
		return nil, fmt.Errorf("could not parse voice helper output:\n%s", tail(stdout.String()))
	}
	if res.Skipped {
		return nil, fmt.Errorf("voice helper skipped: %s", res.Reason)
	}
	out := make([][]float64, 0, len(res.Embeddings))
	for _, e := range res.Embeddings {
		if len(e.Vector) > 0 {
			out = append(out, e.Vector)
		}
	}
	return out, nil
}

// concatSpeakerAudio extracts and concatenates a speaker's spans into one 16 kHz
// mono WAV using a single ffmpeg call with a trim/concat filtergraph. Spans are
// merged so the embedding reflects only that speaker's voice.
func concatSpeakerAudio(ffmpeg, input string, spans []SpeakerSpan) (string, error) {
	if len(spans) == 0 {
		return "", fmt.Errorf("speaker has no segments")
	}
	tmp, err := os.CreateTemp("", "becky_ident_*.wav")
	if err != nil {
		return "", err
	}
	path := tmp.Name()
	tmp.Close()

	filter := buildConcatFilter(spans)
	args := []string{"-y", "-i", input,
		"-filter_complex", filter,
		"-map", "[out]",
		"-ac", "1", "-ar", "16000", "-acodec", "pcm_s16le",
		"-loglevel", "error", path}

	cmd := exec.Command(ffmpeg, args...)
	var errBuf strings.Builder
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		os.Remove(path)
		return "", fmt.Errorf("ffmpeg concat: %v\n%s", err, tail(errBuf.String()))
	}
	return path, nil
}

// buildConcatFilter constructs an atrim+concat filtergraph for a speaker's spans.
func buildConcatFilter(spans []SpeakerSpan) string {
	var b strings.Builder
	for i, s := range spans {
		fmt.Fprintf(&b, "[0:a]atrim=start=%.3f:end=%.3f,asetpts=PTS-STARTPTS[a%d];", s.Start, s.End, i)
	}
	for i := range spans {
		fmt.Fprintf(&b, "[a%d]", i)
	}
	fmt.Fprintf(&b, "concat=n=%d:v=0:a=1[out]", len(spans))
	return b.String()
}

func round4(f float64) float64 { return float64(int(f*10000+0.5)) / 10000 }

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func tail(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 800 {
		return s[len(s)-800:]
	}
	return s
}
