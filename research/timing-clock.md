# Timing, Clock & Sample-Accurate Scheduling for becky's Drum Machine / Sequencer

Research brief for the becky-canvas DAW / drum-machine timing engine. Goal: the beat
**never drifts**, and live edits (pad hits, tempo changes, pattern edits) stay in time.
Audio path is **Go + ebitengine/oto v3** (a pull-model `io.Reader` mixer). Every claim
below is cited. Where something is not yet decided/needed for v1 it is marked **[LATER]**.

> TL;DR of the whole document: **The audio device pulls samples from your `io.Reader`.
> That `Read` method is your audio callback. Keep ONE monotonically-increasing
> `frameCounter` there, convert tempo+PPQ+sampleRate into a `samplesPerStep`, and fire
> a step the instant the counter crosses its sample position. Do NOT drive the beat from
> `time.Ticker`/wall-clock. Live pad hits are mixed in immediately; sequenced steps are
> scheduled by frame index. Everything else (look-ahead, swing, loop wrap, tempo change)
> is bookkeeping on top of that one counter.**

---

## 0. Why this matters / the core failure mode

If you drive a sequencer from a wall-clock timer (`time.Ticker`, `setTimeout`,
`Date.now()`), the beat **drifts and jitters**, because that timer runs on a general-purpose
thread that gets preempted by GC, layout, rendering, and OS scheduling. Chris Wilson's
canonical article documents callbacks "skewed by tens of milliseconds or more," and a real
case where a timer fired ~50 ms late.[^webdev] `Date.now()` is only millisecond-precise, and
at 44.1 kHz **one millisecond is ~44 samples** — far coarser than glitch-free audio
needs.[^webdev]

The audio hardware, by contrast, consumes samples at an exact, fixed rate. The number of
samples it has consumed **is** a perfect clock: `currentTime = samplesPlayed / sampleRate`
(this is literally how Web Audio defines `AudioContext.currentTime`).[^webdev][^sonoport]
The discipline is therefore: **measure time in sample-frames, counted inside the audio
callback — never in wall-clock seconds on another thread.**[^kvr][^cp3][^loopy]

---

## 1. The precise math

### 1.1 Definitions

- **sampleRate** `sr` — frames/second the device consumes (e.g. 44100 or 48000). In oto
  this is the `SampleRate` you pass to `NewContext`.[^otodoc]
- **BPM** — quarter-note beats per minute (the tempo).
- **PPQ / PPQN** — *Pulses (ticks) Per Quarter Note*: the internal resolution at which the
  sequencer places events.[^ppqn][^sweetwater] Common values: **24** (the MIDI-clock
  standard, the minimum that still expresses triplets + swing), 96, 120, 480, **960**
  (modern DAWs, for human-feel nuance).[^ppqn] Higher PPQ = finer placement; lower PPQ can
  sound quantized/artificial.[^ppqn]
- **stepsPerBeat** — for a classic 16-pad drum machine this is **4** (sixteenth notes =
  ¼ of a quarter note).[^webdev]

### 1.2 The fundamental conversion (memorize this)

Duration of one beat (quarter note) is `60 / BPM` seconds, so:

```
samplesPerBeat = sr * 60 / BPM            # frames in one quarter note
```

Worked example: 90 BPM @ 44.1 kHz → `60/90 * 44100 = 29400` samples/quarter; an eighth
note is `14700`.[^sweetcare] These are the formulas every DAW/step-sequencer uses to turn
tempo into a sample count.[^sweetcare][^maxmsp]

From there:

```
samplesPerStep = samplesPerBeat / stepsPerBeat      # 16-step grid → /4
               = (sr * 60) / (BPM * stepsPerBeat)

samplesPerTick = samplesPerBeat / PPQ               # finest internal grid
               = (sr * 60) / (BPM * PPQ)
```

So a step's absolute sample position from song start is just:

```
stepSamplePos(i) = round(i * samplesPerStep)        # i = 0,1,2,...
```

Use **floating-point accumulation or round-from-the-running-product**, never integer
`samplesPerStep` added repeatedly — integer truncation accumulates and drifts. Compute each
position from `i * samplesPerStep` (the product) and round once, so error never accumulates.

