# Message to Claude Code — architecture review from Qwen (the model that understands Jordan)

Claude — this is a peer review of becky-tools' editing pipeline, written for you
by the model family Jordan trusts for intent. He asked me to be opinionated and
to cover high-level architecture, not the per-step build specs (those are in
BUILD-PROMPTS.md; implement them as written, they came from reading your code).
Read this whole thing before touching anything. Jordan will read it with you.

## What is genuinely good — keep it, defend it, do not "improve" it

1. **The spine is right.** Intake → transcript → becky-cut silence map →
   whole-stream analysis → decision → edges on the frame grid → breath check →
   VEGAS build → verify-by-re-transcription. That ordering encodes the correct
   philosophy: deterministic evidence first, models answer narrow questions
   inside it, humans decide taste at the end. The fact that all four variants
   share those exact functions (I verified: select/edges/breath/vegas/verify are
   literally one code path) is the best thing in the repo. Protect it. Every
   future feature must hang off a numbered spine step, never bypass it.
2. **becky-cut as the cut authority + caption anchor** is correct engineering
   AND correct craft (frame-accurate, never clips words when threshold matches
   treated audio). Keep the LLM's power strictly one-directional: propose/put
   back, never add cuts. Your coarse.go retake-confirmation logic (model claims,
   code checks against actual words) is the pattern every other model touchpoint
   should copy. You already learned the false-positive lesson once; enshrine it
   in RULEBOOK.md.
3. **OBS sync handling** (synced.mkv for measurement, original file with stream
   delays for VEGAS, ~43 ms AAC offset accounted) is careful, correct, and easy
   for a future agent to break. Comment it like the load-bearing wall it is.
4. **The 2026-10-13 --video redesign** (exceptions-only Gemma-12B read instead
   of voting every sentence) was the right call given Jordan's verdict, and the
   header comment recording his reasoning is exactly how decisions should be
   preserved. Do NOT let anyone re-litigate sentence-voting without new footage
   evidence.
5. **Verification by rebuilding the edit audio and re-transcribing** (S9), with
   the two-signal rule (word lost only if timeline math AND re-hearing both
   agree, because Parakeet timestamps run ~0.3 s early) — that is real QA
   thinking. Extend the same instinct to the PICTURE side; that's what F06 is.
