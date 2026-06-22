//go:build gui

// gui_speak.go — becky's VOICE, in the canvas (Jordan works in the GUI, not the CLI).
//
// A "Speak" button voices the last becky output line through NeuTTS Air. To stay
// FAST (the per-call CLI reloads the model ~35s every time), it talks to a persistent
// warm server (internal/pyhelpers/tts_server.py) that loads the model ONCE on the GPU.
// The canvas auto-starts that server on the first Speak and reuses it after, so the
// first click warms up (~once) and every later click is quick.
//
// degrade-never-crash: no model/server -> one quiet neon line, never a Microsoft voice,
// never a freeze (everything runs off the UI goroutine).
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"becky-go/internal/proc"
)

const (
	ttsPort       = 11436
	ttsHealthURL  = "http://127.0.0.1:11436/health"
	ttsSpeakURL   = "http://127.0.0.1:11436/speak"
	ttsBootWait   = 90 * time.Second // first load (import torch + model) can take ~30-60s
	ttsSpeakLimit = 120 * time.Second
)

// startSpeak voices text without blocking the UI. Empty text → a quiet note.
func (a *App) startSpeak(text string) {
	text = strings.TrimSpace(text)
	if text == "" {
		a.appendLine("nothing to speak yet — run something or type a line first")
		return
	}
	a.mu.Lock()
	if a.speaking {
		a.mu.Unlock()
		return // one utterance at a time
	}
	a.speaking = true
	a.mu.Unlock()

	a.appendLine("")
	a.appendLine("🔊 speaking…")
	go func() {
		err := a.speak(text)
		a.mu.Lock()
		a.speaking = false
		a.mu.Unlock()
		if err != nil {
			a.appendLine(err.Error())
		}
		a.window.Invalidate()
	}()
}

// speak ensures the warm voice server is up, asks it to synthesize text, and plays
// the returned WAV. Blocking; call from a goroutine.
func (a *App) speak(text string) error {
	if err := a.ensureVoiceServer(); err != nil {
		return err
	}
	body, _ := json.Marshal(map[string]any{"text": text, "voice": "default"})
	ctx, cancel := context.WithTimeout(context.Background(), ttsSpeakLimit)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, ttsSpeakURL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("voice: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("voice server didn't answer: %w", err)
	}
	defer resp.Body.Close()
	wav, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("voice: %s", firstLine(strings.TrimSpace(string(wav))))
	}
	if len(wav) < 64 {
		return fmt.Errorf("voice: empty audio")
	}
	return playWAVBytes(wav)
}

// ensureVoiceServer returns nil once the warm server answers /health, starting it
// (detached, no console) on first use. It is safe to call repeatedly.
func (a *App) ensureVoiceServer() error {
	if voiceHealthy() {
		return nil
	}
	a.mu.Lock()
	already := a.voiceProc != nil
	a.mu.Unlock()
	if !already {
		if err := a.launchVoiceServer(); err != nil {
			return err
		}
		a.appendLine("warming up becky's voice (one-time model load)…")
	}
	deadline := time.Now().Add(ttsBootWait)
	for time.Now().Before(deadline) {
		if voiceHealthy() {
			return nil
		}
		time.Sleep(500 * time.Millisecond)
	}
	return fmt.Errorf("voice didn't warm up in time — check the TTS model is installed")
}

// launchVoiceServer starts tts_server.py in the dedicated venv, detached + no console.
func (a *App) launchVoiceServer() error {
	py := getenvOr("BECKY_TTS_PY", `X:\AI-2\becky-tools\models\tts\venv\Scripts\python.exe`)
	script := getenvOr("BECKY_TTS_SERVER", `X:\AI-2\becky-tools\becky-go\internal\pyhelpers\tts_server.py`)
	model := getenvOr("BECKY_TTS_MODEL", `X:\AI-2\becky-tools\models\tts\neutts-air-Q4_0.gguf`)
	if !fileExists(py) {
		return fmt.Errorf("voice: python venv not found (%s) — TTS isn't installed yet", py)
	}
	cmd := exec.Command(py, script, "--model", model)
	cmd.Dir = filepath.Dir(script)
	proc.NoWindow(cmd) // no console window over the GUI
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("voice: couldn't start the server: %w", err)
	}
	a.mu.Lock()
	a.voiceProc = cmd.Process
	a.mu.Unlock()
	go func() { _ = cmd.Wait() }() // reap
	return nil
}

// stopVoiceServer kills the warm server (called on window close to free VRAM).
func (a *App) stopVoiceServer() {
	a.mu.Lock()
	p := a.voiceProc
	a.voiceProc = nil
	a.mu.Unlock()
	if p != nil {
		_ = p.Kill()
	}
}

// voiceHealthy reports whether the warm server answers /health with 200.
func voiceHealthy() bool {
	ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, ttsHealthURL, nil)
	if err != nil {
		return false
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)
	return resp.StatusCode == http.StatusOK
}

// playWAVBytes writes wav to a temp file and plays it (PowerShell SoundPlayer on
// Windows, no console). Best-effort: the synth already succeeded if we got here.
func playWAVBytes(wav []byte) error {
	f, err := os.CreateTemp("", "becky-speak-*.wav")
	if err != nil {
		return fmt.Errorf("voice: couldn't stage audio: %w", err)
	}
	path := f.Name()
	_, _ = f.Write(wav)
	f.Close()
	defer os.Remove(path)
	if !isWindows() {
		return nil // synth worked; playback path is Windows-specific here
	}
	ps := fmt.Sprintf("(New-Object System.Media.SoundPlayer '%s').PlaySync()", path)
	cmd := exec.Command("powershell", "-NoProfile", "-Command", ps)
	proc.NoWindow(cmd)
	return cmd.Run()
}

// getenvOr returns the env value for key or def when unset/empty.
func getenvOr(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}
