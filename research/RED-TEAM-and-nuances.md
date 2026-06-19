# RED TEAM: what's wrong with the drum-machine rebuild (cloud self-critique, 2026-06-19)

Jordan's instruction: "assume your own work is full of shit. critique everything, add the
subtle nuances which were almost certainly overlooked." This is that pass, written AFTER
re-reading the actual Phase-1 code (not from memory). Every item is grounded in a specific
file/line or a verified fact. Severity: **P0** = the thing is wrong/misleading and will bite;
**P1** = real musical/UX gap; **P2** = nuance to not relearn the hard way.

The meta-lesson, again: **green tests proved the code does what I thought to test, not what a
drum machine needs.** The `sampler` package has 100% passing tests and still can't make a hit
get louder when you hit it harder. Tests must assert MUSICAL behavior, not just struct plumbing.

---

## P0 — these are wrong or actively misleading

### P0-1. The four new packages are ORPHANS. The rebuild is not wired to anything.
Verified: `grep` shows nothing imports `internal/sampler`, `internal/sampledecode`,
`internal/kitimport`, or `internal/samplelib`; and `cmd/drummachine` / `internal/audioengine`
/ `internal/machinectl` (the shipped slop) reference NONE of them. So there are now TWO
disconnected worlds: the old sine-tone model (Pad with a single SamplePath + sine fallback,
wired to the GUI + a render-then-exec engine + the keyword "AI") and the new SFZ-aligned model
(unwired). My §6 handoff described this as "wire a Pad to reference a `sampler.Sound`" — that
DRAMATICALLY understates it. The honest scope for local: the existing engine, GUI, sequencer
bridge, and machinectl all speak the old model; adopting the new one is a migration, and a
decision must be made — retrofit the new model into the old GUI/engine, or rebuild the engine
around the new model (Phase 2) and re-point the GUI at it. Either way it is NOT a one-line field.
**Recommendation:** Phase 2 builds the oto engine directly on `sampler.Sound` + `sampledecode`,
and `internal/drummachine.Pad` gets `Sound *sampler.Sound` (sequencing stays in drummachine,
sound lives in sampler). Treat the old `internal/audioengine` machine_* sine path as throwaway.

### P0-2. Velocity does NOT affect volume. (internal/sampler `PickLayer`)
Verified by reading sampler.go: velocity is used ONLY to select a Layer (`PickLayer`), then the
chosen Variant plays at its fixed `Gain`. There is no `amp_veltrack` / `amp_velcurve` / any
velocity→amplitude mapping. **Consequence: for the common case of a pad with ONE velocity layer
(most user one-shot kits are NOT multisampled), a velocity-1 ghost note and a velocity-127 accent
are byte-identical in level.** Dynamics — the soul of a groove — are silently dead. The 31 tests
never caught it because none asserted "louder velocity → louder output." FIX (done in this pass,
see below): add `AmpVelTrack` (0..1) + a `VelGain(vel)` helper so the engine scales amplitude by
velocity; default it ON for drums so hits respond out of the box.

### P0-3. "Random" round-robin is a lie. (internal/sampler `SelectVariant`)
`SelectVariant` maps `RRMode == Random` to `((rrCounter % n)+n)%n` — i.e. it returns the SAME
sequential position as Sequential mode. So a Sound marked Random round-robin machine-guns through
its variants in fixed order. SFZ random RR (`lorand`/`hirand` bands) is not modeled at all (no
rand fields on Variant). This is a feature wearing a name it does not implement — the exact class
of dishonesty that started this whole correction. FIX (this pass): add an explicit
`SelectVariantRandom(layer, r float64)` that honestly picks by a caller-supplied random value, and
make the doc on the deterministic `SelectVariant` state plainly that it is sequential-only.

