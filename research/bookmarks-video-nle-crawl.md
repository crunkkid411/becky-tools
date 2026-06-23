# Bookmarks Crawl: FOSS NLE host for becky's video editor

Source: Jordan's curated "AI Tools / Video Editing" Chrome bookmarks (~62 entries).
Crawled 2026-06-22 to find the best mature FOSS NLE to ADOPT/MODIFY as a host for an
embedded AI agent (+ embedded Claude Code terminal + native llama.cpp), replacing the
failed from-scratch Go/Gio NLE attempt.

## TOP-LINE FINDING (read this first)

**The bookmark folder is NOT a desktop-NLE collection.** It is overwhelmingly an
*AI-clip-generation / video-understanding* collection (ClipsAI, Vizard, Submagic, Reka,
TwelveLabs, SmolVLM2, MCP servers, React web editors). The classic adopt/modify desktop
NLEs the task asks about — **Olive, Shotcut, kdenlive, OpenShot, Flowblade, Cinelerra —
are NOT bookmarked.** Only ONE traditional desktop NLE appears: `opencodewin/MediaEditor`
(and it is suspended). So the strongest host candidates were assessed by direct research,
not the bookmarks. **becky already uses kdenlive+MLT as a headless backend** (per CLAUDE.md
`internal/kdenlive` + `cmd/becky-nle`), which is the decisive context: the host should be
the same MLT family becky already drives.

---

## RANKED NLE-HOST CANDIDATES

### 1. (RECOMMENDED) kdenlive — KDE/kdenlive  [NOT bookmarked; becky already uses it]
- **URL:** https://github.com/KDE/kdenlive
- **Lang/toolkit:** C++ / **Qt6 + KDE Frameworks 6** (fully Qt6 since 24.02; Qt5 dropped).
- **License:** GPL-3.0.
- **Render engine:** **MLT 7.28** framework + FFmpeg (same engine becky already writes
  `.kdenlive`/`.mlt` projects for and renders headless via bundled `melt.exe`).
- **Extensibility:** MLT services/filters plugin system; effects are MLT plugins;
  **OpenFX support incoming**; contributes to **OpenTimelineIO** (OTIO) for interchange —
  a clean, scriptable project model. Qt/QML + C++ UI panels can be added.
- **Maturity/activity:** Very active — v26.04.2 (June 2026), 8-member core team, 38 code
  contributors in 2025, decade-roadmap (10/12-bit color, per-param keyframing, Dopesheet).
- **Windows build:** Official Windows builds + CI; CMake; the heaviest of the three to
  build from source on Windows (KDE Frameworks dependency chain) but documented.
- **Why:** becky ALREADY authors MLT projects and renders with melt — adopting kdenlive
  means the GUI host and becky's existing engine speak the **same project format**. Lowest
  conceptual mismatch; OTIO gives a deterministic, agent-writable timeline. Best long-term.

### 2. (RECOMMENDED — easier Windows host) Shotcut — mltframework/shotcut  [NOT bookmarked]
- **URL:** https://github.com/mltframework/shotcut
- **Lang/toolkit:** C++ (~55%) + **Qt6 / QML** (~35%) — modern, the UI is largely QML.
- **License:** GPL-3.0.
- **Render engine:** **MLT** + FFmpeg (same family as kdenlive/becky).
- **Extensibility:** **QML-based UI** (easy to add panels/docks without deep C++), MLT
  service/filter plugins, and a **JavaScript** layer (~7%) for scripting. Filters are
  authored in QML — friendliest surface for adding an AI/agent dock.
- **Maturity/activity:** Very active — 14.4k stars, **v26.4 (April 2026)**, 191 releases,
  maintained Windows CI workflow.
- **Windows build:** **Excellent** — automated `build-shotcut-windows` CI, CMake. Easiest
  of the mature NLEs to build on Jordan's Win10 box.
