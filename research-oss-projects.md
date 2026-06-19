# OSS Research — Building a Maschine-class Drum Machine / Groovebox

Research date: 2026-06-19. Goal: find real, existing open-source projects becky can
**borrow code from** or **learn architecture/UX from** for a 16-pad, AI-controlled drum
machine (Maschine 2 class), broken down by functionality.

**License flags (read first):** becky is an MIT-style, locally-run tool suite. We can
*vendor/borrow code* only from permissive licenses (MIT, BSD, Apache-2.0, public domain,
"MIT OR Apache-2.0"). We can **NOT** vendor **GPL/LGPL/AGPL** or **non-commercial (CC-BY-NC)**
code — those are **LEARN-ONLY** (read for architecture/UX, do not copy code). Each entry
below is tagged accordingly. Stars are approximate as of June 2026.

---

## 1. Drum machines / grooveboxes (full apps — architecture & UX)

| Project | URL | Stars | License | Lang | Last active | What to reuse / learn |
|---|---|---|---|---|---|---|
| **Hydrogen** | https://github.com/hydrogen-music/hydrogen | ~1.3k | **GPLv2+ (LEARN-ONLY)** | C++/Qt5 | active (2026-06) | The canonical OSS pattern-based drum machine. Learn its data model: drumkit → instrument (with velocity layers + sample selection) → pattern → song; per-instrument mute/solo, humanize, swing, MIDI/OSC automation. The reference for "what a drum machine's domain model looks like." Cannot copy code (GPL). |
| **Giada** | https://github.com/monocasual/giada | ~2.0k | **GPLv3 (LEARN-ONLY)** | C++/JUCE | active (2026-06) | Sample-accurate loop/clip engine, VST3 hosting, MIDI I/O, built-in wave editor. Learn the sample-accurate sequencing core and the action/automation recording model. GPL — architecture reference only. |
| **drumhaus** | https://github.com/mxfng/drumhaus | ~150 | **CC BY-NC-SA 4.0 (LEARN-ONLY — non-commercial)** | TypeScript/React/Tone.js | active (2026-06) | The single best *UX + sequencing-architecture* reference for our exact use case. Its sequencer **precomputes the whole pattern on every edit** (matches becky's `SequenceMachinePattern` design exactly), and it decouples animation from React via a "centralized animation clock" writing DOM attrs directly — a great model for our Gio pad lighting. Has per-step ratchet/flam and per-voice timing nudge. Non-commercial license = don't copy code, but the design is gold. |
| **Stargate** | https://github.com/stargatedaw/stargate | ~850 | **GPLv3 (LEARN-ONLY)** | Python/C17 | active (2026-06) | Full DAW with a real drum machine + sampler. Learn how a sampler instrument plugs into a sequencer/host. GPL. |
| **Revisto/drum-machine** | https://github.com/Revisto/drum-machine | ~150 | **GPLv3 (LEARN-ONLY)** | Python/GTK4 | active (2026-06) | A clean, small, modern desktop drum-machine UX (GNOME Circle app). Good for *layout/interaction* ideas at our scale. GPL. |

---

## 2. Step sequencers / pattern engines

| Project | URL | Stars | License | Lang | Last active | What to reuse / learn |
|---|---|---|---|---|---|---|
| **sequencer64** | https://github.com/codenickycode/sequencer64 | ~230 | **MIT (BORROW-OK)** | JS/React | active (2026-06) | A 64-step sequencer with 9 kits × 9 samples; per-sound edit of pattern/slice/pitch/length/velocity. Permissive — we can adapt its step-grid state model and edit semantics. |
| **web-drum-sequencer** | https://github.com/stufreen/web-drum-sequencer | ~170 | **MIT (BORROW-OK)** *(verify LICENSE on clone)* | JS/React/WebAudio | active (2026-04) | Web Audio + Redux step sequencer; a clear reference for an immutable pattern state + scheduler split (mirrors becky's immutable `Pattern`). |
| **beats** | https://github.com/jstrait/beats | ~440 | **MIT (BORROW-OK)** | Ruby | active (2026-05) | CLI drum machine: YAML beat notation → WAV. Excellent reference for a **deterministic, file-in→audio-out** pattern engine (very becky-shaped) and for a human-readable pattern DSL. |
| **textbeat** | https://github.com/flipcoder/textbeat | ~440 | **MIT (BORROW-OK)** *(verify)* | Python | active (2026-06) | Plaintext music/drum sequencer with music-theory-aware shorthand — a model for a text/plain-English → pattern layer (relevant to becky's `machinectl` English→edits translator). |

