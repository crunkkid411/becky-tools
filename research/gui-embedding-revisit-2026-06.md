# GUI embedding + timeline-snappiness revisit (2026-06-27)

> **Why this exists:** Jordan asked to re-open the GUI/embedding decision in light of the two
> recurring failures — **laggy timeline scrubbing** and **the AI being unable to build/verify the
> GUI** — and to scrutinize the WebView2 alternatives by name (CefSharp, DotNetBrowser, Ultralight,
> legacy WebBrowser/MSHTML). The first-pass research was too shallow (same lesson as the 5x ASR
> speedup we only got after Jordan pointed at Handy's DirectML/Vulkan path). This is the robust pass:
> ~10 research agents, primary sources, adversarial, confidence-rated. **No CLAUDE.md edits.**

---

## 0. The decision (lead with it)

**Adopt a native WPF (.NET 8) shell that splits the two jobs:** (a) the **UI/chrome/timeline-strip =
HTML/JS** in a **WebView2** control, loaded via *virtual-host mapping* (NOT a TCP server) and
CDP-enabled so the AI can drive/screenshot-verify it; (b) the **video preview/scrub pane = native
`libmpv`** (render API, hardware-decoded, `--hr-seek=yes`, frame-exact) — **not** an HTML `<video>`.

**Why the split, and why it changed from the obvious "just use WebView2 for everything":** the deep
research found that **WebView2's *no-server* local-video path is broken for seeking** — Microsoft's own
docs call virtual-host media "very slow," seeking breaks on large files, and `WebResourceRequested`
buffers instead of streaming (crashes on big clips). Reliable scrubbable video in WebView2 effectively
**requires the localhost Range-server Jordan banned.** So the video does NOT go through the browser at
all — it goes through libmpv, which decodes on the GPU, seeks frame-exact, needs no server, and has no
codec/patent footgun. The browser does what it's *good* at (the inspectable, LLM-authorable UI); libmpv
does what *it's* good at (frame-accurate forensic video). This also sidesteps both the WebView2
video-seek breakage AND CefSharp's H.264 problem (below). Verify the **engine↔GUI seam** (becky's
NDJSON model state), not the pixels.

> **One pinned-decision flag for Jordan:** this reverses `GUI-RULES.md`'s "no embedded browsers /
> WebView2 retired" call. The research shows *why that call was originally right* (the video-seek
> limitation IS the concrete evidence behind it) — but it only applies to **video through the browser**.
> Putting video in libmpv and only the *UI* in WebView2 honors the spirit (no fragile media server)
> while regaining the AI-verifiable HTML surface. Worth an explicit yes before any build.

This **resolves the three-way conflict** the repo had accumulated:

| Prior decision | What it chose | The conflict |
|---|---|---|
| `R-STACK.md` (becky-clip) | Go + WebView2 + a **localhost HTTP server** | uses a server |
| `gui-toolkit.md` (becky-canvas) | **Gio** (immediate-mode, pure Go) | "no widgets" → 3 failed attempts; **not AI-verifiable** |
| `HANDOFF-BECKY-WPF-GUI.md` (becky-window) | **WPF**, "**no web views, no localhost/server**" | banned the thing becky-clip relied on |

**The reframe that dissolves it:** the fragile thing on Jordan's box is the *localhost TCP server*,
**not HTML**. WebView2's `SetVirtualHostNameToFolderMapping` serves local HTML+media over a real
`https://appassets.example/` origin **with no server process at all**. So the customizable, AI-friendly
HTML surface Jordan liked in becky-clip can live *inside* the native WPF window, with no server, full
hardware-accelerated video, and full AI verifiability. One stack, all three constraints satisfied.

---

## 1. The embedding shoot-out (the controls Jordan named)

Ranked for becky's exact constraints: Windows 10 + RTX 3070; **mingw-gcc only (no MSVC, no Qt6)**;
**.NET 8 available**; must play/scrub video; must run **offline**; **avoid a TCP server**; the **AI must
build + verify** it; free; high-contrast (ACCESSIBILITY.md).

Scored on TWO different jobs, because the answer differs for each:

**Job A — host the HTML UI/chrome (no video):**

