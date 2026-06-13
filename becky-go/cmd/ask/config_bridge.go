// config_bridge.go — the single point where becky-ask reads the shared becky
// config. We reuse internal/config (read-only) for the llama-server.exe path so
// the intent model uses the SAME binary every other becky tool does — no
// hardcoded path. The model GGUF path is resolved separately (run.go) because the
// shared config does not carry an ask-specific intent-model field, and the brief
// scopes edits to cmd/ask/; BECKY_ASK_MODEL is the override seam there.
package main

import "becky-go/internal/config"

// resolveLlamaServer returns the configured llama-server.exe path (cfg.LlamaServer),
// the same one internal/avlm uses. config.Load() already falls back to the known
// C:\llama.cpp\build\bin location and PATH, so this never hardcodes.
func resolveLlamaServer() string {
	return config.Load().LlamaServer
}
