# SPEC-BECKY-HUM.md — hum/sing → key + tempo + melody MIDI with key-aware suggestions

> **STATUS: design only — NOT built (2026-06-15).** The producer's-default workflow,
> ported to becky: **sing or hum an idea → becky detects the key + tempo → transcribes
> the melody to MIDI → tells you, per note, where you went off-key and what you probably
> meant.** This is becky's offline answer to Antares **Auto-Key** (key/tempo detection
> that arms Auto-Tune) — except instead of *silently correcting* pitch, becky **surfaces
> a key-aware suggestion with a confidence and lets the producer accept or reject it.**
> "Corroborate, then conclude" applied to pitch. Awaits Jordan's go/no-go (one online-vs-
> offline decision at the end; the floor is fully offline + deterministic).

---

## 1. Intent (read carefully)

Jordan's Cubase default (15+ yrs): he **verbally sings or hums a melodic idea**, runs
**Antares Auto-Key** to detect the song's key and tempo, and Auto-Key arms every Auto-Tune
instance to that key. becky should do the front half of that natively — and then go one
step further that Auto-Key/Auto-Tune deliberately do not:

> Jordan is, in his words, "sometimes bad or off-key." Auto-Tune's answer is to *snap the
> pitch to the scale and move on*. becky's answer is to **detect the intended note, flag the
> ones that are ambiguous, and suggest** — never silently overwrite. The producer stays in
> the loop. Off-key singing becomes a *labelled* set of decisions, not a hidden correction.

So `becky-hum` is: **WAV in → `{key, tempo, melody MIDI + per-note confidence + per-note
suggestion}` out.** The melody MIDI feeds straight into `becky-compose` (`--melody hum.mid
--key <detected> --bpm <detected>`) to build a full arrangement around the hummed idea. In
`becky-canvas` the same path is a button: **record → transcribe → drop an editable MIDI stem.**

This is a **new capability = a new tool** (the load-bearing single-tool principle, CLAUDE.md
§1). `becky-hum` does ONE thing — turn a voice into a key/tempo/melody decision sheet — and
hands JSON to the next tool. It does not arrange, mix, or autotune.

---

## 2. Where it sits in becky (grounding — the seeds already exist)

| Need | becky already has it |
|------|----------------------|
| 16 kHz mono WAV extraction | ffmpeg path used by `becky-transcribe` / VAD (`-ar 16000 -ac 1`). |
| Thin Python ML helper pattern | `internal/pyhelpers/` (Parakeet ASR, Silero VAD, sherpa-onnx) — model call isolated behind a one-line-JSON stdout contract; degrade-to-`{"skipped":true,...}` on failure. |
| Local ONNX runtime in-process | sherpa-onnx already vendored; basic-pitch ships an ONNX model that fits the same runtime. |
| Key/scale + MIDI math, deterministic | `internal/music/theory.go` (`ParseKey`, scale-interval sets, `ScaleMidi`, `Triad`, Roman degree) and `smf.go` (dependency-free Standard MIDI File type-1 writer). **We reuse these directly** — no new MIDI writer, no new key parser. |
| Consumer of `{key,bpm,melody.mid}` | `becky-compose` (`--genre --key --bpm --seed`, SPEC-BECKY-COMPOSE.md). `becky-hum` produces exactly the three inputs compose wants. |
| Audio recording surface | `becky-canvas` (SPEC-BECKY-CANVAS.md): native GUI, miniaudio I/O, embedded llama.cpp — the place a hum gets recorded and the MIDI stem gets dropped. |

`becky-hum` is the missing connective tissue between **a voice** and **`becky-compose`**.

---

## 3. The pipeline (five deterministic stages + one model stage)

```
 WAV/mic ─► [1] ingest ─► [2] KEY (Krumhansl-Schmuckler) ─┐
                       └─► [3] TEMPO (onset + autocorr)   ├─► [5] key-aware
                       └─► [4] PITCH→notes (basic-pitch /  │      suggestions
                                pYIN, segmented)           ┘   (snap+confidence)
                                                            └─► [6] emit JSON + melody.mid
```

