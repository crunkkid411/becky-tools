// Command neutts-air is the thin launcher that becky-tts shells out to as its
// BECKY_TTS_BIN. It exists so the Go side (internal/tts) can stay model-free: it
// forwards the exact NeuTTSArgs argv (--model/--text/--out/--voice/--seed[/--rate])
// to `python tts_neutts.py` running in the dedicated, isolated TTS venv.
//
// It is NOT a becky tool (it lives under internal/, so build-all-tools.bat does
// not pick it up). Build it directly to the becky models dir:
//
//	go build -o X:\AI-2\becky-tools\models\tts\neutts-air.exe ./internal/pyhelpers/neutts_launcher
//
// then point becky-tts at it:
//
//	setx BECKY_TTS_BIN   "X:\AI-2\becky-tools\models\tts\neutts-air.exe"
//	setx BECKY_TTS_MODEL "X:\AI-2\becky-tools\models\tts\neutts-air-Q4_0.gguf"
//
// Overrides (env): BECKY_TTS_PY (the venv python), BECKY_TTS_SCRIPT (the helper).
package main

import (
	"os"
	"os/exec"
)

const (
	defaultPython = `X:\AI-2\becky-tools\models\tts\venv\Scripts\python.exe`
	defaultScript = `X:\AI-2\becky-tools\becky-go\internal\pyhelpers\tts_neutts.py`
)

func getenv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func main() {
	python := getenv("BECKY_TTS_PY", defaultPython)
	script := getenv("BECKY_TTS_SCRIPT", defaultScript)

	// Forward every flag becky-tts passed straight through to the helper.
	args := append([]string{script}, os.Args[1:]...)
	cmd := exec.Command(python, args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			os.Exit(ee.ExitCode())
		}
		os.Stderr.WriteString("neutts-air launcher: " + err.Error() + "\n")
		os.Exit(1)
	}
}
