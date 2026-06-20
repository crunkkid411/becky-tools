// Package reaper turns becky's editable arrangement into a REAL, openable,
// renderable REAPER project (.rpp). The strategy (CLAUDE.md fork-first pivot):
// don't hand-build a DAW — author Jordan's sessions for REAPER, the most
// scriptable pro DAW (already installed on his machine, hosts all his VSTs),
// and let becky be the AI brain that drives it.
//
// This file is the DETERMINISTIC writer: same Project in -> byte-identical .rpp
// out (the becky house rule). It hand-writes the plain-text .rpp format that
// REAPER itself produced (captured ground-truth in becky-reaper-work/reference.rpp),
// so a generated project opens and renders via `reaper.exe -renderproject x.rpp`.
//
// What the writer CAN do with no REAPER running: tracks, named bus FOLDERS
// (Cubase-style summing busses), per-track gain/pan/mute/solo, audio items
// (WAV), MIDI items (notes -> the E-event list at 960 PPQ), and an OPTIONAL
// built-in ReaSynth instrument on MIDI tracks (exact FX state copied from
// ground-truth) so a rendered MIDI track is AUDIBLE with zero plugin guessing.
//
// What it CANNOT do: embed arbitrary third-party VSTs (Serum 2, TAL-Drum,
// Maschine 2, Ozone) — those need REAPER to instantiate their binary state.
// That is the job of the companion Lua control script (lua.go), which calls
// REAPER's TrackFX_AddByName on the real plugins.
package reaper

import (
	"fmt"
	"hash/fnv"
	"sort"
	"strings"
)

// midiPPQ is REAPER's MIDI source resolution. becky arrangements are 480 PPQ
// (music.PPQ); the clean 2x scale is applied when emitting MIDI events.
const midiPPQ = 960

// reaSynthFXChain is the exact, ground-truth ReaSynth instrument block REAPER
// wrote (becky-reaper-work/reference.rpp). Embedding it verbatim makes a MIDI
// track audible on render with no plugin-state guessing. %s = a per-track FXID.
const reaSynthFXChain = `    <FXCHAIN
      SHOW 0
      LASTSEL 0
      DOCKED 0
      BYPASS 0 0 0
      <VST "VSTi: ReaSynth (Cockos)" reasynth.dll 0 "" 1919251321<5653547265737972656173796E746800> ""
        eXNlcu9e7f4AAAAAAgAAAAEAAAAAAAAAAgAAAAAAAABEAAAAAAAAAAAAEAA=
        776t3g3wrd6mm8Q7F7fROgAAAAAAAAAAAAAAAM5NAD/pZ4g9AAAAAAAAAD8AAIA/AACAPwAAAD8AAAAAAAAAAAAAAAA=
        AAAQAAAA
      >
      FLOATPOS 0 0 0 0
      FXID %s
      WAK 0 0
    >
`

// Item is one media item on a track: an audio clip (WavFile set) or a MIDI clip
// (Notes set). Position/Length are in seconds (project time).
type Item struct {
	Position float64
	Length   float64
	Name     string
	WavFile  string     // absolute path; set => audio item
	Notes    []MIDINote // set => MIDI item
}

// MIDINote is one note in arrangement ticks (480 PPQ), mirroring dawmodel.Note.
type MIDINote struct {
	Start int // ticks @ 480 PPQ
	Dur   int // ticks (>0)
	Pitch int // 0..127
	Vel   int // 1..127
	Ch    int // 0..15
}

// Track is one REAPER track. Bus folders model Jordan's Cubase bus tree: a
// FolderStart track sums the member tracks that follow it; the last member sets
// FolderCloses>0 to close the folder. ReaSynth toggles the built-in instrument.
type Track struct {
	Name         string
	Vol          float64 // linear, 1 = unity (0 dB)
	Pan          float64 // -1 (L) .. 0 (C) .. 1 (R)
	Mute, Solo   bool
	FolderStart  bool // this track is a bus/folder parent (sums its children)
	FolderCloses int  // # of folder levels this (last-child) track closes
	ReaSynth     bool // embed the built-in ReaSynth instrument (audible MIDI)
	Items        []Item
}

// Project is a deterministic, REAPER-renderable session.
type Project struct {
	BPM        float64
	Num, Den   int    // time signature
	SampleRate int    // render sample rate
	RenderFile string // absolute output path for `reaper.exe -renderproject`
	Tracks     []Track
}

// normalize fills sane defaults (degrade, never crash): a zero BPM/sig/SR is
// replaced rather than producing a broken file.
func (p *Project) normalize() {
	if p.BPM <= 0 {
		p.BPM = 120
	}
	if p.Num <= 0 {
		p.Num = 4
	}
	if p.Den <= 0 {
		p.Den = 4
	}
	if p.SampleRate <= 0 {
		p.SampleRate = 48000
	}
}

