# MediaPipe for becky's picture check (October 2026), tested on the 27-livestream

## The short answer

MediaPipe can give becky three facts it does not have today. All three worked on the 14 test moments from the 27-livestream:

1. **"He's holding a drink up to the camera."** The object finder spots the raised water bottle during the toast, and nothing like it in any breath. Never cut.
2. **"His mouth is held wide open."** MediaPipe's face model agrees with the face model becky already uses. Both say "wide open" for the two big expressions, and neither says it for any of the 8 breaths. Two models agreeing is becky's rule for trusting something.
3. **"His hands are up at his head or above his shoulders."** True for all 3 movements, the toast and the hands-to-head breath. False for all 6 still breaths and the arms-spread breath.

**What did NOT help:** the hand-gesture reader (it saw no gestures), head-turn speed (too jittery), and MediaPipe's sound labeler (YAMNet). YAMNet almost never hears a short breath, so it is **not** a useful second opinion next to BEATs.

**Caution:** the sample is small (one toast, two expressions). Also, moving is not the same as "keep": you want the arms-spread and hands-to-head breaths cut. So movement can only make becky more careful about a cut. It cannot stop a cut by itself.

---

## What is installed on this PC

- **mediapipe 1.0.1** in `X:\PythonUserBase\Lib\site-packages`. It runs with `C:\ProgramData\anaconda3\python.exe` (Python 3.12.3) when `PYTHONNOUSERSITE=1` and `PYTHONPATH=X:\PythonUserBase\Lib\site-packages` are set.
- The top-level package only exposes `Image`, `ImageFormat` and `tasks`. The old `mp.solutions` API is gone. The Python layer now drives a bundled `mediapipe\tasks\c\libmediapipe.dll`.
- Tasks in the installed package:
  - Vision: FaceDetector, FaceLandmarker, GestureRecognizer, HandLandmarker, HolisticLandmarker, ImageClassifier, ImageEmbedder, ImageSegmenter, InteractiveSegmenter (plus InteractiveSegmenterLegacy), ObjectDetector, PoseLandmarker.
  - Audio: AudioClassifier.
  - Text: LanguageDetector, TextClassifier, TextEmbedder, TextProofreader, TextSummarizer.
- Imports that work: `from mediapipe.tasks.python import BaseOptions, vision, audio`.
- No upgrade is needed for anything in this report. PyPI published **1.1.0 today (2026-10-06)**. It has no GitHub release notes or tag yet, so I could not verify what changed, and I did not test it.

### Release history 2025-2026 (GitHub releases + PyPI)

| Version | Date | What matters for becky |
|---|---|---|
| 0.10.21 | Feb 2025 | Multiclass NMS option for Object Detector |
| 0.10.32 | Jan 2026 | C API for AudioClassifier; Bazel 7 / Protobuf 5 |
| 0.10.33 | Mar 2026 | **Holistic Landmarker re-added to Python**; FULL_RANGE face detection supported |
| 0.10.35 | Apr 2026 | Internal/platform changes only |
| 1.0.0 | 28 Jul 2026 | **Python GenAI (LLM) components removed**; new TextSummarizer + TextProofreader (summarization_200m / proofread_200m); Gecko + EmbeddingGemma in Text Embedder; EXIF orientation support in the Python Task API; Interactive Segmenter moved to "legacy" ahead of a v2 |
| 1.0.1 | 14 Aug 2026 | PyPI only, no notes. **Installed.** |
| 1.1.0 | 6 Oct 2026 | PyPI only, no notes. Not tested. |

## Capability table

The official docs (developers.google.com/edge/mediapipe, updated 2026-09-28 to 2026-10-01) list 11 vision tasks, 5 text tasks, 1 audio task, and LLM Inference for Android/Web only.

"Ran here" means I ran it on this PC: Python 3.12, CPU, frames scaled to 640 px. Times are per frame.

