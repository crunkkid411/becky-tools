# OpenDaw → becky-canvas adoption plan

**Author:** cloud research subagent · 2026-06-22
**Question:** Jordan killed "drive REAPER via REAPER-chat." He wants everything inside
**becky-canvas** (native Go + Gio). He flagged **glenwrhodes/OpenDaw** — a GPL3, C++/Qt6/Tracktion
DAW with a built-in Claude assistant exposing ~63 DAW-control tools — as "becky-canvas's exact
end-state." Fork it, or port its design into becky-canvas Go?

---

## 0. VERDICT (lead with it)

**PORT the design, do NOT fork the code. Hybrid, heavily weighted to PORT.**

- **Treat OpenDaw as a *design reference + tool-schema donor*, not a dependency.** Its single most
  valuable asset — the **~63-tool Claude control schema** (`src/ai/AiToolDefs.cpp`) — is a
  language-neutral *capability list*; copying that idea is not copying GPL code. becky already owns
  the equivalent brain: `internal/ctledit` (20 ops, deterministic applier) + `internal/ctlmodel`
  (NL→batch proposer). **becky is ~70% of the way to OpenDaw's tool surface already.**
- **Forking is wrong and breaks three pinned facts:**
  1. **License.** OpenDaw is **GPL3** + links **Tracktion Engine (GPL3) + JUCE (AGPL3)**. becky is
     private/single-user, but adopting a JUCE/Tracktion base permanently chains becky's audio core
     to a foreign copyleft stack — for capability becky already chose the **MIT VST3 SDK** to get
     (`GUI-RULES.md` §6, VST3→MIT 2025-11-04).
  2. **Stack.** OpenDaw is **C++20 + Qt6 + CMake**. becky-canvas is **Go + Gio**, and
     `CANVAS-NORTH-STAR.md` *pins*: "becky-canvas IS the tool Jordan opens… do NOT swap in a
     different app because the native thing is hard." A Qt6 fork is exactly that anti-pattern.
  3. **"Everything in becky-canvas."** A fork IS a separate app — the same category error as the
     dead REAPER plan, just with a nicer chat. Porting keeps the one-window promise.
- **Honest cost:** a fork hands you a working audio engine + VST3 host + piano-roll UI *today* — but
  in the wrong language, under AGPL, as a second window. And that one "free" thing
  (audible VST3 hosting) is **already on becky's roadmap** as the license-clean C++ sidecar
  (`GUI-RULES.md` Phase 2–3). So porting the *schema + UX + feature checklist* is high-value /
  low-regret; forking the *implementation* is high-regret.

**One sentence:** becky already owns OpenDaw's brain pattern (deterministic applier + NL proposer);
copy OpenDaw's **capability list** and **agentic-loop UX**, extend becky's existing packages, and
never take on its C++/Qt/AGPL body.

---

## 1. What OpenDaw actually is

### 1.1 Architecture & stack
| Layer | OpenDaw | becky equivalent |
|---|---|---|
| Language | C++20 | Go engine + Gio GUI; C++ only in the future audio sidecar |
| GUI | **Qt6 Widgets** | **Gio** (D3D11, no cgo) |
| Audio engine | **Tracktion Engine (JUCE, GPL3/AGPL3)** | `internal/audioengine` (pure-Go synth/sampler) + planned **C++ VST3/ASIO sidecar** (MIT VST3 SDK) |
| Plugin host | VST3 via Tracktion/JUCE | planned C++ sidecar; `internal/fxchain` already models chains |
| Build | CMake 3.22 + Ninja | `go build` + `build-all-tools.bat` |
| AI | embedded **Claude**, agentic tool-use loop, ~63 tools, `Ctrl+Shift+Space` overlay | `ctlmodel` (local GBNF/keyword) → `ctledit` applier; show-me overlay built |
| License | **GPL3** (+ Tracktion GPL3 + JUCE AGPL3) | private / VST3-MIT path |

A QTimer pumps JUCE's `MessageManager` every 10 ms; audio runs on JUCE realtime threads — exactly
the GUI-thread / separate-realtime-audio-thread split `GUI-RULES.md` §2 already mandates.