// WriteRPP writes the project as REAPER .rpp text. Deterministic: no timestamps,
// no randomness — GUIDs are derived from stable seeds so the same Project yields
// byte-identical output (becky house rule; testable in CI without REAPER).
func WriteRPP(p Project) string {
	p.normalize()
	var b strings.Builder

	// Header. The trailing "0 0" fields are a creation timestamp REAPER ignores;
	// fixed at 0 for determinism.
	fmt.Fprintf(&b, "<REAPER_PROJECT 0.1 \"7.69/win64\" 0 0\n")
	b.WriteString("  RIPPLE 0 0\n")
	b.WriteString("  GROUPOVERRIDE 0 0 0\n")
	b.WriteString("  AUTOXFADE 129\n")
	fmt.Fprintf(&b, "  TEMPO %s %d %d 0\n", ftoa(p.BPM), p.Num, p.Den)
	fmt.Fprintf(&b, "  SAMPLERATE %d 0 0\n", p.SampleRate)

	// Render settings so `-renderproject` works headlessly. RENDER_CFG is the
	// ground-truth 24-bit WAV blob REAPER wrote.
	if p.RenderFile != "" {
		fmt.Fprintf(&b, "  RENDER_FILE %s\n", p.RenderFile)
	}
	b.WriteString("  RENDER_PATTERN \"\"\n")
	fmt.Fprintf(&b, "  RENDER_FMT 0 2 %d\n", p.SampleRate)
	b.WriteString("  RENDER_1X 0\n")
	b.WriteString("  RENDER_RANGE 1 0 0 0 1000\n")
	b.WriteString("  RENDER_RESAMPLE 3 0 1\n")
	b.WriteString("  RENDER_ADDTOPROJ 0\n")
	b.WriteString("  RENDER_STEMS 0\n")
	b.WriteString("  RENDER_DITHER 0\n")
	b.WriteString("  RENDER_CHANNELS 2\n")
	b.WriteString("  <RENDER_CFG\n    ZXZhdxgBAA==\n  >\n")

	// Minimal master (REAPER fills the rest on load).
	b.WriteString("  MASTER_VOLUME 1 0 -1 -1 1\n")
	b.WriteString("  MASTER_NCH 2 2\n")

	for i, t := range p.Tracks {
		writeTrack(&b, t, i, p)
	}
	b.WriteString(">\n")
	return b.String()
}

func writeTrack(b *strings.Builder, t Track, idx int, p Project) {
	if t.Vol == 0 {
		t.Vol = 1
	}
	g := guid(fmt.Sprintf("track:%d:%s", idx, t.Name))
	fmt.Fprintf(b, "  <TRACK %s\n", g)
	fmt.Fprintf(b, "    NAME %s\n", quoteIfNeeded(t.Name))
	fmt.Fprintf(b, "    VOLPAN %s %s -1 -1 1\n", ftoa(t.Vol), ftoa(t.Pan))
	fmt.Fprintf(b, "    MUTESOLO %d %d 0\n", btoi(t.Mute), btoi(t.Solo))
	fmt.Fprintf(b, "    ISBUS %s\n", isbus(t))
	b.WriteString("    NCHAN 2\n")
	b.WriteString("    FX 1\n")
	fmt.Fprintf(b, "    TRACKID %s\n", g)
	b.WriteString("    MAINSEND 1 0\n")

	if t.ReaSynth {
		fmt.Fprintf(b, reaSynthFXChain, guid(fmt.Sprintf("fxid:%d", idx)))
	}
	for ii, it := range t.Items {
		writeItem(b, it, idx, ii, p)
	}
	b.WriteString("  >\n")
}

// isbus maps becky folder flags to REAPER's ISBUS line. A folder parent is
// "1 1"; a folder-closing last child is "2 -<n>"; everything else is "0 0".
func isbus(t Track) string {
	if t.FolderStart {
		return "1 1"
	}
	if t.FolderCloses > 0 {
		return fmt.Sprintf("2 -%d", t.FolderCloses)
	}
	return "0 0"
}

func writeItem(b *strings.Builder, it Item, trackIdx, itemIdx int, p Project) {
	g := guid(fmt.Sprintf("item:%d:%d", trackIdx, itemIdx))
	ig := guid(fmt.Sprintf("iguid:%d:%d", trackIdx, itemIdx))
	b.WriteString("    <ITEM\n")
	fmt.Fprintf(b, "      POSITION %s\n", ftoa(it.Position))
	fmt.Fprintf(b, "      LENGTH %s\n", ftoa(it.Length))
	b.WriteString("      LOOP 0\n")
	fmt.Fprintf(b, "      IGUID %s\n", ig)
	fmt.Fprintf(b, "      IID %d\n", itemIdx+1)
	fmt.Fprintf(b, "      NAME %s\n", quoteIfNeeded(it.Name))
	fmt.Fprintf(b, "      GUID %s\n", g)
	switch {
	case it.WavFile != "":
		b.WriteString("      <SOURCE WAVE\n")
		fmt.Fprintf(b, "        FILE \"%s\"\n", it.WavFile)
		b.WriteString("      >\n")
	default:
		writeMIDISource(b, it, p, guid(fmt.Sprintf("midi:%d:%d", trackIdx, itemIdx)))
	}
	b.WriteString("    >\n")
}

