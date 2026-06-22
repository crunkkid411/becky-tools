package tts

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"becky-go/internal/pathx"
	"becky-go/internal/proc"
)

// Resolution constants — mirror internal/reaperbrain/internal/config so a single
// env edit retargets the tool and code never hardcodes Jordan's machine.
const (
	// EnvBin overrides the NeuTTS Air on-device runtime binary.
	EnvBin = "BECKY_TTS_BIN"
	// EnvModel overrides the NeuTTS Air GGUF (the LM backbone) path.
	EnvModel = "BECKY_TTS_MODEL"

	// DefaultBin is becky's expected on-device NeuTTS runtime binary. The local
	// agent installs this (see SPEC-BECKY-TTS.md §8.2); until then the synth
	// degrades to printed text.
	DefaultBin = `X:\AI-2\becky-tools\models\tts\neutts-air.exe`
	// DefaultModel is the becky-owned NeuTTS Air GGUF (q4).
	DefaultModel = `X:\AI-2\becky-tools\models\tts\neutts-air-q4.gguf`
	// ModelDir is the becky-owned TTS model root scanned when no model is set.
	ModelDir = `X:\AI-2\becky-tools\models\tts`

	// DefaultVoice is the stock NeuTTS Air preset (Jordan's v1 choice, §9.4).
	DefaultVoice = "default"
)

// Options configure a single synthesis call. They are deterministic: the same
// (Text, Voice, Seed, SampleRate) must produce the same WAV from a given model.
type Options struct {
	Voice      string // preset name or a reference sample path (NeuTTS clones it)
	Seed       int64  // determinism seed (default 42)
	SampleRate int    // 0 = let the helper/selftest choose its native rate
	Model      string // explicit model override (else env/default resolution)
	Bin        string // explicit binary override (else env/default resolution)
}

// Synth turns text into a WAV byte stream. Implementations must degrade-never-crash:
// a missing model/binary or a failed synth returns a *DegradeError (so the CLI can
// still print the text), never a panic and never a substitute (Microsoft) voice.
type Synth interface {
	Synthesize(text string, opts Options) ([]byte, error)
}

// DegradeError is the typed signal that synthesis could not run (model/binary
// absent, or the helper failed). The CLI treats it as "print the text + a plain
// reason + non-zero exit", per SPEC §4.4. It is NEVER a Microsoft-voice fallback.
type DegradeError struct {
	Reason string // plain-language explanation for the human
	Err    error  // underlying cause, if any
}

func (e *DegradeError) Error() string {
	if e == nil {
		return "tts degraded"
	}
	if e.Err != nil {
		return fmt.Sprintf("%s: %v", e.Reason, e.Err)
	}
	return e.Reason
}

func (e *DegradeError) Unwrap() error { return e.Err }

// AsDegrade reports whether err is (or wraps) a *DegradeError.
func AsDegrade(err error) (*DegradeError, bool) {
	var d *DegradeError
	if errors.As(err, &d) {
		return d, true
	}
	return nil, false
}

// Resolver locates the NeuTTS runtime binary + GGUF. Its function fields are
// injectable so resolution is unit-testable with no real filesystem (same pattern
// as internal/reaperbrain.Resolver).
type Resolver struct {
	Getenv   func(string) string
	Exists   func(string) bool
	LookPath func(string) (string, error)
	Glob     func(string) ([]string, error)
}

// NewResolver returns the real, os-backed Resolver used by the CLI.
func NewResolver() Resolver {
	return Resolver{
		Getenv:   os.Getenv,
		Exists:   func(p string) bool { _, err := os.Stat(p); return err == nil },
		LookPath: exec.LookPath,
		Glob:     filepath.Glob,
	}
}

