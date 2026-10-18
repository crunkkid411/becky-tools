# becky-transcribe to-do (2026-10-17)

From Jordan's request on 2026-10-17 and `2026-10-15_becky-tools-agent-feedback.md`.
Tonight's priority: plain transcription in Becky Review that works, with names spelled right.

## Done tonight (2026-10-17)

- [x] **Becky Review gave up after 15 minutes.** The "+" (transcribe) button waited 900 s, so every video
      longer than a few minutes failed with a timeout even while becky-transcribe was still working. Now it
      waits up to 6.5 hours (becky-clip's own limit went from 30 min to 6 h; "Transcribe all" from 2 h to 7 days).
- [x] **Progress.** becky-transcribe always prints "step N of M: ..." on screen, with a percentage while
      Parakeet listens. Becky Review's busy box shows the same line and a filling bar.
- [x] **Becky Review already runs the plain path.** Its "+" asks for an .srt, which never runs speakers or
      the forensic check. It does run: Parakeet, the WhisperX second listen, the word list, the Gemma-4 E4B
      proofread. (E4B is the small Gemma; the 12B is only used by the speaker/forensic tools.)
- [x] **Word list read live.** `cmd/transcribe/lexicon.txt` is read from the repo on every run, so names
      added there work without rebuilding the .exe (the built-in copy is the fallback).
- [x] **Batch.** `X:\AI-2\becky-tools\Transcribe Videos.bat`: drag videos or folders onto it (or double-click
      and pick a folder). Makes the same transcript file Becky Review makes, skips videos that have one.

## Next (in order)

1. **Hear names right the first time (the 1,100-word problem).** The word list only fixes a mistake it has
   seen before, word for word ("the U Girl" vs "the Uwu Girl" vs whatever comes next). Real fix: give the
   speech models the name list WHILE they listen ("hotwords" / context biasing).
   - Check whether sherpa-onnx's Parakeet-TDT transducer accepts a hotwords file (modified beam search).
     The GPU path (onnx-asr / DirectML) has no hotword option; may need the sherpa path for biasing.
   - WhisperX second listen: pass the top names as `--initial_prompt` (Whisper prompt limit ~224 tokens,
     so pick the most frequent names, not all 289).
   - Measure on the 9 redone livestreams (`E:\hair_jordan\youtube`, answer key = corrected transcripts).
2. **Add the remaining checked fixes to the word list.** Feedback doc section 12 D (86 name-like fixes not
   yet in the list) and the names in section 11. Review each for false swaps (a real word that is usually
   right must not be swapped everywhere).
3. **Stacked becky-intake processes.** On 2026-10-17 at 18:20 five becky-intake.exe were running (started
   1 AM, 5 AM, 9 AM, 1 PM, 5 PM), never exiting. Each can start its own Gemma server and take the graphics
   card from transcription. Find why it never exits; make the scheduled task skip if one is running.
4. **One shared Gemma server** per model instead of one per becky tool (feedback 3A.4).
5. **Speakers/face/voice on the GPU** (config default `Device: "cpu"`, feedback 3A.1-3). Only matters for
   `--diarize` / forensic runs, not Becky Review.
6. **Never `-ngl 99`**: scout, ground, validate, avlm, vision still hard-code it (feedback 3A.5).
7. **Timings in the transcript JSON** (per step seconds + device, and `fell_back`), feedback 3A.6.
8. **True batch inside one process** (models loaded once, next video's listening overlaps this one's
   checks). The .bat reloads models per video; fine for now.
9. **Explain becky-transcribe's self-orchestration** in one page for Jordan (what runs, when it escalates).
