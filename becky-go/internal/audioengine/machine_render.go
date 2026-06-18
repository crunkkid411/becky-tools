package audioengine

// machine_render.go — pure-Go, deterministic render of a drummachine.Machine's
// pattern to a mono float32 buffer. NO build tag: every function here is headless,
// cgo-free, and fully unit-testable on CI. The cgo play path (machine_audio.go,
// //go:build audio) calls RenderMachine to get a buffer, encodes it to a temp WAV,
// and hands it to the existing becky_play_wav path — same render-then-play
// discipline as synth_audio.go (Go never runs on the audio thread).
//
// Why a dedicated schedule type (not SequenceDrumGrid): a Machine pad carries
// per-voice settings the GM-note path can't express — Level (gain), Pan,
// PitchSemitones (resample ratio), Decay (amp envelope), and ChokeGroup (a later
// hit in a group cuts an earlier one). So we expand a Pattern into []MachineEvent
// keyed by PAD INDEX, carrying those params, then render with the per-pad sample
// from a MachineKit (sine fallback when a pad has no sample). Swing biases the
// off-beat 1/16 cells exactly like the rest of becky's groove math.
//
// Determinism contract: same Machine + same MachineKit + same sampleRate → a
// byte-identical buffer. Events are produced in a fixed order (pad index, then
// step), sample offsets are integer-rounded once, and choke cutoffs are resolved
// by deterministic offset comparison. No maps in any output-ordering path.

import (
	"math"
	"sort"

	"becky-go/internal/drummachine"
)

// stepsPerBeat is the step resolution of a Machine pattern: 1/16 cells -> 4 steps
// per quarter-note beat. (Patterns are 16/32/64 cells == 1/2/4 bars of 4/4.)
const stepsPerBeat = 4

// MachineEvent is one scheduled pad hit. Unlike ScheduledEvent (a flat MIDI
// note-on/off), it carries the pad index and the per-voice render params so the
// renderer can apply gain/pan/pitch/decay and resolve chokes without re-reading
// the Machine. SampleOffset is the absolute frame the hit fires at (integer,
// rounded once — the determinism anchor).
type MachineEvent struct {
	SampleOffset int64   // absolute frame the hit fires at
	Pad          int     // pad index 0..15
	Velocity     int     // 1..127
	Level        float64 // pad linear gain 0..1
	Pan          float64 // -1..+1 (carried for stereo Phase-2; mono render uses gain only)
	PitchSemis   float64 // playback transpose in semitones (resample ratio)
	DecaySec     float64 // amp decay seconds; 0 = play the full sample / one-shot
	ChokeGroup   int     // 0 = none; a later same-group hit cuts this one
	Note         int     // GM percussion note (for the sine fallback when no sample)
}

// SwingStepOffsetTicks returns the tick nudge applied to an off-beat 1/16 step for
// a given swing amount (0.5 == straight, up to 0.75). Even steps (0,2,4,…) are on
// the beat and never move; odd steps are delayed by up to half a step. This mirrors
// the swing convention used across becky's groove tools.
//
// stepTicks is the tick length of one 1/16 cell; the result is in the same ticks.
func swingDelayTicks(step int, swing float64, stepTicks float64) float64 {
	if step%2 == 0 {
		return 0
	}
	// swing 0.5 -> 0 delay; swing 0.75 -> half a step late.
	frac := (swing - 0.5) // 0..0.25
	if frac < 0 {
		frac = 0
	}
	return frac * 2.0 * (stepTicks / 2.0) // == frac * stepTicks
}

