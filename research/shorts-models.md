# Short-form repurposing — model research: what the field uses vs what becky has

**Status: v1, 2026-08-15 (cloud).** Written because Jordan is repurposing long videos into
social shorts, has tried "virtually every AI tool" and found the quality subpar, and asked
whether the models those OSS projects use are things becky already has — and if not, whether
they beat becky's alternative.

**Conclusion up front:** becky already has the *better* model at three of the stages that
matter, and **nothing at all** at five others. The five gaps are not exotic — they are the
entire reframing half of the problem. But model choice is **not** why becky's vision is weak
today; see §6, which is the real finding of this document.

---

## 1. The class question (what this problem actually is)

Short-form repurposing is not one model, it is a **six-stage chain**:

    transcribe → find the moment → verify the moment → find the subject →
    decide who is speaking → reframe + render

Each stage consumes the previous stage's output. That structure matters more than any single
model: a chain of six 90%-accurate stages is ~53% accurate end to end. The field's tools are
mostly strong at stage 5 (active speaker) and weak everywhere else. becky is the inverse —
strong at 1–3, absent at 4–6.

---

## 2. Every model Jordan named, vs becky

| Their model | Stage | becky's counterpart | Verdict |
|---|---|---|---|
| **YOLOv8** (person detection) | subject | *nothing* | **They win.** No person detector in becky. Ultralytics is **AGPL-3.0** — license-hostile for us. |
| **MediaPipe FaceMesh** + MAR variance | speaking | *nothing* | **Mixed** — the library is excellent, the *technique* is obsolete. See §4. |
| **GroundingDINO** (open-vocab detection) | subject | **Falcon-Perception 0.6B** (`becky-perceive`) | **becky wins, measurably.** §3.2 |
| **BoostTrack** (MOT) | subject | *nothing* | **They win.** becky has no tracker of any kind. |
| **UltraFace** (1MB face detector) | subject | **SCRFD-10GF** (InsightFace `buffalo_l`) | **becky wins, by a wide margin.** §3.1 |
| **LR-ASD** (audio-visual ASD, ONNX) | speaking | *nothing* | **They win.** No lip-sync model in becky. §5 |
| scipy interpolation (centering) | reframe | *nothing* | Not a model. ~50 lines of deterministic Go. |
| "dramatic motion" as crop metric | speaking | `becky-motion` | Not a model. Ours is better engineered; **the technique is wrong** (§4.3). |
| unnamed LLM (hook/build/payoff) | moment | Qwen3.5-4B, Gemma-4 E4B/12B, Claude (OAuth) | Comparable. Ours is free at point of use. |
| Berger & Milkman (JMR 2012) rubric | moment | *nothing* | A paper, not a model. Not implemented. |
| Kayal et al. (ACL 2025) vision pass | moment | Gemma-4 could serve it | **Training-free** — needs no new model. §3.4 |

**Two stages Jordan didn't name, where becky is also ahead:** transcription (the field defaults
to Whisper; becky runs Parakeet — §3.3) and speaker identity (the field has none at all — §3.5).

---

## 3. The head-to-heads, with numbers

### 3.1 Face detection — the largest single gap, in becky's favour

WIDER FACE validation mAP:

| Detector | Easy | Medium | **Hard** |
|---|---|---|---|
| UltraFace version-slim @320×240 | 77.0% | 67.1% | **39.5%** |
| UltraFace version-RFB @320×240 | 78.7% | 69.8% | **43.8%** |
| UltraFace version-RFB @640×480 | 85.5% | 82.2% | **57.9%** |
| SCRFD-2.5GF | 93.78% | 92.16% | **77.87%** |
| **SCRFD-10GF — what becky runs** | **95.16%** | **93.87%** | **83.05%** |

The **Hard** subset is defined by small, occluded, profile and low-contrast faces. That is
precisely the second person in a two-shot, someone angled away from camera, or a participant
seated further back. becky's detector is **25–39 points ahead there**.

This is not academic. In a shorts pipeline the detector feeds the active-speaker model. When
the detector drops a face, the ASD model has no candidate to score, and the crop falls back to
whoever is left in frame. A detector at 43.8% Hard AP loses the second speaker roughly half the
time in exactly the shots where a cut between speakers is called for. **This is a plausible
primary cause of the jank Jordan has been seeing in off-the-shelf tools.**

### 3.2 Open-vocabulary detection — becky wins

`becky-perceive` wraps **Falcon-Perception** (TII, 0.6B params, early-fusion transformer,
ONNX/CPU, zero VRAM). On the SA-Co open-vocabulary benchmark it scores **68.0 Macro-F1 vs
62.3 for SAM 3**, with +8.2 on attribute-heavy splits, and **+21.9 points on spatial
understanding** (PBench Level 3). SAM 3 is a generation *newer* than GroundingDINO, so becky is
ahead of GroundingDINO's class while running at zero VRAM.

