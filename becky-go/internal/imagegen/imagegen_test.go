package imagegen

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// fakeRunner is a test double for the sd-cli exec — it never spawns the model. It
// optionally writes the output file so the post-run existence check passes.
type fakeRunner struct {
	out       string
	err       error
	writeOut  string // if set, create this file when run() is called
	gotBin    string
	gotArgs   []string
	callCount int
}

func (f *fakeRunner) run(bin string, args []string) (string, error) {
	f.gotBin = bin
	f.gotArgs = args
	f.callCount++
	if f.writeOut != "" {
		_ = os.WriteFile(f.writeOut, []byte("PNG"), 0o644)
	}
	return f.out, f.err
}

func writeFile(t *testing.T, path string) {
	t.Helper()
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// argValue returns the value following flag in args, or "" if absent.
func argValue(args []string, flag string) string {
	for i, a := range args {
		if a == flag && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}

func hasFlag(args []string, flag string) bool {
	for _, a := range args {
		if a == flag {
			return true
		}
	}
	return false
}

// (a) argument construction ---------------------------------------------------

func TestBuildArgs_carriesTheThreeKrea2Pieces(t *testing.T) {
	o := withDefaults(Options{
		Prompt: "a lovely cat", Model: "Krea-2-Raw-Q8_0.gguf",
		VAE: "wan_2.1_vae.safetensors", LLM: "Qwen3-VL-4B.gguf", Out: "cat.png",
	})
	args := BuildArgs(o)

	if got := argValue(args, "--diffusion-model"); got != "Krea-2-Raw-Q8_0.gguf" {
		t.Errorf("--diffusion-model: got %q", got)
	}
	if got := argValue(args, "--vae"); got != "wan_2.1_vae.safetensors" {
		t.Errorf("--vae: got %q", got)
	}
	if got := argValue(args, "--llm"); got != "Qwen3-VL-4B.gguf" {
		t.Errorf("--llm: got %q", got)
	}
	if got := argValue(args, "-p"); got != "a lovely cat" {
		t.Errorf("-p: got %q", got)
	}
	if got := argValue(args, "-o"); got != "cat.png" {
		t.Errorf("-o: got %q", got)
	}
}

func TestBuildArgs_fixedSeedByDefault(t *testing.T) {
	args := BuildArgs(withDefaults(Options{Prompt: "x", Model: "m", VAE: "v", LLM: "l"}))
	if got := argValue(args, "--seed"); got != strconv.FormatInt(DefaultSeed, 10) {
		t.Fatalf("default seed: got %q want %d", got, DefaultSeed)
	}
}

func TestBuildArgs_explicitSeedWins_includingZeroAndRandom(t *testing.T) {
	for _, seed := range []int64{0, -1, 12345} {
		o := withDefaults(Options{Prompt: "x", Model: "m", VAE: "v", LLM: "l"}.WithSeed(seed))
		if got := argValue(BuildArgs(o), "--seed"); got != strconv.FormatInt(seed, 10) {
			t.Errorf("seed %d: got %q", seed, got)
		}
	}
}

func TestBuildArgs_negativeOnlyWhenSet(t *testing.T) {
	with := BuildArgs(withDefaults(Options{Prompt: "x", Negative: "blurry", Model: "m", VAE: "v", LLM: "l"}))
	if argValue(with, "-n") != "blurry" {
		t.Errorf("expected -n blurry, got %v", with)
	}
	without := BuildArgs(withDefaults(Options{Prompt: "x", Model: "m", VAE: "v", LLM: "l"}))
	if hasFlag(without, "-n") {
		t.Errorf("did not expect -n when negative empty, got %v", without)
	}
}

func TestBuildArgs_optionalFlagsGatedOff(t *testing.T) {
	args := BuildArgs(withDefaults(Options{Prompt: "x", Model: "m", VAE: "v", LLM: "l"}))
	for _, f := range []string{"--diffusion-fa", "--offload-to-cpu", "-v", "-t"} {
		if hasFlag(args, f) {
			t.Errorf("did not expect %s by default, got %v", f, args)
		}
	}
}

func TestBuildArgs_optionalFlagsOn(t *testing.T) {
	args := BuildArgs(withDefaults(Options{
		Prompt: "x", Model: "m", VAE: "v", LLM: "l",
		FlashAttn: true, OffloadCPU: true, Verbose: true, Threads: 8,
	}))
	for _, f := range []string{"--diffusion-fa", "--offload-to-cpu", "-v"} {
		if !hasFlag(args, f) {
			t.Errorf("expected %s, got %v", f, args)
		}
	}
	if argValue(args, "-t") != "8" {
		t.Errorf("-t: got %q", argValue(args, "-t"))
	}
}

func TestBuildArgs_cfgFloatsTrimmed(t *testing.T) {
	args := BuildArgs(withDefaults(Options{Prompt: "x", Model: "m", VAE: "v", LLM: "l"}))
	if got := argValue(args, "--cfg-scale"); got != "1" {
		t.Errorf("--cfg-scale default: got %q want \"1\"", got)
	}
	if got := argValue(args, "--guidance"); got != "4.5" {
		t.Errorf("--guidance default: got %q want \"4.5\"", got)
	}
}

// (b) defaults ----------------------------------------------------------------

func TestWithDefaults_rawVsTurboSteps(t *testing.T) {
	raw := withDefaults(Options{Prompt: "x"})
	if raw.Steps != DefaultStepsRaw {
		t.Errorf("raw steps: got %d want %d", raw.Steps, DefaultStepsRaw)
	}
	turbo := withDefaults(Options{Prompt: "x", Turbo: true})
	if turbo.Steps != DefaultStepsTurbo {
		t.Errorf("turbo steps: got %d want %d", turbo.Steps, DefaultStepsTurbo)
	}
}

func TestWithDefaults_sizeAndSamplerAndOut(t *testing.T) {
	o := withDefaults(Options{Prompt: "x"})
	if o.Width != DefaultWidth || o.Height != DefaultHeight {
		t.Errorf("size: got %dx%d want %dx%d", o.Width, o.Height, DefaultWidth, DefaultHeight)
	}
	if o.Sampler != DefaultSampler {
		t.Errorf("sampler: got %q want %q", o.Sampler, DefaultSampler)
	}
	if o.Out != DefaultOut {
		t.Errorf("out: got %q want %q", o.Out, DefaultOut)
	}
}

func TestWithDefaults_envFallback(t *testing.T) {
	t.Setenv(EnvModel, "/env/krea.gguf")
	t.Setenv(EnvVAE, "/env/vae.safetensors")
	t.Setenv(EnvTextEncoder, "/env/qwen.gguf")
	o := withDefaults(Options{Prompt: "x"})
	if o.Model != "/env/krea.gguf" || o.VAE != "/env/vae.safetensors" || o.LLM != "/env/qwen.gguf" {
		t.Errorf("env fallback not applied: %+v", o)
	}
	// An explicit flag still wins over env.
	o2 := withDefaults(Options{Prompt: "x", Model: "/flag/m.gguf"})
	if o2.Model != "/flag/m.gguf" {
		t.Errorf("flag should win over env: got %q", o2.Model)
	}
}

// (c) variant label -----------------------------------------------------------

func TestVariantLabel(t *testing.T) {
	if got := variantLabel(Options{Model: "Krea-2-Raw-Q8_0.gguf"}); got != "krea-2-raw" {
		t.Errorf("raw: got %q", got)
	}
	if got := variantLabel(Options{Model: "Krea-2-Turbo-Q8_0.gguf"}); got != "krea-2-turbo" {
		t.Errorf("turbo by filename: got %q", got)
	}
	if got := variantLabel(Options{Model: "anything.gguf", Turbo: true}); got != "krea-2-turbo" {
		t.Errorf("turbo by flag: got %q", got)
	}
}

// (d) degrade paths -----------------------------------------------------------

func TestGenerate_degradesWhenPromptMissing(t *testing.T) {
	res := generateWith(&fakeRunner{}, Options{Model: "m", VAE: "v", LLM: "l"})
	if !res.Degraded || !strings.Contains(res.Error, "prompt") {
		t.Fatalf("expected prompt degrade, got %+v", res)
	}
}

func TestGenerate_degradesWhenModelMissing(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "sd-cli")
	writeFile(t, bin)
	// model path points nowhere
	res := generateWith(&fakeRunner{}, Options{
		Prompt: "x", SDCli: bin, Model: filepath.Join(dir, "nope.gguf"),
		VAE: bin, LLM: bin, Out: filepath.Join(dir, "o.png"),
	})
	if !res.Degraded || !strings.Contains(res.Error, "Krea-2 diffusion model") {
		t.Fatalf("expected model-missing degrade, got %+v", res)
	}
}

func TestGenerate_degradesWhenRunFails(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "f")
	writeFile(t, f)
	res := generateWith(&fakeRunner{err: errors.New("boom")}, Options{
		Prompt: "x", SDCli: f, Model: f, VAE: f, LLM: f, Out: filepath.Join(dir, "o.png"),
	})
	if !res.Degraded || !strings.Contains(res.Error, "sd-cli failed") {
		t.Fatalf("expected run-fail degrade, got %+v", res)
	}
}

