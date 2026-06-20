# SPEC-BECKY-REAPER.md — the AI-first DAW: becky drives REAPER

> **Status: BUILT + PROVEN on Jordan's machine (2026-06-20).** This is the
> resolution of "build me a functional AI-first DAW." Read `becky-fork-first-pivot`
> (memory) and `GUI-RULES.md` first for the strategy context.

## 1. The decision (and the communication error it fixes)

For days the plan was to **hand-build a Cubase clone in Go/Gio** (`cmd/canvas`):
drum machine, piano roll, mixer, VST host — all from scratch. `CANVAS-BLUEPRINT.md`
itself admits the result is "wired to the weakest of four models," the drum grid is
"a 4-lane toy," and "piano roll, mixer, and VST are not in the window at all." That
is months of work to *maybe* match Cubase, and Jordan still had no usable app.

Jordan's own instruction: *"just download an opensource daw but give yourself
complete control of it."* And the decisive fact: **REAPER 7.69 is already installed**
(`C:\Program Files\REAPER (x64)\reaper.exe`). REAPER is the most scriptable pro DAW
in existence — plain-text `.rpp` project format, full Lua/Python ReaScript API,
hosts every VST Jordan owns (Serum 2, TAL-Drum, Maschine 2, Ozone 11, Pro-Q).

**Decision: REAPER is the DAW Jordan opens; becky is the AI brain that authors and
drives it.** Same fork-first pivot already applied to video (kdenlive) and drums
(Hydrogen) — now applied to the audio DAW, using a tool that was already on disk.
This does NOT replace the GUI-RULES native stack or `cmd/canvas`; it is the
pragmatic, working DAW *today* while those remain longer-horizon.

## 2. What was built — `becky-reaper` (+ `internal/reaper`)

A deterministic Go tool that turns becky's arrangement model into a REAL, openable,
renderable REAPER project.

