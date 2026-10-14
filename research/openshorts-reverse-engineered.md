# openshorts, reverse-engineered (2026-10-13)

Source: `X:\AI-2\content_generators\openshorts` (mutonby/openshorts, commit 0240abe). Read from the
Python source directly. It is a full source tree, not a shipped binary, so the REA disassembly tools
had nothing to add. Jordan asked: *how do they get better results with worse models?*

## The answer in one line

**The model only makes a few coarse decisions, each with a lot of context, and code does everything
else.** It never times a cut, never measures anything, and never decides one small piece at a time.
becky's `--model systemone` edit did the opposite: hundreds of per-sentence votes and per-pause "keep
this gesture?" calls, each one a small model guessing with little context. That came out worse than
becky-cut alone.

## The rules they follow (each one measured on their production jobs)

| # | Rule | Where | Their evidence |
|---|---|---|---|
| 1 | The model **ranks** whole ~90 s windows of transcript against each other. It does not judge one candidate alone. | `clip_selection.build_transcript_windows`, `SCORE_PROMPT_TEMPLATE` | A single call over a whole transcript clusters its picks near the start. Windows give even coverage. |
| 2 | Every window gets a score, and **code** takes the global top N. A batch never "selects up to 3". | `score_batches` | "Choose up to 3 per batch" capped the shortlist and starved every source under ~30 min. |
| 3 | Batches are near-equal in size. | `score_batches` | A trailing batch of 1 window returned that window whatever its score. |
| 4 | **The model never does timing arithmetic.** Its in/out points are snapped to real word edges plus half the pause around them (at most 0.35 s before, 0.45 s after). | `snap_clip_to_words` | p90 of the model's raw proposals was 0.4-0.5 s off the nearest word. |
| 5 | Floors and limits live **in code**, not in the prompt. A clip floor gets one extra ask. Cutting down is by score, never by position. Clips that share 25% are deduplicated. | `clip_count_targets`, `trim_to_best`, `dedupe_overlapping` | "A prompt is not a contract": the model returned half the floor. Slicing by position dropped the back half of the video. |
| 6 | Visual questions are a **closed choice** between named options, with a safe default ("none is usually right"). Never a measurement. | `layout_picker.py`, `LAYOUT_CHOICE_PROMPT` | A measurement scored 1% and 97% on the same video in two runs. A closed choice scored 94/92/96%. |
| 7 | The model gets a **few frames at a readable size** (12 frames at 1024 px), not the whole video. | `layout_picker`, `hook_grounding` | 640 px could not read a spreadsheet. 24 frames did slightly WORSE than 12. |
| 8 | Prompts name the exact mistakes earlier versions made. Once tuned against labeled clips, they are frozen ("Do not reword casually"). | `LAYOUT_CHOICE_PROMPT`, `SHOT_CONTENT_PROMPT` | 48 hand-labelled clips, re-run after every prompt change. |
| 9 | The output schema is enforced (`response_schema` / `json_schema`), checked against the same model in code, and retried on transient errors. A small local model gets 3 windows per call, not 8. | `llm_backend.py`, `_run_gemini_stage` | "A silently truncated prompt scores garbage." |
| 10 | Effects are opt-in and capped. The model picks WHAT and WHEN, from a closed list. Code builds the filter with hard limits. | `edit_builder.py`, `punch_in.py` | Model-written ffmpeg filters broke. Default punch-in settings fired every 7 s and read as "a twitch". |

## Feature list, and where each one is in becky now

| openshorts feature | becky | Status |
|---|---|---|
| Moment picker: score windows, then the detail pass, floor, trim, snap, dedupe | `internal/clippick`, the default picker in `becky-moment` (`--picker windows`) | **built 2026-10-13** |
| Titles, descriptions, hook text per clip | same pass: `hook`, `title`, `tiktok`, `instagram` in becky-moment's output | **built 2026-10-13** |
| YouTube description with chapters | `becky-livestream --video` writes `becky-edit\youtube-upload.txt`. Code computes the chapter times on the finished edit. | **built 2026-10-13** |
| The coarse-decision rule applied to a full edit | `becky-livestream --video` (`coarse.go`) | **built 2026-10-13** |
| Vertical reframe that follows the face | `becky-short` (burned MP4); **`becky-vertical` on the VEGAS timeline** (Pan/Crop on his face, a hard cut to a new framing when he moves - he almost never pans) | **built 2026-10-13** (VEGAS) |
| Word-timed captions | `becky-short` (burned); `becky-vertical`: Titles & Text events with his "becky-captions" preset, same cli-cut chunker | **built 2026-10-13** (VEGAS) |
| Silent-footage vision fallback | `becky-vertical`: under 30 words, Gemma-4 writes one line per picture every 3 s and becky-moment's picker reads those lines | **built 2026-10-13**, not yet run on silent footage |
| SPLIT (two speakers stacked) and SCREENCAST layouts | `becky-vertical`: closed choice from InsightFace faces (single default), two tracks, Pan/Crop only (no masks) | **built 2026-10-13**, unit-tested; his test footage is one person |
| Hook text overlay placed away from faces | `becky-vertical`: their `choose_y` on Titles & Text, his title look (Montserrat Black 1.52) | **built 2026-10-13** |
| Punch-in on emphasis beats, AI effect list | `becky-vertical`: Gemma-4 12B picks `punch_in` / `face_zoom` per line, code keeps the limits; beats (their 0.6 / 18 s rule) when the model is down. Sizes are Jordan's (1.27x, 2.0x) | **built 2026-10-13** |
| Thumbnails (AI image + his face) | not built | becky-imagegen takes 7-8 min per image on the 3070 |
| ElevenLabs dubbing, auto-posting, S3, billing, SEO | not built | paid services or not needed |