---

## 3. Drum sampler / multi-sample playback engines (voice mgmt + choke groups + velocity layers)

| Project | URL | Stars | License | Lang | Last active | What to reuse / learn |
|---|---|---|---|---|---|---|
| **sfizz** | https://github.com/sfztools/sfizz | ~525 | **BSD-2-Clause (BORROW-OK)** | C++ | active (2026-06) | **The most important engine reference here.** A full SFZ sampler library: voice pool/polyphony management, `group=`/`off_by=` (= **choke groups**, e.g. open-hat choked by closed-hat — exactly our pad-choke feature), velocity layers (`lovel`/`hivel`), round-robin (`seq_*`), per-region amp/pan/pitch/decay envelopes. Permissive BSD — we can study or even bind to it. Maps 1:1 onto becky's per-pad model (level/pan/pitch/decay/choke). |
| **sfizz-render** | https://github.com/sfztools/sfizz-render | ~15 | **BSD-2-Clause (BORROW-OK)** *(archived)* | C++ | archived | Offline "MIDI file + SFZ → WAV" renderer — a direct analogue of becky's deterministic `RenderMachine`. Tiny, readable example of headless rendering with sfizz. |
| **rudiments** | https://github.com/jonasrmichel/rudiments | ~150 | **MIT OR Apache-2.0 (BORROW-OK)** | Rust | active (2026-03) | A drum machine in Rust built on the `rodio` audio crate. Learn a compact voice/playback model and pattern→trigger loop in a systems language (closer to becky's Go than the C++ giants). |
| **AcidBox** | https://github.com/copych/AcidBox | ~200 | **GPL (LEARN-ONLY — check exact)** | C++ (ESP32) | active (2026-06) | 808-style drum voices + FX chain on a microcontroller. Learn lean per-voice synthesis/decay if we ever want *synthesized* (not just sampled) drum voices. Treat as learn-only until license confirmed. |

> **Choke groups, specifically:** the cleanest open, permissive reference is **sfizz's
> `off_by`/`group` mechanism** (BSD). Hydrogen also implements "stop-note"/mute-group
> behavior but is GPL (learn-only). becky's existing `Pad.choke` + open/closed-hat
> resolution already follows the sfizz model.

---

## 4. Swing / groove quantize implementations

There is **no single well-known permissive library** that does just "MPC swing" — it's a
small, well-documented algorithm, so the win here is the *spec*, not borrowed code.

- **The canonical algorithm (Roger Linn / MPC):** swing delays every *even* 16th note
  within each 8th-note pair; the swing % is the time ratio between the first and second
  16th. 50% = straight, ~54–58% = hip-hop feel, 66% = true triplet. Source write-ups:
  https://www.attackmagazine.com/features/interview/roger-linn-swing-groove-magic-mpc-timing/
  and https://palsen.tumblr.com/post/182157488304/about-mpc-swing .
  becky already has swing in `internal/drummachine` (Pattern swing) and reuses
  quantize/swing math in `internal/drumcmd` — this confirms the approach is textbook-correct.
- **drumhaus** (above, CC-BY-NC, learn-only): per-step timing nudge + ratchet/flam is a
  good UX reference for *humanize* on top of swing.
- **Hydrogen** (GPL, learn-only): has a humanize (timing + velocity randomization) feature
  whose parameter design is worth reading.

