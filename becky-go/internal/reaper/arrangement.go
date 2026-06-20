package reaper

import (
	"fmt"

	"becky-go/internal/dawmodel"
)

// arrPPQ is becky's arrangement resolution (music.PPQ = 480).
const arrPPQ = 480

// FromArrangement maps a becky dawmodel.Arrangement onto a REAPER Project so the
// same editable session becky reasons about becomes an openable .rpp. MIDI clips
// become MIDI items (audible via the built-in ReaSynth); audio clips are carried
// only when they reference a real file (the model stores peaks, not paths, so
// audio-file wiring is the caller's job).
//
// Bus structure: tracks sharing a Strip.Bus are grouped under a REAPER folder
// (a summing bus) named for that bus — the Cubase bus tree, one level deep. This
// is deterministic: bus order follows first appearance in a.Tracks.
func FromArrangement(a *dawmodel.Arrangement, renderFile string) Project {
	p := Project{
		BPM:        float64(a.BPM),
		Num:        a.Num,
		Den:        a.Den,
		SampleRate: 48000,
		RenderFile: renderFile,
	}

	// Group tracks by bus, preserving first-seen order.
	var busOrder []string
	members := map[string][]dawmodel.Track{}
	var flat []dawmodel.Track
	for _, t := range a.Tracks {
		bus := t.Strip.Bus
		if bus == "" || bus == "bus.master" || bus == "master" {
			flat = append(flat, t)
			continue
		}
		if _, seen := members[bus]; !seen {
			busOrder = append(busOrder, bus)
		}
		members[bus] = append(members[bus], t)
	}

	// Emit each bus as a folder parent + its members (last member closes it).
	for _, bus := range busOrder {
		p.Tracks = append(p.Tracks, Track{Name: busName(bus), Vol: 1, FolderStart: true})
		ms := members[bus]
		for i, t := range ms {
			rt := convertTrack(t, a)
			if i == len(ms)-1 {
				rt.FolderCloses = 1
			}
			p.Tracks = append(p.Tracks, rt)
		}
	}
	// Then any tracks routed straight to master.
	for _, t := range flat {
		p.Tracks = append(p.Tracks, convertTrack(t, a))
	}
	return p
}

func convertTrack(t dawmodel.Track, a *dawmodel.Arrangement) Track {
	rt := Track{
		Name: t.ID,
		Vol:  t.Strip.Gain,
		Pan:  t.Strip.Pan,
		Mute: t.Strip.Mute,
		Solo: t.Strip.Solo,
	}
	if rt.Vol == 0 {
		rt.Vol = 1
	}
	isMIDI := t.Kind != dawmodel.KindAudio
	if isMIDI {
		rt.ReaSynth = true // make MIDI audible on render with the built-in synth
	}
	for _, c := range t.Clips {
		if len(c.Notes) == 0 {
			continue
		}
		rt.Items = append(rt.Items, midiItemFromClip(c, a))
	}
	return rt
}

// midiItemFromClip turns a dawmodel MIDI clip into a REAPER MIDI item, computing
// the item length in seconds from the note extent at the project tempo.
func midiItemFromClip(c dawmodel.Clip, a *dawmodel.Arrangement) Item {
	bpm := float64(a.BPM)
	if bpm <= 0 {
		bpm = 120
	}
	secPerTick := 60.0 / (bpm * float64(arrPPQ))
	endTick := 0
	notes := make([]MIDINote, 0, len(c.Notes))
	for _, n := range c.Notes {
		notes = append(notes, MIDINote{
			Start: c.Offset + n.Start,
			Dur:   n.Dur,
			Pitch: n.Pitch,
			Vel:   n.Vel,
			Ch:    n.Ch,
		})
		if e := c.Offset + n.Start + n.Dur; e > endTick {
			endTick = e
		}
	}
	name := c.Name
	if name == "" {
		name = "midi"
	}
	return Item{
		Position: float64(c.Offset) * secPerTick,
		Length:   float64(endTick) * secPerTick,
		Name:     name,
		Notes:    notes,
	}
}

