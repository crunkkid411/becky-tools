package audioengine

// sampler_engine.go — the new sampler-based render path for the 16-pad drum machine.
// NO build tag: pure Go, headless-testable, fully usable on CI.
//
// This complements machine_render.go's sine-tone / dsp-sample path with a path
// that drives internal/sampler.Sound — the SFZ-aligned model — and applies the
// musical dynamics the red-team found missing:
//
//   P0-2  Velocity → amplitude via Sound.VelGain (ghost notes ARE quieter)
//   P0-3  Honest random round-robin via SelectVariantRandom + seeded LCG RNG
//   P0-4  Declick: every voice stop/choke/steal micro-fades (Sound.DeclickMs floor)
//   P1-1  AmpEnv (Attack/Hold/Decay/Sustain/Release) applied per voice
//   P1-2  Hermite cubic resampling for pitch (Transpose+Tune+pad PitchSemitones)
//         AND device-rate conversion — linear aliasing on pitch-up is gone
//   P2-6  Per-pad RR counter owned by the render state, advanced deterministically
//
// Entry point: RenderSamplerPattern — deterministic offline render of a
// drummachine.Pattern using a SamplerKitMap (padIndex → *sampler.Sound) and a
// SamplerKitPCM (pre-decoded float32 at device rate).
//
// Also exports WriteMonoFloat32WAV (no build tag, no cgo) for the
// --render-machine CLI flag.
//
// Determinism: same inputs + same RNGSeed → byte-identical output.

import (
	"encoding/binary"
	"math"
	"os"
	"sort"

	"becky-go/internal/drummachine"
	"becky-go/internal/sampler"
)

// ---------------------------------------------------------------------------
// Hermite cubic resampling (P1-2)
// ---------------------------------------------------------------------------

// resampleHermite resamples src at ratio = dstFrames/srcFrames using four-point
// Catmull-Rom / Hermite interpolation. ratio > 1 upsamples; ratio < 1 downsamples.
// Unity ratio (within 1e-9) returns a copy without interpolation. Empty src or
// non-positive ratio returns nil (degrade). Pure Go, no allocations beyond output.
func resampleHermite(src []float32, ratio float64) []float32 {
	if len(src) == 0 || ratio <= 0 {
		return nil
	}
	if math.Abs(ratio-1.0) < 1e-9 {
		out := make([]float32, len(src))
		copy(out, src)
		return out
	}
	outLen := int(math.Round(float64(len(src)) * ratio))
	if outLen <= 0 {
		return nil
	}
	out := make([]float32, outLen)
	srcLen := len(src)
	// playback step in source frames per output frame = 1/ratio
	step := 1.0 / ratio
	for i := range out {
		srcPos := float64(i) * step
		i1 := int(srcPos)
		frac := srcPos - float64(i1)

		// Clamp all neighbours into [0, srcLen-1] for boundary safety.
		i0 := i1 - 1
		i2 := i1 + 1
		i3 := i1 + 2
		if i0 < 0 {
			i0 = 0
		}
		if i1 >= srcLen {
			i1 = srcLen - 1
		}
		if i2 >= srcLen {
			i2 = srcLen - 1
		}
		if i3 >= srcLen {
			i3 = srcLen - 1
		}
		p0 := float64(src[i0])
		p1 := float64(src[i1])
		p2 := float64(src[i2])
		p3 := float64(src[i3])

		// Catmull-Rom coefficients.
		a := -0.5*p0 + 1.5*p1 - 1.5*p2 + 0.5*p3
		b := p0 - 2.5*p1 + 2.0*p2 - 0.5*p3
		c := -0.5*p0 + 0.5*p2
		d := p1
		out[i] = float32(((a*frac+b)*frac+c)*frac + d)
	}
	return out
}