### P0-4. No declick / micro-fade on voice stop or choke. (whole audio path)
`ChokeMode` has `Fast`/`Normal` but they are just labels — there is no release-ramp field and no
engine to apply one. When a voice is cut (choke, polyphony steal, or a OneShot reaching the data
end mid-cycle) with no ~2–5 ms fade, the waveform jumps to zero = an audible click/pop. This is
THE signature artifact of an amateur sampler. Even `Fast` choke needs a tiny (~1–3 ms) ramp.
FIX (this pass): add a per-Sound `AmpEnv` with at least a release, and a `DeclickMs` floor; the
engine MUST apply a release ramp on every voice termination, including "fast" choke.

---

## P1 — real musical / UX gaps

### P1-1. No amplitude envelope at all. (internal/sampler `Sound`)
No A/H/D/S/R. Drums need at least Hold+Decay (a one-shot that can be shortened — "tighten the
kick"); sustained/melodic pads (the piano roll) need full ADSR with note-off. The model can't
express "shorten the snare" or a sustained synth note. FIX (this pass): add
`AmpEnv{Type, A,H,D,S,R}` with Oneshot/AHD/ADSR types.

### P1-2. No resampling — Pitch/Transpose/Tune are inert, and rate-mismatch detunes everything.
`sampledecode` returns samples at the FILE's rate. `sampler` stores `PitchKeycenter/Transpose/
Tune` but nothing applies them. The engine (unbuilt) must resample per-voice for (a) pitch and
(b) device-rate conversion (a 44.1 kHz kit on a 48 kHz device plays sharp and fast otherwise).
NUANCE: linear interpolation aliases audibly on pitch-up; use at least cubic/Hermite, ideally
windowed-sinc for big shifts. Also: `smpl`/`LoopStart/End` are in SOURCE frames — scale them when
resampling. Decode+resample up front, never in the audio callback.

### P1-3. samplelib has no persistent index — it re-walks the whole drive every launch.
Verified by design (in-memory `Index` only). `Scan` on `X:\Splice` / `X:\music-2\SAMPLES` (tens
to hundreds of thousands of files) on every app open is unusable. Maschine uses a database. FIX
(local): persist the index to disk (e.g. `~/.becky/samplelib.json` or a small SQLite/JSON), and
incremental-rescan by mtime. Also add `acid`/`smpl` chunk reading (via `sampledecode`) for real
loop/tempo/key signals instead of filename-only guessing (already noted as deferred).

### P1-4. Project portability: kits store ABSOLUTE sample paths.
`Variant.SamplePath` is whatever the kit pointed at. Move the project, change a drive letter, or
hand it to someone else and every sound goes Missing. FIX (local): store paths relative to a kit
root when possible + a relink/"locate missing samples" flow; record a content hash so a moved
sample can be re-found.

### P1-5. Stereo samples have no defined behavior with pad Pan.
`sampledecode` returns N interleaved channels; `Variant.Pan` is a single scalar. How a stereo
clap/loop combines with pad pan (and whether the engine sums to mono per voice or keeps stereo)
is undefined. Mono one-shots are fine; stereo content needs a decision (recommend: keep stereo
voices, apply Pan as a balance).

### P1-6. Two conflicting choke models. `internal/drummachine` already has its own choke
(last-trigger-wins by group); `sampler.Sound` has SFZ `ChokeGroup`/`OffBy`. When wired together
these must be reconciled to ONE semantics (recommend SFZ `group`/`off_by`, which is strictly more
expressive and is what imported kits use).

### P1-7. Sequencer step has no LENGTH/gate. The drummachine Step is on/velocity (one-shot
oriented). The piano roll needs note duration, and sustained sounds need a gate (note-off) to end
the AmpEnv. Without it, the piano roll can't represent a held note. FIX (local): add Length to the
note/step model when wiring the piano roll.

### P1-8. No real-time recording / overdub / count-in / metronome / quantize-on-input.
A groovebox is played in, not just clicked. Step-record vs live-record with input-quantize,
count-in, overdub, and a metronome are core workflow and are absent from the model + plan. FIX
(local): design the record path in Phase 2/3.

### P1-9. No bounce/export to audio. Jordan will want to export the beat to a WAV/stems. Not in
the plan. (MIDI export is covered via internal/music; audio export is not.)

---

## P2 — nuances to not relearn the hard way

- **P2-1. Go GC vs real-time audio.** An allocation-free oto callback is necessary but NOT
  sufficient: a STW GC pause triggered by OTHER goroutines (the GUI, the model) can still starve
  the audio thread and cause dropouts. Budget for it: pre-allocate everything on the audio path,
  keep per-buffer work bounded, consider `GOGC` tuning / `debug.SetGCPercent`, and accept that
  hard real-time guarantees are not Go's strength (the malgo/C fallback exists if dropouts persist).
- **P2-2. 8-bit and unsigned PCM unsupported.** `sampledecode` decodes 16/24/32-bit only; 8-bit
  WAV is UNSIGNED (128 = zero) and would currently error. Lo-fi/vintage kits use it. Add it or
  document the limit (currently undocumented).
- **P2-3. EXTENSIBLE `wValidBitsPerSample` ignored.** Decoding by container size is correct for
  left-justified 24-in-32, wrong for right-justified. Rare, but document the assumption.
- **P2-4. smpl `MIDIPitchFraction` (fine tune) ignored**; acid byte offsets came from a KVR/
  libsndfile reconstruction — verify against a real Acidized file before trusting tempo exactly.
- **P2-5. Sample slicing to pads** (chop a loop across 16 pads) is a Maschine core feature and is
  absent from Phase 1. becky already has onset detection in `internal/dsp` (ported from dawbase) —
  wire it. Also time-stretch / loop tempo-sync is unresearched.
- **P2-6. RR counter ownership.** `SelectVariant` returns the next counter but nothing owns per-
  Sound playback state. The engine needs a `map[padIndex]int` (or per-voice-allocator state),
  reset on stop; design it so it stays deterministic for offline render but live-random for play.
- **P2-7. "Full context awareness" is a curated snapshot, not literal full state.** The agent-
  control research is right that you send the selected slice + a summary, never the whole project.
  My chat messages to Jordan overstated "full context awareness of the entire drum machine" — be
  honest that it's a smart snapshot (which is better, not worse, but it is not literally everything).
- **P2-8. Velocity layer gaps.** `PickLayer` nearest-matches so a hit is never silent (good), but
  kitimport should still WARN when imported layers leave a velocity gap, so a kit author can fix it.
- **P2-9. SFZ `default_path`/`<control>` and `#include`/`#define`** are deferred in kitimport but
  appear in many real-world SFZ libraries; local will hit them on Jordan's actual kits — implement
  before claiming broad SFZ support.

---

## What I changed in THIS pass (cloud-verifiable, tested)
Closed the most musically load-bearing model gaps so they aren't rediscovered, with tests that
assert MUSICAL behavior (not just plumbing):
- `sampler.AmpEnv{Type:Oneshot/AHD/ADSR, A,H,D,S,R}` on `Sound` (+ normalization, JSON).
- `Sound.AmpVelTrack` + `Sound.VelGain(vel)` so velocity scales amplitude (P0-2) — test asserts
  127 is louder than 1.
- `Variant.Reverse`, `Sound.Polyphony` (engine enforces oldest-steal).
- `Sound.DeclickMs` (release floor for choke/steal, P0-4).
- Honest random RR: `SelectVariantRandom(layer, r)` + `Variant.RandLo/RandHi`; `SelectVariant`'s
  doc now states plainly it is sequential-only (P0-3 no longer a hidden lie).
These are model + selection logic only (still pure-Go, deterministic, no engine). The engine that
APPLIES envelope/declick/resampling/velocity-gain is Phase 2 (local) — flagged honestly, not stubbed.
