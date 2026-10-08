# Learning Jordan's edit decisions: night run progress (started 2026-10-07 ~21:00 PDT)

Pick-up file. Newest entries at the bottom. Jordan's full requests (verbatim) are saved in the
session scratchpad as `jordan-2026-10-07-night.md`; the binding ones are copied here.

## What Jordan asked for (short)

- Spend the $5/month on whichever System One decision model fits each task (Perplexity Decider
  by default). DONE.
- Update llama-server (Whoretana is unused, so no conflict). DONE.
- Build a database of HIS edit decisions, extracted systematically from years of edited video.
  "Above all else, make sure the original files are not altered in any way - the Vegas project
  files can be READ for information, but they also should not be altered."
- A decision model is ONE data point; becky's point is several signals corroborating each other,
  with confidence scores.
- Livestreams: download live chat with yt-dlp (never touch his yt-dlp .conf), work out the chat
  delay, and use eyes/posture (leaning in, eyes on screen) as "reading chat" evidence.
- Deep-dive Reka's clipping platform; zooms and censoring must work in VEGAS; what else can't we
  do in VEGAS?
- Mine Albert Olgaard's skills, the Jev video repos (hermes-research-agent, HyperEdit, ...),
  theAIsearch "AI is reverse engineering everything" video. What are they doing that we
  overlook, and why? Use a decision model to triage.
- Consider EmbeddingGemma 2 and Google's 4B Gemma DiarizationLM. Remember research better
  (sqlite database / modern RAG).
- Free models for grunt work: Space Bunny Free (OpenCode Zen), ling-3.1-flash (OpenRouter free);
  Haiku 5.5 when free limits bite.
- Document progress and push to GitHub after milestones.

## Answer keys found on this PC (read-only)