// resampleHermitePitch resamples pcm by semitones (positive = pitch UP = shorter,
// faster). semis == 0 returns src unchanged (no copy). Returns nil only when src is
// nil or the resampled length would be 0 (degrade, caller falls back to original).
func resampleHermitePitch(pcm []float32, semis float64) []float32 {
	if pcm == nil || semis == 0 {
		return pcm
	}
	// pitch UP → shorter: outLen = srcLen / speed; speed = 2^(semis/12)
	// ratio for resampleHermite = 1/speed so outLen = srcLen * ratio.
	speed := math.Pow(2.0, semis/12.0)
	if speed <= 0 {
		return pcm
	}
	ratio := 1.0 / speed
	result := resampleHermite(pcm, ratio)
	if result == nil {
		return pcm // degrade: return original rather than silencing
	}
	return result
}

// ---------------------------------------------------------------------------
// Voice-state machine (per-voice AmpEnv + playback cursor)
// ---------------------------------------------------------------------------

type samplerVoiceState int

const (
	vsAttack  samplerVoiceState = iota // ramping up
	vsHold                             // held at full amplitude
	vsDecay                            // decaying toward sustain/zero
	vsSustain                          // held at sustain level
	vsRelease                          // fading to zero (choke / natural end)
	vsDone                             // exhausted; pool removes it
)

// samplerVoice is one active sample-based voice with a full AmpEnv state machine.
type samplerVoice struct {
	pcm        []float32         // decoded+resampled PCM at device rate
	pos        int               // current playback frame
	amp        float64           // base amplitude = VelGain × Variant.Gain(linear) × padLevel
	state      samplerVoiceState // current envelope phase
	envPos     int               // elapsed samples in current phase
	attSmp     int               // attack length (samples)
	hldSmp     int               // hold length (samples)
	decSmp     int               // decay length (samples)
	relSmp     int               // release length (samples) — declick floor applies
	sustLv     float64           // sustain level 0..1 (ADSR only; 0 for AHD/Oneshot)
	envType    sampler.EnvType   // Oneshot / AHD / ADSR
	loopMode   sampler.LoopMode  // NoLoop / OneShot / LoopContinuous / LoopSustain
	loopStart  int               // loop start in pcm frames (scaled to device rate)
	loopEnd    int               // loop end in pcm frames (scaled to device rate)
	startFrame int64             // device-frame this voice started (for oldest-steal)
	padIdx     int               // for polyphony / choke matching
	chokeGroup int               // 0 = none
}

// gain returns the current envelope multiplier (0..1) and advances the FSM by
// one sample. Called once per output sample inside tick().
func (v *samplerVoice) gain() float64 {
	switch v.state {
	case vsAttack:
		if v.attSmp <= 0 {
			v.state, v.envPos = vsHold, 0
			return 1.0
		}
		g := float64(v.envPos) / float64(v.attSmp)
		v.envPos++
		if v.envPos >= v.attSmp {
			v.state, v.envPos = vsHold, 0
		}
		return g

	case vsHold:
		if v.hldSmp <= 0 {
			v.state, v.envPos = vsDecay, 0
			return 1.0
		}
		v.envPos++
		if v.envPos >= v.hldSmp {
			v.state, v.envPos = vsDecay, 0
		}
		return 1.0

	case vsDecay:
		if v.decSmp <= 0 {
			// Skip instantly to sustain.
			v.state, v.envPos = vsSustain, 0
			return v.decayFloor()
		}
		frac := float64(v.envPos) / float64(v.decSmp)
		v.envPos++
		if v.envPos >= v.decSmp {
			v.state, v.envPos = vsSustain, 0
		}
		start := 1.0
		end := v.decayFloor()
		return start + (end-start)*frac

	case vsSustain:
		// AHD and Oneshot: sustain floor is 0; sample plays to its natural end.
		// ADSR: holds at sustain level until a note-off (not yet modelled at the
		// step-sequencer level; treated as unlimited hold).
		return v.decayFloor()

	case vsRelease:
		if v.relSmp <= 0 {
			v.state = vsDone
			return 0
		}
		frac := 1.0 - float64(v.envPos)/float64(v.relSmp)
		if frac < 0 {
			frac = 0
		}
		v.envPos++
		if v.envPos >= v.relSmp {
			v.state = vsDone
		}
		return frac

	default: // vsDone
		return 0
	}
}

