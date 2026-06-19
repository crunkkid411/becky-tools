# Go DSP effects + MIDI — research for the drum-machine mixer

Cloud research, 2026-06-19. Goal: implement (A) real-time DSP mixer effects in Go that run inside an
audio callback at 48 kHz, and (B) MIDI (SMF export + realtime controller input / "MIDI learn") for
the drum machine. Every claim below is cited; where no Go library exists I say "hand-roll" and give the
canonical algorithm and its source.

**Context from this repo (already on master):**
- becky's audio engine is `internal/audioengine` and currently uses **cgo miniaudio** (`miniaudio.h`,
  `miniaudio_impl.c`, build tag `audio`) for device I/O — there is NO `oto` or `gomidi` dependency yet
  (`go.mod` has neither; grep found only incidental matches). So both A and B are *new* dependencies.
- becky already has a **pure-Go SMF type-1 writer** in `internal/music/smf.go` and a reader
  `internal/music/smfread.go` (NoteOn/NoteOff/meta, deterministic tick ordering). **This overlaps MIDI
  export** — see Part B note on reuse.
- becky already has a pure-Go DSP foundation in `internal/dsp` (FFT, WAV decode, Krumhansl analysis).
  That's analysis-side; the *effects* below are new.

---

## Part A — DSP effects in Go (real-time, inside the mixer callback)

### Architectural reality first

