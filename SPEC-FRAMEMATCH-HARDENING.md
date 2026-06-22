# SPEC-FRAMEMATCH-HARDENING.md — make becky-framematch's room-matching reliable on talking-head footage

> **SPEC — NOT BUILT, AWAITING JORDAN'S GO/NO-GO.**
> Design only. No Go code has been written; nothing in `becky-go/` is changed yet.
> This hardens the EXISTING `becky-framematch` tool (`becky-go/cmd/framematch`) — it
> does not add a new binary. Authored 2026-06-22 against the real code in the repo
> (every file/symbol cited below was read, not assumed).
>
> **Scope guard.** A sibling spec, `SPEC-BECKY-LOCATION.md`, is being written that
> *consumes* framematch to produce a dwelling-level location report. This spec stays
> strictly at the PRIMITIVE level: make framematch's same-room call reliable. It does
> NOT decide "this is the suspect's house" — that is the consumer's job.

---

## 1. The problem, stated precisely

`becky-framematch` compares two sources frame-by-frame and surfaces candidate
same-location pairs by **whole-frame average perceptual hash (aHash)**. The hash is
computed by `osintexport.AHashFromImage` (`becky-go/internal/osintexport/phash.go:42`):
it downscales the **entire frame** to 8×8 grayscale, takes the mean, and sets bit *i*
to 1 where cell *i* ≥ mean (`AHashFromGray64`, `phash.go:21`). Pairs are then ranked by
Hamming distance over those 64 bits (`pairFrames`, `becky-go/cmd/framematch/pairing.go:25`;
`hamming64`, `pairing.go:121`), and a pair is a candidate when the distance is
≤ `--threshold` (default 10; `cmd/framematch/main.go:57`).

This is the **README "High" known issue**: on portrait talking-head footage (a centered
person filling most of the frame), an 8×8 whole-frame aHash is dominated by the
**subject's body silhouette and the global light/color tone**, not by the fixed
background fixtures that actually identify a room. Two concrete failure modes follow:

- **FALSE NEGATIVE (same room, missed).** Two clips shot in the *same* room miss each
  other because the person is standing/sitting differently, wearing a different shirt,
  or framed tighter — so the body-dominated 8×8 grid changes more than `--threshold`
  bits even though the ceiling, wall trim, window, and fixtures are identical.
- **FALSE POSITIVE (different rooms, wrongly paired).** Two clips shot in *different*
  rooms pair anyway because the overall brightness/warmth and the "centered dark blob
  on a lighter wall" composition produce near-identical 8×8 means — global tone, not
  shared structure.

Both are the *exact* error `FORENSIC-OUTPUT-PHILOSOPHY.md` forbids: "a low
perceptual-hash distance means the frames LOOK alike — it is NOT proof they are the same
place" is already the honesty note (`cmd/framematch/manifest.go:14`, `ManifestNote`), but
right now the single weak signal (whole-frame aHash) is *all there is*. Per the
**corroborate-then-conclude** invariant, a same-room CONCLUSION needs **≥2 independent
signals agreeing**; a lone weak signal must stay a candidate, never a conclusion.

**The fix (this spec):** stop hashing the part of the frame the subject occupies.
Hash a **region of interest (ROI)** — the ceiling / upper-wall band away from the
centered subject — and corroborate with a second independent signal (static-decor
keypoint matching, optional) before emitting a same/different-room CALL with a
confidence and the signals that produced it.

---

## 2. Design

### 2.1 ROI pre-crop before hashing (the core fix — pure Go, cloud-buildable)

The single highest-leverage change: **hash the upper band of the frame, not the whole
frame.** In talking-head footage the subject is centered and occupies the middle and
lower portion; the ceiling and upper wall (with its trim, vents, light fixtures, corner
lines, window tops) sit in the top band and are background-fixed.

