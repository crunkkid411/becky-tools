//go:build gui

// gui_play.go — make the machine AUDIBLE, the becky way: this window is a pure
// `-tags gui` build with NO cgo; all sound lives in the sibling becky-daw-engine
// (built `-tags audio`), exec'd exactly like cmd/canvas/gui_play.go does.
//
//	▶ Play : stage the live machine to a temp machine.json, then exec
//	         becky-daw-engine --play-machine <tmp> --loops 16  (a seamless groove).
//	         ■ Stop kills the process mid-loop.
//	pad    : exec becky-daw-engine --play-pad <tmp> --pad N for instant audition.
//
// The engine already supports --play-machine / --play-pad (cmd/daw-engine/machine.go).
// degrade-never-crash: a missing engine exe, an empty machine, or a non-zero exit all
// surface as one quiet status line — never a panic, never a frozen window. Engine
// runs happen on a goroutine so the UI thread never blocks.
package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// playLoops is how many times ▶ tiles the bar for a continuous groove. ■ Stop kills
// the process mid-loop, so this is just the upper bound.
const playLoops = 16

// startPlay stages the live machine and plays it looped through becky-daw-engine.
// Non-blocking: the engine runs on a goroutine; the live process handle is stored so
// ■ Stop can kill it. Already-playing is a no-op.
func (a *App) startPlay() {
	a.mu.Lock()
	if a.playing {
		a.mu.Unlock()
		return
	}
	a.playing = true
	a.mu.Unlock()
	a.setStatus("▶ playing…")

	go func() {
		if err := a.execPlayMachine(); err != nil {
			a.setStatus(err.Error())
		}
		a.mu.Lock()
		a.playing = false
		a.mu.Unlock()
		a.window.Invalidate()
	}()
}

// stopPlay kills the live engine process so the loop stops immediately. A killed
// process is a clean stop, not an error (execPlayMachine treats it as such). Safe to
// call when nothing is playing.
func (a *App) stopPlay() {
	a.mu.Lock()
	proc := a.playProc
	a.playProc = nil
	a.playing = false
	a.mu.Unlock()
	if proc != nil {
		_ = proc.Kill()
	}
	a.setStatus("■ stopped.")
	a.window.Invalidate()
}

// execPlayMachine stages the machine to a temp file and runs
// becky-daw-engine --play-machine <tmp> --loops N. Blocking; call from a goroutine.
func (a *App) execPlayMachine() error {
	tmp, err := a.stageMachine()
	if err != nil {
		return err
	}
	defer os.Remove(tmp)

	exePath, err := a.engineExe()
	if err != nil {
		return err
	}
	cmd := exec.Command(exePath, "--play-machine", tmp, "--loops", strconv.Itoa(playLoops))
	var outBuf strings.Builder
	cmd.Stdout, cmd.Stderr = &outBuf, &outBuf
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("couldn't start the audio engine: %w", err)
	}
	a.mu.Lock()
	a.playProc = cmd.Process
	a.mu.Unlock()

	runErr := cmd.Wait()

	a.mu.Lock()
	killed := a.playProc == nil // Stop cleared it → user-initiated, not a failure
	a.playProc = nil
	a.mu.Unlock()
	if killed {
		return nil
	}
	if runErr != nil {
		if msg := firstLine(strings.TrimSpace(outBuf.String())); msg != "" {
			return fmt.Errorf("%s", msg)
		}
		return fmt.Errorf("audio engine: %s", runErr.Error())
	}
	return nil
}

// auditionPad plays a single pad once for instant feedback on click/select. Best-
// effort: a missing engine, an empty machine, or any failure is SILENT (a pad click
// must always feel instant; the window must work without the audio engine). Runs on a
// goroutine and does not touch a.playProc (the loop transport is separate).
func (a *App) auditionPad(pad int) {
	exePath, err := a.engineExe()
	if err != nil {
		return // no engine → silent; the click still selected the pad
	}
	go func() {
		tmp, terr := a.stageMachine()
		if terr != nil {
			return
		}
		defer os.Remove(tmp)
		cmd := exec.Command(exePath, "--play-pad", tmp, "--pad", strconv.Itoa(pad))
		_ = cmd.Run() // best-effort; ignore output + errors (instant-audition path)
	}()
}

// stageMachine writes the live machine to a temp machine.json and returns its path
// (the caller removes it). Degrade-never-crash on any IO error -> a typed error.
func (a *App) stageMachine() (string, error) {
	data, err := a.machine.MarshalBytes()
	if err != nil {
		return "", fmt.Errorf("couldn't build the machine: %w", err)
	}
	f, err := os.CreateTemp("", "becky-drummachine-*.json")
	if err != nil {
		return "", fmt.Errorf("couldn't stage the machine: %w", err)
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		os.Remove(f.Name())
		return "", err
	}
	f.Close()
	return f.Name(), nil
}

// engineExe resolves becky-daw-engine next to becky-drummachine (or in the CWD /
// ./bin), mirroring cmd/canvas/gui_tools.go resolveExe. Returns a friendly error
// when it's not found so the caller can show one quiet line.
func (a *App) engineExe() (string, error) {
	name := "becky-daw-engine"
	if isWindows() {
		name += ".exe"
	}
	if self, err := os.Executable(); err == nil {
		if cand := filepath.Join(filepath.Dir(self), name); fileExists(cand) {
			return cand, nil
		}
	}
	if wd, err := os.Getwd(); err == nil {
		if cand := filepath.Join(wd, name); fileExists(cand) {
			return cand, nil
		}
		if cand := filepath.Join(wd, "bin", name); fileExists(cand) {
			return cand, nil
		}
	}
	return "", fmt.Errorf("audio engine not found next to becky-drummachine — build with build-all-tools.bat")
}

// fileExists reports whether path is an existing regular file.
func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

// firstLine returns the first non-empty line of s (a multi-line engine error reads as
// one quiet status line).
func firstLine(s string) string {
	for _, line := range strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n") {
		if t := strings.TrimSpace(line); t != "" {
			return t
		}
	}
	return ""
}