Caveat that keeps this honest: **open-vocab detection is the wrong tool for reframing.** You
need "person" and "face", both of which have cheap dedicated detectors. Falcon-Perception's
~9s per-process model load (`cmd/becky-perceive/main.go`) makes it unusable per-frame. It wins
the comparison and still should not be in this pipeline's hot path.

### 3.3 Transcription — becky wins, and the *architecture* matters more than the score

| | Avg WER (8 English benchmarks) | Languages | Behaviour in silence |
|---|---|---|---|
| Whisper large-v3 (the field default) | ~7.4% | 99 | **Can invent text** |
| **Parakeet-TDT-0.6B-v3 (becky)** | **6.34%** | 25 | **Stays silent** |

The 1.1-point WER gap is real but modest. The architectural difference is the one that decides
output quality here: Parakeet is a **transducer**, so it emits nothing during silence. Whisper's
encoder-decoder design hallucinates text into pauses. In a pipeline where an LLM then reads the
transcript to pick "viral moments", a hallucinated line inside a silent gap becomes a **fake
moment that gets confidently scored and clipped**. becky's architecture cannot make that error.
Parakeet also runs ~3,332× real time.

Known becky-side caveat carried from `STATE-OF-MASTER.md`: 49% of Parakeet's words have
`end == start`, which broke naive pause-detection once — `becky-subtitle` now derives its gap
threshold from the transcript's own p90 rather than a fixed constant. Any new consumer of
Parakeet timings must do the same.

### 3.4 Virality scoring — the citations are real, the method is weak alone

Kayal, Mettes, Dehmamy & Park, *Large Language Models Are Natural Video Popularity Predictors*,
Findings of ACL 2025 — a **training-free** framework using modality-aligned VLMs and LLMs. Being
training-free means becky could implement it with models already on disk (Gemma-4 for the vision
pass, Qwen3.5-4B for the text rubric). No new model, no new weights.

Honest limitation: a 0–99 score from one VLM pass over sampled frames is, in becky's own terms, a
**lone weak signal**. Under `FORENSIC-OUTPUT-PHILOSOPHY.md` that is a candidate, not a
conclusion. The two-pass structure the field uses (text rubric + an *independent* vision pass) is
actually well aligned with becky's ≥2-signals rule — that part is sound design and should be kept.

### 3.5 Speaker identity — a capability the field simply lacks

becky has **ArcFace w600k_r50** (512-d face embeddings), **pyannote-segmentation-3.0** +
**CAM++** (0.65% EER on VoxCeleb1-O; pyannote 3.1 ≈ 11% DER on AMI), and `becky-identify` to
attach names.

UltraFace returns a box. It cannot tell you the box in frame 400 is the same person as frame
100, let alone who they are. This is *why* the field's crops "cut between speakers like a camera
switch" — with no persistent identity, the tool re-decides from scratch on every frame and
smooths the jitter afterwards. becky can anchor a crop to a *person*, not a box. **That is the
genuine architectural advantage, and it is bigger than any single benchmark on this page.**

---

## 4. The MediaPipe / OpenCV correction (Jordan was right, v0 of this analysis was wrong)

An earlier pass of this research dismissed "MediaPipe FaceMesh" alongside the MAR technique.
That conflated a **bad technique** with an **excellent library**, and it is worth correcting in
writing so it is not repeated.

### 4.1 MAR variance is genuinely obsolete — that part stands

Mouth-aspect-ratio variance is **vision-only**: it never consults the audio. Chewing, laughing,
yawning, and an animated listener all register as speech. LR-ASD (§5) exists precisely because
this does not work. Do not build MAR.

### 4.2 MediaPipe itself is a different proposition entirely

MediaPipe Tasks (actively maintained by Google through 2026) ships far more than a face mesh:

- **Face Landmarker** — 478 3D landmarks, **52 ARKit-style blendshape coefficients**
  (`jawOpen`, `mouthClose`, `mouthPucker`, …), plus a facial transformation matrix.
- **Pose Landmarker** — 33 body landmarks in image *and* 3D world coordinates.
- **Image / Interactive Segmenter** — subject isolation.
- Hand Landmarker, Face Detector (BlazeFace), Object Detector.

Two of these are directly load-bearing for shorts:

1. **Blendshapes beat hand-rolled MAR outright.** `jawOpen` is a trained coefficient, not a
   ratio of two landmark distances. As an *input feature* to an audio-visual decision it is
   strictly better than MAR. The error the field makes is using it *alone*, without audio — not
   using MediaPipe.