### 1.2 The AI assistant (the part worth studying)
- A **built-in Claude agent that EXECUTES** operations (not a chatbot that tells you what to click).
- **Agentic tool-use loop:** given the tool schemas, Claude returns `tool_use` blocks, the app runs
  each against the live project, feeds results back, and **loops until the request is fulfilled**
  (standard Anthropic loop, up to ~30 tool calls per request).
- **Quick-prompt overlay:** `Ctrl+Shift+Space` anywhere → type intent → watch it happen.
- **Composition:** English idea → agent calls `create_midi_clip` + `add_midi_notes` to write real
  notes (pitch/timing/duration/velocity) into the piano roll.
- Tools live in `src/ai/AiToolDefs.cpp` (definitions) + an executor that dispatches each named tool.
  This is a flat `name → params → mutation` catalog — **precisely becky's `ctledit` op-enum shape.**

> **Cloud-vs-local difference:** OpenDaw calls the **cloud Anthropic API** with a key. becky's
> pinned design uses a **local GBNF-constrained model** (`ctlmodel`, Qwen3-4B on disk) + keyword
> fallback, offline invariant intact. Copy OpenDaw's **tool list + loop UX**, keep becky's **local
> brain.** The catalog is identical either way; only the model behind it differs.

### 1.3 UI features OpenDaw ships
Timeline (multi-track, drag-drop audio/MIDI, fades, grid snap, freeze, MIDI→audio bounce) ·
**piano roll** (Ctrl-click add, drag move/resize, velocity lane, **CC lane**, quantize, zoom) ·
**sheet-music notation** · **destructive audio clip editor** (cut/fade/normalize/reverse/mono) ·
**mixer** (fader/pan/M/S/arm, 2 FX inserts/track, bus+master strips, meters) · **node-based routing
view** (inputs→tracks→buses→master, drag cables, sidechain) · **per-track automation lanes**
(breakpoints, freehand draw) · **8 built-in effects** (Reverb, 4-band EQ, Compressor, Delay, Chorus,
Phaser, LPF, Pitch-shift) · transport bar · file browser.

---

## 2. Gap table — OpenDaw vs becky today

Legend: **HAVE** = shipped + tested · **PARTIAL** = engine exists, not wired to canvas / missing a
piece · **MISSING** = not built.

| OpenDaw capability | becky status | package / note |
|---|---|---|
| Deterministic AI tool-applier ("30 tools") | **HAVE** | `internal/ctledit` — 20 ops, immutable, drops-illegal-with-reason |
| NL → tool batch (agent box) | **HAVE** | `internal/ctlmodel` (keyword + GBNF model; op enum locked) |
| **Agentic multi-step loop (call tools repeatedly until done)** | **MISSING** | becky does ONE batch/turn; no observe→re-call. **Biggest brain gap.** |
| One canonical session model | **HAVE** | `internal/dawmodel.Arrangement` (tracks/clips/notes/mixer/buses/FX) |
| Piano roll (add/move/resize/transpose/velocity) | **HAVE** | `dawmodel/pianoroll.go` + `internal/pianoroll`; canvas `gui_pianopanel.go` |
| Piano-roll **CC lane** | **MISSING** | notes carry no CC stream |
| Drum grid / step sequencer | **HAVE** | `dawmodel/drumgrid.go`, `internal/drummachine`, `internal/beatgen` |
| Quantize / swing / humanize | **HAVE** | `dawmodel/quantize.go`, `internal/pianoroll/humanize.go` |
| MIDI compose from English | **HAVE (deterministic, stem-aware)** | `internal/arrange` (bass/chords/melody) + `becky-compose` — richer than OpenDaw's per-note write |
| Mixer (gain/pan/mute/solo) | **HAVE** | `dawmodel/mixer.go` |
| Bus routing | **HAVE** | dawmodel buses + `internal/autoroute` (rule-based, one-shot) |
| Sidechain | **HAVE** | `dawmodel.AddSidechain` + autoroute rules |
| FX chain model | **PARTIAL** | `internal/fxchain` (as DATA); no real-time FX render until C++ sidecar |
| set effect param / bypass | **PARTIAL** | `FXSlot.Bypass` exists; no `set_effect_parameter` op, no live host |
| 8 built-in effects | **MISSING** | becky plans VST3 host instead; no built-in DSP FX today |
| Transport play/stop/record/seek | **PARTIAL** | `internal/audioengine` transport + canvas ▶/■; record thin |
| set tempo / time signature | **HAVE / PARTIAL** | `set_tempo` HAVE; time-sig fields exist, no edit op |
| Audio clip editor (cut/fade/normalize/reverse) | **PARTIAL** | `internal/audiotrack` peaks+mixdown; destructive ops not exposed |
| Automation lanes (breakpoints/draw) | **MISSING** | no automation-curve model |
| Mix-analysis (levels/LUFS/freq/stereo/transients/masking) | **PARTIAL** | `internal/dsp`, `becky-ref`, `becky-stems` compute most — as CLIs, not canvas tools |
| auto-route by name / buses-from-roles | **HAVE** | `internal/autoroute` IS exactly these two tools |
| save / undo / redo | **PARTIAL** | save=project.json; `internal/undo` exists; not surfaced as agent tools |
| VST3 instrument/effect host | **PARTIAL/PLANNED** | `GUI-RULES.md` Phase 3 C++ sidecar; `fxchain`/`audiohost` scaffolding |
| Sheet-music notation | **MISSING** | low priority for Jordan |
| File browser / drag-drop | **PARTIAL** | argv-drop works; in-window OS drop is the known Gio rough edge |

