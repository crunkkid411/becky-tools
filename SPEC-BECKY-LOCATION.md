# SPEC-BECKY-LOCATION.md — room-fingerprint report: "were these filmed in the same place?"

> **CLOUD CORE BUILT 2026-06-22 — the deterministic clustering/verdict engine + CLI ship and
> are unit-tested; the real visual Fingerprinter (keyframe extract + cv2 features) is LEFT FOR
> LOCAL.** Tier-(a) `phash`+masked-decor-band+color is implemented (the honest deterministic
> floor); tier-(b) cv2 ORB features is the documented stub the local agent wires; tier-(c) a
> learned place-recognition embedding remains an Open Decision routed through deep-research.
> See §6 for the per-item DONE/LEFT split with the exact interface name. The fingerprint
> METHOD ORDER + crop default (Open Decisions §7) still want Jordan's confirmation, but the
> cloud-verifiable core no longer blocks on it.
>
> Authored 2026-06-22. Grounded in the EXISTING perceptual-hash room-matching code
> (`internal/osintexport/phash.go`, `cmd/framematch/`) and the frame-extraction +
> display-rotation primitives that already ship.

---

## 1. Purpose + user need

`becky location <video...>` answers ONE forensic question across a set of clips:

> **How many DISTINCT rooms/dwellings appear across these videos, which clip was filmed
> where, and were any two clips filmed in the SAME place?**

The concrete case (the "2601 Chatham Cir" exhibit work in `FORENSIC-OUTPUT-PHILOSOPHY.md`):
Jordan has many clips and needs to establish that clip X and clip Y were filmed in the same
dwelling — or prove they were NOT — without hand-cropping every pair in a video editor.
Today the only tool for this is `becky-framematch`, which:

- compares **exactly two** sources, not a whole set (`cmd/framematch/main.go:parsePositional`
  takes `srcA, srcB`);
- produces a HUMAN exhibit for one pair at a time (`buildComparison`), not a corpus-wide
  clustering + verdict;
- and — the known blocker in README §"framematch unreliable on portrait talking-head
  footage" — keys its whole-frame aHash on the **subject's body silhouette**, not on the
  fixed room decor, so same-room talking-head pairs get **missed** and different-room
  clips with similar global tone get **false-matched**.

`becky-location` is the corpus-level tool: feed it N clips, it returns the **set of distinct
rooms**, a **per-clip room assignment with confidence**, and a **same-dwelling vs
different-dwelling verdict** — corroborated, then concluded (`FORENSIC-OUTPUT-PHILOSOPHY.md`
TOP PRINCIPLE), not a pile of pairwise maybes.

It is a NEW tool, not a flag on framematch: framematch builds a court-exhibit page set for a
human to confirm a *single* comparison (candidate-not-conclusion), whereas `becky-location`
*clusters and concludes* across *many* clips. They are complementary — `becky-location`'s
output names the clip pairs worth handing to `becky-framematch` for an exhibit. The
single-tool principle (CLAUDE.md §1) is preserved.

---

## 2. The approach

### 2a. The hard part, named up front

