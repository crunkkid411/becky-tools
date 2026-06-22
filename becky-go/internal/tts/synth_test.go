package tts

import (
	"errors"
	"strings"
	"testing"
)

func TestSelfTest_ProducesValidWAV(t *testing.T) {
	wav, err := SelfTest(Options{Seed: 42})
	if err != nil {
		t.Fatalf("SelfTest: %v", err)
	}
	info, verr := ValidateWAV(wav)
	if verr != nil {
		t.Fatalf("SelfTest WAV did not validate: %v", verr)
	}
	if info.DataBytes <= 0 {
		t.Fatalf("SelfTest WAV has empty data chunk")
	}
	if info.SampleRate != DefaultSampleRate {
		t.Errorf("rate = %d, want %d", info.SampleRate, DefaultSampleRate)
	}
	if info.Channels != 1 {
		t.Errorf("channels = %d, want mono", info.Channels)
	}
	if info.BitsPerSample != 16 {
		t.Errorf("bits = %d, want 16", info.BitsPerSample)
	}
}

func TestSelfTest_Deterministic(t *testing.T) {
	a, err := SelfTest(Options{Seed: 42})
	if err != nil {
		t.Fatal(err)
	}
	b, err := SelfTest(Options{Seed: 42})
	if err != nil {
		t.Fatal(err)
	}
	if len(a) != len(b) {
		t.Fatalf("selftest lengths differ: %d vs %d", len(a), len(b))
	}
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("selftest not byte-identical at index %d", i)
		}
	}
}

func TestSelfTest_RespectsRate(t *testing.T) {
	wav, err := SelfTest(Options{Seed: 1, SampleRate: 16000})
	if err != nil {
		t.Fatal(err)
	}
	info, _ := ValidateWAV(wav)
	if info.SampleRate != 16000 {
		t.Errorf("rate = %d, want 16000", info.SampleRate)
	}
}

// fakeResolver builds a Resolver from in-memory maps so resolution is testable
// with no real filesystem.
func fakeResolver(env map[string]string, exists map[string]bool) Resolver {
	return Resolver{
		Getenv: func(k string) string { return env[k] },
		Exists: func(p string) bool { return exists[p] },
		LookPath: func(string) (string, error) {
			return "", errors.New("not on PATH")
		},
		Glob: func(string) ([]string, error) { return nil, nil },
	}
}

func TestSynthesize_DegradesWhenBinAbsent(t *testing.T) {
	g := &ggufSynth{resolver: fakeResolver(nil, nil)}
	_, err := g.Synthesize("hello", Options{})
	if err == nil {
		t.Fatal("expected a DegradeError when no runtime is installed")
	}
	d, ok := AsDegrade(err)
	if !ok {
		t.Fatalf("error is not a *DegradeError: %T %v", err, err)
	}
	if !strings.Contains(strings.ToLower(d.Error()), "neutts") {
		t.Errorf("degrade reason should mention NeuTTS, got %q", d.Error())
	}
}

func TestSynthesize_DegradesWhenModelAbsent(t *testing.T) {
	// Binary resolves via env, but the model does not.
	bin := "/fake/neutts"
	g := &ggufSynth{resolver: fakeResolver(
		map[string]string{EnvBin: bin},
		map[string]bool{bin: true},
	)}
	_, err := g.Synthesize("hello", Options{})
	if err == nil {
		t.Fatal("expected a DegradeError when the model GGUF is absent")
	}
	if _, ok := AsDegrade(err); !ok {
		t.Fatalf("expected *DegradeError, got %T", err)
	}
}

func TestSynthesize_EmptyTextDegrades(t *testing.T) {
	g := &ggufSynth{resolver: NewResolver()}
	_, err := g.Synthesize("   ", Options{})
	if _, ok := AsDegrade(err); !ok {
		t.Fatalf("empty text should degrade, got %v", err)
	}
}

func TestSynthesize_RunsHelperWhenResolved(t *testing.T) {
	bin, model := "/fake/neutts", "/fake/model.gguf"
	called := false
	g := &ggufSynth{
		resolver: fakeResolver(
			map[string]string{EnvBin: bin, EnvModel: model},
			map[string]bool{bin: true, model: true},
		),
		runHelper: func(gotBin, gotModel, outPath, text string, opts Options) ([]byte, error) {
			called = true
			if gotBin != bin || gotModel != model {
				t.Errorf("helper got bin=%q model=%q, want %q/%q", gotBin, gotModel, bin, model)
			}
			if text != "speak this" {
				t.Errorf("helper text = %q", text)
			}
			// Return a real WAV so validation passes.
			return WriteWAVPCM16([]int16{1, 2, 3, 4}, 24000)
		},
	}
	wav, err := g.Synthesize("speak this", Options{})
	if err != nil {
		t.Fatalf("Synthesize: %v", err)
	}
	if !called {
		t.Fatal("helper was not called")
	}
	if _, verr := ValidateWAV(wav); verr != nil {
		t.Fatalf("returned WAV invalid: %v", verr)
	}
}

