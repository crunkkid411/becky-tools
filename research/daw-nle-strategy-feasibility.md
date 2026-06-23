# DAW + NLE Strategy Feasibility — Honest Decision Support

Date: 2026-06-22. Author: cloud research subagent. Status: decision-support, not a build order.
Related docs in this folder: `qwen-daw-review.md` (the review this corrects), `opendaw-adoption-plan.md`,
`daw-video-timeline-gui-components.md`, `gui-toolkit.md`. This doc is the three-way **A/B/C feasibility
comparison + de-risking spikes**; it does not duplicate those.

## TL;DR per surface

- **DAW → Option B (adopt + recompile a mature C++ DAW).** The single best-fit host already exists and is almost
  becky-shaped: **OpenDAW (glenwrhodes/OpenDaw)** — Windows, Qt 6.10 / C++20, Tracktion Engine audio core, MIDI piano
  roll, **native VST3 hosting via JUCE**, and *already ships a built-in Claude AI assistant that drives the whole DAW
  through 30 tools*. GPLv3, actively committed (v0.1.25, March 2026). Building Cubase-grade widgets from scratch
  (Option A) or even via ImGui (Option C) is a multi-month slog that this host has *already done*.
- **NLE → Option B as well, but a different host: keep driving MLT, and adopt a forkable MLT/Qt editor as the GUI
  shell — Shotcut is the most extensible.** becky already writes `.kdenlive`/MLT and renders headless via `melt.exe`
  (per CLAUDE.md). The missing piece is a *manual* timeline GUI, not the engine. Shotcut's QML-panel plugin system is
  the cleanest extension surface of the FOSS NLEs; kdenlive is the most capable but extends only in C++/Qt with no
  scripting. **Olive is NOT a candidate right now** — author mid-rewrite (C#/Godot), no public builds since Dec 2024;
  only the community fork ("Olive CE") still ships.
- **Option C (Go engine + Dear ImGui via cimgui-go) is the strong fallback if Jordan rejects a C++ codebase** — but it
  is *not* a clean drop-in beside Gio (window/backend conflict, below), so it means replacing the GUI layer, not
  augmenting it.

The Go engine/brain/CLI layer (~157K lines) stays in **all** options. It is the right tool and is not wasted.

---

## The two de-risking SPIKES to run FIRST (1–2 days each, before committing)

### SPIKE-DAW (do this one first): "Can becky drive OpenDAW?"
1. Clone + build OpenDAW on Jordan's PC (Qt 6.10 + MSVC 2022 + JUCE + Tracktion). Confirm it opens, loads a VST3,
   plays a track. **This is the gate** — if the C++/Qt/JUCE toolchain won't build cleanly on his machine, Option B is
   dead and you pivot to C.
2. Read `AiToolExecutor.h/cpp` (its 30-tool layer) and `plugin-cache.xml`. Answer one question: *can a becky CLI emit
   a session OpenDAW can open, or can OpenDAW's tool layer shell out to becky's CLIs?* If yes, you have a ~1-week path
   to "becky composes → opens in a real DAW with manual editing + VST3," instead of months of widget work.
3. Decide: fork OpenDAW (replace its Anthropic-API panel with becky's local-model + becky CLIs), or just *use* it as
   the manual front-end behind becky-compose. Either way you skip building the DAW widgets entirely.

### SPIKE-NLE: "Shotcut filter/panel plugin + becky timeline import"
1. Build Shotcut from source on Windows; confirm a QML-panel plugin loads (it reads QML from the filesystem, not the
   binary — easy to iterate).
2. Prove becky can produce an MLT XML / project Shotcut opens with becky's cuts already laid out (becky already writes
   MLT). If Shotcut opens a becky-authored timeline, the NLE problem is *routing + a thin panel*, not a from-scratch
   NLE.

If both spikes pass, becky becomes "the AI brain that authors and drives two real, manually-editable pro apps" — the
same fork-first pivot already chosen for audio (REAPER) and drums (Hydrogen) in CLAUDE.md, done properly.

---

## Option A — stay Go + Gio, hand-build the pro widgets. HONEST VERDICT: avoid for the hard surfaces.

- **Prior art: effectively zero.** Searches for any Gio-based DAW / sequencer / piano-roll widget returned nothing —
  only core `gioui/gio`, generic widget sets (`jkvatne/gio-v`, `gioui-plugins`), and unrelated piano-roll repos in
  other toolkits. Gio ships **no** timeline, no piano roll, no mixer strip, no waveform widget. You build 100% of it
  from raw clip/paint ops. This matches becky's lived experience ("weeks producing toys").
- **Effort to Cubase-grade:** a pixel-precise arranger (50–100 tracks, drag-trim, snapping), a real piano roll, mixer
  strips with sidechain routing, and waveform rendering are each multi-week efforts in an immediate-mode toolkit with
  no scaffolding. Realistically *months* to reach even "usable," and you'd still be reinventing what mature DAWs ship.
- **Where Gio is genuinely fine:** becky-canvas's HUB, the agent box, simple panels, launch buttons. Keep Gio there.
  Do not keep trying to grow it into Cubase.

## Option B — modify + recompile a mature FOSS C++ app; embed local LLM + tool layer; becky CLIs as the toolbox.

Recommended for **both** surfaces. Ranked host tables:

### DAW hosts

| Host | Lang / toolkit | License | UI extension surface | LLM/terminal embedded before? | Verdict |
|---|---|---|---|---|---|
| **OpenDAW** (glenwrhodes) | C++20, Qt 6.10, Tracktion Engine, JUCE | GPLv3 | Full source; native Qt panels; **`AiToolExecutor` 30-tool agent layer already built** | **YES** — built-in Claude (Anthropic *API*, not local; no PTY) | **TOP PICK.** Already ~90% of the target. Fork it: swap the API panel for becky's local model + becky CLIs. |
| **Ardour** | C++, GTK | GPLv3 (contributions must be GPL) | **Lua scripting** (real-time-safe), incl. GUI-context access to the Editor object: open/close windows, undo/redo, select | No known LLM/terminal embed | Strong #2. Lua is a sanctioned extension point — no recompile needed for the agent layer. Mature, huge feature set. GTK (not Qt) is the friction. |
| **Zrythm** | C++, GTK4 | AGPLv3 (copyleft) | Scripting was Guile/ECMAScript but **deprecated/disabled**; migrating to libpeas | No | Skip for now — extension story mid-migration and unstable. |
| **Qtractor** | C++, Qt6 | GPLv2 | Plugins = LADSPA/LV2/VST/**CLAP**; UI not designed for panel plugins | No | **Linux/JACK-only** — disqualifying for a Windows-only target. |
| **LMMS** | C++, Qt (still defaults Qt5) | GPLv2 | Source-level only; no scripting/panel API | No | Qt5→6 migration ongoing; weaker arranger; not pro-grade. Skip. |
| **Tracktion Engine** | C++20 library (JUCE module) | **GPL/Commercial dual** + needs a JUCE licence | It's an *engine library*, not an app — you build the UI | n/a | This is the engine *under* OpenDAW. Use it *via* OpenDAW, don't start from the bare library. |

**DAW recommendation: fork or drive OpenDAW.** Fallback: Ardour via Lua if a GTK/C++ fork is too heavy.

### NLE hosts

| Host | Lang / toolkit | License | UI extension surface | LLM/terminal before? | Verdict |
|---|---|---|---|---|---|
| **Shotcut** | C++/Qt6 + MLT | GPLv3 | **QML-panel plugin system** (filters today; UI is QML loaded from filesystem — easy to iterate/extend) | No | **TOP NLE PICK.** Cleanest extension surface; same MLT engine becky already writes for. |
| **kdenlive** | C++/Qt6 + MLT + KDE Frameworks | GPLv3 | C++/Qt only; Frei0r effects; **no scripting layer** | No | Most *capable* NLE, but heavyweight KDE deps and no scripting → all extension is C++ recompiles. becky already uses it headless as a backend. |
| **Olive** | C++/Qt (rewriting to C#/Godot) | GPLv3 | n/a mid-rewrite | No | **Not viable now** — author mid-rewrite, no builds since Dec 2024. Only "Olive CE" fork alive. |
| **OpenShot / libopenshot** | C++ lib + Python/Qt UI | GPLv3 | libopenshot has a Python API | No | Python API is a real extension point, but the editor is less robust/precise than Shotcut/kdenlive. Secondary. |
| **Flowblade** | Python + GTK + MLT | GPLv3 | Python, but tightly coupled; Linux-focused | No | Linux-leaning, GTK — weak Windows story. Skip. |

**NLE recommendation: adopt Shotcut as the GUI shell, keep MLT as the engine becky already drives.**

## Option C — keep Go engine, switch the hard GUI surfaces to Dear ImGui via cimgui-go (MIT). HONEST VERDICT: viable, but not a clean coexistence with Gio.

- **Is ImGui faster than Gio for DAW widgets?** The speed claim is real but *secondary*. Immediate-mode GUIs are
  performant (Dear ImGui batches to ~10–20 draw calls; uploading a few hundred KB/frame at 60fps is negligible), and
  ImGui's draw model suits a timeline if you **cull aggressively, binary-search the visible range, aim for O(pixels)
  not O(data)**. But the decisive advantage over Gio is **not raw FPS — it's the ecosystem**: ImGui has real DAW-shaped
  prior art (Sequentity) and a far larger example pool. Gio has none. "3–5× faster" is the wrong framing; "there's
  actually example code to copy" is the right one.
- **Sequentity claim — CONFIRMED.** `alanjfs/sequentity` is a genuine single-file (`Sequentity.h`) immediate-mode
  sequencer widget for C++17 / Dear ImGui / EnTT, in the spirit of Ableton/Bitwig/FL clip editing. Real, exists.
  **Caveat:** it's C++ (not Go) and EnTT-coupled — a *design reference*, not a drop-in for cimgui-go. No equivalent
  ready-made sequencer exists for cimgui-go; you'd port the *approach*, not the code.
- **cimgui-go status:** `AllenDang/cimgui-go` is the actively-maintained auto-generated Go binding (MIT), tracking
  current Dear ImGui, default backend **GLFW + OpenGL** (also SDL/Ebiten).
- **The window/backend problem — THIS is the real catch.** Gio on Windows renders with **Direct3D 11**. cimgui-go
  renders with **OpenGL (GLFW)**. They cannot trivially share one window/context. To composite ImGui *inside* a Gio
  window you'd render Gio→offscreen D3D11 texture and bridge it into an OpenGL pass (Gio documents this offscreen-texture
  interop pattern, but it's fiddly and fragile). The clean path is **ImGui owns its own window** (own GLFW/OpenGL
  context), Gio for the HUB and ImGui for the DAW/NLE surfaces — two windows, not one. That undercuts the
  "augment the existing canvas" pitch: Option C effectively *replaces* the heavy surfaces with a separate ImGui app.
- **Net:** C is a legitimate "stay-in-Go-world, get real DAW-widget prior art, embed llama.cpp via cgo" path, and it's
  better than A. But it is more work than B (you still build every widget, just with better references and faster paint),
  and it forces a second window/toolkit. Only pick C if Jordan refuses to live in a C++ codebase.

---

## Specific claims, researched honestly

### 1. In-process llama.cpp vs llama-server HTTP — Jordan's "HTTP is the latency culprit" is **largely WRONG; correct the diagnosis.**
- For a localhost call to an **already-warm** llama-server, HTTP framing + JSON parsing is **~10–15 ms** — utterly dwarfed
  by model inference time (hundreds of ms to seconds). HTTP is **not** the latency culprit for warm requests. (In-process
  libllama only wins measurably for *microsecond*-class, sub-64-token calls — not becky's case.)
- **The real latency source in becky is architectural: cold process-spawn-per-click** (booting a fresh `llama-*.exe`,
  loading the model, re-reading weights every interaction). CLAUDE.md already half-knows this — the GGUF-on-GPU + *warm
  server* work cut a click from ~85 s to ~6 s precisely by **not** reloading per call. The proven fix on becky's side is
  "keep the server warm," not "drop HTTP."
- The genuine wins of native embedding are **architectural, not raw latency**: no subprocess lifecycle to manage,
  immediate token streaming into the GUI, a single distributable binary, no port/health-check plumbing. Real benefits —
  just not the ones Jordan thinks.
- **Go cgo bindings to libllama (maintenance reality, drift risk is real):**
  - `go-skynet/go-llama.cpp` — the classic, but **unmaintained since ~Oct 2023** (drifts hard from current GGUF).
  - `tom/llama-go` — an **actively maintained** fork tracking upstream; thread-safe concurrent inference. Best cgo
    candidate today.
  - `dianlight/gollama.cpp` — **no-cgo** path via purego/libffi (cross-platform). Interesting if avoiding cgo matters.
  - **Drift-risk caveat:** llama.cpp's C API changes frequently; *any* binding lags upstream. If becky needs the newest
    model/quant the day it lands, the warm `llama-server` (which the llama.cpp team ships and updates first) is actually
    the **lower-maintenance** choice than chasing a binding. Don't embed for embedding's sake.

### 2. Embedding a PTY that runs Claude Code with full app context — feasible, and there's a Windows-clean way.
- **Terminal widget options:** for a Qt host, `lxqt/qtermwidget` is the obvious one **but it is BSD/Linux/macOS only —
  no Windows**, so it's wrong for Jordan. The Windows-correct choice is **`kafeg/ptyqt`** (Qt/C++ binding over **WinPTY
  and ConPTY** on Windows, plus Unix PTY). ConPTY is the native Win10+ pseudo-console API (`CreatePseudoConsole`); Go has
  `charmbracelet/x/conpty` if the terminal lives on the Go side instead.
- **The part ACE-Step-DAW did poorly:** dumping a raw terminal in a panel gives the in-terminal agent **no live app
  state**. The right design exposes the host's live state to the agent through a **read/queryable channel** the `claude`
  process can see: write a compact session snapshot to a known file/socket on every edit (becky already does exactly this
  — `dawmodel.Arrangement` JSON + `becky-arrange status --json` introspection), and let the agent read it + drive edits
  back through becky's existing `ctledit`/CLI verbs (its own tool API / MCP). The terminal is just the *transport*; the
  context comes from the snapshot file + tool layer, not from scraping the TUI.
- **Bottom line:** "PTY running `claude` inside the app" is doable on Windows via ptyqt/ConPTY, and becky is unusually
  well-positioned because the state-snapshot + tool-CLI plumbing the agent needs **already exists**.

### 3. Modern open-audio advances a 2024-era review likely missed:
- **VST3 SDK is now MIT (confirmed).** Steinberg released **VST 3.8.0 on 2025-10-20 under the MIT license** (was dual
  GPLv3/proprietary). No agreement with Steinberg needed; free in commercial + non-commercial. CLAUDE.md already notes
  this — correct and load-bearing for any hosting path.
- **ASIO went GPLv3-compatible** in the same 2025 Steinberg relicensing — relevant to becky's low-latency Windows audio
  via the UR12 (matches the GUI-RULES note).
- **CLAP is real and growing (MIT, no licensing agreement ever).** By late 2025: ~430+ plugins and **17+ DAWs/hosts**
  (FL Studio 2024, Bitwig, Ardour, Carla, Qtractor…). First-class multicore host/plugin threading. Any forked host should
  support CLAP alongside VST3 — it's the license-clean future-proof format.
- **JUCE 8** is dual **AGPLv3 / commercial**, with a **free "Starter" tier under ~US$20k/yr gross revenue** (monthly subs
  only above that; no perpetual). Matters because OpenDAW and Tracktion Engine both pull in JUCE — for Jordan's
  non-commercial personal use the AGPL/Starter path is fine, but it's a real constraint if becky-DAW is ever distributed.
- **Tracktion Engine** (the audio core under OpenDAW) is GPL/commercial dual, ~115K LOC, C++20 — a serious real engine,
  not a toy.

---

## Synthesis / recommendation

1. **Stop trying to grow Gio into Cubase (Option A is the trap that ate the weeks).** Keep Gio for the HUB + agent box.
2. **DAW: fork/drive OpenDAW (Option B).** It is becky's strategy already built by someone else — Qt6/Tracktion/JUCE/VST3
   with an AI tool-layer. Run SPIKE-DAW first; the build-on-Windows step is the make-or-break gate.
3. **NLE: adopt Shotcut as the manual GUI shell (Option B), keep driving MLT** (becky already writes it). Run SPIKE-NLE.
4. **Option C (Go + cimgui-go) is the fallback** if Jordan won't own a C++ codebase — better than A, but it's a separate
   ImGui window (D3D11/OpenGL can't trivially co-host), so it *replaces* the heavy surfaces rather than augmenting canvas.
5. **Correct the llama latency model:** the enemy is cold per-click process spawn, not HTTP. Keep the warm server; embed
   libllama only for the architectural wins (single binary, streaming), not for imaginary latency.
6. **The Go forensic/composition CLIs are the toolbox in every option** — not wasted; they become what the in-app agent
   calls.

## Honest uncertainties / what I could NOT verify
- I did not build OpenDAW on Jordan's exact machine — the Qt6.10/MSVC/JUCE/Tracktion build succeeding on his PC is the
  unverified gate (hence SPIKE-DAW). OpenDAW is also young (18 stars, 67 commits) — small, single-author, could stall.
- I did not benchmark ImGui vs Gio on a real 50-track timeline; the "performant" claim is from general IMGUI sources, not
  a becky-specific measurement.
- Shotcut's plugin system is documented for *filters*; whether a full custom *arranger/timeline* panel can be added
  without forking core is not certain — SPIKE-NLE must confirm how far the QML-panel system stretches.
- llama.cpp cgo binding maintenance can change quickly; re-check `tom/llama-go`'s recency before committing to embedding.

## Sources
- Gio: https://github.com/gioui/gio · https://gioui.org/ · GPU/D3D11 interop: https://pkg.go.dev/gioui.org/gpu ·
  https://lists.sr.ht/~eliasnaur/gio (embed Gio with other OpenGL rendering)
- Sequentity: https://github.com/alanjfs/sequentity
- cimgui-go: https://github.com/AllenDang/cimgui-go
- ImGui performance: https://www.forrestthewoods.com/blog/proving-immediate-mode-guis-are-performant/ ·
  https://billydm.github.io/blog/daw-frontend-development-struggles/
- OpenDAW: https://github.com/glenwrhodes/OpenDaw
- Ardour Lua: https://manual.ardour.org/lua-scripting/
- Zrythm: https://www.zrythm.org/ · scripting overview https://manual.zrythm.org/en/scripting/overview.html
- Qtractor: https://qtractor.org/ · LMMS Qt6: https://github.com/LMMS/lmms/issues/6614
- Tracktion Engine: https://github.com/Tracktion/tracktion_engine
- Shotcut plugins: https://www.shotcut.org/notes/make-plugins/ · repo https://github.com/mltframework/shotcut
- kdenlive: https://github.com/KDE/kdenlive · https://kdenlive.org/
- Olive: https://github.com/olive-editor/olive · https://olivevideoeditor.org/
- llama.cpp HTTP overhead: https://markaicode.com/architecture/llamacpp-system-design-architecture-1158/ ·
  server README https://github.com/ggml-org/llama.cpp/blob/master/tools/server/README.md
- Go llama bindings: https://github.com/go-skynet/go-llama.cpp · https://git.tomfos.tr/tom/llama-go ·
  https://github.com/dianlight/gollama.cpp
- VST3 MIT: https://www.steinberg.net/press/2025/vst-3-8/ · ASIO GPLv3 https://librearts.org/2025/11/steinberg-relicenses-vst3-and-asio/
- CLAP: https://u-he.com/community/clap/ · https://cleveraudio.org/
- JUCE 8 licence: https://juce.com/get-juce/ · https://juce.com/legal/juce-8-licence/
- PTY: https://github.com/kafeg/ptyqt · ConPTY https://learn.microsoft.com/en-us/windows/console/creating-a-pseudoconsole-session ·
  QTermWidget https://github.com/lxqt/qtermwidget
