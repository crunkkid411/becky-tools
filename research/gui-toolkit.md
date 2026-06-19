# GUI Toolkit Decision: Maschine-2-class Drum Machine + Piano Roll + Mixer (Go, Windows, GPU)

**Date:** 2026-06-19
**Question:** Which GUI toolkit should becky use for a Maschine-2-style instrument surface
(16-pad grid that lights up, full piano roll with draggable notes + velocity lane, mixer with
faders/meters, waveform display, sample drag-and-drop, chat panel, 60 fps) in a Go codebase on
Jordan's Windows PC (has a GPU)?

**Bottom line:** Stay on **Gio (gioui.org)** — the toolkit becky's `cmd/canvas` already uses and
which already opens + plays sound on Jordan's real Windows desktop. Honor the spirit of Jordan's
"in the IMGUI canvas" request by building an *ImGui-style immediate-mode DAW surface* on top of Gio,
not by ripping out the working stack to adopt a cgo Dear ImGui binding. The reasons are concrete and
sourced below. Critically, this choice is **reversible and low-risk** because the audio/sequencer/
sampler engine is already UI-agnostic (verified in the codebase), so the toolkit sits behind a thin
seam either way.

---

## 1. Comparison table

| Criterion | **Gio** (gioui.org) | **cimgui-go** (Dear ImGui binding) | **giu** (wraps cimgui-go) | **Ebiten** | **Fyne** |
|---|---|---|---|---|---|
| What it is | Pure-Go immediate-mode GUI w/ vector renderer | Auto-generated Go binding to Dear ImGui (C++) | High-level immediate-mode wrapper over cimgui-go | 2D game engine (immediate-mode draw loop) | High-level retained widget toolkit |
| License | MIT + Unlicense (dual) [1] | MIT [2] | MIT [3] | Apache-2.0 [4] | BSD-3 |
| cgo on Windows? | **No** (pure Go; D3D11 via syscall) [1][5] | **Yes** — needs mingw-w64 gcc [2] | **Yes** — mingw-w64 ≥ v12 required [3] | **No on Windows** (pure Go) [4] | Yes (OpenGL via cgo) |
| Windows GPU backend | **Direct3D 11** (native) [1][5] | GLFW+**OpenGL** or SDL2+OpenGL (prebuilt Go backends) [2] | GLFW + **OpenGL 3.2+** [3] | DirectX (pure-Go) [4] | OpenGL |
| Maturity / stars | ~2.2k, used in production apps [5] | ~523, active (v1.5.0 May 2026, 2013 commits) [2] | ~2.8k, active (v0.15.0 Jun 2026) [3] | very mature, large ecosystem [4] | very mature |
| Custom drawing (pad grid / piano roll) | `clip` + `paint` ops; full control over every pixel [1] | `ImDrawList::AddRect/AddLine/AddImage` — ideal for grids/timelines [6] | exposes Canvas + draw-list [3] | draw images/shapes per frame [4] | not designed for it |
| Drag-drop OS files | core `io/transfer` (internal DnD) + `gio-plugins/explorer`; OS file-drop is weak [7][8] | ImGui DnD payloads (in-app); OS file-drop via the windowing backend [6] | same as cimgui-go [3] | has Dropped-files API | limited |
| Text input (chat panel) | mature `widget.Editor` [1] | ImGui `InputText` (mature) [6] | ImGui `InputText` [3] | crude (game-grade) | mature |
| Docking | manual (no built-in) | ImGui docking branch (`io.ConfigDpiScaleFonts`) [6][9] | not exposed by default [3] | none | none |
| DPI / HiDPI | density-aware units (`unit.Dp`) [1] | docking branch DPI font scaling [9] | "auto scaling font/UI to HiDPI" [3] | manual | automatic |
| 60 fps feasible | yes (GPU vector renderer) [5] | yes (ImGui is built for this) [6] | yes [3] | yes (game loop) [4] | retained, not ideal |
| Reference DAW UI to learn from | becky's own `cmd/canvas` (3.6k LOC, working) | **Sequentity** (MIT-ish single-header sequencer) [10] | few audio apps | few audio apps | none |
| Build complexity on the PC | trivial (`go build -tags gui`) — already green [in-repo] | adds a C toolchain + prebuilt static libs to every build | same as cimgui-go | trivial | moderate |
| Fit for THIS app | **strong** | strong (but stack change) | strong (but stack change) | possible, lower-level | poor |

---

## 2. Recommendation

### Stay on Gio; build the ImGui-*style* DAW surface on it. Do not migrate to cimgui-go/giu.

**Reasoning, weighed against Jordan's stated "in the IMGUI canvas" preference:**

