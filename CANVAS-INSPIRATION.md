# CANVAS-INSPIRATION.md — design-inspiration brief for becky-canvas

> Research-only. Mined from Jordan's GitHub stars (`crunkkid411`, 400 starred repos
> scanned) plus the three references he named. Goal: steal the interaction patterns
> that make these tools **fluid, icon-first, drag-and-drop, draw-on-canvas, one
> agent box — NOT a wall of text**, and turn them into concrete direction a builder
> can use for becky-canvas (native, Gio / immediate-mode, pure-Go, GPU on his PC).
>
> The hard requirement from `SPEC-BECKY-CANVAS.md` / DAW specs stands: **audio
> editing is VISUAL-FIRST** — waveforms + pitch lanes are the surface, Jordan fixes
> by eye, becky learns from his corrections. Everything below serves that.

---

## 1. Relevant starred repos (the inspo set)

400 stars scanned; ~38 are directly relevant to a canvas/agent/DAW GUI. The most
useful ones, grouped by the pattern they teach:

| Repo | Why it's relevant to becky-canvas |
|---|---|
| **0-AI-UG/cate** *(named)* | Infinite-canvas desktop IDE: float/dock/detach panels, `Cmd+K` palette, drag-to-dock, saved layouts. The whole "spread tools on freeform space" model becky wants. |
| **fal-ai-community/infinite-kanvas** *(named)* | Infinite-canvas image editor: pan/zoom, drag-drop upload, **select-an-image → describe-in-words → AI transforms it in place**, multi-select, undo/redo, viewport culling. The "talk to the thing you selected" loop. |
| **ThioJoe/Thio-Universal-Agent** *(named)* | Visual computer-use agent (observe→think→act on raw pixels). Candidate becky-tool for the **testing** lane + a Gemma-driven "do it on screen" mode. |
| ace-step/ACE-Step-DAW | "AI-native DAW where every instrument knows what came before it" — **LEGO context** sequential generation (drums→bass→guitar→vocals), Strudel track type. Direct cousin of becky-compose + RegenTrack. |
| ariknel/DAW-Copilot | VST3 plugin: **one chat box inside the DAW**, prompt → stems + MIDI you **drag** into tracks. The single agent-box → draggable artifact pattern. |
| betweentwomidnights/gary4juce | Seven open-source AI music models living **inside** the DAW (JUCE). Reference for "models as on-canvas tools, not a separate app." |
| tuneflow/tuneflow | Next-gen DAW with a **plugin = data-model transform** system (plugin only edits the song JSON; DAW applies + redraws). Maps cleanly onto becky's project.json/DAG. |
| tsondo/StemForge | Music → stems → modify → audio-to-MIDI → synth → remix. The visual stem pipeline becky-canvas wants as nodes. |
| mojomast/shoedelussy | Chat-to-compose "semi-DAW" dashboard — chat + DAW surface side by side. |
| ace-step/ACE-Step-1.5, ServeurpersoCom/acestep.cpp, ElWalki/ProdIA_Max (Chord Progression Editor, floating LoRA manager) | Visual chord-progression editing + floating tool panels for a music generator UI. |
| **NickPittas/DirectorsConsole** | **Infinite-canvas Storyboard** + Gallery + Orchestrator for parallel ComfyUI render nodes. Best reference for "infinite canvas as a *production* surface" (not just doodling) with a node/job graph underneath. |
| **AykutSarac/jsoncrack.com** | Turns JSON/YAML into **interactive node graphs**. becky's project.json / harness workflow / VST chain → live graph, almost for free conceptually. |
| **toeverything/blocksuite** | Dual-mode editor framework: `PageEditor` (doc) + `EdgelessEditor` (infinite canvas) sharing one data model. The "same content, two views (list vs canvas)" idea. |
| LingyiChen-AI/AIComicBuilder, waooAI/waoowaoo | Storyboard/timeline canvases for AI media — node→clip composition patterns. |
| **Mdeux25/agentwatch** | "IDE where the AI is the developer, human is the **director**" — live 3D map of the agent working. The watch-and-steer ethos for becky's agent box + Omnigent share-URL. |
| daggerhashimoto/openclaw-nerve | Real-time web cockpit for an agent: voice, **kanban the agent fills in**, file control, sub-agent sessions, inline charts. "Mission control for an agent." |
| zarazhangrui/tab-out | New-tab page as a **mission-control dashboard**: cards grouped by domain, swoosh+confetti on action, 100% local, no server. Tactile micro-interactions + zero-backend. |
| mudrii/openclaw-dashboard, getpaseo/paseo, almogdepaz/wolfpack | Zero-dependency / desktop+mobile **command centers** for agents — layout + "watch from phone" precedent (ties to Omnigent share-URL). |
| zdenham/anvil, BloopAI/vibe-kanban, openai/symphony | Visual **parallel-agent** boards (worktrees/cards/runs) — for becky-harness/Omnigent run views. |
| JanaSundar/luzo | "Design API workflows like a **flowchart**, debug them like a **timeline**." Two-view (graph + timeline) model = perfect for harness workflows + DAW transport. |
| FellouAI/eko, ag-ui-protocol/ag-ui, BuilderIO/agent-native, supercorp-ai/superinterface | Agent↔UI interaction protocols / agent-native app frameworks — vocabulary for "the agent manipulates the canvas." |
| **ocornut/imgui** | Dear ImGui — the canonical **immediate-mode** GUI. Confirms IMGUI viability and gives the mental model Gio shares. |
| **Raais/ImStudio** | Visual **layout designer for Dear ImGui** (drag widgets onto a canvas). Proof that drag-to-place panels work in immediate mode. |
| **CedricGuillemet/ImGuizmo** | Immediate-mode on-canvas **gizmo** (move/rotate/scale handles). Directly portable to "selection handles on a clip/region" in Gio. |
| gui-cs/Terminal.Gui, charmbracelet/bubbletea | TUI toolkits — fallback/where-NOT-to-go; reminds us becky-canvas should be *graphical*, not another TUI. |
| wretcher207/dead-pixel-design, ReaTeam/Extensions, **conormkelly/reamo** | REAPER scripting + a **phone/tablet remote** for a DAW (mixer/timeline/MIDI over WebSocket). Remote-control + scriptable-surface precedent. |
| **wtfazz/cubase-tools** | Cubase score editing / VST expression / audio alignment — direct Cubase-parity feature inventory. |
| sudara/awesome-juce, juce-framework/JUCE(+tutorials) | The audio-UI canon (waveforms, meters, plugin hosting). Where to crib widget behavior. |
| tubone24/midi-agent-skill | Text → MIDI as an agent skill — the INPUT shape for becky-compose/becky-hum, driven from one box. |
| ahmadawais/excalidraw-cli, excalidraw skill | Hand-drawn diagram / draw-on-canvas vocabulary (freeform sketch → structured element). |
| uiverse-io/galaxy | Large free UI component/animation library — source for tactile button/hover/active states. |
| huggingface/meshgen, purzbeats/gen-art-playground | "AI agent lives inside a creative canvas tool (Blender / p5+three)" precedent. |