> **PPQ note for v1.** A 16-pad grid only *needs* 16 positions per bar. But store the model
> in **ticks at a real PPQ (recommend 96 or 480)** even in v1, so swing, micro-timing, and
> later MIDI/`.mid` import (which carry their own division) map in cleanly. At 24 PPQ you can
> still do triplets and swing by counting alternate ticks;[^ppqn] 96/480 gives headroom for
> human-feel without rework.

### 1.3 Swing / shuffle math

Swing delays the **off-beat** subdivisions (the even-indexed 8ths/16ths in a 0-based grid)
by a fraction of the step. It is applied **at schedule time** — you nudge the computed sample
position; you do not change the clock.[^webdev-swing][^ircam]

```
swing ∈ [0.0 .. ~0.75]      # 0 = straight; 0.5 ≈ triplet feel; MPC "50%" ≈ this
swingOffsetSamples = swing * samplesPerStep

stepSamplePos(i) = round(i * samplesPerStep
                         + (isOffBeat(i) ? swingOffsetSamples : 0))
```

`isOffBeat(i)` is typically `i % 2 == 1` for 16th-note swing (delay every other 16th).
Hardware groove boxes express this as a swing percentage; 50 % means the off-beat sits exactly
between straight and triplet. Keep swing as a per-pattern (optionally per-track) parameter so
it can change live — because it's pure schedule-time arithmetic on the next step, a swing
change takes effect on the very next off-beat with no glitch.

### 1.4 Look-ahead window (when you also schedule from a helper goroutine)

If you ever schedule from outside the callback (see §2.4), Wilson's tuning is the reference:
**scheduler tick interval ≈ 25 ms, schedule-ahead window ≈ 100 ms**, and you schedule every
event whose time is `< now + scheduleAheadTime`.[^webdev] The windows overlap so a late
wake-up is harmless. Larger look-ahead = more robust but laggier response to tempo/edits;
smaller = snappier but riskier on a busy machine.[^webdev] **In becky's pure-callback design
(§2) the look-ahead is simply "one audio buffer," which is already tiny and rock-solid.**

---

## 2. Recommended scheduler design for becky's oto-based engine

### 2.1 Key fact about oto v3: your `Read` IS the audio callback

oto v3's model: one `Context` (sample rate, channels, format, buffer size), and `Player`s
each created from **one `io.Reader`**.[^otodoc] The device **pulls** PCM by calling that
reader's `Read` as its internal buffer drains — "data is read from the provided `io.Reader`
into the player's internal buffer … on-demand as its internal buffer has space."[^otodoc]
A single reader must not be shared by multiple players.[^otodoc]

Therefore the right architecture is **one master `Player` fed by one mixer `io.Reader`**.
That reader's `Read([]byte)` call is becky's audio callback. Inside it you (1) advance the
global frame counter, (2) fire any steps whose sample position falls in this buffer,
(3) mix all active voices, (4) write PCM out. This gives sample-accurate timing **for free**,
identically at 64-frame or 2048-frame buffers — exactly the property sample-offset sequencers
have and buffer-quantized ones lack.[^loopy][^kvr]

> Do **not** make one oto Player per drum pad and call `Play()` on a hit from the UI thread:
> that reintroduces wall-clock/UI-thread timing and per-player buffer slop. Mix everything in
> the one master reader. (oto explicitly notes "when the internal buffer moves data to the
> audio device is not guaranteed, so there might be a small delay" — fine for *output
> latency*, unacceptable as your *beat clock*.)[^otopkg]

### 2.2 Buffer size vs. responsiveness

`BufferSize` in oto's `NewContextOptions` trades latency for safety: too big → audible input-
to-sound latency; too small → buffer-underrun glitches.[^otodoc] Pick the smallest buffer
that runs glitch-free on Jordan's machine (a pro interface allows smaller). Crucially, with
the §2.1 design **timing accuracy does not depend on buffer size** — only *output latency*
and *responsiveness of a live pad hit* do. Sample-accurate engines stay tight from 64 to 2048
frames; only buffer-quantized ones fall apart at large buffers / fast tempos.[^kvr][^loopy]

### 2.3 The code sketch (global frame counter → fire steps)

