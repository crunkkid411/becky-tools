package music

import (
	"hash/fnv"
	"sort"
	"strings"
)

// Resolution + bar math (SPEC-BECKY-COMPOSE §2.6): 480 PPQ => 16th = 120t, 4/4 bar
// = 1920t. The 16-step grid maps step s -> s*StepTicks.
const (
	PPQ       = 480
	StepTicks = PPQ / 4 // a 16th note
	EighthT   = PPQ / 2 // an 8th note
	BarTicks  = PPQ * 4 // one 4/4 bar
)

// NamedTrack is one generated MIDI track plus the metadata the renderer/router need.
type NamedTrack struct {
	Name    string
	Channel int
	Program int // GM program, or -1 for percussion / none
	Track   *Track
}

// Song is the deterministic result of Generate. Same (profile, root, bpm, seed)
// => identical Song bytes.
type Song struct {
	Genre   string
	Root    string
	Scale   string
	RootPC  int
	BPM     int
	TPQ     int
	Seed    int64
	Prog    []string
	Tracks  []NamedTrack
	Routing Project
}

type bar struct {
	start  int
	energy float64
	active map[string]bool
	chord  []int
}

type span struct {
	name  string
	start int
	bars  int
}

type activeBar struct {
	start  int
	energy float64
	chord  []int
}

// Generate is the pure composition function. rootArg like "F#m"/"Am"/"" (empty =>
// profile default); bpmArg <=0 => profile default; seed makes humanization
// reproducible.
func Generate(p Profile, rootArg string, bpmArg int, seed int64) *Song {
	var rootPC int
	var scaleName string
	if strings.TrimSpace(rootArg) != "" {
		rootPC, scaleName = ParseKey(rootArg)
	} else {
		rootPC, _ = ParseKey(p.Key.DefaultRoot)
		scaleName = p.Key.DefaultScale
	}
	scaleName = scaleAlias(scaleName)
	scale := ScaleIntervals(scaleName)

	bpm := p.Tempo.Default
	if bpmArg > 0 {
		bpm = bpmArg
	}
	if p.Tempo.Min > 0 && bpm < p.Tempo.Min {
		bpm = p.Tempo.Min
	}
	if p.Tempo.Max > 0 && bpm > p.Tempo.Max {
		bpm = p.Tempo.Max
	}

	prog := pickProgression(p, NewRng(perSeed(seed, "progression")))

	var bars []bar
	var spans []span
	barNo := 0
	for _, sec := range p.Arrangement {
		active := map[string]bool{}
		for _, t := range sec.Tracks {
			active[t] = true
		}
		spans = append(spans, span{name: sec.Name, start: barNo * BarTicks, bars: sec.Bars})
		for b := 0; b < sec.Bars; b++ {
			roman := prog[barNo%len(prog)]
			chord := romanChord(roman, rootPC, scale, 4, nil)
			bars = append(bars, bar{barNo * BarTicks, sec.Energy, active, chord})
			barNo++
		}
	}

	swingTicks := int((p.Swing-0.5)*2*float64(StepTicks) + 0.5)
	jitter := p.Humanize.TimingJitter
	velHum := p.Humanize.VelHumanize

	song := &Song{Genre: p.ID, Root: rootName(rootPC), Scale: scaleName, RootPC: rootPC,
		BPM: bpm, TPQ: PPQ, Seed: seed, Prog: prog}

	names := make([]string, 0, len(p.Tracks))
	for n := range p.Tracks {
		names = append(names, n)
	}
	sort.Strings(names)

	for _, name := range names {
		spec := p.Tracks[name]
		rng := NewRng(perSeed(seed, name))
		tr := &Track{}
		tr.Name(0, name)
		prg := -1
		if spec.Program != nil {
			prg = *spec.Program
			tr.Program(0, spec.Channel, prg)
		}
		var ab []activeBar
		for _, b := range bars {
			if b.active[name] {
				ab = append(ab, activeBar{b.start, b.energy, b.chord})
			}
		}
		switch {
		case len(spec.Patterns) > 0:
			genDrums(tr, spec, ab, swingTicks, jitter, velHum, rng)
		case name == "bass":
			genBass(tr, spec, p, ab)
		case name == "chords":
			genChords(tr, spec, ab)
		case name == "melody":
			genMelody(tr, spec, ab, rootPC, scale, rng)
		case name == "lead":
			genLead(tr, spec, ab)
		case name == "counter":
			genCounter(tr, spec, ab)
		case name == "sfx":
			genSfx(tr, spec, spans)
		}
		song.Tracks = append(song.Tracks, NamedTrack{Name: name, Channel: spec.Channel, Program: prg, Track: tr})
	}

	song.Routing = buildProject(p, song)
	return song
}