(Stars that are pure CLIs, model weights, OSINT, OpenClaw/agent-router clones,
awesome-lists, or unrelated infra were skipped — not design inspo.)

---

## 2. The three named references — takeaways

### A. `0-AI-UG/cate` — infinite-canvas desktop IDE
**What it is:** an Electron IDE built on an infinite canvas. Editors, terminals,
browsers, docs, and AI agents are **panels** you spread across freeform space
instead of stacking tabs. Float them, dock them into tabs/splits across four
zones, or detach into OS windows; layout is **saved per workspace** and restored.
Embedded coding agent is Pi (same `@earendil-works/pi` becky's harness targets).

**Steal this:**
- **Infinite canvas as the home surface.** Zoom/pan, zoom-to-fit, zoom-to-selection,
  one-key **auto-layout**, a minimap. Panels, not windows.
- **Drag-to-dock.** Drag a panel onto a dock zone to make tabs/splits; drag off to
  float. No config files — "open a folder → it's a workspace."
- **Command palette (`Cmd/Ctrl+K`)** as the keyboard escape hatch *alongside* the
  mouse-first surface (Jordan is mouse/visual, but a palette costs little).
- **Per-panel model memory + multi-provider** (Anthropic/OpenAI/Gemini/…): becky
  should let each agent panel remember its model; Gemma-4 stays the local default.
- **Saved/restored layouts** are load-bearing for a non-dev: he arranges once, it
  comes back exactly. Make layout persistence a day-1 feature, not polish.

**Caveat:** Cate gets free panels (Monaco, xterm, browser) from web tech. In Gio
those are bespoke. Adopt the *canvas + docking + palette* model; don't try to clone
a browser/terminal panel early.

### B. `fal-ai-community/infinite-kanvas` — infinite-canvas image editor + AI
**What it is:** React-Konva infinite canvas. Drag-drop images, pan/zoom,
multi-select, undo/redo, auto-save to IndexedDB. The killer loop: **select an
object → describe it in plain words → AI transforms it in place**, streaming the
result onto the canvas live (style transfer, background removal, "isolate the red
car" via natural-language segmentation). Viewport culling renders only visible
items; large images resized before upload.

**Steal this:**
- **Selection → natural-language action is THE interaction.** Click a thing on the
  canvas, type a short instruction about *that thing*, watch it change. For becky:
  select a vocal region → "tune this to key + tighten timing"; select a clip → "make
  this the breakdown"; select a frame → "who is this / OCR this". This is the
  antidote to a wall of text — the agent box is **scoped to the selection**.
- **Stream results back onto the canvas** (progress in place), don't pop a modal.
- **Drag-and-drop ingest** (audio/video/MIDI/image dropped → becomes a node).
- **Local persistence + undo/redo** as first-class (IndexedDB → for becky, a local
  project file). Auto-save with debounce.
- **Viewport culling** — only draw visible tracks/clips. Essential for a Gio
  immediate-mode canvas at 500GB-evidence scale.

### C. `ThioJoe/Thio-Universal-Agent` — visual computer-use agent
**What it is:** a single portable Windows `.exe` (zero deps, .NET only) that drives
*any* app by **looking at raw pixels and sending real mouse/keyboard input**
(observe→think→act loop). Defaults to **Human-Control-Only mode**: it draws
crosshairs/boxes and tells you where to click *without* taking over — and can flip
to autonomous. Multi-provider (Gemini default, OpenAI, **Claude**, **local ONNX**);
queued multi-action steps; global pause/stop hotkeys; live mid-run redirection;
~3k tokens/step. Source-available, **personal-use-only license** (not OSS, not
commercial).

**Steal this (UX):**
- **"Show me, don't do it" mode** — overlay crosshairs/target boxes + a copyable
  suggestion instead of acting. Perfect for a non-dev: becky can *point* before it
  *does*. Map to: highlight the region/control on becky-canvas it's about to touch.
- **Live mid-run steering + global pause/stop.** The user stays in the loop.
- **Coordinate-accurate clicking via vision** at 4K — validates Jordan's
  browser-harness "screenshot → click_at_xy" instinct as a general control method.

**See §5 for the becky-tool fit.**

---

## 3. Patterns to adopt in becky-canvas — prioritized (most impactful first)

### P1 — Selection-scoped agent box (the anti-wall-of-text core)
One persistent, small **"talk to it" box**. Its meaning is **whatever is selected**:
no selection → talk to the project; a clip/region/track/frame selected → the
instruction applies to *that*. Results render **in place** on the canvas, streaming,
with undo. (From infinite-kanvas; reinforced by DAW-Copilot's single in-DAW chat.)
Realistic in Gio: a text input + a "current selection" model + immediate-mode redraw
of the affected node. **Build this first — it defines the whole product.**

### P2 — Infinite canvas + dockable, draggable panels with saved layouts
Pan/zoom/zoom-to-fit/auto-layout/minimap; tools are **panels** you drag, dock,
float; **layout persists per project**. (From cate; ImStudio proves drag-to-place
in immediate mode; ImGuizmo proves on-canvas handles.) Realistic in Gio: a
world-space transform (offset+scale), hit-testing in world coords, a small docking
state machine. Reuse Jordan's "coordinate clicks pass through everything" instinct.

### P3 — Everything-is-a-node visual graph (workflows, stem pipeline, VST chains)
Render `project.json` / harness workflows / VST plugin chains as a **node graph**:
boxes = tools/tracks/plugins, wires = routing/order, drag to reconnect. Two views of
the *same* data: **graph** (wiring) and **timeline** (transport) — toggle, don't
duplicate. (From jsoncrack = JSON→graph, luzo = flowchart+timeline, tuneflow =
plugin-as-data-transform, blocksuite = page/edgeless dual view, DirectorsConsole =
canvas over a render-node graph.) Realistic in Gio: nodes are rects, wires are
bezier paths, drag = update the JSON then redraw. Determinism stays in the JSON.

### P4 — Visual-first audio with on-canvas direct manipulation + gizmos
Waveform tracks + **pitch lanes** are the surface; Jordan drags/edits **by eye**;
becky **learns from his corrections** (the DAW-spec hard requirement). Selection
handles/region edges = ImGuizmo-style on-canvas gizmos. **LEGO/context-aware**
regeneration (ACE-Step-DAW, RegenTrack): right-click a track → "regenerate, aware of
everything around it." Prompt → **draggable** stems/MIDI artifact (DAW-Copilot).
Realistic in Gio: waveforms/meters are custom-drawn (crib JUCE behavior); GPU on his
3070 handles it. The audio engine itself is the separate DAW-engine spec.

### P5 — Icon/shape/color language + tactile states over text
Dock of **icons** for modes (ask / video / drum / piano-roll / mix / hum / vox);
**color + shape** carry meaning (track type, corroborated vs candidate, kick/bass/
guitar buses) instead of labels; designed **hover/focus/active** states with a small
sound/animation reward on actions (tab-out's swoosh+confetti; uiverse for states).
A **"director" stance**: the agent acts, Jordan watches/steers, with **show-me-first**
overlays + global pause/stop (Thio, agentwatch, openclaw-nerve). Realistic in Gio:
immediate-mode is *good* at per-frame hover/press visuals; ship an SVG/icon set early.

> **Gio / immediate-mode reality check (carry through all five):** infinite canvas,
> drag-drop, node graphs, gizmos, waveforms, hover/press feedback, and a pan/zoom
> world transform are all **squarely realistic** in a pure-Go immediate-mode window
> (Dear ImGui + ImStudio + ImGuizmo are existence proofs; Gio shares the model).
> **Web-only / out of scope for the native window:** embedded Monaco/xterm/Chromium
> panels (cate gets these free from Electron — don't), HTML-in-canvas, IndexedDB
> (use a local project file), and React/Konva-specific bits. The Omnigent
> **share-URL Jordan watches from his iPhone** is the one piece that is legitimately
> web (a viewer/stream of the native session), not part of the Gio window itself.

---

## 4. Quick "what each pattern maps to in becky" cheat-sheet

- Agent box → scoped to selection; streams onto canvas (P1)
- Project / harness workflow / VST chain → node graph + timeline twin-view (P3)
- becky-compose / becky-hum → text/hum → draggable MIDI+stems artifact (P4)
- becky-ask / becky-ocr / face-ID → select a frame/region → ask about *it* (P1)
- becky-harness / becky-omni runs → parallel-agent board + director/watch stance, phone share-URL (P5)
- Mix (JST) buses → color/shape-coded nodes, sidechain wires drawn on canvas (P3/P5)

---

## 5. Thio-Universal-Agent as a possible becky-tool — read

**Fit: promising for the testing lane and a "show-me" control mode; with caveats.**

- **Testing lane (Jordan's instinct is right):** it's a ready, vision-only GUI driver
  that needs no per-app integration — useful to **smoke-test becky-canvas itself** by
  having an agent click through the real window, complementary to Jordan's existing
  browser-harness (which is Chrome/CDP-only; Thio covers the *native* window CDP
  can't reach).
- **becky alignment:** it already supports **Claude and local ONNX** providers and is
  built on a screenshot→coordinate-click loop — the *same* method as Jordan's harness.
  becky integrates Gemma-4 for audio; Thio is **Gemini-tuned and image-only**, so it
  doesn't reuse Gemma directly, but the *pattern* (local VLM picks pixel coords →
  native input) maps onto becky's planned **LFM2.5-VL** vision backends. A small
  pure-Go "observe→think→act on the native window" helper is more on-brand than
  adopting Thio wholesale.
- **The "Human-Control-Only / show-me" overlay is the most valuable idea to port** —
  it's exactly the non-dev-friendly "point before you act" UX P5 calls for.
- **Blockers to adopting the code as-is:** (1) **License is personal-use-only,
  source-available — NOT open-source/commercial**; can't be vendored into becky-tools.
  (2) It's **C#/.NET 10**, off becky's Go+thin-Python stack. (3) It executes
  **unauthenticated OS-level input** ("prototype, not for production") — clashes with
  becky's offline-deterministic, degrade-never-crash invariants unless sandboxed.
- **Recommendation:** treat it as a **reference design**, not a dependency. Port the
  *show-me overlay* + *observe→think→act loop* into a tiny Go/Gio control mode (or a
  `becky-*` helper) driven by becky's own local VLM; keep it opt-in, supervised, and
  off the deterministic forensic path. If a turnkey native-app test driver is wanted
  fast, run Thio **externally** as a black-box tester — never check its code in.

---

*Sources: Jordan's 400 GitHub stars (`crunkkid411`) + READMEs of cate,
infinite-kanvas, Thio-Universal-Agent, ACE-Step-DAW, DAW-Copilot, tuneflow,
DirectorsConsole, tab-out, agentwatch, blocksuite. Design direction only — no app
code written.*
