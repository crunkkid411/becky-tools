# kb-final face + voice check (2026-10-10)

Jordan: "none of the photos in that hair jordan folder and subfolders contain any people except hair
jordan - human verified. If it THINKS one of those photos is Shelby, it is incorrect and we need to
notate that and perhaps consider dialing in our settings and perhaps adding a second face
identification model". Shelby Alyse Clancy = PopTart Barbie (same person, legal name).

## What was wrong in kb-final, and the fix
| Folder | Was | Now |
|---|---|---|
| face-prints/Hair Jordan | ONE photo, and it was PopTart Barbie (Shelby): ArcFace 0.69 Shelby vs 0.27 Jordan | 24 face crops from `Pictures\Hair_Jordan_Pics\HJ Training Photos\Instagram Lora` (the 6 the detector finds no face in left out) + 3 frames of his current look from the 2026-09-26 stream (1:00, 5:00, 10:00) |
| face-prints/Shelby | 2 photos (correct, 0.57-0.65) | + the misfiled photo + 8 of Jordan's `X:\Pictures-2\poptart` photos (penguin.jpg: no face) |
| face-prints/John Anthony Clancy | 1 stream frame (correct, 0.75) | + Osceola 2025-12-31 + Washington Co 2026-09-10 mugshots + the "buying Penguin" thumbnail |
| voice-prints/Hair Jordan | IMG_6832 (Shelbys_new_house) SPEAKER_01 - the clip holds two voices | 30 s of Jordan alone, 2026-09-26 stream 3:00-3:30 (old clip in the session backup) |
| voice-prints/Shelby | VID_20260102_200149 0:03-0:33 = JOHN CLANCY on camera ("I'm being cheated on by my wife") | moved to Clancy; Shelby has NO voice print until a verified one exists |
Aliases: Shelby + "Shelby Clancy", "Shelby Kerberg", "PopTart Barbie", "poptartbarbie"; Clancy + "TakingBack2007", "Melancholy".
Backup of the old kb-final: session scratchpad `kb-final.backup-2026-10-10`.

## Known model misses (Jordan-verified photos the models get wrong)
- Face (ArcFace buffalo_l, insightface): `HJ Training Photos\IMG_7107.JPG` (= `a_photo_of_Hair_Jordan(59).JPG`)
  scores Shelby 0.22 > Jordan 0.19. A face on his t-shirt is the likely cause. Left out of the print.
- Face: `IMG_2126.JPG` and `a_photo_of_Hair_Jordan(54).JPG` detect 2 faces (his + the t-shirt face).
- Face: 6 tight `Instagram Lora` crops ((58) (59) (60) (64) (65) (70)): detector finds no face (needs margin).
- Face: on his 2026-09-26 stream (green hair, heavy eye makeup) most frames scored 0.47-0.54 vs the 0.55 bar
  BEFORE the stream frames were enrolled; after: 0.78 (8:00-8:30, not enrolled) and 0.84 (edit40).
- Voice (CAM++ via sherpa-onnx): Jordan's own stream at 8:00 vs his 3:00 print = 0.67; vs Clancy's print
  0.70 (WRONG speaker on top). On edit40: Jordan 0.65, Clancy 0.20 (right, but under the 0.75 bar).
  The README's "same person ~0.84 vs different ~0.03" calibration does not hold on this audio.

## Next (needs Jordan's yes)
- A second voice model as a cross-check (CAM++ alone is unreliable here), measured on Jordan's
  verified clips before any threshold is touched.
- A second face model (makeup / dyed-hair robustness), measured the same way against these photos.