Stages 1, 2, 5, 6 are **pure-Go, deterministic** (the offline floor). Stages 3 and 4 have a
deterministic Go floor *and* an optional model helper; stage 4's model path (basic-pitch) is
the one ONNX call, isolated in a pyhelper exactly like Parakeet.

### Stage 1 — Audio input / recording

- **CLI:** `becky-hum analyze --wav hum.wav` takes any audio file; becky shells ffmpeg to
  normalize to **16 kHz mono PCM WAV** (the same `-ar 16000 -ac 1` becky-transcribe uses).
  Missing ffmpeg → typed degrade error + exit, never a panic (CLAUDE.md §2 invariant).
- **Canvas:** `becky-canvas` records the hum via **miniaudio** (already its audio I/O layer)
  to a temp 16 kHz mono WAV, then calls the same `becky-hum` code path. Record button →
  transcribe → MIDI stem appears in the piano roll.
- **Headroom note:** humming is quiet and breathy; ingest applies a deterministic peak-
  normalize (fixed target dBFS, no compression) before analysis so a soft take and a loud
  take give the same pitch decisions. Normalization gain is logged in the output.

### Stage 2 — KEY detection (the Auto-Key analog, offline floor = no model)

**Algorithm: Krumhansl-Schmuckler key-finding** — deterministic, no ML, citable, and the
field-standard since 1990. Build a 12-bin **pitch-class profile (PCP)** from the detected
pitches (stage 4), weighting each pitch class by **note duration** (a held note counts more
than a passing one). Correlate that PCP against all 24 **key profiles** (12 major + 12 minor),
each profile being one of the two K-S template vectors rotated to each tonic. The key whose
profile has the highest Pearson correlation wins; report the **runner-up and the correlation
gap** as the confidence signal.

K-S template vectors (the empirically-derived tonal-hierarchy ratings we hard-code, tonic
first):

```
major: 6.35 2.23 3.48 2.33 4.38 4.09 2.52 5.19 2.39 3.66 2.29 2.88
minor: 6.33 2.68 3.52 5.38 2.60 3.53 2.54 4.75 3.98 2.69 3.34 3.17
```

Output is a key string in the exact form `becky-compose --key` already parses (`F#m`, `Am`,
`C`, …) via `theory.go ParseKey`. **Corroborate-then-conclude:** a clear correlation winner
with a wide gap over the runner-up → state the key. A narrow gap (e.g. relative major/minor
ambiguity, which K-S is known to confuse) → report **both candidates** and mark key
`confidence: "ambiguous"` rather than guessing.

- **Determinism caveat:** fully deterministic (a histogram + 24 fixed correlations). Same
  WAV → same key, always. No seed needed.
- **Optional ML comparison (logged, opt-in):** a learned key classifier (Essentia's
  `KeyExtractor`, or a small librosa/CNN model) can be run as a *second signal*. Per the
  corroborate rule, an ML key that **agrees** with K-S raises confidence; a disagreement is
  reported as a conflict, not silently preferred. The ML path is opt-in and logged; the K-S
  floor is the default and never needs the network or a model.

### Stage 3 — TEMPO / beat detection

**Algorithm: onset-strength envelope → autocorrelation / tempogram.** Compute an onset-
strength envelope, take its windowed autocorrelation (the **tempogram** is exactly this), and
pick the dominant periodicity as the BPM; this is the standard librosa/aubio/essentia pipeline.
Output a single integer BPM for `becky-compose --bpm`, plus the beat grid for quantization
(stage 4).

- **Offline floor (pure-Go):** a self-contained onset-envelope + autocorrelation BPM
  estimator in Go (no model) — deterministic, our default.
- **Optional helper:** librosa/aubio/essentia in the pyhelper for a stronger estimate when
  installed.
- **Determinism caveat (state it plainly):** beat trackers that use **dynamic programming with
  internal randomness or platform-dependent FFT** are *not* bit-reproducible across machines.
  becky pins this: fixed FFT size/window/hop as constants, integer-rounded BPM, and a fixed
  **octave-resolution rule** for the classic half/double-tempo ambiguity (prefer the BPM
  inside `becky-compose`'s genre tempo window when a target genre is supplied; otherwise the
  band nearest 120). Same WAV + same constants → same BPM. Any model-backed estimate is
  reported with its own confidence and never silently overrides the deterministic floor.

