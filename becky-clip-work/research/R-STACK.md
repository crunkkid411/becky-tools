# R-STACK.md — becky-clip frontend stack decision (evidence-based)

> **Decision:** Build `becky-clip` on **WebView2 (Go backend + the installed system
> WebView2 runtime, HTML/`<video>` frontend)**. Both candidate spikes compile and open
> on this exact PC; WebView2 wins decisively for *functional-today video preview*
> because its `<video>` element decodes and renders a real H.264 frame **with zero extra
> native dependencies**, while the Gio path needs an mpv that is **not installed** and a
> nontrivial Win32 SetParent/IPC integration before any frame appears.
>
> Settled 2026-06-18 by building two real, runnable spikes and screenshotting them with
> vision. This is the gating technical decision for the tool.

---

## 0. TL;DR for Jordan (plain language)

I built two tiny throwaway test apps and took photos of them running on your PC:

- **Test 1 (Gio, pure Go):** a window opened with the transcript list, a black "video
  area" box, and the timeline strip. It works — but the black box stays black. To make
  video actually *play in it*, I'd have to install **mpv** (it's not on your PC) and glue
  it into the window with some fiddly Windows code. More moving parts, slower to working.
- **Test 2 (WebView2):** the **same** layout, but the video box showed a **real playing
  video frame** — and clicking a transcript quote jumped the video to that moment
  instantly. Nothing extra to install; the WebView2 the tool needs is **already on your
  PC**. The AI can drive the screen by sending little messages the page obeys.

**Go with Test 2 (WebView2).** It's the one that does the whole job today, stays
lightweight (no Electron, no 150 MB bundle), and is the easiest for the AI to control.

Screenshots (proof):
- `becky-clip-work/spikes/gio/gio-spike-screenshot.png`
- `becky-clip-work/spikes/webview2/webview2-spike-screenshot.png`

---

## 1. What actually built + opened on THIS PC

Toolchain confirmed live: Go 1.26.1 windows/amd64 · ffmpeg/ffprobe (anaconda) · node v24 ·
mingw gcc `C:\msys64\mingw64\bin\gcc.exe` · **WebView2 runtime 144.0.3719.115 installed**.

### Spike A — Gio (pure Go, native Direct3D 11) — built + opened (PASS)