There is **no comprehensive real-time effects DSP library in Go**. The audio-Go ecosystem is thin:
- `gopxl/beep` (MIT, the maintained fork of the archived `faiface/beep`) ships only **Volume, Gain,
  Pan, Transition, Mono, Swap, Doppler, and an Equalizer** in its `effects` package — **no compressor,
  reverb, or delay** ([gopxl/beep effects](https://pkg.go.dev/github.com/gopxl/beep/v2/effects); the
  original is archived in favour of gopxl, [faiface/beep](https://github.com/faiface/beep)).
- `moutend/go-equalizer` (MIT, ~21★) is a clean RBJ-cookbook biquad implementation with all 8 filter
  types and a per-sample `Apply()` API ([go-equalizer](https://github.com/moutend/go-equalizer), import
  `github.com/moutend/go-equalizer/pkg/equalizer`).
- `samuel/go-dsp` and `mattetti/audio/dsp/filters` have biquad structs but are oriented at offline
  processing, not a live callback ([go-dsp](https://pkg.go.dev/github.com/samuel/go-dsp/dsp),
  [mattetti filters](https://pkg.go.dev/github.com/mattetti/audio/dsp/filters)).

**Conclusion:** treat the effects as **hand-rolled pure-Go**, in a new `internal/dspfx` (or grow
`internal/dsp`). They are individually small (tens of lines each), well-documented by canonical sources,
and hand-rolling keeps them deterministic and dependency-free — consistent with becky's invariants. Pull
in `moutend/go-equalizer` only if we don't want to type the RBJ coefficient formulas ourselves; the
formulas are public ([RBJ Audio-EQ-Cookbook](https://webaudio.github.io/Audio-EQ-Cookbook/Audio-EQ-Cookbook.txt),
[musicdsp mirror](https://www.musicdsp.org/en/latest/Filters/197-rbj-audio-eq-cookbook.html)). Each
effect is a tiny stateful struct with a `Process(x float64) float64` (or per-block) method — that is the
right shape to call from an oto/v3 or miniaudio render callback.

### Effect-by-effect table

| Effect | Standard real-time algorithm | Existing Go lib? | Hand-roll difficulty |
|---|---|---|---|
| **Biquad EQ / filters** (LP/HP/BP/notch/peak/low-shelf/high-shelf/allpass) | RBJ "Audio EQ Cookbook" biquad. Transposed Direct Form II: `y = b0/a0*x + s1; s1 = b1/a0*x - a1/a0*y + s2; s2 = b2/a0*x - a2/a0*y`. Coefficients from intermediate `w0=2π f0/fs, α=sin(w0)/(2Q)` per the cookbook table. | **Yes** — `moutend/go-equalizer` (MIT, 8 filter types, `Apply()` per sample). Also `samuel/go-dsp` biquad struct. | **Easy** (one struct, ~30 lines + a coefficient switch). Lib optional. Sources: [RBJ cookbook](https://webaudio.github.io/Audio-EQ-Cookbook/Audio-EQ-Cookbook.txt), [go-equalizer](https://github.com/moutend/go-equalizer). |
| **Compressor / limiter (attack/release + SIDECHAIN)** — the key ducking feature | Feed-forward peak compressor: (1) **detector** on the *sidechain* signal → rectify `|x|`; (2) **ballistics** one-pole: `env = (in>env) ? a*env+(1-a)*in : r*env+(1-r)*in` with `a=exp(-1/(attack_s*fs))`, `r=exp(-1/(release_s*fs))`; (3) **gain computer** in dB: `over = envDB - thresholdDB; gainDB = over>0 ? -over*(1-1/ratio) : 0`; (4) apply `lin(gainDB)` to the *main* signal. SIDECHAIN = feed a different buffer (e.g. the kick bus) into step 1 instead of the channel's own signal — that is exactly the kick→bass ducking move. Limiter = ratio→∞ + fast attack (+ optional lookahead). | **No.** | **Medium** (~60–80 lines incl. dB helpers and the sidechain input). This is the centerpiece, build it carefully. Sources: musicdsp "SimpleComp"/EnvelopeDetector — feed-forward peak, detect sidechain pre-gain-reduction ([SimpleComp class](https://www.musicdsp.org/en/latest/Effects/204-simple-compressor-class-c.html), [KVR envelope-detector thread](https://www.kvraudio.com/forum/viewtopic.php?t=89402)); coefficient `ga=exp(-1/(SR*attack))` / `gr=exp(-1/(SR*release))` ([KVR](https://www.kvraudio.com/forum/viewtopic.php?p=2985317), [earlevel/MATLAB confirm exp form](https://www.mathworks.com/help/audio/ref/compressor-system-object.html)); ratio semantics ([compressor explained](https://christiangaertner.github.io/uhh-digitalaudioprocessing/c/2018/01/29/compressor-explanation.html)); ref C++ impls [CTAGDRC](https://github.com/p-hlp/CTAGDRC), [jcurtis4207 JUCE](https://github.com/jcurtis4207/Juce-Plugins). |
| **Gate (noise gate / expander)** | Same envelope detector as the compressor; when `envDB < thresholdDB` → ramp gain toward 0 (with attack/hold/release ballistics, downward-expander slope). Reuses the compressor's detector + ballistics code. | **No.** | **Easy–Medium** once the compressor exists (shares detector). Same musicdsp envelope source as above. |
| **Soft-clip / saturation / distortion** | Memoryless waveshaper applied per-sample: `tanh(drive*x)` (smooth) or cubic soft-clip `x - x³/3` (clamped to ±1) or hard clip `clamp(x,-1,1)`. Optional pre-gain (drive) + post-gain makeup. becky's synth already uses a `tanh` limiter on its master (`internal/audioengine/synth.go`), so the pattern is in-repo. | **No** (trivial; beep has none). | **Trivial** (1–5 lines per shaper). No external source needed — standard nonlinear shaping. |
| **Delay line (with feedback)** | Circular buffer of `delaySamples = delay_s*fs`; per sample: `out = buf[read]; buf[write] = x + feedback*out; advance read/write (wrap)`. Add a wet/dry mix and an optional one-pole lowpass in the feedback path for analog-style repeats. | **No** (beep has none). | **Easy** (~25 lines). The same circular-delay primitive underlies the reverb. |
| **Reverb (Schroeder / Freeverb)** | **Freeverb** = 8 parallel **lowpass-feedback comb filters** + 4 series **allpass** filters per channel; sum the combs, run through the allpasses. Comb diff-eq: `y[n] = buf; filt = filt*(1-damp) + buf_lp; buf_new = x + filt*feedback`. Stereo: add `stereospread=23` to the right-channel delay lengths. **Tuning (44.1 kHz, scale to 48 kHz):** combs L = 1116,1188,1277,1356,1422,1491,1557,1617; allpasses L = 556,441,341,225; `fixedgain=0.015, scaledamp=0.4, scaleroom=0.28, offsetroom=0.7, initialroom=0.5, initialdamp=0.5`. | **No Go lib** (faust/C reference only). | **Medium** (comb + allpass structs + the 12 tuned delay lines; ~120 lines but mechanical). Sources: [CCRMA Freeverb](https://ccrma.stanford.edu/~jos/pasp/Freeverb.html), [tuning.h constants](https://github.com/alexmacrae/SamplerBox/blob/master/freeverb/tuning.h), [Valhalla Schroeder](https://valhalladsp.com/2009/05/30/schroeder-reverbs-the-forgotten-algorithm/). A simpler v1 is a plain Schroeder reverb (4 combs + 2 allpasses). |
| **Gain / pan law** | Gain: `out = x * lin(dB)` where `lin(dB)=10^(dB/20)`. Pan: **equal-power** (constant-power) law — `θ=(pan+1)*π/4; L=x*cos θ; R=x*sin θ` (pan ∈ [-1,1]); linear pan is cheaper but dips 3 dB at center. | **Yes** — `gopxl/beep` `Gain`, `Volume` (log), `Pan` (MIT). | **Trivial** to hand-roll; or use beep's. Sources: [beep effects](https://pkg.go.dev/github.com/gopxl/beep/v2/effects). |

### Denormals & latency (the two real-time gotchas)

- **Denormals.** Feedback effects (comb/allpass reverb, delay feedback, filter state, compressor
  envelope) decay toward zero and hit *denormal* floats, which on x86 can cost 10–100× CPU and can stall
  the audio thread ([randomascii](https://randomascii.wordpress.com/2012/05/20/thats-not-normalthe-performance-of-odd-floats/),
  [EarLevel denormals](https://www.earlevel.com/main/2019/04/19/floating-point-denormals/)). C++ fixes
  with the SSE FTZ/DAZ CPU flags, but **Go gives no portable way to set FTZ/DAZ**. Use the portable
  software fix: add a tiny DC offset / "anti-denormal" constant `~1e-18` (about −200 dB, inaudible) to
  feedback state each sample, or flush sub-threshold values to 0 (`if math.Abs(v) < 1e-15 { v = 0 }`)
  ([EarLevel](https://www.earlevel.com/main/2012/12/03/a-note-about-de-normalization/),
  [randomascii](https://randomascii.wordpress.com/2012/05/20/thats-not-normalthe-performance-of-odd-floats/)).
  Apply it in every effect that has a recursive/feedback term.
- **Latency / real-time discipline.** The render callback must be allocation-free and lock-free: no
  `make`, no channel ops, no `mutex.Lock`, no logging on the audio path (Go GC pauses + lock contention
  cause dropouts). Pre-allocate all delay/comb buffers at construction; pass parameter changes via
  pre-allocated atomics or a single-reader ring, never by allocating. Lookahead limiting adds latency =
  lookahead window; for a drum machine keep lookahead off in v1. Buffer size on oto/v3 / miniaudio sets
  baseline latency; 48 kHz with a few-ms buffer is the target.

### What's cheap to hand-roll vs worth a library

- **Hand-roll (cheap, and keeps determinism + zero deps):** soft-clip/saturation, gain/pan, delay line,
  gate. These are a handful of lines each.
- **Hand-roll but write carefully:** the **sidechain compressor/limiter** (the marquee feature) and the
  **Freeverb** reverb — both are mechanical given the cited formulas/constants but have the most state.
- **Library worth it only to skip typing:** **biquad EQ** via `moutend/go-equalizer` (MIT) — the one
  effect with a clean, ready Go implementation. Everything in `gopxl/beep` we'd want (gain/pan) is
  trivial enough to inline, but beep is MIT if we prefer to depend on it.

---

## Part B — MIDI in Go

### Recommended library: `gitlab.com/gomidi/midi/v2` (MIT)

This is the de-facto standard Go MIDI library: full MIDI standard (cable + SMF), unified driver
interface, pure-Go core with **no stdlib-external deps**, GM/Sysex shortcuts
([gomidi v2](https://pkg.go.dev/gitlab.com/gomidi/midi/v2), repo moved to GitLab with a GitHub mirror,
[gomidi/midi](https://github.com/gomidi/midi)). License: **MIT**.

**SMF read/write** is the `smf` subpackage (`gitlab.com/gomidi/midi/v2/smf`) — write type-0/1 files,
read with running-status support, `io.Reader`/`io.Writer` integration
([smf](https://pkg.go.dev/gitlab.com/gomidi/midi/v2/smf)).

**Overlap with becky's existing SMF writer:** becky already has a **pure-Go, dependency-free, deterministic**
type-1 SMF writer/reader in `internal/music/smf.go` + `smfread.go` used by `becky-compose`. For *exporting
patterns / piano-roll to `.mid`*, **reuse that** — it already guarantees byte-identical output (becky's
determinism invariant) and adds no dependency. Bringing in gomidi's `smf` for export would duplicate it.
So: **MIDI export = extend `internal/music` SMF; MIDI realtime I/O = add gomidi v2.** Use gomidi only for
the thing becky can't do today: live ports.

### Windows realtime story (cgo or not)

gomidi's realtime I/O is driver-pluggable; the drivers differ on cgo
([gomidi v2 drivers](https://pkg.go.dev/gitlab.com/gomidi/midi/v2),
[rtmididrv](https://pkg.go.dev/gitlab.com/gomidi/midi/v2/drivers/rtmididrv)):

| Driver | Backend | CGO? | Notes for Windows |
|---|---|---|---|
| **rtmididrv** | RtMidi (C++) | **Yes (cgo)** | Cross-platform; on Windows uses the built-in WinMM MIDI — no extra runtime install. The usual production choice. Needs a C++ toolchain at build (becky already has mingw at `C:\msys64\mingw64\bin\gcc.exe` for the `audio` build, so this is consistent). |
| **portmididrv** | PortMidi (C) | **Yes (cgo)** | Cross-platform alternative; needs the portmidi lib. |
| **midicatdrv** | pipes to a `midicat` helper binary | **No cgo** | Pure-Go side; shells out to the prebuilt `midicat` tool. Avoids cgo at the cost of shipping/depending on that binary. |
| **webmididrv** | Web MIDI API (WASM) | No | Browser only — not relevant to the Windows desktop tool. |
| **testdrv** | in-memory | No | For unit tests — lets us test MIDI-learn logic in CI with no hardware/cgo (matches becky's "deterministic fakes" pattern). |

**Recommendation for the drum machine on Jordan's Win10 PC:** use **rtmididrv (cgo)** — it's the
standard, talks to WinMM with no end-user install, and the build already tolerates cgo behind a build
tag. Gate it behind a `midi` build tag (like the existing `audio` tag) so the default `go build ./...`
stays pure-Go/green on CI, and use **testdrv** for unit tests. If we ever want to keep the realtime path
cgo-free, `midicatdrv` is the fallback (bundle the `midicat` binary). Note: `rtmididrv` requires CGO and
is multi-platform per its docs ([rtmididrv](https://pkg.go.dev/gitlab.com/gomidi/midi/v2/drivers/rtmididrv)).

### MIDI export of patterns / piano-roll → `.mid`

Use becky's existing `internal/music` SMF writer: convert each pad/lane hit to a NoteOn at its tick +
NoteOff (drums on GM channel 9), set tempo/ppq meta, write type-1 with one track per lane (or one merged
track). This is already how `becky-compose` emits stems, so the piano-roll/drum-grid export is a thin
adapter over `dawmodel.DrumGrid` / `[]Note` → `music.Track`. (gomidi's `smf` is the alternative if we
ever drop the in-house writer, but there's no reason to.)

### MIDI-controller input + "MIDI learn" (drive pads/knobs from Maschine or any controller)

gomidi v2 makes this direct ([gomidi v2 examples](https://github.com/gomidi/midi/blob/master/v2/example_test.go)):

```go
import "gitlab.com/gomidi/midi/v2"
import _ "gitlab.com/gomidi/midi/v2/drivers/rtmididrv" // registers the driver (cgo)

in, _ := midi.FindInPort("Maschine")          // or midi.InPort(0); midi.GetInPorts() to list
stop, _ := midi.ListenTo(in, func(msg midi.Message, ts int32) {
    var ch, key, vel, cc, val uint8
    if msg.GetNoteStart(&ch, &key, &vel) {  /* pad hit  -> trigger voice */ }
    if msg.GetNoteEnd(&ch, &key)         {  /* pad off              */ }
    if msg.GetControlChange(&ch, &cc, &val) { /* knob -> param      */ }
})
defer stop()
defer midi.CloseDriver()
```

**MIDI-learn mapping:** put the app in "learn" mode for a target param; the *next* incoming CC (or note)
in the callback captures `(channel, controller)` and is stored as the binding for that param; subsequent
matching messages scale `val/127` into the param's range. Persist the bindings as a small JSON map
(`{"cutoff": {"cc":74,"ch":0}, ...}`) so they survive restarts. This is exactly becky's "learn the user's
preference" pattern and ties into the existing `internal/habits` corrections log if desired. The
`testdrv` driver lets us unit-test the learn/dispatch logic deterministically with no hardware.

---

## (3) v1-minimal vs later split

**v1 (build now — pure-Go, deterministic, the moves Jordan actually asked for):**
1. **Gain + equal-power pan** per channel/bus — trivial, foundational.
2. **One sidechain compressor** (feed-forward peak, attack/release ballistics, dB gain computer, with a
   sidechain input) — *the* feature: kick→bass / kick→synth ducking. Include a limiter mode (ratio→∞).
3. **Biquad EQ** (RBJ) per channel — optionally via `moutend/go-equalizer` (MIT) to save time, else
   hand-rolled.
4. **Soft-clip/saturation** — basically free, big sonic payoff on drums.
5. **MIDI export** of the pattern/piano-roll via becky's existing `internal/music` SMF writer (no new dep).
6. **Anti-denormal guard + allocation-free callback discipline** baked into every effect from day one.

**Later (Phase-2):**
- **Reverb** (Freeverb) and **delay-with-feedback** — desirable but not load-bearing; more state, can
  ship after the mixer core is solid.
- **Gate / downward expander** — easy once the compressor's detector exists; add when needed.
- **Realtime MIDI input + MIDI-learn** via gomidi `rtmididrv` (cgo, `midi` build tag) so Maschine/any
  controller drives pads + knobs — genuinely useful but it's the hardware/cgo boundary, so it lands after
  the offline core and gets `testdrv`-based unit tests.

**Build-tag hygiene (matches the repo):** keep effects pure-Go in the default build; gate any cgo
(realtime MIDI like the existing `audio` tag) behind a `midi` tag so `go build ./...` + CI stay green.

---

## Sources

- RBJ Audio EQ Cookbook: https://webaudio.github.io/Audio-EQ-Cookbook/Audio-EQ-Cookbook.txt , https://www.musicdsp.org/en/latest/Filters/197-rbj-audio-eq-cookbook.html
- go-equalizer (MIT, RBJ biquads): https://github.com/moutend/go-equalizer
- go-dsp biquad: https://pkg.go.dev/github.com/samuel/go-dsp/dsp ; mattetti filters: https://pkg.go.dev/github.com/mattetti/audio/dsp/filters
- gopxl/beep effects (MIT): https://pkg.go.dev/github.com/gopxl/beep/v2/effects ; faiface/beep (archived): https://github.com/faiface/beep
- Compressor / envelope detector: https://www.musicdsp.org/en/latest/Effects/204-simple-compressor-class-c.html , https://www.kvraudio.com/forum/viewtopic.php?t=89402 , https://www.kvraudio.com/forum/viewtopic.php?p=2985317 , https://christiangaertner.github.io/uhh-digitalaudioprocessing/c/2018/01/29/compressor-explanation.html , https://www.mathworks.com/help/audio/ref/compressor-system-object.html
- Compressor reference C++: https://github.com/p-hlp/CTAGDRC , https://github.com/jcurtis4207/Juce-Plugins
- Freeverb: https://ccrma.stanford.edu/~jos/pasp/Freeverb.html , https://www.dsprelated.com/freebooks/pasp/Freeverb.html , tuning.h: https://github.com/alexmacrae/SamplerBox/blob/master/freeverb/tuning.h , Schroeder: https://valhalladsp.com/2009/05/30/schroeder-reverbs-the-forgotten-algorithm/
- Denormals: https://randomascii.wordpress.com/2012/05/20/thats-not-normalthe-performance-of-odd-floats/ , https://www.earlevel.com/main/2019/04/19/floating-point-denormals/ , https://www.earlevel.com/main/2012/12/03/a-note-about-de-normalization/
- gomidi v2 (MIT): https://pkg.go.dev/gitlab.com/gomidi/midi/v2 , https://github.com/gomidi/midi , smf: https://pkg.go.dev/gitlab.com/gomidi/midi/v2/smf , rtmididrv: https://pkg.go.dev/gitlab.com/gomidi/midi/v2/drivers/rtmididrv , examples: https://github.com/gomidi/midi/blob/master/v2/example_test.go
- oto/v3 (audio output, Apache-2.0, ebitengine): https://pkg.go.dev/github.com/ebitengine/oto/v3 , https://github.com/hajimehoshi/oto