| Task | Outputs | Model file (size) | Ran here? | Useful for edit decisions? |
|---|---|---|---|---|
| **Face Landmarker** | 478 3D face points, **52 expression scores (blendshapes)**, head transform matrix | `face_landmarker.task` (3.8 MB; BlazeFace + FaceMesh V2 + Blendshape V2) | **Yes, 9.7 ms** | **Yes:** `jawOpen` is a second, independent mouth-open signal. The other 51 scores did not separate anything here (see measurements). |
| **Hand Landmarker** | Up to N hands × 21 points + left/right label | `hand_landmarker.task` (7.8 MB; palm detector + landmarks) | **Yes, 21.3 ms** | **Yes, as a second movement signal:** "a hand is in the picture" was 0/31 frames on still breaths and 36-100% on movements. It lost the hand at the toast's peak (bottle grip). |
| Gesture Recognizer | 8 canned gestures (None, Closed_Fist, Open_Palm, Pointing_Up, Thumb_Down, Thumb_Up, Victory, ILoveYou) + hand points; can be retrained (Model Maker) | `gesture_recognizer.task` (8.4 MB) | Yes, 21.5 ms | **No:** "None" for all 34 hands it found inside the spans |
| **Holistic Landmarker** | Pose 33 + face 478 + both hands 21 + optional blendshapes + segmentation mask, in one call | `holistic_landmarker.task` (13.7 MB; contains a lighter face mesh, 1.15 MB, and a lighter pose model, 2.8 MB, than the separate tasks) | **Yes, 31.8 ms** | Optional: one call instead of face + hand + pose (51 ms). Its own `jawOpen` also split expressions from breaths. No head matrix. |
| Pose Landmarker | 33 body points + visibility (lite/full/heavy) | `pose_landmarker_full.task` (9.4 MB, already on disk) | Yes, 20.3 ms | **Yes, and becky already runs it.** Just read more points from it (nose, ears, index fingers). |
| **Object Detector** | Boxes + labels for 80 COCO classes (bottle, cup, wine glass, cell phone, ...) | `efficientdet_lite0_int8.tflite` (4.6 MB); `efficientdet_lite2_float32.tflite` (23.1 MB); SSD-MobileNetV2 also offered | **Yes, Lite0 50.7 ms, Lite2 128.9 ms** | **Yes:** a raised bottle marks the toast |
| Face Detector | Face boxes + 6 key points; short-range, full-range and full-range-sparse BlazeFace | `blaze_face_*.tflite` (not downloaded) | Not run | No: becky already has insightface + LR-ASD |
| Image Segmenter | Pixel masks: selfie (person/background), hair, multiclass (background, hair, body-skin, face-skin, clothes, accessories), DeepLab-V3 | `selfie_*`, `hair_segmenter`, `selfie_multiclass_256x256`, `deeplab_v3` (not downloaded) | Not run | Not for breath/keep decisions. Possibly for crops later. |
| Interactive Segmenter | Mask of the object at a clicked point/stroke (v2 "brush" API; old one now "legacy") | MagicTouch (not downloaded) | Not run | No |
| Image Classifier / Embedder | ImageNet labels / image vectors | EfficientNet-Lite, MobileNetV3 (not downloaded) | Not run | No: whole-frame labels, and plain pixel change does the job of an embedding diff |
| **Audio Classifier** | 521 AudioSet labels per 0.975 s window (includes Breathing, Sigh, Gasp, Pant, Sniff, Snort, Wheeze) | `yamnet.tflite` (4.1 MB; docs say 12.3 ms per window on CPU) | **Yes** | **No:** "Breathing" was ≤ 0.02 when a marked breath's own audio was classified, and ≤ 0.2 in sliding windows (details below) |
| Text tasks | Language, text class, text vector, **proofread** and **summarize** (new in 1.0) | summarization_200m / proofread_200m etc. (not downloaded) | Not run | Not for the picture check |
| LLM Inference | On-device LLM | Android/Web only; removed from Python in 1.0.0 | No | No |

**Licenses.** The MediaPipe Hands model card says "Licensed under Apache License, Version 2.0" (I opened the PDF). The task pages only state page text CC BY 4.0 and code samples Apache 2.0. No license text is embedded in any of the downloaded model files. I did not open the other models' cards.

---

## Measurements

**Method.** Source: `2026-09-26_SOME_of_my_videos_are_back_[17VOWCqJows].mp4` (1080×1920, 30 fps, 900 s). For each span I extracted frames at 10 fps from 1 s before to 1 s after (ffmpeg, auto-rotate) and scaled them to a 640 px long side, the same scale as `picture_signals.py`. MediaPipe ran in VIDEO mode with a fresh tracker per span. Values below are taken from frames inside the span only.

"Insightface mouth" comes from running becky's own `picture_signals.py` on the same spans, unchanged. Scripts and raw JSON are in the session scratchpad (`mediapipe-research\`).

### The numbers that separate

