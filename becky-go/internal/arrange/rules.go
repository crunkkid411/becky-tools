// Package arrange is becky's DETERMINISTIC, stem-aware arrangement engine: it adds
// ONE musical layer at a time to an existing dawmodel.Arrangement, and every layer
// is written AWARE of the stems already there — the bass locks to the actual kick,
// chords and melody stay in the established key, the melody lands chord-tones on
// strong beats. No model, no tokens: it is pure music-theory math, instant and
// hand-editable (MIDI).
//
// WHY THIS EXISTS IN CODE (read CLAUDE.md + ARRANGEMENT-RULES.md): the layering
// rules below were researched repeatedly and then lost between sessions because they
// only ever lived in chat. They are ported here from the ACE-Step-DAW `.claude`
// skill set — whose three music skills (music-theory-engine, strudel-maestro,
// compose) INDEPENDENTLY agree on the same build order — plus Jordan's 8-bar rule.
// Encoding them as functions means "oops, I forgot that step" can't happen: the
// step runs.
package arrange

// LayerOrder is the deterministic build order. Each later layer reads the earlier
// ones (ACE-Step's "one layer at a time", corroborated across all three skills):
//
//		key+progression → drums → bass → chords → melody → texture
//
//	  - drums set the groove (built/edited first; the rhythmic foundation),
//	  - bass LOCKS to the kick and lands chord roots on strong beats,
//	  - chords verify harmonic fit (same key),
//	  - melody sits on top — chord-tones on strong beats, most rests,
//	  - texture (pads/fx/fills) fills the remaining space.
var LayerOrder = []string{"drums", "bass", "chords", "melody", "texture"}

// NextLayer returns the layer becky should suggest building next given the roles
// already present (ACE-Step jam rule: "if drums exist, suggest bass next"). Returns
// "" when every layer in the order is present.
func NextLayer(present map[string]bool) string {
	for _, l := range LayerOrder {
		if l == "drums" {
			continue // drums are the user's starting point, not auto-suggested
		}
		if !present[l] {
			return l
		}
	}
	return ""
}

// MaxChunkBars caps a generated chunk at 8 bars. Jordan's rule: anything longer is
// slop, because (e.g.) the B-section of verse 2 differs subtly from the A-section —
// distinct musical ideas are DISTINCT chunks, generated separately, not one long run.
const MaxChunkBars = 8

// Velocity windows (MIDI 1..127) per layer role. ACE-Step's hard rule is "no flat
// velocity"; these are its 0..1 figures converted to MIDI (drums .3–.9, pads .3–.5,
// melody .5–.8), with a solid-but-expressive band for bass.
const (
	DrumVelLo, DrumVelHi = 38, 115 // ghost notes low, accents high
	PadVelLo, PadVelHi   = 38, 64  // chords/pads sit back
	MelVelLo, MelVelHi   = 64, 102 // melody is expressive
	BassVelLo, BassVelHi = 56, 104
)

// Bass register: ACE-Step puts bass at MIDI 36–71 (octave 1 / below 36 is "too
// low"). becky keeps the bass in the low half of that allowance so it reads as bass.
const (
	BassMidiLo = 36
	BassMidiHi = 55
)

// Chord/pad register and melody register (ACE-Step: chords mid, melody highest).
const (
	ChordMidiLo, ChordMidiHi   = 48, 72
	MelodyMidiLo, MelodyMidiHi = 60, 84
)