```go
// Master mixer: its Read is the audio callback. One oto Player wraps this.
// Stereo, FormatFloat32LE assumed; adjust for your chosen format.
type Engine struct {
    mu          sync.Mutex      // guards transport/pattern edits from UI thread

    sr          float64         // sample rate, e.g. 44100
    channels    int             // 2

    // ---- transport (the clock) ----
    playing     bool
    frame       int64           // GLOBAL frame counter = the master clock
    nextStep    int             // index of the next step to fire
    nextStepAt  int64           // absolute frame where nextStep fires

    // ---- musical params (live-editable under mu) ----
    bpm         float64
    stepsPerBar int             // 16
    swing       float64         // 0..~0.75
    loopBars    int             // 1 for a classic 16-step loop

    pattern     [][]bool        // pattern[track][step] = hit?
    kits        []Sample        // decoded one-shot PCM per track

    voices      []*Voice        // currently-sounding one-shots (pad hits + steps)
}

// samplesPerStep for the CURRENT tempo (recomputed lazily; cheap).
func (e *Engine) samplesPerStep() float64 {
    return (e.sr * 60.0) / (e.bpm * float64(e.stepsPerBar) / 4.0) // /4: 16ths
    // equivalently: samplesPerBeat / 4
}

func (e *Engine) stepFrame(step int) int64 {
    sps := e.samplesPerStep()
    off := 0.0
    if step%2 == 1 { // off-beat 16th gets the swing delay
        off = e.swing * sps
    }
    return int64(math.Round(float64(step)*sps + off))
}

// Read: the audio callback. Called by oto when it needs more PCM.
func (e *Engine) Read(buf []byte) (int, error) {
    e.mu.Lock()
    defer e.mu.Unlock()

    frames := len(buf) / (4 * e.channels) // float32 = 4 bytes
    out := bytesToFloat32(buf)            // view; or accumulate then encode
    clear(out)

    for f := 0; f < frames; f++ {
        if e.playing {
            // Fire EVERY step whose sample position == this frame.
            // (while-loop handles ultra-fast tempo / multiple steps per frame)
            for e.frame >= e.nextStepAt {
                e.fireStep(e.nextStep)          // start one-shot voices NOW
                e.advanceStep()                 // handles loop wrap + tempo
            }
            e.frame++
        }
        // Mix all live voices (pad hits AND sequenced steps share this path).
        for _, v := range e.voices {
            l, r := v.NextSample()
            out[f*e.channels] += l
            out[f*e.channels+1] += r
        }
    }
    e.reapFinishedVoices()
    floatsToBytes(out, buf)
    return len(buf), nil // a mixer never reports EOF: silence is still output
}

// advanceStep: recomputes the next fire-frame so a mid-playback tempo or swing
// change takes effect on the NEXT step (see §2.5, §2.6).
func (e *Engine) advanceStep() {
    e.nextStep++
    if e.nextStep >= e.stepsPerBar*e.loopBars {
        e.nextStep = 0                          // seamless loop wrap (§2.7)
        e.loopBase += e.barFrames()             // base frame of the new loop
    }
    e.nextStepAt = e.loopBase + e.stepFrame(e.nextStep%e.stepsPerBar)
}

func (e *Engine) fireStep(step int) {
    for t := range e.pattern {
        if e.pattern[t][step] {
            e.voices = append(e.voices, NewVoice(e.kits[t])) // sample-accurate start
        }
    }
}
```

Notes that make this correct:
- `e.frame++` happens **once per output frame**, so the clock advances at exactly the sample
  rate — this is the entire reason it can't drift.[^sonoport][^webdev]
- The **`for e.frame >= e.nextStepAt`** loop (not `if`) means even pathological tempos that
  put several steps in one frame still fire in order.
- Pattern/transport edits from the UI take `e.mu` — they mutate state that the very next
  `Read` reads, so an edit lands within one buffer (≈ a few ms), in time.

### 2.4 Optional helper-goroutine variant (only if you ever need it)

If decoding/allocating a voice in the callback ever becomes too heavy (it shouldn't for
one-shots), use Wilson's look-ahead split: a goroutine with a `time.Ticker(25ms)` *plans*
upcoming events into a lock-free queue tagged with absolute **frame** positions, and the
callback only *consumes* the queue by frame index.[^webdev] The clock is still the frame
counter; the goroutine never sets timing, it only pre-stages work ahead by ~100 ms.[^webdev]
**For v1, prefer the pure-callback design in §2.3 — simpler and tighter.**