// decayFloor returns what the decay ramps toward:
//   - EnvADSR  → sustain level (held until note-off)
//   - EnvAHD   → 0 (attack→hold→decay to silence; sustain phase is silent)
//   - EnvOneshot → 1.0 (no envelope shaping; sample plays at full amplitude until
//     it naturally ends; the FSM reaches vsSustain only when A/H/D are all zero,
//     meaning "play flat out" — NOT "silence immediately")
func (v *samplerVoice) decayFloor() float64 {
	switch v.envType {
	case sampler.EnvADSR:
		return v.sustLv
	case sampler.EnvOneshot:
		return 1.0 // sustain = full amplitude; ends when sample runs out
	default: // EnvAHD
		return 0
	}
}

// startRelease transitions the voice to the release phase. The ramp length is the
// maximum of the argument and the stored relSmp (which already embeds the declick
// floor), so a choke/steal never causes a zero-length fade.
func (v *samplerVoice) startRelease(rampSmp int) {
	if v.state == vsDone {
		return
	}
	if rampSmp < v.relSmp {
		rampSmp = v.relSmp // declick floor
	}
	if rampSmp < 1 {
		rampSmp = 1
	}
	v.state, v.envPos, v.relSmp = vsRelease, 0, rampSmp
}

// isDone reports whether the voice is fully silent and should be removed.
func (v *samplerVoice) isDone() bool {
	if v.state == vsDone {
		return true
	}
	// Sample past its end — trigger the natural release if not already releasing.
	if v.pos >= len(v.pcm) {
		switch v.loopMode {
		case sampler.NoLoop, sampler.OneShot:
			if v.state != vsRelease {
				v.startRelease(v.relSmp)
			}
		}
	}
	return false
}

// tick returns one output sample and advances the voice by one frame.
func (v *samplerVoice) tick() float32 {
	if v.isDone() {
		return 0
	}
	g := v.gain()
	if v.state == vsDone {
		return 0
	}
	var s float32
	if v.pos < len(v.pcm) {
		s = v.pcm[v.pos]
		v.pos++
		// Loop handling.
		if v.loopMode == sampler.LoopContinuous || v.loopMode == sampler.LoopSustain {
			le := v.loopEnd
			if le <= 0 || le > len(v.pcm) {
				le = len(v.pcm)
			}
			ls := v.loopStart
			if ls < 0 {
				ls = 0
			}
			if v.pos >= le && le > ls {
				v.pos = ls
			}
		}
	}
	return float32(v.amp*g) * s
}

// ---------------------------------------------------------------------------
// newSamplerVoice — construct one voice from an event
// ---------------------------------------------------------------------------

