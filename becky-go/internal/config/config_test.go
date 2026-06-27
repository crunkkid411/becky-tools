package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestGemmaLabel(t *testing.T) {
	cases := map[string]string{
		`X:\m\gemma-4-E4B-it-qat-UD-Q4_K_XL.gguf`: "gemma-4-E4B-it-qat",
		`X:\m\gemma-4-12B-it-qat-UD-Q4_K_XL.gguf`: "gemma-4-12B-it-qat",
		`X:\m\gemma-4-12B-it-Q4_K_M.gguf`:         "gemma-4-12B-it",
		`X:\m\gemma-4-E4B-it-Q4_K_M.gguf`:         "gemma-4-E4B-it",
		``:                                        "gemma-4",
	}
	for path, want := range cases {
		if got := gemmaLabel(path); got != want {
			t.Errorf("gemmaLabel(%q) = %q, want %q", path, got, want)
		}
	}
}

func TestGemmaAVLMDefaultsToE4B(t *testing.T) {
	t.Setenv("BECKY_AVLM_VARIANT", "")
	c := Config{
		GemmaModel:    `X:\m\gemma-4-E4B-it-qat-UD-Q4_K_XL.gguf`,
		GemmaMMProj:   `X:\m\mmproj-BF16.gguf`,
		GemmaModel12B: `X:\m\gemma-4-12B-it-qat-UD-Q4_K_XL.gguf`,
	}
	model, mmproj, label := c.GemmaAVLM()
	if label != "gemma-4-E4B-it-qat" {
		t.Fatalf("default variant should be E4B QAT, got label %q", label)
	}
	if model != c.GemmaModel || mmproj != c.GemmaMMProj {
		t.Fatalf("default should return the E4B model+mmproj")
	}
}

func TestGemmaAVLM12BFallsBackWhenMissing(t *testing.T) {
	t.Setenv("BECKY_AVLM_VARIANT", "12b")
	c := Config{
		GemmaModel:    `X:\m\gemma-4-E4B-it-qat-UD-Q4_K_XL.gguf`,
		GemmaModel12B: `X:\m\does-not-exist-12b.gguf`, // not on disk
	}
	_, _, label := c.GemmaAVLM()
	if label != "gemma-4-E4B-it-qat" {
		t.Fatalf("12b requested but file absent should fall back to E4B; got %q", label)
	}
}

func TestQwenLabel(t *testing.T) {
	cases := map[string]string{
		`X:\m\Qwen3.5-4B-Q4_K_M.gguf`:             "qwen3.5-4b",
		`X:\m\Qwen3.5-4B-UD-Q4_K_XL.gguf`:         "qwen3.5-4b-UD-Q4_K_XL",
		`X:\m\Qwen3-4B-Instruct-2507-Q4_K_M.gguf`: "qwen3-4b-instruct-2507",
		``: "qwen3.5-4b",
	}
	for path, want := range cases {
		if got := qwenLabel(path); got != want {
			t.Errorf("qwenLabel(%q) = %q, want %q", path, got, want)
		}
	}
}

func TestQwenResolvesConfiguredModelAndMMProj(t *testing.T) {
	t.Setenv("BECKY_QWEN_MODEL", "")
	c := Config{
		QwenModel:  `X:\m\Qwen3.5-4B-Q4_K_M.gguf`,
		QwenMMProj: `X:\m\mmproj-F16.gguf`,
	}
	model, mmproj, label := c.Qwen()
	if model != c.QwenModel {
		t.Errorf("model = %q, want configured %q", model, c.QwenModel)
	}
	if mmproj != c.QwenMMProj {
		t.Errorf("mmproj = %q, want configured %q", mmproj, c.QwenMMProj)
	}
	if label != "qwen3.5-4b" {
		t.Errorf("label = %q, want qwen3.5-4b", label)
	}
}

func TestQwenEnvOverrideWins(t *testing.T) {
	t.Setenv("BECKY_QWEN_MODEL", `X:\override\Qwen3.5-4B-UD-Q4_K_XL.gguf`)
	c := Config{QwenModel: `X:\m\Qwen3.5-4B-Q4_K_M.gguf`}
	model, _, label := c.Qwen()
	if model != `X:\override\Qwen3.5-4B-UD-Q4_K_XL.gguf` {
		t.Errorf("BECKY_QWEN_MODEL must win, got %q", model)
	}
	if label != "qwen3.5-4b-UD-Q4_K_XL" {
		t.Errorf("label must follow the override file, got %q", label)
	}
}

func TestGemmaAVLM12BSelectedWhenPresent(t *testing.T) {
	t.Setenv("BECKY_AVLM_VARIANT", "12b")
	dir := t.TempDir()
	model12 := filepath.Join(dir, "gemma-4-12B-it-qat-UD-Q4_K_XL.gguf")
	mmproj12 := filepath.Join(dir, "mmproj-12B-BF16.gguf")
	if err := os.WriteFile(model12, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(mmproj12, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	c := Config{
		GemmaModel:     `X:\m\gemma-4-E4B-it-qat-UD-Q4_K_XL.gguf`,
		GemmaMMProj:    `X:\m\mmproj-BF16.gguf`,
		GemmaModel12B:  model12,
		GemmaMMProj12B: mmproj12,
	}
	model, mmproj, label := c.GemmaAVLM()
	if label != "gemma-4-12B-it-qat" || model != model12 || mmproj != mmproj12 {
		t.Fatalf("12b present + requested should select the 12B; got label=%q model=%q", label, model)
	}
}