### 2.5 Transport: play / stop / continue / song position

Transport is just state on the frame counter:
- **Play (from start):** `frame=0; loopBase=0; nextStep=0; nextStepAt=stepFrame(0); playing=true`.
- **Stop:** `playing=false` and (typically) let currently-sounding voices ring out (don't hard-
  cut, or you get clicks). Reset `frame`/`nextStep` to 0.
- **Continue / pause-resume:** keep `frame`, `nextStep`, `nextStepAt`; flip `playing=true`.
- **Song position / locate:** set `frame` (and recompute `nextStep`,`nextStepAt`,`loopBase`
  from it) to jump anywhere — same model MIDI "Song Position Pointer" expresses in
  16th-note units.[^midiclock] Because everything derives from one counter, scrubbing is a
  single assignment.

### 2.6 Tempo changes mid-playback

Because `samplesPerStep()` reads `e.bpm` **fresh every time `advanceStep` runs**, a tempo
change you write under `e.mu` automatically applies from the **next step onward** — exactly
the property Wilson highlights: the per-note advance "picks up the CURRENT tempo
value," enabling real-time tempo changes without pre-scheduling.[^webdev] Do **not** rescale
`frame` itself; only future step positions use the new tempo. (If you want already-scheduled
future steps to also move, recompute `nextStepAt` from the new `samplesPerStep` immediately —
but the simplest correct behavior is "new tempo from the next step.")

### 2.7 Loop boundaries (seamless wrap)

Seamlessness comes from **never resetting the frame counter at the loop point** — only the
*step index* wraps, while a `loopBase` advances by one bar's worth of frames
(`barFrames = stepsPerBar * samplesPerStep`). The last step of the loop and step 0 of the next
loop are scheduled with the identical math, so there is no gap, double-hit, or rounding seam.
Critically, **voices started near the end of the loop keep ringing across the wrap** (they're
in `e.voices`, independent of step state) — a kick on step 15 with a long tail doesn't get cut
when the loop returns to step 0. This is the audio-thread analogue of computing the wrap with
the same beat math on both sides, as Link/loop guidance recommends.[^link]

### 2.8 Live pad hits vs. sequenced steps

Both produce **the same kind of `Voice`** mixed in the same callback — the only difference is
*when* the voice is created:

| | Sequenced step | Live pad hit |
|---|---|---|
| Trigger | `fireStep()` when `frame` crosses `nextStepAt` | UI/MIDI thread → enqueue "hit pad N" |
| Timing | sample-accurate, on the grid (+swing) | **immediate**: started at the start of the *next* `Read` |
| Latency | none (grid-locked) | one buffer of output latency (so keep buffer small)[^otodoc] |

Implementation for a live hit: the UI thread does **not** call into the audio path directly.
It pushes a message onto a small lock-free/channel queue (or sets a flag under `e.mu`); at the
top of the next `Read`, the engine drains that queue and `append`s the new `Voice`. The hit
then sounds at frame 0 of that buffer — as immediate as the buffer size allows, and never on
the wall clock.[^kvr][^cp3] (Optionally, if you want pad hits *recorded* into the pattern to
quantize, snap the hit's record position to the nearest step frame while still **sounding it
immediately** — sound now, quantize the stored event.)

This unifies everything: there is exactly one timeline (the frame counter), one voice list,
one mix loop. No second clock can disagree with the first.

---

## 3. External sync — what it'd take **[LATER]**

Not needed for v1. Documented so the v1 design doesn't preclude it. **Good news: the §2 design
is already "sync-ready" because all timing derives from one place.**

### 3.1 MIDI Beat Clock [LATER]
The MIDI clock standard is **24 PPQN** — 24 clock messages per quarter note.[^midiclock][^ppqn]
- **As a follower:** measure the incoming clock interval to derive BPM, and align step 0 to the
  MIDI **Start**/**Continue**/**Stop** real-time messages; **Song Position Pointer** locates in
  16th-note units.[^midiclock] You'd feed the derived tempo/phase into the same `samplesPerStep`
  / `nextStepAt` machinery.
- **As a leader:** emit 24 clocks per quarter, i.e. one clock every `samplesPerBeat/24` frames —
  trivially scheduled by the same frame counter.