func newSamplerVoice(
	snd *sampler.Sound,
	rawPCM []float32, // pre-decoded at device rate, BEFORE pitch-shift
	v sampler.Variant,
	vel int,
	padLevel float64,
	sampleRate int,
	startFrame int64,
	padIdx int,
) *samplerVoice {
	if snd == nil || len(rawPCM) == 0 {
		return nil
	}
	// Total pitch semitones: Variant.Transpose (whole semis) + Variant.Tune (cents).
	semis := float64(v.Transpose) + float64(v.Tune)/100.0
	pcm := rawPCM
	if semis != 0 {
		shifted := resampleHermitePitch(rawPCM, semis)
		if shifted != nil {
			pcm = shifted
		}
	}
	if v.Reverse && len(pcm) > 0 {
		rev := make([]float32, len(pcm))
		for i, x := range pcm {
			rev[len(pcm)-1-i] = x
		}
		pcm = rev
	}

	// Scale loop points proportionally to the (possibly resampled) PCM length.
	loopStart, loopEnd := 0, len(pcm)
	if (v.LoopStart > 0 || v.LoopEnd > 0) && len(rawPCM) > 0 {
		scale := float64(len(pcm)) / float64(len(rawPCM))
		loopStart = int(math.Round(float64(v.LoopStart) * scale))
		loopEnd = int(math.Round(float64(v.LoopEnd) * scale))
	}

	// Amplitude: Variant.Gain (dB) × VelGain (velocity dynamics) × pad level.
	gainLin := math.Pow(10.0, v.Gain/20.0)
	amp := gainLin * snd.VelGain(vel) * clampUnit(padLevel)

	sr := float64(sampleRate)
	secToSmp := func(sec float64) int {
		if sec <= 0 {
			return 0
		}
		n := int(math.Round(sec * sr))
		if n < 1 {
			n = 1
		}
		return n
	}

	env := snd.AmpEnv
	attSmp := secToSmp(env.A)
	hldSmp := secToSmp(env.H)
	decSmp := secToSmp(env.D)
	relSmp := secToSmp(env.R)

	// Declick floor: DeclickMs → samples, minimum 1.
	declickSmp := int(math.Round(snd.DeclickMs * sr / 1000.0))
	if declickSmp < 1 {
		declickSmp = 1
	}
	if relSmp < declickSmp {
		relSmp = declickSmp
	}

	// Pick the correct start state.
	startState := vsAttack
	if attSmp == 0 {
		startState = vsHold
		if hldSmp == 0 {
			startState = vsDecay
			if decSmp == 0 {
				startState = vsSustain
			}
		}
	}

	return &samplerVoice{
		pcm:        pcm,
		amp:        amp,
		state:      startState,
		attSmp:     attSmp,
		hldSmp:     hldSmp,
		decSmp:     decSmp,
		relSmp:     relSmp,
		sustLv:     clampUnit(env.S),
		envType:    env.Type,
		loopMode:   v.LoopMode,
		loopStart:  loopStart,
		loopEnd:    loopEnd,
		startFrame: startFrame,
		padIdx:     padIdx,
		chokeGroup: snd.ChokeGroup,
	}
}

// ---------------------------------------------------------------------------
// Voice pool with polyphony cap and choke/steal
// ---------------------------------------------------------------------------

const maxVoicesTotal = 64 // hard cap across all pads

type voicePool struct {
	voices []*samplerVoice
}

// add inserts a voice into the pool after applying choke / polyphony rules.
func (p *voicePool) add(v *samplerVoice, snd *sampler.Sound) {
	if v == nil {
		return
	}
	// ChokeGroup: cut all voices in this sound's group.
	if snd.ChokeGroup != 0 {
		for _, ov := range p.voices {
			if ov != nil && ov.chokeGroup == snd.ChokeGroup && !ov.isDone() {
				ov.startRelease(ov.relSmp)
			}
		}
	}
	// OffBy: cut voices in each listed group.
	for _, g := range snd.OffBy {
		for _, ov := range p.voices {
			if ov != nil && ov.chokeGroup == g && !ov.isDone() {
				ov.startRelease(ov.relSmp)
			}
		}
	}
	// Per-pad polyphony cap: oldest-steal.
	cap := snd.Polyphony
	if cap > 0 {
		count := 0
		for _, ov := range p.voices {
			if ov != nil && ov.padIdx == v.padIdx && !ov.isDone() {
				count++
			}
		}
		if count >= cap {
			oldestIdx := -1
			var oldestFrame int64 = math.MaxInt64
			for i, ov := range p.voices {
				if ov != nil && ov.padIdx == v.padIdx && !ov.isDone() {
					if ov.startFrame < oldestFrame {
						oldestIdx, oldestFrame = i, ov.startFrame
					}
				}
			}
			if oldestIdx >= 0 {
				p.voices[oldestIdx].startRelease(p.voices[oldestIdx].relSmp)
			}
		}
	}
	// Hard pool cap: evict oldest voice overall.
	if len(p.voices) >= maxVoicesTotal {
		oldestIdx := -1
		var oldestFrame int64 = math.MaxInt64
		for i, ov := range p.voices {
			if ov == nil || ov.isDone() {
				oldestIdx = i
				break
			}
			if ov.startFrame < oldestFrame {
				oldestIdx, oldestFrame = i, ov.startFrame
			}
		}
		if oldestIdx >= 0 {
			if p.voices[oldestIdx] != nil && !p.voices[oldestIdx].isDone() {
				p.voices[oldestIdx].state = vsDone // immediate silence
			}
			p.voices[oldestIdx] = v
			return
		}
	}
	p.voices = append(p.voices, v)
}