| Project | What it is | Raw footage | Render |
|---|---|---|---|
| `X:\Videos\2025\8_August\4-human-brain-robot\human-brain-robot.veg` | Talking-head cut, 720x1280: 71 kept pieces from SNOW-2/5/6_converted.mp4 | `hj-footage\` (present) | not identified (`explaining-ai.mp4` predates the .veg) |
| `X:\Videos\2025\8_August\cool-little-tripod-product-video\cool-little-tripod.veg` | Product video | IMG_21xx.MOV, SNOW-11/12/13 (present) | `Rendered\cool-little-tripod-WITH-CAPTIONS.MP4` (65 s) |
| 91 `.veg` files under `X:\Videos` | years of edits | varies | varies |

## Log

- **21:05 Decision client:** `internal/systemone` `Hosted` now accepts any OpenRouter model whose output is
  "decisions" (refuses chat models), default `perplexity/pplx-decider-v1.1-27b`; $5/month cap
  unchanged. Live: Perplexity and Jev both pick the right take. Pushed 532c788.
- **21:06 llama.cpp updated:** b9551 -> b11487 (CUDA 12.4) in `C:\llama.cpp\build\bin`; old build kept in
  `C:\llama.cpp\build\bin-b9551-backup`. Same Gemma behaviour on text and image on both builds
  (Gemma "thinks" on image prompts in both). New build serves `/v1/systemone` for local decision models.
- **21:10 `vegas/BeckyDumpProject.cs`:** headless, read-only export of a whole VEGAS project to JSON
  (events, source offsets = the cuts, pan/crop keyframes = zooms, effects + parameter values,
  envelopes, Titles & Text, markers, regions). Always run on a COPY in `X:\AI-2\edit-learning-work\`
  (outside the repo). Originals' SHA-256 checked before and after: unchanged.
  - human-brain-robot: works (2 tracks, 71 video events; track FX: LUT, Beauty Box, AutoLooks,
    Color Corrector, Color Curves; audio: Noise Gate, EQ, Compressor).
  - cool-little-tripod: VEGAS crashes while opening it headless (even with the old verify script),
    so it is the project, not the exporter. Not solved yet.
- **21:15 Reka research done** (scratchpad `reka-report.md`): Reka Clip runs on their private Reka Flash, not
  the local Reka Edge; their repos call a paid API that returns rendered video only, so nothing
  lands on a timeline. VEGAS can do zooms (Pan/Crop keyframes), censoring (Pixelate/Blur FX +
  presets; Jordan's own 2021 `CENSOR.js`), bleeps/ducking (volume envelopes). Bridge reads pan/crop
  but writes none of these yet. Proposed: 3 small writer scripts (pan/crop, FX+preset on a range,
  volume/speed envelopes).
- **In progress:** transcribing SNOW-2/5/6 to label every raw sentence keep/cut against the .veg.
  Space Bunny is reading Albert's skills and hermes/HyperEdit (OpenCode blocks paths outside its
  folder, so it works on copies).
- **21:45 Answer key #1 loaded:** `scripts/editlearn.py` labels every raw word/sentence kept or cut
  against the exported edit and stores it in `X:\AI-2\edit-learning-work\edits.db` (sqlite, outside
  the repo: tables projects, pieces, sentences, removed). human-brain-robot: 113 raw sentences ->
  54 kept, 30 partly, 29 cut; 63 removed stretches contained words (retakes, false starts, a whole
  affiliate pitch), only 6 were pure pauses. His pattern = Paul Borg's: keep the LAST good attempt.
- **22:00 `becky-besttake` (new tool, `cmd/besttake`):** Paul Borg's take picker with two signals per
  decision (System One model + plain-code word match), plus two fixes found on Jordan's footage:
  split a line where it restarts itself ("Do not buy one of the Do not buy one of the AI..."), and
  only cut lines of a dropped attempt that the kept take actually repeats (decision model +
  word containment). Scored on the 3 raw clips vs Jordan's real cut (`scripts/besttake_score.py`):

  | Clip | Perplexity Decider | Jev | baseline (cut only 1-2 word noise) |
  |---|---|---|---|
  | SNOW-2 | 77.5% agree | 80.0% | 62.5% |
  | SNOW-5 | 90.0% | 87.5% | 80.0% |
  | SNOW-6 | 73.3% | 77.8% | 71.1% |

  It catches ~60% of his cuts (baseline ~12%). Total spend for every test run tonight: $0.0067.
  Too little data (125 lines, one video) to keep tuning or to pick a model: more answer keys first.
  Not yet on the VEGAS timeline: it outputs keep/cut per line (JSON).
- **22:20 VEGAS effect writer `vegas/BeckyFX.cs` - zooms, censoring, bleeps ON THE TIMELINE.**
  Jordan's own `HJ Scripts` (Documents\Vegas_Assets\Scripts\HJ Scripts) all put one plug-in +
  HIS preset on a split event: CENSOR = VEGAS Pixelate "CENSOR", CUT IN / IN EXTREME / OUT /
  1-5 JUMP / up-down-left-right = VEGAS Picture In Picture presets, BIG HEAD = Spherize, DRAMA /
  HORROR / NEON = Color Corrector, MEME SWIRL / WAVE, TV, ... BeckyFX does exactly that on a frame
  range (`fx`), plus `zoom` (Pan/Crop push-in held across jump cuts), `duck` (volume envelope) and
  `audio` (e.g. a 1 kHz bleep). His 23 filter packages ("ZOOM FAST", "SAD ZOOM", ...) CANNOT be
  added by a script: VEGAS 18's API shows them as empty shells (no id, no contents; AddEffect
  fails). Verified on a copy of human-brain-robot by VEGAS's OWN render (`BeckyRenderRanges.cs`):
  pixelated only inside the range; zoom pushes in on his face, holds across a cut, returns after;
  "IN" punch-in only inside; bleep range: voice -54 dB, tone on top. Fixed on the way: a 1-frame
  sliver (ranges now snap to a cut within 3 frames) and the zoom restarting at each jump cut.
- **22:20 Research memory:** qmd (sqlite, keyword + vector, already installed, already uses
  EmbeddingGemma-300M) now also indexes `becky-tools/research`, the becky canon docs and the
  Obsidian YouTube notes + transcripts (`qmd query "<question>"`). EmbeddingGemma 2 and the 4B
  DiarizationLM: not worth switching now (report in the night's scratchpad; numbers in
  `research/jev-integration-plan.md` follow-up).
- **22:20 Repos triage (Hermes, HyperEdit, Albert Olgaard's skills):** none worth installing (they
  render their own MP4s, overwrite originals, call paid APIs, or skip the yt-dlp gate). Ideas worth
  taking: Albert's Jev "said twice" question (its "false" option protects deliberate emphasis),
  re-transcribe after a cut to prove no word was clipped, and the VEGAS writer (now built).
- **22:20 Batch export:** `scripts/veg_export_all.py` answers VEGAS's "media not found" (old
  projects point at the external D: drive) and "plug-in not installed" dialogs, retries anything
  not ok, and only closes its own VEGAS. Running over all 91 projects.
- **22:25 Live chat, first real run (apology stream 9T7Me7-2Aec):** 2,477 messages (16 super
  chats). `becky-livechat --transcript` finds 14 messages Jordan read aloud; 4 spot-checked against
  the transcript, all real (he paraphrases: "pretty much the same room"). Read 2-26 s after the
  message arrived, median 14.6 s (= YouTube's chat delay + his reading time). First version also
  matched 2 coincidences built from words he says all the time ("sorry" 248x, "john" 45x in this
  stream): a match now needs 2+ words he rarely says.
- **22:25 Albert's "said twice" wording tried in becky-besttake's coverage check:** 80.0 / 87.5 /
  73.3% vs 77.5 / 90.0 / 73.3% - no real difference on 125 lines; original kept. Spend so far
  this month: $0.0089 of $5.
- **22:25 yt-dlp gate is now first come, first served** (the backfill starved the chat download
  for 40 min).
- **22:40 "Reading chat" from posture (apology stream):** picture_signals.py (face, head angle) on the
  3 s before each of the 14 confirmed reads vs 28 ordinary talking moments (random, 60+ s from any
  read, fixed seed). Head down 12+ deg, or head dropped out of the face model, or face low in frame
  (centre below 0.40): **13 of 14 reads, 6 of 28 ordinary moments.** Head angle alone: down 9+ deg
  before 10/10 reads where the face model held. So posture is a real second signal next to the
  read-aloud match. Work files: `X:\AI-2\edit-learning-work\gaze\`.
- **22:40 `scripts/edit_habits.py`** over the first 14 exported projects: 2,160 video pieces, median
  piece 1.72 s (10% under 0.77 s, 90% under 6.87 s); 56 push-ins, median 1.55x, reached in median
  0.70 s; 23 pull-outs; 13 pans; almost no fades; effects led by S_Shake (31 uses / 4 projects).
  Re-run after the batch ends for all 91.
- **23:15 Filter packages, reverse engineered:** theAIsearch's "AI is reverse engineering everything"
  video (now titled "The AI unlock has begun", h5zkzon0gM4, intake note written) points at ILSpy for
  .NET. VEGAS's script library has no way to apply a package (only `PlugInNode.IsPackage`). But
  `Vegas Effects HJ.sfpreset` shows what a package is: "ZOOM IN OUT" = one Picture In Picture with
  keyframed Location/Scale. The exporter now writes animated settings' keyframes (`"anim"`), so his
  packages can be rebuilt from his own projects. 13 animated Picture In Picture zooms in the first
  51 projects.
