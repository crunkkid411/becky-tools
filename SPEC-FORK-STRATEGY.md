# SPEC-FORK-STRATEGY.md — Fork mature open-source, give becky AI control (pivot: 2026-06-20)

**Status: IN FLIGHT. This document records a DECISION and an ARCHITECTURE. It makes
NO claim that any surface "works" — every such claim is gated on reproducible evidence
(a real render / real audio / ffprobe output) produced by the verification agents.**

## The decision (Jordan's call, 2026-06-20)

Jordan judged the hand-built Gio drum machine (`becky-drummachine`) and video NLE
(`becky-nle`) "complete disasters" and directed: **stop hand-building Maschine/Cubase/
Vegas-class GUIs; fork a mature open-source app and integrate AI into it.** This is the
correct engineering call (it is the `Research & Reuse` principle) and it overrides the
build-from-scratch stance of `GUI-RULES.md` **for human-grade DAW/NLE surfaces**. The
deterministic Go cores and the NDJSON seam stay valuable — as the *brain*, not the app.

The root failure being corrected: a hand-built Gio app will never reach the tool a
15-year producer actually uses, and the effort produced docs that declared GUIs
"BUILT / VERIFIED" while the drum machine played **sine beeps** and the NLE preview pane
was **empty**. (See the honest audit in this session.)

## The principle

**becky becomes the AI BRAIN, not the application.** It reads/writes the real app's
NATIVE project files (= full context) and drives render/playback over CLI / OSC / MIDI.
"Any AI can control it" = one deterministic JSON **state+edits** contract
(`research/becky-control-schema.md`) fanned out to backend adapters. Propose-then-apply
("show me, don't do it") is mandatory — the human stays in control.

## The bases (as of the pivot)

### VIDEO / NLE -> kdenlive  (DECIDED)
- Installed: `C:\Program Files\kdenlive\bin\kdenlive.exe`; bundled headless renderer
  `melt.exe` **v7.37** beside it. ffmpeg/ffprobe present.
- Control = edit the `.kdenlive` **MLT XML** project + render headless with `melt`
  (`melt proj.kdenlive -consumer avformat:out.mp4 vcodec=libx264 acodec=aac`).
- **kdenlive is becky's backend ONLY — Jordan does not use its GUI; he downloaded it
  purely so an AI can drive it from the CLI.** Its settings are becky's to set/clobber.
  Startup popups disabled in `kdenliverc` (`showWelcome=false`, `checkForUpdate=false`,
  `checkfirstprojectclip=false`). DONE.
- Gotcha: inject `xmlns:kdenlive` namespace when parsing/writing "Gen-2" projects.
- Fallback: **Shotcut** (same MLT engine, even simpler `.mlt`, same `melt` render).

### DRUM -> try the REAL NI Maschine 2 first; fork Hydrogen as the guaranteed fallback
Jordan owns and loves Maschine 2 (he had it open during the pivot). Two routes are being
tried before settling for a clone:
- **VST3 route:** host the Maschine 2 VST3 in becky's existing C++ VST3 host
  (`native/audio-host` + `internal/audiohost` + `becky-vst`, which already loaded 309 of
  his plugins). Send MIDI notes, render. Risk: Maschine's kit/state lives behind its own
  editor — headless control may be blocked.
- **MIDI route:** drive the open Maschine standalone via a virtual MIDI port + becky's
  pure-Go SMF writer.
- **Fallback = Hydrogen** (GPL, ships Windows binaries): the only FOSS groovebox an AI can
  drive three ways — editable `.h2song`/`drumkit.xml` XML, rich **OSC**, and a MIDI action
  map. Honest caveat: a classic drum sequencer, not a 4x4-pad Maschine lookalike.
  `LMMS` (on choco) is the alternate.

## Reused becky cores (the brain's parts — keep, do not rebuild)
`internal/music` (SMF MIDI read+write), `internal/sampler`, `internal/sampledecode`,
`internal/samplelib` (scans `X:\music-2\SAMPLES`, `X:\Splice`), `internal/kitimport`
(SFZ/DecentSampler), `internal/edl`, `internal/reel`, `internal/mediainfo`, and the VST3
host stack. The audit confirmed these are solid (no TODO/panic, tested, deterministic).

## Honesty mandate (the cultural fix — non-negotiable)
No document or status line may claim a surface "works" / "verified" without reproducible
evidence: a real rendered file with ffprobe output, or real non-silent audio
(volumedetect mean > -80 dB). "It compiled" is NOT "it works." Every verification agent in
this pivot is required to paste real command output or report an honest blocker.

## Status (2026-06-20) — what is proven vs in flight
- PROVEN: `melt.exe -version` runs (v7.37); kdenlive popup keys set in `kdenliverc`.
- IN FLIGHT (4 parallel worktree agents, evidence pending — NO success claim yet):
  A = kdenlive bridge (`internal/kdenlive` + `cmd/becky-nle`, melt render, verify on a real video).
  B = Hydrogen install + bridge (`internal/hydrogen` + `cmd/becky-groove`, OSC, real-sample audio).
  C = real-Maschine VST3 feasibility spike (becky-vst scan/load Maschine 2, render).
  D = Maschine-MIDI route + the `research/becky-control-schema.md` contract.
- OPEN QUESTION (decided by evidence, not opinion): which drum backend wins —
  the real Maschine or Hydrogen.
