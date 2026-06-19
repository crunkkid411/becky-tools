# Real-time, interactive audio for a Go drum machine on Windows — the proven approach

**Date:** 2026-06-19
**Question:** What actually works for live pad hits + a loop you edit while it plays, in a Go drum machine with a Gio (Direct3D11) GUI, built one-click by a non-developer?
**Verdict up front:** **Standardize on `github.com/ebitengine/oto/v3`** (the maintained successor to `hajimehoshi/oto`). It is the only current option that gives you a continuously-streamed, low-latency output you feed live **and** needs **no cgo / no C compiler on Windows** (it uses `purego`), so the one-click build for Jordan stays one-click. Your previous "render WAV → shell out to a player" approach is replaced by: implement one `io.Reader` whose `Read()` mixes all voices, hand it to one `oto` player, and never stop it.

The catch you must internalize: oto exposes a **pull model** (`io.Reader`), not a named "callback". That is not a downside — *a background goroutine calls your `Read()` continuously*, which is exactly a pull-callback. You write the mixer inside `Read()`. That is the whole trick, and it is the same pattern a malgo/PortAudio `DataCallback` gives you, minus the C toolchain.

---

## 1. Real-time output libraries for Go on Windows — compared

| Library | Import path | License | Stars / maturity | Last activity | Windows backend | cgo on Windows? | Latency | Live continuous stream you feed? |
|---|---|---|---|---|---|---|---|---|
| **oto v3** | `github.com/ebitengine/oto/v3` | Apache-2.0 | ~1.9k, very mature (powers Ebitengine) | v3.4.0, Oct 2025 | **WASAPI** (+ WinMM fallback) | **No** (uses `purego`) | Tunable via `BufferSize`; small buffer = low latency, too small = glitches | **Yes** — pull model: a bg goroutine calls your `io.Reader.Read()` forever |
| oto v2 | `github.com/hajimehoshi/oto/v2` | Apache-2.0 | (same lineage) **deprecated** | superseded by v3 | WASAPI | **Yes** (v2 needed cgo on Windows) | same model as v3 | Yes (same io.Reader model) but deprecated — do not start here |
| **malgo** | `github.com/gen2brain/malgo` | Unlicense | ~415, mature bindings | active | WASAPI / DirectSound / WinMM | **Yes** — needs a C compiler (MinGW) to compile `miniaudio.c` | very low (miniaudio is purpose-built for it) | **Yes** — true `DataCallback(out, in []byte, frames uint32)` |
| **gopxl/beep v2** | `github.com/gopxl/beep/v2` | MIT | ~573, **the maintained beep** (faiface/beep is archived) | v2.1.1, Jan 2025 | (via oto v3) WASAPI | **No** (depends on `ebitengine/oto/v3` v3.3.2) | inherits oto's | **Yes** — `Mixer.Add()` live; built on the oto stream |
| portaudio | `github.com/gordonklaus/portaudio` | MIT | mature bindings, thin wrapper over PortAudio C lib | low churn | WASAPI/DirectSound/WDM-KS/MME (PortAudio's host APIs) | **Yes — worst case**: needs gcc **and** PortAudio dev headers/libs + `pkg-config` finding `portaudio-2.0.pc` | low (PortAudio is the pro standard) | **Yes** — real stream callback, `LowLatencyParameters` helper |

### Notes that decide it
- **faiface/beep is archived / no longer developed**; the living fork is **`gopxl/beep`** (MIT). Don't depend on faiface.
- **oto v3 dropped cgo on Windows** by switching to `ebitengine/purego` (call C/OS functions without cgo). v2 *did* require cgo on Windows and is now deprecated — this is the single most important version fact.
- **malgo "requires cgo but does not require linking to anything on Windows"** — read carefully: *no linking* ≠ *no compiler*. It still **compiles `miniaudio.c`**, so a Windows build needs MinGW/gcc on `PATH`. (Your repo already has `C:\msys64\mingw64\bin\gcc.exe`, but that's an extra dependency the one-click build must guarantee.)
- **portaudio** is the heaviest Windows build: multiple users hit `pkg-config can't find portaudio-2.0.pc`. Great library, wrong fit for a zero-friction non-dev build.

---

## 2. The mixing reality — the callback-mixer pattern

To trigger many overlapping one-shots (each pad hit spawns a voice) plus a running 16-step pattern, **you do the mixing**, the library only hands you buffers. The universal pattern, regardless of library:

1. One output stream/device/player is opened **once** with a fixed format (e.g. 44100 Hz, 2ch, int16 or float32).
2. The library repeatedly asks you to fill a small buffer (a few ms of audio):
   - **oto v3:** your `io.Reader.Read(p []byte)` is called — fill `p`.
   - **malgo:** `Data func(pOutput, pInput []byte, frames uint32)` — fill `pOutput`.
   - **portaudio:** `func(out [][]float32)` (or interleaved) — fill `out`.
   - **gopxl/beep:** `Mixer.Stream(samples [][2]float64)` does this for you; you only `Add()` streamers.
3. Inside that fill, you **sum every active voice** sample-by-sample, advance each voice's read position, drop voices that finished, and clamp/limit the sum to avoid clipping. Sequencer timing is computed in *sample frames* against a running global sample counter, so the beat is sample-accurate and never drifts.

**Which libraries expose the needed live-feed mechanism:** all four do. oto v3 (pull `io.Reader`), malgo (push `DataCallback`), portaudio (push callback), gopxl/beep (`Mixer.Add` + internal `Stream`). The difference is *build cost*, not capability.

**Hard rule for that fill function:** it runs on the audio thread. No allocations, no mutexes you might block on, no file I/O, no model calls. Load/decode WAVs to PCM **up front**; the fill function only reads pre-decoded slices and adds ints/floats. Communicate "play pad 3 now" via a lock-free-ish channel or an atomic/`sync.Mutex`-guarded slice you copy quickly — keep the critical section to microseconds.

---

## 3. cgo build pain on Windows — the honest trade-off

This is the crux for a one-click build by a non-developer.

- **oto v3 — pure Go on Windows (no cgo).** `go build` with the default toolchain produces the `.exe`. No MSYS2, no MinGW, no `gcc.exe` on PATH, nothing to install. This is the entire reason it wins for Jordan. Cross-compiling to Windows from Linux also Just Works (`GOOS=windows go build`), which means the **cloud agent can compile-verify the audio build**, not just the GUI.
- **malgo — needs MinGW/gcc.** It bundles `miniaudio.c` and compiles it via cgo, so a Windows build requires a C compiler discoverable on PATH (`CGO_ENABLED=1` + `gcc`). It links nothing extra (good), but the compiler is a hard prerequisite. If gcc isn't present/at the expected path, the build silently degrades or fails — exactly the failure class that left Jordan stuck before.
- **portaudio — needs gcc *and* the PortAudio dev package.** cgo + headers + libs + `pkg-config` resolving `portaudio-2.0.pc`. The most setup, the most documented Windows breakage. Avoid for one-click.

Trade-off in one line: **pure-Go (oto/beep) = a one-double-click `go build` for a non-dev; cgo (malgo/portaudio) = marginally lower latency and a "real" callback, paid for with an MSYS2/MinGW dependency every build.** For a drum machine, oto's tunable buffer already reaches low-enough latency for pad triggering; the cgo tax buys you little here and costs you the one-click guarantee.

---

## 4. Gio compatibility — yes, run audio on its own goroutine

Gio (`gioui.org`, Direct3D 11 on Windows) owns the main/GUI event loop. The audio engine must be **independent of it**:

- With **oto v3**: `oto.NewContext(...)` spins up its own background goroutine that pulls your `io.Reader`. You call `NewContext` + `player.Play()` once at startup (from any goroutine), and audio runs forever on oto's own goroutine. The Gio loop never touches it. The GUI thread only sends messages ("pad 3 pressed", "tempo = 128", "toggle step 5") into the engine via a channel or a mutex-guarded state struct. This is the clean separation you want: **Gio renders; oto sounds; they meet only at a thin message boundary.**
- Same is true for malgo/portaudio (their callbacks run on the driver's audio thread) and beep (its speaker runs oto under the hood). None of them require being on the Gio thread; in fact they must *not* be — never block the audio fill on a GUI frame, and never do audio work on the GUI thread.
- Your existing split already proves this is the right shape: `cmd/drummachine` stays a pure `-tags gui` Gio window, and the sound lives behind a build tag. With oto v3 you can fold the engine **into the same binary** (no cgo conflict with Gio) instead of exec'ing a sibling `becky-daw-engine` — one process, in-memory triggering, lowest latency, no process-launch jitter on every pad hit.

---

## RECOMMENDATION — standardize on `ebitengine/oto/v3`

**Pick oto v3.** Justification against the one-click-build-for-a-non-dev constraint:
1. **No cgo on Windows** → the build is the default `go build`, no MinGW dependency that can be missing → no silent build failure for Jordan. This single fact outweighs malgo's marginal latency edge.
2. **It's the maintained, widely-deployed standard** (Apache-2.0, ~1.9k stars, powers Ebitengine, released Oct 2025). Low bus-factor risk.
3. The `io.Reader` pull model **is** a callback-mixer once you write `Read()` — it does live pad hits and a loop-you-edit-while-playing natively.
4. It runs on its own goroutine, cleanly decoupled from the Gio loop, and can live **in the same binary** as the Gio GUI (no cgo to fight with), killing the render-WAV-then-shell-out latency entirely.

**When to reconsider:** if you later need sub-5ms hard-real-time or ASIO-class pro latency, drop to **malgo** (accepting the MinGW build tax — and your repo already has gcc). Keep the engine behind an interface so swapping oto→malgo is a backend change, not a rewrite. **gopxl/beep** is a fine higher-level alternative (it gives you the Mixer + WAV/MP3 decoders for free and is *also* pure-Go on Windows via oto v3) — choose it if you'd rather not hand-roll the mixer; choose raw oto if you want full control of the sample-accurate sequencer. Either way you're on the oto v3 foundation.

### Minimal end-to-end sketch (raw oto v3, one binary, live mixer + 16-step loop)

```go
package main

import (
	"encoding/binary"
	"sync"
	"time"

	"github.com/ebitengine/oto/v3"
)

const (
	sampleRate = 44100
	channels   = 2 // stereo
)

// ---- a Voice is one playing sample (a pad hit) ----
type Voice struct {
	pcm []int16 // pre-decoded mono samples for this drum hit
	pos int     // current read position; >= len(pcm) means finished
	gain float64
}

// ---- Engine is the single io.Reader oto pulls forever ----
type Engine struct {
	mu sync.Mutex

	voices []*Voice // active one-shots (pad hits + sequenced hits)

	// pre-decoded kit: index by pad (0..15)
	kit [16][]int16

	// sequencer
	playing   bool
	tempoBPM  float64
	steps     [16][16]bool // [pad][step] on/off
	frame     int64        // global sample-frame counter (sample-accurate clock)
	nextStep  int64        // frame index at which the next step fires
	stepIdx   int
}

func NewEngine() *Engine {
	return &Engine{tempoBPM: 120}
}

// framesPerStep: a 16-step bar of 16th notes. 1 beat = 4 steps.
func (e *Engine) framesPerStep() int64 {
	secPerBeat := 60.0 / e.tempoBPM
	secPerStep := secPerBeat / 4.0
	return int64(secPerStep * sampleRate)
}

// Trigger a pad immediately (called from the Gio/GUI goroutine on a pad press).
func (e *Engine) Trigger(pad int) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.voices = append(e.voices, &Voice{pcm: e.kit[pad], gain: 1.0})
}

func (e *Engine) SetTempo(bpm float64) { e.mu.Lock(); e.tempoBPM = bpm; e.mu.Unlock() }
func (e *Engine) Play()                { e.mu.Lock(); e.playing = true; e.mu.Unlock() }
func (e *Engine) Stop()               { e.mu.Lock(); e.playing = false; e.mu.Unlock() }

// ToggleStep can be called WHILE PLAYING — this is "edit the loop as it runs".
func (e *Engine) ToggleStep(pad, step int) {
	e.mu.Lock()
	e.steps[pad][step] = !e.steps[pad][step]
	e.mu.Unlock()
}

// Read is THE callback. oto calls it on its own goroutine, forever.
// p is interleaved stereo int16 little-endian. Fill it; never block.
func (e *Engine) Read(p []byte) (int, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	nFrames := len(p) / (2 * channels) // 2 bytes/sample * channels
	fps := e.framesPerStep()

	for i := 0; i < nFrames; i++ {
		// --- sequencer: fire any steps due at this exact frame ---
		if e.playing && fps > 0 && e.frame >= e.nextStep {
			for pad := 0; pad < 16; pad++ {
				if e.steps[pad][e.stepIdx] {
					e.voices = append(e.voices, &Voice{pcm: e.kit[pad], gain: 1.0})
				}
			}
			e.stepIdx = (e.stepIdx + 1) % 16
			e.nextStep += fps
		}

		// --- mix all active voices into one sample ---
		var acc float64
		live := e.voices[:0]
		for _, v := range e.voices {
			if v.pos < len(v.pcm) {
				acc += float64(v.pcm[v.pos]) * v.gain
				v.pos++
				live = append(live, v) // keep voices that still have samples
			}
		}
		e.voices = live // dropped finished voices (no realloc)

		// clamp to int16 (cheap soft-limit: just clip; tanh limiter is nicer)
		if acc > 32767 {
			acc = 32767
		} else if acc < -32768 {
			acc = -32768
		}
		s := int16(acc)

		// write same sample to L and R
		off := i * 2 * channels
		binary.LittleEndian.PutUint16(p[off:], uint16(s))
		binary.LittleEndian.PutUint16(p[off+2:], uint16(s))

		e.frame++
	}
	return len(p), nil // never return EOF: an instrument plays silence, not "done"
}

func main() {
	eng := NewEngine()
	// loadKit(eng) // decode kick.wav/snare.wav/... to []int16 into eng.kit, up front.

	op := &oto.NewContextOptions{
		SampleRate:   sampleRate,
		ChannelCount: channels,
		Format:       oto.FormatSignedInt16LE,
		BufferSize:   10 * time.Millisecond, // tune: smaller = lower latency, too small = glitches
	}
	ctx, ready, err := oto.NewContext(op)
	if err != nil {
		panic(err)
	}
	<-ready // wait for the audio hardware

	player := ctx.NewPlayer(eng) // <- the engine IS the io.Reader oto pulls
	player.Play()                // audio now runs on oto's goroutine, forever

	// ... start the Gio window here on its own goroutine; on a pad press call
	// eng.Trigger(pad); the ▶ button calls eng.Play(); toggling a grid cell
	// calls eng.ToggleStep(pad, step) — all while audio keeps streaming. ...

	select {} // keep the process alive
}
```

### What is genuinely hard (don't pretend otherwise)
- **The mutex in `Read()`.** The sketch locks `e.mu` for the whole fill, and the GUI also locks it to trigger/toggle. With a tiny buffer this can cause an audible glitch if the GUI holds the lock during a fill. Real fix: keep GUI critical sections to a few instructions (just append a trigger / flip a bool), or pass triggers via a buffered channel that `Read()` drains non-blockingly at the top of each call. Never decode a WAV or call a model while holding it.
- **Latency vs glitches is a real knob, not free.** `BufferSize` ~5–10 ms feels responsive; push it too low and you get dropouts (XRUNs) on a busy machine. There is no universal "right" value — expose it and let it default safely (oto's default if you pass 0).
- **Sample-accurate timing must use the frame counter**, not `time.Now()` or `time.Ticker`. The sketch advances `e.frame` per sample and fires steps against it; that's what keeps the beat from drifting. A `time.Ticker`-driven sequencer *will* audibly wobble.
- **Clipping.** Summing 16 simultaneous hits overflows int16. The sketch hard-clips; a `tanh` soft-limiter (you already have one in `internal/audioengine/synth.go`) sounds far better and is the right call.
- **Voice management / polyphony.** Fast repeated hits on one pad pile up voices; you'll want a voice cap and a choke-group (open hat cut by closed hat) — your `internal/drummachine` model already encodes choke groups, so honor them when spawning voices.
- **Format conversion up front.** Decode every kit WAV to the engine's exact sample rate/format *once* at load (resample if needed). Doing any decode/resample inside `Read()` is the classic real-time sin.
- **In-window file drag-drop is unrelated** but still the Gio gap noted elsewhere in the repo — not an audio problem.

---

## Top sources

- [ebitengine/oto (GitHub) — low-level multi-platform sound, no cgo on Windows](https://github.com/hajimehoshi/oto)
- [oto v3 API reference (pkg.go.dev)](https://pkg.go.dev/github.com/ebitengine/oto/v3)
- [oto v2 (deprecated, cgo on Windows) (pkg.go.dev)](https://pkg.go.dev/github.com/hajimehoshi/oto/v2)
- [ebitengine/oto releases (v3.4.0, Oct 2025)](https://github.com/ebitengine/oto/releases)
- [ebitengine/purego — call C without cgo (why oto v3 is pure-Go on Windows)](https://github.com/ebitengine/purego)
- [gen2brain/malgo (GitHub) — miniaudio bindings, cgo, WASAPI/DSound/WinMM](https://github.com/gen2brain/malgo)
- [malgo playback example — DataCallback signature](https://github.com/gen2brain/malgo/blob/master/_examples/playback/playback.go)
- [gopxl/beep (GitHub) — maintained successor to faiface/beep](https://github.com/gopxl/beep)
- [gopxl/beep v2 API incl. Mixer (pkg.go.dev)](https://pkg.go.dev/github.com/gopxl/beep/v2)
- [faiface/beep (GitHub) — archived; points to gopxl](https://github.com/faiface/beep)
- [gordonklaus/portaudio (GitHub) — PortAudio Go bindings, cgo](https://github.com/gordonklaus/portaudio)
- [gordonklaus/portaudio API incl. LowLatencyParameters (pkg.go.dev)](https://pkg.go.dev/github.com/gordonklaus/portaudio)
- [gordonklaus/portaudio issue #47 — Windows install pain (pkg-config / portaudio-2.0.pc)](https://github.com/gordonklaus/portaudio/issues/47)
