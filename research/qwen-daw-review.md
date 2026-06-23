# Adversarial Review: `daw-video-timeline-gui-components.md`

The doc is well-researched and the survey of the landscape is solid. But I see several structural blind spots and claims that don't hold up under pressure.

---

## 1. "They're all the same widget" is the doc's load-bearing claim — and it's wrong

The one-line synthesis says the arranger, drum grid, and video NLE timeline are "all the same widget — a horizontal time-scaled lane stack with clips you drag/resize/snap." This is a **dangerous oversimplification** that will bite during implementation.

**Video NLE vs audio arranger are NOT the same widget:**
- Video has **frame-rate-locked quantization** (24/25/30/60fps). Audio DAWs quantize to musical time (beats/bars, tempo map). The coordinate systems, snap rules, and rounding logic are fundamentally different. A clip edge at frame 71623 in a 29.97fps timeline is a *different kind of number* than a clip ending at beat 16.5.
- **Ripple/roll/slip/trim** (the doc explicitly calls these out as Shotcut's "pro editing verbs") have no audio-arranger equivalent. A ripple edit shifts all downstream clips — that's a mutation the audio arranger's data model doesn't need. You can't "build one widget, then specialize" when the *verbs* are different.
- **Thumbnail generation** for video is a rendering pipeline problem (async frame extraction, cache invalidation) that audio waveforms solve differently (peaks are cheap and deterministic).

**Drum grid vs piano roll are NOT the same widget either**, and the doc actually catches itself:
> "the grid is a piano-roll with lanes, not a new widget. **Bar-paging + velocity-per-step are the gaps.**"

Those "gaps" are the entire interaction model. A step sequencer is **click-to-toggle with fixed-length steps**. A piano roll is **click-and-drag-to-create with variable-length notes**. The input grammar, hit-testing, and visual affordances diverge at the most basic interaction level. Calling the drum grid a "special case" of the piano roll means you'll build the piano roll first, try to reuse it, and discover the drum grid needs a different tool.

**Verdict:** This claim is doing heavy rhetorical work to justify building one component. In practice you'll end up with 2-3 genuinely distinct widgets, not one parameterized one.

---

## 2. The license strategy is legally naive

The doc repeatedly says "read for ideas; don't copy code" for GPLv3/AGPLv3 projects (Shotcut, Zrythm, LMMS). This is the **clean-room defense**, and it only works if you *actually do a clean room* — meaning the person who reads the GPL code is a different person from the one who writes the Gio implementation, with a spec document in between.

What the doc actually recommends is: one developer reads Shotcut's `timeline.qml`, internalizes its structure, then writes Gio code that "ports the interaction model." That's **not clean-room** — it's derivative work with a thin procedural fig leaf. If anyone ever litigates (unlikely for a personal project, but becky could grow), the "I read the GPL code and then wrote my own version of the same ideas" defense is weak.

**The fix:** either (a) actually do a two-person clean-room with a written spec, (b) only port from MIT/BSD/Apache projects (react-timeline-editor is fine), or (c) accept the GPL taint and license the resulting widget GPL too. The doc should pick one explicitly instead of hand-waving.

---

## 3. Gio's retained-mode reality is underexamined

The doc treats Gio as a neutral canvas: "Render with Gio's `op`/`clip`/`paint` primitives." But Gio is **immediate-mode** — widgets don't retain state between frames. Every frame, you rebuild the entire widget tree. This has massive implications for timeline widgets that the doc never addresses:

- **Persistent selection state** (which clips are selected, marquee selection rectangles, multi-select with shift/ctrl) must live *outside* Gio's widget system. Where? The doc doesn't say.
- **Virtual scrolling** — a song with 200 tracks × 500 clips = 100,000 drawable elements per frame. Gio doesn't virtualize. You'll need to cull off-screen clips yourself or watch frame rate crater.
- **Hit-testing for drag/resize** — Gio's `pointer.InputOp` is primitive compared to what a timeline needs (edge-detect for resize vs body-detect for move, cursor changes, drag thresholds). Every "port the drag mechanics from react-timeline-editor" recommendation silently assumes a hit-testing framework that Gio doesn't provide.
- **Text rendering** — track names, time labels, BPM markers. Gio's text is functional but expensive for dense label layouts.

**Verdict:** the doc surveys *what to build* without honestly assessing *how hard it is in Gio specifically*. A "Gio feasibility" section would have caught this.

---

## 4. signal's WebGL advantage is load-bearing and ignored

The doc recommends porting ryohey/signal's piano roll — but signal renders its piano roll with **WebGL** (the doc even notes this in the table). That's not incidental; it's why signal can draw thousands of notes at 60fps with smooth zoom. Gio's 2D pipeline (GPU-accelerated but not raw WebGL) may not match this for dense note data.

The risk: you port signal's interaction model faithfully, and it *feels slow* because the rendering substrate is different. The doc should flag this as a performance risk and propose mitigation (e.g., tile-based rendering, dirty-rect invalidation).

---

## 5. Missing: the editing-grammar layer

The doc covers *visual surfaces* (what the widgets look like) but completely ignores the **command/editing layer** that sits on top:

- **Undo/redo** — every drag, resize, trim, and delete must be reversible. This is not a widget concern; it's an architectural one. The doc never mentions it.
- **Clipboard** — cut/copy/paste clips, notes, mixer settings. Cross-surface (copy from arranger, paste into piano roll).
- **Keyboard shortcuts** — DAWs live and die by shortcut density. The doc doesn't address how shortcuts map onto Gio's key event system or conflict resolution.
- **Context menus** — right-click on a clip → 15 options. Gio doesn't have a native context menu widget.

These are not "details to figure out later" — they're **architectural requirements** that shape the widget design. A mixer strip designed without undo in mind will need to be rewritten when undo is bolted on.

---

## 6. Missing references that should be here

- **Ardour** (C++/GTK, GPLv2) — has one of the most mature FOSS timeline implementations. Its region/clip model is more relevant than Zrythm's for the arranger surface. Skipping it is a gap.
- **REAPER** — proprietary, but its [action list and ReaScript API](https://www.reaper.fm/sdk/reascript/reascript.php) document the *editing grammar* better than any FOSS project. Essential for shortcut/verb design.
- **Tracktion/TracktionEngine** — JUCE-based, and while the engine is proprietary, its [documentation](https://www.tracktion.com/develop/tracktion-engine) describes a clean separation of edit model ↔ GUI that maps directly to what becky needs.
- **Aubio / Rubber Band** — for the waveform surface, these handle transient detection and time-stretching, which affect how waveform clips *behave* when resized (not just how they look).

---

## 7. Smaller issues

| Claim | Problem |
|---|---|
| "peaks.js is LGPL-3.0, study UX only" | peaks.js's *API surface* (zoom levels, segment markers, keyboard nav) is what you'd port. LGPL-3.0 §5 "Modification" requires that modified versions be LGPL too. If you mirror the API shape closely, is that a "modification"? Unclear. |
| "ptcollab (C++/Qt, verify)" | The doc says "verify" for the license but doesn't actually verify it. It's GPLv3. |
| react-timeline-editor "~600★" | Star count from when? It's at ~1.4k now (if the same repo). Stale data. |
| "becky already has `gui_drumpanel.go`" and "`gui_mixerpanel.go`" | The doc references existing becky files but never assesses how far along they are or what they're missing. Are these stubs or functional panels? This matters for the port-vs-extend decision. |

---

## Where I agree

The core thesis — **"there is no reusable Go/Gio DAW widget, so port proven foreign interaction models"** — is correct and well-supported. The Go ecosystem survey is thorough and the conclusion is honest. The per-surface table is a good decision matrix. Choosing signal (MIT) for piano roll is the right call. The peaks.js recommendation for waveform UX is solid.

The doc's *survey* is strong. Its *synthesis* (one widget, license hand-waving, Gio feasibility) is where the gaps live.

---
---

# First-Principles Analysis: Should becky be all-Go?

**Date:** 2026-06-22 · **Author:** Qwen Code · **Context:** After reading the entire project (157K lines of Go across 818 files, 21 research documents, full handoff history, all architectural decisions). This is an honest, adversarial take on the language/stack question, written for Jordan directly.

---

## The question you're actually asking

You said: *"this project as a whole is starting to feel a bit dogmatic and I just want it to work, not adhere to rules that make it worse."*

That's the right instinct. Let me separate what's dogma from what's load-bearing.

---

## 1. Go is right for 80% of what becky does. Here's the honest breakdown.

### Where Go is genuinely excellent (keep it, no debate)

| What | Why Go wins |
|---|---|
| **73 CLI forensic tools** | Go's killer feature: one binary, cross-platform, fast compile, simple concurrency. These tools are done and working. |
| **`internal/dawmodel` session model** | Clean structs, JSON serialization, immutability via copy-on-write. Go's type system is fine here. |
| **`internal/ctledit` / `internal/ctlmodel`** | The AI edit pipeline — pure logic, no GUI, no realtime. Textbook Go. |
| **`internal/beatgen` / `internal/arrange`** | Deterministic music composition — pure math on structs. Go excels. |
| **NDJSON seam / sidecar protocol** | NDJSON over stdio is language-neutral. The Go side is clean and testable. |
| **LLM/TTS integration** | `internal/llmlocal`, `internal/tts` — HTTP clients, process management. Go's stdlib is perfect. |
| **`internal/reaper` / `internal/kdenlive`** | File format writers. Go is great for this. |

**Bottom line:** The engine, the AI brain, the session model, the CLI tools — Go is the right call. These are ~120K of the 157K lines. Nobody should suggest rewriting them.

### Where Go is adequate but not ideal

| What | The friction |
|---|---|
| **`internal/audioengine` sampler/synth** | The DSP logic works, but the audio callback had to drop to C (`miniaudio_impl.c`) because Go's GC pauses are incompatible with realtime audio. This is a known, accepted cost — the C bridge is 4 files. Fine. |
| **`internal/drummachine` model** | The model is clean Go. The audio rendering path goes through the C bridge. No complaint. |

### Where Go is genuinely the wrong tool (and the project knows it)

| What | The reality |
|---|---|
| **VST3 plugin hosting** | The VST3 SDK *is* C++. The project already ratified a C++ sidecar for this. Correct decision. |
| **ASIO low-latency audio** | The ASIO SDK *is* C++. Same sidecar. Correct. |
| **Video preview / scrubbing** | Frame-accurate GPU video decoding needs wgpu or equivalent. Rust sidecar is planned. Correct. |

**So the polyglot architecture (Go + C++ + Rust) is already ratified and correct.** The project's own `GUI-RULES.md` already acknowledges that forcing one language makes two of three surfaces worse. Good.

### Where Go is the problem and nobody wants to say it

| What | The honest problem |
|---|---|
| **The entire GUI widget layer** | Gio is immediate-mode with no complex widget ecosystem. Building DAW-grade timeline, piano roll, mixer, and waveform widgets from scratch in Gio is a **multi-year effort** that no amount of "porting the interaction model" shortcuts. You still have to implement every pixel of hit-testing, drag thresholds, selection state, virtual scrolling, context menus, and text layout yourself. The existing panels (`gui_drumpanel.go` at 450 lines, `gui_pianopanel.go` at 594 lines) are functional but thin — they prove the approach works for simple surfaces but don't prove it scales to pro-grade DAW complexity. |

---

## 2. The GUI question from first principles

Here's the question the project keeps dancing around: **is Gio the right toolkit for the hardest 20% of the UI?**

### What Gio gives you

- Pure Go, no cgo on Windows
- Direct3D 11 native rendering
- Immediate-mode architecture (stateless per frame)
- MIT/Unlicense
- `--render-frame` headless verification
- ~2.2k stars, small but active community

### What Gio does NOT give you

- **Complex widgets.** No timeline, no piano roll, no mixer strip, no waveform viewer. You build every one from `clip`+`paint` primitives.
- **Retained selection state.** Multi-select, marquee, shift-click — all must live outside Gio's widget system in your own state management.
- **Virtual scrolling.** A 200-track arrangement with hundreds of clips per track = tens of thousands of draw calls per frame. Gio doesn't cull.
- **Hit-testing framework.** Edge-detect for resize vs body-detect for move, cursor changes, drag thresholds — you implement all of it.
- **Context menus.** Gio doesn't have one. Every DAW has right-click menus with 15+ options per surface.
- **Text layout at density.** Track names, time labels, BPM markers, parameter values — Gio's text is functional but expensive for the label density a DAW needs.
- **Community widgets.** The Gio plugin ecosystem (`gio-plugins`, `gio-v`, `giox`) has buttons, lists, and inputs. Nothing timeline-shaped.

### The comparison that matters

| Capability | Gio (Go) | Qt (C++) | JUCE (C++) | Web (TS/React) |
|---|---|---|---|---|
| Timeline widget | **Build from scratch** | qml Timeline exists | Tracktion has one | react-timeline-editor (MIT) |
| Piano roll widget | **Build from scratch** | None good | MIDI editor built-in | ryohey/signal (MIT) |
| Mixer strip widget | **Build from scratch** | Qt widgets | Mixer built-in | Many options |
| Drag/resize/snap | **Build from scratch** | QDrag + custom | Built-in | HTML5 DnD + libraries |
| Context menus | **Build from scratch** | QMenu | PopupMenu | Native |
| Virtual scrolling | **Build from scratch** | QListView | ListBox | react-virtualized |
| Build complexity | `go build` | CMake + Qt SDK | CMake + JUCE | npm + bundler |
| cgo required | **No** | Yes (it IS C++) | Yes (it IS C++) | No (browser) |

The table is stark. Every other ecosystem has pre-built DAW widgets. Gio has none. The "port the interaction model" strategy means you study the foreign widget's behavior and then **rebuild it from scratch in Gio** — which is still building it from scratch, just with better specs.

### The cost nobody's counting

Building a pro-grade timeline widget (drag, resize, snap, multi-select, zoom, scroll, virtualize, hit-test, context menu, keyboard shortcuts, undo integration) from scratch in an immediate-mode toolkit is approximately **3,000-5,000 lines of carefully engineered code per surface**. You need at least 3 surfaces (arranger, piano roll, drum grid), plus the mixer strip and waveform viewer. That's **15,000-25,000 lines of pure GUI widget code** — all state management, hit-testing, and rendering — before any of it sounds good or feels responsive.

The existing 4 panels total ~2,000 lines and they're thin. The gap is 10x what's been built.

---

## 3. Licensing explained plainly

You said you don't know the difference. Here it is, no jargon:

### The license spectrum

| License | What it means for you | Examples |
|---|---|---|
| **MIT / BSD / Apache-2.0** | Do whatever you want. Use it, modify it, sell it, keep it private. Just keep the copyright notice. | Gio, Go, react-timeline-editor, ryohey/signal, PortAudio, VST3 SDK (since Nov 2025) |
| **LGPL-3.0** | You can *link to* the library in your own code (even proprietary). But if you modify the library itself, those modifications must be shared. Think of it as: "use it, don't change it." | peaks.js |
| **GPL-3.0** | If you use GPL code in your project (link to it, copy from it, derive from it), your **entire project** must also be GPL-3.0. It's viral — it infects everything it touches. | Shotcut, LMMS, Helio, Ardour, OpenDaw, ptcollab |
| **AGPL-3.0** | GPL-3.0 + "if you run it on a server and users interact with it over a network, that counts as distribution." The most viral. Closes the "SaaS loophole." | Zrythm, JUCE |

### What this means for becky specifically

**For a personal tool you never distribute: licensing is almost irrelevant.** GPL, AGPL, whatever — nobody is suing you over software that lives on your laptop. The "study for ideas, don't copy code" approach is practically fine for personal use.

**If becky ever becomes a product or you share it:**
- Any GPL code you studied and replicated means becky must be GPL too
- Any AGPL code means the same, plus network-use obligations
- MIT/BSD/Apache code is safe to use directly

**The practical rule:** stick to MIT/BSD/Apache for anything you directly use or closely replicate. Study GPL projects for ideas (that's legal and normal). The current doc's approach of "read Shotcut for ideas, write your own Gio code" is fine for personal use and fine for distribution as long as you're genuinely writing original code, not translating QML line-by-line.

**JUCE specifically:** AGPL-3.0 or ~$2K/year commercial license. For a personal tool, AGPL doesn't trigger (you're not distributing). But adopting JUCE would mean becky's audio layer is AGPL forever, which limits future options. The current plan (C++ sidecar with raw MIT VST3 SDK, reading JUCE only as reference) is the right call.

---

## 4. The three dogmas (and which ones to break)

I see three rules the project treats as non-negotiable that deserve honest pressure-testing:

### Dogma 1: "No embedded browser engines, ever. Native pixels only."

**Where this came from:** WebView2 caused lag and fragility in the old becky-clip. Trauma response. Understandable.

**Why it might be wrong now:** The "no browser" rule was a reaction to a *bad implementation* (WebView2 embedded as the entire app shell), not a fundamental flaw in web rendering. A targeted, embedded web view for ONE complex widget (the timeline) inside a native Gio shell is architecturally different from "the whole app is a browser."

**The honest question:** Would you rather spend 2 years hand-building timeline widgets in Gio, or embed a proven MIT-licensed React timeline widget in a small web panel within the Gio window? The web panel would be ~100 lines of integration code, not 5,000 lines of from-scratch widget engineering.

**My take:** The rule made sense as a course-correction after the WebView2 disaster. But applied universally, it's costing you the richest widget ecosystem on the planet (the web) for the hardest 20% of the UI. The NDJSON seam already exists — a web panel reading the same seam as the Gio panels is architecturally clean. This is worth reconsidering, not as a full pivot but as a targeted escape hatch for the timeline surface specifically.

**Counter-argument I'll make against myself:** WebView2 on Windows has real problems — memory overhead (~40-80MB), process management complexity, and the lag you experienced may recur. If the timeline panel is the only web view, the overhead might be acceptable. But it's a second rendering pipeline to maintain.

### Dogma 2: "becky-canvas (Go + Gio) IS the tool. Everything lives inside it."

**This one is correct.** I'm not challenging it. One window, shared state, no launching separate programs. The north star is right.

**But the implementation path is wrong.** "Everything lives inside it" doesn't mean "everything is rendered by Gio." It means everything shares one `dawmodel.Arrangement` and one window. The rendering technology for each panel is an implementation detail — the seam (§2 of GUI-RULES.md) already makes this true. A web-rendered timeline panel inside the Gio window, reading from the same NDJSON seam, IS "inside the canvas." It's the same app.

### Dogma 3: "Port the interaction model, don't adopt the code."

**Where this came from:** Licensing concerns + the instinct to keep everything native Go.

**Why it's the most expensive dogma:** "Port the interaction model" is a euphemism for "rebuild from scratch after studying someone else's working product." For the piano roll, that's ~3,000 lines. For the timeline, another ~5,000. For the mixer strip, ~2,000. That's **10,000+ lines of from-scratch GUI code** — the hardest, most iteration-dependent code in the project — written in the toolkit with the LEAST existing DAW widget infrastructure.

**The alternative nobody's proposing:** `cimgui-go` (MIT) wraps Dear ImGui, which has `ImDrawList` for custom drawing AND a mature docking/layout system. The Sequentity project proves a full sequencer in ~1,000 lines of ImGui. Yes, it requires cgo (mingw-w64). Yes, it uses OpenGL instead of D3D11. But the build complexity is a one-time setup cost, and the widget development speed is 3-5x faster than raw Gio for this specific class of work.

**My take:** The "port don't adopt" instinct is right for the engine (where Go excels). For the GUI widgets (where Gio has nothing), it's leaving enormous leverage on the table. The project already accepts cgo for the audio engine — accepting it for a Dear ImGui panel within the same window is not a philosophical leap, it's a practical one.

---

## 5. What C++ / Rust / other languages would actually change

### If you rewrote the GUI in C++ (Qt or JUCE)

**What you'd gain:**
- Every DAW widget already exists (Qt: QML timeline, JUCE: audio plugin editor, mixer components)
- Industry-standard audio GUI toolkit with decades of polish
- JUCE's `AudioProcessorEditor` is literally designed for what becky needs
- Qt's declarative QML makes complex interactive surfaces 3-5x faster to develop

**What you'd lose:**
- 157K lines of Go engine code would need to be called from C++ (possible but ugly — Go↔C FFI is painful)
- Cloud build/verify breaks — C++ needs a full toolchain everywhere
- `--render-frame` headless testing goes away (Gio's unique advantage)
- The "degrade, never crash" philosophy is harder in C++ (exceptions, UB, segfaults)
- Development speed for the engine/logic layer drops significantly
- Jordan's existing workflow with the AI agents (who all write Go) breaks

**Verdict:** The GUI would be better. Everything else would be worse. Not worth the full rewrite.

### If you rewrote the GUI in Rust (iced, slint, or egui)

**What you'd gain:**
- Memory safety without GC (no audio callback concerns)
- `egui` is immediate-mode like Gio but has more community widgets
- `slint` has a declarative UI language and good tooling
- wgpu integration is native

**What you'd lose:**
- Rust's learning curve is steep; the AI agents write Go, not Rust
- Ecosystem is younger than Qt/JUCE for DAW-specific widgets
- Same FFI problem as C++ for calling the Go engine
- Development speed for non-GUI code would crater

**Verdict:** Rust is right for the video sidecar (already planned). It's not right for the main GUI — the same "build widgets from scratch" problem exists in Rust's GUI toolkits too (egui is better than Gio for widgets, but not dramatically so).

### If you kept Go for the engine but used a different Go GUI toolkit

**Options:** `giu`/`cimgui-go` (Dear ImGui binding), `Fyne`, `Ebiten`

**The strongest case is `cimgui-go` + Dear ImGui:**
- ImGui has `ImDrawList` for custom timeline/piano-roll drawing
- Sequentity proves a full sequencer UI in ~1,000 lines
- Docking, context menus, drag-and-drop are built-in
- MIT license, large community (67k+ stars)
- `giu` wraps it in a higher-level Go API

**Cost:** Requires mingw-w64 (cgo). Uses OpenGL, not D3D11. Existing Gio panels (~2,000 lines) need rewriting.

**My honest take:** If the project were starting the GUI from scratch today, `cimgui-go` would be a stronger pick than Gio for DAW surfaces. But the existing Gio code works, the `--render-frame` loop is valuable, and the migration cost isn't zero. The right move isn't a full switch — it's allowing ImGui as an **additional** panel renderer within the same Gio window, for the surfaces where Gio's primitives are the bottleneck.

---

## 6. The question nobody's asking: does becky need pro-grade timeline widgets at all?

This is where I think the project is misdiagnosing its own problem.

You said: *"80% of the time is spent doing mundane, deterministic tasks, hunting through drop-down menus, trying to decide if it's worth it to add another synth layer because adding all the routing is a deterrent."*

**That's not a GUI widget problem. That's an AI assistant problem.** And becky's AI assistant is already the right solution for it.

The propose-preview-apply loop (`ctledit` → overlay → approve/reject) is the genuinely novel part of becky. No other DAW has this. The "show me, don't do it" overlay is the killer feature. The deterministic composition engine (`beatgen` + `arrange` + `musictheory`) is the value.

**What if the complex timeline widgets aren't the priority?**

Consider: what if becky's arranger view didn't need to be a Cubase-grade timeline with pixel-perfect drag/resize/snap? What if it could be:

- A **simplified track list** with clip names, durations, and colors
- **AI does the precision editing** — "move the bass clip to bar 5," "trim the guitar to 8 bars," "duplicate the drum pattern for 16 bars"
- **The overlay shows the result** before applying
- **Manual editing** is limited to simple click-to-select + type-a-number (start bar, length, etc.)
- **Piano roll** stays for note-level work (where manual editing IS necessary and the widget IS well-scoped)

This is genuinely different from every other DAW. It's the "AI assistant that happens to have a track view" instead of "a DAW that happens to have an AI chatbox." And it's 1/5 the GUI widget effort.

**The timeline widget is the most expensive thing to build and the least aligned with becky's actual value proposition.** The value is the AI brain + deterministic engine + propose-preview-apply loop. The timeline is table-stakes chrome that every other DAW already does better with 20 years of polish.

---

## 7. My actual recommendation

### Keep (these are right, don't touch them)

1. **Go for the engine, brain, session model, CLI tools.** This is 80% of the codebase and Go is the right tool.
2. **C++ sidecar for VST3/ASIO.** Non-negotiable — the SDKs are C++.
3. **Rust sidecar for video.** Correct for GPU video decoding.
4. **The NDJSON seam.** This is the architectural backbone. It's right.
5. **`dawmodel.Arrangement` as single source of truth.** The most important invariant in the project.
6. **The propose-preview-apply loop.** This is the killer feature. Protect it.
7. **`--render-frame` headless verification.** This is how the cloud agent verifies GUI work. Unique and valuable.

### Reconsider (these are dogma costing you real time)

1. **"No embedded browser, ever."** For the timeline surface specifically, an embedded web panel running `react-timeline-editor` (MIT) inside the Gio window is 100 lines of integration vs. 5,000 lines of from-scratch Gio widget. The NDJSON seam makes this architecturally clean. The WebView2 trauma was about using a browser as the *entire app shell* — a targeted panel is a different animal.

2. **"Port the interaction model, don't adopt the code."** For the piano roll specifically, consider embedding `cimgui-go` (MIT) and using Dear ImGui's `ImDrawList` for the note grid. ImGui + DAW widgets is a proven combination (Sequentity). The cgo cost is already accepted for audio.

3. **"Build the DAW inside becky-canvas."** The *spirit* is right (one window, shared state). But "inside the canvas" should mean "reads from the same dawmodel via the same seam," not "rendered by the same Gio `clip`+`paint` calls." Allow heterogeneous rendering for the hardest surfaces.

### Prioritize differently

The project's roadmap says Phase 2 is "build the ONE timeline widget." I'd argue the priority order should be:

1. **Phase 0: Persistent audio engine** (already identified — the spawn-per-click fix). This is the most impactful single change.
2. **Phase 1: Wire beatgen rich data to drum UI** (already planned). Cheap win, high value.
3. **Phase 2: Lean AI-first track view** instead of pro-grade timeline widgets. Simplified clip list + AI-driven precision editing + the overlay. This is the path that makes becky *different* rather than *a worse Cubase*.
4. **Phase 3: Multi-step agent loop** (the actual capability gap vs OpenDaw).
5. **Phase 4: Piano roll with cimgui-go or Gio** — this one surface is well-scoped enough for Gio to handle.
6. **Phase 5: Pro timeline widgets** — only after the AI-first approach is proven. If the AI-first track view works well enough (and it might), you may never need them.

### The one-line version

**becky's competitive advantage is the AI brain, not the GUI chrome. Build the brain first. Let the AI do the precision editing that would otherwise require 10,000 lines of hand-crafted timeline widgets. If you must build pro-grade widgets later, allow heterogeneous rendering — the seam already supports it.**

---

## 8. What I got wrong or might be wrong about

In the interest of honesty:

- **I haven't run becky-canvas on your hardware.** Everything I say about Gio's GUI limitations is from reading the code, not from experiencing the lag. The panels might feel better than I think.
- **The "AI-first track view" might not be enough.** If you need pixel-precise drag editing for your actual workflow (not just the AI-assisted parts), then the timeline widgets ARE the priority and my recommendation is wrong.
- **WebView2 in a panel might still be bad.** The old trauma might recur. I'm reasoning from architecture, not from having tested it.
- **cimgui-go's cgo tax might be worse than I think.** mingw-w64 on Windows can be finicky. The "already accepted for audio" argument assumes the build stays clean, which may not hold.
- **The Gio community might have solutions I'm not aware of.** The project has ~2.2k stars and an active Discord. There may be timeline/piano-roll widget efforts I didn't find.

These are the places where my analysis is most vulnerable to reality. Test them on hardware before acting on anything above.

---
---

# Addendum: Corrections After Jordan's Context (2026-06-22)

Jordan provided critical real-world workflow context that changes several of my positions. Here I retract what I got wrong and add new analysis.

---

## Corrections: what I got wrong

### RETRACTED: "Embed a web panel for the timeline"

Jordan has tried ~10 web-based video editors and ACE-Step-DAW (web-based, localhost). **All of them had unusable lag for his workflow.** This isn't trauma from one bad implementation — it's empirical evidence across many tools. The web is not the right rendering substrate for real-time DAW interaction at the scale Jordan works at (50-100 tracks, 100 VST3 plugins, complex routing, render-in-place).

**New position:** The "no embedded browser" rule is **correct and load-bearing**, not dogma. My suggestion was wrong.

### RETRACTED: "AI-first simplified track view"

Jordan needs **full Cubase-grade manual controls.** 50-100 tracks, pixel-precise drag editing, complex bus routing and sidechaining, render-in-place, stem bouncing, real-time VST3 parameter tweaking. The AI can't learn from him if he doesn't have the same fine-grained control he has in Cubase.

My "simplified track list + AI does the editing" suggestion assumed Jordan's pain was about the *precision* of manual editing. It's not — his pain is about the *mundane, deterministic parts* (hunting through menus, adding routing layers, repetitive tasks). He still needs the full manual controls for the creative 20%.

**New position:** The pro-grade timeline widgets ARE the priority. The project needs them, and they need to be Cubase-quality for manual editing. The AI sits *alongside* the manual controls, not *instead of* them.

### CONFIRMED: "Gio is the problem for the hardest 20%"

With the web escape hatch ruled out and full manual controls required, the Gio widget problem is now more acute than I originally stated. The project needs to build Cubase-grade timeline, piano roll, mixer, and waveform widgets — all from scratch, in an immediate-mode toolkit with no DAW widget ecosystem. The 15,000-25,000 lines estimate from my original analysis still stands, and it's now the critical path.

---

## The idea I missed: native llama.cpp embedding

Jordan said something that reframes the entire architecture:

> *"llama.cpp has .dll files. It's the same language as Kdenlive. No HTTP. No sockets, immediate token streaming, zero latency. Pure native integration with a 4b GGUF llama.cpp model compiled directly into [the runtime] with a custom toolset would be able to run my deterministic workflows."*

This is the most important insight in the entire project, and none of the 21 research documents mention it. Here's why:

### What becky does today (the abstraction tax)

```
User action → Gio GUI → Go engine → HTTP request → llama-server (subprocess) → 
model inference → HTTP response → Go engine → ctledit → proposal → overlay → approve
```

That's **3 network hops + 1 process spawn + 1 HTTP serialization** between the user's click and the model's first token. On top of that, `llama-server` is a full HTTP server with request parsing, routing, JSON marshaling — overhead that exists for every single inference call, even when the model is already loaded in GPU memory.

### What Jordan is proposing (native embedding)

```
User action → GUI → engine → llama.cpp function call (in-process) → 
first token streaming immediately → proposal → overlay → approve
```

No HTTP. No subprocess. No JSON round-trip. Just a C function call from Go (via cgo) directly into `libllama`. The model stays loaded in VRAM. Token streaming is immediate — the first token arrives in microseconds, not milliseconds.

### Why this matters for becky

**Latency:** For a DAW where the AI is embedded in every interaction (propose-preview-apply on every edit), the HTTP round-trip is the difference between "feels instant" and "feels laggy." At 50-100 interactions per session, those milliseconds add up to the subjective experience Jordan described: "server bloat, too many abstraction layers."

**Reliability:** No subprocess to crash, no port conflicts, no health-check polling, no "is llama-server running?" logic. The model is a library call, not a service.

**Determinism:** becky already avoids LLMs unless necessary (deterministic beats, deterministic arrangement). When it does use the model (NL → edit batch), the native call is faster AND more deterministic — no HTTP timeout, no connection retry, no server restart on failure.

**How to do it in Go:**
- `go.llama.cpp` (formerly `go-skynet/go-llama.cpp`) provides Go bindings that link directly against `libllama` via cgo. No HTTP server. No subprocess. Function calls.
- `myzhan/llama.go` is another option — direct cgo binding.
- Both load a GGUF model into memory and call `llama_decode` / `llama_sample` directly.
- This is exactly the pattern Jordan described: `.dll` files, same process, immediate streaming.

**The cost:** cgo dependency (mingw-w64 on Windows). But the project already accepts cgo for the audio engine. Adding one more cgo surface for the LLM is the same architectural pattern — a thin C bridge that Go calls into.

### This also reframes the "embed into existing FOSS DAW" question

Jordan asked: *"I was curious about ANY open source DAW we could do this with."*

With native llama.cpp embedding, the question becomes: **which FOSS DAW has the cleanest C/C++ codebase for embedding a model directly?**

| FOSS DAW | Language | License | Embeddability | AI-ready? |
|---|---|---|---|---|
| **Ardour** | C++ / GTK | GPLv2 | Good — modular, clean audio engine, plugin-like architecture | Could add an AI panel as a "plugin" |
| **LMMS** | C++ / Qt | GPLv2 | Moderate — Qt widget system, plugin architecture for instruments/effects | AI as a custom instrument/effect plugin? |
| **Zrythm** | C / GTK4 | AGPLv3 | Good — modern C codebase, GTK4 is approachable | Would need AGPL compliance |
| **OpenDaw** | C++ / Qt6 | GPL3 | Already has an AI assistant (~63 tools). Closest to becky's vision. | GPL3 + Tracktion(GPL3) + JUCE(AGPL3) taint |
| **Tracktion Engine** | C++ / JUCE | Proprietary SDK | Best — clean edit model ↔ GUI separation, designed for embedding | Commercial license required |

**But here's the catch:** embedding into ANY of these means adopting their stack (C++/Qt or C++/GTK), their build system (CMake), their widget toolkit, and their license. You'd get working timeline widgets (the thing Gio can't deliver) but lose the Go engine, the 157K lines of working code, the `--render-frame` verification loop, and the ability for AI agents to write code for it.

**The hybrid approach that might work:**

1. **Keep becky's Go engine** (dawmodel, ctledit, beatgen, arrange, the AI brain) — this is the value.
2. **Embed llama.cpp natively** into the Go process (via cgo, replacing the HTTP server pattern). This eliminates the abstraction tax.
3. **Build the GUI widgets in the toolkit that has them** — which means either:
   - (a) Bite the bullet and build them in Gio (slowest path, most control)
   - (b) Use `cimgui-go` + Dear ImGui for the complex surfaces (Sequentity proves it works, cgo already accepted)
   - (c) Build the hardest widgets (timeline, piano roll) in C++ as a sidecar that renders into the same window via a shared surface (this is how OBS Studio embeds browser panels — native chrome + embedded web views sharing one window)

Option (b) is the pragmatic middle ground. ImGui is immediate-mode (matches Gio's paradigm), has `ImDrawList` for custom drawing, context menus, docking, drag-and-drop all built-in, and Sequentity proves a full sequencer UI in ~1,000 lines. The existing Gio panels stay Gio. The complex surfaces use ImGui. Both render into the same window. Both read from the same `dawmodel.Arrangement`. The NDJSON seam makes this architecturally clean.

---

## The hot-reload concern

Jordan mentioned finding repos showing Go GUI hot-reload, and is worried about performance drag.

**The concern is valid.** Go GUI hot-reload typically works by:
- Watching file changes and re-running `go build` + restarting the app (slow, ~2-5 seconds)
- Using `gow` or similar watchers (same problem)
- Gio's hot-reload pattern re-creates the entire window on file change

For a DAW with 100 tracks and real-time audio, any mechanism that interrupts the render loop or causes GC pressure is unacceptable. The audio callback must never be interrupted.

**The right approach for becky:** Don't use hot-reload for the DAW surfaces. Use `--render-frame` for visual verification (already built — renders one frame to PNG headlessly, no window needed). Use the `go test` suite for logic verification. The iteration loop is: edit code → `--render-frame out.png` → check PNG → fix. No hot-reload needed, no performance overhead, no risk of GC pauses in the audio path.

Hot-reload is useful for **UI layout exploration** (what does the mixer look like with wider faders?) but not for **real-time audio interaction**. The `--render-frame` approach is the right one for becky, and it's already in the project.

---

## Per-VST plugin protocols

Jordan mentioned wanting to "teach" becky about specific plugins he uses, possibly with a dedicated becky-tool per VST.

This is the right instinct. Here's why it works well with becky's architecture:

**The VST3 parameter model is standardized.** Every VST3 plugin exposes parameters as `ParamID → value` pairs (0.0 to 1.0 normalized). becky's C++ sidecar can enumerate these: name, default, min, max, current value. That's the *data* layer.

**The "teaching" layer is where becky's preference learning fits.** For each plugin Jordan uses frequently:
1. becky reads the full parameter list from the C++ sidecar
2. Jordan names/describes them in natural language ("this is the compressor attack, I usually set it to 5-15ms for drums")
3. becky stores this as a `PluginProfile` — a mapping from natural-language descriptions to parameter IDs and preferred ranges
4. The AI can then say "set the compressor attack to 10ms" and the ctledit op `set_fx_param` maps it to the right ParamID

**This doesn't need a dedicated tool per VST.** It needs:
- A `PluginProfile` data model (JSON, stored alongside the project)
- A "learn this plugin" workflow in the canvas (enumerate params → Jordan describes them → store)
- The existing `ctledit OpSetFxParam` (already in the roadmap, Phase 4)
- The existing `internal/habits` preference learning system (MinEvidence=2 corroboration)

The per-plugin profile is essentially a lightweight "cheat sheet" that bridges natural language → VST3 parameter ID. Once Jordan teaches becky his 10-20 most-used plugins, the AI can set parameters by name without him hunting through dropdown menus. **This directly solves the "80% mundane" problem for FX parameter tweaking.**

---

## Updated priority recommendations

Given the new context, here's the revised priority order:

1. **Phase 0: Persistent audio engine** (unchanged — the spawn-per-click fix is still the most impactful single change)

2. **Phase 0.5: Embed llama.cpp natively** (NEW — replace `llama-server` HTTP subprocess with direct cgo binding to `libllama`. This eliminates the abstraction tax Jordan identified as the root cause of lag in every AI-integrated tool he's tried. ~2-3 days of work to wire up `go.llama.cpp` bindings, replace `internal/llmlocal`'s HTTP transport with direct function calls. Highest ROI per effort of anything in this doc.)

3. **Phase 1: Wire beatgen rich data to drum UI** (unchanged)

4. **Phase 2: Build pro-grade timeline widgets** (CHANGED — these are now the priority, not the "simplified track view." Use `cimgui-go` + Dear ImGui for the complex surfaces. The `--render-frame` loop stays for visual verification. Plan ~3,000-5,000 lines per widget surface.)

5. **Phase 3: Multi-step agent loop + plugin profiles** (the agent loop from the original plan, plus the per-VST "teach this plugin" workflow)

6. **Phase 4: Mixer / FX completeness** (unchanged — aux sends, AI-settable FX params, the C++ VST3 sidecar)

### The one-line version (revised)

**becky's value is the AI brain + deterministic engine + propose-preview-apply loop, BUT Jordan needs Cubase-grade manual controls for the creative 20%. Build the native llama.cpp embedding first (eliminate the abstraction tax), then build the pro-grade widgets (in ImGui if Gio can't deliver fast enough), then wire the agent loop. The "no embedded browser" rule is correct — keep it.**

---

## What I'm still uncertain about

- **cimgui-go within a Gio window:** I'm recommending this but haven't verified that ImGui can render into a Gio-managed D3D11/OpenGL context on Windows. It may need its own window or a separate rendering pipeline. This is a spike-and-verify task, not a guaranteed path.
- **go.llama.cpp binding quality:** The Go bindings for llama.cpp vary in quality and maintenance. `go.llama.cpp` is the most active, but cgo bindings for C++ libraries can be brittle across llama.cpp upstream changes.
- **The "embed into Ardour" path:** I listed it but didn't deeply investigate Ardour's plugin architecture for AI integration. It may be more or less approachable than I estimated.
- **Jordan's actual workflow speed:** Without watching him use Cubase for an hour, I'm inferring his workflow from descriptions. The priority ordering above is my best guess; Jordan should push back on anything that doesn't match his actual pain points.