Recommendation: implement directly from the Linn spec (it's ~10 lines) rather than vendoring
anything — which is what becky already does.

---

## 5. Sample browsers / library indexers (tagging, search, role detection)

| Project | URL | Stars | License | Lang | Last active | What to reuse / learn |
|---|---|---|---|---|---|---|
| **Sæmpl (Saempl)** | https://github.com/jonasblome/Saempl | small | **Apache-2.0 (BORROW-OK)** | C++/JUCE | recent | **Best-fit reference.** A sample-library manager that auto-analyzes **key, tempo, loudness**, then **clusters by similarity** into a grid; grid/table/folder views, waveform preview, drag-drop to DAW, filter by analyzed property. This is almost exactly becky's `becky-stems` role/loudness scan + a browser UX on top. Permissive — we can study its analysis→index→cluster pipeline directly. |
| **Samplecat** | https://github.com/ayyi/samplecat | small | **GPLv3 (LEARN-ONLY)** | C/GTK | active | Mature sample cataloguer with SQLite/MySQL backing store, tags, auditioning. Learn the **DB schema + tagging/search model** for a large sample library. GPL — design reference only. |
| **awesome-soundfonts** | https://github.com/ad-si/awesome-soundfonts | ~175 | list/CC | — | active | Curated index of soundfont/SFZ instruments + tooling — a sourcing list for free drum kits/instruments to ship with becky. |

> Note: generic "sample manager" GitHub searches return mostly tiny repos; Sæmpl is the
> standout permissive one and maps directly onto becky's existing `internal/stemscan`
> (peak/loudness/crest/role-guess) — its analysis features validate that design and suggest
> the clustering/grid as the next UX step.

---

## 6. Pad-grid / sequencer GUIs (layout & interaction) — incl. Go + Gio

**Go + Gio (gioui.org) — same toolkit becky-canvas / cmd/drummachine already use:**

| Project | URL | Stars | License | Lang | Last active | What to reuse / learn |
|---|---|---|---|---|---|---|
| **gui-with-gio** | https://github.com/jonegil/gui-with-gio | ~240 | **UNLICENSE/CC0-ish (BORROW-OK — verify)** | Go/Gio | active (2026-06) | The best Gio tutorial repo — concrete patterns for layout, buttons, animation, state. Direct reference for our pad grid + transport row interaction code. |
| **giocanvas** | https://github.com/ajstarks/giocanvas | ~150 | **BSD (BORROW-OK)** | Go/Gio | active (2026-05) | A high-level canvas/drawing API over Gio (rects, circles, text in relative coords). Could simplify drawing the 4×4 pad grid + waveform/step lanes. Permissive. |
| **gio-plugins** | https://github.com/gioui-plugins/gio-plugins | ~75 | **MIT/Unlicense (BORROW-OK — verify)** | Go/Gio | active (2026-05) | Extra Gio plugins (e.g. **file drag-drop**, webview) — directly relevant to becky's #1 friction (in-window OS file drag-drop onto the pad grid). Worth evaluating to replace the hand-rolled WinAPI IDropTarget shim. |
| **chapar** | https://github.com/chapar-rest/chapar | ~700 | **MIT (BORROW-OK)** | Go/Gio | active (2026-06) | The largest real-world Gio *application* (an API client). Best reference for structuring a non-trivial multi-panel Gio app (docking, lists, theming) — i.e. how to grow becky-canvas/drummachine cleanly. |
| **giox / cu** | https://github.com/scartill/giox , https://github.com/arjenjb/cu | ~12 / ~11 | permissive (verify) | Go/Gio | active | Extra Gio widgets (comboboxes, widget libs) if we need controls beyond Gio's stdlib widgets. |

**Non-Go pad-grid UX references (learn layout/interaction only):**
- **sequencer64** (MIT) and **drumhaus** (CC-BY-NC) above are the clearest 16-step pad-grid
  interaction references.
- **arpeggiator** https://github.com/collidingScopes/arpeggiator (~155, MIT, JS) — hand/gesture
  control of a drum machine; interesting for becky's "AI controls the GUI" angle.

---

## 7. Go audio / DAW / sequencer projects & Go MIDI libraries

| Project | URL | Stars | License | Lang | Last active | What to reuse / learn |
|---|---|---|---|---|---|---|
| **ebitengine/oto** | https://github.com/hajimehoshi/oto | ~1.5k | **Apache-2.0 (BORROW-OK)** | Go (+cgo) | active | The standard low-level Go audio *output* library (Windows WASAPI, macOS, Linux, etc.). The realistic playback backend for becky's `-tags audio` engine (alternative/complement to the vendored miniaudio). |
| **gopxl/beep** (maintained fork of faiface/beep) | https://github.com/gopxl/beep | ~450 | **MIT (BORROW-OK)** | Go | active | High-level Go audio: `Streamer` interface, **mixer**, resampling, WAV/MP3/OGG/FLAC decode, effects. A mixer + streamer model we could borrow for layering pad voices. Built on oto. (Original: https://github.com/faiface/beep .) |
| **gomidi/midi** | https://github.com/gomidi/midi | ~356 | **MIT (BORROW-OK)** | Go (pure; cgo only for rtmidi driver) | active (2026-05) | Best Go MIDI lib: SMF read/write **and** real-time MIDI via a driver interface; no stdlib-external deps for the core. Direct fit for becky's SMF needs + future MIDI-controller input (drive pads from a hardware pad controller). |
| **go-audio/midi** | https://github.com/go-audio/midi | ~62 | **Apache-2.0 (BORROW-OK)** *(archived)* | Go | archived | Part of the go-audio family (go-audio/wav, /audio) — useful if we standardize on go-audio's WAV buffer types. Archived; gomidi is the livelier choice. |
| **gen2brain/malgo** | https://github.com/gen2brain/malgo | (mid) | **Unlicense / public-domain-ish (BORROW-OK — miniaudio is PD)** | Go (cgo→miniaudio) | active | Go bindings to **miniaudio** — capture + playback, device enumeration. becky already vendors miniaudio.h directly; malgo is the ready-made Go binding alternative for the audio engine. |
| **gordonklaus/portaudio** | https://github.com/gordonklaus/portaudio | (mid) | **MIT (BORROW-OK)** | Go (cgo→PortAudio) | active | Mature Go PortAudio bindings — another viable cross-platform realtime audio I/O backend. |

---

## Recommended building blocks (license-checked) — what I'd actually base becky's drum machine on

becky already has the right spine (immutable `Pattern`/`Pad`/`Kit`, deterministic
`RenderMachine`, Gio window). These are the real, permissive projects to lean on to finish it:

1. **sfizz** — https://github.com/sfztools/sfizz — **BSD-2-Clause ✅ borrow/bind OK.**
   *The* reference (and a usable engine) for the sampler half: voice pooling, **choke
   groups (`off_by`/`group`)**, velocity layers, round-robin, per-region pitch/decay.
   Even if we keep our pure-Go renderer, model our voice/choke semantics on sfizz, and
   adopt **SFZ as becky's kit format** so users get a huge existing instrument ecosystem.
   (`sfizz-render`, also BSD, is a tiny headless MIDI+SFZ→WAV example matching our
   `RenderMachine`.)

2. **gomidi/midi** — https://github.com/gomidi/midi — **MIT ✅.**
   Pure-Go SMF read/write + real-time MIDI driver interface. Use for MIDI export/import
   and to let a hardware pad controller drive the pads later. Cleanest fit for a Go suite.

3. **ebitengine/oto** (Apache-2.0 ✅) + **gopxl/beep** (MIT ✅) —
   https://github.com/hajimehoshi/oto , https://github.com/gopxl/beep .
   Proven Go audio output + a `Streamer`/`Mixer` model for layering pad voices and looping.
   Real, maintained alternatives/complements to the vendored miniaudio for the `-tags audio`
   engine. (malgo/portaudio are fallback backends, also permissive.)

4. **Gio app patterns: chapar (MIT ✅) + gui-with-gio + giocanvas (BSD ✅) + gio-plugins (MIT-ish ✅)** —
   for structuring the multi-panel pad-grid window, drawing the grid/step lanes, and
   especially **in-window file drag-drop** (gio-plugins) to kill becky's #1 friction.

5. **Sæmpl** — https://github.com/jonasblome/Saempl — **Apache-2.0 ✅.**
   The blueprint for the sample-browser side: auto-analyze key/tempo/loudness → cluster →
   grid/table/folder browse with waveform preview + drag-drop. Maps onto becky's existing
   `becky-stems` analysis; borrow its analyze→index→cluster pipeline shape.

6. **Swing/groove — implement from the Roger Linn/MPC spec, don't vendor.**
   ~10 lines (delay even 16ths by a swing-% ratio; 50/54–58/66%). becky already does this;
   the spec link above is the citation. Use **drumhaus** (CC-BY-NC, *learn-only*) and
   **Hydrogen** (GPL, *learn-only*) purely as UX references for humanize/ratchet/flam.

**Hard avoids for code (GPL/non-commercial — learn architecture only, never copy):**
Hydrogen (GPLv2+), Giada (GPLv3), Stargate (GPLv3), Samplecat (GPLv3), Revisto/drum-machine
(GPLv3), drumhaus (CC-BY-NC-SA). They are the best *design/UX* references — read them, don't
vendor them.

> Verify-on-clone caveat: a couple of permissive tags above (web-drum-sequencer, textbeat,
> gui-with-gio, gio-plugins, giox/cu) were confirmed via package metadata/READMEs; re-check
> the actual `LICENSE` file before copying any code, per becky's invariants.
