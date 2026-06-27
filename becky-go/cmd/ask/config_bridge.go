// config_bridge.go — the single point where becky-ask reads the shared becky
// config. We reuse internal/config (read-only) for the llama-server.exe path AND
// the intent model GGUF, so becky-ask uses the SAME binary + the SAME Qwen3.5-4B
// orchestrator every other becky tool does — no hardcoded path. BECKY_ASK_MODEL
// (run.go) and BECKY_QWEN_MODEL (config) are the override seams.
package main

import "becky-go/internal/config"

// resolveLlamaServer returns the configured llama-server.exe path (cfg.LlamaServer),
// the same one internal/avlm uses. config.Load() already falls back to the known
// C:\llama.cpp\build\bin location and PATH, so this never hardcodes.
func resolveLlamaServer() string {
	return config.Load().LlamaServer
}

// resolveQwenModel returns the becky-wide Qwen3.5-4B orchestrator GGUF
// (UD-Q4_K_XL) from config.Qwen() — the SAME model every becky tool shares, so
// the path is configured in ONE place, never hardcoded per tool. Empty when
// config resolves nothing (run.go then falls back to its on-disk default const).
func resolveQwenModel() string {
	m, _, _ := config.Load().Qwen()
	return m
}