func perSeed(seed int64, label string) int64 {
	h := fnv.New64a()
	h.Write([]byte(label))
	return seed*1000003 + int64(h.Sum64()&0x7fffffff)
}

func rootName(pc int) string {
	names := []string{"C", "C#", "D", "D#", "E", "F", "F#", "G", "G#", "A", "A#", "B"}
	return names[((pc%12)+12)%12]
}

// pickProgression chooses a weighted progression deterministically.
func pickProgression(p Profile, rng *Rng) []string {
	if len(p.Progressions) == 0 {
		return []string{"i", "bVI", "bVII", "i"}
	}
	total := 0
	for _, pr := range p.Progressions {
		total += maxInt(pr.Weight, 1)
	}
	r := rng.Intn(total)
	for _, pr := range p.Progressions {
		w := maxInt(pr.Weight, 1)
		if r < w {
			return pr.Roman
		}
		r -= w
	}
	return p.Progressions[0].Roman
}

// romanChord resolves a Roman numeral to MIDI notes, applying the harmonic-minor V
// rule (uppercase V over a minor diatonic v => raise the third to a major dominant).
func romanChord(roman string, rootPC int, scale []int, octave int, extensions []string) []int {
	r := strings.TrimSpace(roman)
	for len(r) > 0 && (r[0] == 'b' || r[0] == '#') {
		r = r[1:]
	}
	deg := RomanToIndex(r)
	tri := Triad(rootPC, scale, deg, octave, false)
	// Harmonic-minor dominant: an UPPERCASE V (the 5th degree) over a minor
	// diatonic third gets its third raised to a true major dominant. Lowercase v
	// (and VI/VII, which are other degrees) stay diatonic.
	if deg == 4 && len(r) > 0 && r[0] == 'V' && len(tri) >= 2 && tri[1]-tri[0] == 3 {
		tri[1]++
	}
	if strings.Contains(r, "7") || extHas(extensions, "7") || extHas(extensions, "m9") || extHas(extensions, "maj9") || extHas(extensions, "m11") {
		tri = append(tri, ScaleMidi(rootPC, scale, deg+6, octave))
	}
	if strings.Contains(r, "9") || extHas(extensions, "9") || extHas(extensions, "m9") || extHas(extensions, "maj9") || extHas(extensions, "add9") {
		tri = append(tri, ScaleMidi(rootPC, scale, deg+8, octave))
	}
	return tri
}

func extHas(ext []string, s string) bool {
	for _, e := range ext {
		if strings.EqualFold(e, s) {
			return true
		}
	}
	return false
}

// ---- per-track generators (all deterministic) ------------------------------

func genDrums(tr *Track, spec TrackSpec, ab []activeBar, swingTicks, jitter, velHum int, rng *Rng) {
	voices := make([]string, 0, len(spec.Patterns))
	for v := range spec.Patterns {
		voices = append(voices, v)
	}
	sort.Strings(voices)
	for _, b := range ab {
		velScale := 0.7 + 0.3*b.energy
		for _, vn := range voices {
			v := spec.Patterns[vn]
			base := Vel(v.Vel)
			isHat := strings.Contains(vn, "hat")
			for step := 0; step < 16 && step < len(v.Grid); step++ {
				if v.Grid[step] == 0 {
					continue
				}
				if spec.DensityByEnergy && isHat && b.energy < 0.7 && step%2 == 1 {
					continue // thin offbeat hats in low-energy sections
				}
				sw := 0
				if step%2 == 1 {
					sw = swingTicks
				}
				t := b.start + step*StepTicks + sw + rng.Jitter(jitter)
				vel := clampVel(int(float64(base)*velScale) + rng.Jitter(velHum))
				tr.Note(t, StepTicks/2, spec.Channel, v.Note, vel)
			}
			for _, rl := range v.Rolls {
				if rl.Cell < 0 || rl.Cell >= 16 || rl.N <= 0 {
					continue
				}
				cell := b.start + rl.Cell*StepTicks
				sub := StepTicks / rl.N
				for k := 0; k < rl.N; k++ {
					tr.Note(cell+k*sub, maxInt(sub/2, 1), spec.Channel, v.Note, rampVel(base, k, rl.N, rl.Ramp, velScale))
				}
			}
		}
	}
}