2. **Pose Landmarker solves a problem becky cannot currently solve at all.** A 9:16 crop framed
   only on a face looks wrong — it decapitates gestures and puts the head dead-centre. Framing
   on shoulders/torso is what makes a crop look edited rather than auto-generated. becky has no
   body-level signal whatsoever.

### 4.3 OpenCV is the dependency becky has spent a year routing around

Nothing in becky uses OpenCV. `gocv` (Apache-2.0, tracks OpenCV 4.12, Windows-supported,
actively maintained as of Jan 2026) has been proposed repeatedly and never built — always
deferred as "opt-in, left for local" (`SPEC-FRAMEMATCH-HARDENING.md`). What that costs:

| OpenCV capability | What becky does instead | Cost |
|---|---|---|
| ORB + RANSAC homography | pure-Go census descriptor | `framematch/decor.go:35` — *"weaker than ORB+RANSAC"*, self-documented |
| Farnebäck / DIS optical flow | ffmpeg dense frame-difference | `motion/motion.go:16` — the **degrade path**, self-documented |
| Camera-path smoothing / stabilization | *nothing* | The standard estimate→smooth→warp pipeline is exactly what a crop path needs |
| CSRT/KCF trackers, background subtraction | *nothing* | No tracking at all |

Jordan's read — that MediaPipe and OpenCV are powerhouses with a lot of untapped potential for
video, and that vision has been the weak part of becky — is **correct and is supported by
becky's own source comments**. See §6.

---

## 5. LR-ASD — the one new model worth adopting

| | |
|---|---|
| Lineage | Light-ASD (CVPR 2023) → **LR-ASD (Springer IJCV 2025)** |
| Size | **1.0M params** vs 22.5M for the SOTA it matches (~23× smaller) |
| Compute | **0.6G FLOPs** vs 2.6G (~4× fewer) |
| Accuracy | **94.45% mAP**, AVA-ActiveSpeaker validation |
| Runtime | ONNX; CPU-real-time — same zero-VRAM class as `becky-motion` |

It fits becky philosophically, not just technically: it decides "is this face speaking" by
corroborating **two independent modalities** — lip motion against the actual soundtrack. That is
becky's corroborate-then-conclude invariant expressed as a model.

**Caveat from the current literature (do not skip):** models near-saturated on AVA do *not*
saturate on in-the-wild footage. The UniTalk benchmark (arXiv 2505.21954) was built to show this.
GateFusion is the present accuracy ceiling (77.8% Ego4D-ASD, 86.1% UniTalk, 96.1% WASD) but is
substantially heavier. Correct call under `CLAUDE.md`'s research-a-class rule: **LR-ASD is the
right class** (tiny + robust + ONNX), with GateFusion named as the escalation if LR-ASD
underperforms on Jordan's real footage.

**becky would stack it better than the field does.** These tools run ASD alone. becky can run
LR-ASD *and* `becky-diarize` (voice turns) *and* ArcFace (face identity) — three independent
signals into one decision, which is the ≥2-signal rule applied to speaker selection.

---

## 6. The systemic finding (the real output of this research)

While checking whether becky had counterparts, a pattern appeared in becky's own source
comments that matters more than any model on this page:

- `cmd/events/main.go:14` — *"multi_face — OPTIONAL. **No face detector ships in this
  environment**, so it is skipped gracefully"* — while `internal/faceembed` (SCRFD + ArcFace)
  exists and is used by `becky-identify`.
- `cmd/framematch/decor.go:35` — *"It is **weaker than ORB+RANSAC** but offline, deterministic"*.
- `cmd/motion/motion.go:16` — *"No OpenCV/optical-flow dependency is required … **this IS that
  path**"* — i.e. the documented *degrade* branch is the only branch built.
- `cmd/identify/face.go` — samples 1 fps, caps at 60 frames, and keeps only the **most prominent
  face per frame**. No identity across time.

**becky's vision layer is largely the degrade path of a stack that was never fully built.** And
the reason is visible in `SPEC-FRAMEMATCH-HARDENING.md`: the heavy CV option was rejected because
*"cgo + native OpenCV cannot be built or tested [by the cloud agent]"*.

That is an architecture shaped by **what the cloud agent could compile**, not by what the job
needed. Every time the choice arose, the cloud-buildable pure-Go option won and the stronger
option was deferred to "local, opt-in" — where it was never built. Repeated across a year, that
is the whole explanation for "vision was a total flop on the forensic footage." It was never a
model-selection failure.

**The fix is a process rule, not a model swap:** when a stage's correct implementation needs a
native dependency, the cloud agent's job is to **spec it and hand it over as a checkboxed work
order**, not to ship the weaker version as the default and mark the branch done.

---

## 7. Recommendation