| Option | Offline w/o server (static UI) | AI build+verify (CDP) | mingw-only OK? | License/cost | Verdict for UI |
|---|---|---|---|---|---|
| **WebView2 + .NET 8** | **Yes** (virtual-host) | **Yes** — in-process CDP + `CapturePreviewAsync` + Playwright `connectOverCDP` | **Yes — needs NO C++ compiler** | **Free** | **✅ best** |
| CefSharp | Yes (custom scheme) | Yes (CDP) | prebuilt only | BSD free, ~150–200 MB bundle | ✅ ok, heavier |
| DotNetBrowser | Yes | Yes | Yes (.NET) | **~$1,299+/dev** | ❌ cost |
| Ultralight | Yes | **No CDP** | n/a | **$3,000/yr**, closed | ❌ |
| Sciter | Yes | **No CDP** | n/a | ~$310, closed | ❌ |
| Legacy MSHTML | Yes | COM DOM, no modern stack | Yes | Free | ❌ trap |

**Job B — play + SCRUB local video with no server (the hard one):**

| Option | Seekable local video, no TCP server | The catch |
|---|---|---|
| **libmpv (native pane)** | **✅ yes** — GPU decode, `--hr-seek=yes` frame-exact, any codec, no server, no patent issue | not HTML; embed via render API into a WPF pane |
| CefSharp | ✅ *if* you hand-write a 206-range `IResourceHandler` | **can't decode H.264** (prebuilt strips it; building it needs MSVC, which Jordan lacks) — only VP9/AV1/WebM free |
| DotNetBrowser | ✅ custom scheme returns 206 | **~$1,299+/dev**; closed-source |
| WebView2 | **❌ broken** — virtual-host media "very slow," seeking breaks on large files; `WebResourceRequested` buffers (crashes on big clips); doesn't fire for virtual-host URLs | reliable scrub needs the **localhost server Jordan banned** |

**The three findings that drive the split decision:**
1. **For the UI, WebView2 needs no C++ toolchain** — builds entirely with the .NET 8 SDK. becky's
   "mingw-only, no MSVC/Qt6" constraint (which killed C++/Qt and complicated Gio-cgo) doesn't apply.