- Add a new pure-Go helper `osintexport.AHashFromImageROI(img image.Image, roi ROI) uint64`
  (or a `framematch`-local `roiHash`) that restricts the 8×8 sampling grid to a
  sub-rectangle of `img.Bounds()` instead of the full bounds. The existing center-of-cell
  sampling math in `AHashFromImage` (`phash.go:50-60`) is reused verbatim, with `b` set
  to the ROI rectangle rather than `img.Bounds()`. This is a small, additive change; the
  existing `AHashFromImage` stays as the `--roi full` path so nothing regresses.
- `ROI` is a fractional rectangle (fractions of width/height, resolution-independent so
  portrait and landscape behave the same): `TopFrac, LeftFrac, WidthFrac, HeightFrac`.
  The default ROI is the **upper band**: `Top=0.0, Left=0.0, Width=1.0, Height=0.35`
  (the top 35% of the frame — see Open Decisions for the fraction).
- A second built-in ROI, `--roi corners`, hashes the two upper corners (where wall/ceiling
  lines and trim live) and away from a possibly tall subject; this is an alternative when
  a ceiling band is featureless (a plain white ceiling). Selectable; not the default.
- Crucially, the ROI hash is computed **in addition to** the existing whole-frame hash,
  not instead of it — the whole-frame hash stays in the sidecar for provenance and as a
  fallback signal. Both are stored per frame.

This is deterministic geometry over already-decoded pixels: no model, no ffmpeg crop,
no new dependency. It is fully unit-testable on synthetic fixture images (§5, §6).

### 2.2 Static-decor keypoint / feature matching (optional second signal)

ROI-aHash fixes most of the false negatives/positives, but aHash is still a coarse
64-bit global descriptor of one band. A stronger, viewpoint-tolerant second signal is
**feature matching on static decor**: detect repeatable keypoints (corners/blobs) in the
ROI of each frame, describe them, and count how many *geometrically-consistent* matches
exist between the two frames. Many matched fixed features that line up = strong same-room
evidence that survives a camera-angle change (which aHash does not).

- This is the signal that needs a CV capability Go's stdlib does not have. Two honest
  options, both flagged:
  - **Pure-Go light corner/blob descriptor** (e.g. a hand-rolled FAST-style corner
    detector + a simple binary descriptor over the ROI, all in `internal/dsp`-adjacent
    pure Go). Cheaper, no dep, cloud-buildable and testable, but weaker than ORB.
  - **Heavy CV dep (e.g. gocv → OpenCV ORB + BFMatcher + RANSAC homography).** Much
    stronger, but pulls a cgo/native OpenCV dependency that **cannot be built or tested
    on the cloud agent** and is heavy on Jordan's machine.
- **Decision deferred to Jordan** (§7). To keep the corroborate-then-conclude logic
  shippable NOW, the keypoint signal is behind an **interface** with a deterministic
  pure-Go default implementation; the heavy-dep implementation, if chosen, plugs into
  the same interface as the documented local-build step. If keypoints are absent
  (degrade), the call falls back to ROI-aHash alone and is **capped at "candidate"** —
  it can never reach a "documented" same-room conclusion on one signal (§2.3).

### 2.3 Combining signals — corroborate, then conclude

Per `FORENSIC-OUTPUT-PHILOSOPHY.md` and the CLAUDE.md invariant, each pair gets a
**room call** computed from the independent signals, not a single number:

Signals (each independent, each yields agree / disagree / unknown):
1. **ROI-aHash distance** — Hamming over the upper-band hash. `agree` when ≤ the ROI
   threshold, `disagree` when ≥ a clear-difference threshold, else `unknown`.
2. **Keypoint inliers** — count of geometrically-consistent static-decor matches.
   `agree` when ≥ `--min-inliers`, `disagree` when near zero with enough keypoints
   detected, else `unknown` (too few keypoints to judge).
3. **Whole-frame aHash distance** (the legacy signal) — kept as a WEAK tie-breaker /
   provenance only; it never alone produces a conclusion (it is the signal that caused
   the false positives).

The room call (mirrors the ≥2-signals rule):

