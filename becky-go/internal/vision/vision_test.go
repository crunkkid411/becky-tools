package vision

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeRunner is a test double for the model exec — it never spawns llama.cpp.
type fakeRunner struct {
	out     string
	err     error
	gotBin  string
	gotArgs []string
}

func (f *fakeRunner) run(bin string, args []string) (string, error) {
	f.gotBin = bin
	f.gotArgs = args
	return f.out, f.err
}

// writeFile is a tiny helper that creates a file with some bytes.
func writeFile(t *testing.T, path string) {
	t.Helper()
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// (a) argument construction ---------------------------------------------------

func TestBuildArgs_matchesKnownGoodInvocation(t *testing.T) {
	opts := Options{Image: "frame.jpg", Prompt: "what is this?", NGL: 99}
	args := BuildArgs("ignored-bin", "model.gguf", "mmproj.gguf", opts)

	want := []string{
		"-m", "model.gguf",
		"--mmproj", "mmproj.gguf",
		"--image", "frame.jpg",
		"-ngl", "99",
		"--temp", "0",
		"-p", "what is this?",
	}
	if len(args) != len(want) {
		t.Fatalf("arg count: got %d want %d (%v)", len(args), len(want), args)
	}
	for i := range want {
		if args[i] != want[i] {
			t.Errorf("arg[%d]: got %q want %q", i, args[i], want[i])
		}
	}
}

func TestBuildArgs_alwaysTemperatureZero(t *testing.T) {
	args := BuildArgs("", "m", "p", Options{Image: "i", Prompt: "x", NGL: 5})
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "--temp 0") {
		t.Errorf("temperature must be 0 for determinism, got args: %v", args)
	}
}

// (b) model / mmproj discovery -----------------------------------------------

func TestDiscoverModels_picksMainAndMMProj(t *testing.T) {
	dir := t.TempDir()
	mainModel := filepath.Join(dir, "LFM2.5-VL-450M-Q8_0.gguf")
	mmproj := filepath.Join(dir, "mmproj-LFM2.5-VL-450M-Q8_0.gguf")
	writeFile(t, mainModel)
	writeFile(t, mmproj)

	gotModel, gotMM, err := DiscoverModels(dir)
	if err != nil {
		t.Fatalf("discover: %v", err)
	}
	if gotModel != mainModel {
		t.Errorf("model: got %q want %q", gotModel, mainModel)
	}
	if gotMM != mmproj {
		t.Errorf("mmproj: got %q want %q", gotMM, mmproj)
	}
}

func TestDiscoverModels_prefersQ8MainOverOtherQuant(t *testing.T) {
	dir := t.TempDir()
	q4 := filepath.Join(dir, "LFM2.5-VL-450M-Q4_K_M.gguf")
	q8 := filepath.Join(dir, "LFM2.5-VL-450M-Q8_0.gguf")
	mmproj := filepath.Join(dir, "mmproj-LFM2.5-VL-450M-Q8_0.gguf")
	writeFile(t, q4)
	writeFile(t, q8)
	writeFile(t, mmproj)

	gotModel, _, err := DiscoverModels(dir)
	if err != nil {
		t.Fatalf("discover: %v", err)
	}
	if gotModel != q8 {
		t.Errorf("should prefer Q8_0 main: got %q want %q", gotModel, q8)
	}
}

func TestDiscoverModels_degradeCases(t *testing.T) {
	cases := []struct {
		name  string
		setup func(dir string, t *testing.T)
		want  string
	}{
		{
			name:  "empty dir",
			setup: func(string, *testing.T) {},
			want:  "no model GGUF found",
		},
		{
			name: "only mmproj present",
			setup: func(dir string, t *testing.T) {
				writeFile(t, filepath.Join(dir, "mmproj-LFM2.5-VL.gguf"))
			},
			want: "no model GGUF found",
		},
		{
			name: "only main present",
			setup: func(dir string, t *testing.T) {
				writeFile(t, filepath.Join(dir, "LFM2.5-VL-450M-Q8_0.gguf"))
			},
			want: "no mmproj GGUF found",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			c.setup(dir, t)
			_, _, err := DiscoverModels(dir)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("got err %v, want containing %q", err, c.want)
			}
		})
	}
}

func TestDiscoverModels_emptyDirString(t *testing.T) {
	_, _, err := DiscoverModels("")
	if err == nil || !strings.Contains(err.Error(), "no model directory") {
		t.Errorf("empty dir should degrade clearly, got %v", err)
	}
}

// (c) JSON output shaping (the happy path through describeWith) ---------------

