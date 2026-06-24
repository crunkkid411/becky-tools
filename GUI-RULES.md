# GUI-RULES.md — becky's standard GUI + audio architecture (write-once, read-first)

> ## ⚠️ SUPERSEDED FOR THE BECKY GUI SHELL — 2026-06-24 (Jordan): the becky GUI is now **native WPF (C#/.NET)**.
> After **three Go+Gio `becky-canvas` attempts failed in one week** (Gio ships no real widgets, so every control
> was hand-drawn — the root cause of the jank), Jordan ruled: the becky GUI is a **native Windows WPF app**.
> **No browsers / web views / localhost servers** (his hard constraint — servers are fragile on his machine).
> WPF is native, fast (compiled, GPU-composited), has real widgets + deep Windows resources, and Claude Code
> builds it reliably. becky's Go tools are NOT rewritten — the WPF window **shells out to the existing
> `becky-*.exe`** (JSON in/out). **Work order: `HANDOFF-BECKY-WPF-GUI.md`.** The Go+Gio stack below is retained
> only as reference/for any non-shell engine pieces; **do NOT start new GUI work in Gio** — use WPF.

**Status:** CANONICAL. Ratified by Jordan 2026-06-19. Every becky GUI/audio surface follows
this. It supersedes the audio-licensing conclusion in `research/gui-toolkit.md` (written before
the 2025-11 Steinberg relicensing) and consolidates two research passes (the GUI-toolkit pass and
the VST3/ASIO/native-perf pass) into one decision. If reality and this file disagree, fix this file
and tell Jordan — don't quietly diverge.

Jordan is a non-developer, 15+yr pro music producer on Windows 10 (GPU + a **Steinberg UR12**
interface as the system default in/out). The bar is **radio-ready, no cutting corners, zero lag.**
He abandons slow/laggy GUIs for old tools even when they cost him 6× the time. So **responsiveness
is a correctness requirement, not a nice-to-have.**

---

## 0. The one rule that started this

**No embedded browser engines, ever. Native pixels only.** WebView2 (used by the first becky-clip)
is the source of the lag/fragility pain and is **retired**. Tauri is also out — on Windows it *is*
WebView2. This is the single most load-bearing rule; everything below follows from it.

(The misconception worth killing: "all our GUIs were slow" is false. Only the WebView2 one was.
`becky-canvas` and the drum machine already run on **Gio** — native, GPU, no browser — and they're
snappy. The fix was never "leave Go"; it was "stop embedding a browser.")

---

## 1. The stack (each surface in the tool that's actually best at it)

| Component | Language / toolkit | Role |
|---|---|---|
| **becky engine** | **Go** | the brain: indexing, retrieval, fusion, project/timeline model, AI orchestration. Pure-Go, **UI-agnostic**, deterministic. |
| **DAW / chat / drum machine / piano-roll / mixer UI** | **Go + Gio** (Direct3D 11, no cgo) | native GUI surfaces; cloud-buildable; headless stub keeps CI green. |
| **Audio + VST3 host** | **C++ sidecar** (PortAudio + ASIO SDK + VST3 SDK) | hosts Jordan's **VST3 plugins**, drives the **UR12 over ASIO** at low latency. Owns its own lock-free realtime thread. |
| **Video / NLE preview** | **Rust + wgpu** sidecar | frame-accurate, GPU-decoded video preview — the only path to Vegas-fast scrubbing. |
| (future) CLAP loader | C++ (same host) | newer plugins that ship CLAP; additive. |
| (future) deterministic CLI accelerator | Nim (optional) | only if a specific deterministic CLI hot path is measured too slow. |

