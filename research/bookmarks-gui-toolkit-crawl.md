# Bookmark crawl — DAW-GUI toolkit decision (A=Gio / B=adopt C++ FOSS DAW / C=Dear ImGui via cimgui-go)

Mined from Jordan's curated Chrome bookmarks (`gui-toolkits.tsv` ~104, `languages.tsv` ~114).
Goal: find the projects in HIS OWN bookmarks that resolve "weeks stuck building Cubase-grade
widgets (timeline / piano-roll / mixer / waveform) in Gio, which has zero pre-built DAW widgets."

The three options under decision:
- **A — keep grinding in Gio** (Go immediate-mode GUI; no DAW widgets exist; hand-build all)
- **B — adopt/modify a mature C++/Rust FOSS DAW** and embed an AI agent
- **C — keep the Go engine, switch the hard GUI widgets to Dear ImGui** (via `cimgui-go` / `giu`)

---

## TOP — most decision-relevant (he bookmarked the exact pieces)

### 1. GroGy/im-neo-sequencer  →  **STRONGLY validates Option C**
- URL: https://gitlab.com/GroGy/im-neo-sequencer | C++ / Dear ImGui | **MIT** | ~50 commits, early-dev
- **What it is:** a real, drop-in **ImGui sequencer/timeline widget** (~2,700 LOC across
  `imgui_neo_sequencer.{h,cpp}` + `imgui_neo_internal.*`). Pure "drag the files into your project."
- **Has exactly the DAW-timeline primitives:** `BeginNeoSequencer(current,start,end)` (a scrubbable
  frame range = a timeline + playhead), **`BeginNeoGroup`** (collapsible track FOLDERS — the Cubase
  bus-tree pattern), `BeginNeoTimeline`/`NeoKeyframe` (per-track rows + markers), and flags for
  **multi-select + drag + delete** of keyframes. There is a **C interface** (callable from cimgui-go).
- **Why it matters:** this is the single best proof in his bookmarks that the ImGui-sequencer approach
  is viable — the canonical answer to "ImGui has no DAW widgets." A piano-roll/clip-timeline is the
  same widget with the Y-axis swapped (lanes↔pitches) and keyframes→clips.