// SequenceMachinePattern expands one Pattern of a Machine into a deterministically
// ordered []MachineEvent at the given sample rate, honoring mute/solo (via
// AudiblePads), per-pad level/pan/pitch/decay, swing, and choke cutoffs.
//
// Steps:
//  1. AudiblePads() decides which pads sound (solo wins, else all non-muted).
//  2. Each active step of an audible pad becomes a MachineEvent; odd steps are
//     swing-delayed.
//  3. ChokeGroup cutoffs: within a non-zero group, an earlier hit's sample is cut
//     when a later hit in the same group fires — modeled here by trimming the
//     earlier event's DecaySec so its tail ends at the later hit's offset.
//
// An empty/nil pattern or no audible hits returns (nil) — degrade, not an error.
func SequenceMachinePattern(m *drummachine.Machine, pat drummachine.Pattern, sampleRate int) []MachineEvent {
	if m == nil || sampleRate <= 0 {
		return nil
	}
	steps := pat.Steps
	if steps <= 0 {
		steps = len(maxLane(pat))
	}
	if steps <= 0 {
		return nil
	}

	tempo := m.Tempo
	if tempo <= 0 {
		tempo = 120
	}
	// samples per 1/16 step = (60/bpm)/4 * sampleRate.
	stepSeconds := (60.0 / tempo) / float64(stepsPerBeat)
	stepSamples := stepSeconds * float64(sampleRate)
	// Work in "ticks" == samples for the swing nudge so we keep one rounding.
	stepTicksAsSamples := stepSamples

	audible := boolSet(m.AudiblePads())

	var events []MachineEvent
	for pad := 0; pad < drummachine.PadCount && pad < len(pat.Lanes); pad++ {
		if !audible[pad] {
			continue
		}
		p := m.Kit.Pads[pad]
		lane := pat.Lanes[pad]
		for step := 0; step < steps && step < len(lane); step++ {
			cell := lane[step]
			if !cell.On {
				continue
			}
			vel := cell.Vel
			if vel <= 0 {
				vel = 100
			}
			base := float64(step) * stepSamples
			base += swingDelayTicks(step, pat.Swing, stepTicksAsSamples)
			events = append(events, MachineEvent{
				SampleOffset: int64(math.Round(base)),
				Pad:          pad,
				Velocity:     clampVelInt(vel),
				Level:        clampUnit(p.Level),
				Pan:          clampPan(p.Pan),
				PitchSemis:   p.PitchSemitones,
				DecaySec:     maxZero(p.Decay),
				ChokeGroup:   maxZeroInt(p.ChokeGroup),
				Note:         p.MidiNote,
			})
		}
	}
	if len(events) == 0 {
		return nil
	}

	sortMachineEvents(events)
	applyChokeCutoffs(events, sampleRate)
	return events
}

// SequenceMachine sequences the Machine's CURRENTLY SELECTED pattern (scene 0's
// pattern when present, else bank pattern 0). The GUI passes whichever pattern the
// producer is editing via SequenceMachinePattern; this is the convenience default.
func SequenceMachine(m *drummachine.Machine, sampleRate int) []MachineEvent {
	if m == nil {
		return nil
	}
	if pat, ok := m.PatternForScene(0); ok {
		return SequenceMachinePattern(m, pat, sampleRate)
	}
	if m.PatternCount() > 0 {
		// Fall back to the first bank pattern via a scene-independent read.
		if pat, ok := m.PatternForScene(0); ok {
			return SequenceMachinePattern(m, pat, sampleRate)
		}
	}
	return nil
}

// applyChokeCutoffs trims each event's DecaySec so that, within a non-zero choke
// group, an earlier hit's voice is cut off the instant a later same-group hit
// fires. Events must already be sorted by SampleOffset ascending. Deterministic:
// it only reads/writes integer offsets and the float DecaySec.
func applyChokeCutoffs(events []MachineEvent, sampleRate int) {
	// For each event, find the next later event in the SAME choke group and clamp
	// this event's decay to end at that offset.
	for i := range events {
		g := events[i].ChokeGroup
		if g == 0 {
			continue
		}
		for j := i + 1; j < len(events); j++ {
			if events[j].ChokeGroup != g {
				continue
			}
			gap := events[j].SampleOffset - events[i].SampleOffset
			if gap <= 0 {
				continue
			}
			gapSec := float64(gap) / float64(sampleRate)
			// A choke forces a (short) decay ending exactly at the next hit.
			if events[i].DecaySec == 0 || events[i].DecaySec > gapSec {
				events[i].DecaySec = gapSec
			}
			break // only the immediately-following same-group hit chokes it
		}
	}
}