The README known issue is the whole game: a **whole-frame** perceptual hash on talking-head
footage describes the *person*, not the *room*. Two clips of the same person sitting in two
different rooms can hash closer than two clips of two different people in the *same* room. So
a robust room fingerprint must **ignore the subject and key on the static decor** — the
ceiling, the upper wall band, trim, vents, windows, light fixtures, wall corners, and the
furniture *behind* the speaker. That is the same feature set the human reviewer hint already
names (`cmd/framematch/pairing.go:whatToLookFor`: "windows, vents, trim, outlets, furniture
placement, wall corners").

Two layers of defense, applied in order, corroborate-then-conclude:

1. **Pre-crop to the static-decor band before fingerprinting.** Most talking-head footage
   puts the subject's head/torso in the lower-center; the room's fixed structure lives in
   the **top band** (ceiling, wall corners, upper window/trim) and the **side margins**
   (door frames, wall edges, furniture). The default fingerprint is computed from the
   **top 30% horizontal band** plus the **left/right 15% vertical margins** — a crop mask
   that excludes the central lower region where the speaker sits. This single change is what
   the README fix-pending line calls for ("pre-crop to ceiling band before hashing").

2. **Two complementary fingerprint signals over that masked region** (Phase-1 deterministic,
   then an optional Phase-2 model — see §5):
   - **`phash`** (deterministic, pure-Go, ships first): the existing 64-bit aHash
     (`osintexport.AHashFromImage`) computed over the **masked decor band**, plus a coarse
     **per-region color histogram** of the same band (wall/decor color is a strong dwelling
     signal and is robust to the subject moving). Both are deterministic and need no model.
   - **`features`** (optional, Phase-2): ORB/AKAZE-class keypoint matching on the static
     decor (a vent, an outlet, a picture, trim corners) — the README's "keypoint/feature
     match on static decor". This survives camera-angle changes that defeat a hash and is
     what closes the portrait-footage gap *robustly*. Keypoint extraction needs OpenCV (the
     same cv2 already used in `internal/pyhelpers`) → it is the LOCAL-hardware half of the
     seam (§5). When absent, the tool degrades to `phash`-only with a stated lower
     confidence — never crashes, never silently claims feature-grade certainty.

A robust *learned* room embedding (a place-recognition vision model) is a THIRD possible
signal and the most powerful, but choosing one is a **model decision that MUST go through the
deep-research protocol** (`SPEC-DEEP-RESEARCH.md` / `STANDARDS-MUSIC-RESEARCH.md`), not a
guess in this spec. It is listed as an Open Decision (§7), not designed here.

### 2b. Keyframe sampling (per clip)

A talking-head clip is largely static, so a dense 1-fps sweep is wasteful and noisy. Per clip:

- Probe duration + display-rotation ONCE (`osintexport.DisplayRotation`) and extract frames
  **upright** via `osintexport.ExtractFrameRotated` (reuse verbatim — the rotation gotcha in
  README §"Face rotation" applies equally here: a sideways frame ruins the decor crop).
- Sample at a coarse interval (default `--interval 2.0`), then **deduplicate near-identical
  frames** by aHash Hamming distance so a static clip collapses to a handful of representative
  **keyframes** (the room rarely changes within one clip; a clip that DOES change rooms
  produces multiple keyframe groups — see "multi-room clip" below).
- Compute the room fingerprint (masked phash + color histogram, and features if available)
  on each keyframe. A clip's fingerprint is the **set** of its keyframe fingerprints; its
  *primary* fingerprint is the medoid (the keyframe nearest all others) so a single per-clip
  vector exists for the simple case while a multi-room clip is still representable.

This sampling/dedup is identical in spirit to `framematch`'s greedy 1:1 selection
(`cmd/framematch/pairing.go`) — "one clear comparison per distinct scene."

### 2c. Clustering clips into distinct rooms

Given the per-keyframe fingerprints, build a **distance matrix** and run deterministic
**agglomerative single-link-with-a-cutoff clustering** (no random init — determinism is an
invariant, CLAUDE.md §2):

- Distance between two fingerprints is a **fused** score, not a single number — this is the
  corroboration mechanism, in code:
  - `phash` Hamming over the masked decor band (normalized 0–1, mirrors
    `framematch` `Similarity = 1 - hamming/64`),
  - color-histogram chi-square distance over the same band,
  - and feature-match inlier ratio when `features` is available (1 − inlier_ratio).
- A pair is **"same-room agreeing"** only when **≥2 of the available signals** fall under
  their thresholds (the ≥2-independent-signals rule, `FORENSIC-OUTPUT-PHILOSOPHY.md`). A lone
  signal (e.g. similar color but disagreeing decor hash) is **not** enough to merge two
  clips into one room — it becomes a `weak_link` flagged for human review, never an automatic
  conclusion.
- Cluster keyframes first, then assign each **clip** to the room that holds the majority of
  its keyframes. A clip whose keyframes split across ≥2 rooms is reported as a
  **multi-room clip** (e.g. a walkthrough), with each segment's room + the timestamp where it
  changes — not forced into one bucket.

Thresholds default to the calibrated framematch values (`--threshold 10` Hamming → ~0.84
similarity) and are tunable; like every becky threshold they are documented and overridable,
not magic (README §"Voice threshold").

### 2d. The same/different-dwelling verdict

A **room** is one physical space; a **dwelling** is a set of rooms in one residence. Two
clips can be different *rooms* of the same *dwelling*. The verdict layer therefore reasons
above the room clusters:

- **SAME ROOM** — two clips land in the same room cluster with ≥2 signals agreeing → state
  it plainly: "clip A and clip B were filmed in the same room (decor hash + 41 matched
  features)".