// writeMIDISource emits the <SOURCE MIDI ...> block: notes become the E-event
// delta list at 960 PPQ, terminated by an all-notes-off at the item end.
func writeMIDISource(b *strings.Builder, it Item, p Project, g string) {
	b.WriteString("      <SOURCE MIDI\n")
	b.WriteString("        HASDATA 1 960 QN\n")
	b.WriteString("        CCINTERP 32\n")
	for _, e := range midiEvents(it, p) {
		fmt.Fprintf(b, "        E %d %02x %02x %02x\n", e.delta, e.status, e.d1, e.d2)
	}
	b.WriteString("        CCINTERP 32\n")
	fmt.Fprintf(b, "        GUID %s\n", g)
	b.WriteString("        IGNTEMPO 0 120 4 4\n")
	b.WriteString("      >\n")
}

type midiEvent struct {
	delta          int
	status, d1, d2 int
}

type rawEvent struct {
	tick           int // @ 960 PPQ
	status, d1, d2 int
	off            bool
}

// midiEvents converts an item's notes into delta-encoded REAPER MIDI events
// (note-on/off + a final all-notes-off CC at item end), at 960 PPQ.
func midiEvents(it Item, p Project) []midiEvent {
	var rs []rawEvent
	for _, n := range it.Notes {
		ch := clampi(n.Ch, 0, 15)
		pitch := clampi(n.Pitch, 0, 127)
		vel := clampi(n.Vel, 1, 127)
		on := scaleTo960(n.Start)
		off := scaleTo960(n.Start + maxi(n.Dur, 1))
		rs = append(rs, rawEvent{tick: on, status: 0x90 | ch, d1: pitch, d2: vel})
		rs = append(rs, rawEvent{tick: off, status: 0x80 | ch, d1: pitch, d2: 0, off: true})
	}
	// Deterministic order: by tick; at the same tick, note-offs before note-ons
	// (so a re-trigger of the same pitch reads cleanly).
	sort.SliceStable(rs, func(i, j int) bool {
		if rs[i].tick != rs[j].tick {
			return rs[i].tick < rs[j].tick
		}
		if rs[i].off != rs[j].off {
			return rs[i].off // offs first
		}
		return rs[i].d1 < rs[j].d1
	})

	endTick := midiEndTick(it, p, rs)
	var out []midiEvent
	prev := 0
	for _, r := range rs {
		out = append(out, midiEvent{delta: r.tick - prev, status: r.status, d1: r.d1, d2: r.d2})
		prev = r.tick
	}
	// Trailing all-notes-off (CC 0x7b) padding the item to its end.
	out = append(out, midiEvent{delta: maxi(endTick-prev, 0), status: 0xb0, d1: 0x7b, d2: 0})
	return out
}

func midiEndTick(it Item, p Project, rs []rawEvent) int {
	// 960-PPQ ticks spanned by the item's LengthSec at the project tempo.
	secPerTick := 60.0 / (p.BPM * float64(midiPPQ))
	end := int(it.Length/secPerTick + 0.5)
	last := 0
	for _, r := range rs {
		if r.tick > last {
			last = r.tick
		}
	}
	if end < last {
		end = last
	}
	return end
}

// scaleTo960 converts arrangement ticks (480 PPQ) to REAPER MIDI ticks (960 PPQ).
func scaleTo960(arrTick int) int { return arrTick * midiPPQ / 480 }

// guid derives a stable, REAPER-shaped GUID from a seed so output is reproducible.
func guid(seed string) string {
	a := fnv64(seed + ":a")
	b := fnv64(seed + ":b")
	return fmt.Sprintf("{%08X-%04X-%04X-%04X-%012X}",
		uint32(a>>32), uint16(a>>16), uint16(a), uint16(b>>48), b&0xFFFFFFFFFFFF)
}

func fnv64(s string) uint64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(s))
	return h.Sum64()
}

// quoteIfNeeded wraps a token in quotes when it contains spaces/special chars,
// matching REAPER's tokenizer. Empty -> "".
func quoteIfNeeded(s string) string {
	if s == "" {
		return "\"\""
	}
	if strings.ContainsAny(s, " \t\"") {
		if !strings.Contains(s, "\"") {
			return "\"" + s + "\""
		}
		if !strings.Contains(s, "'") {
			return "'" + s + "'"
		}
		return "`" + strings.ReplaceAll(s, "`", "'") + "`"
	}
	return s
}

// ftoa formats a float compactly and stably across runs.
func ftoa(f float64) string {
	return fmt.Sprintf("%g", f)
}

func btoi(b bool) int {
	if b {
		return 1
	}
	return 0
}

func clampi(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func maxi(a, b int) int {
	if a > b {
		return a
	}
	return b
}