### Stage 4 — Monophonic pitch → MIDI notes (hum/sing transcription)

The core. Three candidate engines, compared:

| Engine | Type | Determinism | Fit for becky |
|--------|------|-------------|----------------|
| **pYIN** (probabilistic YIN + Viterbi HMM, note-segmentation included) | algorithmic | **Deterministic** (fixed thresholds, Viterbi is argmax) | Strong offline floor; the YIN/pYIN note-segmentation is widely used for exactly this (voice→notes). |
| **CREPE** (6-layer CNN on raw waveform) | model | Deterministic *inference* (fixed weights) but heavier, F0-only (needs separate note segmentation) | Excellent F0 on noisy voice, but no built-in note segmentation and a bigger model — overkill for monophonic hum. |
| **Spotify basic-pitch** (CQT + harmonic stacking → small CNN, ONNX) | model | Deterministic inference; ships **note_creation** (onset/frame thresholds, min-note-length) | **Best becky fit:** small ONNX model (drops into the existing sherpa-onnx/ONNX runtime + pyhelper pattern), instrument-agnostic, handles human-voice overtones via harmonic stacking, and *outputs notes with pitch-bend already* — not just an F0 curve. |

**Recommendation: basic-pitch as the primary model path; pYIN as the pure-algorithmic
offline floor.** basic-pitch is the headline because it natively emits **discrete notes**
(onset, duration, MIDI pitch, pitch-bend) — the thing we actually want — and it is small
enough to live beside Parakeet. pYIN is the no-model fallback so `becky-hum` still works on a
machine with no ONNX model present (degrade-never-crash): we run YIN F0 → Viterbi smoothing →
our own segmentation.

**Segmenting the pitch contour into discrete MIDI notes** (whichever engine; this logic is
ours and deterministic):
1. **F0 → semitone** per frame (`12*log2(f/440)+69`), confidence-gated (drop frames below a
   fixed voicing-probability threshold — unvoiced/breath frames are not notes).
2. **Onset detection:** a new note starts on a confident pitch change beyond a fixed semitone
   threshold *or* a re-articulation onset (energy rise). basic-pitch gives onsets directly via
   its onset head + `onset_threshold`; for pYIN we derive them from the Viterbi state changes.
3. **Note formation:** group contiguous voiced frames into a note; **median-pitch** over the
   note's frames sets the MIDI number (median, not mean — robust to scoops/vibrato).
4. **Min-note-length filter:** drop notes shorter than a fixed frame count (basic-pitch's
   `minimum_note_length`) to kill blips and breath clicks.
5. **Rhythmic quantization:** snap onsets/durations to the stage-3 beat grid at a fixed
   resolution (1/16 default), so the melody lines up with `becky-compose`'s PPQ=480 grid.
   Quantization strength is a flag; `--quantize off` keeps the raw human timing.

Output: a list of `{onsetSec, durSec, midi, pitchHz, confidence}` notes, written to
`melody.mid` via the **existing `smf.go` writer** (no new MIDI code), and the same notes feed
stage 5.

### Stage 5 — Key-aware SUGGESTIONS (the headline — corroborate, then conclude)

This is the part Auto-Tune doesn't do. For each transcribed note, given the stage-2 key/scale,
becky produces a **decision**, not a correction:

For each note `n` with detected MIDI pitch `p`:
1. **Distance to nearest scale tone** `d = min cents-distance(p, scaleTones(key))`.
2. **Classify (deterministic thresholds):**
   - `d ≤ on_tone` (e.g. ≤ ~25 cents) → **in-key**, high confidence. No suggestion; keep.
   - `on_tone < d ≤ ambiguous` (roughly the quarter-tone zone, ~25–75 cents) → **ambiguous**:
     between two scale tones. Emit a **suggestion** = the nearest scale tone, and *also* the
     nearest **chord tone** of the chord implied by that bar (from the key's diatonic triad /
     the progression context), because a producer often means the chord tone, not just the
     nearest scale tone. Flag `needs_review: true`.
   - `d > ambiguous` → **out-of-key**: clearly off. Suggest the **most likely intended note**
     by combining (a) nearest scale tone, (b) nearest chord tone, and (c) **melodic
     continuity** (the note that best continues the local interval contour — a leap that
     resolves). Where ≥2 of those three agree → state the suggestion confidently
     (corroborate-then-conclude). Where they disagree → present the top candidates and let the
     producer choose; do **not** auto-pick.
