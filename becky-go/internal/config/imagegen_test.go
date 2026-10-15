package config

import (
	"os"
	"path/filepath"
	"testing"
)

// ImageGen picks Qwen-Image-2.1 by default and falls back to Krea-2 on --krea,
// BECKY_IMAGEGEN_VARIANT=krea, or a missing Qwen file (degrade, never crash).
func TestImageGen_qwenDefaultKreaFallback(t *testing.T) {
	dir := t.TempDir()
	qwen := filepath.Join(dir, "qwen-image-2.1-Q5_K_S.gguf")
	if err := os.WriteFile(qwen, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	c := Config{
		SDCli: "sd-cli", QwenImageModel: qwen, QwenImageVAE: "qvae", QwenImageTextEncoder: "qllm",
		QwenImageVision: "qmm", Krea2Model: "krea.gguf", Krea2VAE: "wan", Krea2TextEncoder: "kllm",
	}
	t.Setenv("BECKY_IMAGEGEN_VARIANT", "")

	_, model, vae, llm, vision, label := c.ImageGen(false, false)
	if model != qwen || vae != "qvae" || llm != "qllm" || vision != "qmm" || label != "qwen-image-2.1" {
		t.Errorf("default: got %s %s %s %s %s", model, vae, llm, vision, label)
	}

	_, model, vae, _, vision, label = c.ImageGen(true, false)
	if model != "krea.gguf" || vae != "wan" || vision != "" || label != "krea-2" {
		t.Errorf("--krea: got %s %s %q %s", model, vae, vision, label)
	}

	t.Setenv("BECKY_IMAGEGEN_VARIANT", "krea")
	if _, model, _, _, _, _ = c.ImageGen(false, false); model != "krea.gguf" {
		t.Errorf("env krea: got %s", model)
	}

	t.Setenv("BECKY_IMAGEGEN_VARIANT", "")
	c.QwenImageModel = filepath.Join(dir, "missing.gguf")
	if _, model, _, _, _, _ = c.ImageGen(false, false); model != "krea.gguf" {
		t.Errorf("missing qwen file should fall back to Krea-2, got %s", model)
	}
}