// ResolveBin: explicit override → env → becky default → PATH. On not-found it
// returns the default path plus an error (so a printed command is still sensible).
func (r Resolver) ResolveBin(override string) (string, error) {
	if o := strings.TrimSpace(override); o != "" {
		if r.exists(o) {
			return o, nil
		}
		return o, fmt.Errorf("NeuTTS runtime not found at %s (--bin)", o)
	}
	if e := strings.TrimSpace(r.getenv(EnvBin)); e != "" {
		if r.exists(e) {
			return e, nil
		}
		return e, fmt.Errorf("NeuTTS runtime not found at %s (%s)", e, EnvBin)
	}
	if r.exists(DefaultBin) {
		return DefaultBin, nil
	}
	for _, name := range []string{"neutts-air", "neutts-air.exe", "neutts", "neutts.exe"} {
		if r.LookPath != nil {
			if p, err := r.LookPath(name); err == nil && p != "" {
				return p, nil
			}
		}
	}
	return DefaultBin, fmt.Errorf("NeuTTS runtime not found (looked at --bin, %s, %s, and PATH; set %s)", EnvBin, DefaultBin, EnvBin)
}

// ResolveModel: explicit override → env → becky default GGUF → best GGUF under
// ModelDir. On not-found returns the default path plus an error.
func (r Resolver) ResolveModel(override string) (string, error) {
	if o := strings.TrimSpace(override); o != "" {
		if r.exists(o) {
			return o, nil
		}
		return o, fmt.Errorf("TTS model GGUF not found at %s (--model)", o)
	}
	if e := strings.TrimSpace(r.getenv(EnvModel)); e != "" {
		if r.exists(e) {
			return e, nil
		}
		return e, fmt.Errorf("TTS model GGUF not found at %s (%s)", e, EnvModel)
	}
	if r.exists(DefaultModel) {
		return DefaultModel, nil
	}
	if best := r.scanModelDir(); best != "" {
		return best, nil
	}
	return DefaultModel, fmt.Errorf("no TTS GGUF found (looked at --model, %s, %s, and %s; set %s)", EnvModel, DefaultModel, ModelDir, EnvModel)
}

// scanModelDir globs ModelDir for *.gguf and returns the best NeuTTS LM candidate
// (codec/projector files disqualified), ties broken lexically for determinism.
func (r Resolver) scanModelDir() string {
	if r.Glob == nil {
		return ""
	}
	var cands []string
	for _, pat := range []string{ModelDir + `\*.gguf`, ModelDir + `\*\*.gguf`} {
		if m, err := r.Glob(pat); err == nil {
			cands = append(cands, m...)
		}
	}
	best, bestScore := "", -1
	for _, p := range cands {
		s := scoreTTSModel(p)
		if s < 0 {
			continue
		}
		if s > bestScore || (s == bestScore && (best == "" || p < best)) {
			best, bestScore = p, s
		}
	}
	return best
}

// scoreTTSModel ranks a GGUF for use as the NeuTTS Air LM. The NeuCodec decoder
// and any projector/mmproj files are disqualified (they are not the LM backbone).
func scoreTTSModel(path string) int {
	name := strings.ToLower(pathx.Base(path))
	for _, bad := range []string{"codec", "neucodec", "mmproj", "vocoder", "decoder"} {
		if strings.Contains(name, bad) {
			return -1
		}
	}
	score := 0
	for _, good := range []string{"neutts", "air", "tts", "qwen2", "q4", "q8"} {
		if strings.Contains(name, good) {
			score++
		}
	}
	return score
}

func (r Resolver) exists(p string) bool {
	if r.Exists == nil {
		return false
	}
	return r.Exists(p)
}

func (r Resolver) getenv(k string) string {
	if r.Getenv == nil {
		return ""
	}
	return r.Getenv(k)
}

// ggufSynth is the real synthesizer: it resolves the NeuTTS Air runtime + GGUF and
// shells out to it, validating the WAV the helper writes. The exec is the LOCAL
// wiring boundary — on a cloud/CI box (no binary/model) ResolveBin/ResolveModel
// fail and Synthesize returns a *DegradeError before any process runs.
type ggufSynth struct {
	resolver Resolver
	// runHelper is injectable for tests; the real one execs the runtime and
	// returns the WAV bytes it produced. It is only reached once bin+model resolve.
	runHelper func(bin, model, outPath string, text string, opts Options) ([]byte, error)
}