3. **Confidence per note** is a single 0–1 number fusing: F0/onset model confidence (stage 4),
   distance-to-scale (stage 2), and agreement among the three suggestion signals. This is the
   exact "≥2 independent signals agreeing → conclude; lone weak signal → flag" rule from
   CLAUDE.md §2, applied to pitch.

Every suggestion is **explainable**: the JSON carries *why* (`"reason":"nearest chord tone of
implied Bm; resolves the leap from A"`), so the producer (or a downstream agent) sees the
reasoning, never a black-box snap. Accept/reject is a per-note boolean the canvas UI toggles;
`--apply-suggestions` writes an alternate `melody.corrected.mid` while always keeping the raw
`melody.mid`. **becky never silently autotunes.**

### Stage 6 — Output

One JSON object to stdout (the becky house contract — JSON in/out, exit code), plus the MIDI
file(s) on disk. See §4.

---

## 4. CLI + output contract

```
becky-hum analyze --wav hum.wav [--out dir] [--key-hint F#m] [--genre crunkcore]
                  [--engine basic-pitch|pyin] [--quantize 1/16|off]
                  [--apply-suggestions] [--ml-key] [--device auto|cpu|cuda]
becky-hum record  --out dir [...same analysis flags...]      # canvas/CLI mic capture
```

`--genre` (optional) lets stage 3 resolve tempo-octave ambiguity inside that genre's window
and stage 5 pull chord context from that profile's progressions. `--key-hint` lets a producer
who already knows the key skip stage 2's ambiguity (still reported for cross-check).

**stdout JSON:**

```json
{
  "tool": "becky-hum", "schemaVersion": 1,
  "input": {"wav": "hum.wav", "durationSec": 7.4, "normalizeGainDb": 4.2},
  "key":   {"root": "F#", "scale": "minor", "compose": "F#m",
            "confidence": 0.82, "method": "krumhansl-schmuckler",
            "runnerUp": "A major", "corrGap": 0.11,
            "mlKey": "F#m", "agreement": true},
  "tempo": {"bpm": 150, "confidence": 0.7, "method": "onset-autocorrelation",
            "alt": [75, 300], "resolvedBy": "genre-window"},
  "notes": [
    {"i":0,"onsetSec":0.00,"durSec":0.48,"midi":66,"pitchHz":370.1,
     "confidence":0.94,"inKey":true,"suggestion":null,"needsReview":false},
    {"i":1,"onsetSec":0.50,"durSec":0.50,"midi":69,"pitchHz":444.9,
     "confidence":0.55,"inKey":false,"distanceCents":61,
     "suggestion":{"midi":68,"name":"G#","reason":"nearest scale tone of F#m; chord tone of F#m bar","alts":[71]},
     "needsReview":true}
  ],
  "files": {"melody": "dir/melody.mid", "corrected": null, "project": "dir/hum.json"},
  "compose": "becky-compose --genre crunkcore --key F#m --bpm 150 --melody dir/melody.mid",
  "degraded": false, "engine": "basic-pitch", "deterministic": true
}
```

The `compose` field is a **ready-to-run command** that arranges a full track around the hum
in the detected key/tempo — closing the loop from voice to song. On any model/ffmpeg failure:
`{"degraded":true,"reason":"...","key":...,"tempo":...,"notes":[...]}` with whatever the
offline floor produced (pYIN + K-S + Go tempo) — a partial result, never a crash.

---

## 5. Integration with `becky-compose` and `becky-canvas`

- **becky-compose:** add a `--melody <file.mid>` input. compose currently *generates* a melody
  track from a seeded stepwise walk; with `--melody` it **imports the hummed notes as the
  melody track** (snapping to the resolved key/scale using the same `theory.go` scale tones),
  and generates drums/bass/chords/lead/etc. *around* it. The hum becomes the song's topline;
  the genre profile supplies everything else. This is a small, additive change to compose
  (one new flag + an import path that lands notes on the melody track), not a rewrite.
