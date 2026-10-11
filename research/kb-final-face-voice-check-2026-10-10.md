# kb-final face + voice check (2026-10-10)

Jordan: "none of the photos in that hair jordan folder and subfolders contain any people except hair
jordan - human verified. If it THINKS one of those photos is Shelby, it is incorrect and we need to
notate that and perhaps consider dialing in our settings and perhaps adding a second face
identification model". Shelby Alyse Clancy = PopTart Barbie (same person, legal name).

## What was wrong in kb-final, and the fix
| Folder | Was | Now |
|---|---|---|
| face-prints/Hair Jordan | ONE photo, and it was PopTart Barbie (Shelby): ArcFace 0.69 Shelby vs 0.27 Jordan | 24 face crops from `Pictures\Hair_Jordan_Pics\HJ Training Photos\Instagram Lora` (the 6 the detector finds no face in left out) + 3 frames of his current look from the 2026-09-26 stream (1:00, 5:00, 10:00) |
| face-prints/Shelby | 2 photos (correct, 0.57-0.65) | + the misfiled photo + 7 of Jordan's `X:\Pictures-2\poptart` photos (penguin.jpg: no face). `Van_Life_Disaster__Broken_Down_Vlogger.mp4` is AI generated: Jordan removed it; it is listed in kb-final `DO-NOT-ENROLL.txt` |
| face-prints/John Anthony Clancy | 1 stream frame (correct, 0.75) | + Osceola 2025-12-31 + Washington Co 2026-09-10 mugshots + the "buying Penguin" thumbnail |
| voice-prints/Hair Jordan | IMG_6832 (Shelbys_new_house) SPEAKER_01 - the clip holds two voices | 30 s of Jordan alone, 2026-09-26 stream 3:00-3:30 (old clip in the session backup) |
| voice-prints/Shelby | VID_20260102_200149 0:03-0:33 = JOHN CLANCY on camera ("I'm being cheated on by my wife") | moved to John Clancy; Shelby has NO voice print until a verified one exists |
Aliases: Shelby + "Shelby Clancy", "Shelby Kerberg", "PopTart Barbie", "poptartbarbie"; John Clancy + "TakingBack2007", "Melancholy".
Backup of the old kb-final: session scratchpad `kb-final.backup-2026-10-10`.

## Known model misses (Jordan-verified photos the models get wrong)
- Face (ArcFace buffalo_l, insightface): `HJ Training Photos\IMG_7107.JPG` (= `a_photo_of_Hair_Jordan(59).JPG`)
  scores Shelby 0.22 > Jordan 0.19. Looked at it: green hair covers one eye and half the face (shirtless,
  chest tattoo - no t-shirt face). Left out of the print.
- Face: `IMG_2126.JPG` and `a_photo_of_Hair_Jordan(54).JPG` detect 2 faces (his + the t-shirt face).
- Face: 6 tight `Instagram Lora` crops ((58) (59) (60) (64) (65) (70)): detector finds no face (needs margin).
- Face: on his 2026-09-26 stream (green hair, heavy eye makeup) most frames scored 0.47-0.54 vs the 0.55 bar
  BEFORE the stream frames were enrolled; after: 0.78 (8:00-8:30, not enrolled) and 0.84 (edit40).
- Voice (CAM++ via sherpa-onnx): Jordan's own stream at 8:00 vs his 3:00 print = 0.67; vs John Clancy's print
  0.70 (WRONG speaker on top). On edit40: Jordan 0.65, John Clancy 0.20 (right, but under the 0.75 bar).
  The README's "same person ~0.84 vs different ~0.03" calibration does not hold on this audio.

## Newer models, measured on Jordan's own footage (2026-10-10)
Survey: HF hub + model cards (search "speaker verification", "redimnet", "wespeaker", "LVFace", "adaface").
Downloaded to `models/voice/redimnet2-plus`, `models/voice/wespeaker_en_voxceleb_resnet293_LM.onnx`,
`models/face/lvface/LVFace-B_Glint360K`. Scripts: session scratchpad `voice_score.py`, `face_score.py`.

**Voice** - 8 clips of Jordan (2026-09-26 stream at 1:00 2:00 4:00 8:00 10:00 12:00 14:00 + edit40) and 8 of
John Clancy (ScreenRecording_01-09-2026 at 0:02 1:00 3:10 5:00 7:00 8:40 - checked on screen: John Clancy alone -
+ VID_20260102_200149 0:03, 0:40), 15 s each, every pair scored:

| Model | worst same-person | worst Jordan-vs-John Clancy | gap |
|---|---|---|---|
| CAM++ 3D-Speaker (current) | 0.73 | 0.72 | +0.01 (barely apart) |
| WeSpeaker ResNet293-LM (2023, CC-BY) | 0.75 | **0.93** | -0.18 (confuses them) |
| **ReDimNet2+ LMFT** (`lab260/redimnet2-plus`, Apache-2.0, ICASSP-2027 submission; 0.35% EER Vox1-O) | 0.49 | **0.21** | **+0.28** |