func TestGenerate_degradesWhenNoOutputProduced(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "f")
	writeFile(t, f)
	// runner "succeeds" but writes no output file.
	res := generateWith(&fakeRunner{}, Options{
		Prompt: "x", SDCli: f, Model: f, VAE: f, LLM: f, Out: filepath.Join(dir, "missing.png"),
	})
	if !res.Degraded || !strings.Contains(res.Error, "no image") {
		t.Fatalf("expected no-output degrade, got %+v", res)
	}
}

// (e) happy path --------------------------------------------------------------

func TestGenerate_succeedsAndRecordsProvenance(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "f")
	writeFile(t, f)
	out := filepath.Join(dir, "art.png")
	fr := &fakeRunner{writeOut: out}
	res := generateWith(fr, Options{
		Prompt: "a forest", SDCli: f, Model: f, VAE: f, LLM: f, Out: out,
	})
	if res.Degraded {
		t.Fatalf("unexpected degrade: %+v", res)
	}
	if fr.callCount != 1 {
		t.Errorf("expected exactly one sd-cli call, got %d", fr.callCount)
	}
	if res.Output != out {
		t.Errorf("output: got %q want %q", res.Output, out)
	}
	if res.Seed != DefaultSeed {
		t.Errorf("seed: got %d want %d", res.Seed, DefaultSeed)
	}
	if len(res.Args) == 0 || res.Args[0] != "--diffusion-model" {
		t.Errorf("args not recorded for provenance: %v", res.Args)
	}
	if !strings.Contains(res.Provenance(), "krea-2-raw") {
		t.Errorf("provenance missing variant: %q", res.Provenance())
	}
}

// (f) Plan is side-effect free ------------------------------------------------

func TestPlan_buildsArgvWithoutRunningOrWritingFiles(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "plan.png")
	res := Plan(Options{Prompt: "x", Model: "m", VAE: "v", LLM: "l", Out: out})
	if res.Degraded {
		t.Fatalf("Plan should never degrade, got %+v", res)
	}
	if _, err := os.Stat(out); !os.IsNotExist(err) {
		t.Errorf("Plan must not create the output file")
	}
	if argValue(res.Args, "-p") != "x" {
		t.Errorf("Plan argv wrong: %v", res.Args)
	}
}
