# NEXT - the video edit to-do list

This is THE list. The "00_CONTINUE" button starts Claude Code on the first unticked job.
One job per session. Tick it only when it was tested on real footage.

Rules for whoever does a job: read RULEBOOK.md first. Improve the existing code, never rebuild
it. Talk to Jordan in plain words, never by job number or file code.

- [ ] **Side-by-side test of the two content-decision versions** (folder 06). Run both on the same
  video, lay both in VEGAS, Jordan watches and picks. NEEDS JORDAN: he watches.
- [ ] **One pipeline for all footage.** The winner from the test becomes the only decision version.
  Live chat becomes an on/off switch. Remove the System One voting mode. Finishing steps (censor,
  zooms, grade, upload text) run on livestreams too.
- [ ] **Audio polish** (folder 14). Skip OBS audio that is already treated; level the rest.
  Use ffmpeg's loudness meter, don't write a new one. Build spec: plan/BUILD-PROMPTS.md, "PROMPT 3".
- [ ] **Export** (folder 24). Render at fixed settings, check the file. Build spec: "PROMPT 9".
- [ ] **Quality check, part 1: machine checks** (folder 20). After zooms, censor and grade: face stays
  in the zoom, nothing off-frame, grade only on the camera track, censor covers each word.
  Build spec: "PROMPT 5" (machine-check half).
- [ ] **Quality check, part 2: fresh eyes.** A vision model that did not build the edit looks at each
  zoom and graphic and says keep / fix / remove. Build spec: "PROMPT 5" (fresh-eyes half).
- [ ] **Zooms that follow the words** (folder 16), not a timer. Build spec: "PROMPT 8".
- [ ] **Sound effects** (folder 18), placed from the zooms and graphics. NEEDS JORDAN: his SFX folder.
  Build spec: "PROMPT 6".
- [ ] **Music bed** (folder 19), on/off switch. Build spec: "PROMPT 7".

## Done

(Move a job here with the date and one plain sentence of what changed.)

- 2026-10-20 - **Jordan can edit what the models are told.** 18 "PROMPT - ....txt" files in the step
  folders (06 both versions, 09, 10, 11, 23); editing one makes only that step ask again (tested on
  the goodbye-youtube-update copy: edited the thinking prompt, only thinking re-ran).
- 2026-10-20 - **Redo one step / carry on a crashed run.** "00_REDO ONE STEP - click me.bat" and
  `becky-livestream --redo 11`; tested: it redid only the publish check. A crashed run carries on by
  running again, because every slow step keeps its saved answers.