**Headline:** becky **matches or beats** OpenDaw on the deterministic music brain (stem-aware
arrangement, rule-based routing, genre theory) and the applier pattern. becky **trails** on only
three: (1) the **agentic multi-step loop**, (2) **audible FX / VST3 hosting actually making sound**,
(3) breadth of **tools exposed to the agent** (analysis/save/undo/transport/automation are computed
but not all wired as `ctledit` ops).

---

## 3. The single biggest thing becky is missing

**An agentic, multi-step tool-use LOOP.** OpenDaw's agent calls a tool, *sees the result*, picks the
next tool, and repeats until "lo-fi beat in F minor at 80 BPM with a sidechained bass" is fully
built. becky's `ctlmodel.Propose` emits **one batch, once** — powerful, but it cannot observe-and-
react ("I placed the kick; now read it and write a bass that locks to it across 8 bars"). That
observe-act loop is what makes OpenDaw feel like it builds the whole track for you.

The fix is small and becky-shaped: a thin **`internal/ctlagent`** loop that per turn (1) snapshots
the arrangement (`ctlmodel.Snapshot`), (2) asks the local model for the *next* batch + a `done`
flag, (3) applies via `ctledit.Apply`, (4) re-snapshots and repeats (cap ~6 iterations, every step
through the **show-me overlay** so Jordan approves). Reuses `ctledit`/`ctlmodel`/`dawmodel`
verbatim — an orchestration layer on top, not a rewrite. **Highest-leverage single addition.**

Second-biggest gap: **sound** — built-in FX / a real VST3 host so the agent's mix moves are
audible. Already `GUI-RULES.md` Phase 2–3 (C++ sidecar); not new scope, just not done.

---

## 4. The gold: OpenDaw's ~63-tool schema → becky `ctledit` op mapping

The reusable artifact. **"add op"** = new `ctledit` op over an existing verb (cheap). **"add
verb+op"** = needs a small new dawmodel/engine verb first.

### Project / info (read tools — feed the loop's snapshot)
| OpenDaw tool | becky mapping | status |
|---|---|---|
| `get_project_info` | `ctlmodel.Snapshot(arr)` | HAVE (snapshot, not a tool) |
| `get_track_list` / `get_track_info` | dawmodel + `ctledit.Describe(arr)` | HAVE |
| `get_clips_on_track` / `get_midi_notes` / `get_channel_names` | dawmodel readers | HAVE (expose as read-tools) |
| `get_transport_state` | audioengine transport | PARTIAL |

### Track management
| OpenDaw | becky op | status |
|---|---|---|
| `create_audio_track` / `create_midi_track` | `OpAddTrack` (Kind) | **HAVE** |
| `create_bus_track` | add op → dawmodel bus create | add op |
| `delete_track` | add op → new dawmodel verb | add verb+op |
| `rename_track` | add op | add op |

