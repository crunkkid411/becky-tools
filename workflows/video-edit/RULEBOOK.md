# RULEBOOK - the video editing pipeline only

Read this first when you work on the video edit pipeline (`becky-livestream`, the
YouTube-Edit / Livestream Edit launchers). It does NOT apply to the rest of becky-tools.

Folders in here are the pipeline steps, top to bottom in the order they run.
Name the step by its folder name when reporting a problem ("Breath-Check left the lip-smack at 01:12"). Never talk to Jordan in job numbers or old step codes (S07, F05, PROMPT 5): they mean nothing to him.
The plan that produced this folder is in `plan/` (start with `plan/MESSAGE-TO-CLAUDE-CODE.md`).

## Jordan's decisions (2026-10-20) - these outrank everything in plan/

1. **ONE pipeline for all footage.** Livestream, YouTube video, vlog: same decisions,
   same cuts, same finishing. Live chat and the music bed are
   opt-in switches, not separate pipelines. A very specific job gets its own prompt,
   not a fork of the pipeline.
2. **ITERATE, NEVER REBUILD.** The decision engine has been rewritten from scratch three
   times while Jordan thought it was being improved. Change the existing code in small
   steps. Replacing a step's engine needs Jordan's yes in words, first.
3. **The System One voting engine (`--model systemone`) is dropped.** It was worse
   than just removing silences.
4. **Content-Decision has two versions today** (the two folders in 06_Content-Decision). A side-by-side
   on the same video picks the winner; until then, don't delete either.
5. **Never add to any CLAUDE.md for this pipeline.** Lessons go in this file or the step's
   JORDAN.md.

## Hard rules (each one cost a real bug)

- Footage is read-only. Everything written goes in the `becky-edit` folder beside it.
- A project Jordan saved by hand is never overwritten.
- Cut points come from becky-cut (04_Silence-Map): frame-accurate, never clip words. Captions snap to them.
- A model may PUT BACK what becky-cut removed (a toast, a gesture) but never adds cuts beyond
  its named exceptions. A model's retake claim is confirmed by code against the actual words.
- Everything that can be deterministic is deterministic. Models answer narrow questions.
- OBS footage arrives with treated audio (pro mic, live denoise/compressor/EQ/limiter).
  Loudness passes measure it and SKIP; untreated footage is levelled to -17 LUFS into a
  -6 dBFS limiter.
- The grade goes on the camera track only, never on graphics, b-roll or captions. It runs
  last on purpose (never changes an edit decision, saves time while iterating).
- Local models first (free). System One is capped at $5/month in code.