**Adopt (new):**
1. **LR-ASD** (ONNX) — the speaking decision. Only genuinely new model needed.
2. **MediaPipe Pose Landmarker** — body-aware framing. becky has no body signal today.
3. **MediaPipe Face Landmarker blendshapes** — as a *feature* into the ASD fusion, never alone.
4. **gocv / OpenCV** (Apache-2.0) — optical flow + camera-path smoothing for the crop path, and
   it retroactively upgrades `framematch` and `motion` off their degrade branches.

**Keep becky's (they are better):** SCRFD-10GF + ArcFace (faces), Parakeet-TDT-v3 (ASR),
pyannote-3.0 + CAM++ (diarization), Falcon-Perception (open-vocab, but off the hot path),
Gemma-4 / Qwen3.5 / Claude-via-OAuth (reasoning).

**Do not build:** MAR-variance speaking detection; motion-magnitude as the speaker selector
(`CLAUDE.md` already forbids treating a motion burst as presence); YOLOv8 (AGPL-3.0 — use
MediaPipe/Pose or a permissive detector for person boxes if one is needed at all).

**License notes:** Ultralytics YOLOv8 is AGPL-3.0. `motcpp`, the C++ library bundling BoostTrack,
is AGPL-3.0 — check upstream BoostTrack's own terms before adopting. gocv is Apache-2.0.
MediaPipe is Apache-2.0. LR-ASD's terms need verifying at adoption time.

**On BoostTrack specifically:** it is genuinely SOTA (first in HOTA on MOT17 among online
trackers; first in HOTA and IDF1 on MOT20 among all trackers) but it is crowd-scale machinery.
For 1–3 talking heads, IoU association + the ArcFace embeddings becky *already computes* is
sufficient and adds no dependency. Revisit BoostTrack only if that proves inadequate on real
footage.

---

## 8. Confidence

**High** — the benchmark numbers, licenses, and model lineages (all verified against primary
sources: papers, model cards, upstream repos), and the §6 finding (quoted directly from becky's
own source).

**Medium** — that LR-ASD specifically holds up on Jordan's footage; the UniTalk result is an
explicit warning that AVA scores overstate in-the-wild performance. Mitigated by naming
GateFusion as the escalation.

**Untested** — the reframing half (§7 items 1-4) has not been built or run. This is model
research, not a proven pipeline.

**Update, 2026-08-15 (same day):** Jordan ratified §7 — *"as for mediapipe and opencv, yes.
absolutely we NEED to use them. weve been cutting corners and all that does is waste my time,
not save time."* The §6 finding is therefore settled policy, not a proposal: the pure-Go
degrade path stops being the default for vision work. Steps 1-2 of the pipeline
(`internal/moment` + `cmd/becky-moment`, and `internal/facetrack`) are now **built and tested**
— see `HANDOFF-SHORTS-PIPELINE.md` §5. Neither needed a new model, which is the point of §2:
five of the six stages were never a model-selection problem.

---

## 9. Lesson logged

Two, both worth keeping:

1. **Do not dismiss a library because a project used it badly.** v0 of this analysis wrote off
   MediaPipe because the projects Jordan found used MAR variance. MediaPipe's blendshapes and
   pose landmarks are load-bearing for this work. Jordan caught this.
2. **"Cloud can't build it" is not a design input.** It is a handoff obligation. §6 is what a
   year of that inversion produces.

---

## Sources

- SCRFD — *Sample and Computation Redistribution for Efficient Face Detection*, arXiv 2105.04714; InsightFace SCRFD model zoo
- UltraFace — github.com/Linzaer/Ultra-Light-Fast-Generic-Face-Detector-1MB (WIDER FACE table in README)
- LR-ASD — github.com/Junhua-Liao/LR-ASD; Springer IJCV 2025; Light-ASD CVPR 2023 (arXiv 2303.04439)
- UniTalk / in-the-wild ASD benchmark — arXiv 2505.21954
- Falcon-Perception — huggingface.co/tiiuae/Falcon-Perception; TII release notes (SA-Co, PBench)
- Open ASR Leaderboard — huggingface.co/blog/open-asr-leaderboard; Parakeet-TDT-0.6B-v3 arXiv 2509.14128
- CAM++ / 3D-Speaker — github.com/modelscope/3D-Speaker; pyannote/segmentation-3.0 + speaker-diarization-3.1 model cards
- BoostTrack++ — arXiv 2408.13003; github.com/vukasin-stanojevic/BoostTrack; motcpp (AGPL-3.0)
- Kayal, Mettes, Dehmamy & Park — *LLMs Are Natural Video Popularity Predictors*, Findings of ACL 2025
- Berger & Milkman — *What Makes Online Content Viral?*, JMR 2012
- MediaPipe Tasks — ai.google.dev/edge/mediapipe/solutions/vision (Face Landmarker, Pose Landmarker)
- gocv — gocv.io/x/gocv (Apache-2.0, OpenCV 4.12)
