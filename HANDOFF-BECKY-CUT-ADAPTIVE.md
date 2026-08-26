# HANDOFF — becky-cut: adaptive threshold (kill the per-mic dial-in forever)

**Status:** specified, NOT implemented. Written 2026-08-26 after a Rode Wireless GO II shoot cost
two days to accomplish what `becky-cut` already does on an iPhone.

**Read first:** `CLAUDE.md` invariant *"USE THE SPECIALIST'S TOOL FOR THE MECHANICS; WRITE ONLY THE
CALIBRATION"* and `SKILL.md` `# ROUGH CUT`. This work order IS that invariant applied: `auto-editor`
keeps the cutting, we replace ONLY the ~40 lines that pick the threshold.

---

## 0. The decision, and the evidence for it

Jordan asked: adaptive threshold, or a few fixed per-mic profiles?

**Answer: adaptive — but NOT Otsu. Use a valley-fraction rule.** Fixed profiles are rejected on
operational cost, and Otsu is rejected as needlessly fragile. All three were measured against the
16-clip Rode shoot (`X:\Videos\2026\08_august\23_hj-fbi-recap`).

### Why not fixed profiles

They would technically work — within one mic/room/shoot the optimal threshold varied 8 dB, and a
single averaged "Rode profile" would be off by at most **4.5 dB inside a 48 dB-wide valley**, which
is harmless. They are rejected because of what they cost to run:

- **Every new mic needs a human dial-in session.** That is precisely the two days being complained
  about. A profile list does not prevent the next occurrence, it schedules it.
- **Jordan has to pick the flag, and picking wrong fails SILENTLY** — a bad cut that looks like a
  working one. He is non-dev and the contract is one dumb call with no flags.
- **Room changes too, not just mic.** His `-27dB` is correct "only when I use an iPhone 13 in a
  specific room". Profiles multiply: mic x room.

Keep profiles as an **escape hatch**, never the primary mechanism (step 5).

### Why not Otsu (what `scripts/speechcut.py` currently does)

Otsu works, but measurement shows it is solving a much simpler problem than it appears to. Across
all 16 clips it consistently landed at the same relative position in each file's own dynamic range:

| statistic | value |
|---|---|
| fraction of the valley Otsu chose | **mean 0.523, median 0.526** |
| spread across 16 clips | 0.455 .. 0.570 |
| a flat `floor + 0.52 * valley` vs Otsu's actual pick | **mean 1.2 dB, worst 3.6 dB** |
| average valley width (speech - floor) | 48 dB |

A 1.2 dB mean difference inside a 48 dB valley is nothing. So Otsu buys no accuracy, and it carries
a real failure mode: **it assumes the histogram is bimodal.** On a clip that is ~100% speech or
~100% silence there is one mode, and Otsu confidently splits noise from noise (or speech from
speech). Two percentiles have no such assumption.

### The rule to implement

```
floor_db   = 5th  percentile of per-frame RMS dBFS      # room tone
speech_db  = 90th percentile of per-frame RMS dBFS      # programme level
valley     = speech_db - floor_db
threshold  = floor_db + 0.52 * valley                   # halfway up, in dB
```

Why this generalises where the alternatives do not — it adapts to **both** the level and the
**width** of the gap. Measured spread of each candidate constant across the 16 clips:

| candidate rule | spread of its constant | verdict |
|---|---|---|
| fixed dB per mic | 8.0 dB | needs a dial-in per mic |
| `floor + N` | 10.9 dB | worst — ignores how loud the voice is |
| `speech - N` | 4.7 dB | tightest here, but assumes valley width is constant across mics — it is NOT (a phone in a room has a ~20 dB valley, this Rode has 48 dB) |
| **`floor + 0.52 * valley`** | **5.6 dB equivalent** | **use this** — the only one that tracks level AND valley width |

`speech - N` looks marginally tighter in this table, but that table is **one mic**. It is rejected
because it hard-codes the valley width, which is exactly the thing that changed between the iPhone
and the Rode. Do not be fooled by the single-mic number.

---

## 1. Work order (ordered, checkboxed)

### [ ] 1. Replace the estimator in `becky-go/cmd/cut/level.go`

Current code derives the threshold from ffmpeg `volumedetect`'s `mean_volume` and clamps it:

```go
const (
    defaultThresholdDB = -28.0   // ceiling
    minThresholdDB     = -50.0   // <-- THE BUG
    defaultHeadroomDB  = 1.0
)
func detectThresholdDB(meanDB, headroomDB float64) float64 { ... }
```