- **Why:** Same MLT engine as becky, but a **QML UI that is far cheaper to extend** than
  kdenlive's C++/KF6 stack. Best balance of "mature + actively developed + MLT-compatible +
  easy Windows build + easy to bolt an AI panel on." Strong default if kdenlive's KF6 build
  is too heavy.

### 3. (NOT recommended) Olive — olive-editor/olive  [NOT bookmarked]
- **URL:** https://github.com/olive-editor/olive
- **Lang/toolkit:** C++ (~92%) / **Qt + OpenGL**, node-based.
- **License:** GPL-3.0.
- **Render engine:** Own **node-based** GPU (OpenGL) compositor; OCIO color; VST topic.
- **Extensibility:** Node graph is elegant but undocumented for third parties; no plugin API.
- **Maturity/activity:** **Stalled.** 9.1k stars but "highly unstable alpha"; last
  significant code ~Sep 2023, master build ~Dec 2024; no stable release. The "Razor"
  editorial rewrite never landed.
- **Windows build:** CMake cross-platform, but builds are known-fragile.
- **Why not:** Modern codebase but **development has stagnated and it has no stable base** —
  adopting it means inheriting an unfinished rewrite. The task flagged Olive specifically;
  the honest verdict is it's the riskiest of the three despite the nicest architecture.

### 4. (NOT recommended) MediaEditor — opencodewin/MediaEditor  [the ONLY NLE in the bookmarks]
- **URL:** https://github.com/opencodewin/MediaEditor
- **Lang/toolkit:** C++ / custom **ImGui**; own MediaCore engine (Vulkan shaders) + FFmpeg.
- **License:** LGPL-3.0 (most permissive of the set).
- **Extensibility:** **Blueprint node system** (45+ filters, 70+ transitions) — genuinely
  interesting for an agent to drive via nodes.
- **Maturity/activity:** **Project suspended (Dec 2024)**, "broken build for a long time,"
  502 stars, last release v0.9.9 (Feb 2024); funding dried up.
