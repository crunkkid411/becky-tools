//go:build gui

// gui_play.go — make a beat AUDIBLE. The ▶ Play button (drum + piano modes) turns
// the current pattern into a project.json and hands it to the sibling
// becky-daw-engine, which renders + plays it through the real audio synth
// (`--play-pattern-audio`). The canvas itself stays a pure `-tags gui` build with no
// cgo: all sound happens in the already-audio-built engine exe, composed the becky
// way (one tool does one thing). degrade-never-crash: a missing engine, an empty
// grid, or a non-zero exit all surface as one quiet neon line — never a panic.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"gioui.org/layout"
	"gioui.org/unit"
	"gioui.org/widget/material"

	"becky-go/internal/canvas"
	"becky-go/internal/dawmodel"
	"becky-go/internal/habits"
)

// drumLaneNote maps a drum lane index to its General-MIDI percussion note so the
// synth voices each lane as the right drum. Order matches gui_drum.go's lanes and
// internal/canvas gesture.LaneName: kick / snare / hat / clap.
var drumLaneNote = [drumLaneCount]int{36, 38, 42, 39}

// drumChannel is GM percussion (channel 9, zero-based). The synth treats channel 9
// as percussion (short decay, ignores note-off) — see internal/audioengine/synth.go.
const drumChannel = 9

// hasActiveCells reports whether any drum step is on (nothing to play if not).
func (d *drumGrid) hasActiveCells() bool {
	for lane := 0; lane < drumLaneCount; lane++ {
		for step := 0; step < drumStepCount; step++ {
			if d.cells[lane][step] {
				return true
			}
		}
	}
	return false
}

// arrangementFromDrum builds a playable dawmodel.Arrangement from the 4×16 grid: one
// MIDI track whose notes are the active steps (a 16th-note bar). Deterministic.
func arrangementFromDrum(d *drumGrid) *dawmodel.Arrangement {
	arr := dawmodel.New() // BPM 120, PPQ music.PPQ
	stepTicks := arr.PPQ / 4
	if stepTicks <= 0 {
		stepTicks = 120
	}
	var notes []dawmodel.Note
	var id uint64
	for lane := 0; lane < drumLaneCount; lane++ {
		for step := 0; step < drumStepCount; step++ {
			if !d.cells[lane][step] {
				continue
			}
			id++
			notes = append(notes, dawmodel.Note{
				ID:    id,
				Start: step * stepTicks,
				Dur:   stepTicks / 2,
				Pitch: drumLaneNote[lane],
				Vel:   110,
				Ch:    drumChannel,
			})
		}
	}
	arr.NextID = id
	arr.Tracks = []dawmodel.Track{{
		ID:   "drums",
		Kind: dawmodel.KindMIDI,
		Clips: []dawmodel.Clip{{
			Name:    "drums",
			Channel: drumChannel,
			Program: -1, // percussion
			Notes:   notes,
		}},
	}}
	return arr
}

// resolvePlayJSON returns the project.json path to play. A .json target is played
// directly; otherwise the in-canvas drum grid is serialised to a temp file (returned
// as toClean for the caller to remove). Returns an error when there's nothing to play.
func resolvePlayJSON(target string, d *drumGrid) (path, toClean string, err error) {
	t := strings.TrimSpace(target)
	if strings.HasSuffix(strings.ToLower(t), ".json") && fileExists(t) {
		return t, "", nil
	}
	if d != nil && d.hasActiveCells() {
		data, merr := json.Marshal(arrangementFromDrum(d))
		if merr != nil {
			return "", "", fmt.Errorf("couldn't build the pattern: %w", merr)
		}
		f, ferr := os.CreateTemp("", "becky-canvas-pattern-*.json")
		if ferr != nil {
			return "", "", fmt.Errorf("couldn't stage the pattern: %w", ferr)
		}
		if _, werr := f.Write(data); werr != nil {
			f.Close()
			os.Remove(f.Name())
			return "", "", werr
		}
		f.Close()
		return f.Name(), f.Name(), nil
	}
	return "", "", fmt.Errorf("nothing to play — paint a beat on the drum grid, or open a project.json")
}

// execPlay resolves a playable project.json (the target, or the drum grid serialised
// to a temp file) and plays it audibly through the sibling becky-daw-engine. Blocking;
// call it from a goroutine. Degrade-never-crash: every failure is a returned error the
// caller surfaces as a quiet neon line.
func execPlay(target string, _ canvas.Mode, d *drumGrid) error {
	jsonPath, toClean, err := resolvePlayJSON(target, d)
	if err != nil {
		return err
	}
	if toClean != "" {
		defer os.Remove(toClean)
	}

	exeName := "becky-daw-engine"
	if isWindows() {
		exeName += ".exe"
	}
	exePath, err := resolveExe(exeName)
	if err != nil {
		return fmt.Errorf("audio engine not found next to becky-canvas — build with build-all-tools.bat")
	}

	out, runErr := exec.Command(exePath, "--play-pattern-audio", jsonPath).CombinedOutput()
	if runErr != nil {
		msg := firstLine(strings.TrimSpace(string(out)))
		if msg == "" {
			msg = runErr.Error()
		}
		return fmt.Errorf("%s", msg)
	}
	return nil
}

// firstLine returns the first non-empty line of s (so a multi-line engine error reads
// as one quiet neon line in the output panel).
func firstLine(s string) string {
	for _, line := range strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n") {
		if t := strings.TrimSpace(line); t != "" {
			return t
		}
	}
	return ""
}

// logDrumEdit records one drum-step toggle as a habits correction (best-effort) so
// becky learns Jordan's by-eye beat fixes. Failure is silently ignored (degrade).
func (a *App) logDrumEdit(lane, step int, was, now bool) {
	logPath := overlayHabitsLogPath()
	if logPath == "" {
		return
	}
	_ = canvas.AppendDrumEdit(logPath,
		canvas.DrumCellEdit{Lane: lane, Step: step, WasOn: was, IsOn: now},
		habits.AppendCorrectionLog)
}

// layoutTransport draws the ▶ Play / ■ Stop transport row above the agent box. It is
// shown only in drum + piano modes and takes ZERO space otherwise (matching the
// overlay). The buttons reuse the overlay's neon-square affordance for one visual
// language across the window.
func (a *App) layoutTransport(gtx layout.Context) layout.Dimensions {
	if a.activeMode != canvas.ModeDrum && a.activeMode != canvas.ModeMIDI {
		return layout.Dimensions{}
	}
	return layout.Inset{Top: unit.Dp(6), Bottom: unit.Dp(2)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return a.overlayBtn(gtx, &a.playBtn, "▶", colNeonGreen)
			}),
			layout.Rigid(layout.Spacer{Width: unit.Dp(8)}.Layout),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return a.overlayBtn(gtx, &a.stopBtn, "■", colCrimson)
			}),
			layout.Rigid(layout.Spacer{Width: unit.Dp(12)}.Layout),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return material.Caption(a.th, a.transportHint()).Layout(gtx)
			}),
		)
	})
}

// transportHint is the one short, plain-language line beside the transport buttons.
func (a *App) transportHint() string {
	a.mu.Lock()
	playing := a.playing
	a.mu.Unlock()
	if playing {
		return "▶ playing…"
	}
	if a.activeMode == canvas.ModeMIDI {
		return "piano — open a project.json, then ▶"
	}
	return "drum machine — paint a beat, then ▶"
}
