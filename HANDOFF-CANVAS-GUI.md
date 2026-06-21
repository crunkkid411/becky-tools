# HANDOFF — Becky Canvas GUI: wire the PROVEN engines into the window

> **>>> LOCAL: do NOT merge-and-stop. This is an ordered, checkboxed work order. Drive it
> to completion box-by-box and paste the evidence under each step (a screenshot, an ffprobe
> line, "I clicked it and X happened"). A stuck box reported honestly beats a green check
> that isn't true. <<<**

**What this is.** Six panel-by-panel jobs that wire **engines that already exist and are
already proven offline** into the **existing Gio window** (`becky-go/cmd/canvas`, `-tags gui`).
You are **WIRING PROVEN PIECES INTO PANELS — never inventing logic.** Every step names the exact
existing file/function to call, gives a **one-command offline proof you run FIRST** (so you
confirm the engine before you touch Gio), and a **DONE-WHEN** that is the `CANVAS-NORTH-STAR.md`
§2 standard: *window opens / ▶ Play makes sound / the button does what it says / no freeze.*

**Why a handoff like this exists.** Past GUI handoffs were prose that got merged-and-skipped
(`HANDOFF-TEMPLATE.md`). This one obeys the **provable-handoff** rule: cloud already RAN each
offline proof and pasted the numbers below; your job is to confirm the same number on your
machine, then make the window do it.

**Read before touching a panel:** `CANVAS-NORTH-STAR.md` (the DoD checklist), `STANDARDS-CANVAS-UX.md`
(§3 dual human+agent operability — every button also needs a `ctledit` op), `CANVAS-BLUEPRINT.md`
(the one `dawmodel.Arrangement` spine). Run all commands from `becky-go/`.

---

## The single spine (so nothing here invents a new model)

Everything edits ONE `*dawmodel.Arrangement` held on the `App` (`cmd/canvas/gui.go:143  arr *dawmodel.Arrangement`).
The ONLY commit path is `App.applyArr` (`gui_spine.go:169`) — it swaps the arrangement, rebuilds
the derived scene via `canvasbridge.SceneFromArrangement`, pushes undo history (`internal/undo`),
and invalidates the window. **Every panel action you wire MUST end in `a.applyArr(next)`** so it
is undoable and redrawn. The panels already do this (see `gui_mixerpanel.go:105`). Do not bypass it.

The build the user runs (Windows, the step that silently gets skipped):
```
cd becky-go && build-all-tools.bat
```
For the GUI exe specifically the canvas builds `-tags gui -ldflags "-H windowsgui"` (no console
flash — see CLAUDE.md §3). The audio actually comes out of the **sibling** `becky-daw-engine.exe`,
which the canvas execs (`gui_play.go:191 runEngine`) — keep the canvas a pure cgo-free `-tags gui`
build; sound lives in the engine exe.

---

## Step 0 — deterministic layer green (do this once, first)
```
go build ./... && go vet ./... && go test ./... && gofmt -l .
go build -tags gui ./cmd/canvas        # the GUI must compile
```
- [ ] **DONE WHEN:** all pass; `gofmt -l .` prints nothing (CRLF-only complaints on Windows are
      cosmetic per CLAUDE.md §4). Paste the last line of each. The canvas `-tags gui` build is the
      gate that catches "it compiles but not with the GUI tag."

---

## Step 1 — "make a song from a phrase" → ▶ plays (CONFIRM the wired flow)

This is **already wired** in `gui_spine.go:95 maybeBuildSong` (called from `applyPhrase`, which the
agent box routes to). Typing *"make a dark trap song"* calls `songbuild.BuildPhrase` → `applyArr`,
switches to `canvas.ModeDrum`, and prints "built a song … ▶ to hear it". ▶ then plays it:
`gui_play.go:144 execPlay` routes a drum clip through `drummachine.MachineFromArrangement` +
`--play-machine` (the real sampler engine), and pitched tracks through `--play-pattern-audio`.
**Your job is to CONFIRM it builds AND sounds — not to write it.**

**Offline proof FIRST (cloud ran this — match the number):**
```
go run ./cmd/song "dark trap" --out /tmp/darktrap        # → .wav + .json + .mid
# measure it:
ffprobe -v error -show_entries stream=codec_name,sample_rate,duration -of default=nw=1 /tmp/darktrap.wav
```
- **Cloud's result:** `✓ darktrap.wav … 4 tracks, 199 notes`, WAV = **1,508,618 bytes** (≈8.5s @
  44.1k stereo). Same pipe (`songbuild.BuildPhrase` → `audioengine.RenderArrangementWAV`) the
  canvas uses. Paste yours.
