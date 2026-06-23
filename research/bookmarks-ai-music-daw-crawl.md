# Bookmark Crawl — Open-Source DAW / Host Candidates (vs OpenDAW)

**Date:** 2026-06-22 · **For:** the DAW-host decision (adopt OpenDAW vs build UI in Go/giu/ImGui)
**Input:** `ai-music-stuff.tsv` (84) + `music-stuff.tsv` (38)
**Question:** Is any bookmarked project a *robust, well-developed* open-source DAW worth adopting as the host Jordan opens daily — more so than OpenDAW (young: 18★, 67 commits, v0.1.25, ~2026)?

## Bottom line

**No bookmarked project is a more-robust full standalone DAW than OpenDAW that you'd adopt as a daily host.** The closest things in the bookmarks are a MIDI sequencer (Helio), a plugin *host/rack* (Element, Carla), and a *headless* engine (DawDreamer) — none is a complete "open it every day" multitrack DAW. The genuinely robust full DAWs (Ardour, Zrythm, LMMS, Qtractor) are **NOT in Jordan's bookmarks at all**. The AI-music repos he bookmarked are uniformly **VST3 plugins that run *inside* a host DAW**, not hosts — but several are gold *study targets* for the agent-in-DAW UX.

## Ranked DAW-host candidates (from the bookmarks)

| # | Name / URL | Lang / Toolkit | License | Maturity | Completeness | Extensibility (AI + terminal) | vs OpenDAW verdict |
|---|---|---|---|---|---|---|---|
| 1 | **Element** — github.com/kushview/element | C++ / JUCE | GPL/REUSE | **1.6k★, v1.0.0 Feb 2026, 4,154 commits, very active** | Plugin **host/rack** (AU/LV2/VST/VST3/CLAP), node graph, sub-graphs, MIDI routing, undo. **No** arrangement timeline / piano-roll / multitrack record / render. | **Lua scripting** for DSP+UI (`element.readthedocs.io`); embeds plugin UIs. | More mature & stable than OpenDAW, but it's a **plugin host, not a DAW** — no arrangement surface. Adopt only if the product is "a rack," not "a DAW." |
| 2 | **Helio** — github.com/helio-fm/helio-sequencer | C++ / JUCE | GPLv3 | **3.5k★, 2,281 commits, active, cross-platform incl. Windows** | MIDI **sequencer**: linear+pattern, piano roll, microtonal, version control. **No** documented VST hosting / audio recording / mixer / render. | No documented plugin/scripting API. | Far more mature/polished than OpenDAW, but **MIDI-only composition tool**, not an audio DAW host. Not a Cubase replacement. |
| 3 | **DawDreamer** — github.com/DBraun/DawDreamer | C++ / JUCE + Python (nanobind) | GPLv3 | **1.2k★, v0.8.3 Sep 2024, active, Windows x86_64** | **Headless** DAW engine: VST2/3 host, full MIDI, param automation (audio-rate + PPQ), warp markers, FAUST, multi-render, JAX. **No GUI.** | Python API — *purpose-built* for programmatic/AI control. | Not a daily-open GUI DAW — it's the **engine** an AI agent drives. The best **backend** candidate in the bookmarks if you ever want a non-REAPER render path. |
| 4 | **Carla** — kx.studio (KXStudio) | C++ | GPLv2+ | Mature, widely shipped (gh URL 404; lives at kx.studio) | Plugin **host** (VST2/VST3/LV2/AU/etc.), patchbay/rack, OSC control. Not a full arrangement DAW. | OSC + plugin host API. | Mature host, but same class as Element — **rack, not DAW**. |
| 5 | **SuperCollider** — github.com/supercollider/supercollider | C++ + sclang | GPLv3 | Very mature, decades old | Audio **server + language** for synthesis/algorithmic comp. Not a track-based DAW. | Scriptable language IDE. | Wrong shape entirely — synthesis env, not a DAW Jordan opens to arrange. |
| 6 | **TuneFlow** — github.com/tuneflow/tuneflow | **TypeScript** SDK (DAW is separate app) | MIT | 210★, active | The repo is the **plugin SDK**, not the DAW. Core DAW is a separate closed app. | This IS a plugin API (TS + Python). | Not adoptable as a host — the open part is just the SDK. |
| 7 | **subfractal "AI DAW w/ Co-Producer"** (PR) — github.com/subfractal/AI-Search-App/pull/1 | React/TS/Tone.js/WebAudio | none stated | 87 commits, personal research, bug-fix phase | Browser multitrack + mixer + piano roll + drum machine + AI co-producer. | Web app. | A solo research web-DAW; not a stable adoptable host. Interesting AI-co-producer UX only. |
| 8 | **WavTool** — wavtool.com | Closed web app | proprietary | **Taken offline** | — | — | Not open source; dead. Skip. |