- Caveat: MIDI clock jitters (it rides on a serial/USB transport); typically you smooth the
  derived BPM rather than re-jumping the phase every message.

### 3.2 Ableton Link [LATER]
Link syncs **tempo, beat, and phase** across apps on a machine/LAN; first joiner sets tempo,
anyone can change it, all follow; a **quantum** (in beats) aligns bar/loop boundaries across
peers.[^abletonmanual][^link] Integration is a **capture-commit** model used **from the audio
thread**:[^link]
1. In the callback, `captureAudioSessionState()` to get a consistent snapshot.[^link]
2. Use `beatAtTime()` / `timeAtBeat()` / `phaseAtTime()` to map between Link's beat timeline and
   your sample timeline.[^link]
3. Optionally modify tempo/start-stop and `commit`.[^link]
Link insists you only touch its state from the audio thread and use the same session state for
the whole buffer[^link] — which fits §2 exactly: you'd convert Link's `beatAtTime` for this
buffer's start frame into a step position instead of advancing `nextStep` from your own tempo.
Effort: add the Link library (C++; needs a cgo binding for Go) + replace the internal tempo
source with Link's mapping. Moderate; clearly post-v1.

---

## 4. v1-minimal vs. later

**v1 (build this now — all pure Go, deterministic, runs on oto):**
1. One `Context` + one master `Player` fed by the `Engine.Read` mixer (§2.1).[^otodoc]
2. Global `int64` frame counter incremented once per output frame = the only clock (§2.3).
3. `samplesPerStep = sr*60/(BPM*4)` for a 16-step grid; positions from `round(i*samplesPerStep)`
   (§1.2).[^sweetcare]
4. Steps fired when `frame` crosses `nextStepAt`; voices = decoded one-shots, mixed in-callback
   (§2.3).
5. Swing as a schedule-time offset on off-beats (§1.3).[^webdev-swing]
6. Live pad hits enqueued from UI → started at next buffer, mixed in the same voice list (§2.8).
7. Transport play/stop/continue + song-position locate as counter state (§2.5).
8. Mid-playback **tempo** and **swing** changes via fresh recompute on each step (§2.6).
9. Seamless loop via wrapping the step index + `loopBase`, voices ring across the wrap (§2.7).
10. Store the model in ticks at PPQ 96/480 even though the grid is 16 steps (future-proofing, §1.2).

