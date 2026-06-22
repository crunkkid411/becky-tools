# SPEC-FACE-CROP-DB.md — Tight face-crop artifacts + face embeddings written to forensic.db

> **SPEC — NOT BUILT, AWAITING JORDAN'S APPROVAL.**
> Research + design only. No Go code has been written. No new binary exists. Nothing
> in `becky-go/` has been changed. Jordan approves before any build starts.
>
> Authored 2026-06-22. Every code reference below was checked against the current tree
> (`becky-go/`, module `becky-go`) and is cited as `file:symbol`. No model, library, or
> behavior is invented — the geometry + DB layer this spec adds is pure-Go and offline;
> the SCRFD detection + ArcFace embedding it consumes ALREADY EXIST in the codebase.

---

## 1. The problem, stated precisely (and where it lives in the code)

This closes the README **"High"** known issue:

> **Face crops are torso-only on talking-head footage** (`osint`/`identify`)
> When SCRFD detects a face, the saved frame is the full scene — the face bbox is not
> cropped to a tight artifact. This makes "teach becky this person" unreliable on footage
> where faces are small or off-center. **Fix pending:** save a tight face-crop (+margin)
> as its own artifact, write the face embedding into `forensic.db`.

Two concrete, code-grounded failures:

### 1a. The saved artifact is the whole scene, not the face

`becky-osint` exports one frame per event. For a `multi_face` event (`cmd/osint/osint.go:filePrefix`
maps `"multi_face"` → `"face"`), `cmd/osint/main.go:exportEvent` calls
`osintexport.ExtractFrame(cfg.FFmpeg, input, ts, framePath, format, ffmpegQ)`
(`cmd/osint/main.go:219`) which writes the **full-resolution, full-scene** frame
(`internal/osintexport/osintexport.go:ExtractFrame` / `ExtractFrameRotated`). There is **no
bbox crop** anywhere on that path: `becky-osint` reads a `becky-events` JSON file
(`cmd/osint/osint.go:Event`, `loadEvents`) that carries only `type`/`start`/`timestamp`/`frame`
— **it never sees a face bounding box.** So the saved `face_*.jpg` is a wide shot, and on a
talking-head clip where the subject is small or off-center, that wide shot is dominated by
torso, room, and background.

Why this breaks enrollment: `becky enroll` / `becky "this is <name>" <clip>` builds
`kb/face-prints/<name>/*.jpg`, and at match time `cmd/identify/face.go:identifyFaces` embeds
those print images via `faceembed.Embed` (`internal/faceembed/faceembed.go`) which detects the
**most prominent** face (largest bbox × det_score) in the image. A wide, torso-dominated print
is fragile: SCRFD may pick a different/secondary face, or the face is so small that the
512-d ArcFace embedding is low quality — exactly the "co-present person's face" / weak-print
failure mode the existing **face-collision guard** warns about (README "Enrollment"; SPEC-PERSON-CLUSTERING §7.3).

### 1b. The face embedding is computed, then thrown away

`cmd/identify/face.go:identifyFaces` already has, per sampled frame, a `faceembed.Face` with:

- `Face.BBox []float64` — `[x1,y1,x2,y2]` of the best face,
- `Face.DetScore float64`,
- `Face.Vector []float64` — the **L2-normalized 512-d ArcFace embedding**,
- `Face.NFaces int`,

(`internal/faceembed/faceembed.go:Face`, lines 32–39). The match loop
(`cmd/identify/face.go:94`) uses `Vector` for cosine, then **discards** the vector, the bbox,
and the det score. Nothing persists. This is the same observation SPEC-PERSON-CLUSTERING §7.1
makes ("Today embeddings are computed and thrown away after matching") — and it is why
`becky-cluster` cannot run across the corpus, and why there is no per-appearance face artifact
to review.

### 1c. The table for the embedding already exists — and nothing fills it