### Reference / heavyweights NOT in the bookmarks (the real adopt-or-fork field)
For context — these are the substantial full DAWs Jordan did *not* bookmark, and the only things that genuinely out-robust OpenDAW as a daily host:
- **Ardour** (C++/GTK, GPLv2) — pro multitrack/MIDI/mix/master, VST2/VST3/LV2. The mature heavyweight; GTK UI, hard to embed an agent into.
- **Zrythm** (C++/GTK4, AGPLv3) — modern, LV2/VST2/VST3/AU/CLAP/SFZ/SF2; the rising star.
- **LMMS** (C++/Qt, GPL) — mature, Windows-friendly, pattern/tracker + VST.
- **Qtractor** (C++/Qt, GPL) — Linux-only (disqualifies for Jordan's Windows).
- **Maolan** (github, **Rust**) — modern Rust DAW, CLAP/VST3/LV2, Linux/FreeBSD/macOS (no Windows yet). *Stack-aligned with becky but immature + no Windows.*
- **RS VST Host** (**Rust + egui**) — Rust VST3 host w/ DAG routing GUI. Stack-aligned, but a host not a DAW.
- **REAPER** — already Jordan's proven `becky-reaper` path; the pragmatic incumbent.

## AI-in-DAW study targets (agent-driven music software — study, don't adopt as host)
All are **VST3/AU plugins that run inside a host**, not hosts themselves — but valuable references for becky's agent UX:
- **ariknel/DAW-Copilot** (C++/JUCE8 + Python, MIT, 5★) — NL prompt → multitrack stems + MIDI; chat UI, Python sidecar over HTTP, drag-to-DAW. *Cleanest reference for the agent+sidecar pattern.*
- **betweentwomidnights/gary4juce** (C++/JUCE8, AGPLv3, 63★, 30 releases) — **7** AI models in one plugin (Stable Audio, MusicGen, ACE-Step, Magenta RT…); tabbed per-model UI, seed recall, local/remote backends. *Best reference for multi-model UX.*
- **ace-step/acestep.vst3** (C++17/GGML, MIT, 56★) — ACE-Step music-gen as a VST3 on CPU/CUDA/Metal/Vulkan; built-in HTTP server + web UI.
- **ariknel/gemini-audio-engineer-react** — React audio-engineer agent (study UX).
- **Vipin-Baniya/KalaOS** PR #18 — "smart simplified music studio with AI" (WIP, study only).

## One-line catalog of the rest (nothing lost)

**AI music generators / services (web/closed):** Suno; Udio; Stable Audio (Stability); Audimee; Kits AI; Musicfy; Lalals; Soundverse; Mureka; Fadr; AIVA; Producer.ai (AI music *agent*); Replay (RVC voice/stemming).
**Local music-gen models / tooling:** ACE-Step-1.5 (local SOTA music gen, multi-GPU); awesome-ace-step; Side-Step + Ace-Step Dataset-Manager + Kawaii-Future-Bass LoRA (ACE-Step training); declare-lab/jamify (flow song gen); HeartMuLa/heartlib (open music model); StemForge (stems→MIDI→remix); MOSS-Audio (OpenMOSS audio foundation model).
**MIDI / theory / generation helpers:** Audiocipher (melody/chord MIDI plugin); MIDIGEN; ChordChord; chordloops presets; Hooktheory (theorytab/genres/trends, Chord Crush); RM-Song-Generator (rule-based comp); tubone24/midi-agent-skill (text→MIDI agent skill); Conductor MIDI-Learn (Claude Code MIDI-map skill); Redtri/Hum2Midi; Redtri/Dawzy-chatbot; juancopi81 GPT-2 / mutopia_guitar_mmm (HF music LMs); Performance RNN; freshbots pop-chorus gen; AudioCipher AI-DAW/AI-music guide articles.
**Plugin frameworks / hosts / DSP libs:** JUCE + JUCE-tutorials (the C++ audio framework underneath most of these); spotify/pedalboard (Python audio FX); janminor/python-vstpreset (VST3 preset IO).
**Synthesis / neural audio:** DDSP-VST / DDSP-VST blog (Magenta neural synth VST); adobe-research/DeepAFx-ST (differentiable audio-FX style transfer); Arm real-time AI sound gen.
**Live-coding:** Strudel — calvinw/strudel-llm-docs + apadaki/strudel-ai (text→Strudel).
**REAPER / Cubase ecosystem (relevant to becky-reaper):** reaper-oss/sws (SWS extension); indiscipline/awesome-reaper; ReaLinks code; REAPER stash; JSFX-in-any-DAW thread; wtfazz/cubase-tools; janminor/cubase; bjoluc/cubase-mcu-midiremote; Steinberg forum threads on a Cubase scripting API / Logical-Editor AI (the "AI integration gap" Jordan is reacting to).
**Other / infra:** benkuper/Chataigne (modular art/tech control); mikeroyal/PipeWire-Guide; tekaratzas/GuitarGPT; ariknel/gingoduino (embedded MIDI theory engine); VoloBuilds/toaster; zottmann batch-FX article; NeurIPS AI-for-Music workshop; Gumroad; hilarl (HF profile).
**music-stuff.tsv (samples/theory/utilities, no DAWs):** Hooktheory; ChordChord; BMI Songview/sync; Pianobook; Orange Tree Samples; Kilohearts; Soundpaint; Cymatics/Serum presets; Thomann NI; Modern Metal Serum; REAPER stash; MacroKeyboardV2; **Samplab (audio→MIDI)**; **Jam Origin audio→MIDI**; off-the-beat Kontakt/MIDI dirs; SongStems/SongStems.net; freemidi/midiworld/nonstop2k; onemotion chord player; SampleSort; Black Salt Audio; Oolimo; Spitfire LABS; Heavyocity; STL Tones; Vital presets; Club Remixer; Gaga stems; Chrome Music Lab.

## Verdict on the OpenDAW-vs-giu/ImGui decision
Nothing in the bookmarks changes it. The bookmarks contain **no more-robust adoptable full DAW than OpenDAW** — only sub-DAW pieces (host, sequencer, headless engine) and a swarm of *plugins* that assume a host already exists. So the real choice stays: (a) adopt **OpenDAW** (young but already feature-complete because **Tracktion Engine** carries it, ships the ~30-tool Claude assistant — the closest existing thing to becky's goal), (b) keep driving **REAPER** via `becky-reaper` (the mature incumbent), or (c) build the UI in Go. If a hosting engine is ever wanted under becky's own UI, **DawDreamer** (headless, scriptable) is the bookmark-sourced backend; **Maolan/RS-VST-Host** (Rust) are the only stack-aligned options but are immature and Windows-unproven.