2. **For video, WebView2's no-server path is genuinely broken for seeking** — confirmed by Microsoft's
   own docs + multiple WebView2Feedback issues (#2679, #1206, #3519, #5070). This is the recurring pain
   point, so it's disqualifying for the video pane *under the no-server rule*.
3. **CefSharp can't play H.264 evidence on Jordan's box** — prebuilt CEF strips H.264; enabling it
   needs an MSVC CEF build he can't produce (mingw-only). So CefSharp can't be the video path either.

→ **No single free Chromium embed satisfies {no-server + H.264 + seekable} at once.** The resolution is
to stop forcing video through the browser: **libmpv for video, WebView2 for the UI.** libmpv hits all
three (no server, any codec incl. H.264/HEVC, frame-exact seek) and is the standard native media path.

---

## 2. Why the timeline was laggy — and the real levers (ranked)

This is the part the first research missed. Scrub lag is a **media-decode problem first, a UI-toolkit
problem almost last.** Ranked by impact:

1. **Codec structure (dominant).** Long-GOP H.264/HEVC stores most frames as deltas; to show one
   mid-GOP frame the decoder must decode the whole chain from the prior keyframe — worst case
   **30–250 frame-decodes to display ONE frame**, and B-frames make reverse/jump-scrub worse.
   All-intra (every frame a keyframe) makes every frame a random-access point. The industry fix
   (Resolve/Premiere/FCP) is **transcode to intra proxies (DNxHR LB / ProRes 422 LT / MJPEG) — even
   when hardware decode works.** [Frame.io; Adobe community; Richard Lackey; cutback.video]
2. **Variable frame rate (VFR).** Phone/OBS/GoPro footage is VFR; NLEs "constantly recalculate the
   next frame" → frame-step stutter. Fix: **remux VFR → CFR** on ingest. [cutback.video; Shotcut forum]
3. **GPU hardware decode (D3D11VA / NVDEC).** Speeds *forward playback throughput* and multi-stream,
   but **cannot erase GOP seek latency**. On the 3070, **D3D11VA** is the pragmatic default (keeps
   frames on-GPU, no copy-back). [DXVA/NVDEC docs; mpv #11151]
4. **Storage I/O.** Keep proxies/cache on **local NVMe**, never a NAS/USB.
5. **Precomputed sprite-sheet thumbnails** for the scrub strip — web editors (OpenCut, react-video-
   editor, FreeCut) scrub fast by showing precomputed frames, **not** live-decoding the timeline.
6. **UI toolkit (last).** Only matters in one specific trap (below).

### The Shotcut smoking gun (why the fork was unusable)
**Shotcut cannot use the GPU decoder (NVDEC) for timeline playback at all — it is CPU-core-bound for
scrubbing.** That is an *architectural* limit of Shotcut/MLT's timeline consumer, not a config you can
fully tune away. Documented fixes are MJPEG source/proxy + preview-scaling (360/540/720p) + realtime
frame-drop. This is exactly the trap to avoid: **any becky path that wraps or imitates the Shotcut/MLT
timeline consumer inherits its no-GPU-decode scrub ceiling.** [binarytides; Shotcut forum/release notes]

### becky's own latent bug (already noted in HANDOFF-PROXY-SNAPPINESS.md)
`internal/reel/proxy.go` only builds a proxy when the source **isn't** already H.264, and uses
**long-GOP** libx264 when it does — so the common long-GOP-H.264 evidence gets **no scrub proxy at
all**. The fix (a `ScrubProxy` that emits low-res all-intra CFR regardless of source codec) is the
single highest-leverage change and is already speced in that handoff.

### DirectML / Vulkan honesty check
**DirectML is a DirectX-12 ML *inference* API — it has nothing to do with video decode.** The ASR
DirectML/Vulkan win does **not** transfer to video. On this hardware the real decode paths are
**NVDEC (CUDA) and D3D11VA**; Vulkan video decode exists but is not the mainstream NVIDIA-on-Windows
path. Don't chase DirectML for scrubbing. [Microsoft Learn DirectML; NVIDIA Video Codec SDK]

### Can an HTML-hosted timeline be snappy? — Yes, with the right engine
A Chromium host (WebView2) gets **WebCodecs `VideoDecoder`** (hardware-accelerated, seek-by-keyframe)
+ `requestVideoFrameCallback` + GPU `<canvas>`/WebGL/OffscreenCanvas. Web editors hit native-ish
scrub this way **over proxies + sprite-sheet thumbnails**. Honest cost: a WebCodecs scrub engine is
real work and can spiral if you also chase frame-exact gapless multi-clip playback + export. **For a
forensic *review* surface it's tractable; for a full editor it isn't — and you shouldn't build one.**
[WebCodecs/Chrome docs; OpenCut/FreeCut]

### The native escape hatch: libmpv
If `<video>`/WebCodecs scrub precision isn't enough for forensic frame-stepping, embed **libmpv via
the render API** (not `--wid`): hardware-decoded, GPU-rendered frames you composite your own playhead
over, with `--hr-seek=yes` for **decode-to-exact-frame** seeking — strictly more precise than a
browser `<video>`. Precedent: mpv.net, Celluloid, Haruna. This can be the *preview pane* inside the
WPF window even if the rest of the UI is WebView2/HTML. [mpv-examples render API; mpv manual]

---

## 3. Why the AI couldn't build/verify the GUI — and the fix

- **LLMs genuinely author HTML/CSS/JS best** (largest training corpus; a whole web-codegen benchmark
  ecosystem; screenshot→code is productized). Caveat: no one has benchmarked HTML-vs-WPF-vs-Gio
  head-to-head, so it's inferred, not measured. [DesignBench; WebSight; v0]
- **Verifiability is the real differentiator:**
  - **Gio/immediate-mode has NO accessibility/semantic tree** (ImGui's docs literally say
    accessibility "not supported"). An agent can only OCR pixels → **this is why the 3 Gio canvas
    attempts couldn't be verified.** [ImGui README; UI Automata]
  - **HTML (DOM-over-CDP) and WPF (UI Automation) both expose a queryable tree.** So HTML's
    verifiability edge is **large over Gio, small over WPF.** [UI Automata; MS UIA docs]
  - **The sharp caveat:** a zoomable timeline blows past DOM limits (~800–1,400 nodes; style/layout
    recalc janks) → you're forced to `<canvas>`/WebGL → which **erases** DOM-inspectability. For the
    *timeline core*, every toolkit converges to "the agent sees pixels." [Lighthouse DOM-size;
    wavesurfer/waveform-playlist use canvas; WebAIM canvas a11y]
- **The fix:** verify the **engine↔GUI seam** (becky's deterministic NDJSON model state, per
  GUI-RULES.md), **not** the rendered UI. That's a stabler, better target than scraping any UI tree —
  and becky already has the seam. Pixels/screenshots become a secondary sanity check.
- **For WebView2 specifically, the AI loop is the shortest of any option:** one .NET process that
  builds with no C++ compiler, opens the window, and **self-drives + screenshots via in-process CDP**
  (`CallDevToolsProtocolMethodAsync`, `CapturePreviewAsync`) — or is driven externally by
  **Playwright `connectOverCDP`** (officially supported for WebView2). [Playwright WebView2; WebView2 API]

---

## 4. Recommended architecture (concrete)

```
  ┌─ becky window (native WPF, .NET 8) ───────────────────────────────┐
  │  • native chrome: menus, file/folder pickers, high-contrast theme  │
  │                                                                    │
  │  PANE 1 — UI  = WebView2 control (NO TCP server):                  │
  │   • local UI  -> SetVirtualHostNameToFolderMapping(appassets)      │
  │   • panels/forms/results = DOM (LLM-authorable, CDP-inspectable)   │
  │   • timeline strip = <canvas> + sprite-sheet thumbnails (precomputed)
  │   • Fixed-Version runtime (~250MB) for offline determinism         │
  │   • CDP (--remote-debugging-port / in-process) for AI build+verify │
  │                                                                    │
  │  PANE 2 — VIDEO = native libmpv (separate child HWND, no overlap): │
  │   • render API, --hwdec=auto-safe (D3D11VA/NVDEC), --hr-seek=yes   │
  │   • frame-exact scrub of H.264/HEVC over INTRA proxies, NO server  │
  │   • becky's own playhead/markers drawn on top via the render API   │
  └────────────────────────────────────────────────────────────────────┘
        │ shells out to existing becky-*.exe (JSON in/out) — unchanged
        ▼ verification target = the NDJSON engine seam, not the pixels
```

- **The two panes don't overlap**, so there's no WPF "airspace" conflict (each is a child HWND in its
  own region — UI on one side, video preview on the other, the standard NLE layout).
- **Simpler fallback if Jordan ever relaxes the no-server rule for media only:** drop libmpv and put
  video back in WebView2 `<video>` behind a tiny **loopback Range-serving HTTP endpoint** (the agents
  clarified this is a media-only loopback, not the fragile app-server he banned). This is less code but
  reintroduces a localhost socket — offered only as an explicit trade, not the default.

- **becky's Go tools are NOT rewritten** — the WPF/WebView2 window is a thin shell over the existing
  `becky-*.exe` (same principle as HANDOFF-BECKY-WPF-GUI). This also *unifies* becky-window and
  becky-clip: one shell, the HTML surface becky-clip proved, hosted natively with no server.
- **Proxies are mandatory infrastructure** regardless of host (HANDOFF-PROXY-SNAPPINESS.md): all-intra
  + CFR, on NVMe.

---

## 5. What to spike before committing (honest unknowns)

1. **libmpv render-API preview pane in a WPF window** — prove a hardware-decoded, frame-exact,
   no-server video pane that becky drives (load/seek/play). This is the new highest-risk unknown,
   since it replaces the broken WebView2 video path. (Precedent: mpv.net, Celluloid, Haruna.)
2. **The intra-proxy fix** (HANDOFF-PROXY-SNAPPINESS.md Step 1–2) — does all-intra + CFR make
   scrubbing smooth? Cheap, and may rescue the current Shotcut fork in the meantime regardless of host.
3. **WebView2 UI + CDP self-verify loop** — prove a local/headless agent can launch the WPF+WebView2
   window, load the HTML UI via virtual-host mapping (no server), drive it over CDP, and screenshot —
   the verification loop that was impossible on Gio.
4. **The seam:** confirm the AI can verify outcomes by reading the NDJSON engine state, not pixels.
5. Only if the libmpv embed proves too heavy: reconsider the media-only loopback Range-server + WebView2
   `<video>` fallback (requires Jordan relaxing the no-server rule for media).

## 6. Confidence + sources

High confidence: WebView2 needs no C++ compiler; CefSharp prebuilts strip H.264 + custom build needs
MSVC; Ultralight video crashes on seek + $3k/yr; Sciter no-CDP + libVLC; MSHTML is IE11-frozen; codec/
GOP is the dominant scrub lever; Shotcut can't GPU-decode the timeline; DirectML ≠ video decode; HTML
verifiable over Gio via CDP, Gio has no a11y tree. Medium: DotNetBrowser exact price (commercial, per-
dev — verify current tier); Sciter exact price (~$310); "LLMs author HTML better" is inferred from
corpus/benchmark asymmetry, not a head-to-head benchmark.

Primary sources (selected): WebView2 docs (working-with-local-content, SetVirtualHostNameToFolderMapping,
evergreen-vs-fixed, overview-features-apis), Playwright WebView2 (connectOverCDP), CEF forum + build
docs, Ultralight GitHub issues #217/#451 + pricing, Sciter docs/prices, IE11 retirement FAQ, Frame.io
codec guides, Adobe community intra-vs-longGOP, cutback.video NLE-lag series, binarytides Shotcut lag,
mpv-examples render API + mpv manual hr-seek, Microsoft Learn DirectML, NVIDIA Video Codec SDK,
Lighthouse DOM-size, wavesurfer.js / waveform-playlist, WebAIM canvas accessibility, DesignBench /
WebSight / v0. (Full URLs captured in the session research transcripts.)