**Later (don't build yet; design leaves room):**
- Look-ahead helper goroutine **only if** in-callback voice allocation proves too heavy (§2.4).[^webdev]
- Per-step micro-timing / per-track swing / groove templates (the PPQ tick model already supports it).
- **MIDI Beat Clock** follow/lead (24 PPQN) (§3.1).[^midiclock]
- **Ableton Link** tempo/beat/phase sync (capture-commit in the callback) (§3.2).[^link]
- Sample-accurate parameter automation (same frame-offset technique as events).[^loopy]

---

## 5. The one-paragraph rule to remember

Time is **sample-frames counted in the audio callback**, never wall-clock seconds on another
thread.[^webdev][^kvr][^cp3] The hardware consuming samples at a fixed rate is your only
reliable clock (`t = samples/sr`).[^sonoport][^webdev] Convert tempo with
`samplesPerStep = sr*60/(BPM*stepsPerBeat)`,[^sweetcare] fire a step when the global frame
counter crosses its position, apply swing as an offset at schedule time,[^webdev-swing] mix
live pad hits and sequenced steps through the same voice list, and let tempo/loop changes fall
out of recomputing the next step's frame position.[^webdev] Sample-accurate engines stay tight
at any buffer size; buffer-quantized ones drift and flam — be the former.[^loopy][^kvr]

---

## Sources

[^webdev]: Chris Wilson, "A tale of two clocks — scheduling web audio with precision," web.dev (orig. HTML5 Rocks). Why `setTimeout`/`Date.now()` jitter (tens of ms; ms-precision = ~44 samples @44.1k); the look-ahead scheduler (25 ms timer, 100 ms schedule-ahead, `while (nextNoteTime < currentTime + scheduleAheadTime)`); `secondsPerBeat = 60/tempo`, `nextNoteTime += 0.25*secondsPerBeat`; picks up current tempo each note. https://web.dev/articles/audio-scheduling
[^webdev-swing]: Same article/series — swing is applied by offsetting the off-beat note's scheduled time at schedule time (groove handled where notes are placed, not by changing the clock). https://web.dev/articles/audio-scheduling
[^sonoport]: "Understanding the Web Audio Clock," sonoport.github.io — `AudioContext.currentTime = samplesProcessed / sampleRate`; the audio clock is the reliable timebase. https://sonoport.github.io/
[^ircam]: IRCAM-ISMM Web Audio Tutorials, "Timing and Scheduling" — look-ahead scheduling and groove/swing at schedule time. https://ircam-ismm.github.io/webaudio-tutorials/scheduling/timing-and-scheduling.html
[^kvr]: KVR Audio DSP & Plugin Dev forum, "Sample accurate timing?" — events aligned to sample frames from the audio callback; the audio callback thread is the master timer; consistent at 64 vs 2048 buffer; only put what must be sample-accurate on the audio thread. https://www.kvraudio.com/forum/viewtopic.php?t=283119
[^cp3]: cp3.io, "Sample-accurate MIDI timing in AUv3 plugins" — do timing calculations in the realtime render block (count sample frames), not on a wall-clock timer. https://cp3.io/posts/sample-accurate-midi-timing/
[^loopy]: Loopy Pro forum, "Let's talk about MIDI sequencer timing" — sample-offset (place events at the exact sample within the buffer) vs buffer-quantized; large buffers make bad timing worse; sample-accurate apps stay consistent 64–2048; mismatches cause audible flams. https://forum.loopypro.com/discussion/46251/let-s-talk-about-midi-sequencer-timing
[^otodoc]: ebitengine/oto v3 package docs — `NewContext(SampleRate, ChannelCount, Format, BufferSize)`; `NewPlayer(io.Reader)`; pull model (reader read on demand as internal buffer drains); one reader per player; `BufferSize` latency-vs-underrun tradeoff; formats incl. `FormatFloat32LE`. https://pkg.go.dev/github.com/ebitengine/oto/v3
[^otopkg]: ebitengine/oto README/GoDoc — data flows reader→internal buffer→device; when the buffer moves to the device is not guaranteed (small delay), so reader position ≠ playing position. https://github.com/ebitengine/oto
[^ppqn]: Wikipedia, "Pulses per quarter note" — PPQN/PPQ/TPQN definition; values 24/96/120/480/960; 24 is the MIDI standard and the minimum for triplets + swing (counting alternate ticks); higher PPQ preserves human feel. https://en.wikipedia.org/wiki/Pulses_per_quarter_note
[^sweetwater]: Sweetwater inSync, "PPQN (Pulses Per Quarter Note)." https://www.sweetwater.com/insync/ppqn-pulses-per-quarter-note-parts-per-quarter-note/
[^midiclock]: Wikipedia, "MIDI beat clock" — 24 PPQN clock standard; Start/Stop/Continue real-time messages; Song Position Pointer in 16th-note units. https://en.wikipedia.org/wiki/MIDI_beat_clock
[^sweetcare]: Sweetwater SweetCare, "How do I calculate the number of samples per beat?" — `samplesPerBeat = (60/BPM)*sampleRate`; e.g. 90 BPM @44.1k = 29400 samples/quarter, 14700/eighth. https://www.sweetwater.com/sweetcare/articles/calculate-number-of-samples-per-beat-audio-recording-software/
[^maxmsp]: Cycling '74 Max/MSP forum, "tempo to samples conversion" — quarter note = (60/BPM)*sampleRate samples. https://cycling74.com/forums/tempo-to-samples-conversion
[^link]: Ableton Link developer documentation (ableton.github.io/link) — tempo/beat/phase; quantum aligns bar/loop boundaries; capture-commit (`captureAudioSessionState`, `beatAtTime`, `timeAtBeat`, `phaseAtTime`) from the audio thread; use one captured session state per buffer. https://ableton.github.io/link/
[^abletonmanual]: Ableton Reference Manual, "Synchronizing with Link, Tempo Follower, and MIDI" — first peer sets tempo, anyone changes it and others follow; join/leave non-disruptively. https://www.ableton.com/en/manual/synchronizing-with-link-tempo-follower-and-midi/
