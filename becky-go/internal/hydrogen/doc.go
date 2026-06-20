// Package hydrogen builds project files for Hydrogen, the open-source drum machine
// (https://github.com/hydrogen-music/hydrogen), and drives a running instance over OSC.
//
// becky's pivot: rather than reinvent a sampler/sequencer, becky drives a REAL,
// proven drum machine and lets any AI control it. This package is the bridge —
// pure-Go, stdlib + encoding/xml only (plus the go-osc dependency for live control,
// kept in osc.go so the file writers stay dependency-free).
//
// Two outputs, both round-trip-compatible with Hydrogen 1.2.x:
//
//   - Song   -> a .h2song file (drumkit + patterns + sequence) that Hydrogen can
//     open, play, and export to audio via `h2cli -s song.h2song -o out.wav`.
//   - Drumkit -> a drumkit.xml file (the instrument list a song references), so
//     becky can assemble a kit from arbitrary real samples on disk.
//
// The XML schema mirrors the files Hydrogen itself writes (verified against the
// bundled GMRockKit/drumkit.xml and demo_songs/*.h2song of a 1.2.6 install). The
// format version is pinned to FormatVersion (>= 1.2.4 per the becky contract) so the
// files declare a modern Hydrogen format.
//
// Timing: Hydrogen uses TicksPerQuarter (48) ticks per quarter-note. A 4/4 bar is
// TicksPerBar (192) ticks. A classic 16-step pattern therefore places steps every
// TicksPerStep16 (12) ticks. Helpers (StepPattern, TicksForStep) hide this so a
// caller thinks in steps, not ticks.
//
// Design rules (per CLAUDE.md): offline, deterministic (same input -> byte-identical
// XML), degrade-never-crash (validation clamps; nothing panics). Windows `\` paths are
// handled via internal/pathx where a base name is needed.
package hydrogen
