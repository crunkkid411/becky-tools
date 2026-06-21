# LOCAL WORK ORDER — becky-canvas drum machine: make it SOUND, prove each step

**This file exists because the handoff kept failing.** Cloud researched + built + wrote
"LEFT FOR LOCAL: wire up the audio" as vague prose; local merged the branch, ran the
tests green, and the runtime wiring never happened. So this is not prose — it is an
**ordered checklist of commands**. Each item has the EXACT command, the EXPECTED output,
what "done" looks like, and what to paste back. **Do them in order. Do not skip. Do not
mark the branch done until every box is checked and the evidence is in CLAUDE.md §6.**

The good news: the deterministic chain is already built AND proven by cloud (Step 1
below renders real audio offline). Your job is to run it on the real hardware/kit, build
the live-audio + GUI variants, and confirm the window makes sound. You are *connecting
proven pieces*, not building from scratch.

Branch: `claude/project-completion-9jvjwj`. All commands run from the repo's `becky-go/`
directory unless noted. Windows / PowerShell.

---

## THE FASTEST WIN — do this FIRST (pure Go, no special build, no GUI)
becky now has a one-command PIPE that reads PLAIN ENGLISH: phrase → a full song you can
play. No `-tags audio`, no mingw, no window. If nothing else in becky has ever worked, this will.
```
cd becky-go
go run ./cmd/song "dark trap at 140, 8 bars"
```
Out comes `trap-song.wav` (play it), `trap-song.json` (open in becky-canvas), and
`trap-song.mid` (open in Cubase/FL/Ableton/REAPER). Try:
- `go run ./cmd/song "happy lo-fi in C major" --variations 4`  → 4 different ideas
- `go run ./cmd/song "aggressive metalcore, just drums" --save-as my-beat`  then  `... --template my-beat`
- [ ] **DONE WHEN:** the .wav plays a 4-layer song and `becky heard:` echoes what you typed.
  Cloud proved it: "dark trap at 140 8 bars" → 14.7s, peak −1.0 dBFS, valid 5-track MIDI.
  **Report that you heard it.** This is the whole pipe — intent → beatgen → arrange →
  evaluate → render — in one command.

## Step 0 — Build the deterministic layer (must already be green)
```
cd becky-go
go build ./... ; go vet ./... ; go test ./...
```
- [ ] **DONE WHEN:** all three succeed (gofmt CRLF-only noise is fine). Paste the last line.

## Step 1 — PROVE the drum audio offline (no GUI, no audio device) — cloud already proved this
This renders the EXACT chain the canvas ▶ uses (`MachineFromArrangement` + your default
kit) to a WAV. It is pure Go — it works without the audio build.
```
go run ./cmd/beat new --genre house --seed 3 --out %TEMP%\beat.json
go run ./cmd/daw-engine --render-arrangement %TEMP%\beat.json --out %TEMP%\beat.wav
ffprobe -hide_banner %TEMP%\beat.wav
ffmpeg -hide_banner -i %TEMP%\beat.wav -af volumedetect -f null NUL 2>&1 | findstr "mean_volume max_volume"
```
- [ ] **DONE WHEN:** the engine prints `... N audible pads`, and volumedetect shows a
  mean above ~−40 dB (real sound). It uses `X:\AI-2\becky-tools\samples\kit\{kick,snare,
  hat,clap}.wav` — if that folder is missing, set `BECKY_DRUM_KIT` to a folder that has
  those 4 files first (`$env:BECKY_DRUM_KIT="X:\path\to\kit"`). **Paste the volumedetect line.**
- If it's silent: the kit folder is the problem, not the code (cloud proved the code on a
  synthesized kit: peak −2.1 dBFS). Fix the kit path and rerun.

## Step 2 — Build the REAL binaries Jordan double-clicks
The live audio engine needs the `audio` build tag + the mingw C compiler. THIS is the
step that silently gets skipped — do not skip it.
```
$env:CC="C:\msys64\mingw64\bin\gcc.exe"
go build -tags audio -o bin\becky-daw-engine.exe ./cmd/daw-engine
go build -tags gui -ldflags "-H windowsgui" -o bin\becky-canvas.exe ./cmd/canvas
go build -tags gui -ldflags "-H windowsgui" -o bin\becky-drummachine.exe ./cmd/drummachine
```
- [ ] **DONE WHEN:** all three exes exist in `bin\`. Confirm `becky-daw-engine.exe` is the
  audio build: `.\bin\becky-daw-engine.exe --render-arrangement %TEMP%\beat.json --out %TEMP%\b2.wav`
  still works (the audio build keeps the offline path). Paste the exe list (`dir bin\*.exe`).
- NOTE: `build-all-tools.bat` builds the headless/stub variants for some tools — for the
  drum chain you MUST use the explicit `-tags audio` / `-tags gui` commands above.

## Step 3 — SOUND-CHECK the canvas drum machine (the whole point)
```
.\bin\becky-canvas.exe
```
Then in the window:
1. Click the **Drum machine** dock button → a starter beat (4-on-floor kick + backbeat +
   hats) appears in the grid (this is `ensureDrumMachineArr`, already wired).
2. Click **▶ Play**.
- [ ] **DONE WHEN:** you HEAR the beat through the UR12 (it routes through
  `--play-machine`, the sampler — same chain as Step 1). If the window opens but is
  silent, run Step 1's command again to confirm the engine+kit are fine, then check
  `becky-daw-engine.exe` sits next to `becky-canvas.exe` in `bin\`. **Report: heard it / a
  screenshot / the failure.**
3. Type in the agent box, pressing Enter after each, confirm each applies:
   `make a house beat` · `add a bassline` · `set bpm to 128` · `mute the bass` · `undo` ·
   `save as my-test`.
- [ ] **DONE WHEN:** each line changes the session (and `save as` writes `my-test.json`).
  Report which worked.

## Step 4 — The remaining REAL feature: the in-canvas kit browser
Today the canvas sounds the 4 default voices. To map ANY of Jordan's 43k samples onto the
16 pads, port the kit browser that ALREADY exists in the standalone drum machine:
- Source: `cmd/drummachine/gui_kit.go` (`startLoadKitFolder`, `startScanBrowser`,
  `assignSampleToPad`, `layoutBrowserPanel`) — it browses `X:\music-2\SAMPLES` / `X:\Splice`.
- Target: add a browser panel + "load kit / assign sample to pad" to the canvas drum
  panel (`cmd/canvas/gui_drumpanel.go`), writing the chosen sample paths onto the pads of
  the `Machine` that `MachineFromArrangement` produces (extend it to take an optional kit,
  or assign via a new `ctledit` op so it's also agent-operable per STANDARDS-CANVAS-UX §3).
- [ ] **DONE WHEN:** in becky-canvas you can pick a sample folder, the 16 pads show real
  sample names, ▶ plays them, and `save` persists the kit. Screenshot + "heard my 808".

## Step 5 — Report back, honestly
Update CLAUDE.md §6 with: which boxes are checked, the pasted evidence (volumedetect line,
exe list, "heard it"/screenshot), and anything that did NOT work with the exact error.
**Do not write "LEFT FOR LOCAL: nothing" unless Steps 1–4 are all checked with evidence.**
If a step fails after a real attempt, say so plainly with the error — a stuck step reported
honestly is worth more than a green checkmark that isn't true.