func TestSynthesize_RejectsInvalidWAVFromHelper(t *testing.T) {
	bin, model := "/fake/neutts", "/fake/model.gguf"
	g := &ggufSynth{
		resolver: fakeResolver(
			map[string]string{EnvBin: bin, EnvModel: model},
			map[string]bool{bin: true, model: true},
		),
		runHelper: func(_, _, _, _ string, _ Options) ([]byte, error) {
			return []byte("not a wav, just the printed text"), nil
		},
	}
	_, err := g.Synthesize("hi", Options{})
	if err == nil {
		t.Fatal("expected rejection of an invalid WAV from the helper")
	}
	if _, ok := AsDegrade(err); !ok {
		t.Fatalf("expected *DegradeError, got %T", err)
	}
}

func TestSynthesize_HelperErrorDegrades(t *testing.T) {
	bin, model := "/fake/neutts", "/fake/model.gguf"
	g := &ggufSynth{
		resolver: fakeResolver(
			map[string]string{EnvBin: bin, EnvModel: model},
			map[string]bool{bin: true, model: true},
		),
		runHelper: func(_, _, _, _ string, _ Options) ([]byte, error) {
			return nil, errors.New("runtime crashed")
		},
	}
	_, err := g.Synthesize("hi", Options{})
	if _, ok := AsDegrade(err); !ok {
		t.Fatalf("helper error should degrade, got %v", err)
	}
}

func TestResolveBin_Precedence(t *testing.T) {
	// override wins over env + default.
	r := fakeResolver(map[string]string{EnvBin: "/env/bin"}, map[string]bool{"/over/bin": true, "/env/bin": true})
	got, err := r.ResolveBin("/over/bin")
	if err != nil || got != "/over/bin" {
		t.Fatalf("override: got %q err %v", got, err)
	}
	// env wins when no override.
	got, err = r.ResolveBin("")
	if err != nil || got != "/env/bin" {
		t.Fatalf("env: got %q err %v", got, err)
	}
	// nothing found => error, returns default path for a sensible printed command.
	r2 := fakeResolver(nil, nil)
	got, err = r2.ResolveBin("")
	if err == nil {
		t.Fatal("expected error when nothing resolves")
	}
	if got != DefaultBin {
		t.Errorf("fallthrough path = %q, want %q", got, DefaultBin)
	}
}

func TestResolveModel_Precedence(t *testing.T) {
	r := fakeResolver(map[string]string{EnvModel: "/env/m.gguf"}, map[string]bool{"/over/m.gguf": true, "/env/m.gguf": true})
	got, err := r.ResolveModel("/over/m.gguf")
	if err != nil || got != "/over/m.gguf" {
		t.Fatalf("override: got %q err %v", got, err)
	}
	got, err = r.ResolveModel("")
	if err != nil || got != "/env/m.gguf" {
		t.Fatalf("env: got %q err %v", got, err)
	}
}

func TestScanModelDir_PicksLMNotCodec(t *testing.T) {
	r := Resolver{
		Glob: func(pat string) ([]string, error) {
			if strings.Contains(pat, `\*\*.gguf`) {
				return nil, nil
			}
			return []string{
				ModelDir + `\neucodec.gguf`,      // disqualified
				ModelDir + `\neutts-air-q4.gguf`, // best
				ModelDir + `\random.gguf`,        // low score
			}, nil
		},
	}
	got := r.scanModelDir()
	if !strings.Contains(got, "neutts-air") {
		t.Fatalf("scanModelDir picked %q, want the neutts-air LM", got)
	}
	if strings.Contains(got, "codec") {
		t.Fatalf("scanModelDir picked the codec %q", got)
	}
}

func TestNeuTTSArgs_Contract(t *testing.T) {
	args := NeuTTSArgs("/m.gguf", "/out.wav", "hello world", Options{Voice: "", Seed: 7, SampleRate: 24000})
	joined := strings.Join(args, " ")
	for _, want := range []string{"--model /m.gguf", "--text hello world", "--out /out.wav", "--voice default", "--seed 7", "--rate 24000"} {
		if !strings.Contains(joined, want) {
			t.Errorf("args missing %q; got %q", want, joined)
		}
	}
	// No rate flag when SampleRate is 0.
	args2 := NeuTTSArgs("/m.gguf", "/o.wav", "x", Options{Seed: 42})
	if strings.Contains(strings.Join(args2, " "), "--rate") {
		t.Errorf("unexpected --rate when SampleRate=0: %v", args2)
	}
}

func TestDegradeError_UnwrapAndAs(t *testing.T) {
	base := errors.New("boom")
	d := &DegradeError{Reason: "x", Err: base}
	if !errors.Is(d, base) {
		t.Error("errors.Is should find the wrapped error")
	}
	if got, ok := AsDegrade(d); !ok || got != d {
		t.Error("AsDegrade should return the same *DegradeError")
	}
	if _, ok := AsDegrade(errors.New("plain")); ok {
		t.Error("AsDegrade should be false for a plain error")
	}
}
