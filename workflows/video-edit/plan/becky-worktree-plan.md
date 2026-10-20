# becky worktree plan — the repo arranged as the pipeline

> **2026-10-20 - READ FIRST:** the to-do order is ../NEXT.md, the rules are ../RULEBOOK.md; both outrank this file. Step folders were renamed into run order, so translate old codes and paths here: S1=01_Intake, S1b=02_OBS-Sync, S2=03_Transcript, S3=04_Silence-Map, S4=05_Analysis, S5 (D-QWEN / D-VIDEO)=06_Content-Decision (A_Qwen-version / B_YouTube-version; D-S1 and D-STACK dropped), Q3 chat=07_Live-Chat, S6=08_Cut-Points, 4b=09_Visual-Moments, S7=10_Breath-Check, F07 publish=11_Publish-Check, S8=12_VEGAS-Build, S9=13_Edit-Check, F02=14_Audio-Polish, F03=15_Censor, F04=16_Zooms, F01=17_Grade, F05 sfx=18_Sound-Effects, F05 music=19_Music, F06=20_Quality-Check, S10=21_Markers, S11=22_Report, F08=23_Upload-Text, F09=24_Export. Paths written as workflows/video-edit/<old code>/ mean the matching folder above. RUN.json is a feature, not a folder. Never use these codes when talking to Jordan.

This is the layout Jordan asked for: **every step of the edit gets its own
folder, its own numbered label (matching WORKFLOW-DIAGRAMS.md and
becky-livestream-worktree.html), its own `JORDAN.md` (what Jordan knows that
the machine doesn't), and — where the step uses a model — its prompt as a plain
`.md` file he can edit without rebuilding anything.**

Rules:
- Folder names use the canonical step numbers. When something is wrong, say
  "S6" or "F3" — never write a novel.
- A step folder contains exactly: `README.md` (what runs, in/out artifacts),
  `JORDAN.md` (Jordan's inputs: folders, tastes, rules, examples), optional
  `prompt*.md` files (LLM steps only), and pointers to the code that implements
  it. No code moves; this layer wraps it. The tools read their config from here.
- `RUN.json` lives at the job root (`becky-edit\`) and is the resume pointer —
  see F10/S13.

```
becky-tools/
├─ RULEBOOK.md                     ← one file, read first; every rule → owning doc
├─ STEPS-AND-ENGINES.md            ← the table: step × deterministic / local model / paid / human
├─ workflows/video-edit/                       ← THE MAP (this layout; wrappers, not relocations)
│  ├─ S01-intake/            README.md · JORDAN.md ("where footage lands, naming")
│  ├─ S01b-obs-sync/         README.md · JORDAN.md ("which OBS quirks are known")
│  ├─ S02-transcript/        README.md · JORDAN.md (word-list.txt lives here) · PROMPT-corrections.md
│  ├─ S03-silence-map/       README.md · JORDAN.md ("mic type, typical level, margin taste")
│  ├─ S04-analysis/          README.md · JORDAN.md ("sounds I make that aren't breaths")
│  ├─ S05-decision/          ← THE BRANCH POINT
│  │   ├─ qwen/              PROMPT-lead.md · PROMPT-reviewer.md · PROMPT-context.md · JORDAN.md (guidance.md seed)
│  │   ├─ systemone-stack/   PROMPT-kind-of-talk.md · PROMPT-belongs.md · JORDAN.md
│  │   ├─ systemone/         JORDAN.md (voting notes)
│  │   └─ video/             PROMPT-exceptions.md · PROMPT-instructions.md · PROMPT-thinking-islands.md · PROMPT-viewer-read.md · JORDAN.md
│  ├─ S06-cut-points/        README.md · JORDAN.md ("never cut mid-word even if volume says so")
│  ├─ S07-breath-check/      README.md · JORDAN.md ("keep the sigh before 'So…'")
│  ├─ S08-vegas-build/       README.md · JORDAN.md (template veg, track naming)
│  ├─ S09-edit-check/        README.md · JORDAN.md ("what counts as lost word vs style")
│  ├─ S10-markers/           README.md · JORDAN.md ("marker colors = my review language")
│  ├─ S11-report/            README.md
│  ├─ V-video-extras/        ← --video-only finishing block (also runs on qwen branch once built)
│  │   ├─ F01-grade/         hj-starter.look.json · JORDAN.md ("grade BEFORE graphics — why")
│  │   ├─ F02-audio-polish/  audiopolish.look.json · JORDAN.md ("OBS mic = skip; else my pre-treat chain")   [BUILD #3]
│  │   ├─ F03-censor/        censor.look.json (sound path, −12.1 dB) · JORDAN.md
│  │   ├─ F04-motion/        motion.look.json (crop math per framing) · JORDAN.md ("how I ACTUALLY zoom: establish→punch on keyword→hold→release")  [BUILD #8 replaces cadence]
│  │   ├─ F05-assets/        sfx-map.json · music.look.json · JORDAN.md (SFX LIBRARY PATH + choice logic)   [BUILDS #6,#7]
│  │   ├─ F06-graphics-qa/   PROMPT-fresh-eyes.md · QA-CHECKS.md · converge loop                      [BUILD #5 — THE LOOP]
│  │   ├─ F07-publish-check/ README.md · JORDAN.md ("NEVER-show list moves to intake per BUILD #11")
│  │   ├─ F08-upload-copy/   README.md
│  │   └─ F09-export/        export.look.json (locked specs) · JORDAN.md (" Voukoder template names ")  [BUILD #9]
│  └─ S13-run-file/          README.md · RUN.json schema                                            [BUILD #10]
├─ becky-go/cmd/livestream/  (unchanged — the compiled spine; each module header points at its worktree folder)
├─ vegas/*.cs                (unchanged — the app hands; BeckyKeepList/BeckyMarks/BeckyFX gain new verbs per builds)
└─ workflows/                (bat files unchanged; add Worktree-Status.bat = print RUN.json + next step)
```

## How Jordan uses it
- **"The zooms look random"** → open `V-video-extras/F04-motion/JORDAN.md`,
  describe the real move grammar in plain words, rerun step F04 only
  (`becky-livestream --video x.mp4 --only F04`).
- **Want to change what the reviewer model is told** → edit
  `S05-decision/qwen/PROMPT-reviewer.md`. No rebuild. The tool loads prompts
  from disk; embedded defaults are fallback only.
- **Crashed mid-run** → double-click `Worktree-Status.bat`; it reads
  `becky-edit\RUN.json` and says "stopped after S07; resume with --resume".
- **Giving feedback to any agent** → name the folder. "S07 cut the breath but
  left the lip-smack at 01:12" is a complete bug report.

## Order of build (matches BUILD-PROMPTS.md numbering)
#0 rulebook+table (docs only) → #11 secrets-at-intake → #1 prompts-externalize
→ #10 run-file → #3 audio-polish → #5 QA-converge (biggest quality win) →
#6 SFX → #7 music → #8 motion-story → #9 export → #12 System One expansion.
