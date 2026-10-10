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
| voice-prints/Shelby | VID_20260102_200149 0:03-0:33 = JOHN CLANCY on camera ("I'm being cheated on by my wife") | moved to Clancy; Shelby has NO voice print until a verified one exists |
Aliases: Shelby + "Shelby Clancy", "Shelby Kerberg", "PopTart Barbie", "poptartbarbie"; Clancy + "TakingBack2007", "Melancholy".
Backup of the old kb-final: session scratchpad `kb-final.backup-2026-10-10`.

## Known model misses (Jordan-verified photos the models get wrong)
- Face (ArcFace buffalo_l, insightface): `HJ Training Photos\IMG_7107.JPG` (= `a_photo_of_Hair_Jordan(59).JPG`)
  scores Shelby 0.22 > Jordan 0.19. Looked at it: green hair covers one eye and half the face (shirtless,
  chest tattoo - no t-shirt face). Left out of the print.
- Face: `IMG_2126.JPG` and `a_photo_of_Hair_Jordan(54).JPG` detect 2 faces (his + the t-shirt face).
- Face: 6 tight `Instagram Lora` crops ((58) (59) (60) (64) (65) (70)): detector finds no face (needs margin).
- Face: on his 2026-09-26 stream (green hair, heavy eye makeup) most frames scored 0.47-0.54 vs the 0.55 bar
  BEFORE the stream frames were enrolled; after: 0.78 (8:00-8:30, not enrolled) and 0.84 (edit40).
- Voice (CAM++ via sherpa-onnx): Jordan's own stream at 8:00 vs his 3:00 print = 0.67; vs Clancy's print
  0.70 (WRONG speaker on top). On edit40: Jordan 0.65, Clancy 0.20 (right, but under the 0.75 bar).
  The README's "same person ~0.84 vs different ~0.03" calibration does not hold on this audio.

## Newer models, measured on Jordan's own footage (2026-10-10)
Survey: HF hub + model cards (search "speaker verification", "redimnet", "wespeaker", "LVFace", "adaface").
Downloaded to `models/voice/redimnet2-plus`, `models/voice/wespeaker_en_voxceleb_resnet293_LM.onnx`,
`models/face/lvface/LVFace-B_Glint360K`. Scripts: session scratchpad `voice_score.py`, `face_score.py`.

**Voice** - 8 clips of Jordan (2026-09-26 stream at 1:00 2:00 4:00 8:00 10:00 12:00 14:00 + edit40) and 8 of
Clancy (ScreenRecording_01-09-2026 at 0:02 1:00 3:10 5:00 7:00 8:40 - checked on screen: Clancy alone -
+ VID_20260102_200149 0:03, 0:40), 15 s each, every pair scored:

| Model | worst same-person | worst Jordan-vs-Clancy | gap |
|---|---|---|---|
| CAM++ 3D-Speaker (current) | 0.73 | 0.72 | +0.01 (barely apart) |
| WeSpeaker ResNet293-LM (2023, CC-BY) | 0.75 | **0.93** | -0.18 (confuses them) |
| **ReDimNet2+ LMFT** (`lab260/redimnet2-plus`, Apache-2.0, ICASSP-2027 submission; 0.35% EER Vox1-O) | 0.49 | **0.21** | **+0.28** |

ReDimNet2+ gotcha: the model card's loading snippet is WRONG (`return_2d_output: False` -> size mismatch).
The real config is the public b6-vox2 release it was tuned from, saved as `model_config.json`; build
`ReDimNet2Wrap(**config)`, load the `_orig_mod.encoder.model.*` keys strict, feed `model.spec(wave)`.
Importing `asv.models` pulls the training stack (omegaconf); stub the package to skip it.

**Face** - 123 photos with a face: Jordan 89 (HJ Training Photos incl. subfolders + 5 stream frames),
Shelby 10, Clancy 12 (2 named mugshots + penguin thumbnail + 9 frames of his two videos), plus impostors
Brandon Grosso 8 and Paul Denino 4 (from `X:\Videos\2026\10_oct\mugshots` - that folder holds THREE men,
see its mugshots.txt; only Osceola + Washington County are Clancy). Each photo vs each person's average
of their OTHER photos:

| Model | right person (mean / lowest) | best wrong person (mean / highest) | wrong or <0.05 apart |
|---|---|---|---|
| ArcFace buffalo_l (current) | 0.72 / 0.15 | 0.19 / 0.33 | 1 (IMG_7107 -> Shelby) |
| **LVFace-B Glint360K** (ByteDance, ICCV 2025 highlight; weights non-commercial research) | 0.63 / 0.17 | **0.05 / 0.11** | **0** |

Stream frames (green hair + makeup): ArcFace 0.61-0.76, LVFace 0.46-0.70 - both name him; LVFace scores run
lower overall, so its naming bar must be set from these numbers, not ArcFace's 0.55.
Tight crops: padding the photo by half its size before detection recovered 6 of the "no face" crops.

## Next (needs Jordan's yes)
- Voice: put ReDimNet2+ in becky-identify (replace CAM++ for naming, or both must agree), new bar from
  the numbers above.
- Face: LVFace-B as a second opinion next to ArcFace (name only when both agree), plus the padding retry.