**Measured failure on the Rode footage** — the clamp makes the correct answer unreachable at ANY
`--headroom`:

| file | mean_volume | becky-cut picks | actually needs | error |
|---|---|---|---|---|
| SNOW_20260823114143.mp4 | -38.7 | -37.7 | -58.25 | **+20.5 dB** |
| VTNZ3433.MP4 | -44.4 | -43.4 | -63.25 | **+19.9 dB** |
| LZTE3925.MP4 | -42.3 | -41.3 | -62.75 | **+21.5 dB** |

Changes:

- **DELETE `minThresholdDB` entirely.** It is a floor on a value whose whole job is to follow the
  recording down. Keep `defaultThresholdDB = -28.0` as the CEILING (it preserves today's behaviour
  on already-normal-level footage, which is why it exists).
- Replace `detectThresholdDB(meanDB, headroom)` with
  `detectThresholdDB(floorDB, speechDB, fraction float64) float64`:
  ```go
  t := floorDB + fraction*(speechDB-floorDB)
  if t > defaultThresholdDB { t = defaultThresholdDB }
  return t
  ```
- Keep `--headroom` working as an ADDITIVE nudge in dB on top (`t += headroomDB`), default 0.
  Jordan may want to bias a specific shoot without recompiling.
- Add `--valley-fraction` (default `0.52`) so the one magic number is reachable from the CLI.
- **Keep the function PURE and unit-test it** (`cut_test.go` already tests the old one — assert
  VALUES, not truthiness, per `STANDARDS-ENGINEERING.md`).

### [ ] 2. Measure the two percentiles, not `mean_volume`

`mean_volume` is a single RMS number over the whole file and cannot express a valley. Replace
`measureMeanVolumeDB` with a per-frame envelope + two percentiles.

- **Reuse the proven implementation** rather than writing a third one: `scripts/speechcut.py`'s
  `decode_mono()` + `frame_db()` (20 ms window / 10 ms hop) and the `noise_floor_db` /
  `speech_level_db` it already reports. Port those two functions into a small pyhelper
  (`becky-go/internal/pyhelpers/levels.py`) emitting one line of JSON:
  `{"floor_db":..., "speech_db":..., "valley_db":..., "duration":...}`, following the existing
  helper contract (`vad_silero.py` is the template — on failure emit `{"skipped":true,"reason":...}`
  and exit 0).
- Decode audio-only (`-vn -ac 1 -ar 16000 -f s16le`). Measured cost: **15 s for 145 minutes** of
  footage. Do not optimise this; it is already free.

### [ ] 3. Sanity-gate it so it fails LOUDLY, never silently

This is the load-bearing safety of going adaptive, and the reason adaptive is acceptable at all.

- **If `valley_db < 12.0`, DO NOT use the adaptive value.** A narrow valley means the file has no
  usable silence/speech separation (all-speech clip, heavy compression, loud continuous background).
  Fall back to `defaultThresholdDB` (-28) or an explicit `--profile`, and **say so on stderr and in
  the JSON output** (`"threshold_source": "fallback: valley 7.2dB < 12dB"`).
- **Always print and record the chosen number**: `threshold_db`, `floor_db`, `speech_db`,
  `valley_db`, `threshold_source`. becky-cut already emits a `threshold` note — extend it. An
  adaptive value nobody can see is an adaptive value nobody can debug.
- Degrade, never crash (existing invariant).

### [ ] 4. Use the VAD to measure the SPEECH level, not just to post-filter

Cheap, and it closes the biggest real-world hole: `speech_db` = 90th percentile assumes the loudest
material IS the speaker. On footage with music, a TV, or a second voice, it is not — and this is
exactly what the forensic material (`E:\TakingBack2007`) looks like.

becky-cut **already runs Silero VAD** (`models/silero_vad.onnx`, whole-file — see the streaming trap
below). Compute `speech_db` as the 90th percentile of frames **inside VAD-positive spans only**.
If the VAD is unavailable, fall back to the plain p90 and record that in `threshold_source`.

> **Trap, already paid for once:** sherpa-onnx's VAD is STREAMING and cannot latch onto speech that
> is already running at sample 0. Run it over the WHOLE file once and score by overlap. Never hand
> it a segment that starts mid-word — it returns 0% and deletes a real word.

### [ ] 5. Keep profiles as an escape hatch

- `--threshold "<n>dB"` already exists in `cmd/cut/main.go` and is passed to auto-editor verbatim.
  **Keep it working unchanged.**
