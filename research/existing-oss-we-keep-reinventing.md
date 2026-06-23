# Existing OSS we keep reinventing — a "stop building from scratch" survey

**Date:** 2026-06-22. **Author:** cloud research subagent.
**Why this doc exists (Jordan's words):** *"we've wasted way too much time trying to
create things from scratch and the most basic stuff keeps getting overlooked."* The
agents keep hand-building a drum machine, piano roll, DAW, and video editor in Go and
shipping toy results. This doc answers one question per surface: **does a mature FOSS
thing already exist that we should USE / FORK / DRIVE / EMBED / PORT instead of
reinventing?** Every project below is real and was checked live (license, stars,
recency) — nothing invented.

It **extends and confirms** the project's existing fork-first decisions (kdenlive for
video, Hydrogen as a drum export, REAPER as the AI-driven DAW, the C++ VST3 host) — it
does not relitigate them. The one genuinely new, high-value finding is at the very top.

---

## 0. The headline finding

**We keep rebuilding a piano roll as a "toy." A mature, MIT-licensed, 2.3k-star piano
roll already exists and is the cleanest one in open source: [`ryohey/signal`](https://github.com/ryohey/signal).**
It is the single best "stop reinventing X, use Y" on this list. Second place: an
early-stage but *uncannily on-target* project, [`glenwrhodes/OpenDaw`](https://github.com/glenwrhodes/OpenDaw)
— a Qt6 DAW on the Tracktion Engine with a piano roll, VST3, **and a built-in Claude AI
assistant with 30 DAW-control tools**. Someone is building becky's exact end-state; worth
watching/learning-from even though its GPL3 + early maturity argue against forking today.

---

## 1. Recommendation matrix (read this, skip the rest if busy)

| Surface | Verdict | Named project | License | One-line why |
|---|---|---|---|---|
| **Piano-roll / MIDI editor** | **PORT (study) the interaction model; or DRIVE as a web view** | [`ryohey/signal`](https://github.com/ryohey/signal) (MIT, 2.3k★, TS/React/WebAudio) | MIT | The toy we keep rebuilding already exists, done right; MIT means we can port its UX/layout math into Gio, or embed the web app. |
| **Audio output / decode / streaming** | **EMBED-LIBRARY** | [`gopxl/beep`](https://github.com/gopxl/beep) (MIT) over [`ebitengine/oto`](https://github.com/hajimehoshi/oto) | MIT / Apache-2.0 | Pure-Go playback+streaming+WAV/MP3/FLAC/Ogg decode; stop hand-rolling device I/O. Backs the native sampler engine. |
| **Realtime MIDI ports (controllers)** | **EMBED-LIBRARY** | [`gomidi/midi v2`](https://github.com/gomidi/midi) | MIT | De-facto Go MIDI; live ports via rtmididrv. (SMF export stays the in-house writer — already deterministic.) |
| **Drum machine (engine)** | **HAND-ROLL (keep) + EMBED a sampler lib** | becky's own `internal/audioengine` sampler; optional [sfizz](https://github.com/sfztools/sfizz) for SFZ | becky / BSD-2 | PINNED decision is correct — the native Go sampler IS the drum machine; don't re-evaluate. sfizz only if SFZ playback fidelity becomes the bar. |
| **Drum machine (export/FX)** | **DRIVE (optional)** | [Hydrogen](https://github.com/hydrogen-music/hydrogen) | GPL | Already the chosen optional export path; keep it optional, not the core. |
| **DAW (the full app)** | **DRIVE** | **REAPER** (already chosen) | proprietary, scriptable | Plain-text `.rpp` + full Lua/ReaScript + hosts Jordan's VSTs. Correct call; confirmed below. |
| **DAW engine (if we ever embed one in-process)** | **EMBED-LIBRARY (C++ sidecar, license-gated)** | [Tracktion Engine](https://github.com/Tracktion/tracktion_engine) | **dual GPL3 / commercial** | The only mature embeddable DAW engine — but GPL3-or-pay. Only if driving REAPER ever proves insufficient. |
| **Video editor / NLE** | **DRIVE (engine: libmlt)** | **kdenlive / MLT** (already chosen) | GPL / LGPL (libmlt) | kdenlive + OpenShot both ride MLT; libmlt is the LGPL engine becky already drives headlessly via `melt`. Correct call; confirmed below. |
| **Audio clip / waveform edit** | **HAND-ROLL on `internal/dsp` + beep** | becky `internal/dsp` + `gopxl/beep` | becky / MIT | Region/peaks/mixdown is small and already started; no whole-app to fork here. |

**Bias of this table:** *stop reinventing — drive or embed a mature thing.* The only
surfaces where "hand-roll" is the right answer are the drum-machine engine (already
pinned, and intentionally becky-owned) and trivial waveform/region editing.

---

## 2. Per-surface detail

### 2.1 Piano roll / MIDI editor — the big one

**The problem:** this is the surface the project explicitly keeps shipping as a toy
(the §6 log calls the in-window editor a "4-lane toy" / "piano not in the window").

**[`ryohey/signal`](https://github.com/ryohey/signal)** — **MIT**, **2.3k★**, 237 forks.
Tech: TypeScript (98%), React, Web Audio API, **Electron** for desktop. It is a
full multi-track piano-roll sequencer: velocity / pitch-bend / expression / modulation
lanes, tempo + non-4/4 via graph editors, SoundFont playback, Web MIDI in/out, WAV
export, runs as web, PWA, **or Electron desktop**, and has Docker support. Live app:
[signalmidi.app](https://signalmidi.app/).

Two ways to use it instead of reinventing:
- **PORT the interaction model into Gio** (the recommended, cheapest-risk path). MIT
  lets us read its layout math, drag/resize/velocity-lane logic, zoom/pan, and snap
  behaviour and reimplement them in becky's existing Gio canvas. This is the *exact* gap
  the `research/piano-roll.md` doc and the Sequentity reference were trying to fill —
  Signal is a far more complete, MIT-clean reference than Sequentity.
- **DRIVE/EMBED the web app** as a panel (Electron/WebView) if becky-canvas ever hosts a
  web surface. The project deliberately **retired WebView2** (GUI-RULES), so this is the
  weaker option — but it is a turnkey full piano roll if a web panel ever returns.

Honest caveat: Signal is described as an *application*, not a drop-in React *component*
library, so "embed as a widget" means running its app surface, not importing a package.
That is why **PORT the UX into Gio** is the lead recommendation.

Runner-up references (study, don't fork): **[LMMS](https://github.com/LMMS/lmms)** (GPL2,
C++/Qt — mature piano roll but heavyweight and GPL), **[MuseScore](https://github.com/musescore/MuseScore)**
(GPL3, notation-first), **[Helio](https://github.com/helio-fm/helio-workstation)**
(GPL3, C++/JUCE, clean modern sequencer — good UX reference, GPL blocks porting code).

### 2.2 Audio libraries (the Go ecosystem — confirms `research/go-dsp-midi.md`)

The existing `research/go-dsp-midi.md` already nailed this; summarizing the live-checked
state so it's in one place:

- **[`gopxl/beep`](https://github.com/gopxl/beep)** — **MIT**, v2.1.1 (Jan 2025), 574★.
  The maintained fork of the archived `faiface/beep`. Pure-Go **playback + streaming**
  via oto; decodes **WAV / MP3 / Ogg Vorbis / FLAC / MIDI**; `Streamer` interface for
  mixing/looping/effects; encodes WAV. **Use it for device I/O + decode** instead of
  hand-rolling — this is the "stop writing miniaudio glue" answer for the pure-Go path.
- **[`ebitengine/oto` v3](https://github.com/hajimehoshi/oto)** — **Apache-2.0**. The
  low-level cross-platform output backend under beep. Pure-Go on Windows.
- **[`gomidi/midi` v2](https://github.com/gomidi/midi)** — **MIT**, updated Feb 2026. The
  Go MIDI standard; use **only** for live controller ports (rtmididrv, cgo). **SMF export
  stays becky's in-house `internal/music` writer** (already deterministic — do not
  duplicate it with gomidi's smf).
- **[`go-audio/*`](https://github.com/go-audio)** (wav/midi/audio) — permissive, but
  lower-level and the wav decoder has the known 32-bit-float bug becky already fixed in
  `internal/sampledecode`; prefer becky's decoder + beep.
- **DSP effects:** there is **no** comprehensive real-time Go effects library — beep ships
  only gain/pan/EQ. Compressor/sidechain/reverb/delay stay **hand-rolled pure-Go**
  (`internal/dspfx`), per the cited formulas in `research/go-dsp-midi.md`. This is correct;
  it is not "reinventing," it's that nothing to reuse exists.
- **SFZ/SoundFont playback:** if sample-instrument fidelity becomes the bar, the mature
  engine is **[sfizz](https://github.com/sfztools/sfizz)** (BSD-2, C++, SFZ player as a
  lib/LV2/VST). becky's own sampler (`internal/sampler` + `kitimport.ParseSFZ`) is the
  pinned default; sfizz is the escape hatch, not the baseline.

### 2.3 Drum machine — the pinned decision is right; don't re-open it

The repo's `DRUM-MACHINE-DECISION.md` already settled this: **becky's own pure-Go sampler
engine IS the drum machine; Hydrogen is an optional FOSS-FX export; REAPER is the
full-DAW path.** Live-checked context confirming that's a sane split:
- **[Hydrogen](https://github.com/hydrogen-music/hydrogen)** (GPL) — advanced
  pattern-based drum machine, cross-platform, has command-line options + **OSC** control,
  XML `.h2song`/`drumkit.xml` formats becky already writes. Good as a *driveable export*
  for its FX/kits; GPL + GUI-centric means it's wrong as the core. **Keep it optional.**
- No credible **pure-Go** drum machine exists to fork — so "hand-roll the engine" here is
  genuinely the only path, and the project already did it well (velocity layers, choke,
  round-robin, Hermite resample). This is the one place where building from scratch was
  the *correct* call.

### 2.4 DAW — DRIVE REAPER (confirmed) / Tracktion as the only embeddable fallback

- **REAPER (already chosen)** — proprietary but the most automatable pro DAW: plain-text
  `.rpp`, full Lua/EEL **ReaScript** API, hosts every VST Jordan owns. The repo's
  `SPEC-BECKY-REAPER.md` path (becky authors `.rpp` + drives ReaScript) is the right
  "download a DAW and control it" answer. **Confirmed — no change.**
- **[Tracktion Engine](https://github.com/Tracktion/tracktion_engine)** — the *only*
  mature **embeddable** DAW engine (15+ yrs, ~115k LOC, JUCE module, C++20). **License is
  dual GPL3-or-later / commercial** — so embedding it in a closed product needs a paid
  license, and even GPL use forces becky's audio path to GPL. Recommendation: **do not
  embed unless driving REAPER proves insufficient**; if becky ever needs an *in-process*
  engine (no external DAW), this is the one to license. It also powers OpenDaw (below),
  which is a working proof it can be the spine of a small DAW.
- **Zrythm** (GPL2/3, C++23/Qt-QML/JUCE), **Giada** (GPL3, "hardcore loop machine"),
  **Ardour** (GPL2, has Lua scripting + headless `ardour --session`), **LMMS** (GPL2) —
  all GPL, all *applications* not embeddable engines. They're **DRIVE candidates** only if
  REAPER were ever unavailable; REAPER beats them all on scriptability + Jordan already
  owns/uses it. No reason to switch.

### 2.5 OpenDaw — the "someone already built becky" datapoint

**[`glenwrhodes/OpenDaw`](https://github.com/glenwrhodes/OpenDaw)** — **GPL3**, **early
(≈18★, v0.1.25 Mar 2026, ~67 commits)**, **C++20 / Qt6 / Tracktion Engine / VST3**. It
has: a **piano roll** with velocity + CC lanes, MIDI import/export, automation, 8 built-in
effects, audio export — **and an AI assistant powered by Claude (BYO API key) with 30
tools for autonomous DAW control.** This is, almost exactly, becky-canvas's end-state.

Why **not** fork it today: GPL3 (taints becky's licensing posture), very early/unproven,
Qt6 + Tracktion (a whole second native stack vs the chosen Go+Gio / drive-REAPER lines),
and it duplicates the REAPER-driving decision. **Value:** watch it, read its 30-tool
agent schema and piano-roll for ideas, and treat it as evidence the chosen direction is
right. Don't adopt the stack.

### 2.6 Video / NLE — DRIVE kdenlive's MLT (confirmed)

- **kdenlive / MLT (already chosen)** — kdenlive and OpenShot **both** build on the
  **MLT** multimedia framework over FFmpeg; **libmlt is LGPL** (engine) while the kdenlive
  app is GPL. becky already drives `melt` (the MLT CLI) headlessly. kdenlive leads on
  render efficiency + 4K preview after its GPU-engine overhaul. **Confirmed — correct
  engine choice.**
- **[`libopenshot`](https://github.com/OpenShot/libopenshot)** (LGPL3) — OpenShot's C++
  engine, the main alternative embeddable NLE library (has Python bindings). Viable, but
  OpenShot's render speed is its known weakness and becky is already invested in MLT/melt.
  **No reason to switch.**
- **[Flowblade](https://github.com/jliljebl/flowblade)** (GPL3) — also MLT-based, and
  notably **Python-scripted** (its whole app is Python over libmlt via GObject). If becky
  ever wanted to *script* MLT from a higher level than raw `melt` XML, Flowblade is the
  reference for how to drive libmlt programmatically. Engine is the same MLT becky already
  uses — so this is a *technique* reference, not a switch.
- Pure ffmpeg-assemble (becky's `internal/reel`) stays the right tool for deterministic
  forensic cut/concat where a full NLE is overkill.

---

## 3. How this reconciles with the existing fork-first decisions

| Existing decision | This survey's verdict |
|---|---|
| Video = DRIVE kdenlive/MLT (`melt`) | **Confirmed.** libmlt (LGPL) is the right embeddable engine; OpenShot/Flowblade ride the same MLT. No change. |
| Drums = becky's own Go sampler is THE drum machine; Hydrogen optional | **Confirmed.** No pure-Go drum machine exists to fork; hand-rolling the engine was correct. Hydrogen stays an optional GPL export. |
| DAW = DRIVE REAPER (`.rpp` + ReaScript) | **Confirmed.** Most-scriptable pro DAW; beats every GPL app on automation; Jordan owns it. Tracktion Engine is the only embeddable fallback, and it's GPL/commercial. |
| Audio hosting = C++ VST3 host sidecar (VST3 SDK MIT) | **Confirmed/unaffected.** Independent of the above. |
| GUI = Go + Gio, no embedded browser (WebView2 retired) | **Mostly confirmed** — the one tension is Signal (a web piano roll). Recommendation respects the no-browser rule by **porting Signal's UX into Gio**, not re-introducing a WebView. |

**Net new actions this survey recommends (small, additive):**
1. **Piano roll:** adopt **Signal (MIT)** as the *reference to port* — its drag/resize/
   velocity-lane/zoom logic into becky's Gio canvas. Replaces the weaker Sequentity
   reference. This is the highest-leverage change.
2. **Audio I/O:** lean on **`gopxl/beep` + `oto`** for pure-Go playback/decode instead of
   growing the cgo miniaudio glue, where a pure-Go path is acceptable.
3. **Keep everything else pinned.** REAPER, kdenlive/MLT, the native sampler, the VST3
   host, and Gio are all the correct calls — this survey *confirms* them rather than
   reopening them.

---

## Sources

- awesome-go audio/music: https://awesome-go.com/audio-and-music/
- ryohey/signal (MIT piano roll): https://github.com/ryohey/signal · https://signalmidi.app/
- gopxl/beep (MIT): https://github.com/gopxl/beep · oto: https://github.com/hajimehoshi/oto
- gomidi/midi v2 (MIT): https://github.com/gomidi/midi
- go-audio: https://github.com/go-audio
- sfizz (BSD-2 SFZ player): https://github.com/sfztools/sfizz
- Hydrogen (GPL): https://github.com/hydrogen-music/hydrogen
- REAPER ReaScript: https://www.reaper.fm/ (Lua/EEL scripting, plain-text .rpp)
- Tracktion Engine (dual GPL3/commercial): https://github.com/Tracktion/tracktion_engine · license: https://github.com/Tracktion/tracktion_engine/blob/develop/LICENSE.md
- OpenDaw (GPL3, Qt6 + Tracktion + Claude assistant): https://github.com/glenwrhodes/OpenDaw
- Zrythm (GPL): https://github.com/zrythm/zrythm · Giada (GPL3): https://github.com/monocasual/giada · Ardour (GPL2): https://ardour.org/ · LMMS (GPL2): https://github.com/LMMS/lmms · Helio (GPL3): https://github.com/helio-fm/helio-workstation
- kdenlive/MLT: https://kdenlive.org/ · libmlt: https://github.com/mltframework/mlt
- libopenshot (LGPL3): https://github.com/OpenShot/libopenshot · Flowblade (GPL3, MLT+Python): https://github.com/jliljebl/flowblade
- Slant Flowblade vs kdenlive (both MLT): https://www.slant.co/versus/7480/8189/~flowblade_vs_kdenlive
