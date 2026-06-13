# SPEC-PERSON-CLUSTERING.md — Unknown-person clustering across the corpus

> **SPEC — NOT BUILT, AWAITING JORDAN'S APPROVAL.**
> Research + design only. No Go code has been written. No new binary exists. Nothing
> in `becky-go/` has been changed. Jordan approves before any build starts.
>
> Authored 2026-06-07. External algorithms/libraries below were verified against current
> (2026-06-07) practice with sources cited. The face/voice models themselves are the ones
> becky ALREADY runs (InsightFace buffalo_l, sherpa-onnx CAM++) — re-verified current.

---

## 1. Goal (the corpus's real "who" question)

The KB enrolls John Clancy + Hair Jordan (faces) and John/Shelby/Jordan (voices). But per
`TEST-FEEDBACK.md` rec #6, the corpus's actual unknowns are the **unenrolled TX
associates** — Braxton, Preston/"Tim Tam", VBun, Trevor — for whom there are **zero
prints**. `becky-identify` can only ever say "unidentified face/voice" for them.

**This feature turns "unidentified" into "Person A, appears in 41 clips."** It clusters
the face (and voice) embeddings becky already produces **before any name exists**, so a
human names a recurring stranger **once** and that name back-fills every clip in the
cluster. That converts 500 GB from "search for the 3 people we enrolled" into "here are
the 6 recurring strangers and where each shows up."

This is **candidate-not-conclusion** (FORENSIC-OUTPUT-PHILOSOPHY): a cluster is "these
faces are probably the same person — a human confirms," never an assertion of identity.

---

## 2. HARD PREREQUISITE: the face-rotation fix (F1) must land first

`TEST-FEEDBACK.md` F1 (CRITICAL): becky samples frames WITHOUT applying the container
display-rotation, so portrait phone video (most of the corpus) is fed to the detector
**90° rotated** → `dim=0`, faces silently dropped. Clustering on a corpus where most
faces never embed would produce **tiny, wrong, mostly-empty clusters** — worse than
nothing, because it would imply a stranger appears rarely when they appear constantly.

**Therefore: do not build face clustering until F1 is fixed** (rotation applied in the
shared `internal/osintexport.ExtractFrame` / `cmd/identify/face.go` sampling path, plus
denser sampling than 3 frames/3 s on short clips). Voice clustering does NOT depend on
F1 and can ship first (see §6 phasing). This dependency is stated as a gate, not a note.

---

## 3. What becky already produces (the raw material)

We are clustering embeddings that ALREADY EXIST — no new model needed:

- **Face:** `internal/faceembed` → InsightFace `buffalo_l` (SCRFD + w600k_r50 ArcFace),
  **512-d, L2-normalized** (`normed_embedding`), so **cosine similarity == dot product**.
  `cmd/identify/face.go` already samples frames, embeds the prominent face, and computes
  cosine in Go (`vec.go`). The calibrated same-person vs different-person separation is
  already encoded in the codebase: face-threshold default **0.40**, comment notes
  "same-person ~0.60 vs different-person ~0.32." InsightFace verified current: **v1.0
  released 2026-05-23**, buffalo_l (w600k_r50) is still the recommended server default;
  library is MIT, model packs are non-commercial research license (already becky's status
  quo). [InsightFace model zoo](https://github.com/deepinsight/insightface/blob/master/model_zoo/README.md)
  [InsightFace recognition-model guide](https://www.insightface.ai/guides/choose-face-recognition-model-and-evaluate)
- **Voice:** `internal/pyhelpers/voice_embed.py` → sherpa-onnx CAM++
  (`3dspeaker_speech_campplus_sv_en_voxceleb_16k.onnx`), **192-d**, VAD-gated to speech,
  cosine matched in Go. Voice-threshold default **0.45**, comment notes "same-person
  ~0.84 vs different-person ~0.03" — an enormous, reliable margin. SKILL.md: **"Voice is
  more reliable than face."**
- **Per-appearance provenance** comes free: each embedding ties back to `source_file`,
  `timestamp`/`frame_index`, `source_sha256` (the same fields `becky-osint` and
  `becky-identify` already emit).

So clustering is a **pure post-processing layer over existing outputs** — the cheapest
possible way to add the highest-value "who" capability.

---

## 4. Algorithm + thresholds (verified current practice)

### 4a. Face clustering — Chinese Whispers (primary), DBSCAN/agglomerative (alternates)

Current (2026) practice for clustering ArcFace/InsightFace embeddings with an **unknown
number of identities**:

- **Graph-based Chinese Whispers** is repeatedly reported as the best performer for
  InsightFace embeddings, outperforming DBSCAN/HDBSCAN in comparative tests; it partitions
  by having each node inherit the strongest label among its neighbors, and naturally
  discovers the cluster count. [Face clustering comparison (DBSCAN/HDBSCAN/Chinese Whispers)](https://medium.com/pythons-gurus/clustering-faces-with-python-aef799514cd8)
  [Chinese Whispers / threshold discussion](https://ar5iv.labs.arxiv.org/html/2106.04112)
- **DBSCAN with cosine distance** (the classic dlib/PyImageSearch recipe) and
  **agglomerative (single/average linkage)** are solid, well-understood alternates;
  HDBSCAN handles varying density but several reports found Chinese Whispers cleaner on
  InsightFace vectors specifically. [Face clustering with Python (DBSCAN)](https://pyimagesearch.com/2018/07/09/face-clustering-with-python/)

**Threshold — anchor on becky's OWN calibration, not generic web numbers.** The codebase
already measured this corpus's models: same-person face cosine ~0.60, different-person
~0.32, with 0.40 chosen recall-first for *matching*. For **clustering** (where a false
merge of two different strangers is costly), use a **stricter same-person edge threshold
than the matching threshold** — start around **cosine ≥ 0.50** to draw a graph edge, then
tune empirically on a labeled sample of the corpus (grid-search step 0.05, the documented
method). Clustering should be **precision-leaning**: better to split one person into two
clusters a human merges, than to merge two strangers a human can't unmerge. This
deliberately differs from `becky-identify`'s recall-first matching threshold, and the
spec makes that asymmetry explicit.

### 4b. Voice clustering — agglomerative, cosine, threshold ~0.5+

Cross-file speaker clustering is well-established: each speaker turn → CAM++ embedding →
**agglomerative clustering with a cosine-similarity stop threshold** (merge highest-
similarity pair until none exceeds the threshold; the *threshold*, not a target count,
decides where to stop). Documented threshold ~0.5; CAM++ is a recognized robust embedding
for this. [Cross-recording diarization with CAM++/agglomerative, OpenWhispr](https://openwhispr.com/blog/local-speaker-diarization)
Given becky's measured voice margin (same ~0.84 vs different ~0.03), voice clustering will
be **very clean** — set the same-speaker threshold high (e.g. cosine ≥ 0.65–0.70) and
expect tight, trustworthy clusters. Voice is the more reliable modality to lead with.

### 4c. Implementation note
Keep the **matching math in Go** where becky already does it (deterministic, testable),
OR run the clustering step in a small embedded Python helper (scikit-learn
AgglomerativeClustering / a Chinese-Whispers implementation) via the existing
`pyhelpers.Materialize` pattern — clustering is a batch, offline, one-shot op so either is
fine. Recommendation: Python helper for the graph/clustering algorithm (mature libs), Go
owns orchestration, I/O, provenance, and the deterministic cosine pre-filter — the same
split as faceembed/voiceembed.

---

## 5. Proposed tool: `becky-cluster`

```
becky-cluster --embeddings <dir-or-db> [options]

Inputs (any/all):
  --identify-glob "<dir>/**/identify.json"   harvest unidentified[] faces/voices + provenance
  --db <forensic.db>                          read stored embeddings (see §7 storage)
  --modality face|voice|both   default: both
Options:
  --face-edge 0.50      cosine threshold to link two faces as same-person (clustering)
  --voice-edge 0.65     cosine threshold to link two voices as same-speaker
  --min-cluster 2       smallest cluster to report (a stranger seen in ≥2 clips)
  --kb <dir>            cross-check clusters against the enrolled KB (label known ones)
  --output <file>       clusters JSON here instead of stdout
  --verbose
```

Behavior: JSON-in/JSON-out, exit-coded, offline, graceful-degrade, never modifies
sources. Heavy clustering in the embedded helper; provenance + cosine in Go.

### Output JSON (proposed; synthetic values)
```json
{
  "tool": "becky-cluster v1.0.0",
  "modality": "both",
  "face_edge": 0.50,
  "voice_edge": 0.65,
  "clusters": [
    {
      "cluster_id": "face-A",
      "modality": "face",
      "suggested_name": null,                 // null until a human names it once
      "member_count": 41,                      // "Person A appears in 41 clips"
      "distinct_source_files": 41,
      "representative_frame": "osint-export/clip0312/scene_5s_frame150.jpg",
      "cohesion": 0.71,                        // mean intra-cluster cosine (quality signal)
      "members": [
        {"source_file": "20250703_2031.mp4", "timestamp": 5.0,
         "frame_index": 150, "source_sha256": "ab12cd34…", "confidence": 0.68},
        {"source_file": "20250704_0915.mp4", "timestamp": 12.3,
         "frame_index": 369, "source_sha256": "cd34ef56…", "confidence": 0.63}
      ],
      "kb_crosscheck": {"best_known": "John Clancy", "cosine": 0.21, "is_known": false}
    },
    {
      "cluster_id": "voice-B", "modality": "voice", "suggested_name": null,
      "member_count": 12, "distinct_source_files": 9, "cohesion": 0.81,
      "representative_clip": "ingest-out/clip0207/speaker_SPEAKER_01.wav",
      "members": [ /* … */ ],
      "kb_crosscheck": {"best_known": "Shelby", "cosine": 0.07, "is_known": false}
    }
  ],
  "singletons": 18,                            // seen once (not yet a "recurring" person)
  "notes": {
    "honesty": "clusters are candidate same-person groupings for human confirmation; not identity conclusions",
    "prereq": "face clustering requires the F1 rotation fix; verify rotation_applied on inputs"
  }
}
```

Naming: `suggested_name` stays `null`. When a human looks at `representative_frame`/clip
and decides "that's Braxton," they name the cluster **once** (see §7), and it propagates.

---

## 6. Phasing

- **Phase 1 — voice clustering.** No F1 dependency, cleanest margins, "Voice is more
  reliable than face." Immediate value: recurring unknown speakers across the corpus.
- **Phase 2 — face clustering.** ONLY after F1 (rotation) lands. Gated, as in §2.
- **Phase 3 — cross-modal fusion.** When a face cluster and a voice cluster co-occur in
  the same clips/timespans, propose they are the same person (a stranger seen AND heard).
  Higher value, more complex; defer until both single-modality paths are trusted.

---

## 7. Integration with `becky-identify` and the KB

The existing schema and tools already have the right hooks:

1. **Storage of embeddings (small new addition).** Today embeddings are computed and
   thrown away after matching. To cluster across the corpus, persist them once:
   a new table, e.g. `appearance_embeddings(appearance_id PK, source_file, source_sha256,
   modality, vector_json, timestamp, frame_index, det_score, created_at)` — deterministic
   `appearance_id = sha12(source_file)+":"+modality+":"+frame_index`, `created_at` RFC3339
   to match `schema.sql`. Written by `becky-identify` (or a tiny `--store-embeddings`
   flag) and the pipeline. Additive table → no migration risk, embed/search unaffected
   (same discipline as the existing `media_meta`/`live_chat` additions).
   *(Alternatively, harvest from existing `identify.json` files via `--identify-glob` with
   no schema change — viable for a first cut, but storing vectors is the durable path.)*

2. **KB cross-check.** `becky-cluster --kb kb-final` cosine-checks each cluster's centroid
   against enrolled face/voice prints. If a cluster matches a known person above the
   *matching* threshold, label it (`kb_crosscheck.is_known: true`) so the unknown set is
   purely the strangers. This reuses the exact enrolled-embedding + cosine code already in
   `cmd/identify/face.go` / `voice.go`.

3. **Name-once → enroll (the human-in-the-loop close).** When a human names `face-A` =
   "Braxton," the natural action is to **enroll the cluster's best frames/clips as a new
   KB identity** (`kb-final/face-prints/braxton/*.jpg`, `voice-prints/braxton/*.wav`).
   becky already has `becky-enroll`; `becky-cluster` should optionally emit an
   enroll-ready bundle (top-N highest-det-score, most-frontal members) so naming a cluster
   = creating a KB entity in one step. From then on, `becky-identify` recognizes Braxton
   by name corpus-wide — the cluster bootstraps the KB. **Reuse the face-collision guard**
   (the Shelby bug in SKILL.md/TEST-FEEDBACK): when auto-selecting enroll frames from a
   cluster, prefer single-face frames to avoid grabbing a co-present person's face.

4. **Write identifications back (optional).** Once a cluster is named, write
   `identifications` rows (the existing table) for every member with
   `verified_by = "human:cluster-name"`, so `becky-consolidate` coverage instantly
   reflects "Braxton recognized in 41/… videos." This rides the existing
   identifications + propagation machinery rather than inventing a parallel one.

5. **Pipeline + search.** `becky-cluster` is a **corpus-level** step (runs once over many
   clips, not per-clip), so it sits outside the per-video `becky-pipeline` loop — invoked
   after a pipeline pass, like `becky-consolidate`. Cluster membership becomes another
   rank-by-actionability lever ("show clips containing Person A").

---

## 8. Honesty / forensic guardrails
- A cluster is a **[CANDIDATE]** same-person grouping with a basis (cohesion, member
  cosines) — never an identity conclusion. The human confirms via the representative
  frame/clip before any name sticks.
- **Precision-leaning thresholds** (§4a): a wrong *merge* of two different strangers is
  the dangerous error (it would attribute one person's appearances to another). Tune to
  split-rather-than-merge; surface low-`cohesion` clusters for extra human scrutiny.
- Carry `rotation_applied` from inputs into the cluster record so an analyst can see the
  F1 fix was in effect for face clusters (provenance/auditability).
- Reuse the enroll **face-collision guard** so naming a cluster can't silently enroll the
  wrong person's face (the documented Shelby failure mode).

---

## 9. Open items to confirm before building
1. **F1 rotation fix landed** (hard gate for face clustering).
2. Empirically calibrate `--face-edge` / `--voice-edge` on a small hand-labeled corpus
   sample (grid search 0.05 step) — start from becky's measured margins, don't trust
   generic web thresholds.
3. Choose the clustering lib (scikit-learn agglomerative vs. a Chinese-Whispers impl)
   and confirm it's available to the becky Python interpreter (offline).
4. Decide store-embeddings (durable) vs. harvest-from-identify.json (quick first cut).
5. Confirm InsightFace model-pack license stance is acceptable for the case (non-
   commercial research — already becky's status quo, but cluster-then-enroll makes it
   more central).

---

## Sources (verified 2026-06-07)
- [Face clustering: DBSCAN / HDBSCAN / Chinese Whispers comparison](https://medium.com/pythons-gurus/clustering-faces-with-python-aef799514cd8)
- [Chinese Whispers + ArcFace threshold discussion](https://ar5iv.labs.arxiv.org/html/2106.04112)
- [Face clustering with Python (DBSCAN recipe)](https://pyimagesearch.com/2018/07/09/face-clustering-with-python/)
- [InsightFace model zoo — buffalo_l / w600k_r50 default; licensing](https://github.com/deepinsight/insightface/blob/master/model_zoo/README.md)
- [InsightFace recognition-model & threshold guide](https://www.insightface.ai/guides/choose-face-recognition-model-and-evaluate)
- [Cross-recording speaker clustering with CAM++ + agglomerative (~0.5)](https://openwhispr.com/blog/local-speaker-diarization)