Scored the way becky-identify uses it (each clip against the person's AVERAGED print, leave-one-out):
ReDimNet2+ own person 0.658 at the lowest, the other person 0.147 at the highest. becky-identify's
ReDimNet bars (detect 0.30, name 0.50, margin 0.15) sit inside that gap.

ReDimNet2+ gotcha: the model card's loading snippet is WRONG (`return_2d_output: False` -> size mismatch).
The real config is the public b6-vox2 release it was tuned from, saved as `model_config.json`; build
`ReDimNet2Wrap(**config)`, load the `_orig_mod.encoder.model.*` keys strict, feed `model.spec(wave)`.
Importing `asv.models` pulls the training stack (omegaconf); stub the package to skip it.

**Face** - 123 photos with a face: Jordan 89 (HJ Training Photos incl. subfolders + 5 stream frames),
Shelby 10, John Clancy 12 (2 named mugshots + penguin thumbnail + 9 frames of his two videos), plus impostors
Brandon Grosso 8 and Paul Denino 4 (from `X:\Videos\2026\10_oct\mugshots` - that folder holds THREE men,
see its mugshots.txt; only Osceola + Washington County are John Clancy). Each photo vs each person's average
of their OTHER photos:

| Model | right person (mean / lowest) | best wrong person (mean / highest) | wrong or <0.05 apart |
|---|---|---|---|
| ArcFace buffalo_l (current) | 0.72 / 0.15 | 0.19 / 0.33 | 1 (IMG_7107 -> Shelby) |
| **LVFace-B Glint360K** (ByteDance, ICCV 2025 highlight; weights non-commercial research) | 0.63 / 0.17 | **0.05 / 0.11** | **0** |

Stream frames (green hair + makeup): ArcFace 0.61-0.76, LVFace 0.46-0.70 - both name him; LVFace scores run
lower overall, so its naming bar must be set from these numbers, not ArcFace's 0.55.
Tight crops: padding the photo by half its size before detection recovered 6 of the "no face" crops.

## Done (Jordan said yes, 2026-10-10): wired into becky-identify
- Voice: ReDimNet2+ names (detect 0.30, name 0.50, margin 0.15); CAM++ only disputes when it is sure of
  someone else, and then the face must agree.
- Face: LVFace-B next to ArcFace on the same crop, name only when both agree (LVFace bar 0.30, margin 0.10);
  the padding retry runs automatically.
- Unsure faces are routed by System One (`internal/route`, `cmd/identify/escalate.go`).

## Who can judge a face pair (head crops, 24 test photos: Jordan 8, Shelby 5, John Clancy 6, strangers 5)
| Model, how asked | Right | Notes |
|---|---|---|
| Gemma-4 E4B, one pair at a time ("same person? yes/no/unclear") | 22/24 | both misses were "none" for Jordan; never a false "same" |
| Gemma-4 12B, same question | 13/24 | said Jordan and Shelby are the same person; called 3 strangers John Clancy. Not used |
| Gemma-4 E4B, all references in one picture set | 8/24 | poor |
| d1-3B (System One), one pair at a time | coarse | separates Jordan / Shelby / John Clancy, but reads strangers as "John Clancy": routing only, never naming |

So in becky-identify Gemma E4B is the face second opinion, d1 picks the route, and neither names alone.

**Correction (Jordan, same night):** the first build ignored hair ("hair colour can change") - wrong.
Neon green vs blonde vs black hair is strong evidence. Why the face models miss it: ArcFace and LVFace
only ever see a 112 px crop from eyebrows to chin, by design. So Gemma now asks two questions:

| Gemma-4 E4B, 24 photos x 3 people (72 pairs) | Right | Wrong "same person" |
|---|---|---|
| old prompt (head crop, "hair colour can change") | 22/24 | 2 (stranger Paul Denino -> John Clancy) |
| face only (head crop) | 23/24 | 1 |
| whole person: face, hair colour/style, facial hair, tattoos, piercings, build, sex (head-to-chest crop) | 20/24 | 1 |
| **face yes AND whole person not no** (shipped) | **21/24** | **0** - the 3 misses come back "unclear" and routing goes on |

Scripts: session scratchpad `pair_dump.py`, `person_probe.py`, answers in `ladder/pairs.json`.

## Frame by frame, reverse image search, nobody asked first (2026-10-10, night)
- **Clear shot first:** every unsure face, code (no model choice) writes EVERY frame of the 2 s either
  side (ffmpeg `-vsync passthrough`), steps ONE frame at a time outward both ways, follows the face, and
  stops at the first clear shot (det >= 0.70, width >= 80 px, sharpness >= 200, near straight on).
  Measured on the 2026-09-26 stream: sharp frames 440-1240, a 7 px blur 210, 15 px 70-90. It stops a
  direction when the face it follows stops matching (ArcFace < 0.25: a cut or someone else).
  Bug found live: on a slideshow it walked across a cut into the next photo - fixed by that check.
- **becky-revsearch:** Yandex + Bing in Camoufox (hidden; Google blocks automated uploads with its
  "unusual traffic" page). Every result thumbnail goes through both face models; only arc >= 0.55 AND
  LVFace >= 0.30 counts as the same face. Tested: John Clancy's Osceola mugshot -> 60 look-alikes, 0 same
  face (correct: none was him); Jordan's IMG_1137 -> 18 same-face pages, 15 naming Hair Jordan
  (YouTube, Facebook, OnlyFans review sites, hairjordancosmetics.com).
- **Asking Jordan:** System One's menu no longer has a "hold" step. Code lists a face for Jordan only
  after every check ran, with what each found in `why_unnamed`.