1. **The working stack already does the hard part on his actual hardware.** becky's `cmd/canvas`
   (~3,656 lines across `gui*.go`) is a Gio app that opens a native Direct3D 11 window and *plays
   audio* on Jordan's Windows PC — verified live in the §6 handoff log. The §6 note that "ImGui via
   giu was rejected" was specifically because **GLFW/OpenGL could not create a window in a
   non-interactive/headless cloud session.** That constraint does **not** apply on his real GPU
   desktop — but it *does* still apply to the cloud agent that writes most of becky's code, which is
   a real, recurring cost: every cloud build/test of a giu/cimgui-go GUI would need a C toolchain and
   would fail to even open a window for verification, whereas Gio's headless stub keeps
   `go build ./...` green in CI (this is exactly why becky split GUI behind `//go:build gui`).

2. **cgo is a permanent tax for a one-time preference.** Gio on Windows is **pure Go, no cgo**,
   rendering through Direct3D 11 natively [1][5]. cimgui-go and giu both **require a C++ compiler
   (mingw-w64 ≥ v12)** on every machine that builds them [2][3]. becky's build philosophy
   (`build-all-tools.bat`, CI on Ubuntu+Windows, "degrade, never crash") is built around a clean Go
   toolchain; adding a mandatory cgo path for the GUI complicates every build forever. (becky already
   accepts cgo for the *audio engine* behind `//go:build audio` + mingw — adding a second cgo
   surface for the GUI doubles that fragility.)

3. **The DirectX-vs-OpenGL detail matters on Windows.** cimgui-go's *prebuilt Go backends* are
   GLFW+OpenGL, SDL2+OpenGL, Ebiten, RayLib, DRM/EGL [2]. The native `imgui_impl_dx11` backend is a
   C++ file in upstream Dear ImGui [6] — it is **not** one of cimgui-go's ready-made Go bindings, so
   "ImGui on DirectX 11 from Go" is not a turnkey path; you'd be on GLFW+OpenGL. Gio already gives
   you D3D11 for free on Windows [1].

4. **ImGui's only genuine edge here is custom-widget ergonomics — and Gio matches it.** Dear ImGui's
   `ImDrawList` (AddRect/AddLine/AddImage) is a famously clean way to hand-draw a pad grid, timeline,
   and piano roll [6], and Sequentity proves a full sequencer fits in ~1,000 lines of it [10]. But
   Gio's `clip` + `paint` operation model gives the same pixel-level control [1], and becky's canvas
   already hand-draws a clickable 4×16 drum grid and a waveform with it (`gui_drum.go`,
   `gui_waveform.go`). The piano roll + mixer are the same class of work in either toolkit.

5. **What Jordan actually wants is the *feel*, not the *library*.** His north-star (§6, CANVAS-
   INSPIRATION.md) is "colors & shapes > text," "select → say it → AI changes it in place," the
   "show-me-don't-do-it" overlay, drag-and-drop, one small agent box. Every one of those is an
   immediate-mode interaction pattern that Gio expresses directly — and several are already built in
   `gui_overlay.go`. Adopting Dear ImGui would deliver the same *immediate-mode* feel at the price of
   throwing away the overlay, theming, drum grid, waveform, dock, and drag-drop shim already working.

**Honest counter-point (where ImGui would win):** if becky were starting from zero *and* the primary
developer worked interactively on the GPU box (not a headless cloud agent), Dear ImGui via
cimgui-go/giu would be a defensible first choice — it has the richest out-of-the-box DAW-widget
idioms (`ImDrawList`, docking, `InputText`), and Sequentity is a near-perfect head start [10]. The
recommendation flips *only* because (a) the cloud agent can't verify a GLFW/OpenGL window, (b) cgo is
a recurring build tax, and (c) a working Gio stack already exists.

### Migration-cost estimate (Gio → cimgui-go/giu), if ever reversed

- **Throwaway:** ~3,656 lines of `cmd/canvas/gui*.go` (theme, dock, drum grid, waveform, overlay,
  drag-drop shim, play transport). Rewrite, not port — different paradigm and draw API.