func TestDescribeWith_successShapesResult(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "llama-mtmd-cli.exe")
	model := filepath.Join(dir, "LFM2.5-VL-450M-Q8_0.gguf")
	mmproj := filepath.Join(dir, "mmproj-LFM2.5-VL-450M-Q8_0.gguf")
	image := filepath.Join(dir, "frame.jpg")
	for _, p := range []string{bin, model, mmproj, image} {
		writeFile(t, p)
	}

	fr := &fakeRunner{out: "  A person in a green wig.\n"}
	res := describeWith(fr, Options{Image: image, Bin: bin, ModelDir: dir, Prompt: "describe"})

	if res.Degraded {
		t.Fatalf("unexpected degrade: %s", res.Error)
	}
	if res.Tool != ToolName {
		t.Errorf("tool: got %q want %q", res.Tool, ToolName)
	}
	if res.Description != "A person in a green wig." {
		t.Errorf("description should be trimmed model stdout, got %q", res.Description)
	}
	if res.Image != image || res.Model != model || res.Prompt != "describe" {
		t.Errorf("result fields not populated: %+v", res)
	}
	if res.Error != "" {
		t.Errorf("error should be empty on success, got %q", res.Error)
	}
	// The runner must have been handed the discovered model + the image.
	joined := strings.Join(fr.gotArgs, " ")
	if !strings.Contains(joined, model) || !strings.Contains(joined, image) {
		t.Errorf("runner args missing model/image: %v", fr.gotArgs)
	}
	if fr.gotBin != bin {
		t.Errorf("runner bin: got %q want %q", fr.gotBin, bin)
	}
}

// TestResult_MarshalJSON_okField is the regression test for
// becky-AI-Agent-review-1.md acceptance criterion 8's RESOLUTION "Left open"
// gap #2: Result never carried a top-level "ok" field, so a caller could not
// tell success from failure without also checking "degraded" (inverted
// polarity, easy to get backwards). ok must be the exact inverse of Degraded,
// on every Result this package produces, regardless of which code path
// constructed it (additive: json.Marshal, not json.Unmarshal).
func TestResult_MarshalJSON_okField(t *testing.T) {
	t.Run("success: ok true, degraded false, other fields survive", func(t *testing.T) {
		r := Result{Tool: ToolName, Image: "frame.jpg", Description: "a cat"}
		b, err := json.Marshal(r)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		var m map[string]any
		if err := json.Unmarshal(b, &m); err != nil {
			t.Fatalf("unmarshal back to map: %v", err)
		}
		if ok, _ := m["ok"].(bool); !ok {
			t.Errorf("ok should be true on a non-degraded Result, got %s", b)
		}
		if degraded, _ := m["degraded"].(bool); degraded {
			t.Errorf("degraded should be false, got %s", b)
		}
		if m["description"] != "a cat" {
			t.Errorf("existing fields must survive unchanged, got %s", b)
		}
	})

	t.Run("degrade: ok false alongside degraded true", func(t *testing.T) {
		r := degrade(Result{Tool: ToolName, Image: "missing.jpg"}, errors.New("image not found"))
		b, err := json.Marshal(r)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		var m map[string]any
		if err := json.Unmarshal(b, &m); err != nil {
			t.Fatalf("unmarshal back to map: %v", err)
		}
		if ok, has := m["ok"].(bool); !has || ok {
			t.Errorf("ok should be present and false on a degraded Result, got %s", b)
		}
		if degraded, _ := m["degraded"].(bool); !degraded {
			t.Errorf("degraded should stay true, got %s", b)
		}
		if m["error"] != "image not found" {
			t.Errorf("error field must survive unchanged, got %s", b)
		}
	})
}

func TestResult_Provenance(t *testing.T) {
	r := Result{Model: `X:/models/LFM2.5-VL-450M-Q8_0.gguf`}
	prov := r.Provenance()
	if !strings.Contains(prov, ToolName) || !strings.Contains(prov, "LFM2.5-VL-450M-Q8_0.gguf") {
		t.Errorf("provenance should name the tool + model basename, got %q", prov)
	}
	if strings.Contains(prov, "X:/models") {
		t.Errorf("provenance should use the basename, not the full path: %q", prov)
	}
}

// (d) degrade paths: missing binary / model / image / empty output / exec error
//
//	-> Degraded=true, plain error, NO panic, NO call to the real model.

