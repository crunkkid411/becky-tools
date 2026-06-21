# HANDOFF — VST3 FX hosting in becky-canvas (host an effect on a bus, apply a saved state, render a track THROUGH a bus's FX chain)

**For the LOCAL agent (Jordan's Win10 PC). The GUI/audio comms have been poor, so this is
spelled out command-by-command per `HANDOFF-TEMPLATE.md`: each step has the exact command,
the expected output, DONE-WHEN, and what to paste back. Do NOT merge-and-stop.**

Goal (Jordan's in-canvas FX vision, three concrete capabilities):
1. **Host a VST3 EFFECT on a mix bus** (e.g. a compressor/EQ on `bus.drums`), not just an
   instrument fed notes.
2. **Apply a saved STATE chunk** to a plugin so it comes up with the dialed-in settings.
3. **Bounce-in-place**: take a track's audio (a real input WAV) → run it THROUGH a chain of
   VST3 effects (each with its saved state) → write the processed output WAV.

---

## What ALREADY works today (verified by reading the code, not by running it)

The native host is `native/audio-host/` (C++, MIT VST3 SDK v3.8.x + PortAudio, both MIT).
The Go client is `becky-go/internal/audiohost/` driving it over the NDJSON seam.

**The full verb set the host exposes** (`native/audio-host/src/main.cpp` `dispatch()`):
`ping`, `shutdown`/`quit`, `audio.devices`, `audio.open`, `audio.start`, `audio.stop`,
`vst.scan`, `vst.load`, `vst.param.list`, `vst.param.set`, `note.on`, `note.off`,
`vst.editor.open`, `vst.state.save`, `vst.state.load`, `render`.

**The Go client wraps all of them** (`internal/audiohost/client.go`): `Ping`, `Devices`,
`OpenAudio`/`StartAudio`/`StopAudio`, `ScanVST`, `LoadVST`/`LoadVSTOptions`, `ParamList`,
`SetParam`, `NoteOn`/`NoteOff`, `Render`/`RenderPath`, `SaveState`, `LoadState`/`LoadStatePath`,
`Shutdown`. (`vst.editor.open` is the only host verb with no client method — it's a Phase-3
GUI concern, honestly stubbed: the editor exists but there's no window/run-loop to attach it.)

**FX hosting on a buffer — PARTIALLY there.** `VstHost::render` (`src/vst_host.cpp` ~L622)
already detects effect vs instrument (`is_effect = no event-input bus + has audio-input bus`,
L650-652) and, for an effect, **fills the input bus with a 0.25-amplitude 220 Hz sine test
tone** (L737-751) and processes block-by-block through `processor->process()`. So the host
CAN instantiate a VST3 effect, activate its busses, and push audio buffers through it. This
is real — `--selftest` (main.cpp L125) loads + renders a real plugin and asserts non-silent.

**State apply — FULLY there and real.** `vst.state.load` (`src/vst_host.cpp` L550) opens the
`.vstpreset` file, deactivates the component (setState must run inactive), and calls
`PresetFile::loadPreset(...)` which invokes `component->setComponentState`/`setState` (reaches
the processor — same object) AND the controller's `setState`, then reactivates (L578-603). It
verifies the file's class id matches the loaded plugin. `vst.state.save` (L500) is the inverse
via `PresetFile::savePreset` and first flushes queued param edits through one silent process
block (`flush_pending_params`, L472) so live edits are baked in. **This works for ANY VST3.**

**Build story** (`native/audio-host/scripts/`): `build.ps1` resolves MinGW g++ from
`C:\msys64\mingw64\bin` (override `BECKY_MINGW_BIN`), runs `fetch-deps.ps1` if deps are
missing, then cmake-configures + builds Release → `native/audio-host/build/becky-audio-host.exe`.
`fetch-deps.ps1` shallow-clones the **MIT** VST3 SDK (`steinbergmedia/vst3sdk`, recursive),
**MIT** PortAudio, and the **MIT** nlohmann/json header into the gitignored `third_party/`.
ASIO is account-gated (drop it at `BECKY_ASIO_SDK` for low-latency to the UR12; WASAPI works
without it). `CMakeLists.txt` compiles a minimal SDK hosting slice (no vstgui) directly in.

---

## The THREE GAPS (what's missing for the vision)

**GAP 1 — `render` cannot take an input WAV. It only feeds effects a synthetic 220 Hz tone.**
`src/vst_host.cpp` L737-751 hardcodes a sine into the effect's input bus. There is NO WAV
*reader* anywhere (`wav_writer.h` only WRITES 16-bit PCM; `src/` has no wav_reader). So you
cannot yet process a real track through an effect → no bounce-in-place. **This is the #1 gap.**

**GAP 2 — no FX CHAIN. One `render` = one plugin.** Each verb operates on a single instance.
There is no verb to run audio through an ORDERED LIST of effect instances (load A, load B,
apply each one's state, pipe buffers A→B→out). The pieces exist (load, state.load, the
block-process loop) but nothing chains them.

**GAP 3 — the canvas bus model has nowhere to store an FX chain.** `internal/dawmodel/mixer.go`
`Bus` struct (L23) has `ID`, `Out`, `Sidechain` — but no insert/FX field. So a canvas mix bus
can't yet declare "compressor.vstpreset then EQ.vstpreset". The model needs an FX-chain field
before the canvas can drive bounce-in-place per bus.

The smallest addition that delivers the vision: **a `render.chain` verb** that reads an input
WAV, runs it through an ordered list of `{path, state?}` effects, and writes the output WAV —
plus a `Bus.FX []BusFX` field on the dawmodel so the canvas can describe a chain. Named files +
functions are in Step 4 below.

---

# THE WORK ORDER

Run from `becky-go/` unless noted. Windows PowerShell. The C++ build is from
`native/audio-host/`.

## Step 0 — deterministic Go layer green (no hardware)
```
cd becky-go
go build ./... ; go vet ./... ; go test ./...
```
- [ ] DONE WHEN: all three pass. Paste the last line of each. (This proves the audiohost Go
  client + dawmodel still compile before you touch the C++.)

## Step 1 — BUILD the native host (the step that silently gets skipped — do it first)
```
cd native\audio-host
.\scripts\build.ps1 -SelfTest
```
- Expected: `[fetch] ...` (first run clones the MIT SDKs into `third_party\`), `[build] OK ->
  ...\build\becky-audio-host.exe`, then selftest lines ending in
  `[selftest] RESULT: PASS` (or `PARTIAL` if your default-dir plugins render silent on a tone —
  still a pass for the host).
- [ ] DONE WHEN: `native\audio-host\build\becky-audio-host.exe` exists AND selftest prints
  `RESULT: PASS` or `PARTIAL`. Paste the `[selftest] RESULT:` line and `dir build\*.exe`.
- If g++ isn't found: install MSYS2 mingw64 or set `BECKY_MINGW_BIN`. If cmake/ninja missing:
  Strawberry Perl ships mingw32-make; the script falls back to it.

## Step 2 — PROVE state-apply on a REAL plugin (capability 2, works TODAY, no new code)
Pick one of Jordan's real effect plugins (a compressor/EQ). Set `BECKY_AUDIO_HOST` so the Go
tool finds the exe you just built:
```
cd becky-go
$env:BECKY_AUDIO_HOST="..\native\audio-host\build\becky-audio-host.exe"
go run ./cmd/becky-vst scan                      # find a real effect .vst3 path
go run ./cmd/becky-vst save-state --plugin "C:\Program Files\Common Files\VST3\<SomeEQ>.vst3" --out eq.vstpreset --json
go run ./cmd/becky-vst load-state --plugin "C:\Program Files\Common Files\VST3\<SomeEQ>.vst3" --state eq.vstpreset --out eq_applied.wav --json
```
- Expected: save-state prints `saved=true` + a `classId`; load-state prints `loaded:{applied:true}`
  and a render result. Verify the WAV is real:
```
ffprobe eq_applied.wav
```
- [ ] DONE WHEN: `save-state` wrote `eq.vstpreset` (non-zero bytes), `load-state` reported
  `applied:true`, and `ffprobe` shows a valid PCM WAV. Paste the two JSON `saved`/`applied`
  lines + the ffprobe format line. (NOTE: this proves state apply; the audio is still the
  220 Hz test tone through the effect, because Step 3's gap isn't filled yet.)

## Step 3 — ADD the gap: process a REAL input WAV through an FX CHAIN (capabilities 1 + bounce-in-place)
This is the new code. Add these to the C++ host (named precisely):

**3a. A WAV reader** — new file `native/audio-host/src/wav_reader.h` (mirror `wav_writer.h`'s
style; header-only, no deps): `bool read_wav(const std::string& path, std::vector<float>& out_interleaved, int& channels, int& sample_rate)`. Parse RIFF/`fmt `/`data`; accept 16-bit
PCM and 32-bit float (the two becky writes/encounters); return de-interleaved-friendly
interleaved floats in [-1,1]. Degrade-never-crash: bad file → return false.

**3b. A chain render verb** — add `json VstHost::render_chain(const json& args)` to
`src/vst_host.h` + `src/vst_host.cpp`, and dispatch it as `"render.chain"` in
`src/main.cpp` `dispatch()` (next to the existing `render` case). Contract:
```
render.chain {
  in:  "track.wav",                  // input WAV (read via wav_reader)
  out: "bounced.wav",
  sampleRate?: 48000, buffer?: 512,
  chain: [ {path:"comp.vst3", state?:"comp.vstpreset"},
           {path:"eq.vst3",   state?:"eq.vstpreset"} ]
} -> { out, frames, channels, sampleRate, peak, rms, peakDb, rmsDb, nonSilent, stages:N }
```
Implementation: read the input WAV → for each chain entry, `instantiate()` it (reuse the
existing helper, L231) and, if `state` given, apply it via the SAME `PresetFile::loadPreset`
path `state_load` uses (factor the load body into a small helper so both call it). Then run the
existing block loop (L715-788) but, **instead of the 220 Hz sine (L737-751), copy the current
signal buffer into the effect's input bus**; take the effect's output as the signal buffer for
the next stage. After the last stage, `write_wav_pcm16` (already there) the final buffer. Keep
the peak/RMS/nonSilent corroboration that `render` already computes.

**3c. (optional but cheap) A one-command offline PROOF flag** — add `--render-chain <in.wav>
<out.wav> <pluginA.vst3> [pluginB.vst3 ...]` to `main.cpp` (alongside `--selftest`/`--probe`)
so the chain can be exercised with no Go and no GUI. Have it print the result JSON to stderr +
`RESULT: PASS` when `nonSilent`.

Build + prove:
```
cd native\audio-host
.\scripts\build.ps1
# make a real input WAV first (any becky render, or ffmpeg a clip to wav):
ffmpeg -i <any-audio> -ar 48000 -ac 2 track.wav
.\build\becky-audio-host.exe --render-chain track.wav bounced.wav "C:\Program Files\Common Files\VST3\<SomeEQ>.vst3"
ffmpeg -i bounced.wav -af volumedetect -f null NUL 2>&1 | findstr mean_volume
ffmpeg -i track.wav   -af volumedetect -f null NUL 2>&1 | findstr mean_volume
```
- [ ] DONE WHEN: `bounced.wav` exists, `RESULT: PASS` (nonSilent), AND the two `mean_volume`
  numbers DIFFER (proves the effect actually changed the track's audio, not a passthrough).
  Paste both mean_volume lines + the result JSON. If they're identical, the input bus copy
  isn't reaching the plugin — check the bus channel count / that you zeroed the OUTPUT bus
  before process (the existing loop does this at L753-760).

## Step 4 — WIRE it to becky-canvas buses (capability: FX chain per mix bus)
Add the model field + the Go client method + the canvas hook.

**4a. dawmodel** — in `internal/dawmodel/mixer.go`, add to `Bus`:
```
FX []BusFX `json:"fx,omitempty"`   // ordered insert chain on this bus
```
and a `type BusFX struct { Path string `json:"path"`; State string `json:"state,omitempty"` }`.
Add a pure helper `AddBusFX(busID, BusFX) *Arrangement` next to the existing `busPtr` (L148)
edits (immutable, returns a NEW arrangement — match the file's existing style). Add a unit
test asserting the chain is appended in order (assert VALUES, not truthiness).

**4b. Go client** — in `internal/audiohost/client.go` add `RenderChain(ctx, inWav, outWav,
chain []ChainStage, opts RenderOptions) (RenderResult, error)` wrapping the `render.chain` verb
(mirror `RenderPath`). Add `type ChainStage struct { Path, State string }` to `types.go`.

**4c. canvas bounce** — in `cmd/canvas`, on a "bounce bus" action: for the selected `Bus`,
gather the tracks routed to it, render/mix them to a temp `track.wav` (the existing engine /
`becky-daw-engine` render path), then call `audiohost.Client.RenderChain` with the bus's
`FX[]` (each `{Path, State}` → `ChainStage`), writing `<bus>_bounced.wav`. Surface the
returned peak/RMS in the panel. Per `STANDARDS-CANVAS-UX.md`, also make it drivable from a
`ctledit` op so it's operable from BOTH a panel and the agent box.
- [ ] DONE WHEN: in becky-canvas, adding an effect+state to a bus and clicking bounce writes a
  `<bus>_bounced.wav` whose audio differs from the dry mix. Report: a screenshot + the two
  mean_volume numbers, or the exact error.

## Step 5 — report back honestly
Update `CLAUDE.md` §6: which boxes are checked WITH pasted evidence (selftest line, ffprobe,
the two differing mean_volume numbers, the canvas screenshot). Do NOT write "LEFT FOR LOCAL:
nothing" unless every box is checked. A stuck step reported honestly beats a false green.

---

### Cloud's honesty note
Cloud could not run ANY of this (no Windows, no MinGW, no VST3 plugins, no audio device). The
"already works" claims above are from reading the C++/Go source, and the SDK-license/version
claim is from the upstream repo (MIT, v3.8.x). Steps 2 and the existing `render`/state verbs
should work as written; Step 3 is genuinely new code with the files/functions named. The one
thing cloud is least sure of is whether a given effect plugin needs specific bus arrangement
negotiation (`setBusArrangements`) beyond `activateBus` — if `process` returns an error or a
silent output on a real stereo effect, that's the first thing to add in `instantiate()`.