| span | label | jawOpen max | frames jawOpen ≥ 0.6 | insightface mouth max | bottle Lite0 / Lite2 | bottle box (share of frame) | hand in picture (HandLm / Holistic) | pose hand-to-head (shoulder widths) | wrist above shoulder (< 0 = above) |
|---|---|---|---|---|---|---|---|---|---|
| toast | KEEP | 0.42 | 0/7 | 0.101 | **0.48 / 0.58** | **0.12** (largest 0.24) | 0% / 29% | 0.40 | -0.76 |
| expr1 | KEEP | **0.79** | **8/10** | **0.313** | 0 / 0 | 0 | 0% / 0% | 1.31 | 0.87 |
| expr2 | KEEP | **0.80** | **6/10** | **0.320** | 0 / 0 | 0 | 0% / 0% | 1.53 | 1.24 |
| breath_arms | cut | 0.29 | 0/5 | 0.128 | 0 / 0 | 0 | 0% / 0% | 1.51 | 0.60 |
| breath_head | cut | 0.51 | 0/4 | 0.165 | 0 / 0.09 | 0.001 | 100% / 100% | 0.13 | -0.45 |
| still1 | cut | 0.37 | 0/6 | 0.188 | 0 / 0 | 0 | 0% / 0% | 1.23 | (wrists not visible) |
| still2 | cut | 0.39 | 0/5 | 0.150 | 0 / 0 | 0 | 0% / 0% | 1.56 | 1.04 |
| still3 | cut | 0.45 | 0/5 | 0.111 | 0 / 0 | 0 | 0% / 0% | 1.17 | 0.79 |
| still4 | cut | 0.28 | 0/5 | 0.102 | 0 / 0 | 0 | 0% / 0% | 1.27 | 0.84 |
| still5 | cut | 0.22 | 0/5 | 0.064 | 0 / 0.17 | 0.007 | 0% / 0% | 1.14 | 0.82 |
| still6 | cut | 0.003* | 0/5 | 0.037 | 0.13 / 0.25 | 0.019 | 0% / 0% | 1.13 | 0.69 |
| mv_hair | move | 0.21 | 0/5 | 0.107 | 0 / 0 | 0 | 100% / 100% | **0.05** | **-0.50** |
| mv_moving | move | 0.34 | 0/8 | 0.048 | 0.09 / 0 | 0 | 100% / 100% | **0.27** | **-0.52** |
| mv_situp | move | 0.04* | 0/11 | 0.197 | 0 / 0.06 | 0.015 | 36% / 45% | **0.23** | **-1.06** |

\* The face was found in only 2 of 5 frames (still6) and 7 of 11 frames (mv_situp).

### The proposed rules, checked on all 14 spans

| span | label | keep rule fires? | which part fired | movement rule fires? |
|---|---|---|---|---|
| toast | KEEP | **yes** | raised bottle (Lite2 0.58, area 0.123, centre y 0.32; Lite0 alone: 0.48, 0.121, 0.33) | yes |
| expr1 | KEEP | **yes** | jawOpen run 8 + insightface 6 frames | no |
| expr2 | KEEP | **yes** | jawOpen run 6 + insightface 6 frames | no |
| breath_arms | cut | no | | no |
| breath_head | cut | no | | yes |
| still1-6 | cut | no (all 6) | | no (all 6) |
| mv_hair / mv_moving / mv_situp | move | no | | yes (all 3) |

The bottle he holds at chest height while talking (192.4 s, next to expr1) scored 0.77, but its centre sits at 0.70 of the frame height. The height condition correctly ignores it. Lite0 also produced big false "bottle" boxes (16-21% of the frame) on the movement spans, but only at scores of 0.06-0.09. The score condition removes them.

### Numbers that did NOT separate

| span | label | eyeWide max | browOuterUp max | head turn °/s max | pose wrist speed max (frame heights/s) | pixel change max |
|---|---|---|---|---|---|---|
| toast | KEEP | 0.010 | 0.33 | 123 | 1.71 | 18.6 |
| expr1 | KEEP | 0.051 | 0.34 | 70 | 0.60 | 10.6 |
| expr2 | KEEP | 0.057 | 0.41 | 163 | 0.39 | 7.8 |
| breath_arms | cut | 0.012 | 0.11 | 71 | 2.62 | 19.0 |
| breath_head | cut | 0.014 | 0.58 | 45 | 0.47 | 12.2 |
| still1 | cut | 0.026 | 0.24 | 80 | - | 15.0 |
| still2 | cut | 0.16 | 0.72 | 186 | 0.54 | 18.8 |
| still3 | cut | 0.006 | 0.014 | 42 | 0.83 | 5.5 |
| still4 | cut | 0.027 | 0.14 | 93 | 0.47 | 6.4 |
| still5 | cut | 0.014 | 0.024 | 21 | 0.84 | 5.2 |
| still6 | cut | 0.004 | 0.15 | 186 | 0.87 | 18.8 |
| mv_hair | move | 0.013 | 0.78 | 130 | 1.01 | 8.7 |
| mv_moving | move | 0.008 | 0.50 | 52 | 1.46 | 14.5 |
| mv_situp | move | 0.009 | 0.62 | 146 | 4.50 | 30.8 |