- **Dir:** `becky-clip-work/spikes/gio/` · **Entry:** `main.go` (self-contained `main()`).
- **Module / import lines that worked** (`go.mod` — same Gio version the becky-canvas GUI
  already ships, so it's a known-good pin on this machine):
  ```
  module beckyclip-gio-spike
  go 1.26
  require gioui.org v0.10.0
  ```
  ```go
  import (
      "gioui.org/app"; "gioui.org/font/gofont"; "gioui.org/layout"
      "gioui.org/op"; "gioui.org/op/clip"; "gioui.org/op/paint"
      "gioui.org/text"; "gioui.org/unit"; "gioui.org/widget"; "gioui.org/widget/material"
  )
  ```
- **Build (canvas convention) — exit 0:**
  ```bash
  go build -tags gui -o becky-clip-gio-spike.exe .   # 11.3 MB, no cgo
  go build -o /dev/null .                             # plain build also green
  ```
- **Ran:** window opened at 1100x640, rendered the transcript list (neon-green selected
  row), the black `[ VIDEO PREVIEW AREA ]` panel, the `+ append clip` button, and the
  timeline strip of clip blocks. Screenshot confirms a real native window.
- **Gio v0.10.0 was already in the local module cache** -> builds fully **offline**.

### Spike B — WebView2 (Go backend + system runtime) — built + opened + **played H.264** (PASS)

- **Dir:** `becky-clip-work/spikes/webview2/` · **Entry:** `main.go` (`main()` + localhost
  HTTP server + WebView2 window).
- **Module / import lines that worked** (`go.mod`):
  ```
  module beckyclip-webview2-spike
  go 1.26
  require github.com/jchv/go-webview2 v0.0.0-20260205173254-56598839c808
  // indirect: github.com/jchv/go-winloader v0.0.0-20250406163304-c1995be93bd1
  // indirect: golang.org/x/sys v0.0.0-20210218145245-beda7e5e158e
  ```
  ```go
  import "github.com/jchv/go-webview2"
  ```
- **Build — exit 0, with cgo explicitly OFF (proves "no cgo"):**
  ```bash
  CGO_ENABLED=0 go build -o becky-clip-webview2-spike.exe .   # 9.6 MB
  ```
- **Ran (cwd = spike dir so `test.mp4` resolves):** window opened at 1100x640. The
  `<video>` element **decoded and displayed an actual H.264 frame** (the ffmpeg `testsrc`
  color-bars + timecode). The auto-seek to 3.0 s landed (4th quote row highlighted neon),
  and the Go process printed `[bridge] page reported: seeked to 3` — i.e. the **JS->Go
  bound function fired live**, which only happens after the page's JS ran and the seek
  executed. Exit code 0. (One benign Chromium teardown warning on stderr:
  `Failed to unregister class Chrome_WidgetWin_0` — cosmetic, ignore.)

### What failed / caveats encountered

- **No failures of either spike.** Both compiled clean and opened.
- The WebView2 binding was **not pre-cached** -> required one online `go get` (network was
  available; proxy.golang.org returned 200). After that, builds are local. The binding
  **embeds `WebView2Loader` and loads it in-memory via `go-winloader`** — so there is **no
  separate `WebView2Loader.dll` to ship**, and it uses the already-installed Evergreen
  runtime. Pin the version in go.mod for offline reproducibility going forward.
- `github.com/webview/webview_go` (the other option named in the task) was **not used**:
  it needs `WebView2Loader.dll` shipped alongside and historically wants cgo — strictly
  worse than `jchv/go-webview2` for "no-cgo, nothing extra to install." Recommend
  `jchv/go-webview2`.

---

## 2. Recommendation: **WebView2 (Go backend + HTML/`<video>`)** over Gio+mpv

Judged on the four axes the task set:

| Axis | WebView2 (Go + HTML/`<video>`) | Gio + mpv | Winner |
|---|---|---|---|
| **Functional TODAY** | Plays H.264 **now**; zero extra installs (runtime present) | Black rect until mpv installed **and** SetParent/IPC glue written | **WebView2** |
| **Native + lightweight (not Electron)** | One ~10 MB exe + OS-shared WebView2; no bundled Chromium | One ~11 MB exe + ~40 MB mpv/libmpv to bundle | **WebView2** (slightly; both are light) |
| **AI-controllability (AI emits JSON -> frontend applies)** | AI text -> JSON -> `w.Eval(js)` / `w.Bind` -> page mutates DOM. Trivial, demonstrated (bridge fired) | AI text -> JSON -> hand-written Gio widget mutations in Go; every UI surface is bespoke immediate-mode code | **WebView2** |
| **Instant click-to-preview seek** | `video.currentTime = t; play()` — sub-frame, demonstrated on screen | mpv `seek` over IPC or render-API; works but you must build the player first | **WebView2** |

**Deciding evidence:** in the WebView2 screenshot a **real video frame is on screen and a
quote-click seek had just executed**, with the Go<->JS bridge confirmed in stdout — the
entire becky-clip core loop, working, today, with **nothing to install**. The Gio
screenshot shows a polished but **empty** preview pane; closing that gap requires
installing mpv (absent) and writing Win32 `--wid`/`SetParent` embedding or a libmpv
render-API cgo layer. For a tool that must be *functional today* and whose UI is
**standard widgets** (lists, a video rect, a strip, a chat box) — not a zoomable creative
canvas — HTML/CSS is the faster, lighter, more AI-legible surface. The becky-canvas "web
rejected, use Gio" decision was for its *custom immediate-mode zoomable* needs and does
**not** transfer here.

> **Why this doesn't contradict becky-canvas.** becky-canvas needs a per-frame
> immediate-mode draw-list for an infinite zoomable timeline/piano-roll — Gio/ImGui
> territory, and web genuinely fought back there. becky-clip needs a transcript list, a
> video player, a clip strip, and a chat panel — the things HTML is *best* at, with the
> one hard part (frame-accurate video) handled for free by the Chromium `<video>` element.
> Different problem -> different right answer. Keep Gio for canvas; use WebView2 here.

---

## 3. The recommended path, concretely

### Dependency (one line, pin it)
```
require github.com/jchv/go-webview2 v0.0.0-20260205173254-56598839c808
```
Pure-Go, no cgo, no DLL to ship; uses the installed WebView2 runtime
(`C:\Program Files (x86)\Microsoft\EdgeWebView\Application`, currently 144.x).

### Window creation (the snippet that worked)
```go
w := webview2.NewWithOptions(webview2.WebViewOptions{
    Debug: true, // set false in release
    WindowOptions: webview2.WindowOptions{
        Title: "becky-clip", Width: 1100, Height: 640, Center: true,
    },
})
if w == nil { /* runtime missing -> degrade with a friendly message */ }
defer w.Destroy()
w.Navigate("http://127.0.0.1:<port>/")  // or w.SetHtml(...) for a static page
w.Run()                                  // blocks until closed/Terminate
```

### How the Go backend talks to the frontend (use BOTH; the spike proved both)
1. **Localhost HTTP (for media + the app shell).** Serve the page and the video from a
   `net/http` server on `127.0.0.1:0` (random free port). Serve the video with
   `http.ServeFile` — it emits `Accept-Ranges`, so the `<video>` element can **range-seek**
   without downloading the whole file. This is how the forensic source video (and ffmpeg
   proxies) reach the player. Offline-safe: it's loopback only.
2. **Bound Go functions (for AI actions + events).** `w.Bind("name", goFunc)` exposes
   `window.name(...)` to JS (returns a Promise). The page calls back into Go (in the spike,
   `window.reportReady("seeked to 3")` printed from Go — verified). Use this for
   timeline edits, "append clip", export, and telemetry.
3. **Go -> page (drive the UI / the AI's hands).** `w.Eval(jsString)` or
   `w.Dispatch(func(){ w.Eval(...) })` from any goroutine pushes commands into the page.
   The **AI router emits JSON**, Go validates it against an allowlist of operations, then
   `w.Eval("beckyApply(" + jsonLiteral + ")")` lets the page apply it — the
   "show-me / propose-then-apply" posture from becky-canvas, but trivially, in JS.

### How the preview seek works (instant, demonstrated)
- The page holds `<video id="vid" src="http://127.../source.mp4">`.
- Click a transcript quote (or the AI emits `{op:"seek", t:12.4}`):
  `vid.currentTime = 12.4; vid.play();` -> the Chromium media stack seeks within the
  buffered/ranged stream and paints the frame immediately. No Go round-trip needed for the
  seek itself (Go only needs to be involved when *it* initiates the action).

### Codec reality + the forensic-proxy plan
- WebView2 (Chromium 144) plays **H.264/AAC in mp4** natively (proven on screen). It also
  handles VP9/WebM/Opus and usually AV1.
- It will **not** play exotic forensic codecs (raw/Dahua/Hikvision/H.265-in-odd-containers,
  proprietary CCTV). **Mitigation, already in becky's wheelhouse:** on import, `ffprobe`
  the source; if the codec isn't web-playable, `ffmpeg`-transcode a **proxy** (e.g.
  `-c:v libx264 -movflags +faststart proxy.mp4`) and point `<video>` at the proxy while
  the durable Go engine keeps operating on the original for frame-accurate cuts. This is
  deterministic, offline, and consistent with becky's "ffmpeg is the offline workhorse"
  design.

---

## 4. Honest risks + fallback

**Risks of the WebView2 path**
- **Runtime dependency.** Needs the Evergreen WebView2 runtime. *Present today (144.x).*
  It ships in-box on Win11 and on essentially all updated Win10; if ever absent, the tool
  must detect `w == nil` and show a one-click "install WebView2 runtime" message
  (Microsoft's tiny Evergreen bootstrapper) — never crash (CLAUDE.md §2 degrade rule).
- **Codec gaps** for forensic formats -> handled by the ffmpeg-proxy step above (this is a
  feature becky should have anyway for evidence integrity).
- **Frame-exact scrubbing in `<video>`** is good but not guaranteed sample-perfect on every
  codec. For forensic cut points, treat the Go/ffmpeg engine as the source of truth for
  exact frame/timestamp; the `<video>` element is the *preview*, and ffmpeg performs the
  authoritative cut. (mpv would have the same caveat.)
- **Online fetch of the binding once.** Pin the version (done above) and `go mod download`
  into the cache so future builds are offline.

**Fallback if WebView2 hits a wall**
- **Primary fallback = Gio (already proven to open here) + embedded mpv** for the *preview
  pane only*: bundle a portable `mpv.exe` (~40 MB) or `libmpv-2.dll`, and either (medium)
  embed mpv via Win32 `--wid`/`SetParent` into the Gio HWND, or (easy, ships fastest) run
  mpv as a sibling window/process driven over its JSON IPC socket. Everything else (lists,
  timeline, chat) stays native Gio. This keeps becky 100% Go-native and offline at the cost
  of writing the player integration and bundling mpv.
- The two spikes are **disjoint and both green**, so switching later is low-risk: the
  durable value is the **becky-native Go engine** (deterministic tools + ffmpeg + AI
  router) behind whichever thin client wins. Build the engine first; the frontend is
  replaceable.

---

## 5. C++/Qt path — confirmed OFF the table for today

**Refuted as a today-option, with specifics:**
- **Qt6: not installed** (no `qmake6`/`windeployqt6`; no `C:\Qt`).
- The only Qt present is **Qt 5.15.2 bundled inside anaconda** (`qmake`/`windeployqt` under
  `C:\ProgramData\anaconda3\Library\bin`) — it's a **PyQt/Python** Qt5, **MSVC-ABI**, and
  there is **no MSVC `cl` compiler on PATH**. The only C/C++ compiler here is **mingw gcc
  (GCC ABI)**, which is **ABI-incompatible** with that MSVC-built Qt5. So you cannot build
  or link a C++/Qt GUI against it today without installing a full Qt6 + a matching
  compiler/IDE toolchain.
- Net: a C++/Qt frontend would require a fresh multi-hundred-MB toolchain install before a
  single window opens — the opposite of "functional today." **Off the table.** (It also
  orphans becky's Go/CLI/JSON layer, per the becky-canvas analysis.)

---

## Appendix — files produced (all under `becky-clip-work/`, nothing else touched)

```
becky-clip-work/
  research/R-STACK.md                                   (this file)
  spikes/gio/main.go, go.mod, becky-clip-gio-spike.exe
  spikes/gio/gio-spike-screenshot.png                   (Gio window — proof)
  spikes/webview2/main.go, go.mod, test.mp4, becky-clip-webview2-spike.exe
  spikes/webview2/webview2-spike-screenshot.png         (WebView2 playing H.264 — proof)
```

Reproduce:
```bash
# Gio
cd becky-clip-work/spikes/gio        && go build -tags gui -o becky-clip-gio-spike.exe . && ./becky-clip-gio-spike.exe
# WebView2 (run from the dir so test.mp4 resolves)
cd becky-clip-work/spikes/webview2   && CGO_ENABLED=0 go build -o becky-clip-webview2-spike.exe . && ./becky-clip-webview2-spike.exe
```