- **becky-canvas:** the record→transcribe→stem loop. miniaudio captures the hum, `becky-hum`
  returns the JSON, the piano roll renders the notes with **off-key notes coloured** and the
  suggestion shown as a ghost note the producer clicks to accept. "Need a topline? Hum it."
  The corrected/raw toggle is a per-note switch; nothing is ever applied without a click.

---

## 6. Cloud-vs-local build split + helper-stub contract

Per CLAUDE.md §4, work splits at the **model boundary.**

**Cloud / web agent (no GPU, no models):**
- `cmd/hum/main.go` — the CLI (flags, ffmpeg shell-out to 16 kHz mono, JSON envelope, exit
  codes, degrade handling). Mirrors `cmd/transcribe`.
- `internal/hum/` deterministic Go:
  - `keyfind.go` — Krumhansl-Schmuckler: PCP builder + the two template vectors + 24
    correlations + ambiguity gap. **Pure Go, fully unit-testable on cloud** (golden PCPs →
    known keys; relative-major/minor ambiguity case).
  - `tempo.go` — onset-envelope + autocorrelation BPM floor + octave-resolution rule.
  - `segment.go` — pitch-contour → notes (semitone conversion, onset/median/min-length,
    quantization to a beat grid). Operates on a frame array, so it is testable with a
    **synthetic F0 contour fixture** with no audio.
  - `suggest.go` — the key-aware suggestion/confidence engine (distance-to-scale, chord-tone
    via `theory.go Triad`, contour continuity, the ≥2-signal fusion). **Pure Go, the most
    important thing to unit-test** (off-key fixture → expected suggestion + reason).
  - melody output reuses `internal/music/smf.go` (no new MIDI writer).
- **Unit tests** for all of the above (the deterministic core is 100% cloud-testable with
  synthetic contours — no WAV, no model needed).
- Push to a `claude/*` branch, open a **draft PR**, update CLAUDE.md §6.

**Local agent (Jordan's Win10 PC — real models + GPU + ffmpeg):**
- Wire the **pitch pyhelper** (below) to the real basic-pitch ONNX model (and optional CREPE/
  pYIN/librosa) and run it on real hums.
- Optional: wire the ML-key (`--ml-key`) and librosa/aubio tempo helpers.
- `build-all-tools.bat`, test against real vocal takes, tune the fixed thresholds
  (voicing-probability, onset, min-note-length, on-tone/ambiguous cents) on real off-key
  singing — accuracy tuning is a local job (CLAUDE.md §4).

**Helper-stub contract** — `internal/pyhelpers/pitch_basicpitch.py` (documented stub the cloud
agent writes; local agent plugs in the model), same shape as `transcribe_parakeet.py`:

```
stdin/args: --wav <16k-mono.wav> [--engine basic-pitch|pyin|crepe] [--device auto|cpu|cuda]
stdout (one line JSON):
  {"engine","version",
   "frames":[{"t":sec,"f0":hz,"voiced":prob}],     # F0 contour (for pYIN floor + our segmenter)
   "notes":[{"onset":sec,"dur":sec,"midi":int,"bend":cents,"confidence":prob}],  # basic-pitch native notes
   "device","fell_back"[,"fallback_reason"]}
on any failure: {"skipped":true,"reason":"..."} and exit 0   # Go side emits a clean degrade
```

The Go `segment.go`/`suggest.go`/`keyfind.go` consume `frames`/`notes` from this JSON and do
**all** the deterministic work; the helper's only job is the model inference (CQT + harmonic
stacking + CNN for basic-pitch, or YIN+Viterbi for the pYIN floor). The cloud agent ships the
stub with this exact contract + a fixture JSON so the Go side is fully testable before the model
ever runs.

---

## 7. Invariants honored (CLAUDE.md §2)

- **Offline + deterministic floor.** K-S key-finding, the Go tempo estimator, contour
  segmentation, and the suggestion engine are pure math with fixed constants/thresholds —
  same WAV → same `{key,tempo,notes,suggestions}`. The only model call (basic-pitch ONNX) is
  explicit, isolated in a pyhelper, and runs locally; its inference is deterministic (fixed
  weights). No network at runtime on the default path.