### Track properties
| OpenDaw | becky op | status |
|---|---|---|
| `set_track_mute` | `OpMute` | **HAVE** |
| `set_track_solo` | `OpSolo` | **HAVE** |
| `set_track_volume` (dB) | `OpSetGain` (linear; add dB↔linear) | **HAVE** (unit adapt) |
| `set_track_pan` | `OpSetPan` | **HAVE** |
| `set_track_mono` | add op → `Strip.Mono` field | add verb+op |
| `set_track_record_enabled` | add op → `Strip.Arm` field | add verb+op |

### Routing
| OpenDaw | becky op | status |
|---|---|---|
| `set_track_output` | `OpRouteTo` | **HAVE** |
| `clear_track_output` | add op | add op |
| `assign_input_to_track` / `clear_track_input` / `get_available_inputs` | record-input path | MISSING (low priority) |
| `auto_route_by_name_patterns` | `OpRoute` → `autoroute.Apply` | **HAVE** |
| `create_mix_buses_from_roles` | `autoroute.Apply` builds the bus tree | **HAVE** |
| `group_tracks_by_inferred_role` | `autoroute.BusFor` per track | HAVE (expose as read) |

### Effects
| OpenDaw | becky op | status |
|---|---|---|
| `list_available_effects` | enumerate built-ins + VST scan | PARTIAL |
| `get_track_effects` | read `Strip.FX` / `fxchain` | HAVE (expose) |
| `add_effect_to_track` / `remove_effect_from_track` | add ops over `fxchain.Add` / strip FX | add op |
| `set_effect_parameter` | add op + param model on `FXSlot` | add verb+op |
| `set_effect_bypass` | add op over `FXSlot.Bypass` | add op |

### Transport
| OpenDaw | becky op | status |
|---|---|---|
| `play` / `stop` / `record` | canvas transport (record thin) | PARTIAL |
| `set_position` | add op (seek) | add op |
| `set_tempo` | `OpSetTempo` | **HAVE** |
| `set_time_signature` | add op over `Num`/`Den` | add op |

### MIDI composition (headline UX)
| OpenDaw | becky op | status |
|---|---|---|
| `create_midi_clip` | add op `create_clip` → dawmodel verb | add verb+op |
| `add_midi_notes` | `OpAddNotes` | **HAVE** |
| `clear_midi_notes` | add op | add op |
| `remove_midi_notes` (by filter) | `OpDeleteNotes` (by id; add filter form) | HAVE/extend |
| `set_clip_channel` | add op | add op |
| `setup_midi_channels` / `set_channel_name` | multi-clip helper | MISSING (niche) |
| **becky bonus:** `OpAddLayer`, `OpGenerateBeat`, `OpEuclidLane`, `OpDuplicateNotes` | — | **HAVE — beyond OpenDaw** |

### Analysis (OpenDaw's "mix engineer" tools — becky has the DSP, not the wiring)
| OpenDaw | becky source | status |
|---|---|---|
| `analyze_track_levels` / `analyze_master_levels` | `internal/dsp`, `becky-stems` (peak/RMS/crest/LUFS-ish) | PARTIAL (CLI → wire as read-tool) |
| `analyze_frequency_balance` | `internal/dsp` FFT + `becky-ref` 8-band | PARTIAL |
| `analyze_stereo_image` | mono today (`becky-ref` notes stereo as Phase-2) | MISSING (mono) |
| `analyze_transients` | dsp onset/crest | PARTIAL |
| `analyze_masking` | `becky-ref` band-compare between two stems | PARTIAL |