`internal/beckydb/cluster.go` already ships `appearance_embeddings` (created by
`DB.EnsureClusterSchema`), with columns purpose-built for exactly this:
`appearance_id` (PK), `source_file`, `source_sha256`, `modality` (`"face"`/`"voice"`),
`vector_json`, `dim`, `timestamp`, `frame_index`, `speaker_id`, `det_score`, `created_at`
(`cluster.go:39–51`), plus `UpsertAppearance` / `ListAppearances` / `CountAppearances`
(`cluster.go:105,126,148`). **No tool writes a single row to it today** (grep: only the
package's own tests call `UpsertAppearance`). The table was built ahead of the producer.

**So the fix is small and mostly already-scaffolded:** (1) crop to the SCRFD bbox + a margin
and save it as a distinct artifact; (2) write the face embedding + bbox + provenance into the
**existing** `appearance_embeddings` table (one additive column: `crop_path`). This unblocks
reliable enrollment AND becky-cluster face clustering at once.

---

## 2. Design

### 2a. The crop, as its own artifact (geometry is the new pure-Go core)

When SCRFD returns a face bbox for a sampled frame, crop the **decoded frame image** to the
bbox **expanded by a configurable margin**, clamped to the frame edges, and save that tight
crop as a separate file next to (not instead of) the full frame.

- **Margin** is a fraction of the bbox's larger side (so it scales with face size, not pixels).
  Default `0.4` (40%) — generous enough to include hairline/chin/ears that ArcFace alignment
  benefits from, the standard "context margin" for face-recognition crops. Configurable via
  `--face-margin`.
- **Clamping** at frame edges: an off-center or edge face must never produce an out-of-bounds
  rect. The expanded rect is intersected with `[0,W]×[0,H]`. (This asymmetric clamp — e.g. a
  face flush against the left edge keeps its full right margin but a truncated left margin — is
  the behavior the unit tests pin; see §6.)
- **The crop is a NEW artifact, the full frame is unchanged.** We never modify the source
  video and never overwrite the full-scene frame (FORENSIC-OUTPUT-PHILOSOPHY "Provenance /
  honesty": work on copies, reveal-don't-create). The crop is a faithful pixel sub-rectangle —
  no scaling, no warping, no enhancement — so it survives "what did you do to this image?".
- The crop image is produced from the SAME decoded-and-rotation-corrected frame the embedder
  saw (`internal/osintexport.ExtractFrameRotated` already applies display rotation —
  `cmd/identify/face.go:163`), so the crop is upright and matches the embedding's coordinate
  frame. **The bbox is in the rotation-corrected frame's coordinates** (SCRFD ran on the
  upright frame), so the crop math needs no rotation handling of its own.

**Crop is done in pure Go from the decoded image** (stdlib `image` + `image.Rectangle`), NOT
via a second ffmpeg `crop=` pass. The frame JPEG/PNG is already decoded in-process for the
perceptual hash (`cmd/osint/main.go:perceptualHash` decodes via `image.Decode`); cropping a
`*image.RGBA`/`image.SubImage` and re-encoding with `image/jpeg`/`image/png` keeps the new
math deterministic, dependency-free, and unit-testable without ffmpeg or a GPU. (`image.Image`
exposes `SubImage` on the stdlib decoders; for the general case we copy the sub-rect into a
fresh `*image.RGBA` so any decoder works.)

### 2b. Where the bbox comes from — the seam

Two producers, one shared geometry+DB helper:

- **`becky-identify` (primary producer).** `cmd/identify/face.go:identifyFaces` already holds
  every frame's `faceembed.Face{BBox, DetScore, Vector, ...}` and the frame file path + the
  frame timestamp (`times[i]`). It is the only place that has bbox **and** embedding together.
  This is where crops + DB rows are written, behind a flag (§3), without changing match logic.
- **`becky-osint` (secondary producer).** `becky-osint` itself has no embeddings (it reads an
  events JSON). To give it tight crops it must call the shared `faceembed.Embed` on the frame
  it just extracted for a `multi_face` event and use the returned bbox. This is opt-in
  (`--face-crop`, §3) and degrade-gracefully: no face-model configured → keep today's
  full-scene behavior + a note. (Phase 2; identify is Phase 1 — see §6.)

The shared code lives in a small NEW pure-Go package, **`internal/facecrop`**, that depends on
neither ffmpeg nor Python nor a GPU:

```
internal/facecrop/
  geom.go    // CropRect(bbox [4]float64, margin float64, frameW, frameH int) image.Rectangle
             //   - expands bbox by margin*max(bboxW,bboxH) on each side
             //   - clamps to [0,W]x[0,H]; returns the empty rect for a degenerate/zero bbox
  crop.go    // CropImage(src image.Image, r image.Rectangle) image.Image  (copy into fresh RGBA)
             // SaveCrop(img image.Image, outPath, format string, jpegQ int) error
  appear.go  // AppearanceFromFace(...) beckydb.AppearanceRow  (build the DB row + det. id)
```

`geom.go` is the heart of the testable cloud-buildable work: it is **pure integer/float
arithmetic, no I/O**, so the clamping behavior is verified with table-driven tests (§6).

### 2c. Persisting the embedding + metadata into forensic.db

Write one `appearance_embeddings` row per detected, embedded face, using the **existing**
`beckydb.UpsertAppearance` (`internal/beckydb/cluster.go:105`) after
`db.EnsureClusterSchema()`. Mapping (all fields already exist except `crop_path`):

| `appearance_embeddings` column | value | source |
|---|---|---|
| `appearance_id` (PK) | `sha12(source_file)+":face:"+frame_index` | deterministic; matches the scheme in cluster.go header + SPEC-PERSON-CLUSTERING §7.1 |
| `source_file` | the clip path | identify input |
| `source_sha256` | clip SHA-256 | `osintexport.SHA256File` (identify can compute once per run) |
| `modality` | `"face"` | constant |
| `vector_json` | `Face.Vector` as a JSON float array `[…]` | `faceembed.Face.Vector` (L2-normalized 512-d) |
| `dim` | `len(Face.Vector)` (512) | recorded, not assumed |
| `timestamp` | frame time (s) | `times[i]` in `identifyFaces` |
| `frame_index` | `int(ts*fps + 0.5)` | same formula already in `face.go:133` |
| `speaker_id` | `""` (face) | constant |
| `det_score` | `Face.DetScore` | `faceembed.Face.DetScore` |
| `crop_path` | path to the tight crop artifact (**NEW column**) | §2a |
| `created_at` | RFC3339 | defaulted by `UpsertAppearance` |

**Schema change (additive, one column).** Add `crop_path TEXT` to the `appearance_embeddings`
DDL in `internal/beckydb/cluster.go:clusterSchema`, add the field to `AppearanceRow`
(`cluster.go:88`), and thread it through `UpsertAppearance`'s INSERT and `ListAppearances`'s
SELECT. Because that table is created by `EnsureClusterSchema` (separate from the canonical
`schema.sql`/`EnsureSchema`), this touches **only** the clustering path — embed/search are
unaffected, exactly as the cluster.go header already promises. For a pre-existing DB that has
the table without the column, `EnsureClusterSchema` runs `CREATE TABLE IF NOT EXISTS` which is
a no-op; an idempotent `ALTER TABLE appearance_embeddings ADD COLUMN crop_path TEXT` guarded by
"ignore duplicate-column error" is added so older DBs gain the column (degrade-never-crash:
a failed ALTER that isn't "duplicate column" is surfaced; "duplicate column" is swallowed —
same pattern as `isFTS5Unavailable` / `isMissingAppearanceTable` in beckydb.go).

The face-print link ("person link"): a freshly-clustered face has **no** name yet
(candidate-not-conclusion). The link is realized through the existing chain:
`appearance_embeddings.appearance_id` → a `clusters` row (`beckydb.UpsertCluster`, member list)
→ `clusters.suggested_name` set once by a human (`beckydb.NameCluster`) → optional back-fill of
`identifications` rows (SPEC-PERSON-CLUSTERING §7.4). This spec **does not** auto-attach a name
to a crop; it persists the raw material so that chain can run.

### 2d. How this feeds enroll + becky-cluster

- **Enroll:** the tight crop artifact is exactly what should land in
  `kb/face-prints/<name>/*.jpg`. A tight, single-face crop is a far stronger print than a wide
  scene and sidesteps the "most-prominent-face" ambiguity at `faceembed.Embed`. When a human
  names a cluster (or confirms `becky "this is <name>" <clip>`), the enroll step copies the
  cluster's highest-`det_score`, single-face crops (`NFaces == 1`) — reusing the
  **face-collision guard** (SPEC-PERSON-CLUSTERING §7.3) so a co-present person's face is never
  grabbed.
- **becky-cluster:** `becky-cluster` reads `appearance_embeddings` via `ListAppearances("face")`
  (`cluster.go:126`) — which until now returned nothing because nothing was stored. With this
  feature, a normal `becky-identify` (or pipeline) pass populates the table, so face clustering
  has real input. Each cluster's `representative_frame` (SPEC-PERSON-CLUSTERING §5 output) points
  at a tight `crop_path`, so the human reviews a face, not a wide room. This is the producer
  SPEC-PERSON-CLUSTERING §7.1 explicitly calls for ("storing vectors is the durable path").

---

## 3. CLI flags, artifact naming, schema change

### 3a. Flags

**`becky-identify`** (Phase 1):

```
--store-faces            persist each detected face's embedding + tight crop into the DB
                         (writes appearance_embeddings rows; off by default — opt-in, so a
                         plain match run is unchanged)
--db <forensic.db>       DB to write appearance rows into (required with --store-faces)
--face-crop-dir <dir>    where tight face crops are written
                         (default: <db-dir>/face-crops/  — next to the DB, never next to source)
--face-margin <f>        crop margin as a fraction of the face's larger side (default 0.4)
--face-crop-format jpg|png   crop image format (default jpg)
```

**`becky-osint`** (Phase 2, opt-in):

```
--face-crop              for each multi_face event, detect the face on the extracted frame and
                         ALSO save a tight crop (+margin) as a distinct artifact + write its
                         embedding to --db. No face model / no detection -> keep the full-scene
                         frame, add a note, exit 0 (degrade).
--db <forensic.db>       DB to write appearance rows into (with --face-crop)
--face-margin <f>        (default 0.4, same as identify)
```

All other behavior (JSON to stdout, exit codes, never modify source) is unchanged.

### 3b. Artifact naming

Crops live in their own directory and never collide with the full-scene frame:

```
<face-crop-dir>/<sha12(source)>_<frameIndex>_face<bboxOrdinal>.jpg
```

- `sha12(source)` = first 12 hex chars of the source SHA-256 (same short-id scheme used for
  `appearance_id` and segment ids elsewhere in beckydb) — keeps crops from different clips with
  the same frame index distinct, and ties the filename to provenance.
- `frameIndex` = `int(ts*fps + 0.5)` (the value also stored in the DB row).
- `bboxOrdinal` = `0` for the prominent face becky-identify embeds today; reserved (`1`, `2`, …)
  for the multi-face extension. The on-disk name is deterministic, so a re-run overwrites the
  same crop (idempotent, matching `UpsertAppearance`'s INSERT OR REPLACE).

For becky-osint the existing full frame keeps its current stem
(`face_<sec>s_frame<idx>.jpg`, `cmd/osint/main.go:215`); the crop is the new sibling file above.

### 3c. forensic.db schema change (exact)

In `internal/beckydb/cluster.go`:

1. `clusterSchema` — add to the `appearance_embeddings` CREATE:
   `crop_path TEXT,  -- path to the tight face-crop artifact for this appearance ("" for voice)`
2. `AppearanceRow` — add `CropPath string \`json:"crop_path"\``.
3. `UpsertAppearance` — add `crop_path` to the column list + a `nullableStr(a.CropPath)` value.
4. `ListAppearances` — add `COALESCE(crop_path,'') AS crop_path` to the SELECT.
5. `EnsureClusterSchema` — after the CREATEs, run the guarded
   `ALTER TABLE appearance_embeddings ADD COLUMN crop_path TEXT` (swallow only the
   duplicate-column error) so pre-existing DBs migrate.

No change to `schema.sql`, `EnsureSchema`, `segments`, `segments_vec`, FTS, embed, or search.

---

## 4. Deterministic / offline / degrade-never-crash

- **Deterministic.** Given a frame image, a bbox, and a margin, the crop rect and the cropped
  pixels are a pure function (no randomness, no model in `internal/facecrop`). The DB row is
  deterministic (fixed `appearance_id`, INSERT OR REPLACE). Crop filenames are deterministic.
- **Offline.** `internal/facecrop` is stdlib-only (no network, no Python, no GPU). The DB write
  is the existing driver-free sqlite3 CLI path (`internal/beckydb`). The only model in the chain
  — SCRFD/ArcFace — is the local InsightFace helper `becky-identify` already runs.
- **Degrade, never crash (the explicit no-bbox path):**
  - **No bbox available** (face not found, `Face.Found == false`, empty `Face.BBox`, or a
    degenerate/zero-area rect): **do not** write a crop or a DB row for that frame; **keep the
    current behavior** (the full frame and the existing match output are untouched) and record a
    per-frame skip note. `geom.CropRect` returns the empty `image.Rectangle` for a degenerate
    bbox, and the caller treats an empty rect as "skip this crop" — no panic.
  - **No DB / no `--db` with a store flag:** error out at flag-parse with a clear message (a
    store request with nowhere to store is a user error, not a silent no-op).
  - **No face model configured** (`cfg.FaceModelRoot == ""`, the exact guard at
    `cmd/identify/face.go:46` and `becky-osint`): for becky-osint `--face-crop`, skip cropping,
    keep the full-scene frame, add a note, exit 0. becky-identify already returns early when the
    KB has no faces — `--store-faces` simply has nothing to store and notes so.
  - **Crop encode / write failure:** best-effort — note the failure (like the audio-snippet
    best-effort at `cmd/osint/main.go:255`), keep the full frame, do not abort the run.
  - **Never modify the source video; never overwrite the full-scene frame** (the crop is always
    a new file in `--face-crop-dir`).

---

## 5. Cloud-vs-local split (the testable seam)

The seam is drawn so the **geometry math and the DB write/read are 100% cloud-buildable and
cloud-testable** with no hardware, while the parts that genuinely need Jordan's machine are
isolated behind the existing `faceembed`/sqlite3 boundaries.

| Cloud (here) — pure-Go, no GPU/ffmpeg/model | Local (Jordan's Win10 + RTX 3070) |
|---|---|
| `internal/facecrop` (`CropRect` clamp math, `CropImage`, `SaveCrop`, `AppearanceFromFace`) + full unit tests | Run real SCRFD/ArcFace via the existing `faceembed.Embed` (InsightFace, GPU) |
| `appearance_embeddings.crop_path` schema add + `UpsertAppearance`/`ListAppearances`/migration | `build-all-tools.bat`; run `becky-identify --store-faces` on a real clip |
| Round-trip DB test (write an AppearanceRow incl. `crop_path` → read it back, assert equality) — using the same sqlite3-CLI path beckydb tests already use | Verify crops in `--face-crop-dir` are tight on real talking-head footage; verify rows in the DB; run `becky-cluster` over the populated table |
| Wiring in `cmd/identify/face.go` / `cmd/osint` (compiles; logic unit-tested with a fake `faceembed` result) | Confirm enroll from a cluster's crops improves identify recall on the corpus |

The cloud agent can build and test **everything except the actual SCRFD detection + ArcFace
embedding** — because `internal/facecrop` takes a `faceembed.Face` (a plain struct with `BBox`,
`Vector`, `DetScore`) as input, the geometry + DB layer is exercised end-to-end with a
**synthetic `faceembed.Face`** (a known bbox + a known 512-float vector) and a real decoded test
image, asserting the crop rect, the cropped pixels' bounds, and the DB round-trip — no model
needed. The local agent only confirms the real model feeds that struct correctly on real
footage.

> **PROVABLE HANDOFF (per CLAUDE.md §2/§4):** the cloud half ships a one-command offline proof
> it has RUN — a `facecrop` self-test / unit-test run that (a) crops a checked-in test image to
> a known bbox+margin and prints the output rect + crop file dimensions (measurable), and (b)
> writes one synthetic AppearanceRow to a temp DB and reads it back, printing the row. Plus an
> ordered checkboxed work order (the §6 build plan) the local agent drives to completion. "It
> compiles" is not the proof.

---

## 6. Build plan (checkboxed) + unit tests

### Phase 1 — geometry + DB (cloud-buildable, no hardware)

- [ ] **`internal/facecrop/geom.go`** — `CropRect(bbox [4]float64, margin float64, frameW, frameH int) image.Rectangle`.
      Expand by `margin*max(bboxW,bboxH)` per side; clamp to `[0,W]×[0,H]`; return the empty
      rect for a zero/degenerate/NaN bbox or non-positive frame dims.
- [ ] **`internal/facecrop/crop.go`** — `CropImage(src image.Image, r image.Rectangle) image.Image`
      (copy sub-rect into a fresh `*image.RGBA`) + `SaveCrop(img, outPath, format, jpegQ) error`
      (stdlib `image/jpeg`/`image/png`; `MkdirAll` the dir).
- [ ] **`internal/facecrop/appear.go`** — `AppearanceFromFace(srcFile, srcSHA string, ts float64, frameIndex int, f faceembed.Face, cropPath string) beckydb.AppearanceRow`
      (deterministic `appearance_id = sha12(srcFile)+":face:"+frameIndex`, `modality="face"`,
      `vector_json` = JSON of `f.Vector`, `dim=len(f.Vector)`, `det_score=f.DetScore`).
- [ ] **`internal/beckydb/cluster.go`** — add `crop_path TEXT` to `clusterSchema`; add
      `CropPath` to `AppearanceRow`; thread it through `UpsertAppearance` + `ListAppearances`;
      add the guarded `ALTER TABLE … ADD COLUMN crop_path` migration in `EnsureClusterSchema`.
- [ ] `go build ./... && go vet ./... && go test ./... && gofmt -l .` all green.

### Phase 2 — producers (compiles cloud; runs local)

- [ ] **`cmd/identify/face.go`** — when `--store-faces`: for each `recs[i]` with `f.Found`,
      decode the already-saved frame `frames[i]`, `CropRect`+`CropImage`+`SaveCrop` to
      `--face-crop-dir`, build the row via `AppearanceFromFace`, `db.EnsureClusterSchema()` once
      then `db.UpsertAppearance(row)`. Match logic untouched; skips degenerate bboxes with a note.
- [ ] **`cmd/identify/main.go`** — add `--store-faces`, `--db`, `--face-crop-dir`,
      `--face-margin`, `--face-crop-format`; flag validation (store ⇒ db required); surface a
      `notes` count of crops stored / skipped.
- [ ] **`cmd/osint`** (`main.go`/`osint.go`) — add `--face-crop` + `--db` + `--face-margin`: for
      a `multi_face` event, `faceembed.Embed` the extracted frame, crop the prominent face, save
      the sibling crop, write the DB row; degrade to full-scene + note when no model.
- [ ] `build-all-tools.bat` (auto-discovers; no edit needed) — local step.
- [ ] **Local:** run on a real talking-head clip; eyeball crop tightness; confirm DB rows;
      run `becky-cluster` over the populated `appearance_embeddings`.

### Unit tests (cloud, assert VALUES not truthiness — STANDARDS-ENGINEERING)

- [ ] `geom_test.go` — table-driven `CropRect`:
  - centered bbox, margin 0.4 → exact expanded rect (assert all four coords).
  - bbox flush against left edge → left clamped to 0, right keeps full margin (assert the
    asymmetric clamp).
  - bbox at the bottom-right corner → clamped to `W`/`H` (assert no overflow).
  - margin 0 → rect == bbox (rounded).
  - degenerate bbox (`x2<=x1`), zero frame dims, NaN → empty `image.Rectangle{}` (skip signal).
  - non-square bbox → margin uses `max(w,h)` (assert the larger-side basis).
- [ ] `crop_test.go` — `CropImage` on a synthetic gradient image returns an image whose bounds
  equal the requested rect's size and whose corner pixels match the source at the mapped
  coordinates (assert pixel values). `SaveCrop` round-trips (write → `image.Decode` → bounds).
- [ ] `appear_test.go` — `AppearanceFromFace` produces the expected deterministic `appearance_id`,
  `dim`, `det_score`, and a `vector_json` that JSON-parses back to the input vector (assert exact
  values).
- [ ] `cluster_test.go` (extend existing) — **DB round-trip:** `EnsureClusterSchema` →
  `UpsertAppearance` an `AppearanceRow` with a non-empty `crop_path` and a known vector →
  `ListAppearances("face")` returns exactly that row with `crop_path`, `vector_json`, `det_score`,
  `frame_index` byte-for-byte equal (regression test for the schema change). A second
  `UpsertAppearance` with the same `appearance_id` REPLACES (count stays 1).

Every fixed bug ships a regression test; tests assert values, not just non-nil.

---

## 7. Open Decisions for Jordan

1. **Default margin.** `--face-margin 0.4` (40% of the face's larger side) is the proposed
   default — generous enough to keep hairline/chin/ears that help ArcFace, tight enough to drop
   the torso/room. Acceptable, or do you want tighter (`0.25`, just the face) or looser (`0.6`,
   a head-and-shoulders portrait for nicer review thumbnails)? This is the one number most worth
   eyeballing on real footage.
2. **Dedup of near-identical crops.** A 1-fps sample of a talking head yields many almost-identical
   face crops of the same person seconds apart — bloating disk and the DB. Options, in order of
   effort: (a) **none** — store every detected face (simplest; cluster cohesion handles redundancy
   downstream); (b) **per-clip perceptual-hash dedup** — skip a crop whose aHash
   (`osintexport.AHashFromImage`, already in the tree) is within a Hamming threshold of the
   previous stored crop from the same clip+person (cheap, deterministic); (c) **embedding-cosine
   dedup** — skip a crop whose ArcFace vector is ≥ ~0.95 cosine to the last stored one (most
   accurate, slightly more compute). Recommendation: ship (a) for Phase 1, add (b) as a
   `--dedup-phash <hamming>` flag if disk/DB growth bites. Which do you want?
3. **Should `--store-faces` ever be ON by default in the pipeline?** Storing appearances is what
   makes `becky-cluster` work, so the pipeline's `identify` step arguably *should* store by
   default. Default-on (storage is the durable path, SPEC-PERSON-CLUSTERING §7.1) or keep it
   opt-in to avoid surprising disk use on a 500 GB corpus?
4. **Crop location.** Proposed `<db-dir>/face-crops/` (next to the DB, central, easy for
   `becky-cluster` to reference). Alternative: next to each source clip (matches the becky-clip
   "outputs live next to the originals" protocol). Central is cleaner for a corpus-level DB;
   confirm the preference.
