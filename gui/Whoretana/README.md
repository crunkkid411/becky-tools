# WHORETANA - the native becky voice shell

The face of becky. A native **WPF (.NET 8, Windows)** window: a glitchy cyan HUD with a
living particle **orb** in the center that you **talk to** to run becky's tools, plus a
full tool/launcher/chat surface. No browser, no web view, no localhost/server (Jordan's
hard constraint) - it is a `.exe` that launches the existing `becky-*.exe`'s.

Implements `handoff-becky-wpf-gui.md` (the tool-runner shell, grown up) and the local
half of `HANDOFF-BECKY-VOICE.md` (the talk loop).

## Run it
- Double-click the Desktop **"Whoretana"** shortcut (instant, no console), or
- Double-click **`Open Whoretana.bat`** at the repo root (builds on first run, then launches), or
- `dotnet run` from this folder.

It finds `becky-go\bin` on its own and puts it on PATH, so the tools just work.

## What's on screen (matches `whoretana-gui-blue.png`)
- **Title** - "WHORETANA" in Deacon Flock with a cyan glow.
- **Hero orb** (SkiaSharp) - a dendrite particle cloud inside rotating reticle/gear rings.
  - *Idle*: ambient drift + a breathing central bloom.
  - *Listening*: the cloud pulses outward with your mic level ("it hears me").
  - *Speaking*: particles re-arrange into an emergent **face**; the **mouth opens with the
    TTS amplitude (lip-sync)**, under a heavy datamosh glitch that hides the rough edges
    (the head3.gif look). Returns to the ambient orb when silent.
- **Top-left**: glowing command bar (`</>`, search, `/do`, shield, settings) + a tool search box.
- **Left**: the **live tool grid** (bracket buttons from `becky-catalog --json`, tier-colored:
  cyan = safe, amber = asks, **#ff3366** = needs your OK), **workflow** buttons, and a **menu**
  of becky ops.
- **Top-right**: circular **LAUNCH** buttons - open a CLI instance (Claude Code, becky-ask,
  Research, DAW, Review, Tools, Canvas, Shell).
- **Right**: a small **VU dial** that tracks the mic.
- **Bottom-center**: a **status / transcript** strip.
- **Bottom-right**: the **chat box** with the "leaking electricity" jagged border - type or
  hold-to-talk.

## Colors
Cyan/blue homage to Cortana (`#22E8FF`); the only secondary is `#ff3366` (danger/red tier).
No purple. Grain + scanlines + subtle glitch throughout. It is meant to look like a
dystopian program that pieced itself back together - not corporate AI.

## How talking works (the loop)
1. Hold-to-talk records the mic to a wav (NAudio).
2. `becky-transcribe` turns it into text.
3. The text is **routed**: preferred = pipe an NDJSON intent to `becky-voice` (the Go driver
   from HANDOFF-BECKY-VOICE.md - tier-gates, runs green tools, returns a spoken line);
   fallback = `becky-ask --question` if `becky-voice.exe` isn't built yet.
4. The reply is spoken back with `becky-tts` (NeuTTS Air) and the orb lip-syncs to it.

The typed chat box uses the same route.

## Architecture
```
Whoretana.exe (WPF)
  App.xaml            theme: cyan palette, Deacon Flock font, neon styles
  MainWindow.xaml     the HUD layout (all regions above)
  Orb/OrbControl.cs   SkiaSharp particle orb (idle/listen/speak + gears + glitch)
  Audio/AudioEngine   NAudio mic RMS -> MicLevel; TTS wav metering -> SpeechLevel (lip-sync)
  Voice/VoiceClient   route via becky-voice (NDJSON) / becky-ask; STT + TTS
  Tools/Catalog.cs    reads becky-catalog --json (single source of truth)
  ProcessRunner.cs    crash-proof shell-out + CLI launcher
```

## Status / boundaries
- The shell, orb (all three states + lip-sync), catalog tool grid, workflows, launchers,
  chat, and the local talk loop (transcribe -> route -> tts) are **built and verified**.
- `becky-voice.exe` (the deterministic router) is wired; when present the chat/voice route
  through it. Until then it falls back to `becky-ask`.
- **Gemini 2.5 Flash realtime** (the pinned low-latency talk model in HANDOFF-BECKY-VOICE.md
  Phase 3.1) needs Jordan's `GEMINI_API_KEY` + the realtime Python helper; it is the clean
  next step on top of this working local loop, not wired here.
