package audioengine

// drumkit.go — sample-based drum voices for pattern playback. NO build tag: pure
// Go, headless-testable. The audio play path (synth_audio.go) loads a DrumKit and
// passes it to RenderScheduleWithKit so GM-percussion notes (channel 9) trigger real
// one-shot WAVs (Jordan's kick/snare/hat/clap) instead of the sine fallback in
// synth.go. A missing sample silently degrades to the sine voice — never a crash.
//
// Kit layout: a directory with kick.wav / snare.wav / hat.wav / clap.wav. Each maps
// to its General-MIDI percussion note (the same notes gui_play.go writes):
//
//	kick → 36   snare → 38   hat → 42   clap → 39

import (
	"fmt"
	"math"
	"os"
	"path/filepath"

	"becky-go/internal/dsp"
)

// GM percussion note numbers for the four kit voices (match cmd/canvas drumLaneNote).
const (
	kitNoteKick  = 36
	kitNoteSnare = 38
	kitNoteHat   = 42
	kitNoteClap  = 39
)

// kitFiles maps each kit WAV filename to its GM percussion note.
var kitFiles = []struct {
	file string
	note int
}{
	{"kick.wav", kitNoteKick},
	{"snare.wav", kitNoteSnare},
	{"hat.wav", kitNoteHat},
	{"clap.wav", kitNoteClap},
}

// DrumKit holds decoded one-shot PCM (mono float32, target sample rate) keyed by
// GM percussion note. The zero value / nil kit means "no samples" (sine fallback).
type DrumKit struct {
	samples map[int][]float32
}

// sample returns the PCM for a note, or nil when absent (nil-safe).
func (k *DrumKit) sample(note int) []float32 {
	if k == nil {
		return nil
	}
	return k.samples[note]
}

// Len reports how many kit voices loaded (nil-safe).
func (k *DrumKit) Len() int {
	if k == nil {
		return 0
	}
	return len(k.samples)
}

// DefaultDrumKitDir is the becky-owned kit folder, overridable via BECKY_DRUM_KIT.
func DefaultDrumKitDir() string {
	if d := os.Getenv("BECKY_DRUM_KIT"); d != "" {
		return d
	}
	return `X:/AI-2/becky-tools/samples/kit`
}

// LoadDefaultDrumKit loads DefaultDrumKitDir() best-effort: returns nil (sine
// fallback) on any failure, never an error — degrade, never crash.
func LoadDefaultDrumKit(targetSampleRate int) *DrumKit {
	kit, err := LoadDrumKit(DefaultDrumKitDir(), targetSampleRate)
	if err != nil {
		return nil
	}
	return kit
}

// LoadDrumKit reads kick/snare/hat/clap WAVs from dir, decodes them, and resamples
// each to targetSampleRate. Missing/undecodable files are skipped (that note falls
// back to the sine voice). Returns an error only when NO sample could be loaded.
func LoadDrumKit(dir string, targetSampleRate int) (*DrumKit, error) {
	if targetSampleRate <= 0 {
		targetSampleRate = 48000
	}
	kit := &DrumKit{samples: make(map[int][]float32)}
	for _, kf := range kitFiles {
		b, err := os.ReadFile(filepath.Join(dir, kf.file))
		if err != nil {
			continue
		}
		au, err := dsp.DecodeWAV(b)
		if err != nil {
			continue
		}
		if pcm := resampleTo(au.Samples, au.SampleRate, targetSampleRate); len(pcm) > 0 {
			kit.samples[kf.note] = pcm
		}
	}
	if len(kit.samples) == 0 {
		return nil, fmt.Errorf("drumkit: no samples loaded from %q", dir)
	}
	return kit, nil
}

// resampleTo linearly resamples mono float64 PCM from srcRate to dstRate, returning
// float32. Equal rates are a straight copy. Deterministic.
func resampleTo(src []float64, srcRate, dstRate int) []float32 {
	if len(src) == 0 || srcRate <= 0 || dstRate <= 0 {
		return nil
	}
	if srcRate == dstRate {
		out := make([]float32, len(src))
		for i, v := range src {
			out[i] = float32(v)
		}
		return out
	}
	ratio := float64(srcRate) / float64(dstRate)
	outLen := int(float64(len(src)) / ratio)
	out := make([]float32, outLen)
	for i := range out {
		srcPos := float64(i) * ratio
		i0 := int(srcPos)
		frac := srcPos - float64(i0)
		s0 := src[i0]
		s1 := s0
		if i0+1 < len(src) {
			s1 = src[i0+1]
		}
		out[i] = float32(s0 + (s1-s0)*frac)
	}
	return out
}

// samplePlay is one in-flight one-shot sample being mixed into the render.
type samplePlay struct {
	pcm []float32
	pos int
	amp float64
}

// RenderScheduleWithKit is RenderSchedule with optional sample-based drums: a
// channel-9 note-on whose note is in kit triggers the kit's one-shot PCM (mixed in
// at the event offset); every other note uses the sine polyphony from synth.go.
// kit == nil reproduces RenderSchedule exactly. Output is tanh soft-limited to
// (-1, +1). Deterministic for fixed inputs.
func RenderScheduleWithKit(events []ScheduledEvent, sampleRate int, numSamples int64, kit *DrumKit) []float32 {
	if numSamples <= 0 || sampleRate <= 0 {
		return nil
	}
	buf := make([]float32, numSamples)
	if len(events) == 0 {
		return buf // silence
	}

	poly := newPolyphony(sampleRate)
	var plays []samplePlay
	ei := 0
	for s := int64(0); s < numSamples; s++ {
		for ei < len(events) && events[ei].SampleOffset <= s {
			ev := events[ei]
			ei++
			if !ev.On {
				poly.noteOff(ev)
				continue
			}
			if ev.Channel == 9 {
				if pcm := kit.sample(ev.Note); pcm != nil {
					amp := float64(ev.Velocity) / 127.0
					if ev.Velocity <= 0 {
						amp = 0.5
					}
					plays = append(plays, samplePlay{pcm: pcm, amp: amp})
					continue
				}
			}
			poly.noteOn(ev)
		}

		raw := poly.tick()
		for i := range plays {
			if plays[i].pos < len(plays[i].pcm) {
				raw += float32(plays[i].amp) * plays[i].pcm[plays[i].pos]
				plays[i].pos++
			}
		}

		limited := math.Tanh(float64(raw))
		if limited > 0.999 {
			limited = 0.999
		} else if limited < -0.999 {
			limited = -0.999
		}
		buf[s] = float32(limited)
	}
	return buf
}
