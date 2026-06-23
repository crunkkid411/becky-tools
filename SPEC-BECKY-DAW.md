# SPEC-BECKY-DAW.md — the real DAW: pick the host via a spike, keep becky's brain

> **STATUS: SPEC (2026-06-23). Design-only — NOT built.** Built AFTER the NLE (Jordan's
> ordering). The host decision is deliberately **spike-first** (verify before committing —
> the cure for this project's churn). Grounded in: `research/daw-nle-strategy-feasibility.md`,
> `research/opendaw-adoption-plan.md`, `research/bookmarks-gui-toolkit-crawl.md`,
> `research/bookmarks-ai-music-daw-crawl.md`, `research/reference-projects-gap-analysis.md`,
> and `BECKY-CANVAS-ROADMAP.md`. Supersedes the "build the whole DAW in Gio" path for the
> hard widget surfaces (that was the trap that produced toys).

---

## 0. TL;DR — what we are building and why

Jordan needs a **Cubase-grade DAW** with **full manual controls** (50-100 tracks, pixel-
precise drag editing, complex bus routing/sidechain, render-in-place, real-time VST3
tweaking) — and becky's **AI brain** *inside* it. The blocker has been the GUI: **Gio has
ZERO pre-built DAW widgets**, so hand-building timeline/piano-roll/mixer/waveform from raw
primitives is the single hardest path (weeks → toys). The research settled the rest:

- becky's **engine + brain is right and stays Go** (`dawmodel`, `ctledit`/`ctlmodel`,
  `arrange`/`beatgen`/`musictheory`, the propose-preview-apply loop). ~120K lines, not wasted.
