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
