# Drum Machine — the settled decision (stop the ping-pong)

**Status: PINNED.** This answers, once, "are we using Hydrogen or REAPER or JSON or what?"
so it stops flip-flopping every session. Read it before any drum/canvas-audio work.

## The decision

**THE drum machine is becky's own pure-Go SAMPLER engine** — `internal/drummachine`
(the 16-pad model) + `internal/audioengine` (`sampler_engine.go`: real multi-sample
playback with velocity, amp envelopes, Hermite resampling, choke groups). It is
deterministic, offline-renderable (`becky-daw-engine --render-machine`, cgo-free,
unit-tested), and it plays Jordan's real kits.

- **becky-canvas's drum panel is the authoring surface.** Its ▶ Play now routes the
  beat through the sampler engine via `becky-daw-engine --play-machine` (the SAME
  engine the standalone `becky-drummachine` window uses). The bridge is
  `drummachine.MachineFromArrangement` (the canvas's `dawmodel.Arrangement` drum clip
  → a `Machine`), wired in `cmd/canvas/gui_play.go`.
- **`becky-daw-engine` has two play paths:** `--play-machine` (the **sampler** — real
  kits; used for drums) and `--play-pattern-audio` (the older 4-sample **synth**; used
  for melodic preview). Drum mode uses `--play-machine`. Do not route drums through the
  synth path again.

## What Hydrogen and REAPER are (and are NOT)

- **Hydrogen** (`internal/hydrogen` + `cmd/becky-groove`) is an **optional export /
  alternate backend**, NOT the core drum machine. It authors real `.h2song`/`drumkit.xml`
  and can drive a live Hydrogen over OSC (verified once, in isolation, making sound from
  43k samples). Keep it for its FOSS mixer/FX and for people who want Hydrogen — reach it
  as a tool (`becky-groove`) or a future canvas "export to Hydrogen" button. It is **not**
  what the canvas ▶ uses, because it adds a hard Hydrogen-install dependency the native
  sampler avoids. (It was orphaned — imported only by `cmd/becky-groove` — which is the
  confusion this doc ends.)
- **REAPER** (`becky-reaper`) is the **full multitrack DAW** path (a separate concern,
  per `SPEC-BECKY-REAPER.md`). It is not a drum-playback path.

This is consistent with `CANVAS-NORTH-STAR.md`: the native panel is the app; external
tools (Hydrogen/REAPER) are at most a button, never the substitute.

## What's DONE vs what's LEFT (honest)

**Done (cloud-verified):** the `dawmodel.Arrangement → Machine` bridge + tests; the
canvas ▶ wired to `--play-machine`; the core voices (kick/snare/hat/clap, GM 36/38/42/39)
auto-assigned to the becky default kit (`$BECKY_DRUM_KIT`) so it sounds without loading a
kit first.

**Left (LOCAL — needs the GPU/audio/display this cloud agent doesn't have):**
1. **Sound-check:** open becky-canvas → Drum mode → ▶ and confirm the sampler plays the
   beat (the one thing cloud cannot verify).
2. **In-canvas kit browser** — the canvas can currently only sound the 4 default voices.
   The standalone `becky-drummachine` ALREADY has a full kit/sample browser
   (`cmd/drummachine/gui_kit.go`, browses `X:\music-2\SAMPLES`/`X:\Splice`). Port/share
   that panel into the canvas drum panel so any of Jordan's 43k samples map onto the 16
   pads. THIS is the remaining piece that makes the canvas drum machine fully real.
3. Optional: a canvas "export to Hydrogen" button (drive his FOSS Hydrogen with the
   current beat) using the existing `internal/hydrogen` writer.