- **SAME DWELLING (different rooms)** — clips are in *different* room clusters but share a
  **dwelling signal**: a recurring decor element across rooms (same flooring, same trim/paint
  palette, same outlet/switch style), and/or corroborating metadata (EXIF/QuickTime GPS or
  capture-time proximity via `internal/exifmeta` — README §"Reuse YouTube sidecars; never
  trust mtime"). ≥2 dwelling signals agreeing → "same dwelling, different rooms".
- **DIFFERENT DWELLING** — distinct room clusters AND no corroborating dwelling signal AND a
  large decor distance → "filmed in different places".
- **UNDETERMINED** — signals conflict, or only one weak signal exists, or footage is too
  degraded (no upright frames, all-dark, single keyframe) → say so plainly and name what's
  missing, rather than guess (the "one weak signal → unknown" half of the philosophy).

The verdict is emitted **per clip pair of interest** AND as one corpus-level headline
("3 distinct rooms across 7 clips; all consistent with ONE dwelling"). Confidence is a
number plus the human-readable basis, exactly like `identify`'s fused output.

---

## 3. CLI, JSON schema, and human summary

### 3a. CLI

```
becky location <video-or-folder...> [options]

Positional: one or more video files, and/or a folder (every video inside is a clip).

  --interval <sec>       seconds between keyframe samples per clip   (default 2.0)
  --crop <preset|spec>   decor crop mask: "talking-head" (default; top 30% + side 15%),
                         "top" (top 30% only), "full" (no mask — legacy whole-frame),
                         or "T,L,R,B" explicit percentages to drop                (default talking-head)
  --fingerprint <m>      "phash" (deterministic, default) | "features" | "auto"
                         ("auto" uses features when the helper is available, else phash)
  --threshold <bits>     same-room aHash Hamming cutoff over the masked band (0-64)  (default 10)
  --color-threshold <f>  same-room color chi-square cutoff (0-1)                    (default 0.25)
  --min-signals <n>      independent signals that must agree to MERGE clips (1-3)    (default 2)
  --metadata             also read EXIF/QuickTime GPS + capture-time as a dwelling signal (default on)
  --pair A,B             restrict the verdict section to specific clip indices/names (repeatable)
  --frames-dir <path>    where extracted keyframes/sidecars go     (default location-out/)
  --output <file>        write JSON here instead of stdout
  --verbose              progress to stderr
```

Conventions are the becky standard (README §"Conventions"): JSON to stdout, diagnostics to
stderr (silent without `--verbose`), exit 0 on success, sources only READ, all frames are
COPIES, degrade-never-crash.

### 3b. JSON schema (stdout)

```json
{
  "tool": "becky-location v1.0.0",
  "generated_at": "2026-06-22T18:00:00Z",
  "fingerprint_method": "phash",            // phash | features | features+phash
  "crop": "talking-head",
  "clip_count": 7,
  "clips": [
    {
      "index": 0,
      "path": "E:/case/livingroom_01.mp4",
      "sha256": "…",
      "duration": 142.5,
      "keyframe_count": 4,
      "room_id": "room-1",
      "room_confidence": 0.91,              // confidence this clip belongs to room-1
      "multi_room": false,
      "segments": [                          // present when multi_room: room per time span
        { "room_id": "room-1", "start": 0.0, "end": 142.5 }
      ],
      "decor_hash": "1f3c…",                // masked-band aHash (provenance)
      "metadata": { "gps": null, "capture_time": "2025-11-03T14:02:00", "capture_time_source": "quicktime" }
    }
  ],
  "rooms": [                                 // the DISTINCT room clusters
    {
      "room_id": "room-1",
      "label": "Room 1",
      "clip_indices": [0, 2, 5],
      "member_count": 3,
      "cohesion": 0.88,                      // mean intra-cluster agreement
      "representative_keyframe": "location-out/clip0_room1_kf2.jpg",
      "decor_features": "windows + wall trim + ceiling line (top band)"
    }
  ],
  "room_count": 3,
  "dwellings": [                             // rooms grouped into residences
    { "dwelling_id": "dwelling-1", "room_ids": ["room-1","room-2"], "basis": ["shared flooring", "capture-time within 6 min"] }
  ],
  "dwelling_count": 1,
  "verdict": {
    "headline": "3 distinct rooms across 7 clips, all consistent with ONE dwelling.",
    "level": "SAME_DWELLING",              // SAME_ROOM | SAME_DWELLING | DIFFERENT_DWELLING | UNDETERMINED
    "confidence": 0.86,
    "basis": [
      "rooms 1 and 2 share flooring and wall-paint palette (2 signals)",
      "all clips' capture times fall within a 22-minute span"
    ]
  },
  "pair_verdicts": [                         // explicit same/different per pair of interest
    {
      "a": 0, "b": 2,
      "level": "SAME_ROOM",
      "confidence": 0.93,
      "signals": { "decor_hash_hamming": 4, "color_chi2": 0.11, "feature_inliers": 41 },
      "basis": "same room — decor hash + color + 41 matched static features agree",
      "exhibit_hint": "becky-framematch \"E:/case/livingroom_01.mp4\" \"E:/case/livingroom_03.mp4\""
    }
  ],
  "review_required": [                        // weak/conflicting links the human must judge
    { "a": 3, "b": 6, "reason": "color matches but decor hash disagrees (1 signal only)" }
  ],
  "degraded": [],                            // clips that could not be fingerprinted, with reason
  "notes": "Room fingerprints are decor-band perceptual signals, not a geolocation conclusion."
}
```

Field choices mirror existing structs so the seam is familiar: `sha256` + per-frame
provenance follow `osintexport.Sidecar`; `decor_hash_hamming` / similarity follow
`framematch` `Pair`; `basis`/`confidence`/`level` follow `identify`'s corroborated output and
the `[DOCUMENTED]`/`[CANDIDATE]` tagging in `FORENSIC-OUTPUT-PHILOSOPHY.md` §6.

### 3c. Human summary (concise, eyes-friendly — ACCESSIBILITY.md)

`becky-location` is a CLI that writes JSON, but per ACCESSIBILITY.md (Jordan reads the
terminal, "lead with the answer, keep it tight"), a `--summary` flag (or the `becky` runner)
prints a short, plain block — the verdict FIRST, then the per-clip room map, then anything
flagged. No table that only makes sense by alignment; one fact per line:

```
VERDICT: Same dwelling. 3 rooms across 7 clips, all one residence (confidence 0.86).
  Basis: rooms 1 and 2 share flooring + paint; all clips captured within 22 minutes.

Rooms:
  Room 1 — clips 0, 2, 5  (living room: windows + wall trim)
  Room 2 — clips 1, 4     (kitchen: cabinets + tile backsplash)
  Room 3 — clip 3         (bedroom)
  clip 6 — multi-room: Room 1 (0:00-1:10) then Room 3 (1:10-end)

Same room (high confidence):
  clip 0 + clip 2  -> same room (decor hash + 41 matched features). Exhibit: becky-framematch …

Needs your eyes:
  clip 3 + clip 6: color matches but decor disagrees — one signal only.
```

This keeps the corroborated conclusion loud and the maybes few and clearly separated — the
exact anti-"flood of maybes" requirement.

---

## 4. Deterministic / offline / degrade-never-crash

- **Deterministic.** Same clips in → same JSON out. Clustering is agglomerative with a fixed
  cutoff and tie-breaks resolved by clip index (mirrors `pairing.go`'s stable sort). The
  optional feature matcher is run with a fixed seed / fixed parameters; aHash and histograms
  are inherently deterministic. No wall-clock except the `generated_at` provenance stamp.
- **Offline.** No network. The only external processes are `ffmpeg`/`ffprobe` (already a hard
  dep) and, for `--fingerprint features`, the local cv2 helper — the same offline OpenCV
  already embedded in `internal/pyhelpers`. The optional learned-embedding signal (if Jordan
  approves one via deep-research) would be a **local** llama.cpp/ONNX model, never a service.
- **Degrade-never-crash** (README §"Conventions"; CLAUDE.md §2 "Degrade, never crash"):
  - no `ffmpeg`/`ffprobe` → exit 0 with a clear note, empty result.
  - feature helper absent / cv2 import fails → fall back to `phash`, set
    `fingerprint_method: phash`, lower confidence, add a note. Never claim feature-grade
    certainty without features.
  - a clip with no video stream / all-dark / single keyframe / unreadable → listed in
    `degraded[]` with the reason, the rest of the corpus still processed.
  - one clip → no pair verdicts; `verdict.level: UNDETERMINED` with "only one clip provided".
  - conflicting signals → `UNDETERMINED` + the conflict named, never a coin-flip.

---

## 5. Cloud-vs-local split (honest seam)

The deterministic **clustering + verdict logic over a fingerprint vector is fully
cloud-buildable and cloud-testable** — it is pure Go math over `[]float64` / `uint64`
fingerprints, with no media or model needed. The **fingerprint *production*** from real video
is the part that needs Jordan's hardware. The seam is drawn cleanly between them:

| Cloud (here) | Local (Jordan's Win10 + GPU) |
|---|---|
| The whole `internal/roomprint` clustering + dwelling-grouping + verdict engine over an **abstract `Fingerprint`** (decor-hash bits + color histogram + optional feature descriptor) | Real keyframe extraction (`ffmpeg`/`ffprobe`) on actual footage |
| The crop-mask math (compute the decor-band rectangle from `--crop` + frame dims) as a pure function over `(w,h)` | The cv2/ORB **feature** helper (the `features` signal) — runs against real frames |
| The fused-distance + `--min-signals` ≥2 corroboration rule | If a learned room-embedding model is approved (Open Decision), running it on the 3070 |
| Unit tests asserting cluster membership + verdict **from synthetic fingerprint vectors** (no media) | End-to-end run on the real case folder; threshold calibration on actual talking-head clips |
| The `phash` + masked-band path (pure Go, reuses `osintexport.AHashFromImage`) and the CLI/JSON | `build-all-tools.bat` (auto-discovers `cmd/location`); sound/vision-check on real evidence |

**Seam contract (the one boundary cloud stubs):** a `Fingerprinter` interface —

```go
// Fingerprinter turns one keyframe image into a room Fingerprint. The phash
// implementation is pure-Go (cloud). The feature implementation shells to the
// cv2 helper (local). The clustering engine consumes Fingerprint only.
type Fingerprinter interface {
    Print(img image.Image, mask CropMask) (Fingerprint, error)
}

type Fingerprint struct {
    DecorHash uint64    // masked-band aHash (osintexport.AHashFromImage over the crop)
    ColorHist []float64 // coarse per-region histogram of the masked band, L1-normalized
    Features  []byte    // optional ORB/AKAZE descriptors; nil when unavailable
}
```

The pure-Go `phashFingerprinter` ships and is fully testable now. The `featureFingerprinter`
is a documented stub with this exact contract for the local agent to wire to cv2 — the same
"one-call stub with a documented contract" pattern used across §6 handoffs in CLAUDE.md.

**Honest note on a model:** a robust talking-head room fingerprint may genuinely benefit from
a learned place-recognition embedding (NetVLAD-class) rather than hand-rolled features. **That
is a model choice and is OUT OF SCOPE for this spec** — per ACCESSIBILITY.md/CLAUDE.md, model
selection runs through the deep-research protocol, not a guess. `phash`+`features` is the
honest deterministic floor that ships and works offline today; a learned signal is an
additive Open Decision (§7).

---

## 6. Build plan (checkboxed) + unit tests

### Cloud (this branch — deterministic, no media, fully testable) — BUILT 2026-06-22

> NOTE: the package was named `internal/location` (not `internal/roomprint`) to match the
> `cmd/location` binary and the becky one-package-per-tool convention. The Fingerprinter
> interface the LOCAL feature impl must satisfy is **`location.Fingerprinter`**:
> `Print(img image.Image, mask location.CropMask) (location.Fingerprint, error)`.

- [x] `internal/location/fingerprint.go` — `Fingerprint` struct (`DecorHash uint64`,
      `ColorHist []float64`, `Features []byte`), `CropMask`, the `Fingerprinter` interface, and
      `MaskPreset` (talking-head/top/full).
- [x] `internal/location/crop.go` — `CropRect(w, h, mask) image.Rectangle` (pure function;
      degenerate masks fall back to full frame) + `ParseCrop` (presets or explicit "T,L,R,B").
- [x] `internal/location/distance.go` — `decorHamming`, `colorChi2`, `featureDistance`, and
      `fuse(a, b, thresholds) SignalScore` (counts INDEPENDENT agreeing signals — the
      corroboration rule in code).
- [x] `internal/location/cluster.go` — deterministic agglomerative (union-find, lowest-index
      canonical) clustering with the `--min-signals ≥2` merge rule; a lone agreeing signal →
      `WeakLink` (review_required), never an auto-merge. Returns rooms + per-clip assignment +
      cohesion + all scored pairs.
- [x] `internal/location/dwelling.go` — `GroupDwellings` groups rooms into dwellings on
      shared decor (color) + capture-time + GPS signals (≥2 → merge); produces the corpus
      verdict (`SAME_ROOM`/`SAME_DWELLING`/`DIFFERENT_DWELLING`/`UNDETERMINED`) with basis +
      confidence. One lone shared signal → `UNDETERMINED` (no overclaim).
- [x] `internal/location/phash.go` — pure-Go `phashFingerprinter` over the masked band
      (`osintexport.AHashFromImage` on `CropRect`) + a 4×4×4 RGB color histogram.
- [x] `cmd/location/main.go` — CLI flags (§3a), positional clip/folder expansion, per-clip
      keyframe sampling (`sampling.go`, reuses `osintexport.ExtractFrameRotated` +
      `DisplayRotation`), dedup, fingerprinting via the chosen `Fingerprinter`, then the
      engine; emits the exact §3b JSON (`report.go`/`pairs.go`) and the `--summary` block
      (`summary.go`).
- [x] `cmd/location/features_stub.go` — the `featureFingerprinter` documented stub with the
      cv2 contract; `--fingerprint auto`/`features` silently degrades to phash when the stub
      reports `Available()==false`.
- [x] `cmd/location` auto-discovered by `build-all-tools.bat`; `go build ./...` green.
- [x] `go build/vet/test ./cmd/location/... ./internal/location/...` + `gofmt -l` green
      (cloud half of the gates; `build-all-tools.bat` is the LOCAL completion step).

### Local (Jordan's machine — needs media/GPU) — LEFT FOR LOCAL

The cloud half is the **clustering/verdict engine over an abstract `Fingerprint`** — it is
fully built + unit-tested WITHOUT media. What remains needs real footage/CV on hardware:

- [ ] **Implement the real `Fingerprinter` for the `features` signal.** The interface to
      satisfy is `location.Fingerprinter` (`Print(img image.Image, mask location.CropMask)
      (location.Fingerprint, error)`). Replace the body of `cmd/location/features_stub.go`
      `featureFingerprinter.Print` (and make `Available()` return true when the helper is
      present): crop to `location.CropRect`, then shell to the new
      `internal/pyhelpers/room_features.py` (cv2 ORB/AKAZE) — read images via
      `np.fromfile`+`cv2.imdecode`, NEVER `cv2.imread` (README §"Unicode paths"). Fill
      `Fingerprint.Features` with the descriptor blob; for a TRUE geometric inlier ratio,
      replace `internal/location/distance.go:decodeInlierRatio` with a real match-endpoint
      read (today it is a deterministic stand-in so the fused-distance math is testable).
- [ ] **The keyframe-extraction media path already exists** in `cmd/location/sampling.go`
      (ffmpeg/ffprobe via osintexport) and degrades-never-crashes when binaries are absent —
      verify it runs on the real case folder; tune the dedup `dedupBits` and `--interval` if a
      static clip over-/under-samples.
- [ ] `build-all-tools.bat` (auto-discovers `cmd/location`); run `becky-location <folder>` on
      the real case folder; calibrate `--threshold`/`--color-threshold`/`--min-signals` on
      actual portrait talking-head clips.
- [ ] Confirm the masked crop actually fixes the README portrait-footage failure on real
      footage (same-room talking-heads now cluster; different-room same-tone clips no longer
      false-merge). Paste evidence into CLAUDE.md §6.

### Unit tests (cloud — assert from SYNTHETIC fingerprint vectors, assert VALUES)

Per `STANDARDS-ENGINEERING.md` (assert values, not truthiness; regression test per bug):

All cloud tests are written and GREEN (32 tests across the two packages). Mapping (the test
file names differ slightly from the spec's suggested names, noted inline):

- [x] `internal/location/cluster_test.go` (`TestCluster_ThreeRooms`): three synthetic groups
      (decor hashes differing by 2 within a group, ≥thousands of bits across groups, each
      group sharing a color palette) → asserts exactly 3 rooms with the expected membership
      `{0,1},{2,3},{4,5}`. `TestCluster_BorderlineGoesToReview`: a clip sharing ONLY color
      (decor 64 bits away) is NOT merged and a `WeakLink` is recorded (the ≥2-signal rule).
- [x] `internal/location/distance_test.go`: exact `decorHamming` (0/4/64) + `colorChi2`
      (identical→0, disjoint→1.0, unavailable→-1) values; `fuse` returns `Agreeing==2` only
      when two signals are under threshold, `1` (weak link) when only one is.
- [x] `internal/location/dwelling_test.go`: shared color + close capture-time → `SAME_DWELLING`
      (basis names the signals); no shared signal + large decor distance → `DIFFERENT_DWELLING`;
      one signal only → `UNDETERMINED`. Exact `level` asserted.
- [x] `internal/location/crop_test.go`: `CropRect(1920,1080,"talking-head")` → exact
      `(288,0)-(1632,324)`; `"full"` → full bounds; explicit `"10,20,20,40"` → exact pixels;
      determinism; degenerate mask falls back to full.
- [x] verdict cases live in `dwelling_test.go`: single clip → `UNDETERMINED` ("only one clip");
      one-weak-signal → `UNDETERMINED`; clean same-room → `SAME_ROOM` with confidence ≥0.8.
- [x] degrade cases in `internal/location/cluster_test.go` (`TestCluster_DegradedExcluded`) +
      `cmd/location/report_test.go` (`TestBuildReport_Degraded`): a degraded clip lands in
      `degraded[]` with a reason, the rest still cluster; feature helper absent →
      `fingerprint_method: phash`, never a crash (`features_stub.go` `Available()==false`).
- [x] `cmd/location/report_test.go` additionally asserts the §3b JSON schema round-trips, the
      provenance note is present, a SAME_ROOM pair carries a `becky-framematch` exhibit hint,
      and `--summary` leads with `VERDICT:` and lists rooms (ACCESSIBILITY.md).

---

## 7. Open Decisions for Jordan

1. **Fingerprint method — the central decision.** Three tiers, in increasing power and cost:
   (a) **`phash` + masked decor band + color histogram** — pure-Go, ships today, fixes the
   *biggest* portrait failure (the body is cropped out) with zero new deps; (b) **+ cv2 ORB
   feature matching** on static decor — robust to camera-angle change, needs the local cv2
   helper; (c) **+ a learned place-recognition embedding** (NetVLAD-class) — most robust, but
   a **model choice that must go through the deep-research protocol** (not guessed here). I
   recommend building (a) now and (b) as the local wiring, and only researching (c) if real
   footage proves (a)+(b) insufficient. **Confirm this order.**
2. **Crop default.** Is `talking-head` (top 30% + side 15%) the right default for his corpus,
   or is most footage wider/handheld where `full` or `top`-only is better? This is the single
   knob most likely to need real-footage tuning.
3. **Room vs dwelling granularity.** Does Jordan want the dwelling-grouping layer (rooms →
   residence) at all, or is "distinct rooms + same/different ROOM" enough for his cases? The
   dwelling layer adds the shared-decor + metadata reasoning; it's additive and could ship
   later.
4. **Metadata as a dwelling signal.** OK to use EXIF/QuickTime GPS + capture-time proximity
   (`internal/exifmeta`) as a corroborating signal, given capture time is already labeled
   trusted-vs-`mtime(untrusted)` (README §"Reuse YouTube sidecars")? It strengthens the
   verdict but only when the metadata survives the source's processing.
5. **Same-room confidence bar.** The default merge rule is ≥2 agreeing signals; what
   confidence floor should promote a pair to a stated `SAME_ROOM` conclusion vs leaving it in
   `review_required`? (Mirrors the `identify --face-threshold 0.55` calibration debate.)