- **Corroborate, then conclude — don't hedge.** Key: clear correlation winner → state it;
  narrow gap → two candidates + `ambiguous`. Notes: ≥2 of {scale tone, chord tone, contour}
  agree → confident suggestion; disagreement → present candidates, let the human pick. A flood
  of "maybes" the producer must sort = tool failure, so weak lone signals become a single
  `needsReview` flag with a reason, not noise.
- **Suggest, never silently correct.** The raw `melody.mid` is always kept; corrections are an
  opt-in alternate file and per-note accept/reject. This is the explicit anti-Auto-Tune stance
  Jordan asked for ("intelligent suggestions, not blind correction").
- **Degrade, never crash.** Missing ffmpeg → typed error + exit. Missing basic-pitch model →
  fall back to the pYIN/Go floor (`degraded:true`, partial result). Silent take / no voiced
  frames → empty `notes` + `reason`, not a panic.
- **Paths.** Use `internal/pathx` for any path Base/Dir (Windows paths on Linux CI).

---

## 8. Open decisions for Jordan

1. **Primary engine:** ship **basic-pitch (ONNX) as default** with pYIN as the no-model floor
   (recommended), or keep pYIN-only as the floor and treat basic-pitch as opt-in until the
   model is vetted on his voice?
2. **ML key cross-check:** is the optional learned key classifier (`--ml-key`, Essentia/
   librosa) worth the extra local dependency, or is K-S alone enough (it's the field standard
   and fully offline)?
3. **Default quantization:** snap to 1/16 by default (tighter, DAW-ready) or keep raw human
   timing by default (more "feel", `--quantize` to tighten)?
4. **Suggestion aggressiveness:** the on-tone / ambiguous cents thresholds (~25 / ~75) set how
   eagerly becky flags notes — tune on his real off-key takes (a local-agent job), but he picks
   the default temperament (conservative = fewer flags vs. thorough = flag more borderline notes).
5. **Tempo octave default:** when no `--genre` is given and the BPM is ambiguous (75 vs 150 vs
   300), prefer the band nearest 120 (current proposal) or always report all octaves and let
   him choose?

No Go code written yet — this spec is for review before scaffolding, same as the other
proposed-tool specs in CLAUDE.md §6.

---

## 9. References (cited)

- Krumhansl & Schmuckler key-finding algorithm; the major/minor key-profile template vectors
  and the PCP-correlation method (field standard since 1990):
  - https://github.com/Corentin-Lcs/music-key-finder
  - https://online.ucpress.edu/mp/article-abstract/17/1/65/62051/What-s-Key-for-Key-The-Krumhansl-Schmuckler-Key
  - https://www.gmth.de/zeitschrift/artikel/513.aspx
- Spotify **basic-pitch** (CQT + harmonic stacking → small CNN, ONNX; native note output with
  pitch bend; note_creation thresholds):
  - https://github.com/spotify/basic-pitch
  - https://engineering.atspotify.com/2022/6/meet-basic-pitch
  - https://github.com/spotify/basic-pitch/blob/main/basic_pitch/note_creation.py
  - https://basicpitch.spotify.com/about
  - https://github.com/DamRsn/NeuralNote  (basic-pitch model run via ONNXRuntime + RTNeural in a plugin — proves the local in-process path)
- **pYIN** (probabilistic YIN + Viterbi HMM, with note segmentation) and **CREPE** (CNN F0):
  - https://www.semanticscholar.org/paper/PYIN:-A-fundamental-frequency-estimator-using-Mauch-Dixon/cf915bb50cabf54075afffe4a57babf02a83c2e3
  - https://www.semanticscholar.org/paper/Crepe:-A-Convolutional-Representation-for-Pitch-Kim-Salamon/86aeec4d48d949190b3a0c2bf32c101fc23f13a3
  - https://arxiv.org/pdf/2311.08884  (CREPE Notes — segmenting a pitch contour into discrete notes)
- **Tempo / beat** (onset-strength envelope → autocorrelation / tempogram; librosa/aubio/
  essentia):
  - https://deepwiki.com/librosa/librosa/5.2-beat-tracking-and-tempo-estimation
  - https://librosa.org/doc/main/generated/librosa.feature.tempogram.html
  - https://tempobeatdownbeat.github.io/tutorial/ch2_basics/baseline.html