- Add `--profile iphone|rode` as sugar that pins a known-good fixed value (`iphone` = -27dB, his
  decade-old default; `rode` = -61dB, the 16-clip mean measured here). Document both as
  *overrides for when the adaptive value is wrong*, never as the normal path.
- Adaptive stays the DEFAULT so a new mic costs zero human time.

### [ ] 6. Make a re-run reproducible

Adaptive means the number can move if inputs change, which is bad in an editing loop.

- Persist the chosen threshold per file in the run's sidecar JSON.
- On re-run, REUSE the persisted value unless `--recalibrate` is passed.

### [ ] 7. Point `roughcut.py` at becky-cut and delete the duplicate detector

This is the whole point of the exercise — see `SKILL.md` `# ROUGH CUT` -> "KNOWN BUG - NEXT JOB".

- `scripts/roughcut.py` should call `becky-cut` per clip and consume **auto-editor's INTEGER FRAME
  chunks** (becky-cut already parses the Premiere XML), instead of `scripts/speechcut.py`'s spans.
  This makes the one-frame timeline gaps structurally impossible.
- Keep becky-cut's existing Silero post-pass and the **two-signal drop rule** (VAD AND Parakeet must
  agree there is no word inside before a clip is deleted).
- `scripts/speechcut.py` then keeps only its measurement role (or is deleted once `levels.py` lands).
  **Do not keep two detectors.**
- **UNTESTED, must be MEASURED not assumed:** whether `speechcut.py`'s 2 ms edge refinement is still
  needed once on auto-editor's frame grid, or whether `--margin 0.04s,0.25s` alone reaches Jordan's
  <= 1-frame head slack. If it IS needed, it belongs as a post-step ON auto-editor's frames, never
  as a replacement for them.

---

## 2. VERIFY (run these; expected values included)

```bash
# a. the pure estimator, unit-tested with VALUES
cd becky-go && go test ./cmd/cut/... && go vet ./... && gofmt -l .

# b. the estimator reproduces the measured answer on the Rode shoot
#    expect floor -75..-93, speech -30..-42, threshold -53..-64 per clip
becky-cut "X:\Videos\2026\08_august\23_hj-fbi-recap\VTNZ3433.MP4" --dry-run --verbose
#    -> threshold must land near -63.25 dB (the value measured 2026-08-26), NOT -43

# c. it must NOT change his iPhone footage
#    normal-level footage is capped by defaultThresholdDB and behaves as before
becky-cut "<an iPhone 13 clip>" --dry-run --verbose

# d. the gate fires loudly on a degenerate file
#    a clip that is ~all speech must report a fallback, not a confident wrong number

# e. end to end, and the acceptance test
python scripts/roughcut.py --folder "X:\Videos\2026\08_august\23_hj-fbi-recap"
python scripts/verify_timeline.py "<...>\_roughcut\vegas_cut.json"
#    expect: 0 gaps/overlaps IN INTEGER FRAMES (fix that check too - it currently
#    compares seconds at 1e-6 and is blind to the one-frame gap bug)

# f. Jordan's own calibration targets, printed per clip by speechcut.py today
#    head slack p50 <= 1.0 frame, max <= 1.0 frame. Non-negotiable.

cd becky-go && build-all-tools.bat
```

## 3. DONE means

- [ ] `go build/vet/test ./...` green, `gofmt -l .` silent, `build-all-tools.bat` produces the exes
- [ ] `minThresholdDB` is gone; a regression test asserts a threshold below -50 dB is reachable
- [ ] The Rode shoot cuts correctly with **no flag passed** (this is the whole deliverable)
- [ ] An iPhone clip is unchanged vs today
- [ ] A degenerate clip reports a fallback with a reason, on stderr and in the JSON
- [ ] `roughcut.py` consumes auto-editor's integer frames; `verify_timeline.py` checks frames
- [ ] Head slack still p50/max <= 1 frame, verified on real footage, screenshotted in Vegas
- [ ] `SKILL.md`, `INDEX.md`, `STATE-OF-MASTER.md`, `HANDOFF-LOG.md` updated

## 4. Do NOT

- Do not re-introduce a fixed threshold constant as the default path.
- Do not write a third detector. The mechanics belong to auto-editor.
- Do not loosen the <= 1-frame head-slack calibration to make anything else easier.
- Do not cut on transcript cues. Parakeet cue ends run long (13.28 s stamped for a ~5 s sentence).
- Do not trust `verify_timeline.py`'s current gap check — it compares seconds with a 1e-6 tolerance
  and reported "0 gaps" on a timeline that had them.
