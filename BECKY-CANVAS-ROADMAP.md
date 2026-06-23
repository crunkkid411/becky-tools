# BECKY-CANVAS-ROADMAP.md — the real DAW, built INSIDE becky-canvas

Ratified direction (2026-06-22, Jordan). Supersedes the external-driving approach
(REAPER/kdenlive) for human-grade DAW/NLE surfaces. Backed by the research in
`research/` (six docs, summarized below). Read with `CANVAS-NORTH-STAR.md`,
`CANVAS-BLUEPRINT.md`, `GUI-RULES.md`, `GAP-ANALYSIS.md`, `FEATURE-INVENTORY.md`.

## North star (settled)

- **becky-canvas (Go + Gio) is the ONE app.** Everything lives inside it. No second
  window, no puppeted external DAW.
- **REAPER + kdenlive driving is PAUSED, not deleted.** Keep `becky-reaper`,
  `internal/reaper`, `internal/kdenlive` dormant in case we revisit. They are no longer
  the path.
- **OpenDaw is the MODEL, ported — not forked.** `glenwrhodes/OpenDaw` is GPL3 +
  Qt6/C++ + Tracktion(GPL3)/JUCE(AGPL3): forking it is the wrong language under copyleft
  and re-introduces the "escape to another app" mistake. We copy its *architecture,
  feature-completeness, and AI-tool design* into becky's existing Go. becky already owns
  the brain: `internal/ctledit` (the BeckyEditBatch applier = becky's "AI tools") +
  `internal/ctlmodel` (NL→batch) + `internal/dawmodel` (the session).
- **Don't reinvent blind — port a proven interaction model.** No Go/Gio DAW-timeline
  widget exists; the leverage is porting UX, not finding a package.

## The iteration loop now exists (use it)

`becky-canvas --render-frame out.png [--size WxH]` renders ONE frame of any panel
off-screen to a PNG (honours `BECKY_CANVAS_MODE=drum|piano|mixer|audio` + a dropped
target). An agent edits Gio code → renders → looks at (or `becky-vision`-audits) the PNG
→ fixes — no human needed to open the window. This is the standing way to verify every
canvas change below.

## Research grounding (the six docs)

| Doc | One-line finding |
|---|---|
| `research/opendaw-adoption-plan.md` | PORT not fork; becky's biggest gap vs OpenDaw = an **agentic multi-step loop**; of ~63 OpenDaw AI tools, ~20 already exist as ctledit ops, ~10 as CLIs, ~15 are cheap "add op" — a wiring job. |
| `research/reference-projects-gap-analysis.md` | Top gaps: AI-settable **FX params**, a **rich session READ** for the agent, **aux sends/FX-return buses**, generation **range/masking/voice-leading** guards, dawmodel enrichment (key/scale/color/bypassable-plugin intent). |
| `research/daw-video-timeline-gui-components.md` | No Go widget exists. **Arranger + drum grid + video-NLE timeline are ONE Gio widget** — build once, specialize. Port `xzdarcy/react-timeline-editor` (MIT) mechanics, `ryohey/signal` (MIT) for note grids, `bbc/peaks.js` UX for waveforms. |
| `research/existing-oss-we-keep-reinventing.md` | Piano roll = PORT `signal`; audio I/O = EMBED gopxl/beep + oto; MIDI = EMBED gomidi v2; drum engine = keep becky's; the fork-first calls were correct. |
| `research/go-gui-iteration-and-design-tools.md` | The render-frame loop above; no Magic-MCP/superdesign exists for Go (web-only). |
| `research/go-packages-explained-for-jordan.md` | Go deps don't cause runtime bloat/slowness; becky is lean (9 direct deps). |

## The architecture fix that unblocks everything (Phase 0)

**Symptom Jordan reported:** a cmd window flashed on every click; clicking around lags;
the drum machine restarts at bar 1 instead of updating live.

**Root cause:** becky-canvas does "actions" (▶ Play, Speak, tool runs) by **launching a
separate `.exe` per click**. That is not how normal software works; suppressing the
console window hides the flash but not the ~tens-of-ms process-spawn + audio-device
re-open cost, and re-launching the engine is *why* playback restarts at bar 1.

**Fix:** one **persistent in-process / warm audio engine** the canvas talks to over the
GUI-RULES NDJSON seam — start once, send messages per click. This is exactly the pattern
already proven for the warm TTS server (`internal/pyhelpers/tts_server.py`, load-once on
:11436). It is also how OpenDaw works (one app, one in-process engine, tools call into
it). Phase 0 removes the lag AND the bar-1 restart, and is the foundation for live edit.

## Phased plan (each phase EXTENDS existing packages; verify with --render-frame + ears)

**Phase 1 — Drum machine fundamentals (highest pain, cheapest win).**
becky's engine is NOT the weak part — `beatgen.Step` already has per-step
velocity/probability/microtiming/ratchet/flam/pitch/pan. The gaps are pure wiring:
1. Fix the `beatgen ↔ drummachine` bridge so it **stops dropping** the per-step data.
2. Canvas UI to **edit** velocity/probability/microtiming per step (the data already
   exists — the grid just shows on/off today).
3. Add the missing sequencer fundamentals: triplet/finer resolution, **live record +
   note-repeat**, clear/fill, accent, per-pad sample edit + attack.
*Drum-machine verdict (Jordan's direct question): do NOT adopt a foreign drum machine —
none of quality exists in Go, and becky's engine already exceeds the typical OSS one. The
fix is wiring the engine to the UI + porting a proven step-sequencer interaction model.*

**Phase 2 — The ONE timeline widget.**
Build a single Gio lane-stack widget (drag/resize/snap clips, playhead, zoom) by porting
`react-timeline-editor` mechanics + `signal` note-grid behavior. **Specialize it** into:
arranger, drum grid, piano roll, and (later) the video-NLE timeline. Stops the repeated
hand-building of each surface blind. The piano "roll" today is an empty placeholder —
this is what makes it real.

**Phase 3 — OpenDaw-parity AI control (the biggest capability gap).**
1. Add `internal/ctlagent`: the **multi-step agentic loop** (call a ctledit op → read the
   updated session → pick the next → repeat until done), reusing ctledit/ctlmodel/dawmodel.
2. Add the ~15 cheap new `ctledit` ops (set_fx_param, add_fx, aux send, transport nav,
   take management) + expose the ~10 existing CLIs, mapping OpenDaw's ~63 tools.
3. Give the agent a **rich session READ** (a real `Describe`/`Snapshot`) so it proposes
   grounded edits.

**Phase 4 — Mixer / FX completeness + audible FX.**
Aux sends / FX-return buses (pre/post-fader), AI-settable FX params, born-gain-staged
tracks, bypassable-plugin intent, then **audible FX/VST3 via the C++ sidecar** (already
planned in GUI-RULES). Enrich `dawmodel` with key/scale/confidence + track color.

## What is already done (don't rebuild)

dawmodel session, ctledit applier (~20 ops), ctlmodel NL→edit, arrange (stem-aware
layering), beatgen (rich step engine), musictheory, pianoroll model, audioengine sampler,
canvasbridge render adapter, the Gio panels (drum/piano/mixer/audio), the warm TTS server,
and now `--render-frame`. The work is wiring + UI + the agent loop, not new engines.