- **Eyes and brows read wrong on his face.** On expr1 his eyes are visibly wide in the frame, yet `eyeWide` stays ≤ 0.06 on both expressions. Brow raise is *higher* on still2 (0.72) and mv_hair (0.78) than on the expressions (0.34-0.41). The white contact lenses and dark eye makeup are a likely cause; I did not verify that.
- **Head-turn speed** from the face matrix is jitter at 10 fps: still breaths reach 186°/s.
- **Pose wrist speed** almost separates movement from still: everything ≥ 1.0 is movement or the toast; still breaths are ≤ 0.87. But the gap is thin (still6 0.87 vs mv_hair 1.01). It is the only number that flags the arms-spread breath (2.62).
- **Gesture Recognizer:** "None" for all 34 hands it found inside the spans.

### Sound: YAMNet (MediaPipe Audio Classifier)

| span | label | Breathing, span audio alone | same, boosted to fixed level | best Breathing in any window centred inside span | Speech, span alone | top label (boosted) |
|---|---|---|---|---|---|---|
| toast | KEEP | 0.000 | 0.000 | 0.000 | 0.98 | Speech 0.98 |
| expr1 | KEEP | 0.000 | 0.000 | 0.000 | 0.08 | Inside, small room 0.15 |
| expr2 | KEEP | 0.000 | 0.004 | 0.008 | 0.20 | Speech 0.11 |
| breath_arms | cut | 0.016 | 0.004 | 0.082 | 0.20 | Grunt / Groan 0.15 |
| breath_head | cut | 0.004 | 0.000 | 0.000 | 0.11 | Sound effect 0.26 |
| still1 | cut | 0.019 | 0.016 | 0.199 | 0.11 | Squeal 0.67 |
| still2 | cut | 0.004 | 0.000 | 0.000 | 0.11 | Speech 0.04 |
| still3 | cut | 0.008 | 0.004 | 0.012 | 0.11 | Crack 0.11 |
| still4 | cut | 0.004 | 0.012 | 0.000 | 0.50 | Sound effect 0.11 |
| still5 | cut | 0.004 | 0.016 | 0.000 | 0.26 | Grunt / Sigh / Groan 0.26 |
| still6 | cut | 0.016 | 0.004 | 0.000 | 0.06 | Inside, small room 0.06 |
| mv_hair | move | 0.008 | 0.000 | 0.000 | 0.15 | Sound effect 0.33 |
| mv_moving | move | 0.059 | 0.016 | 0.004 | 0.03 | Filing / Rub / Wood 0.26 |
| mv_situp | move | 0.012 | 0.008 | 0.016 | 0.50 | Speech 0.50 |

YAMNet judges 0.975 s windows. A 0.3-0.5 s breath is too short for it, whether it hears the breath alone, boosted, or inside a sliding window. "Sigh" reached only 0.26.

**Side finding, worth a look:** YAMNet does hear a long breath clearly. It scored Breathing 0.94 for windows centred at 186.67-186.77 s, which cover about 186.2-187.2 s. Windows reaching past about 187.2 s turn to Speech (0.59-0.98). So the **still1 region (186.87-187.37) may start after the loudest part of the breath and run into the next word.** That is one coarse model, so treat it as a lead to check, not a verdict.

### CPU cost (per 640 px frame, this PC, CPU only)

Face 9.7 ms · Hand 21.3 · Gesture 21.5 · Holistic 31.8 · Pose full 20.3 · Object Lite0 50.7 · Object Lite2 128.9.

At 10 fps, adding face + hand + Lite0 costs about 0.8 s of CPU per second of span checked.

---

## Recommendation for becky's picture check

All thresholds below come from these 14 spans (1 toast, 2 expressions, 8 breaths, 3 movements). Treat them as a first calibration, not settled values.

