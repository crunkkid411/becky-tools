package arrange

import (
	"fmt"

	"becky-go/internal/dawmodel"
	"becky-go/internal/music"
)

// layers.go implements the deterministic layer adds. Each reads the existing
// arrangement (key + groove) and writes ONE new MIDI track that fits — the
// stem-aware "LEGO" build. All are immutable (return a new arrangement), seeded,
// and degrade-never-crash.

// chordRoots returns, per bar, the chord ROOT midi note for the bass, derived from
// the genre progression in the arrangement's key and clamped to the bass register.
func chordRoots(a *dawmodel.Arrangement, genre string, bars int) []int {
	rootPC, scale, _ := resolveKey(a)
	prog := progressionFor(genre)
	out := make([]int, bars)
	for b := 0; b < bars; b++ {
		degree := prog[b%len(prog)]
		root := music.ScaleMidi(rootPC, scale, degree, 2) // low octave for bass
		out[b] = music.Clamp(root, BassMidiLo, BassMidiHi)
	}
	return out
}

// AddBass adds a bassline that LOCKS to the existing kick and lands the chord root
// on strong beats — the first harmonic layer over a drum groove. It reads the actual
// kick onsets (not a template), so it complements whatever drums are there. Returns
// an error if a bass track already exists or the arrangement is nil.
func AddBass(a *dawmodel.Arrangement, opts Options) (*dawmodel.Arrangement, error) {
	if a == nil {
		return nil, fmt.Errorf("arrange: nil arrangement")
	}
	if hasRole(a, "bass") {
		return a, fmt.Errorf("arrange: a bass track already exists")
	}
	perBarKick, bars := kickOnsets(a)
	if bars == 0 {
		bars = 1 // no drums: still lay down roots on strong beats
		perBarKick = make([][]int, 1)
	}
	roots := chordRoots(a, opts.Genre, bars)
	rng := music.NewRng(opts.Seed)

	out := a.AddTrack("bass", dawmodel.KindMIDI)
	li := len(out.Tracks) - 1
	out.Tracks[li].Clips = append(out.Tracks[li].Clips, dawmodel.Clip{
		Name: "bass", Channel: 0, Program: 38, // 38 = synth bass
	})

	for b := 0; b < bars; b++ {
		onsets := append([]int(nil), perBarKick[b]...)
		// Always anchor the downbeat (strong beat) with the root, even if the kick
		// doesn't hit there — ACE-Step's "bass roots on strong beats" rule.
		if len(onsets) == 0 || onsets[0] != 0 {
			onsets = append([]int{0}, onsets...)
		}
		root := roots[b]
		for i, st := range onsets {
			startTick := (b*stepsPerBar + st) * music.StepTicks
			durSteps := stepsPerBar - st
			if i+1 < len(onsets) {
				durSteps = onsets[i+1] - st
			}
			dur := maxInt(durSteps*music.StepTicks, music.StepTicks)
			vel := humanVel(rng, st, BassVelLo, BassVelHi)
			var err error
			out, _, err = out.AddNote("bass", "bass", dawmodel.Note{
				Start: startTick, Dur: dur, Pitch: root, Vel: vel, Ch: 0,
			})
			if err != nil {
				return a, fmt.Errorf("arrange: add bass note: %w", err)
			}
		}
	}
	return out, nil
}

// AddChords adds a pad/chord layer: one triad per bar voiced in the chord register,
// held across the bar, velocity sitting back. In a MINOR key the V chord uses the
// raised leading tone (a major V) — ACE-Step's explicit harmonic rule — so the
// dominant actually resolves. Stays in the established key.
func AddChords(a *dawmodel.Arrangement, opts Options) (*dawmodel.Arrangement, error) {
	if a == nil {
		return nil, fmt.Errorf("arrange: nil arrangement")
	}
	if hasRole(a, "chords") {
		return a, fmt.Errorf("arrange: a chords track already exists")
	}
	rootPC, scale, scaleName := resolveKey(a)
	prog := progressionFor(opts.Genre)
	_, bars := kickOnsets(a)
	if bars == 0 {
		bars = len(prog)
	}
	rng := music.NewRng(opts.Seed + 1)

	out := a.AddTrack("chords", dawmodel.KindMIDI)
	li := len(out.Tracks) - 1
	out.Tracks[li].Clips = append(out.Tracks[li].Clips, dawmodel.Clip{
		Name: "chords", Channel: 1, Program: 81, // 81 = saw lead/pad
	})

	isMinor := scaleName == "minor" || scaleName == "aeolian" || scaleName == "harmonicMinor"
	for b := 0; b < bars; b++ {
		degree := prog[b%len(prog)]
		triad := music.Triad(rootPC, scale, degree, 4, false)
		if isMinor && degree == 4 { // the V chord in a minor key → raise its 3rd a semitone
			if len(triad) >= 2 {
				triad[1]++
			}
		}
		startTick := b * stepsPerBar * music.StepTicks
		dur := stepsPerBar * music.StepTicks
		for _, n := range triad {
			pitch := music.Clamp(n, ChordMidiLo, ChordMidiHi)
			vel := humanVel(rng, 0, PadVelLo, PadVelHi)
			var err error
			out, _, err = out.AddNote("chords", "chords", dawmodel.Note{
				Start: startTick, Dur: dur, Pitch: pitch, Vel: vel, Ch: 1,
			})
			if err != nil {
				return a, fmt.Errorf("arrange: add chord note: %w", err)
			}
		}
	}
	return out, nil
}