- `internal/reaper/reaper.go` — deterministic `.rpp` **writer**: tracks, named bus
  **folders** (Cubase-style summing busses via ISBUS), gain/pan/mute/solo, audio
  items (WAV), MIDI items (notes -> the `E`-event delta list at 960 PPQ), and an
  optional built-in **ReaSynth** instrument on MIDI tracks (exact FX state copied
  from REAPER's own ground-truth) so a rendered MIDI track is **audible with zero
  plugin guessing**. Same input -> byte-identical output (becky house rule).
- `internal/reaper/arrangement.go` — `FromArrangement(dawmodel.Arrangement)` maps
  becky's editable session onto a REAPER Project (bus grouping is one level deep);
  `JordanTemplate()` emits his Cubase bus tree (from the cubase1-7 screenshots);
  `DemoProject()` is a tiny audible synth-bass riff.
- `cmd/becky-reaper/main.go` — CLI: `template` / `demo` / `build --in arr.json` /
  `render`. `--render` shells out to REAPER (`-renderproject`, `BECKY_REAPER`
  overrides the path) and degrades cleanly if REAPER is absent.
- `internal/reaper/reaper_test.go` — 8 tests (determinism, header/structure, ISBUS
  folders, MIDI delta+hex encoding, 480->960 scaling, FromArrangement bus folders,
  audible-demo, quoting). All green; `go build/vet/test ./...` + gofmt clean.
- `Open Becky DAW.bat` + `open-becky-daw.ps1` (repo root) — one-click: becky authors
  the session and opens it in REAPER. ASCII-only, parse-checked under PS 5.1.

## 3. Proof (the becky standard — pasted evidence, not "it compiled")

All verified locally on 2026-06-20 (scratch in `becky-reaper-work/`):

1. **REAPER renders a becky-driven session to AUDIBLE audio.** A becky ReaScript
   built a 3-track session + rendered a 24-bit/48k stereo WAV: ffprobe = `pcm_s24le
   48000 2ch 2.0s`; ffmpeg volumedetect = **mean -13.7 dB / max -6.8 dB** (silence
   floor is -91 dB). Two corroborating signals.
2. **becky-GENERATED `.rpp` files open correctly in REAPER.** Loaded via REAPER
   (CLI arg) and enumerated through ReaScript:
   - `demo.rpp` -> 2 tracks, `BASS_bus folderdepth=1` / `Synth Bass folderdepth=-1`,
     tempo=132, `ok=true`.
   - `jordan_template.rpp` -> **17 tracks**, all 5 bus folders open/close correctly
     (DRUMS_bus{Kick,Snare,OH,Claps} GUITARS_bus{Gtr L,Gtr R} BASS_bus{Synth Bass,Sub}
     VOCALS_bus{Lead Vox,Backing Vox,Screams} FX_bus{Cymbal Transitions}), tempo=132.
   This mirrors Jordan's actual Cubase bus tree.

## 4. Two control mechanisms (strengths)

- **`.rpp` writer (deterministic, no REAPER needed; CI-safe):** the STRUCTURE —
  tracks, bus folders, tempo, MIDI, audio items, routing. CANNOT embed arbitrary
  third-party VSTs (their binary state must be instantiated by REAPER).
- **ReaScript / Lua (run inside REAPER):** can do EVERYTHING incl.
  `TrackFX_AddByName(tr, "Serum 2", ...)` on his REAL plugins. This is the path to
  load Serum/TAL-Drum/Maschine/Ozone. Proven mechanism: a script REAPER runs at
  launch (the probe used `__startup.lua`, deleted after).

## 5. Cloud vs local

- **Cloud-buildable + CI-tested:** the entire `internal/reaper` writer + CLI + tests
  (pure Go, deterministic, no REAPER/audio).
- **Local-only (needs REAPER + audio HW):** rendering, opening, and loading his real
  VSTs via ReaScript. All of section 3 was local-verified.

## 5b. FIELD FINDING (reaper1.jpg, 2026-06-20) — the live blocker

Jordan's screenshot (`reaper1.jpg`, on `master`) shows the generated `becky-session.rpp`
**open in REAPER with its bus tree**, plus a **"REAPER Chat"** AI-control extension already
installed ("Ask me to control your DAW: add tracks, set FX parameters, create MIDI"). He
typed *"change tempo to 128, add a four-on-the-floor kick for ~2 min, arm the vocal track"*
and it failed: `Error: Failed to connect to http://localhost:11435/v1/chat/completions`.

**Diagnosis (verified locally):** nothing was listening on 11435 (or 11434). The fix is a
**llama.cpp `llama-server`** bound to **port 11435** serving the OpenAI-compatible
`/v1/chat/completions` (the becky standard — see `internal/llmlocal`). **Do NOT use Ollama**
(Jordan's explicit, repeated requirement). Concretely:
`llama-server -m <model.gguf> --port 11435 --host 127.0.0.1` (any chat GGUF on disk).
Then REAPER Chat's natural-language DAW control works. A one-click "start becky's REAPER
brain" launcher (boot `llama-server` on 11435 with a resident GGUF) is the immediate win.

## 6. Next steps (honest, prioritized)

0. **Wire REAPER Chat to llama-server on :11435** (see §5b) — the live blocker; do this first.
1. **ReaScript VST emitter** — generate a `.lua` from the arrangement that loads his
   real plugins onto tracks (`TrackFX_AddByName`) so a generated session opens with
   Serum/TAL-Drum/Maschine already inserted. (The hard, high-value next piece.)
2. **Headless render that isn't blocked by the eval-license nag** — REAPER's
   unregistered nag is a modal; a registered REAPER (Jordan owns a license he can
   add) removes it, making `-renderproject` reliable for batch bounces.
3. **`FromArrangement` routing fidelity** — nested sub-busses (kick_bus inside
   Drums_bus) and sends (vs one-level folders); audio-clip file paths.
4. **Wire `becky-wire` / `becky-drum` / `becky-compose` output** straight into a
   `becky-reaper build` so a plain-English request becomes an open REAPER session.

## 7. Open decisions for Jordan

- Add your REAPER license (kills the eval nag -> reliable headless renders).
- Confirm REAPER as the primary AI-first DAW surface (this spec assumes yes); the
  Gio `cmd/canvas` work stays a longer-horizon native option, not a blocker.