// RenderMachine renders a Machine's pattern (sequenced via SequenceMachinePattern)
// to a mono float32 buffer of numSamples frames, using kit for per-pad samples and
// the sine fallback for pads without one. Per-pad Level scales amplitude; PitchSemis
// resamples the one-shot (or retunes the sine); DecaySec applies a linear amp decay
// (0 = full sample length). Pan is carried but the mono buffer folds L+R to one
// channel (true stereo width is Phase-2). Output is tanh soft-limited to (-1,+1).
//
// numSamples <= 0 → nil. An empty schedule → a zeroed (silent) buffer. Deterministic.
func RenderMachine(events []MachineEvent, sampleRate int, numSamples int64, kit *MachineKit) []float32 {
	if numSamples <= 0 || sampleRate <= 0 {
		return nil
	}
	buf := make([]float32, numSamples)
	if len(events) == 0 {
		return buf
	}

	poly := newPolyphony(sampleRate)
	var plays []machinePlay
	// Pre-stage: convert each event into either a sample play or a sine note-on.
	// We do this lazily in the sample loop (offset-gated) to keep the sine voice's
	// attack timing identical to synth.go.
	ei := 0
	for s := int64(0); s < numSamples; s++ {
		for ei < len(events) && events[ei].SampleOffset <= s {
			ev := events[ei]
			ei++
			pcm := resamplePitch(kit.padSample(ev.Pad), ev.PitchSemis)
			amp := (float64(ev.Velocity) / 127.0) * ev.Level
			if pcm != nil {
				decayLen := decaySamplesFor(ev.DecaySec, len(pcm), sampleRate)
				plays = append(plays, machinePlay{pcm: pcm, amp: amp, decayLen: decayLen})
				continue
			}
			// No sample for this pad: sine fallback on channel 9 (percussion decay).
			poly.noteOn(ScheduledEvent{
				SampleOffset: ev.SampleOffset,
				Note:         ev.Note,
				On:           true,
				Velocity:     scaleVel(ev.Velocity, ev.Level),
				Channel:      9,
			})
		}

		raw := poly.tick()
		for i := range plays {
			pp := &plays[i]
			if pp.pos < len(pp.pcm) {
				env := pp.envAt()
				raw += float32(pp.amp) * pp.pcm[pp.pos] * float32(env)
				pp.pos++
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

// MachineDurationSamples returns enough samples to hold a one-shot render of the
// schedule: the last hit's offset plus a 1-second tail for decays. 0 for an empty
// schedule (degrade, not error).
func MachineDurationSamples(events []MachineEvent, sampleRate int) int64 {
	if len(events) == 0 || sampleRate <= 0 {
		return 0
	}
	last := events[len(events)-1].SampleOffset
	return last + int64(sampleRate) // 1 s tail
}

// MachineLoopSamples returns the sample length of ONE full pattern bar-cycle for
// seamless looping: steps * samples-per-step. This is the tiling unit used by the
// audio loop path (one cycle rendered, then repeated). 0 for invalid input.
func MachineLoopSamples(m *drummachine.Machine, pat drummachine.Pattern, sampleRate int) int64 {
	if m == nil || sampleRate <= 0 {
		return 0
	}
	steps := pat.Steps
	if steps <= 0 {
		steps = len(maxLane(pat))
	}
	if steps <= 0 {
		return 0
	}
	tempo := m.Tempo
	if tempo <= 0 {
		tempo = 120
	}
	stepSeconds := (60.0 / tempo) / float64(stepsPerBeat)
	return int64(math.Round(stepSeconds * float64(steps) * float64(sampleRate)))
}

// machinePlay is one in-flight pad sample being mixed into the render, with an
// optional linear amp decay (decayLen <= 0 means "play full length, no decay").
type machinePlay struct {
	pcm      []float32
	pos      int
	amp      float64
	decayLen int // samples over which amplitude ramps 1->0; 0 = no decay
}

// envAt returns the linear decay envelope multiplier at the current position.
func (p *machinePlay) envAt() float64 {
	if p.decayLen <= 0 {
		return 1.0
	}
	if p.pos >= p.decayLen {
		return 0
	}
	return 1.0 - float64(p.pos)/float64(p.decayLen)
}

// decaySamplesFor converts a pad's DecaySec into a sample count, capped at the
// sample length. 0 sec -> 0 (no decay, full one-shot).
func decaySamplesFor(decaySec float64, sampleLen, sampleRate int) int {
	if decaySec <= 0 {
		return 0
	}
	n := int(math.Round(decaySec * float64(sampleRate)))
	if n < 1 {
		n = 1
	}
	if n > sampleLen {
		n = sampleLen
	}
	return n
}

// resamplePitch linearly resamples a one-shot by a pitch ratio = 2^(semis/12).
// A positive semis speeds the sample up (higher pitch, shorter); negative slows it.
// 0 semis (or nil pcm) returns the input unchanged. Deterministic.
func resamplePitch(pcm []float32, semis float64) []float32 {
	if pcm == nil || semis == 0 {
		return pcm
	}
	ratio := math.Pow(2.0, semis/12.0) // playback speed multiplier
	if ratio <= 0 {
		return pcm
	}
	outLen := int(float64(len(pcm)) / ratio)
	if outLen <= 0 {
		return nil
	}
	out := make([]float32, outLen)
	for i := range out {
		srcPos := float64(i) * ratio
		i0 := int(srcPos)
		if i0 >= len(pcm) {
			break
		}
		frac := srcPos - float64(i0)
		s0 := pcm[i0]
		s1 := s0
		if i0+1 < len(pcm) {
			s1 = pcm[i0+1]
		}
		out[i] = s0 + (s1-s0)*float32(frac)
	}
	return out
}

// sortMachineEvents sorts by (SampleOffset ASC, Pad ASC) — a fixed, map-free order
// so the render is byte-deterministic.
func sortMachineEvents(ev []MachineEvent) {
	sort.SliceStable(ev, func(i, j int) bool {
		if ev[i].SampleOffset != ev[j].SampleOffset {
			return ev[i].SampleOffset < ev[j].SampleOffset
		}
		return ev[i].Pad < ev[j].Pad
	})
}

// ---- small pure helpers ----------------------------------------------------

func maxLane(p drummachine.Pattern) []drummachine.Step {
	var best []drummachine.Step
	for _, ln := range p.Lanes {
		if len(ln) > len(best) {
			best = ln
		}
	}
	return best
}

func boolSet(idx []int) map[int]bool {
	m := make(map[int]bool, len(idx))
	for _, i := range idx {
		m[i] = true
	}
	return m
}

func clampUnit(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

func clampPan(v float64) float64 {
	if v < -1 {
		return -1
	}
	if v > 1 {
		return 1
	}
	return v
}

func clampVelInt(v int) int {
	if v < 1 {
		return 1
	}
	if v > 127 {
		return 127
	}
	return v
}

func maxZero(v float64) float64 {
	if v < 0 {
		return 0
	}
	return v
}

func maxZeroInt(v int) int {
	if v < 0 {
		return 0
	}
	return v
}

// scaleVel scales a velocity by a 0..1 level, clamped to 1..127 (keeps the sine
// fallback's loudness consistent with the sample path's amp = vel/127 * level).
func scaleVel(vel int, level float64) int {
	scaled := int(math.Round(float64(vel) * clampUnit(level)))
	if scaled < 1 {
		scaled = 1
	}
	if scaled > 127 {
		scaled = 127
	}
	return scaled
}