// busName turns a becky bus id (e.g. "bus.drums") into a display name.
func busName(id string) string {
	switch id {
	case "bus.drums":
		return "DRUMS_bus"
	case "bus.808", "bus.bass":
		return "BASS_bus"
	case "bus.music":
		return "MUSIC_bus"
	case "bus.fx":
		return "FX_bus"
	default:
		return id
	}
}

// JordanTemplate returns a starting REAPER session shaped like Jordan's Cubase
// projects (from his cubase1-7 screenshots): the bus tree he actually uses, at
// his usual 132 BPM / 48 kHz. Members are empty, ready for content/plugins via
// the Lua control script. renderFile may be "" (set when rendering).
func JordanTemplate(renderFile string) Project {
	p := Project{BPM: 132, Num: 4, Den: 4, SampleRate: 48000, RenderFile: renderFile}

	// Bus -> member tracks, mirroring the screenshots:
	//   DRUMS_bus: Kick, Snare, OH, Claps   (cubase3)
	//   GUITARS_bus: Gtr L, Gtr R           (cubase4)
	//   BASS_bus: Synth Bass, Sub           (cubase4/6)
	//   VOCALS_bus: Lead, Backing, Screams  (cubase1/2)
	//   FX_bus: Cymbal Transitions          (cubase4)
	groups := []struct {
		bus     string
		members []string
		midi    bool
	}{
		{"DRUMS_bus", []string{"Kick", "Snare", "OH", "Claps"}, false},
		{"GUITARS_bus", []string{"Gtr L", "Gtr R"}, false},
		{"BASS_bus", []string{"Synth Bass", "Sub"}, true},
		{"VOCALS_bus", []string{"Lead Vox", "Backing Vox", "Screams"}, false},
		{"FX_bus", []string{"Cymbal Transitions"}, false},
	}
	for _, g := range groups {
		p.Tracks = append(p.Tracks, Track{Name: g.bus, Vol: 1, FolderStart: true})
		for i, m := range g.members {
			t := Track{Name: m, Vol: 1, ReaSynth: g.midi}
			if i == len(g.members)-1 {
				t.FolderCloses = 1
			}
			p.Tracks = append(p.Tracks, t)
		}
	}
	return p
}

// DemoProject is a minimal self-contained, AUDIBLE session: a synth-bass MIDI
// riff (A1-C2-E2 at 132 BPM) on a ReaSynth track inside a BASS folder. Used to
// prove the writer end-to-end (render -> non-silent WAV) without external files.
func DemoProject(renderFile string) Project {
	p := Project{BPM: 132, Num: 4, Den: 4, SampleRate: 48000, RenderFile: renderFile}
	q := 480 // quarter note in arrangement ticks (480 PPQ)
	notes := []MIDINote{
		{Start: 0 * q, Dur: q, Pitch: 33, Vel: 110, Ch: 0}, // A1
		{Start: 1 * q, Dur: q, Pitch: 36, Vel: 100, Ch: 0}, // C2
		{Start: 2 * q, Dur: q, Pitch: 40, Vel: 100, Ch: 0}, // E2
		{Start: 3 * q, Dur: q, Pitch: 33, Vel: 110, Ch: 0}, // A1
	}
	secPerTick := 60.0 / (132.0 * float64(arrPPQ))
	p.Tracks = []Track{
		{Name: "BASS_bus", Vol: 1, FolderStart: true},
		{
			Name: "Synth Bass", Vol: 1, ReaSynth: true, FolderCloses: 1,
			Items: []Item{{
				Position: 0,
				Length:   float64(4*q) * secPerTick,
				Name:     fmt.Sprintf("bass-%dbpm", 132),
				Notes:    notes,
			}},
		},
	}
	return p
}