func genBass(tr *Track, spec TrackSpec, p Profile, ab []activeBar) {
	lo, hi := registerOr(spec.Register, 28, 48)
	octShift := spec.Octave * 12
	kick := kickSteps(p)
	vel := Vel("hard")
	for _, b := range ab {
		if len(b.chord) == 0 {
			continue
		}
		root := Clamp(b.chord[0]+octShift, lo, hi)
		if spec.Rhythm == "rootFollowsKick" && len(kick) > 0 {
			for i, st := range kick {
				dur := BarTicks - st*StepTicks
				if i+1 < len(kick) {
					dur = (kick[i+1] - st) * StepTicks
				}
				tr.Note(b.start+st*StepTicks, maxInt(dur, StepTicks), spec.Channel, root, vel)
			}
		} else {
			tr.Note(b.start, BarTicks, spec.Channel, root, vel)
		}
	}
}

func genChords(tr *Track, spec TrackSpec, ab []activeBar) {
	lo, hi := registerOr(spec.Register, 48, 72)
	base := Vel(orVel(spec.Vel, "soft"))
	for _, b := range ab {
		notes := voiceInRegister(b.chord, lo, hi)
		if len(notes) == 0 {
			continue
		}
		velScale := 0.7 + 0.3*b.energy
		v := clampVel(int(float64(base) * velScale))
		switch spec.Rhythm {
		case "arpUpEighths":
			for i := 0; i < 8; i++ {
				tr.Note(b.start+i*EighthT, EighthT-10, spec.Channel, notes[i%len(notes)], v)
			}
		case "sidechainPumpQuarter":
			for q := 0; q < 4; q++ {
				for _, n := range notes {
					tr.Note(b.start+q*PPQ, PPQ-10, spec.Channel, n, v)
				}
			}
		default:
			for _, n := range notes {
				tr.Note(b.start, BarTicks, spec.Channel, n, v)
			}
		}
	}
}

func genMelody(tr *Track, spec TrackSpec, ab []activeBar, rootPC int, scale []int, rng *Rng) {
	lo, hi := registerOr(spec.Register, 60, 84)
	base := Vel(orVel(spec.Vel, "normal"))
	density := spec.Density
	if density <= 0 {
		density = 0.5
	}
	prevDeg := 0
	for _, b := range ab {
		eff := density * (0.4 + 0.6*b.energy)
		for i := 0; i < 8; i++ {
			if !rng.Chance(int(eff * 100)) {
				continue
			}
			deg := prevDeg + rng.Intn(5) - 2 // stepwise -2..+2
			note := Clamp(ScaleMidi(rootPC, scale, deg, 5), lo, hi)
			if i%2 == 0 && len(b.chord) > 0 {
				note = nearestChordTone(note, b.chord, lo, hi)
			}
			dur := EighthT
			if rng.Chance(30) {
				dur = EighthT * 2
			}
			tr.Note(b.start+i*EighthT, dur-10, spec.Channel, note, clampVel(base+rng.Jitter(8)))
			prevDeg = deg
		}
	}
}

func genLead(tr *Track, spec TrackSpec, ab []activeBar) {
	lo, hi := registerOr(spec.Register, 64, 88)
	base := Vel(orVel(spec.Vel, "hard"))
	for _, b := range ab {
		if len(b.chord) == 0 {
			continue
		}
		top := Clamp(b.chord[len(b.chord)-1]+12, lo, hi)
		tr.Note(b.start, PPQ-10, spec.Channel, top, base)
		tr.Note(b.start+2*PPQ, PPQ-10, spec.Channel, top, base)
	}
}