- **New build infrastructure:** mandatory mingw-w64 on every dev/CI machine; new headless story for
  the cloud agent (cimgui-go can't open a GLFW window in CI → you lose `go build ./...`-green-GUI).
- **Unaffected (the point):** `internal/audioengine`, `internal/dawmodel`, `internal/canvas` (the
  scene/transform/correction model) — **zero** changes, because the engine is UI-agnostic (see §3).
- **Rough effort:** 2–4 focused days to reach feature-parity with the current canvas, *plus* ongoing
  cgo/CI friction. Not worth it unless Gio hits a hard wall (it hasn't).

---

## 3. KEY architectural point: keep the engine UI-agnostic (it already is)

**This is the load-bearing decision, and it is already correct in the codebase.** The toolkit choice
is only low-stakes *because* the audio/sequencer/sampler engine does not depend on the GUI:

- `internal/dawmodel/` — `drumgrid.go`, `pianoroll.go`, `mixer.go`, `arrangement.go`, `smfio.go`,
  `quantize.go` are **plain data + pure-Go logic** with no Gio/ImGui import.
- `internal/audioengine/` — `sequencer.go`, `synth.go`, `transport.go`, `device.go`, `drumkit.go`,
  `machine_*` render/kit, native miniaudio bridge — all driven by `dawmodel` types, not by widgets.
- `internal/canvas/` — `scene.go`, `transform.go`, `corrections.go` hold the *model* of the
  surface; the Gio code in `cmd/canvas/` is a thin renderer/event layer on top.

**Mandate going forward (write it into the spec):** the GUI may only *read* `dawmodel`/`canvas`
state and *emit* edits/commands (toggle pad, move note, set fader, drop sample) through a small,
toolkit-neutral API; it must never own musical state. The drum machine + piano roll + mixer + sampler
must be fully exercisable from `becky-daw-engine` / unit tests with **no window open** (already true —
e.g. `becky-daw-engine --play-pattern-audio` renders sound headless). Hold this line and the toolkit
becomes a swappable shell: any later move to Dear ImGui (or a future renderer) re-skins, it does not
re-architect.

---

## 4. Concrete Gio patterns for the four hard surfaces

All four are the same recipe: lay out a rectangle, hand-draw with `clip`+`paint`, hit-test pointer
events against grid math. Sketches below are illustrative (Gio v0.10 API; `op`, `clip`, `paint`,
`pointer`, `f32`).

### 4a. 16-pad grid that lights up

```go
// pads laid out 4x4; light a pad when active or pressed.
func drawPads(gtx layout.Context, pads [16]bool, cell int) layout.Dimensions {
    for i := 0; i < 16; i++ {
        col, row := i%4, i/4
        x, y := col*cell, row*cell
        r := image.Rect(x+2, y+2, x+cell-2, y+cell-2)
        col := padColor(pads[i])                 // bright neon if on, dim if off
        stack := clip.RRect{Rect: r, SE: 6, SW: 6, NW: 6, NE: 6}.Push(gtx.Ops)
        paint.ColorOp{Color: col}.Add(gtx.Ops)
        paint.PaintOp{}.Add(gtx.Ops)
        stack.Pop()
        // register a pointer area per pad for tap-to-trigger / toggle
        pointer.InputOp{Tag: padTag(i), Kinds: pointer.Press | pointer.Release}.Add(gtx.Ops)
    }
    return layout.Dimensions{Size: image.Pt(4*cell, 4*cell)}
}
```
becky already does this in `cmd/canvas/gui_drum.go` (4×16). For "lights up" use a short decay timer:
on trigger set a brightness float, decay it each frame, map to color — and call `op.InvalidateOp{}`
to keep animating at 60 fps.

### 4b. Piano roll with draggable notes + velocity lane

- **Coordinate system:** `x = (tick - scrollTick) * pxPerTick`, `y = (highNote - pitch) * rowH`.
  Draw the key-lanes (alternating white/black-key tint) as full-width rects, then bar-lines via
  `clip.Stroke`, then each note as a filled `clip.RRect`.
- **Notes:** one rect per `dawmodel` note; fill = velocity-mapped or track color.
- **Drag:** a `pointer.InputOp` per note (or one global area + hit-test). On `pointer.Drag`,
  convert dx/dy back to tick/pitch (snap to grid via existing `dawmodel/quantize.go`), update the
  model, redraw. Resize by hit-testing the right 6px edge (same idiom becky-clip uses for clip
  trim).
- **Velocity lane:** a strip under the roll; for each note draw a vertical bar whose height = velocity;
  vertical drag in the lane edits `note.Velocity`.

```go
for _, n := range clip.Notes {
    x := f32.Pt(float32(n.Start-scroll)*pxPerTick, float32(hi-n.Pitch)*rowH)
    w := float32(n.Dur) * pxPerTick
    rect := clip.RRect{Rect: image.Rect(int(x.X), int(x.Y), int(x.X+w), int(x.Y)+int(rowH)-1), SE:3,SW:3,NW:3,NE:3}
    st := rect.Push(gtx.Ops)
    paint.ColorOp{Color: velColor(n.Velocity)}.Add(gtx.Ops); paint.PaintOp{}.Add(gtx.Ops)
    st.Pop()
}
```
**Reference to study:** Sequentity (`Sequentity.h`, ~1k lines, MIT, Dear ImGui) [10] — read it for
the *interaction model* (drag/crop/scale events, zoom/pan, overlapping-event priority, minimap) even
though the draw calls are ImGui; the math and UX translate 1:1 to Gio `clip`/`paint`.

### 4c. Mixer faders + meters

- Fader = a vertical track rect + a draggable thumb rect; map thumb-Y to dB. `pointer.Drag` on the
  thumb tag updates `dawmodel/mixer.go` channel gain.
- Meter = a thin rect filled to a height proportional to the engine's current RMS/peak for that
  channel (read each frame from the audio engine's level snapshot); color-ramp green→yellow→red.
  Invalidate every frame while transport runs.

### 4d. Waveform display

becky already has `cmd/canvas/gui_waveform.go`. The pattern: precompute min/max peaks per pixel
column from the sample buffer (a peak cache), then draw one vertical line per column with
`clip.Stroke`/a thin rect — never iterate raw samples per frame. Overlay a playhead rect at the
transport position.

### 4e. Drag-and-drop of samples (the one genuine Gio rough edge — be honest)

- **In-app DnD** (drag a pad/clip/sample chip within the window): use Gio core `io/transfer`
  (`transfer.SourceOp` / `transfer.TargetOp`) [1][7].
- **OS file drop** (drag a `.wav` from Explorer onto a pad): Gio core support for external OS file
  drop is **weak/incomplete** [7] (long-standing upstream gap). Mitigations becky already uses:
  (1) the `gio-plugins/explorer` package for a native file *picker* [8]; (2) the `IDropTarget`
  WinAPI shim becky began in `dragdrop_windows.go` (registers a COM drop target on the Gio HWND).
  Note the §6 caveat: a prior in-window `IDropTarget` registration crashed at launch because OLE was
  set up on a migrating goroutine — the correct fix is to register on Gio's window thread via a
  C-side target (documented in that file). **This is the single area where Dear ImGui (whose GLFW/
  SDL backend hands you OS file-drops for free) would be easier** — weigh it, but it is one widget's
  worth of friction against a whole stack change, and the picker path already works today.

---

## 5. License check on every recommended dependency / reference

| Item | License | Safe to use/vendor? |
|---|---|---|
| Gio (gioui.org) | MIT + Unlicense [1] | Yes |
| gio-plugins (explorer/transfer) | MIT [8] | Yes |
| cimgui-go (if ever needed) | MIT [2] | Yes |
| giu | MIT [3] | Yes |
| Ebiten | Apache-2.0 [4] | Yes |
| Sequentity (reference only) | single-header, permissive (study, don't vendor) [10] | Reference only |

---

## Sources

1. Gio package docs / pkg.go.dev — license (MIT + Unlicense), Windows D3D11 backend, `io/transfer`
   DnD, pure-Go: https://pkg.go.dev/gioui.org  and  https://gioui.org/
2. cimgui-go — MIT, cgo binding, prebuilt backends (GLFW+OpenGL / SDL2 / Ebiten / RayLib / DRM-EGL),
   v1.5.0 May 2026: https://github.com/AllenDang/cimgui-go
3. giu — MIT, cgo, GLFW+OpenGL 3.2+, mingw-w64 ≥ v12 on Windows, HiDPI auto-scaling, v0.15.0
   Jun 2026: https://github.com/AllenDang/giu
4. Ebitengine — Apache-2.0, pure Go on Windows (no C compiler), DirectX:
   https://ebitengine.org/  and  https://github.com/hajimehoshi/ebiten
5. Gio repo (backends, ~2.2k stars): https://github.com/gioui/gio
6. Dear ImGui — `ImDrawList` custom drawing, `imgui_impl_dx11` C++ backend, docking, `InputText`:
   https://github.com/ocornut/imgui  and  https://github.com/ocornut/imgui/docs/FAQ.md
7. Gio drag-and-drop status (internal transfer protocol exists; external OS file-drop weak):
   https://todo.sr.ht/~eliasnaur/gio/153  and  https://github.com/gioui/gio/pull/111
8. gio-plugins (explorer / file picker): https://github.com/gioui-plugins/gio-plugins
9. Dear ImGui DPI font scaling (`io.ConfigDpiScaleFonts`, docking branch):
   https://github.com/ocornut/imgui/docs/FAQ.md
10. Sequentity — single-file immediate-mode sequencer/piano-roll widget for Dear ImGui (~1k LOC,
    264 stars), excellent interaction-model reference: https://github.com/alanjfs/sequentity