| Signals agreeing (independent, strong) | Call            | Emitted as              |
|----------------------------------------|-----------------|-------------------------|
| ≥2 strong signals AGREE same-room      | `same_room`     | conclusion (DOCUMENTED) |
| exactly 1 strong signal agrees         | `candidate`     | candidate (review)      |
| signals DISAGREE / conflict            | `different_room`| conclusion (DOCUMENTED) when ≥2 disagree; else `candidate` |
| not enough signal                      | `unknown`       | candidate (review)      |

`confidence` is a `[0,1]` value derived deterministically from the agreeing-signal
margins (e.g. how far inside each threshold the agreeing signals sit), NOT a probability —
it is a readability score like the existing `Similarity` (`pairing.go:66`). A lone weak
signal NEVER reaches `same_room`; that is the whole point and is asserted in tests (§6).

The `WhatToLookFor` reviewer hint (`pairing.go:82`) is updated to name **which** signal
fired and the ROI used (e.g. "ceiling-band hash matches AND 14 static-decor features
line up — compare the vent and trim to confirm"), so the human eye is pointed at the
corroborating structure, consistent with the candidate-not-conclusion stance.

### 2.4 Accessibility

framematch's human surface is the HTML exhibit (`cmd/framematch/layout.go`) + the
side-by-side PNGs + the JSON manifest. Per `ACCESSIBILITY.md`, Jordan reads the screen
directly with custom high-contrast color and NO screen reader: keep the colored exhibit;
do not strip color or flatten it. The room call must be stated in **words** ("SAME ROOM
— 2 signals agree", "different room", "candidate — 1 signal") with a color accent, never
color/symbol alone. The stdout JSON stays the machine surface. No TUI, no table-only
meaning. (becky-framematch has no interactive TUI today; this spec adds none.)

---

## 3. CLI flags + JSON schema additions

### 3.1 New CLI flags (added to `cmd/framematch/main.go`)

```
--roi <mode>          region hashed for matching: band | corners | full   (default: band)
--roi-top <f>         ROI top edge as a fraction of height   (default 0.0)
--roi-height <f>      ROI height as a fraction of height      (default 0.35)
--roi-left <f>        ROI left edge as a fraction of width    (default 0.0)
--roi-width <f>       ROI width as a fraction of width        (default 1.0)
--keypoints           enable static-decor keypoint corroboration   (default: off until impl chosen)
--min-inliers <n>     keypoint inliers required for an "agree"      (default 12)
--roi-threshold <n>   max ROI-aHash Hamming for an "agree"          (default 8)
```

`--roi full` reproduces today's exact behavior (whole-frame aHash) for backward
compatibility and A/B comparison. All ROI fractions are clamped to `[0,1]` and validated
(`width/height > 0`); an invalid combination is a `beckyio.Fatalf` like the existing
`--threshold` / `--enhance-side` validation (`main.go:79-85`). The legacy `--threshold`
flag is retained and applies to the whole-frame hash (now a weak/provenance signal).

### 3.2 JSON schema additions (additive — no field renamed or removed)

On `Frame` (`cmd/framematch/manifest.go:35`):
```go
ROIHash   string `json:"roi_hash"`            // 16-char hex aHash of the ROI band
ROIUsed   string `json:"roi_used"`            // e.g. "band top=0.00 h=0.35" — exactly what was hashed
Keypoints int    `json:"keypoints,omitempty"` // static-decor keypoints detected in the ROI (0 if off)
```

On `Pair` (`manifest.go:47`):
```go
RoomCall     string  `json:"room_call"`      // "same_room" | "different_room" | "candidate" | "unknown"
Confidence   float64 `json:"confidence"`     // [0,1] readability score from agreeing-signal margins
ROIHamming   int     `json:"roi_hamming"`    // ROI-aHash distance (the primary signal)
KeypointInliers int  `json:"keypoint_inliers,omitempty"` // geometrically-consistent static-decor matches
SignalsUsed  []string `json:"signals_used"`  // e.g. ["roi_ahash","keypoints"] — which independent signals voted
```

The existing `Hamming`/`Similarity` fields stay (whole-frame, now the weak/provenance
signal); `WhatToLookFor` is rewritten to name the firing signals + ROI. On `Manifest`
(`manifest.go:76`) add `ROIMode string` and `ROISpec string` so the run records exactly
what region was hashed (re-runnability — the loop).

The `Sidecar` written per frame (`osintexport.Sidecar`, used at `frames.go:110`) gains
the ROI hash + ROI spec alongside the existing `PerceptualHash`, so provenance records
both the whole-frame and ROI hashes.

---

## 4. Non-negotiables (deterministic / offline / degrade-never-crash)

- **Deterministic.** ROI geometry is integer math over fixed fractions; the pure-Go
  keypoint detector uses a fixed algorithm with no randomness (any RANSAC-style step
  uses a fixed seed). Same input → byte-identical manifest, same as today.
- **Offline.** ROI-aHash and the pure-Go keypoint path use zero network and zero models.
  ffmpeg is used only as today (frame extraction + the honest exhibit composite); the
  matching math is in-process Go.
- **Degrade-never-crash.** A frame that fails to decode or whose ROI is empty is skipped
  with a logged note (mirrors the existing per-frame skip at `frames.go:99-108`), not a
  panic. If keypoints are disabled or unavailable, the room call falls back to ROI-aHash
  alone and is capped at `candidate` — never a false `same_room`. A featureless ROI
  (e.g. a blank white ceiling) yields `unknown`, not a guess.

---

## 5. Cloud-vs-local split

| Cloud agent (build + TEST now)                                  | Local agent (Jordan's PC)                          |
|----------------------------------------------------------------|----------------------------------------------------|
| `AHashFromImageROI` + the `ROI` fractional-rect geometry       | Run on real talking-head clips; tune ROI fraction  |
| The signal-combination / room-call / confidence logic          | If gocv chosen: build OpenCV, wire the ORB impl    |
| Pure-Go keypoint detector (if that option is chosen)           | `build-all-tools.bat` (auto-discovers; no edit)    |
| All unit tests on synthetic fixture images (§6)                | Accuracy validation on his footage; threshold tune |
| The whole `--roi full` backward-compat path                    |                                                    |

The **ROI pre-crop + scoring/threshold logic is 100% pure-Go and cloud-testable** on
synthetic fixture `image.Image`s — no GPU, no models, no ffmpeg. The keypoint second
signal: the pure-Go detector is cloud-buildable+testable; a gocv/OpenCV implementation is
**flagged as the one piece the cloud cannot build or run** (cgo + native OpenCV) and, if
chosen, is left as a documented local wiring step behind the keypoint interface.

---

## 6. Build plan (checkboxed) + unit tests that ASSERT VALUES

A regression test accompanies each failure mode. Tests assert VALUES (specific room
calls, specific hashes/distances), not truthiness, per `STANDARDS-ENGINEERING.md`.

### Phase 1 — ROI pre-crop (pure Go, cloud-complete)
- [ ] Add `ROI` fractional-rect type + `AHashFromImageROI(img, roi)` in
      `internal/osintexport/phash.go` (reuse the center-of-cell sampling; restrict `b`).
- [ ] Test `TestROIHashIgnoresCenteredSubject`: build two synthetic frames with an
      IDENTICAL top band (a fixed pattern = "ceiling/trim") and a DIFFERENT centered
      block ("the subject/body"). Assert `AHashFromImageROI(band)` Hamming **== 0**
      while whole-frame `AHashFromImage` Hamming **> roiThreshold** — proves the ROI hash
      keys on background, not body. (FALSE-NEGATIVE regression.)
- [ ] Test `TestROIHashDistinguishesDifferentDecor`: two frames with the SAME global
      tone (same mean brightness, same centered block) but a DIFFERENT top band.
      Assert ROI Hamming **> roiThreshold** (different room) even though whole-frame
      Hamming is small. (FALSE-POSITIVE regression.)
- [ ] Test `TestROIGeometryClampAndValidate`: out-of-range fractions clamp/reject as
      specified; `--roi full` reproduces the exact legacy `AHashFromImage` value
      (assert byte-equal hash).

### Phase 2 — wire ROI into framematch + new flags/schema
- [ ] Compute + store `ROIHash`/`ROIUsed` per frame in `frames.go` (both video +
      image-folder paths) and in the `Sidecar`.
- [ ] Add `--roi*` flags + validation in `main.go`; add `ROIMode`/`ROISpec` to `Manifest`.
- [ ] Re-point `pairFrames` (`pairing.go`) to rank on ROI-aHash as the primary signal,
      keeping whole-frame as the weak/provenance signal.
- [ ] Test `TestPairFramesUsesROIHash`: a pair that fails on whole-frame Hamming but
      passes on ROI Hamming is surfaced as a candidate; assert `pairs[0].ROIHamming`
      and that it appears (regression for the missed same-room case end-to-end).

### Phase 3 — keypoint second signal (pure-Go default behind an interface)
- [ ] Define a `DecorMatcher` interface (`Match(roiA, roiB image.Image) (inliers, keypoints int)`)
      with a deterministic pure-Go default; gocv impl left as a local build step (§7).
- [ ] Test `TestDecorMatcherCountsSharedFeatures`: synthetic ROI with N planted corners
      shared → inliers ≈ N; disjoint corners → inliers ≈ 0. Assert the counts.

### Phase 4 — corroborate-then-conclude room call
- [ ] Implement the signal-combination table (§2.3) → `RoomCall` + `Confidence` +
      `SignalsUsed` on each `Pair`; rewrite `whatToLookFor` to name the firing signals.
- [ ] Test `TestRoomCall_TwoSignalsAgree_SameRoom`: ROI agree + keypoints agree →
      `room_call == "same_room"`, `len(SignalsUsed) == 2`, confidence in expected range.
- [ ] Test `TestRoomCall_LoneWeakSignal_NeverConcludes`: ONLY whole-frame aHash agrees
      (ROI disagrees, keypoints off) → `room_call == "candidate"` (NOT `same_room`).
      (Directly enforces the ≥2-signal invariant — the headline regression.)
- [ ] Test `TestRoomCall_SignalsConflict_DifferentRoom`: ROI disagrees + keypoints
      disagree → `room_call == "different_room"`.
- [ ] Test `TestRoomCall_KeypointsOff_CappedAtCandidate`: keypoints disabled, ROI agrees
      alone → `room_call == "candidate"` (degrade path can't reach a conclusion).
- [ ] Test `TestRoomCall_FeaturelessROI_Unknown`: a blank/uniform ROI → `unknown`, no guess.

### Phase 5 — gates
- [ ] `go build ./... && go vet ./... && go test ./...` + `gofmt -l .` all green (cloud
      passes gates 1–4; `build-all-tools.bat` is the local completion step — it
      auto-discovers, no edit needed since `cmd/framematch` already exists).

---

## 7. Open Decisions for Jordan

1. **Default ROI fraction.** Proposed default is the **top 35% band**
   (`--roi-height 0.35`). On very tight head-and-shoulders framing the subject's head
   may intrude into the top band; on wide framing 35% may be too generous. Options:
   (a) ship 0.35 and let `--roi` flags tune it; (b) a smaller band (0.25);
   (c) default to `--roi corners` (upper-left + upper-right, most subject-tolerant).
   Recommendation: ship **0.35 band** as default, document `corners` as the fallback.
2. **Keypoint method / dependency.** The pure-Go corner detector is offline,
   cloud-testable, and dependency-free but weaker; **gocv/OpenCV ORB** is much stronger
   and angle-tolerant but pulls a cgo + native OpenCV dependency the cloud cannot build
   and that is heavy on your PC. Recommendation: **ship the pure-Go matcher now** (so
   corroboration works immediately and offline) and treat gocv as an opt-in upgrade only
   if real-footage accuracy is insufficient. Do you want the gocv path built at all, or
   keep framematch dependency-light?
3. **Should `--roi band` be the DEFAULT, or keep `--roi full` default for back-compat?**
   Recommendation: make **`band` the default** (it is the fix) and keep `full` available;
   any existing scripts can pass `--roi full` to get identical old behavior.
