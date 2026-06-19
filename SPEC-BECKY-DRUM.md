# SPEC: becky-drum — the honest rebuild of the 16-pad drum machine

**Status:** spec for review. NO code written from this yet. Supersedes the retracted
"GUI-first AI drum machine" that shipped as slop (sine-tone pads, no sample loading,
a keyword parser mislabeled as AI). This document is the build target; it is grounded
in source-cited research (see the companion files: `SPEC-MASCHINE-CLONE.md`,
`research-go-audio.md`, `research-oss-projects.md`, and §2/§4 below for sample/kit
loading). The Maschine 2 capability target itself is in `SPEC-MASCHINE-CLONE.md`.

---

## 0. Why this exists / the non-negotiables

The first attempt failed for three concrete reasons. The spec exists to make each
impossible to repeat:

1. **It could not load the producer's sounds.** Every pad fell back to a generated
   sine tone. **Non-negotiable #1: the instrument loads the producer's real samples
   and kits** (15+ years of WAV libraries at `X:\music-2\SAMPLES`, `X:\Splice`, BVKER
   kits, etc.). A drum machine that can't open his sounds is not a drum machine.
2. **The audio path could not play live.** It rendered a WAV then shelled out to a
   player — no live pad hits, no edit-while-playing loop. **Non-negotiable #2: one
   persistent audio stream with a real-time mixer** (live pad audition + a running
   pattern + edits applied between loops).
3. **The "AI" was a keyword parser dressed up as AI.** **Non-negotiable #3: honesty.**
   The deterministic parser is named what it is (a fallback); real natural-language
   control means a real local model, which only the local machine (GPU + llama.cpp +
   weights) can run. Nothing in this project may present a stub as a working feature.

**Honest capability boundary (applies to the whole spec).** The cloud agent has no
audio device, no display, no GPU, and no access to the producer's drives. Therefore
the cloud agent builds and unit-tests ONLY pure, headless, deterministic logic; the
local agent builds and verifies anything that needs audio output, a window, the real
model, or the real sample drives. Every section below is tagged **[CLOUD]** (buildable
+ testable with no hardware) or **[LOCAL]** (needs hardware/samples/model — handed off
labeled as unbuilt+unverified, never stubbed-as-done). The split is consolidated in §9.

---

## 1. Data model — SFZ-aligned [CLOUD]