- [ ] **DONE WHEN (engine):** the `.wav` is non-empty (volumedetect mean above −40 dB) and the
      `.json` loads (it's a real `dawmodel.Arrangement`).
- [ ] **DONE WHEN (window):** launch becky-canvas, type **"make a dark trap song"** in the ONE
      agent box → you see "becky: built a song (4 tracks, 199 notes)" and the drum grid fills →
      click **▶** → **you HEAR it**; **■ Stop** silences it. Window never freezes during the
      build/play (both run off the UI thread already — `runCommand` goroutine + `runEngine`).
      Paste a screenshot + "heard it / didn't".
- If no sound: `becky-daw-engine.exe` must sit next to `becky-canvas.exe` (`runEngine` resolves it
  as a sibling) — that's `build-all-tools.bat`, not a code bug. Drum samples resolve from
  `defaultKitDir()` = `X:/AI-2/becky-tools/samples/kit` (or `$BECKY_DRUM_KIT`); sine fallback if absent.

---

## Step 2 — MIXER "Route" action: tracks land on their buses (autoroute → panel + ctledit op)

The mixer panel (`gui_mixerpanel.go`) already shows one strip per track with fader/pan/mute/solo
and a **per-track bus-cycle button** (`busCycle` → `a.arr.RouteTo`), plus a bus→out routing summary
(`layoutRoutingSummary`). What's MISSING is a one-click **"Route"** that applies becky's deterministic
label→bus ruleset to the WHOLE arrangement at once. The engine exists: `autoroute.Apply(arr,
autoroute.Load())` (`internal/autoroute/autoroute.go:104`) — it ensures the bus tree and routes
every track by its label (drums→DRUMS, bass→BASS, etc.). **Labels are the routing contract** (UX
law #6) — autoroute reads `track.ID`/label; do not rename tracks to fight it.

**Offline proof FIRST (cloud ran this — match it):**
```
go run ./cmd/route apply --project /tmp/darktrap.json --out /tmp/darktrap_routed.json
```
- **Cloud's result:**
  ```
    drums  → DRUMS
    bass   → BASS
    chords → SYNTH
    melody → SYNTH
  ✓ routed 4 tracks → 7 buses, wrote darktrap_routed.json
  ```
  Paste yours.

**Wire it (two surfaces — STANDARDS-CANVAS-UX §3 requires BOTH):**
1. **The `ctledit` op (do this first so the agent box gets it for free).** `internal/ctledit` has
   `OpRouteTo` (single track) but **no whole-arrangement route op**. Add `OpRoute = "route"` in
   `internal/ctledit/types.go`, a `case OpRoute:` in `apply.go` that calls `autoroute.Apply(arr,
   autoroute.Load())` and returns the routed arrangement (drop-illegal-with-a-reason, never panic —
   match the existing cases). Add a phrase trigger in `ctledit/phrase.go` ("route the tracks" /
   "set up the buses"). Add a table test asserting the resulting `track.Strip.Bus` values
   (assert VALUES, not truthiness — STANDARDS-ENGINEERING).
2. **The panel button.** Add a `route widget.Clickable` to `mixerPanel`, render a **"Route"** button
   in `mixerHeader` (use the existing `layoutToggleBtn`/neon affordance — no new colors,
   STANDARDS-CANVAS-UX §1), and in `handleStripEvents` on `route.Clicked(gtx)` call
   `next, _ := autoroute.Apply(a.arr, autoroute.Load()); a.applyArr(next); return`. Same commit path
   as every other strip edit.
- [ ] **DONE WHEN (engine):** the offline proof prints the 4 assignments above.
- [ ] **DONE WHEN (window):** open the dark-trap session → **Mixer** dock button → click **Route**
      → the strips' bus labels change to DRUMS/BASS/SYNTH and the routing summary lists the buses;
      typing **"route the tracks"** in the agent box does the same; **undo** ("undo" in the box)
      reverts it. Screenshot before/after.

---

## Step 3 — FX-chain view per bus (show the chain from ~/.becky/fxchains.json)

Show each bus's insert-slot chain even before any plugin actually loads — it's the visible contract
for "my routing/plugins applied at the end" (HANDOFF-ROUTING-CANVAS.md). The data already exists:
`internal/fxchain` reads `~/.becky/fxchains.json` (`fxchain.Load()`, `fxchain.Path()`), exposes
`Chains.Buses()` and `Chains.Get(bus) Chain`, and `DefaultChains()` for first-run. **You are only
displaying it — read-only this step.**

**Offline proof FIRST:**
```
go run ./cmd/fxchain init     # writes ~/.becky/fxchains.json with defaults if absent
go run ./cmd/fxchain list     # prints the buses that have a chain
go run ./cmd/fxchain show --bus DRUMS   # prints the insert slots for one bus
```
- [ ] **DONE WHEN (engine):** `list`/`show` print the per-bus insert slots (paste them).

**Wire it (panel, read-only):** in `gui_mixerpanel.go`, under each strip's bus-cycle button (or in a
collapsible row under `layoutRoutingSummary`), render the insert slots for that strip's
`t.Strip.Bus` from `fxchain.Load().Get(bus).Plugins` as small slot chips (plugin name; muted opacity
= bypassed, per the fixed state-color table in STANDARDS-CANVAS-UX §1). Load the chains ONCE per
frame into a field, not per-strip, to avoid disk thrash. No editing yet — a later step adds insert
via `fxchain.Add`; flag that clearly in a TODO.
- [ ] **DONE WHEN (window):** Mixer panel shows each bus's FX slots as chips; an empty/uninitialized
      `fxchains.json` shows a quiet "no chain" hint (degrade-never-crash). Screenshot.

---

## Step 4 — "Bounce" a track to audio (render-through → an audio track)

A **Bounce** button on a track renders that track's MIDI through the synth/kit to a WAV and replaces
it with a `dawmodel.KindAudio` track pointing at the WAV — the standard "freeze/bounce" DAW feature.
The render engine exists and is proven: `audioengine.RenderArrangementWAV(arr, path, sampleRate,
loops)` (`internal/audioengine/render_song.go:63`), exposed on the CLI as
`becky-daw-engine --render-song <project.json>` (whole mix) and `--render-arrangement` (drum clip).
**The render is the proven piece — you wire it to a button + build the swap-in.**

**Offline proof FIRST (this is the exact engine the button calls):**
```
go run ./cmd/daw-engine --render-song /tmp/darktrap.json --out /tmp/bounce.wav
ffprobe -v error -show_entries stream=codec_name,sample_rate -of default=nw=1 /tmp/bounce.wav
```
- **Cloud's note:** `--render-song` calls the same `RenderArrangementWAV` pure-Go path as
  `becky-song`; cloud confirmed `becky-song` writes a 1.5 MB audible WAV from this project (Step 1).
  Paste your bounce WAV size + codec.
- [ ] **DONE WHEN (engine):** `/tmp/bounce.wav` is a non-empty WAV (codec `pcm_s16le`).

**Wire it (panel + ctledit op):**
1. **Track-render helper:** add a small canvas helper that, for one track, writes a temp
   single-track project.json and execs `becky-daw-engine --render-song <tmp> --out <wav>` via the
   existing `runEngine` pattern (`gui_play.go:191`) — OFF the UI thread (goroutine; post the result
   back + `Invalidate`). When it returns, build a new arrangement with that track replaced by a
   `dawmodel.Track{Kind: KindAudio, ...}` referencing the WAV, and `a.applyArr(next)`.
2. **Bounce button** on each mixer strip (small icon, neon affordance) calling that helper for
   `t.ID`. **And** an `OpBounce = "bounce"` in `internal/ctledit` so "bounce the drums" works in the
   agent box (the op can stage the same render or, if you keep render canvas-side, the op marks the
   track for bounce and the canvas performs it — document which, and test the resulting track Kind).
- [ ] **DONE WHEN (window):** click **Bounce** on a track → window stays responsive ("bouncing…"
      hint) → the track becomes an audio track and the audio panel shows its waveform; **▶** still
      plays the session. Undo reverts to the MIDI track. Screenshot + "heard it".

---

## Step 5 — Save / Load / Undo: CONFIRM + surface as buttons

All three are **already wired** (`gui_spine.go`): `saveSession` (`:136`, writes the arrangement
JSON beside the session), `maybeLoadArrangement` (`:223`, loads a project.json/.mid into the spine),
and `undo`/`redo` (`:194`/`:207`, over `internal/undo` via `applyArr`'s history push). They are
reachable today by **typing** "save" / "save as X" / "undo" / "redo" in the agent box (`applyPhrase`
`:57-70`). The gap is **visible buttons** — a creative should not have to type "undo".

**Offline proof FIRST (the logic is already unit-tested):**
```
go test ./internal/undo/... ./cmd/canvas/... 2>&1 | tail -5     # the !gui spine tests
```
- [ ] **DONE WHEN (engine):** undo/spine tests pass. Paste the last line.

**Wire it (panel/dock):** add `saveBtn`, `loadBtn`, `undoBtn`, `redoBtn` `widget.Clickable`s to the
`App` (near `playBtn`/`stopBtn`, `gui.go:125`), render them in `layoutTransport` (or a small toolbar
row), and on click call the EXISTING methods: `a.saveSession("")`, `a.startExplorerAwareImport()`
(the existing Open path, `gui.go:542`), `a.undo()`, `a.redo()`. Honor the keyboard law too
(STANDARDS-CANVAS-UX §2): **Space = play/pause, Enter = stop, Ctrl+Z = undo** regardless of focus —
the agent overlay already uses Esc/Enter; add Ctrl+Z to the window key filter.
- [ ] **DONE WHEN (window):** Save writes a file (a "saved → name.json" line appears) you can Load
      back; Undo/Redo buttons step the arrangement and the panels redraw; Ctrl+Z works. Screenshot.

---

## Step 6 — The UX LAWS (non-negotiable — obey while wiring every step above)

From the user, verbatim intent. Violating one is a defect even if it "works":

1. **NO decision menus.** Produce **one** result, not a list of options to choose from. If the user
   asks for an alternative, cycle with **"next"** only — never present a picker. (Step 1's
   `maybeBuildSong` returns one song; keep it that way.)
2. **Colors & shapes > text.** Use the theme + the fixed state-color table (Green=active,
   Red=record/destructive, Amber=solo/warn, Blue=selected, muted=bypassed — STANDARDS-CANVAS-UX §1).
   **No hardcoded hex in panels.** New buttons reuse the existing neon affordances (`overlayBtn`,
   `layoutToggleBtn`), not new widgets with new colors.
3. **Everything drag-drop.** Dropping a project.json/.mid/wav onto the window or a panel loads it
   (`maybeLoadArrangement`); don't add a modal where a drop would do.
4. **ONE agent box.** There is exactly one input line (`gui.go` agent box → `runCommand`/`applyPhrase`).
   Do not add a second chat/console. Every new capability is also reachable by typing in this one box.
5. **"Show me, don't do it" overlay.** Edits the agent proposes preview first; the human approves
   (Enter) or rejects (Esc) before `applyArr` commits. Keep destructive/large edits on this path.
6. **Labels are the routing contract.** `autoroute` (Step 2) routes by track label; the mixer shows
   tracks ON their buses. Never silently rename a track to change its routing — change the route.

**Dual-operability gate (STANDARDS-CANVAS-UX §3):** for Steps 2 and 4 the new button is **not done**
until the matching `ctledit` op exists, is undoable (it goes through `applyArr`), and has a test that
asserts the resulting `Arrangement` values.

---

## Step 7 — report back honestly

Update `CLAUDE.md` §6 with: which boxes are checked, the pasted ffprobe/test numbers, the
screenshots, and any box you could NOT complete with the exact error. **Do not write "LEFT FOR
LOCAL: nothing" unless every box above is checked with evidence.** Then `build-all-tools.bat` and
confirm `becky-canvas.exe` + `becky-daw-engine.exe` are fresh.

### Evidence cloud already produced (so you know the engines are real before you start)
- `go run ./cmd/song "dark trap"` → `darktrap.wav` **1,508,618 bytes** + `darktrap.json` (4 tracks,
  199 notes) + `darktrap.mid`.
- `go run ./cmd/route apply --project darktrap.json` → `drums→DRUMS, bass→BASS, chords→SYNTH,
  melody→SYNTH`, **4 tracks → 7 buses**.
- `cmd/daw-engine` exposes `--render-song`/`--render-arrangement`/`--render-machine`/`--play-machine`
  (`cmd/daw-engine/machine.go:43-47`), the pure-Go offline bounce path used by Steps 1 & 4.
