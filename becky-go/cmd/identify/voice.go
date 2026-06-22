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
//
// Two separate floors do two different jobs (the detection-vs-naming invariant):
//   - threshold is the DETECTION floor (default 0.45): below it a speaker is genuinely
//     "unidentified speaker, unknown identity" — no candidate worth surfacing.
//   - nameThreshold is the NAMING floor (default 0.75): a lone voice is asserted as a
//     NAME only when best >= nameThreshold. Between the two floors it is a named
//     CANDIDATE, never a confident identification.
//   - nameMargin is the minimum top-1 minus top-2 gap (default 0.06): even above the
//     naming floor, two near-tied enrollees ("next-nearest male") are ambiguous and
//     demoted to a candidate naming both, never one.
//   - cast, when non-empty, restricts naming to expected enrollees: an enrollee not in
//     cast can never be named or even act as the runner-up that suppresses a real match.
type voiceOptions struct {
	diarizedPath  string
	threshold     float64
	nameThreshold float64
	nameMargin    float64
	cast          []string // resolved cast filter (lowercased keys/names/aliases); empty = all eligible
	device        string
	numThreads    int
	keepTemp      bool
	verbose       bool
}

// speakerAudio holds one diarized speaker, its spans, and its embedding.
type speakerAudio struct {
	id        string
	segments  []SpeakerSpan
	embedding []float64
}

// enrolledVoice is one enrolled name with its averaged (L2-normalized) embedding.
// key and aliases are kept so the --cast plausibility guard can resolve a cast name
// against the dir key, the display name, OR an entity alias (kb.go's pairing).
type enrolledVoice struct {
	name      string
	key       string
	aliases   []string
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
	enrolled, err := embedEnrolled(cfg, script, embedModel, kb, opts)
	if err != nil {
		return nil, nil, err
	}
	if len(enrolled) == 0 {
		beckyio.Logf(opts.verbose, "no voice-prints enrolled; all speakers -> unidentified")
	}

	ids := matchSpeakers(speakers, enrolled, opts)
	unids := unmatchedDescriptions(speakers, enrolled, opts)
	return ids, unids, nil
}

// filterCast restricts the enrolled set to those resolving to a name in cast. An empty
// cast returns all enrollees unchanged (current behavior). Matching is case-insensitive
// against the enrolled dir key, display name, and entity aliases.
func filterCast(enrolled []enrolledVoice, cast []string) []enrolledVoice {
	if len(cast) == 0 {
		return enrolled
	}
	want := castSet(cast)
	var out []enrolledVoice
	for _, e := range enrolled {
		if enrolleeInCast(e, want) {
			out = append(out, e)
		}
	}
	return out
}

// castSet lowercases/trims the cast names into a lookup set.
func castSet(cast []string) map[string]bool {
	want := map[string]bool{}
	for _, c := range cast {
		if s := strings.ToLower(strings.TrimSpace(c)); s != "" {
			want[s] = true
		}
	}
	return want
}