// NewGGUFSynth returns the production NeuTTS Air synthesizer.
func NewGGUFSynth() Synth {
	return &ggufSynth{resolver: NewResolver(), runHelper: execNeuTTS}
}

// Synthesize resolves bin+model, runs the helper, and validates the WAV. Any
// failure becomes a *DegradeError so the CLI prints the text instead.
func (g *ggufSynth) Synthesize(text string, opts Options) ([]byte, error) {
	if strings.TrimSpace(text) == "" {
		return nil, &DegradeError{Reason: "nothing to speak (empty text)"}
	}
	bin, binErr := g.resolver.ResolveBin(opts.Bin)
	if binErr != nil {
		return nil, &DegradeError{Reason: "NeuTTS runtime not installed", Err: binErr}
	}
	model, modelErr := g.resolver.ResolveModel(opts.Model)
	if modelErr != nil {
		return nil, &DegradeError{Reason: "NeuTTS model not installed", Err: modelErr}
	}

	tmp, err := os.CreateTemp("", "becky-tts-*.wav")
	if err != nil {
		return nil, &DegradeError{Reason: "could not allocate a temp WAV", Err: err}
	}
	outPath := tmp.Name()
	tmp.Close()
	defer os.Remove(outPath)

	run := g.runHelper
	if run == nil {
		run = execNeuTTS
	}
	wav, err := run(bin, model, outPath, text, opts)
	if err != nil {
		return nil, &DegradeError{Reason: "NeuTTS synthesis failed", Err: err}
	}
	if _, verr := ValidateWAV(wav); verr != nil {
		return nil, &DegradeError{Reason: "NeuTTS produced an invalid WAV", Err: verr}
	}
	return wav, nil
}

// NeuTTSArgs renders the exact argv (WITHOUT the binary) that the NeuTTS Air
// on-device runtime is invoked with. Kept as a named, testable function so the
// local agent can confirm the contract the helper must honour.
func NeuTTSArgs(model, outPath, text string, opts Options) []string {
	voice := strings.TrimSpace(opts.Voice)
	if voice == "" {
		voice = DefaultVoice
	}
	seed := opts.Seed
	args := []string{
		"--model", model,
		"--text", text,
		"--out", outPath,
		"--voice", voice,
		"--seed", strconv.FormatInt(seed, 10),
	}
	if opts.SampleRate > 0 {
		args = append(args, "--rate", strconv.Itoa(opts.SampleRate))
	}
	return args
}

// execNeuTTS is the real (local-only) helper exec. On cloud/CI it is never reached
// because resolution fails first; it is defined so the contract is explicit and the
// local agent only has to confirm the runtime honours NeuTTSArgs.
func execNeuTTS(bin, model, outPath, text string, opts Options) ([]byte, error) {
	cmd := exec.Command(bin, NeuTTSArgs(model, outPath, text, opts)...)
	proc.NoWindow(cmd)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg != "" {
			return nil, fmt.Errorf("%v: %s", err, msg)
		}
		return nil, err
	}
	wav, err := os.ReadFile(outPath)
	if err != nil {
		return nil, fmt.Errorf("runtime exited cleanly but wrote no WAV at %s: %w", outPath, err)
	}
	return wav, nil
}

// SelfTest deterministically renders the fixed PCM fixture into a real WAV. It
// needs NO model, NO binary, and NO audio device — it is the offline proof that
// becky-tts's text→WAV plumbing (writer + validator) works (SPEC §8.1). The same
// seed yields a byte-identical WAV.
func SelfTest(opts Options) ([]byte, error) {
	rate := opts.SampleRate
	if rate <= 0 {
		rate = DefaultSampleRate
	}
	samples := seededTone(opts.Seed, rate, 600)
	wav, err := WriteWAVPCM16(samples, rate)
	if err != nil {
		return nil, err
	}
	if _, verr := ValidateWAV(wav); verr != nil {
		// Should be impossible (we just wrote it) — guard anyway so a regression
		// here is loud rather than silent.
		return nil, fmt.Errorf("selftest produced an invalid WAV: %w", verr)
	}
	return wav, nil
}