- **Windows build:** Executables exist; needs Vulkan SDK; hibernating.
- **Why not:** Suspended + acknowledged broken build = dead host. Worth a look ONLY for its
  ImGui-blueprint pattern (relevant to becky's Gio agent-overlay ideas), not as a base.

---

## AI-IN-NLE / AGENT-DRIVEN REFERENCES WORTH STUDYING (these ARE in the bookmarks)

- **autoEdit_2** (OpenNewsLabs) — https://github.com/OpenNewsLabs/autoEdit_2 — **MIT**,
  Electron/Backbone. **Transcript-first "paper editing": STT -> words linked to timecodes ->
  EDL export.** The closest existing model to becky-clip's forensic transcript->quote->reel
  flow. Best conceptual reference even though the codebase is old (last release 2021).
- **ExpressEdit / fesiib/video-editing-pipeline** — https://github.com/fesiib/video-editing-pipeline
  — Python/LangChain; **natural language + sketch -> structured JSON edit ops** (frame ranges,
  spatial coords, overlay/blur/crop/zoom). Paper: arxiv 2403.17693. **The canonical pattern
  for an LLM proposing timeline edits as JSON** — directly maps to becky's `ctledit` batch idea.
- **video-edit-mcp** (Aditya2755) — https://github.com/Aditya2755/video-edit-mcp — **MIT**,
  Python MCP server over MoviePy/ffmpeg; agent issues edits as **MCP tool calls**, chained
  in-memory. Reference for the "agent drives the editor over a tool protocol" architecture
  (parallels becky's NDJSON seam idea). Also: vizionik25/mcp-moviepy, FastMCP MoviePy entry.
- **trykimu/videoeditor** (DeepWiki only) — modern web NLE; the DeepWiki "Timeline System"
  + "Backend Services" pages are a clean read on how a contemporary timeline is structured.

## REUSABLE TIMELINE GUI COMPONENTS (web/React — relevant only if a web host is chosen)

- **twick** (ncounterspecialist) — https://github.com/ncounterspecialist/twick — React SDK;
  **`@twick/timeline` is a standalone package** (tracks, ops, undo/redo) usable independent
  of the studio UI; Fabric.js canvas; WebCodecs/Puppeteer+ffmpeg export; Gemini AI captions.
  License: Sustainable Use License (free in-product, can't resell as an SDK). v0.15.31 (2026).
- **reactvideoeditor/free-react-video-editor** — free OSS React timeline editor.
- **remotion** (remotion-dev) — programmatic React video; **revideo/redotvideo** fork +
  examples; **motion-canvas** — code-driven motion graphics (not an NLE but a timeline model).
- **IMG.LY CE.SDK** (imgly/cesdk-web-examples) — commercial web video-UI SDK (reference only).

---

## ONE-LINE CATALOG OF THE REST (AI clip / understanding / utility — not NLE hosts)

- **ClipsAI** (clipsai, video-editor, clipsai.com) — Python lib: long video -> clips; trim/resize.
- **ExpressEdit live demo** — https://expressedit.kixlab.org/ (the hosted ExpressEdit).
- **context-notation/video-notation-schema** — JSON schema for multi-scene AI video prompting.
- **mifi/lossless-cut** — Swiss-army lossless cut/trim (Electron); keyframe-cut, NOT frame-accurate.
- **mifi/editly** — declarative CLI video editing & API (Node).
- **drawcall/FFAIVideo** + **FFCreatorLite** — Node short-video generation from LLM / fast FFmpeg lib.
- **fal-ai-community/video-starter-kit** — browser AI video production starter.
- **OpenTimelineIO hook-script docs** — OTIO scripting (relevant to kdenlive's OTIO support).
- **jianfch/stable-ts** — Whisper transcription + forced alignment (becky already has Parakeet ASR).
- **francozanardi/pycaps** — animated CSS subtitles in Python.
- **roboflow/supervision** + **SmolVLM2** (blog/roboflow) — CV tools + on-device video VLM (triage).
- **yunlong10/Awesome-LLMs-for-Video-Understanding** — survey list of Vid-LLMs.
- **GVCLab/CutClaw** — agentic hours-long editing via music sync; **VAU-R1** — video anomaly RL.
- **egolife-ai/Ego-R1** — ultra-long egocentric video reasoning (chain-of-tool-thought).
- **zarazhangrui/longcut** — learn from long videos; **OpenNewsLabs** org root.
- **Reka Clip/Edge** (+ vllm-reka), **TwelveLabs** (pricing + Claude Code plugin) — video search APIs.
- **edenaion/EZ-CorridorKey** — green-screen keying. **Banuba** — commercial video SDK org.
- **EbenKouao/vlog-node-api**, **Castmagic/Vizard/Crayo/Spikes/Choppity/Submagic/StreamLadder/**
  **FireCut/Reka creator** — hosted SaaS AI-clip/caption products (competitor/feature references).
- **radames/edit-video-by-editing-text** (HF Space), **South-Park-Episodes Generator** (HF) — demos.
- **claudate analyze-video skill**, **2407.07111 / vidio-research review / ai-video-editor-review**
  — papers/surveys on AI video editing.

---

## RECOMMENDATION SUMMARY

Adopt an **MLT-based Qt NLE** so the host shares becky's existing engine and project format.
- **Best fit:** **kdenlive** (becky already writes its projects; OTIO timeline; very active) —
  but heavy Windows/KF6 build.
- **Easiest to extend + build on Windows:** **Shotcut** (same MLT, but QML UI + JS scripting
  makes bolting on an AI/agent dock and an embedded terminal far cheaper). **This is the
  pragmatic pick for embedding the agent.**
- **Avoid:** Olive (stalled alpha) and MediaEditor (suspended).
- **Study for the agent layer:** ExpressEdit (LLM->JSON edits), autoEdit_2 (transcript->EDL),
  video-edit-mcp (agent-over-tool-protocol).