// tick mixes all active voices, advances them, and prunes finished ones.
func (p *voicePool) tick() float32 {
	var sum float32
	alive := p.voices[:0]
	for _, v := range p.voices {
		if v == nil || v.isDone() {
			continue
		}
		sum += v.tick()
		if !v.isDone() {
			alive = append(alive, v)
		}
	}
	p.voices = alive
	return sum
}

// ---------------------------------------------------------------------------
// SamplerKitPCM — pre-decoded PCM at device rate (no disk I/O in render loop)
// ---------------------------------------------------------------------------

// SamplerKitPCM holds decoded float32 PCM for every variant path, keyed by
// padIndex → samplePath. Caller builds this once before calling RenderSamplerPattern.
type SamplerKitPCM struct {
	byPad      map[int]map[string][]float32
	sampleRate int
}

// pcmFor looks up the decoded PCM for a pad + variant path. Returns nil when missing.
func (k *SamplerKitPCM) pcmFor(padIdx int, samplePath string) []float32 {
	if k == nil {
		return nil
	}
	m, ok := k.byPad[padIdx]
	if !ok {
		return nil
	}
	return m[samplePath]
}

// BuildSamplerKitPCMFromFloat32 constructs a SamplerKitPCM from already-decoded
// float32 maps, optionally resampling from srcRate to dstRate. The maps are
// padIdx → (samplePath → []float32). This is the injection point for tests: pass
// synthetic waveforms to exercise the engine without touching disk.
func BuildSamplerKitPCMFromFloat32(
	variantPCM map[int]map[string][]float32,
	srcRate, dstRate int,
) *SamplerKitPCM {
	if dstRate <= 0 {
		dstRate = 48000
	}
	k := &SamplerKitPCM{
		byPad:      make(map[int]map[string][]float32, len(variantPCM)),
		sampleRate: dstRate,
	}
	for padIdx, paths := range variantPCM {
		k.byPad[padIdx] = make(map[string][]float32, len(paths))
		for path, pcm := range paths {
			if srcRate > 0 && srcRate != dstRate {
				ratio := float64(dstRate) / float64(srcRate)
				pcm = resampleHermite(pcm, ratio)
			}
			if len(pcm) > 0 {
				k.byPad[padIdx][path] = pcm
			}
		}
	}
	return k
}

// ---------------------------------------------------------------------------
// SamplerKitMap — maps padIndex → *sampler.Sound
// ---------------------------------------------------------------------------

// SamplerKitMap associates each of the 16 pads with its Sound model. A nil entry
// means "no sound" for that pad (silence, no sine fallback in this engine).
type SamplerKitMap [drummachine.PadCount]*sampler.Sound

// ---------------------------------------------------------------------------
// RenderSamplerPattern — main deterministic render entry point
// ---------------------------------------------------------------------------

// RenderSamplerPatternOpts holds all inputs for a sampler render.
type RenderSamplerPatternOpts struct {
	Kit              SamplerKitMap        // padIndex → *sampler.Sound (nil = silent)
	KitPCM           *SamplerKitPCM       // pre-decoded PCM at DeviceSampleRate
	Pattern          drummachine.Pattern  // pattern to render
	Machine          *drummachine.Machine // source of tempo, mute/solo, pad params
	DeviceSampleRate int                  // output sample rate (e.g. 48000)
	NumSamples       int64                // output buffer length; 0 → nil
	RNGSeed          int64                // fixed seed → deterministic; vary for live
}