The existing `internal/drummachine` model (16 pads, patterns, banks, scenes, song,
choke) is the one good piece that survives — but its `Pad` only references a single
sample path with a sine fallback. We extend the **Sound** behind a pad to the
multisampling model that real kits use. The authoritative anchor is the open **SFZ**
format ([sfzformat.com](https://sfzformat.com/)); each field below maps to an SFZ opcode
so import (§3) is a direct translation.

A **Pad** triggers a **Sound**. A **Sound** is one or more **Layers**:

- **Layer** = a set of round-robin **sample variants** plus the conditions under which
  the layer is chosen:
  - `VelLo`, `VelHi` (SFZ `lovel`/`hivel`, 1–127) — velocity layer bounds. Optional
    crossfade (`xfin_*`/`xfout_*`) is Phase-2.
  - `RoundRobin []Variant` with a mode: **sequential** (SFZ `seq_length`/`seq_position`
    — a per-Sound counter) or **random** (SFZ `lorand`/`hirand`). Avoids the
    machine-gun effect.
- **Variant (one sample)** carries: `SamplePath`, `StartFrame`/`EndFrame` (SFZ
  `offset`/`end`), `LoopMode` (`no_loop`/`one_shot`/`loop_continuous`/`loop_sustain`),
  `LoopStart`/`LoopEnd`, `PitchKeycenter` (default 60), `Transpose` (semitones), `Tune`
  (cents), and gain/pan.
- **Sound-level**: `ChokeGroup` (SFZ `group`) and `Chokes []int` (SFZ `off_by`) +
  `ChokeMode` (`fast`/`normal`); `OneShot` bool (drum default = `loop_mode=one_shot`,
  ignores note-off); optional `KeyLo`/`KeyHi`+root for chromatic/keygroup pads.

Defaults keep a simple one-shot drum a single Layer with a single Variant, so the model
stays trivial for the common case and only grows for true multisamples.

**[CLOUD] deliverable:** the extended `internal/drummachine` Sound/Layer/Variant types
+ choke resolution + JSON, all immutable + table-tested. (Sources: SFZ opcode pages for
`lovel`/`hivel`, `seq_length`/`seq_position`, `lorand`/`hirand`, `group`/`off_by`,
`loop_mode`, `offset`, `pitch_keycenter` — all on sfzformat.com.)

---

## 2. Sample decoding [CLOUD]

WAV is the only format that must be nailed; everything else is optional. (Splice ships
`.wav`; BVKER kits are 24-bit/44.1k WAV; 24-bit WAV is the pro one-shot standard.)

- **WAV (priority):** RIFF chunked; `wFormatTag` 1 = PCM (16/24/32-bit int), 3 = IEEE
  float, `0xFFFE` = WAVE_FORMAT_EXTENSIBLE (must read the sub-format). **Critical trap
  (verified):** the popular `go-audio/wav` (Apache-2.0, but archived 2026-02) decodes
  int PCM and reads `smpl`/`cue` chunks, but **silently mis-decodes 32-bit float** via
  an int-cast (go-audio/audio issue #18). Plan: extend becky's existing `internal/dsp`
  RIFF code into an in-house decoder handling tags 1 + 3 + EXTENSIBLE + 24-bit packing
  (a few hundred lines, removes the archived-dependency risk), OR use `youpy/go-wav`
  (ISC) for the float path. Decode + resample kit samples UP FRONT (never in the audio
  callback).
- **Metadata chunks (read them — they unlock loops + tempo):** `smpl` (root note +
  loop points → feeds the Layer loop fields), `acid` (one-shot-vs-loop flag + float
  BPM + beats → loop/tempo detection in §4), `cue`, and RIFF `INFO`/Maschine WAV tags.
- **Optional pure-Go formats (no cgo):** FLAC via `mewkiz/flac` (Unlicense, actively
  maintained — the standout); OGG via `jfreymuth/oggvorbis` (MIT); MP3 via
  `hajimehoshi/go-mp3` (Apache, archived 2023, best-effort); AIFF via `go-audio/aiff`
  (archived). **Skip Opus** (no pure-Go decoder; not used for drum samples).

**[CLOUD] deliverable:** `internal/sampledecode` — WAV (int+float+EXTENSIBLE) + chunk
parsing (`smpl`/`acid`/`cue`), optional FLAC/OGG. Tested against in-test-generated WAVs
(incl. a 32-bit-float fixture to prove we don't regress the float bug). No cgo.

---

## 3. Kit / preset import [CLOUD for the open formats]

What a producer owns, and the honest verdict on parsing each in pure Go (cross-checked
against ConvertWithMoss's real read/write matrix):

| Format | Verdict |
|---|---|
| **SFZ** (`.sfz`, open text) | **BUILD FIRST** — line parser, `<global>/<group>/<region>` inheritance, the opcode subset in §1, Windows `\` path normalization via `internal/pathx`. |
| **DecentSampler** (`.dspreset`, open XML) | **BUILD 2nd** — `encoding/xml`; `groups/group/sample` with `rootNote`/`lo|hiNote`/`lo|hiVel`/`start`/`end`/`tuning`/`loopStart|End`/`seqMode|Position|Length`. |
| **Akai MPC** (`.xpm`, modern XML) | OPTIONAL — native 16-pad fit; `encoding/xml`. Build if Jordan owns MPC expansions. |
| Ableton `.adg`, Logic `.exs` | PUNT to Phase-2 (gzip+XML / reverse-engineered binary; low ROI for a Cubase/Maschine user). |
| **Maschine `.mxgrp`/`.mxsnd`, Battery `.nbkt`, Kontakt `.nki` (4.2+ encrypted), Bitwig `.bwpreset`** | **DO NOT ATTEMPT** — proprietary/encrypted; even the best open RE tools only poke metadata or pre-2009 Kontakt. **For NI kits, ignore the kit file and read the WAVs from its content folder.** |

This is the honest answer to "open my Maschine kits": we cannot parse the Maschine kit
*file*, but we can load the WAV *samples* it points at, and we can import any SFZ /
DecentSampler the producer exports or downloads. Stated plainly so there's no false promise.

**[CLOUD] deliverable:** `internal/kitimport` with SFZ + DecentSampler parsers →
the §1 model. Table-tested against small fixture files. (Sources: sfzformat.com headers/
opcodes; DecentSampler developers guide; ConvertWithMoss matrix; monomadic/ni-file.)

---

## 4. Library scanner / browser [CLOUD for the index; LOCAL for the real drives]

Make the producer's messy library usable. Scan a folder (e.g. `X:\music-2\SAMPLES`,
`X:\Splice`) and build a searchable index. Per becky's corroborate-then-conclude rule:

- **Role guess** (kick/snare/hat/clap/tom/crash/ride/perc) from filename + parent-folder
  tokens (`kick|kik|bd`, `snare|snr|sd`, `hat|hh|chh|ohh`, `clap|clp`, …). Conclude a role
  only when filename and folder agree; a lone weak token → low-confidence/`unknown` (never
  guess wrong).
- **Loop vs one-shot** by fusing ≥2 signals: `acid`/`smpl` loop flags, a BPM token +
  whole-bar length, and raw duration (sub-~1s ⇒ one-shot).
- **Facets** (mirror Maschine/Splice): role, BPM, key, loop-vs-one-shot. BPM/key from the
  `acid` chunk first, else filename tokens.

**[CLOUD] deliverable:** `internal/samplelib` — pure-Go scan of a directory tree →
index with role/bpm/key/loop classification + search, table-tested against a synthetic
tree of named empty/− tiny WAVs. **[LOCAL]:** running it across the real multi-GB drives
and tuning the heuristics on real results. (This extends becky's existing `becky-stems`.)

---

## 5. Real-time audio engine [LOCAL builds/verifies sound; CLOUD builds the mixer math]

**Standardize on `github.com/ebitengine/oto/v3` (Apache-2.0).** Decisive reason: it
needs **no cgo on Windows** (uses purego→WASAPI), so the one-click build for a non-dev
has no MSYS2/mingw dependency that can silently break — and the cloud agent can
cross-compile-check it via `GOOS=windows`. Its `io.Reader` model *is* a pull-callback:
a background goroutine calls our `Read([]byte)` forever, and we mix all voices inside it.
This natively handles overlapping pad hits + a running pattern + editing the loop while
it plays. It runs on its own goroutine, decoupled from Gio's D3D11 loop — so the engine
lives in the **same binary** as the window (we delete the render-then-exec hack entirely).

Engine design (the mixer): a `Voice` per active sample (read cursor, gain, pan, pitch
ratio, envelope, choke group); `Trigger(pad, vel)` adds a voice; the 16-step sequencer
is driven by a global sample-frame counter (NOT `time.Ticker`, or the beat drifts);
choke cuts same-group voices; sum → tanh limiter (reuse becky's). Honest hard parts,
called out so they're not rediscovered: keep the lock during fill tiny (drain a command
channel; never decode/model-call while locked); tune BufferSize for ~5–10 ms vs glitch;
decode/resample up front.

- **[CLOUD] deliverable:** `internal/drumaudio` — the pure-Go mixer + sequencer math
  (Sound+kit → sample buffer; voice mixing; choke; sample-accurate step timing),
  unit-tested for determinism and correctness, AND a `GOOS=windows go build` check of
  the oto-backed output file.
- **[LOCAL] deliverable:** confirm it actually makes low-latency sound on his interface;
  tune buffer size. (Fallback option documented: `malgo`/miniaudio behind cgo if ASIO-class
  latency is ever needed — repo already has mingw.) (Source: `research-go-audio.md`.)

---

## 6. Sequencing / patterns / scenes / song [CLOUD]

Reuse the surviving `internal/drummachine` pattern/bank/scene/song model and its
`drumcmd` bridge. Extend pads to reference §1 Sounds instead of a bare sample path.
Swing = the standard Roger-Linn/MPC delay-the-even-16ths algorithm (already implemented
correctly; no library needed). **[CLOUD] deliverable:** the extended model + tests.

---

## 7. GUI [LOCAL verifies; CLOUD compile-checks only]

Gio (gioui.org), the toolkit that already opens on his PC. Layout: 4×4 pad grid
(click→audition, light up), a **sample-browser panel** (the §4 index — drag a sample
onto a pad), per-pad step lane, transport (tempo/swing/play/stop), kit Open/Save. File
drag-drop via `gio-plugins` (MIT) addressing his #1 friction. Reference Gio apps:
`chapar`, `giocanvas` (all MIT/BSD — learn/borrow, license-checked).

- **[CLOUD]:** can `go build -tags gui` compile-check it here (Gio libs are installed),
  but CANNOT confirm it renders or is usable. That distinction is stated on the handoff.
- **[LOCAL]:** the only verification that counts — open it, see pads, drag a sample, hear it.

---

## 8. AI control — stated honestly [LOCAL owns the real model]

What "AI controls the GUI" actually requires: a real language model turning free speech
into structured edits to the §1/§6 model. **The cloud agent cannot run a model** (no GPU/
weights). The local machine can (llama.cpp + a small instruct GGUF, `--temp 0 --seed 42`,
strict-JSON schema). Therefore:

- **[CLOUD]:** the deterministic keyword parser (`internal/machinectl`) — useful, but it
  is NOT AI and will be labeled "keyword commands," not "the AI box." Plus the model
  **interface + prompt/JSON schema + a faked-model unit test**. The real `exec` of the
  model is a clearly-labeled unwired boundary, not a feature claim.
- **[LOCAL]:** wire the real model exec; confirm it produces valid edits on the GPU.

No screen will say "AI" until a real model is behind it.

---

## 9. The honest cloud ↔ local split (the whole point)

| Piece | Cloud builds + unit-tests (no hardware) | Local builds + verifies (hardware/samples/model) |
|---|---|---|
| §1 Sound/Layer/Variant model | ✅ types, choke, JSON, tests | — |
| §2 sample decode (WAV/float/chunks, FLAC) | ✅ decoders + tests (synthetic WAVs) | real-library decode sanity |
| §3 SFZ + DecentSampler import | ✅ parsers + tests (fixtures) | import his actual kits |
| §4 library scanner/index | ✅ scan + classify + tests (synthetic tree) | run on real multi-GB drives; tune |
| §5 audio mixer/sequencer math | ✅ pure-Go mix/timing + tests + `GOOS=windows` build check | **actual sound + latency** |
| §6 patterns/scenes/song | ✅ model + tests | — |
| §7 Gio window | ⚠️ compile-check only | **renders + usable (the real test)** |
| §8 natural-language control | ✅ parser + model interface + faked test | **real model on the GPU** |

The cloud agent will NEVER report a ⚠️/LOCAL row as "working." It reports "compiles /
logic tested" and hands off the verification honestly.

---

## 10. Phased plan (smallest honest thing first)

- **Phase 1 [CLOUD]:** §1 model + §2 WAV decode + §3 SFZ import + §4 scanner — all pure-Go,
  all tested. Deliverable a producer can't *see* yet, but every claim is verifiable.
- **Phase 2 [LOCAL]:** §5 oto engine wired → load a folder/SFZ kit, hit a pad, hear a real
  sample, loop a pattern. This is the first time it's a real instrument. Local-only.
- **Phase 3 [LOCAL]:** §7 GUI on top (pad grid + sample browser + transport).
- **Phase 4 [LOCAL]:** §8 real model behind the command box.

## 11. Building blocks (license-checked, permissive only)

oto/v3 (Apache), mewkiz/flac (Unlicense), go-audio/wav (Apache, int-only), youpy/go-wav
(ISC, float), jfreymuth/oggvorbis (MIT), gomidi/midi (MIT, for MIDI export), gio-plugins
(MIT, drag-drop). **Learn-only (GPL/NC — do NOT vendor):** sfizz (BSD — actually
permissive, the reference for SFZ choke/RR semantics), Hydrogen, Giada, drumhaus.

## 12. Open decisions for Jordan

1. In-house WAV decoder vs `youpy/go-wav` for the float path? (Recommend in-house — owns
   the chunks, no archived dep.)
2. Beyond SFZ + DecentSampler, do you actually have MPC `.xpm` kits worth importing?
3. Confirm the real sample roots to scan (`X:\music-2\SAMPLES`, `X:\Splice`, others?).
4. Which small instruct GGUF for the §8 model (Qwen3-4B / Smol / LFM2)?