- **Honest caveat:** it's an *animation keyframe* sequencer (single-frame keyframes), **not** a
  clip/MIDI-note widget, and the README says "early development, breaking API." So it's a strong
  *reference/launch point* to port into a `becky-piano`/`becky-timeline` ImGui widget, not a finished
  DAW timeline. Pairs with `ImGuizmo::ImSequencer` (CedricGuillemet), also bookmarked (#96).

### 2. AllenDang/giu  →  **the actual Option-C vehicle for becky (Go + Dear ImGui)**
- URL: https://github.com/AllenDang/giu | Go (cgo, on **cimgui-go** + GLFW3.3/OpenGL3.2) | **MIT** |
  ~2.8k★, actively maintained (**v0.15.0, Jun 2026**), Windows 10/11 x64 supported.
- **What it is:** "rapid cross-platform GUI for Go **based on Dear ImGui and the cimgui-go binding**."
  Declarative wrapper; has **`Canvas`** (custom draw-list drawing — required for hand-rendering a
  timeline/piano-roll) and `Plot` (waveform/curve charts).
- **Why it matters:** Option C is "keep the Go engine, do the hard widgets in ImGui" — `giu` is the
  bridge that lets becky stay a Go program while getting ImGui's draw-list. The im-neo-sequencer C API
  (#1) can be called through cimgui-go alongside giu.
- **Cost:** needs a **C/C++ compiler (cgo)** on the build box — Jordan already has mingw/MSVC wired for
  the audio engine, so this is not a new toolchain. This is the genuine trade vs. Gio's pure-Go build.

### 3. emilk/egui  →  **the strongest Option-B-in-Rust / learning reference**
- URL: https://github.com/emilk/egui | Rust immediate-mode GUI | **MIT/Apache-2.0** | ~29.5k★, mature.
- Native + web + game-engine; **custom painting via `epaint`** (AA lines/polys/text = enough to hand-draw
  a timeline/piano-roll/mixer). `eframe` app shell for Win/Mac/Linux. He also bookmarked
  `emilk/eframe_template` (lang #44) and `emilk/egui_plot` (#45, 2D plotting).
- **Why it matters:** if the team would rather grind widgets in a *more mature* immediate-mode toolkit
  than Gio, egui is the obvious target — far bigger ecosystem, same "draw it yourself" model. It's the
  Rust counterpart to the ImGui path; aligns with becky already shipping a **Rust/wgpu video sidecar**.
- **Caveat:** moving the *GUI* to Rust means an FFI seam to the Go engine (becky already has the NDJSON
  seam, so this is consistent), or porting. No off-the-shelf egui DAW widget exists either — still
  hand-built, just on a sturdier base. (Note: `rerun` below is NOT an egui-timeline proof.)

### 4. helgoboss/reaper-rs (+ Cloud-Scythe-Labs/cargo-reaper)  →  **supercharges the EXISTING Option B**
- reaper-rs: https://github.com/helgoboss/reaper-rs | Rust bindings to the REAPER C++ API | **MIT** | ~116★
  | 3 tiers (low/medium/high); medium API "approaching stable". Write REAPER **extension or VST** plugins
  in Rust; exposes SWELL for UI.
- cargo-reaper: https://github.com/Cloud-Scythe-Labs/cargo-reaper | **MIT** | young — a cargo plugin that
  handles the `reaper_` naming + UserPlugins symlink so reaper-rs plugins build cleanly.
- **Why it matters:** becky's CURRENT shipped DAW path **is** "drive REAPER" (`internal/reaper`,
  `becky-reaper`, REAPER 7.69 installed). These let an agent live *inside* REAPER as a native extension
  (real-time callbacks, custom panels) instead of only writing `.rpp` files + ReaScript from outside.
  The most concrete, lowest-risk Option B — it builds on what already works.

### 5. C++ host shell + audio-widget kit (for a from-scratch Option C/B in C++)
- **StudioCherno/Walnut** — https://github.com/StudioCherno/Walnut | C++ Vulkan + Dear ImGui **app
  framework** | **MIT** | ~2.4k★ | **Windows-only**, early. A ready ImGui+GPU window shell to host
  im-neo-sequencer-style widgets if becky ever wanted a native C++ ImGui app (no audio included).
- **steinbergmedia/vstgui** — https://github.com/steinbergmedia/vstgui | C++ | **BSD** | mature (since
  1998, release Oct 2025). Battle-tested **knobs/faders/displays** for audio UIs; reusable for a C++
  mixer/strip. Pairs with `steinbergmedia/vst3sdk` (he bookmarked it; SDK is now permissive) — the same
  VST3 path becky's C++ audio-host already uses.

---

## SECOND TIER — supporting building blocks he bookmarked

**Go GUI (Option A context):**
- `gioui.org` (Gio) — the current toolkit; pure-Go, Direct3D11, no DAW widgets (the problem itself).
- `fyne-io/fyne` — Go GUI, **BSD-3**, ~28.4k★, mature; has a `canvas` module but is Material-widget
  oriented — weaker than Gio for hand-drawn pro-audio surfaces; not a clear win.
- `lxn/walk` — Windows-only native Win32 Go toolkit (standard controls; not for custom timelines).
- `NV404/gova`, `shomali11/gridder` (2D grid lib) — minor.

**Rust media/AI building blocks (Option B-in-Rust):**
- `oddity-ai/video-rs` — ffmpeg-based Rust video read/write, **MIT/Apache**, ~415★, WIP (maintainers
  moving to a successor "rave"); candidate for the NLE/preview engine but test seeking first.
- `rust-av/rust-av` (pure-Rust media toolkit), `zmwangx/rust-ffmpeg` (safe ffmpeg wrapper) — alternates.
- `coupler-rs/vst3-rs` — unsafe Rust VST3 bindings, **MIT/Apache**, ~76★ (raw; reaper-rs is friendlier).
- `vanderlokken/rust-vst-gui` — VST plugin GUIs from Rust (niche).
- `huggingface/candle`, `tracel-ai/burn`, `sonos/tract` — Rust ML runtimes (not GUI; agent-side).

**Local-LLM "agent inside the app" (relevant to all 3 options):**
- `Michael-A-Kuykendall/shimmy` — pure-Rust, OpenAI-compatible **GGUF inference server**, single binary,
  **MIT**, ~5.5k★. Clean **sidecar** pattern for the in-app agent (becky already uses a llama-server
  sidecar on :11435 — shimmy is the Rust-native, no-Python alternative).
- `KolosalAI/Kolosal` + `KolosalAI/inference-personal` — C++ llama.cpp LM-Studio-style local runner (C++
  in-process embedding reference). `rustformers/llm` (unmaintained), `floneum/floneum` (local Rust AI).

**Go music/engine:**
- `go-music-theory/music-theory` — Go Note/Scale/Chord/Key, **GPL-3.0** (license-watch), ~458★;
  reference only — becky already has `internal/musictheory`.

**ImGui ecosystem (Option C tooling):**
- `ocornut/imgui` (core), `CedricGuillemet/ImGuizmo` (incl. **ImSequencer** — a 2nd ImGui timeline
  widget), imgui **Useful-Extensions wiki** (catalog of more widgets), `hoffstadt/DearPyGui` (Python
  ImGui), `ggerganov/imgui-ws` (ImGui over WebSockets), `NullPtrExA/ImGuiDesigner`,
  `Code-Building/ImGuiBuilder`, `dalerank/imlottie`, `CedricGuillemet/imgInspect`, `tseli0s/imfile`.

---

## REST — one-line catalog (nothing curated lost)

### gui-toolkits.tsv (TypeScript / Tauri / GUI / imGui folders) — mostly web-chat UIs, NOT DAW-relevant
- TS agent frameworks: ts-edge, PocketFlow-TS, trigger.dev, mastra, 12-factor-agents, better-chatbot, ai-fun.
- Tauri apps: EcoPaste, clippy, PasteBar, imagenie, VIBE (transcribe), agent-browser, synaptic-flow,
  winfunc/opcode (Claude Code GUI), agmmnn/tauri-controls, tauri-ui, awesome-tauri.
- Web/React UI kits: xyflow (node graphs), assistant-ui, prompt-kit, motion-primitives, deep-chat,
  open-canvas, NextChat, chatbot-ui, HF chat-ui, zola, llum, reui, kokonutui, spectrum-ui, 8bitcn-ui,
  shadcn themes (jln13x, shadcnblocks, ui.shadcn), kokonut, Uiverse, 21st.dev (shader/textarea),
  Motion-Primitives, GetSherlog/Canvas (notebook UI), superdesign, claude-flow-gui, Claudia/Claudiatron.
- Node/desktop: nodegui, neutralinojs, Terminal.Gui (.NET TUI), PowerShell ConsoleGuiTools, Gea, Walnut(↑).
- CSS/util: neumorphism gens, cssbuttons, Vidstack player, handlebars, hygen, nx, inquirer.js.
- iocraft (Rust TUI), Round, floating-ui (svelte), skeleton.

### languages.tsv (Rust / C++ / Go folders)
- Rust agent/infra: rustpbx, rig, AutoAgents, octomind, huly-coder, autogpt(kevin-rs), ractor, just,
  moon, turborepo, rs-graph-llm, microsandbox, terminator (desktop automation), nine, taskter, cto,
  riglr, rustchain, edgelinkd (Node-RED in Rust), mnemoria, Odyssey, my-little-soda, ultrafast-mcp.
- Rust ML/CV/media: candle, burn, tract, kornia-rs, gstreamed_rust_inference, diffusers-rs, runnx,
  mediapipe-rs, parakeet-rs, opencv crate, video-rs(↑), rust-av(↑), rust-ffmpeg(↑).
- Rust input/win automation: rustautogui, enigo, mouse-rs, uiautomation, windows-rs, VirtualDesktopAccessor.
- Rust GUI/egui: egui(↑), eframe_template, egui_plot, rerun (own renderer — NOT an egui-timeline proof).
- Rust audio/DAW: vst3-rs, rust-vst-gui, cargo-reaper(↑), reaper-rs(↑).
- Rust LLM: shimmy(↑), rustformers/llm, floneum, kowalski, inception-core-server, sentience, ccos.
- Rust misc: rust-analyzer, clippy, rusty_hermes, ZeroLaunch-rs, aigitcommit, resilient-rs, rustla2,
  vocechat-server, rusty-chat, AI_TERMINAL.
- C++: nothings/stb, KolosalAI (Kolosal + inference-personal), wxWidgets, **vst3sdk**, **vstgui**(↑).
- Go GUI: gioui.org, fyne(↑), lxn/walk, giu(↑), gova, gridder, FyshOS/tyde (Fyne desktop env).
- Go misc: awesome-go, gowitness, **go-music-theory**(↑), go-prompt, charm (bubbletea/bubbles/
  harmonica/vhs), memos, neva (visual+textual lang), flowbase, slang, uniflow, zenflow, routex,
  air/wgo/refresh/vai/fswatcher (live-reload).

---

## Bottom line for the A/B/C decision
- **His bookmarks lean hardest toward Option C.** He saved the exact validators: a working MIT ImGui
  **sequencer widget** (im-neo-sequencer) + the Go→Dear-ImGui bridge (**giu/cimgui-go**) + the broader
  ImGui widget ecosystem (ImGuizmo/ImSequencer, the Useful-Extensions wiki). The path is: port
  im-neo-sequencer's group/timeline/keyframe pattern into clip/note widgets, call it from Go via giu.
- **Option B has two real flavors in his bookmarks:** (i) *go deeper on REAPER* via reaper-rs +
  cargo-reaper (lowest risk — extends becky's already-working REAPER brain into a native extension), or
  (ii) *rebuild the GUI on egui* (Rust) — most mature immediate-mode base, but still hand-built widgets
  and a Go↔Rust seam.
- **Option A (stay in Gio) has no bookmark support** — there is no Gio DAW-widget project saved; that's
  the gap that's had him stuck. Gio remains only the pure-Go-build advantage.