// RenderSamplerPattern renders one pattern bar using the sampler.Sound model for
// every active pad, producing a mono float32 buffer at DeviceSampleRate. It applies:
//
//   - Velocity → amplitude via Sound.VelGain (P0-2)
//   - AmpEnv (A/H/D/S/R) per voice, incl. a declick micro-fade on every stop (P0-4, P1-1)
//   - Hermite cubic resampling for pitch + device-rate conversion (P1-2)
//   - Honest random round-robin via a seeded LCG (P0-3, P2-6)
//   - Polyphony cap + oldest-voice steal; choke groups (P0-4)
//
// Output is mono float32, soft-limited via tanh. NumSamples ≤ 0 → nil.
// The render is byte-identical for a given RNGSeed.
func RenderSamplerPattern(opts RenderSamplerPatternOpts) []float32 {
	m := opts.Machine
	if m == nil || opts.NumSamples <= 0 || opts.DeviceSampleRate <= 0 {
		return nil
	}

	sr := opts.DeviceSampleRate
	pat := opts.Pattern
	steps := pat.Steps
	if steps <= 0 {
		steps = len(maxLane(pat)) // reuse from machine_render.go
	}
	if steps <= 0 {
		return make([]float32, opts.NumSamples) // silent
	}

	tempo := m.Tempo
	if tempo <= 0 {
		tempo = 120
	}
	stepSeconds := (60.0 / tempo) / float64(stepsPerBeat)
	stepSamples := stepSeconds * float64(sr)

	audible := boolSet(m.AudiblePads()) // reuse from machine_render.go

	// Collect and sort events deterministically (same as machine_render.go pattern).
	type samplerEvent struct {
		sampleOffset int64
		padIdx       int
		velocity     int
		level        float64
		pitchSemis   float64 // pad-level pitch offset; Variant has its own Transpose/Tune
	}
	var events []samplerEvent

	for pad := 0; pad < drummachine.PadCount && pad < len(pat.Lanes); pad++ {
		if !audible[pad] || opts.Kit[pad] == nil {
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
			base += swingDelayTicks(step, pat.Swing, stepSamples) // reuse
			events = append(events, samplerEvent{
				sampleOffset: int64(math.Round(base)),
				padIdx:       pad,
				velocity:     clampVelInt(vel),
				level:        clampUnit(p.Level),
				pitchSemis:   p.PitchSemitones,
			})
		}
	}

	sort.SliceStable(events, func(i, j int) bool {
		if events[i].sampleOffset != events[j].sampleOffset {
			return events[i].sampleOffset < events[j].sampleOffset
		}
		return events[i].padIdx < events[j].padIdx
	})

	// Per-pad RR counters for Sequential RR (P2-6: owned here, not in Sound).
	rrCounters := make(map[int]int, drummachine.PadCount)

	// LCG RNG seeded from RNGSeed (deterministic under fixed seed).
	rng := opts.RNGSeed
	nextRand := func() float64 {
		rng = rng*6364136223846793005 + 1442695040888963407
		return float64(uint64(rng)>>11) / float64(1<<53)
	}

	pool := &voicePool{}
	buf := make([]float32, opts.NumSamples)
	ei := 0

	for s := int64(0); s < opts.NumSamples; s++ {
		// Fire all events whose offset <= current frame.
		for ei < len(events) && events[ei].sampleOffset <= s {
			ev := events[ei]
			ei++
			snd := opts.Kit[ev.padIdx]
			if snd == nil {
				continue
			}

			layer, ok := sampler.PickLayer(*snd, ev.velocity)
			if !ok || len(layer.RoundRobin) == 0 {
				continue
			}

			var v sampler.Variant
			if layer.RRMode == sampler.Random {
				v = sampler.SelectVariantRandom(layer, nextRand())
			} else {
				ctr := rrCounters[ev.padIdx]
				v, ctr = sampler.SelectVariant(layer, ctr)
				rrCounters[ev.padIdx] = ctr
			}

			// Apply pad-level pitch on top of Variant.Transpose by adding it to a
			// copy of the variant so newSamplerVoice handles only one resampling pass.
			vWithPad := v
			vWithPad.Transpose += int(math.Round(ev.pitchSemis))

			pcm := opts.KitPCM.pcmFor(ev.padIdx, v.SamplePath)
			if pcm == nil {
				continue // missing sample: silent (degrade-never-crash)
			}

			voice := newSamplerVoice(snd, pcm, vWithPad, ev.velocity, ev.level, sr, s, ev.padIdx)
			if voice == nil {
				continue
			}
			pool.add(voice, snd)
		}

		raw := float64(pool.tick())
		// Soft-limit via tanh to prevent digital clipping on dense hits.
		limited := math.Tanh(raw)
		if limited > 0.999 {
			limited = 0.999
		} else if limited < -0.999 {
			limited = -0.999
		}
		buf[s] = float32(limited)
	}
	return buf
}

