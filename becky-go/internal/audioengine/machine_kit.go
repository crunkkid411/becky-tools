package audioengine

// machine_kit.go — the 16-pad sample kit loader for drummachine.Machine. NO build
// tag: pure Go, headless-testable. This is the wider sibling of drumkit.go's
// 4-voice DrumKit: where DrumKit keys a handful of one-shots by GM note, a
// MachineKit keys ONE decoded sample per PAD INDEX (0..15) so each of the 16 pads
// can carry its own arbitrary WAV (Jordan's actual kicks/snares/loops), with the
// pad's per-voice params (level/pan/pitch/decay/choke/mute/solo) applied at render
// time in machine_render.go.
//
// Degrade-never-crash: a pad with no SamplePath, a missing file, an undecodable
// WAV, or a short/empty buffer simply has no loaded sample — at render time that
// pad silently falls back to the sine voice (via the pad's MidiNote on channel 9),
// exactly as drumkit.go does. A bad kit dir is never fatal; LoadMachineKit always
// returns a usable (possibly empty) *MachineKit.

import (
	"os"
	"path/filepath"

	"becky-go/internal/drummachine"
	"becky-go/internal/dsp"
)

// MachineKit holds decoded one-shot PCM (mono float32 at a target sample rate)
// keyed by PAD INDEX (0..15). The zero value / nil kit means "no samples" — every
// pad then renders through the sine fallback. It is read-only after loading.
type MachineKit struct {
	sampleRate int
	samples    map[int][]float32 // pad index -> decoded PCM
}

// padSample returns the decoded PCM for a pad index, or nil when absent (nil-safe).
func (k *MachineKit) padSample(pad int) []float32 {
	if k == nil {
		return nil
	}
	return k.samples[pad]
}

// SampleRate reports the rate every loaded sample was resampled to (0 if nil).
func (k *MachineKit) SampleRate() int {
	if k == nil {
		return 0
	}
	return k.sampleRate
}

// PadHasSample reports whether pad has a loaded sample (else it sine-falls-back).
func (k *MachineKit) PadHasSample(pad int) bool {
	if k == nil {
		return false
	}
	_, ok := k.samples[pad]
	return ok
}

// Len reports how many pads loaded a real sample (nil-safe).
func (k *MachineKit) Len() int {
	if k == nil {
		return 0
	}
	return len(k.samples)
}

// isAbsCrossPlatform reports whether p looks absolute on EITHER Windows or POSIX,
// regardless of the host OS. This is the §2 "paths may be Windows paths on Linux"
// rule: a stored SamplePath like `C:\kits\kick.wav` must be treated as absolute
// even when the tool runs on CI/Linux (where filepath.IsAbs would say no), and a
// POSIX `/x/kick.wav` must stay absolute on Windows.
func isAbsCrossPlatform(p string) bool {
	if p == "" {
		return false
	}
	// POSIX absolute, or a UNC / `\share` path.
	if p[0] == '/' || p[0] == '\\' {
		return true
	}
	// Windows drive-letter absolute: `C:\` or `C:/`.
	if len(p) >= 3 && p[1] == ':' && (p[2] == '\\' || p[2] == '/') {
		c := p[0]
		if (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') {
			return true
		}
	}
	return false
}

// resolveSamplePath resolves a pad's SamplePath against the kit dir: an absolute
// path (Windows OR POSIX) is used as-is; a relative path is joined onto dir. An
// empty path yields "".
func resolveSamplePath(dir, samplePath string) string {
	if samplePath == "" {
		return ""
	}
	if isAbsCrossPlatform(samplePath) {
		return samplePath
	}
	return filepath.Join(dir, samplePath)
}

// LoadMachineKit loads up to 16 pad samples for m, resolving each pad's SamplePath
// (relative paths are joined with dir) and resampling to targetSampleRate. A pad
// with no path / a missing file / an undecodable or empty WAV is skipped (it will
// render through the sine fallback). Never returns an error and never panics — a
// nil Machine or an unreadable dir yields an empty kit (full sine fallback).
//
// This is the GUI's kit-load entry point: call it once after loading a machine.json,
// then hand the *MachineKit to RenderMachine / PlayMachineLoop / PlayPadOneShot.
func LoadMachineKit(dir string, m *drummachine.Machine) *MachineKit {
	const fallbackSR = 48000
	kit := &MachineKit{sampleRate: fallbackSR, samples: make(map[int][]float32)}
	if m == nil {
		return kit
	}
	for _, pad := range m.Kit.Pads {
		if pad.Index < 0 || pad.Index >= drummachine.PadCount {
			continue
		}
		path := resolveSamplePath(dir, pad.SamplePath)
		if path == "" {
			continue
		}
		b, err := os.ReadFile(path)
		if err != nil {
			continue // missing/unreadable -> sine fallback for this pad
		}
		au, err := dsp.DecodeWAV(b)
		if err != nil {
			continue // undecodable -> sine fallback
		}
		if pcm := resampleTo(au.Samples, au.SampleRate, kit.sampleRate); len(pcm) > 0 {
			kit.samples[pad.Index] = pcm
		}
	}
	return kit
}

// LoadMachineKitAt is LoadMachineKit with an explicit target sample rate (the GUI
// uses the engine's device rate). targetSampleRate <= 0 defaults to 48000.
func LoadMachineKitAt(dir string, m *drummachine.Machine, targetSampleRate int) *MachineKit {
	if targetSampleRate <= 0 {
		targetSampleRate = 48000
	}
	k := LoadMachineKit(dir, m)
	if k.sampleRate == targetSampleRate {
		return k
	}
	// Re-load at the requested rate (cheap; samples are small one-shots).
	k.sampleRate = targetSampleRate
	if m == nil {
		return k
	}
	k.samples = make(map[int][]float32)
	for _, pad := range m.Kit.Pads {
		if pad.Index < 0 || pad.Index >= drummachine.PadCount {
			continue
		}
		path := resolveSamplePath(dir, pad.SamplePath)
		if path == "" {
			continue
		}
		b, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		au, err := dsp.DecodeWAV(b)
		if err != nil {
			continue
		}
		if pcm := resampleTo(au.Samples, au.SampleRate, targetSampleRate); len(pcm) > 0 {
			k.samples[pad.Index] = pcm
		}
	}
	return k
}