func TestDescribeWith_degradePaths(t *testing.T) {
	// Build a fully-valid set, then knock one piece out per case.
	base := func(t *testing.T) (bin, model, mmproj, image, dir string) {
		dir = t.TempDir()
		bin = filepath.Join(dir, "llama-mtmd-cli.exe")
		model = filepath.Join(dir, "LFM2.5-VL-450M-Q8_0.gguf")
		mmproj = filepath.Join(dir, "mmproj-LFM2.5-VL-450M-Q8_0.gguf")
		image = filepath.Join(dir, "frame.jpg")
		for _, p := range []string{bin, model, mmproj, image} {
			writeFile(t, p)
		}
		return
	}

	t.Run("missing image flag with empty dir", func(t *testing.T) {
		fr := &fakeRunner{out: "should not be used"}
		res := describeWith(fr, Options{Image: "", ModelDir: t.TempDir()})
		assertDegrade(t, res, "no model GGUF found") // empty model dir hit first
		assertRunnerNotCalled(t, fr)
	})

	t.Run("missing image flag with valid models", func(t *testing.T) {
		bin, model, mmproj, _, dir := base(t)
		fr := &fakeRunner{out: "should not be used"}
		res := describeWith(fr, Options{Image: "", Bin: bin, Model: model, MMProj: mmproj, ModelDir: dir})
		assertDegrade(t, res, "an image path is required")
		assertRunnerNotCalled(t, fr)
	})

	t.Run("missing binary", func(t *testing.T) {
		bin, _, _, image, dir := base(t)
		os.Remove(bin)
		fr := &fakeRunner{out: "should not be used"}
		res := describeWith(fr, Options{Image: image, Bin: bin, ModelDir: dir})
		assertDegrade(t, res, "llama-mtmd-cli binary not found")
		assertRunnerNotCalled(t, fr)
	})

	t.Run("missing model file", func(t *testing.T) {
		bin, model, mmproj, image, dir := base(t)
		os.Remove(model)
		fr := &fakeRunner{out: "should not be used"}
		// Pass the model explicitly so discovery doesn't pick a different file.
		res := describeWith(fr, Options{Image: image, Bin: bin, Model: model, MMProj: mmproj, ModelDir: dir})
		assertDegrade(t, res, "model GGUF not found")
		assertRunnerNotCalled(t, fr)
	})

	t.Run("missing image file", func(t *testing.T) {
		bin, _, _, image, dir := base(t)
		os.Remove(image)
		fr := &fakeRunner{out: "should not be used"}
		res := describeWith(fr, Options{Image: image, Bin: bin, ModelDir: dir})
		assertDegrade(t, res, "image not found")
		assertRunnerNotCalled(t, fr)
	})

	t.Run("exec error degrades", func(t *testing.T) {
		bin, _, _, image, dir := base(t)
		fr := &fakeRunner{err: errors.New("exit status 0xC0000409")}
		res := describeWith(fr, Options{Image: image, Bin: bin, ModelDir: dir})
		assertDegrade(t, res, "llama-mtmd-cli failed")
	})

	t.Run("empty output degrades", func(t *testing.T) {
		bin, _, _, image, dir := base(t)
		fr := &fakeRunner{out: "   \n  "}
		res := describeWith(fr, Options{Image: image, Bin: bin, ModelDir: dir})
		assertDegrade(t, res, "empty output")
	})
}

func assertDegrade(t *testing.T, res Result, wantSubstr string) {
	t.Helper()
	if !res.Degraded {
		t.Fatalf("expected degraded result, got %+v", res)
	}
	if res.Error == "" {
		t.Error("degraded result must carry a plain-language error")
	}
	if res.Description != "" {
		t.Errorf("degraded result must not carry a description, got %q", res.Description)
	}
	if res.Tool != ToolName {
		t.Errorf("degraded result still names the tool, got %q", res.Tool)
	}
	if !strings.Contains(res.Error, wantSubstr) {
		t.Errorf("error %q does not contain %q", res.Error, wantSubstr)
	}
}

func assertRunnerNotCalled(t *testing.T, fr *fakeRunner) {
	t.Helper()
	if fr.gotArgs != nil || fr.gotBin != "" {
		t.Errorf("model runner must NOT be called on a pre-flight degrade (bin=%q args=%v)", fr.gotBin, fr.gotArgs)
	}
}

// withDefaults: flags win over env over hardcoded defaults. ------------------

func TestWithDefaults_precedenceAndEnvFallback(t *testing.T) {
	t.Setenv(EnvBin, "/env/llama.exe")
	t.Setenv(EnvDir, "/env/models")
	t.Setenv(EnvModel, "/env/model.gguf")

	// Flag set -> wins over env.
	got := withDefaults(Options{Bin: "/flag/llama.exe"})
	if got.Bin != "/flag/llama.exe" {
		t.Errorf("flag should win: got %q", got.Bin)
	}
	// No flag -> env wins over hardcoded default.
	if got.ModelDir != "/env/models" {
		t.Errorf("env dir should be used: got %q", got.ModelDir)
	}
	if got.Model != "/env/model.gguf" {
		t.Errorf("env model should be used: got %q", got.Model)
	}
	// Prompt + NGL fall back to package defaults when unset everywhere.
	if got.Prompt != DefaultPrompt {
		t.Errorf("prompt default: got %q want %q", got.Prompt, DefaultPrompt)
	}
	if got.NGL != DefaultNGL {
		t.Errorf("ngl default: got %d want %d", got.NGL, DefaultNGL)
	}
}

func TestWithDefaults_usesHardcodedWhenNoEnv(t *testing.T) {
	// Ensure env is clear for this case.
	t.Setenv(EnvBin, "")
	t.Setenv(EnvDir, "")
	got := withDefaults(Options{})
	if got.Bin != DefaultBin {
		t.Errorf("bin default: got %q want %q", got.Bin, DefaultBin)
	}
	if got.ModelDir != DefaultModelDir {
		t.Errorf("dir default: got %q want %q", got.ModelDir, DefaultModelDir)
	}
}