### Semantic mix actions
| OpenDaw | becky source | status |
|---|---|---|
| `apply_mix_preset` / `set_track_target_peak` / `set_track_dynamic_goal` / `set_reverb_character` / `set_bus_glue` / `set_master_target` | `becky-mix` (JST mix.json) + `becky-ref` match-plan | PARTIAL (CLI; wire as ops; audible only with the FX host) |
| `preview_mix_plan` / `commit_last_mix_stage` / `revert_last_mix_stage` | **show-me overlay + `internal/undo`** | **HAVE (becky's per-edit approve/undo is better)** |

### Project ops
| OpenDaw | becky op | status |
|---|---|---|
| `save_project` | add op (write project.json) | add op |
| `undo` / `redo` | `internal/undo` | HAVE (expose as ops) |

**Tally:** of ~63 OpenDaw tools, becky **already has ~20 as ctledit ops + ~10 more as engine/CLI
capabilities** to expose, **~15 are cheap "add op"** over existing verbs, **~10 need a small new
verb first**, **~8 are low-priority/niche** (input routing, channel naming, sheet music). The
agent-control surface is mostly a **wiring** job, not a build job.

---

## 5. Phased roadmap (EXTEND existing packages — never rewrite)

Cloud-buildable Go except where marked LOCAL.

**Phase A — close the tool surface (`ctledit`/`dawmodel`, cloud, high ROI).** Add the cheap ops so
the agent reaches OpenDaw breadth: `rename_track`, `set_track_mono`, `set_track_record_enabled`,
`set_time_signature`, `set_position`, `create_clip`/`clear_midi_notes`/`set_clip_channel`,
`add_effect`/`remove_effect`/`set_effect_bypass`, `save_project`, `undo`/`redo`, `create_bus`,
`delete_track`. Each = a new `Op*` constant + an `applyOne` case over an existing (or tiny new) verb
+ a value-asserting test. The GBNF op-enum in `ctlmodel/grammar.go` derives from the constants, so
it stays in sync. **Outcome:** the agent box reaches OpenDaw-level control.

**Phase B — the agentic loop (`internal/ctlagent`, cloud, the biggest brain gap).** New thin
package: `Run(instruction, arr, proposer, applier)` loops snapshot→propose-next→apply→re-snapshot
(cap iters, `done` flag), each step through the show-me overlay. Reuses `ctlmodel` + `ctledit`
unchanged; add `get_*`/`analyze_*` read-tools as snapshot enrichers so the model can observe.
**Outcome:** "build me a full track" works in one prompt, offline.

**Phase C — expose analysis as agent tools (cloud).** Wrap `internal/dsp` / `becky-ref` /
`becky-stems` as read-only "analysis" tools so the loop self-corrects mixes ("kick masks bass →
duck it"). No new DSP — just surface existing CLIs in-process.

**Phase D — make it SOUND (LOCAL, already on the GUI-RULES roadmap).** The C++ VST3/ASIO sidecar
(GUI-RULES Phase 2–3) so `set_effect_parameter`, the mix verbs, and VST3 instruments are audible —
the one thing a fork would've handed us, in becky's license-clean shape. Optionally add a few
pure-Go built-in effects (`internal/dspfx`: RBJ EQ, simple comp/delay/reverb) so the agent has
audible FX even before the sidecar — a small becky-native answer to OpenDaw's 8 built-ins.

**Phase E — UI parity polish (Gio, mostly local).** Piano-roll **CC lane**, automation lanes
(breakpoint model on the strip), and (low priority) a notation view. Each is a Gio panel over a
small dawmodel extension.

**Copy from OpenDaw as a *reference* (not code):** the **tool-schema catalog** (Section 4) — mine
`src/ai/AiToolDefs.cpp` for tool *names + param shapes + descriptions* (great GBNF / system-prompt
copy; facts, not GPL expression); the **agentic-loop UX** (`Ctrl+Shift+Space` quick-prompt,
"executes not narrates," per-request multi-tool autonomy); the **8-effect param layouts** as a spec
for `internal/dspfx`.

**Do NOT take:** any C++/Qt/Tracktion/JUCE source, the GPL3/AGPL3 entanglement, the cloud-API key
path (becky stays local-model + offline). Don't vendor it as a submodule — read it on GitHub, then
delete the checkout.

---

## 6. Bottom line for Jordan

becky-canvas is *closer to OpenDaw than it looks* — it already has the deterministic brain
(`ctledit`/`ctlmodel`/`dawmodel`/`arrange`/`autoroute`) OpenDaw spent its AI tooling building, and
in places (stem-aware composition, rule-based routing, per-edit show-me/undo) becky is ahead. Right
move: **mine OpenDaw's 63-tool list as a checklist, wire the missing tools as `ctledit` ops, add a
small agentic loop, and make it sound via the already-planned C++ sidecar** — all inside the one
becky-canvas window. Forking a GPL3 Qt6/JUCE app would hand Jordan a *second* window in the wrong
language under copyleft, re-running the exact "escape to another app" mistake the REAPER plan just
died from.