1. **Add the Object Detector: the toast.**
   - Model: `X:\AI-2\becky-tools\models\mediapipe\efficientdet_lite0_int8.tflite`, with `category_allowlist=["bottle","cup","wine glass"]`.
   - Rule: **keep (no region) if any frame of the span has a box with score ≥ 0.3, area ≥ 5% of the frame, and box centre in the top half (y < 0.5).**
   - Measured: toast 0.48, 12%, centre 0.33. Every breath ≤ 0.13 and ≤ 1.9%. The low-held bottle was 0.70 down.
   - If a toast is ever missed, swap in `efficientdet_lite2_float32.tflite` (toast 0.58, 2.5× slower).
   - Only "bottle" was tested; cup and wine glass are untested.
   - A good next step: use this as the trigger for the Gemma yes/no question "is he raising a drink to the camera?", not as the only judge.
2. **Add Face Landmarker `jawOpen`: a second face model for the held-open mouth.**
   - Model: `X:\AI-2\becky-tools\models\mediapipe\face_landmarker.task`, with `output_face_blendshapes=True`.
   - Rule: **jawOpen ≥ 0.6 for ≥ 3 frames in a row at 10 fps.** Expressions had runs of 8 and 6; all other spans had 0. The highest on any breath was 0.51.
   - The matching insightface rule, already computable from today's helper, is **mouth ≥ 0.25 for ≥ 3 frames.** Expressions had 6 each; all others had 0; the highest on a breath was 0.188.
   - Either one firing = no region (the safe side for a punchline). Both firing = "expression, two face models agree".
   - Log all 52 blendshapes, but do not threshold the others yet. Smiles, laughs, brow raises and grimaces need their own labeled examples, and eyes/brows read wrong on his face.
3. **Read more points from the pose model becky already runs: hands at head / hands up.**
   - Add nose (0), ears (7, 8) and index fingers (19, 20) to the helper's `BODY` list.
   - Movement rule: **hand-to-head < 0.5 shoulder widths, OR a wrist (visibility > 0.5) above the shoulder line.**
   - Measured hand-to-head: movements 0.05-0.27, still breaths 1.13-1.56.
   - Measured wrist above shoulder: movements -0.50 to -1.06, still breaths +0.69 to +1.04.
   - The same split at 0 holds in becky's *current* helper output, where shoulder width is measured horizontally: movements -0.24 to -0.59, hands-to-head breath -0.24, toast -0.44; still breaths +0.40 to +0.51; expressions +0.47 / +0.67; arms-spread breath +0.31.
   - For a second, independent signal, add **Hand Landmarker** (`hand_landmarker.task`): a hand in the picture was 0/31 frames on still breaths and 36-100% on movements.
   - **This is not a keep rule.** It also fires on the hands-to-head breath, which you want cut. Use it to demand a confident breath label from BEATs before placing a region in a moving span.
4. **Optional: Holistic Landmarker** (`holistic_landmarker.task`) could replace separate face + hand + pose (32 ms vs 51 ms per frame).
   - It found the toast hand in 2 of 7 frames where Hand Landmarker found none.
   - Its `jawOpen` mean also split the groups (expressions ≥ 0.51, breaths ≤ 0.35).
   - Trade-offs: lighter face and pose models inside it, and no head matrix. Not needed now.
5. **Do not add:** Gesture Recognizer, head-turn speed, YAMNet breath scores, segmenters, the image classifier/embedder, or the text tasks. None of them helps these decisions; numbers above.

## Not tested / could not verify

- mediapipe 1.1.0 (released today; no notes to check).
- Face Detector, segmenters, classifier, embedder and text tasks were not run. I have the docs plus their presence in the installed package only.
- No examples of other expression types, so no thresholds for them.
- Cup and wine glass detection.
- Model licenses other than Hands (Apache 2.0).
- YAMNet speed (the 12.3 ms is the docs' figure).

## Primary sources checked (2026-10-06)

- PyPI release history, `pypi.org/project/mediapipe` (1.1.0 on 2026-10-06; 1.0.1 on 2026-08-14; 1.0.0 on 2026-07-27).
- GitHub releases and tags, `google-ai-edge/mediapipe` via `gh` (latest release v1.0.0, 2026-07-28; no v1.0.1/v1.1.0 tags).
- developers.google.com/edge/mediapipe pages: solutions guide, face_landmarker, hand_landmarker, gesture_recognizer, holistic_landmarker, object_detector, image_segmenter, face_detector, audio_classifier.
- MediaPipe Hands model card PDF (mediapipe-assets).
- The installed package's own files and METADATA, plus the contents of the downloaded model bundles.