// enrolleeInCast reports whether an enrollee matches any wanted cast name (lowercased)
// by its dir key, display name, or any entity alias.
func enrolleeInCast(e enrolledVoice, want map[string]bool) bool {
	if want[strings.ToLower(strings.TrimSpace(e.key))] || want[strings.ToLower(strings.TrimSpace(e.name))] {
		return true
	}
	for _, a := range e.aliases {
		if want[strings.ToLower(strings.TrimSpace(a))] {
			return true
		}
	}
	return false
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

// embedEnrolled extracts and averages each enrolled name's clip embeddings, carrying
// the dir key + entity aliases so the --cast guard can resolve a cast name later.
func embedEnrolled(cfg config.Config, script, model string, kb Knowledge, opts voiceOptions) ([]enrolledVoice, error) {
	var out []enrolledVoice
	for _, v := range kb.Voices {
		vecs, err := extractEmbeddings(cfg, script, model, v.Clips, opts)
		if err != nil {
			return nil, fmt.Errorf("embed enrolled %q: %w", v.Name, err)
		}
		if len(vecs) == 0 {
			beckyio.Logf(opts.verbose, "  enrolled %q: no usable clips, skipping", v.Name)
			continue
		}
		var aliases []string
		if e, ok := kb.Entities[v.Key]; ok {
			aliases = e.Aliases
		}
		out = append(out, enrolledVoice{
			name:      v.Name,
			key:       v.Key,
			aliases:   aliases,
			embedding: averageNormalized(vecs),
		})
		beckyio.Logf(opts.verbose, "  enrolled %q: averaged %d clip(s)", v.Name, len(vecs))
	}
	return out, nil
}

// why_unnamed enum — a small closed set so downstream consumers can branch on it.
const (
	whyBelowDetection  = "below-detection"      // best < voice-threshold -> generic unknown
	whyBelowNameThresh = "below-name-threshold" // detection <= best < voice-name-threshold
	whyAmbiguousMargin = "ambiguous-margin"     // best >= name-threshold but margin < voice-name-margin
	whyNotInCast       = "not-in-cast"          // top-1 suppressed because absent from --cast
)

// namedScore is one (name, cosine) candidate.
type namedScore struct {
	name string
	sim  float64
}

// voiceDecision is the resolved naming outcome for one speaker: either NAMED (named=true,
// best is the asserted name+score) or a demoted CANDIDATE/unknown (named=false) with the
// machine-readable reason and the runner-up audit trail.
type voiceDecision struct {
	named    bool
	best     namedScore // top-1 IN-CAST enrollee (name "" when no plausible enrollees)
	runnerUp namedScore // top-2 IN-CAST enrollee (name "" when fewer than 2 plausible)
	margin   float64    // best.sim - runnerUp.sim (== best.sim when no runner-up)
	why      string     // one of the why_unnamed enum values (only set when !named)
}

// decideSpeaker applies the full naming decision to one embedded speaker. The --cast
// guard is applied HERE, before selection: naming runs over the in-cast enrollees, and
// if the unfiltered winner (the real top-1) was suppressed by --cast, the outcome is
// not-in-cast — the next-best in-cast enrollee is never substituted as the answer.
//
// A name is asserted ONLY when best >= nameThreshold AND margin >= nameMargin (the
// corroborate-then-conclude rule on a single modality): below the detection floor ->
// generic unknown; below the naming floor -> weak candidate; above it but too close to
// the runner-up -> ambiguous candidate naming both contenders.
func decideSpeaker(emb []float64, enrolled []enrolledVoice, opts voiceOptions) voiceDecision {
	eligible := filterCast(enrolled, opts.cast)
	best, runnerUp := topTwo(emb, eligible)
	margin := best.sim
	if runnerUp.name != "" {
		margin = best.sim - runnerUp.sim
	}
	d := voiceDecision{best: best, runnerUp: runnerUp, margin: round4(margin)}

	// Did --cast suppress the real winner? (Only meaningful when a cast is set.)
	if castSuppressedWinner(emb, enrolled, opts) {
		d.why = whyNotInCast
		return d
	}
	switch {
	case best.name == "" || best.sim < opts.threshold:
		d.why = whyBelowDetection
	case best.sim < opts.nameThreshold:
		d.why = whyBelowNameThresh
	case margin < opts.nameMargin:
		d.why = whyAmbiguousMargin
	default:
		d.named = true
	}
	return d
}

// castSuppressedWinner reports whether the UNFILTERED top-1 enrollee (over the full
// enrolled set) clears the detection floor but is absent from --cast — i.e. --cast
// removed the real winner. Empty cast -> never suppressed.
func castSuppressedWinner(emb []float64, enrolled []enrolledVoice, opts voiceOptions) bool {
	if len(opts.cast) == 0 {
		return false
	}
	rawBest, _ := topTwo(emb, enrolled)
	if rawBest.name == "" || rawBest.sim < opts.threshold {
		return false
	}
	want := castSet(opts.cast)
	for _, e := range enrolled {
		if e.name == rawBest.name {
			return !enrolleeInCast(e, want)
		}
	}
	return false
}

// matchSpeakers cosine-matches each embedded speaker against the enrolled names and
// returns identifications ONLY for speakers that clear the naming threshold, the top-2
// margin, and the --cast guard. Below those, the speaker is handled by
// unmatchedDescriptions.
func matchSpeakers(speakers []speakerAudio, enrolled []enrolledVoice, opts voiceOptions) []Identification {
	var ids []Identification
	for _, sp := range speakers {
		if sp.embedding == nil {
			continue
		}
		d := decideSpeaker(sp.embedding, enrolled, opts)
		if !d.named {
			continue // handled by unmatchedDescriptions
		}
		beckyio.Logf(opts.verbose, "  %s -> %q (cosine=%.4f >= %.2f, margin=%.4f >= %.2f)",
			sp.id, d.best.name, d.best.sim, opts.nameThreshold, d.margin, opts.nameMargin)
		id := Identification{
			Type:        "voice",
			SpeakerID:   sp.id,
			Name:        d.best.name,
			Confidence:  round4(d.best.sim),
			Match:       "cosine",
			Segments:    sp.segments,
			VoiceMargin: d.margin,
		}
		if d.runnerUp.name != "" {
			id.RunnerUp = d.runnerUp.name
			id.RunnerUpConfidence = round4(d.runnerUp.sim)
		}
		ids = append(ids, id)
	}
	return ids
}

// unmatchedDescriptions returns an unidentified[] entry for each speaker NOT named by
// matchSpeakers — carrying the near-miss candidate, runner-up, margin, and a closed-set
// why_unnamed reason so a downstream step can catch a weak/ambiguous match.
func unmatchedDescriptions(speakers []speakerAudio, enrolled []enrolledVoice, opts voiceOptions) []Unidentified {
	var unids []Unidentified
	for _, sp := range speakers {
		if sp.embedding == nil {
			unids = append(unids, Unidentified{
				Type:        "voice",
				SpeakerID:   sp.id,
				Description: "unidentified speaker, unknown identity",
				Confidence:  0.0,
				WhyUnnamed:  whyBelowDetection,
			})
			continue
		}
		d := decideSpeaker(sp.embedding, enrolled, opts)
		if d.named {
			continue // identified by matchSpeakers
		}
		unids = append(unids, demoteSpeakerToCandidate(sp, d, opts))
	}
	return unids
}

// demoteSpeakerToCandidate renders a not-named speaker as a plain-English Unidentified
// with the full audit trail (candidate, runner-up, margin, reason). The why_unnamed field
// carries the closed-set enum; the human-readable basis lives in description.
func demoteSpeakerToCandidate(sp speakerAudio, d voiceDecision, opts voiceOptions) Unidentified {
	u := Unidentified{
		Type:       "voice",
		SpeakerID:  sp.id,
		Confidence: 0.0,
		WhyUnnamed: d.why,
	}
	switch d.why {
	case whyBelowDetection:
		u.Description = "unidentified speaker, unknown identity"
		return u
	case whyNotInCast:
		u.Description = "unidentified speaker, best match not in expected cast"
		return u
	}
	// below-name-threshold or ambiguous-margin: surface the near-miss as a candidate.
	u.Candidate = d.best.name
	u.CandidateConfidence = round4(d.best.sim)
	u.VoiceMargin = d.margin
	if d.runnerUp.name != "" {
		u.RunnerUp = d.runnerUp.name
		u.RunnerUpConfidence = round4(d.runnerUp.sim)
	}
	switch d.why {
	case whyAmbiguousMargin:
		u.Description = fmt.Sprintf("ambiguous: %.2f for %s vs %.2f for %s (margin %.2f < %.2f) — possible %s, too close to %s to name; unconfirmed",
			d.best.sim, d.best.name, d.runnerUp.sim, d.runnerUp.name, d.margin, opts.nameMargin, d.best.name, d.runnerUp.name)
	default: // below-name-threshold
		u.Description = fmt.Sprintf("possible %s (voice match %.2f) — below the naming threshold (%.2f), not confirmed",
			d.best.name, d.best.sim, opts.nameThreshold)
	}
	return u
}

// topTwo returns the top-1 and top-2 enrolled candidates by cosine similarity. With 0
// enrollees both names are ""; with 1 enrollee the runner-up name is "" (and the caller
// treats margin as best). Deterministic: ties break toward the first-seen enrollee.
func topTwo(emb []float64, enrolled []enrolledVoice) (best, runnerUp namedScore) {
	best = namedScore{name: "", sim: -1.0}
	runnerUp = namedScore{name: "", sim: -1.0}
	for _, e := range enrolled {
		sim := cosine(emb, e.embedding)
		switch {
		case sim > best.sim:
			runnerUp = best
			best = namedScore{name: e.name, sim: sim}
		case sim > runnerUp.sim:
			runnerUp = namedScore{name: e.name, sim: sim}
		}
	}
	if best.name == "" {
		best.sim = 0 // no enrollees: report a 0 best rather than the -1 sentinel
	}
	if runnerUp.name == "" {
		runnerUp.sim = 0
	}
	return best, runnerUp
}

// bestMatch returns the highest-cosine enrolled name and its similarity. Retained for any
// callers that only need the top-1; the naming decision uses topTwo + decideSpeaker.
func bestMatch(emb []float64, enrolled []enrolledVoice) (string, float64) {
	best, _ := topTwo(emb, enrolled)
	return best.name, best.sim
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