- The GUI **widget layer** is the only real problem. Two live, defensible paths remain
  (nothing in Jordan's bookmarks beats them):
  - **Path B — adopt OpenDAW** (glenwrhodes, C++/Qt6 + Tracktion + native VST3) which already
    ships a built-in Claude assistant (~30 DAW tools). Fastest to a full pro DAW **if** it
    builds + is mature enough. Risk: it's young/GPL3; becky's brain becomes an external
    toolset it calls.
  - **Path C — build the UI in Go via `giu` (Dear ImGui binding, MIT)**, porting
    `GroGy/im-neo-sequencer` (an ImGui timeline widget) for the hard surfaces, with a mature
    **C++ engine for audio+VST** (DawDreamer or becky's planned VST3 sidecar). Keeps becky
    all-Go + tightly integrated; matches Jordan's own bookmarks. Risk: more building.

**This spec does NOT pre-pick B or C. Phase 0 is a SPIKE that decides on evidence.**

becky's two pinned ideas fit **either** path and are wired AFTER the substrate is chosen:
- **Native llama.cpp embedding** (in-process via cgo, or native if we adopt C++). Honest
  framing: the win is *architectural* (one binary, no subprocess to babysit, clean
  streaming) — NOT raw latency. A warm llama-server's HTTP overhead is ~10-15 ms, dwarfed by
  inference; the real lag is **cold process-spawn-per-click** (fixed by a persistent engine —
  BECKY-CANVAS-ROADMAP Phase 0).
- **An embedded PTY running Claude Code** with full session context (advanced tasks +
  testing), via the same command bus.

---

## 1. The decision criteria (what the Phase-0 spike must answer)

| | **B — adopt OpenDAW (C++)** | **C — giu/ImGui (all-Go) + C++ audio engine** |
|---|---|---|
| Time to a usable manual DAW | fast **IF** it builds + isn't half-baked | slower (build the UI), but with real widget tools + a port seed |
| becky integration | brain = external CLIs the in-app agent calls (looser) | brain = native, in-process (tight — preserves what makes becky becky) |
| Manual controls | inherited from OpenDAW/Tracktion (mature engine) | inherited from DawDreamer/sidecar engine; UI built on ImGui |
| Stack | adopt Qt6/JUCE/Tracktion + GPL3 | stays Go (+ cgo); agents keep writing Go |
| Verification | OpenDAW's own; lose becky's `--render-frame` | ImGui offscreen FBO render (port the `--render-frame` idea) |
| Biggest risk | OpenDAW is **young** — may build but feel half-baked (the "fighting me" trap) | the widget layer is **real work** even with ImGui |

**Go/no-go for Path B:** OpenDAW (a) builds cleanly on Jordan's PC, (b) its
`AiToolExecutor.cpp` agent does something genuinely useful, (c) it feels usable for a
50-track workflow, AND (d) becky can drive it / open a session it generates. **If any fail →
Path C.**

---

## 2. Reuse map — becky's brain is the toolset either way (do NOT rebuild it)

| becky package (EXISTING) | Role in the DAW | Path B | Path C |
|---|---|---|---|
| `internal/dawmodel` | The canonical session (tracks/clips/mixer/buses/FX). | export to OpenDAW's format | the live model the ImGui UI edits |
| `internal/ctledit` (~20 ops) + `internal/ctlmodel` (NL→edit) | becky's "AI tools" — the propose-preview-apply edit layer. | map onto OpenDAW's ~30 tools | drive the ImGui UI directly |
| `internal/arrange`,`beatgen`,`musictheory` | Deterministic stem-aware composition (drums→bass→chords→melody). | CLIs the in-app agent calls | in-process generators |
| `internal/audioengine`,`drummachine`,`pianoroll`,`fxchain`,`autoroute`,`bounce`,`songbuild`,`library` | engines + render. | feed OpenDAW / headless render | the audio path (with DawDreamer/sidecar for VST) |
| `internal/seam`, `internal/llmlocal`, `internal/assistant`, `internal/habits` | the NDJSON command bus, model transport, agent router, preference learning. | the bridge to OpenDAW | the in-app agent + bus |

**The #1 capability gap vs OpenDAW (from research): an agentic MULTI-STEP loop** — call a
tool → read the updated session → pick the next → repeat. becky emits one batch per turn.
Fix = a thin **`internal/ctlagent`** reusing the packages above. Build this regardless of B/C.

---

## 3. Requirements (both paths must satisfy)

- **Full Cubase-grade MANUAL controls.** The AI sits *alongside* manual editing, never
  instead of it. Timeline (drag/resize/snap/ripple, multi-select, virtual scroll), piano
  roll (drag-create variable-length notes — NOT a step grid), mixer strips (fader/pan/mute/
  solo/sends/inserts), waveform clips, render-in-place, stem bounce.
- **The propose-preview-apply loop ("show me, don't do it").** becky's differentiator —
  every AI edit previews; nothing mutates until ✓. Protect it.
- **Per-VST plugin profiles (the "teach becky my plugins" idea).** Enumerate a VST3's params
  (name/min/max/default), let Jordan describe them in plain English ("compressor attack,
  5-15 ms for drums"), store a `PluginProfile` (JSON, in the project), so `set_fx_param`
  works by name via the existing `internal/habits` learning. Directly kills the "80% mundane
  menu-hunting" pain. (Wire after the substrate; works in B or C.)
- **Native llama embedding + (stretch) PTY/Claude panel** — §0, architectural not latency.
- **The engineering bar:** the five gates green for all Go code; "compiles" ≠ done — a DAW
  feature is done when the window opens, ▶ Play makes sound, the control works, no freeze
  (CANVAS-NORTH-STAR Definition-of-Done).

---

## 4. Build plan

**Phase 0 — THE SPIKE (local, ~1-2 days, decides B vs C):**
- [ ] **SPIKE-B:** clone + build OpenDAW on Jordan's PC (Qt6/MSVC/JUCE/Tracktion). If it
      compiles + launches, read `AiToolExecutor.cpp`; prove becky can emit a session it opens
      OR call becky CLIs from inside it. Score it against the §1 go/no-go.
- [ ] If B fails go/no-go → **SPIKE-C:** `giu` hello-world (cgo/mingw — already present) →
      port `im-neo-sequencer`'s track/timeline → render a real `dawmodel.Arrangement` in it.
      Prove the all-Go ImGui path gives a real, manipulable timeline.
- [ ] **Report honestly + pick the path.** Then continue below on the chosen substrate.

**Phase 1 — the brain hookup (local, pure-Go, green offline; path-independent):**
- [ ] `internal/ctlagent` — the multi-step agentic loop over `ctledit`/`ctlmodel`/`dawmodel`
      (value-asserting tests: a 3-step instruction produces the right sequence of edits).
- [ ] The new `ctledit` ops the research flagged: `set_fx_param`, `add_fx`, aux-send,
      transport-nav, take management; a **rich session READ** (`Describe`/`Snapshot`).

**Phase 2 — the substrate-specific DAW (on B or C):**
- [ ] B: the becky↔OpenDAW bridge (drive its tools / open generated sessions) + map the
      30-tool schema onto `ctledit`. **OR** C: the ImGui timeline/piano-roll/mixer/waveform
      widgets (port im-neo-sequencer; adapt to clips/MIDI) + DawDreamer/sidecar for audio+VST.
- [ ] Persistent in-process audio engine (no spawn-per-click — ROADMAP Phase 0).

**Phase 3 — the manual+AI polish:**
- [ ] Per-VST `PluginProfile` workflow; native llama embed; (stretch) the PTY/Claude panel.

---

## 5. Open decisions for Jordan
1. **Spike B first** (test the biggest shortcut cheaply) — agreed in chat. *Or spike C first
   if you'd rather stay all-Go regardless.*
2. **Audio engine for Path C:** DawDreamer (mature, JUCE, scriptable VST host) vs becky's
   own C++ VST3 sidecar (GUI-RULES). DawDreamer is more proven; the sidecar is more becky.
3. **REAPER stays dormant** (you cooled on the REAPER-chat — "slow, cumbersome, backwards").
   `becky-reaper`/`internal/reaper` are kept, not deleted, as a fallback only.

## 6. Non-goals
- Not driving REAPER/Cubase as the live DAW (the paused external-app path).
- Not an "AI-does-everything, no manual controls" view — you need the full manual surface.
- Not a from-scratch audio engine in Go (use a mature C++ engine for realtime/VST).