6. **Caching discipline**: per-model+guidance decision caches, fresh-cached
   polish reads, votes reused across runs. Good. Just make prompt-file hashes
   part of cache keys (BUILD-PROMPTS #1) or externalized prompts become a
   staleness trap.

## What I'd change — opinions, ranked

1. **The missing converge loop is the whole game.** Jordan said it himself and
   he's right: Jason's step 5c (QA gate → fix → fresh-eyes review → re-check →
   until zero defects) is the single highest-leverage addition in this repo, and
   neither branch has any post-FX verification at all today. Zooms "feel random"
   because nothing ever LOOKS at the zoomed frame and says "his eyes left the
   crop." Ship BUILD-PROMPTS #5 before cosmetics like music. Every finishing
   step (grade/motion/assets/censor/SFX/F02) must end by calling into it.
2. **Stop making Jordan narrate his taste through chat.** The communication gap
   you have with him is structural, not personal: he experiences the pipeline as
   TIMELINE STEPS, you experience it as CODE. The worktree layout
   (becky-worktree-plan.md) is the translation layer: one folder per numbered
   step, each with JORDAN.md (his inputs: SFX library paths, move grammar,
   NEVER-show lists, marker colors) and PROMPT-*.md files he can edit directly.
   From now on: any behavior Jordan might want to tune lives in a .md under
   worktree/, loaded at runtime, hashed into the cache key. Never again say
   "rebuild the exe to change a prompt."
3. **RUN.json (--resume/--only/--from/--status) is a prerequisite, not a
   nicety.** Right now "redo just the zooms" means either a full rerun or a
   Claude Code session, and both are expensive/wrong. Build #10 second, after
   the rulebook. Bonus honesty requirement: `--status` must print step names in
   WORKFLOW-DIAGRAMS vocabulary, so the diagram, the folders, the CLI, and the
   reports all speak ONE language.
4. **Audio treatment: include it, but treat "skip" as a first-class outcome.**
   His OBS chain (pro mic → live denoise/compressor/EQ/limiter) is better than
   anything loudnorm can do; forcing −17 LUFS on it would degrade broadcast
   quality and he'd rightly hate the tool. F02's measured pre-treat gate
   (BUILD-PROMPTS #3) resolves the disagreement between Jason's blueprint and
   Jordan's reality without an if-I-feel-like-it flag: measure, prove treated,
   skip with logged numbers. On untreated vlog footage it does the full job.
5. **Grade placement — you were questioned on this; both of you are right, write
   it down.** Jason grades before graphics; Jordan grades last because grading
   never changes edit decisions and saves compute during iteration. Those are
   compatible AS LONG AS graphics/b-roll/screen-recordings/captions live on
   separate tracks above and receive ZERO grade FX. Make that invariant
   machine-checked in F06 ("no FX chain entries on non-camera tracks"), and put
   the rationale in RULEBOOK.md so no future refactor "helpfully" applies the
   look to the master bus, which would silently grade his overlays.
6. **System One is underused — confirmed, fix deliberately.** It currently only
   decorates transcripts (stack) and votes (together). Its actual superpower —
   cheap batched MCQ — belongs in SFX name picks, music mood pick, keyword
   emphasis for punch-ins, retake pre-seeding, and fresh-eyes triage
   (BUILD-PROMPTS #12). Guardrail that makes this safe: S1 may only PROPOSE or
   PRIORITIZE; deterministic code confirms; `--no-s1` must produce identical cut
   lists (that test is the acceptance criterion).
7. **Secrets/publish safety moves upstream.** A publish check that runs AFTER
   the edit is an apology, not a control. Intake-time NEVER-show list → OCR over
   the frames you already sample at 0.1 s → automatic blur spans + markers
   (#11), keep the late check as the second net. This also reduces what he has
   to catch during review, which is his scarcest resource.
8. **Export parity.** Vertical shorts render via Voukoder; long-form stops at
   the .veg. One locked-spec `--export` (#9) with a validator that refuses bad
   output closes the loop between "done" and "posted."
9. **Is Go holding you back? No — switching languages WOULD.** Yes, Go's C#
   string blobs (videovegas.go embedding whole scripts) are ugly, and yes, the
   Python ML world is next door. But: single-exe deployment into a Windows
   drag-and-drop .bat workflow, goroutine fan-out for multi-window model calls,
   stdlib JSON, COM interop working fine, and a codebase whose correctness lives
   in arithmetic (frame grids, margins, dB math) rather than library depth —
   Go fits. The llama.cpp/Vegas/OBS integration points are process-boundary
   ones; language choice doesn't matter across them. Spend migration energy on
   F06, not on a rewrite. If anything, reduce ugliness incrementally: move the
   big embedded C# blobs to vegas/*.cs files shipped beside the exe (they're
   already installed there anyway — load instead of embed).
10. **One binary with mode flags is right; resist splitting per-variant
    executables.** The variant differences are confined to step S5 + finishing
    set; that's a config axis, not a product axis. Keep main.go thin and honest
    about which modules each variant runs (the switch block is effectively your
    pipeline manifest — mirror it in STEPS-AND-ENGINES.md so the docs can rot-
    check against code).

## How Jordan should drive edits without you in the room (say this TO him too)

- His daily verbs become: `Worktree-Status.bat` (where did it stop / what ran),
  `--resume`, `--only F04`, `--from S05`, edit a JORDAN.md or PROMPT-*.md, drop
  guidance.md lines ("keep beverage gestures even when silent"). Zero Claude
  Code sessions for routine revisions. Reserve Claude Code for NEW steps.
- Feedback format: step number + timecode + observed defect. The worktree
  exists precisely so that's a complete bug report.
- When he disagrees with an edit decision, the question is always "which engine
  owned that moment?" — STEPS-AND-ENGINES.md answers it; deterministic mistakes
  get code fixes, judgment mistakes get prompt/JORDAN.md fixes. Never both at
  once.

## Small honest nits while you're in there

- `workflows/livestream/Livestream Edit - Qwen.bat` hardcodes
  `C:\Users\jordanb\...` — resolve relative to the repo root so the folder
  travels between machines. Same in the System-One bat.
- The untested `Livestream-Edit_System-One.bat` maps to systemone-stack (not
  bare systemone) — rename the bat to match, or Jordan will A/B the wrong thing.
- videoreport.go prints the finishing sequence order — extend it to print the
  RUN.json table too, one screen = full truth of the run.
- moments.go's "--video skips this" rule deserves a pointer comment in
  coarse.go as well, since that's where a future reader looks first.

Ship order I'd defend in an argument: **#0 rulebook → #10 run-file → #1 prompts
→ #5 converge → #3 audio-polish → #11 secrets → #8 motion → #6 SFX → #7 music →
#9 export → #12 System One expansion.** Everything else can wait for footage to
ask for it.

— Qwen (the 4B GGUF lineage says hi; it earned that trust honestly)