**Why polyglot, not a monolith:** the realtime audio callback + VST3 hosting belong in C++ (the SDKs
*are* C++, now MIT/GPL-clean, with reference host code). Frame-accurate GPU video belongs in
Rust+wgpu (Gio can't render video pixels). The DAW chrome belongs in Gio (pure-Go, cloud-buildable).
Forcing one language would hand two of the three surfaces a worse tool. The cost — three toolchains —
is acceptable for a private, single-user suite where each sidecar is small, independent, and
composable (becky's "when one breaks it's obvious which one" philosophy).

**This is single-user, not a product.** We are NOT shipping to a network of users. Reject any
"standard" feature that exists for multi-user/networked deployments and costs performance. Every
hot-path component is as close to bare metal as practical.

---

## 2. THE deterministic seam (the part that prevents the next WebView2 fiasco)

The toolkit is a **swappable shell** only because the engine never depends on it. This contract is
law for every front-end (Gio, the C++ audio host, the Rust video sidecar, the becky-ask TUI):

1. **The GUI may only READ engine state and EMIT commands. It NEVER owns musical/forensic/timeline
   state.** The Go engine is the single source of truth.
2. **Transport = newline-delimited JSON (NDJSON) over stdio.** Not gRPC, not a socket framework —
   zero extra deps, trivially faked in Go unit tests, language-neutral. (This generalizes the
   `beckyCall(reqID, verb, argsJSON)` async bridge that already works in becky-clip.)
3. **Three message kinds, that's it:**
   - `query` → engine returns a state snapshot (project/scene/timeline as JSON); the front-end
     renders from it.
   - `command` → a typed, named edit (`pad.toggle`, `note.move`, `fader.set`, `clip.trim`,
     `sample.drop`, `vst.load`, `transcribe`, `export`). Engine validates, applies **immutably**,
     returns the new snapshot + an undo token.
   - `event` → async push engine→front-end (level meters, transport position, "becky is thinking…",
     proposal-ready, job progress).
4. **Every command is async / non-blocking.** The shell enqueues; the engine runs slow work off the
   UI thread and resolves when done. This is the actual root-cause fix for the becky-clip
   "search works once then freezes" bug, and it is toolkit-independent. **Never run a slow verb on
   the UI/render thread.**
5. **Audio data NEVER crosses the JSON seam.** The C++ audio host owns its own lock-free realtime
   thread; NDJSON is only its *control plane* (load plugin, set param, transport, meter readouts).
6. **Determinism preserved:** same snapshot + same command sequence → same result (fixed seeds; the
   offline invariant). The model never ingests the 500GB folder — it queries the retrieval funnel.

---

## 3. Build & verification rules

- **Every GUI surface behind a build tag with a headless stub beside it.** `//go:build gui` for the
  window; `//go:build !gui` stub so `go build ./...` + CI stay green with ONLY the Go toolchain.
  (Already the pattern — now mandatory.)
- **The engine is always pure Go, no GUI/cgo import.** cgo is allowed only for the existing audio
  engine (`//go:build audio`) and is otherwise isolated in the native sidecars as **separate
  binaries** — never linked into the Go build.
- **SDKs are fetched locally, never vendored/redistributed.** The VST3 SDK and ASIO SDK are
  downloaded to a local `third_party/` (or located via env var) at build time on Jordan's PC,
  exactly like model weights. CI does at most a "headers-absent → skip cleanly" compile check.
- **Who builds / who verifies (honest):** the cloud agent builds + verifies the **Go brain** (the
  hard 95%) headlessly and cross-platform. The **C++ audio host** and **Rust video sidecar** build +
  get verified **on Jordan's PC** (only he can confirm "plugin loads and sounds through the UR12 at
  a low buffer via ASIO" and "video scrubs instantly"). No native toolkit can open a window in
  headless CI — that's toolkit-independent, so it is NOT a reason to prefer one toolkit over another.
  The NDJSON seam itself is fully unit-testable with a faked sidecar (the established becky stub
  pattern), so most of the integration stays cloud-buildable.
- **One-click launchers stay ASCII-only, end with `pause`, parse-checked under PowerShell 5.1**
  (CLAUDE.md §3). Any native dependency (a `becky-audio-host.exe` / `becky-video-preview.exe`
  sidecar) is **detected, not assumed** — degrade to a plain "install/missing X" message, never a
  crash (the `w == nil` pattern).

---

## 4. Standard interaction patterns (Jordan's north-star, codified)

These live in the *shell* but are identical across every surface:

- **Show-me, don't do it (GLOBAL):** every agent action is `propose → preview overlay → human ✓/✗`.
  Nothing mutates until approval. Agent `command`s return a *proposal* snapshot, applied only on an
  explicit `command: apply`. (Already built in `cmd/canvas/gui_overlay.go` + `internal/canvas/transform.go`.)
- **Select → ask → transform-in-place:** the agent box is scoped to the current selection; no
  selection = talk to the whole project. Result streams back onto the surface, with undo. **ONE
  small box, never a wall of text.**
- **Drag-and-drop, two paths:** argv-on-launch (drop onto the exe — reliable everywhere) + in-window
  OS drop. (Honest carry-over: in-window OS file drop is the one Gio rough edge; the picker + argv
  paths work today, and the native sidecars get OS drop for free.)
- **Colors & shapes over text; designed hover/focus/active states.** (CANVAS-INSPIRATION.md.)

---

## 5. Language verdicts (decided; revisit only on the stated condition)

- **C++** — the audio/VST3 host. The SDKs are C++ and now MIT/GPL-clean; reference host code exists.
- **Rust** — the video sidecar (mature wgpu; the Gausian editor proves the AI-NLE pattern).
- **Go + Gio** — the engine + GUI shells (cloud-buildable, native D3D11, no cgo).
- **Nim** — real win, but ONLY for a deterministic CLI hot path later (Jordan's auto-editor
  Python→Nim ≈6× anecdote is verified). Not for audio/VST3/video.
- **Zig** — technically ideal for DSP, but pre-1.0 churn fails "no cutting corners." Revisit
  post-1.0; do not adopt now.
- **zrolang / "Zero" (Vercel Labs)** — real but experimental, built for *AI repairing code*, no
  audio/video/SDK story. **Ignore** — exactly the hype-bloat to avoid.

---

## 6. License facts (precise, dated — the thing that changed the answer)

- **VST3 SDK: MIT**, since VST 3.8 on **2025-11-04**. No agreement, no GPL contagion, free for
  private/proprietary use. → host with the raw SDK in a small C++ process; read JUCE only as a
  reference, don't adopt it.
- **ASIO SDK: dual GPLv3 / proprietary**, since **2025-11-04**. For a non-distributed private host,
  GPLv3 is clean. Headers fetched locally, never committed.
- **PortAudio: MIT** (ships a proven `paASIO` backend when built against the ASIO SDK). This is the
  ASIO path — NOT extending miniaudio, which has no maintained ASIO backend.
- **JUCE: AGPLv3 / commercial** — reference only; its bloat fights the toolkit-neutral seam. (AGPL
  obligations don't even trigger for non-distributed private use, but we still don't adopt it.)
- **CLAP: MIT.** **Gio: MIT/Unlicense. Rust: MIT/Apache. Nim: MIT.** All distribution-clean.

---

## 7. Phased path

0. **Retire WebView2 from becky-clip** (highest ROI; its engine — `reel`/`edl`/`footage`/`quotes`/
   `assistant` — is already pure Go; re-shell on Gio, video preview lands in Phase 4).
1. **Formalize the seam (§2)** as one documented `query/command/event` NDJSON protocol with a fake
   transport for Go unit tests. *Fully cloud-buildable, green CI.* The durable win even if no toolkit
   ever changes.
2. **C++ audio host MVP (local):** PortAudio + ASIO to the UR12 — duplex low-latency round-trip, no
   plugins yet. Verify: tone in/out at a small buffer, no glitches.
3. **VST3 hosting (local):** add the MIT VST3 SDK — scan, instantiate, route audio through one real
   plugin, expose params + the `IPlugView` editor. Verify on a real Maschine/Cubase-era plugin
   through the UR12.
4. **Rust + wgpu video sidecar (local):** frame-accurate GPU preview wired to the NLE over the same
   seam (Gausian as reference). Verify instant scrub on a real clip.
5. **Polish + optional CLAP loader** in the same C++ host. Revisit Nim only on a measured CLI
   bottleneck; revisit Zig only post-1.0.

---

## 8. Sources (load-bearing)

- VST3→MIT / ASIO→GPLv3 (2025-11-04): librearts.org/2025/11/steinberg-relicenses-vst3-and-asio/ ·
  kvraudio.com/news/steinberg-moves-vst-3-sdk-to-mit-open-source-license-asio-now-gplv3-65179 ·
  soundonsound.com/news/steinberg-adopt-mit-license-vst3 · steinbergmedia.github.io/vst3_dev_portal/pages/FAQ/Licensing.html
- PortAudio MIT + ASIO build: portaudio.com/docs/v19-doxydocs/compile_windows_asio_msvc.html · en.wikipedia.org/wiki/PortAudio
- miniaudio has no maintained ASIO backend: github.com/mackron/miniaudio/discussions/263 · github.com/mackron/miniaudio/issues/133
- Gio (MIT/Unlicense, D3D11, no cgo): gioui.org · github.com/gioui/gio
- Rust+wgpu AI video editor reference (Gausian, MPL-2.0): github.com/gausian-AI/Gausian_native_editor
- Tauri = WebView2 on Windows: gethopp.app/blog/tauri-vs-electron
- JUCE AGPL/commercial: github.com/juce-framework/JUCE/blob/master/LICENSE.md · juce.com/get-juce
- CLAP (MIT): martinic.com/en/blog/clap-audio-plugin-format
- Nim ≈6× (auto-editor): basswood-io.com/blog/nim-auto-editor-is-now-in-beta
- Zig pre-1.0: ziglang.org/learn/overview · zrolang/"Zero": github.com/vercel-labs/zerolang
- Native GUI verification needs UI-Automation/image drivers (toolkit-independent): testsprite.com/use-cases/en/the-most-accurate-alternatives-to-winappdriver