// AddMelody adds a simple deterministic melody: a chord-tone on each strong beat
// (so it agrees with the harmony) with eighth-note rests between, in the melody
// register with expressive velocity — ACE-Step's "chord-tones on strong beats,
// space = musicality" rule. It is intentionally sparse and editable, not a flashy
// solo (deterministic melody should be a tasteful skeleton Jordan refines by hand).
func AddMelody(a *dawmodel.Arrangement, opts Options) (*dawmodel.Arrangement, error) {
	if a == nil {
		return nil, fmt.Errorf("arrange: nil arrangement")
	}
	if hasRole(a, "melody") {
		return a, fmt.Errorf("arrange: a melody track already exists")
	}
	rootPC, scale, _ := resolveKey(a)
	prog := progressionFor(opts.Genre)
	_, bars := kickOnsets(a)
	if bars == 0 {
		bars = len(prog)
	}
	rng := music.NewRng(opts.Seed + 2)

	out := a.AddTrack("melody", dawmodel.KindMIDI)
	li := len(out.Tracks) - 1
	out.Tracks[li].Clips = append(out.Tracks[li].Clips, dawmodel.Clip{
		Name: "melody", Channel: 2, Program: 80,
	})

	for b := 0; b < bars; b++ {
		degree := prog[b%len(prog)]
		triad := music.Triad(rootPC, scale, degree, 5, false) // chord tones, higher octave
		// One chord-tone per beat (steps 0,4,8,12), choosing among the triad tones
		// deterministically; a couple of beats rest for space.
		for beat := 0; beat < 4; beat++ {
			if beat == 2 && rng.Chance(50) {
				continue // a rest mid-bar — leave space
			}
			tone := triad[(b+beat)%len(triad)]
			pitch := music.Clamp(tone, MelodyMidiLo, MelodyMidiHi)
			startTick := (b*stepsPerBar + beat*4) * music.StepTicks
			dur := 3 * music.StepTicks // dotted-eighth-ish, leaves a gap before the next
			vel := humanVel(rng, beat*4, MelVelLo, MelVelHi)
			var err error
			out, _, err = out.AddNote("melody", "melody", dawmodel.Note{
				Start: startTick, Dur: dur, Pitch: pitch, Vel: vel, Ch: 2,
			})
			if err != nil {
				return a, fmt.Errorf("arrange: add melody note: %w", err)
			}
		}
	}
	return out, nil
}

// humanVel returns a reproducible "humanised" velocity in [lo,hi]: accented on the
// strong beats (step%4==0), softer off-beat, with a small seeded jitter — never a
// flat value (ACE-Step's no-flat-velocity rule).
func humanVel(rng *music.Rng, step, lo, hi int) int {
	span := hi - lo
	base := lo + span/3
	if step%4 == 0 { // downbeat / beat accent
		base = lo + (span*3)/4
	}
	v := base + rng.Intn(maxInt(span/4, 1)) - span/8
	if v < lo {
		v = lo
	}
	if v > hi {
		v = hi
	}
	return v
}

// AddLayer dispatches to the right layer adder by role name ("bass"/"chords"/
// "melody"). Unknown roles return an error. This is the single entry point a CLI or
// the canvas calls so the deterministic engine is the one source of truth.
func AddLayer(a *dawmodel.Arrangement, role string, opts Options) (*dawmodel.Arrangement, error) {
	switch role {
	case "bass":
		return AddBass(a, opts)
	case "chords":
		return AddChords(a, opts)
	case "melody":
		return AddMelody(a, opts)
	default:
		return a, fmt.Errorf("arrange: unknown layer %q (have: bass, chords, melody)", role)
	}
}

// SuggestNext returns the layer becky recommends adding next, given what's present.
func SuggestNext(a *dawmodel.Arrangement) string {
	return NextLayer(presentRoles(a))
}
