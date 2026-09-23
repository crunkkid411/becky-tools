# timeline-bench

A throwaway spike to answer one question: **does a native (Dear ImGui + libmpv) timeline
scrub faster than the WebView2 timeline in Becky Review?**

It is completely standalone — it touches **nothing** in `gui/BeckyReview`. It only *reads a
copy* of the libmpv runtime that Becky Review already fetched.

## What it is

- **Window:** Win32 + WGL **OpenGL** (no GLFW dependency — OpenGL also matches mpv's render API).
- **Timeline:** the **raw ImSequencer** files from `CedricGuillemet/ImGuizmo` (`src/ImSequencer.*`),
  fed a **mock JSON clip array** (`clips.json`) through `MockSequence : ImSequencer::SequenceInterface`
  (`src/sequencer.h`). Clips are laid end-to-end like Becky Review's EDL.
- **Video:** **libmpv `render_gl`** decodes into a GL texture drawn as an `ImGui::Image`, so the
  video and the timeline share one paint loop — dragging the frame cursor seeks the video.

## Build (MSYS2 MinGW64)

```powershell
.\build.ps1          # fetch deps (imgui, ImGuizmo, json, copy libmpv), configure, compile
```

Produces `build\timeline-bench.exe` (+ `libmpv-2.dll` beside it). Deps land in `third_party\`
(gitignored). Same toolchain as `native\audio-host`.

## Run

```powershell
.\gen-clips.ps1 -Count 500                        # write a 500-clip mock timeline from ..\..\test.mp4
.\build\timeline-bench.exe --clips .\clips.json   # interactive: drag the cursor to scrub
```

## Benchmark

```powershell
.\build\timeline-bench.exe --bench --frames 1000 --clips .\clips.json
```

Prints JSON with **two** sweeps (playhead dragged across the whole timeline):

- **`widget`** — ImSequencer draw only, no decoder. The pure *native-vs-DOM* number: how fast the
  timeline widget itself repaints while scrubbing N clips.
- **`full`** — same sweep **plus** libmpv frame-accurate (`absolute+exact`) seek + render every
  frame. Realistic end-to-end scrub.

`vsync is forced OFF` so fps reflects real throughput, not the monitor refresh.

### Knobs

| flag | default | note |
|------|---------|------|
| `--frames N` | 600 | timed frames per sweep (30 warm-up frames excluded) |
| `--clips PATH` | `clips.json` | the mock array |
| `--no-video` | off | widget-only (skip libmpv) |
| `--video PATH` | — | force one source for all clips |
| `--width/--height` | 960x540 | mpv render texture size |

CPU decode is the default (`hwdec=no` in `src/mpv_gl.h`) to isolate the widget cost; flip it to
`auto` for GPU-realistic decode. `gen-clips.ps1 -Count` scales the timeline (try 1000–5000 to match
a big forensic reel).

## Result (clean machine, 2026-07-03)

Source: the **real** `becky-hits.reel.json` — 14 clips / 11 min timeline across 3 sources
(incl. an 8.9 GB VOD). `--frames 500/2000`. RTX 3070.

**Real reel (14 clips):**

| decode | widget fps | full fps (widget + frame-accurate seek+render) |
|--------|-----------|------------------------------------------------|
| GPU (`hwdec=auto`) | 2822 (0.35 ms) | **412** (2.4 ms avg, 10 ms p99) |
| CPU (`hwdec=no`)   | 3676 (0.27 ms) | 238 (4.2 ms avg, higher tail) |

`max ~203 ms` = one-time open of the 8.9 GB VOD (absorbed by warm-up in real use).

**Widget-only scaling — before vs after viewport culling** (`ImSequencer.cpp` patched to draw
only on-screen rows + visible frame span; see the `timeline-bench:` comments there):

| clips | before cull | after cull |
|------:|------------:|-----------:|
| 14    | 2822 fps | 4319 fps |
| 500   | 165 fps  | 2904 fps |
| 2000  | 43 fps   | 3364 fps |
| 5000  | **17 fps** | **4080 fps** |

After culling the cost is flat (~0.25 ms/frame) regardless of clip count — O(visible), not O(total).

### Head-to-head vs the current Becky Review timeline

Measured on the same machine via CDP: how long one **timeline redraw** takes (`renderTimeline()`
in the WebView2 app vs one culled ImGui frame). Becky Review has **no DOM virtualization** — one
node per clip, forced layout over all of them each redraw.

| clips | Becky Review (WebView2) | Native (ImGui, culled) |
|------:|------------------------:|-----------------------:|
| 14 (real reel) | 0.5 ms (2000/s) — instant | 0.23 ms (4319/s) — instant |
| 500   | 9 ms (111/s) — instant    | 0.34 ms (2904/s) — instant |
| 2000  | 104 ms (10/s) — **laggy** | 0.30 ms (3364/s) — instant |
| 5000  | 571 ms (2/s) — **frozen** | 0.25 ms (4080/s) — instant |

### Verdict

- At real reel sizes both are instant — **no felt difference today**.
- Native stays instant at any scale because it only draws what's on screen; the current timeline
  redraws all N clips, so it degrades past ~1–2k clips (the same fix — DOM virtualization — would
  help it too, but native has far more headroom and plays video in-window at ~700 fps).
- **GLFW is irrelevant to speed** — the window library doesn't affect draw cost; culling does.