func genCounter(tr *Track, spec TrackSpec, ab []activeBar) {
	lo, hi := registerOr(spec.Register, 55, 79)
	base := Vel(orVel(spec.Vel, "soft"))
	for _, b := range ab {
		if len(b.chord) < 2 {
			continue
		}
		third := Clamp(b.chord[1], lo, hi)
		tr.Note(b.start+PPQ+EighthT, PPQ-10, spec.Channel, third, base)
		tr.Note(b.start+3*PPQ+EighthT, PPQ-10, spec.Channel, third, base)
	}
}

func genSfx(tr *Track, spec TrackSpec, spans []span) {
	for _, e := range spec.Events {
		for _, s := range spans {
			switch {
			case e == "impactAtDrop" && strings.Contains(s.name, "drop"):
				tr.Note(s.start, PPQ, 9, 49, Vel("hard")) // crash cymbal on GM drums
			case e == "riserAtBuild" && strings.Contains(s.name, "build"):
				last := s.start + maxInt(s.bars-1, 0)*BarTicks
				for k := 0; k < 16; k++ {
					tr.Note(last+k*StepTicks, StepTicks, spec.Channel, Clamp(60+k, 60, 96), Vel("soft"))
				}
			case e == "downsweepAtOutro" && strings.Contains(s.name, "outro"):
				for k := 0; k < 16; k++ {
					tr.Note(s.start+k*StepTicks, StepTicks, spec.Channel, Clamp(84-k, 48, 96), Vel("ghost"))
				}
			}
		}
	}
}

// ---- small helpers ---------------------------------------------------------

func registerOr(reg []int, lo, hi int) (int, int) {
	if len(reg) == 2 {
		return reg[0], reg[1]
	}
	return lo, hi
}

func voiceInRegister(chord []int, lo, hi int) []int {
	out := make([]int, 0, len(chord))
	for _, n := range chord {
		out = append(out, Clamp(n, lo, hi))
	}
	return out
}

func nearestChordTone(note int, chord []int, lo, hi int) int {
	best, bestD := note, 1<<30
	for _, c := range chord {
		c = Clamp(c, lo, hi)
		d := note - c
		if d < 0 {
			d = -d
		}
		if d < bestD {
			best, bestD = c, d
		}
	}
	return best
}

func kickSteps(p Profile) []int {
	d, ok := p.Tracks["drums"]
	if !ok {
		return []int{0, 8}
	}
	k, ok := d.Patterns["kick"]
	if !ok {
		return []int{0, 8}
	}
	var steps []int
	for i := 0; i < 16 && i < len(k.Grid); i++ {
		if k.Grid[i] == 1 {
			steps = append(steps, i)
		}
	}
	if len(steps) == 0 {
		return []int{0, 8}
	}
	return steps
}

func rampVel(base, k, n int, ramp string, scale float64) int {
	f := 1.0
	if n > 1 {
		switch ramp {
		case "up":
			f = 0.5 + 0.5*float64(k)/float64(n-1)
		case "down":
			f = 1.0 - 0.5*float64(k)/float64(n-1)
		}
	}
	return clampVel(int(float64(base) * f * scale))
}

func clampVel(v int) int {
	if v < 1 {
		return 1
	}
	if v > 127 {
		return 127
	}
	return v
}

func orVel(v, def string) string {
	if v == "" {
		return def
	}
	return v
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// ---- SMF rendering ---------------------------------------------------------

// SMF returns the whole song as a multi-track type-1 SMF (track 0 = tempo/meter).
func (s *Song) SMF() *File {
	f := NewFile(s.TPQ)
	meta := f.AddTrack()
	meta.Name(0, s.Genre+" "+s.Root+" "+s.Scale)
	meta.Tempo(0, s.BPM)
	meta.TimeSig(0, 4, 4)
	for _, nt := range s.Tracks {
		f.Tracks = append(f.Tracks, nt.Track)
	}
	return f
}

// TrackSMF returns one track as its own standalone SMF (with tempo), so each stem
// drags into a DAW independently.
func (s *Song) TrackSMF(nt NamedTrack) *File {
	f := NewFile(s.TPQ)
	meta := f.AddTrack()
	meta.Tempo(0, s.BPM)
	meta.TimeSig(0, 4, 4)
	f.Tracks = append(f.Tracks, nt.Track)
	return f
}