// ---------------------------------------------------------------------------
// WriteMonoFloat32WAV — pure-Go WAV writer (no build tag, no cgo)
//
// Used by --render-machine for offline bounce. We cannot call writeFloat32WAV
// from synth_audio.go because that is behind //go:build audio.
// ---------------------------------------------------------------------------

// WriteMonoFloat32WAV writes buf (mono float32 at sampleRate Hz) as an
// IEEE-float32 WAV file to path. It is the offline-bounce counterpart of the
// audio-tagged writeFloat32WAV in synth_audio.go and uses the same RIFF layout.
// Empty buf → creates a zero-length data chunk (valid WAV). Returns any I/O error.
func WriteMonoFloat32WAV(path string, buf []float32, sampleRate int) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	const (
		waveFormatFloat uint16 = 3
		bitsPerSample   uint16 = 32
		channels        uint16 = 1
		fmtBodySize     uint32 = 18 // 16 + cbSize field
	)
	sr := uint32(sampleRate)
	blockAlign := channels * (bitsPerSample / 8)
	byteRate := sr * uint32(blockAlign)
	dataSize := uint32(len(buf)) * 4
	riffSize := 4 + 8 + fmtBodySize + 8 + dataSize

	le := binary.LittleEndian
	writeTag := func(tag string) error { _, e := f.Write([]byte(tag)); return e }
	writeU16 := func(v uint16) error {
		var b [2]byte
		le.PutUint16(b[:], v)
		_, e := f.Write(b[:])
		return e
	}
	writeU32 := func(v uint32) error {
		var b [4]byte
		le.PutUint32(b[:], v)
		_, e := f.Write(b[:])
		return e
	}

	for _, fn := range []func() error{
		func() error { return writeTag("RIFF") },
		func() error { return writeU32(riffSize) },
		func() error { return writeTag("WAVE") },
		func() error { return writeTag("fmt ") },
		func() error { return writeU32(fmtBodySize) },
		func() error { return writeU16(waveFormatFloat) },
		func() error { return writeU16(channels) },
		func() error { return writeU32(sr) },
		func() error { return writeU32(byteRate) },
		func() error { return writeU16(blockAlign) },
		func() error { return writeU16(bitsPerSample) },
		func() error { return writeU16(0) }, // cbSize
		func() error { return writeTag("data") },
		func() error { return writeU32(dataSize) },
	} {
		if err := fn(); err != nil {
			return err
		}
	}
	// Encode float32 samples to little-endian bytes.
	if len(buf) == 0 {
		return nil
	}
	databuf := make([]byte, len(buf)*4)
	for i, s := range buf {
		bits := math.Float32bits(s)
		databuf[i*4] = byte(bits)
		databuf[i*4+1] = byte(bits >> 8)
		databuf[i*4+2] = byte(bits >> 16)
		databuf[i*4+3] = byte(bits >> 24)
	}
	_, err = f.Write(databuf)
	return err
}
