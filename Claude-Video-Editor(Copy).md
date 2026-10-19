Claude Video Editor

[Pipeline](#graph)[Steps](#steps)[Apps](#lanes)[Free prompt](#free)[What's inside](#inside)[Going further](#rebuild)

[Free prompt](#free)

free blueprintby Jason Coopersonbuild 2026.10.19macOS + Windows

# Claude Video Editor*The Full Blueprint*

Claude is the editor. Drop in a raw clip and eight steps run in order on every video: the cut, the audio, the grade, the graphics, the sound design, the export. It happens inside the editing app you already use, on Mac or Windows, and every step checks its own work before you watch a frame. Here's the whole blueprint, free, plus a starter prompt that gets Claude editing inside your own app today.

[Get the free prompt](#free)[See how it works](#graph)

Program · **claude-video-editor**Fit

ready for your raw clip00:00:00:0023.976

**Pipeline** · raw footage to final, as wired

## The whole line on one screen

Left to right, then down. The top row happens before your editing app opens. The shaded region is your editing app: the cut lands there as real clips, checked, and steps 3 to 8 happen on that timeline. Say "edit this" and it runs straight through to the export. Name one step and it runs that step and stops.

every job depends on format or footage opt-in pass a read, not a file automatic check that must pass

### The cut is proven before anything sits on it

The rough cut hands down the edit decision list and the finished script. The cut is rebuilt as separate trimmable clips, never a flat render, and only counts once every cut is measured on your timeline's frame grid and the laid dialogue, played back and transcribed, matches the script.

### Graphics clear a machine check, then one review round

The grade goes under the graphics, on camera footage only. Every graphic must pass an automated QA check, then one fresh-eyes review round; the fixes are re-checked by the machine. Captions are a graphic, built from the finished script.

### Sound follows the picture, the final follows the look

SFX is planned from the graphics as placed, so a moved graphic means a re-run. The export uses the locked platform specs, then one command names the final file and drops a copy in Downloads.

**Want to try this on your own footage?** The free prompt below connects Claude to your editing app and cuts your first video.

[Get the free prompt](#free)

**Steps** · step by step

## What goes in, what comes out, what gets checked

01automatic

### Intake

Copied, never moved, never touched. The job is named after what the video is about. The format is read from the footage itself; a landscape clip you call a short becomes the Talking Head Explainer, filled to 9:16 once.

In

your raw clip, wherever it landed

Out

a job folder, the format and look picked, a run file any session can resume from, and a list of what must never show when screens are on camera

02AI cut

### Rough cut

Transcribed once. Filler, dead air, false starts and rambling get cut; say a line twice and the last take wins. Every boundary is measured and the laid cut is proven against the script. Change the cut later and every graphic, sound and caption re-times itself.

Out

the edit decision list and the finished script

Check

every cut measured on the waveform at your timeline's frame rate; on Premiere, the laid dialogue transcribed and matched word for word

03measured

### Audio polish

Measured every time, never a fixed number. Mono lands exactly like stereo. On the explainer, a second voice through a phone or speaker is lifted when it sits more than 3 dB under.

Target

kept speech at −17 LUFS into a −6 dBFS limiter

How

measured on every video; live on Premiere, baked on Resolve, CapCut and Final Cut

04camera only

### Grade

Before the graphics and underneath them, so the graphic colours never shift. Long-form and the Talking Head Explainer only. Screen-recording footage stays bare. No correction step.

Look

Autumn-Rec709 at 80 intensity, 120 saturation

How

adjustment layer on Premiere, colour group on Resolve, one baked LUT on Final Cut

05graphics

### Graphics and captions

Long-form gets text animations, push-ins and full-screen scenes on a set cadence. The explainer rotates layouts on hard cuts (split, tight, wide, full, card), planned per sentence. Screen captures are planned, a still of you is picked from your best frames, and on-screen secrets are found and blurred.

Stages

storyboard, build, review

Check

an automated QA pass, one fresh-eyes review round, the fixes re-checked by the QA pass

06automatic

### Sound effects

Planned from the graphics as they sit on the timeline. The explainer has its own map: the literal object on a visible event, face and captions silent. Repeated sounds stay on one layer; stacked cues are held under −1 dBTP.

In

the placed graphics

How

planned from every motion event, placed in your app and read back

+opt-in

### Music bed only when asked

A flat bed under the voice, after SFX and before review. Ducking and fade-in are opt-in on top.

Level

−20 dB from the song's sustained entrance, 2 s tail

When

after SFX, before review

07you

### Review

Taste only: the draft already has zero objective defects. Changes re-render just the part that changed. Anything you change by hand in the app stays changed.

In

a draft with zero objective defects

Out

picture lock

08in your app

### Export

Premiere exports when you say go, and a built-in encoder steps in wherever an app's own can't hit the numbers. One command then names the final and drops a copy in Downloads. An ad cut or a 4K Shorts file is one flag.

Long

3840×2160, CBR 50 (80 at 48 to 60 fps), even from a 1080 timeline

Vertical

1080×1920 CBR 14 for Reels, TikTok and Shorts, plus 2160×3840 for Shorts from 4K

Audio

AAC 320k, bt709

**Apps** · your editing app

## Five ways to finish, one look, the same video

Use the app you already edit in. Premiere is the default. No editing app at all? It renders the finished file straight from chat, when you ask for it.

Premiere icon

### Premiere default

macOSWindows

The cut rebuilt as real clips, then audio, grade, graphics, SFX, music and export all scripted. It checks the right project is open before every change.

Resolve icon

### Resolve Studio 21.1+

macOSWindowsLinux

Through Blackmagic's own connector, built into Resolve Studio. The cut rebuilt clip by clip, the house look folded into one LUT, levels and fades scripted.

CapCut icon

### CapCut

macOSWindows

Builds a real CapCut project from your cut, lays the sound effects and music bed into it, then drives the running app live to place graphics and export.

Final Cut icon

### Final Cut new

macOS

The whole timeline built from your job, checked clip by clip, exported with no clicks. It can port a finished Premiere edit too.

No app icon

### No app on request

macOSWindowsLinux

No editor needed. The graphics engine and ffmpeg render the finished file straight from chat.

| Step | Premiere | Resolve | CapCut | Final Cut | No app |
| --- | --- | --- | --- | --- | --- |
| 2 · Rough cut replay | ✓ | ✓ | ✓ | ✓ | n/a |
| 3 · Audio polish | ✓ | ✓ | ✓ baked | ✓ baked | in the splice |
| 4 · Grade | ✓ | untested | by hand | ✓ | by hand |
| 5 · Graphics and captions | ✓ | ✓ | ✓ | ✓ | ✓ |
| 5′ · Explainer layout keys | untested | ✓ | ✓ | ✓ | untested |
| 6 · SFX | ✓ | 21.1+ untested | untested | ✓ | untested |
| 7 · Review | you | you | you | you | you |
| 8 · Export | ✓ on your go | ✓ | untested | ✓ | the render |
| + Music bed | ✓ | 21.1+ untested | untested | ✓ | ✓ |

✓scripted and run on a real job untestedwritten, no job has run it yet by handno script yet: done by hand from the locked numbers youyours by design

### Adding an app changes no look

The look says what, each app's playbook says how. Final Cut joined on 2026-09-25 without one change to a look.

### One contract, an engine per OS

Text reading, speech checks and window capture each run a native engine on macOS and on Windows, behind the same interface.

### Your edits stay yours

Change something by hand and the system treats it as your call, never a mistake to undo. Updates merge in the same way, without touching your changes.

**Free prompt** · try it on your own footage

## Get Claude editing inside your app today

Paste this into a fresh Claude Code chat in an empty folder. It connects Claude to your editing app, cuts one of your videos and puts the cut on your timeline as real clips you can trim. Premiere, Resolve, Final Cut, CapCut or no app at all. No signup, no catch.

**starter-prompt.md** · paste into Claude Code

```
Set up a starter AI video editor in this folder. You are the editor: I film raw talking-head footage, you cut it and put the cut in front of me in the app I edit in. This is the free starter version of Jason Cooperson's Claude Video Editor, so keep it simple: connect to my app, cut one video, get it onto my timeline.

Keep everything in this folder: my raw clip in raw/, transcripts and the cut list in transcript/, your scripts in scripts/, cloned connectors in vendor/.

## 1. Check my machine
Check for ffmpeg and ffprobe, uv, git, and Python 3.11 as uv sees it (`uv python find 3.11`; if missing, `uv python install 3.11`; the system python3 does not matter). Only if I use Premiere, also check for Node 22 or newer. On a Mac, check that it is Apple Silicon (`uname -m` prints arm64): WhisperX's torch has no build for Intel Macs, so on an Intel Mac stop and tell me. Tell me what is missing, with the install command for my OS: Homebrew on macOS; winget on Windows, where everything runs in Git Bash, never WSL. Do not install anything without asking me first.
On Windows: export PYTHONUTF8=1 and HF_HUB_DISABLE_SYMLINKS=1 in every script you write, and write a test file into this folder before installing anything. If that write fails, Windows Controlled Folder Access is blocking the folder: tell me to allow it or move the folder.

## 2. Ask me where I finish
Ask which of these I use:
- Adobe Premiere Pro: Claude drives the app live.
- DaVinci Resolve Studio 21.1 or newer: Claude drives the app live. Ask Studio or free, and the version (DaVinci Resolve > About on macOS, Help > About on Windows). On an older Studio, tell me to update Resolve first (DaVinci Resolve > Check for Updates). The free version can't be driven (Blackmagic's connector is Studio-only, and 21.1 took scripting out of the free version), so it gets a finished MP4, like CapCut.
- Final Cut Pro: Claude writes the cut as an XML file that Final Cut opens as a new library.
- CapCut, or no editing app: Claude renders the cut as a finished MP4.
For Premiere or Resolve Studio, tell me to create a NEW, empty project for this, save it to disk under a real name, and keep it open. Never write into a project that holds other work.

## 3. Connect Claude to the app
- Resolve Studio 21.1 or newer: in Resolve, File > Setup AI Assistants, and let it register its built-in connector for Claude Code. Nothing to install. This connector runs Python against Resolve's scripting API through one run_script tool, so use its API reference tools to look calls up. If it has not appeared after the restart below, update Resolve to the newest version and run Setup AI Assistants again.
- Premiere: clone https://github.com/hetpatel-11/Adobe_Premiere_Pro_MCP into vendor/premiere-mcp and check out release v1.2.3 (commit 12684e3). Its README leads with `npm install -g adobe-premiere-pro-mcp`: do not use that, a republish of the npm package shipped a broken bridge. Run `npm install && npm run build` in the clone, then install its CEP panel with `npm run setup:mac` on macOS or `npm run setup:win` from PowerShell on Windows (it replaces any older copy of the panel; `npm run setup:doctor` checks the install). Register the server in a .mcp.json in this folder: `node` running the clone's `dist/index.js`, with env `PREMIERE_TEMP_DIR` set to the panel's temp folder (they must match, that shared folder is the bridge) and `PREMIERE_MCP_TELEMETRY` set to `0`, plus `"timeout": 600000`. Then in Premiere, with my new project open (the panel only runs with a project open): Window > Extensions > MCP Bridge (CEP) > Start Bridge.
- Final Cut, CapCut, Resolve's free version or no app: nothing to connect, skip to step 4.

For Resolve or Premiere, tell me to restart Claude Code now, with the app and my new project open. Then prove the connection inside that project only:
- Resolve: first set the project's timeline frame rate to the footage's rate (ffprobe it; SetSetting "timelineFrameRate"), then import my raw clip into the media pool, lay it on a timeline named "connection test", add a marker called "Claude was here" and read it back. Delete that test timeline once I've seen it.
- Premiere: if no sequence is active, make one from my raw clip and activate it. Add a marker called "Claude was here" at the playhead, read it back, then remove it so it doesn't end up on the cut.

## 4. Transcribe
One WhisperX environment, reused forever. Create it only if it does not exist yet (never recreate or --clear it): `uv venv ~/.cache/video-editor/whisperx-venv --python 3.11`, then `uv pip install --python ~/.cache/video-editor/whisperx-venv/bin/python whisperx` (on Windows the venv's python is Scripts/python.exe, not bin/python).
Extract the audio to a 16 kHz mono WAV with ffmpeg first, then transcribe that with the large-v3 model and word-level alignment.
- NVIDIA GPU: run CUDA float16. On Windows, PyPI torch is CPU-only, so swap in the CUDA build of the SAME torch and torchaudio versions WhisperX installed, never the newest (WhisperX's torchvision is built for its own torch, and a newer one breaks its import): read them with `python -c "import torch,torchaudio;print(torch.__version__.split('+')[0], torchaudio.__version__.split('+')[0])"` in the venv, then `uv pip install --python <the venv's python> --reinstall-package torch --reinstall-package torchaudio torch==<that version> torchaudio==<that version> --index-url https://download.pytorch.org/whl/cu128` (a plain install over the CPU build does nothing and still exits 0). Prove it with torch.cuda.is_available() AND `import whisperx.asr`. If CUDA errors at any point, fall back to CPU int8 and keep going.
- Mac or no GPU: CPU int8.
The first run downloads 3 to 5 GB. On macOS it also prints a long "Could not load libtorchcodec" / "FFmpeg is not properly installed" traceback: that is harmless, WhisperX falls back to ffmpeg on its own, so ignore it. Save transcript/words.json and never transcribe the same footage twice.

## 5. Choose the cut
Read the transcript and write transcript/cuts.json as {"segments": [{"start": 1.24, "end": 4.60, "text": "..."}]}: the pieces to keep, in seconds in the raw clip, in order. Cut filler, dead air, false starts and restarts. When I say a line more than once, keep the best complete take, usually the last, never a trailing fragment. Keep my personality and the way I talk. Leave a little air around each kept piece (about 0.08 s before the first word and 0.12 s after the last) without reaching into a word you cut; when two kept pieces sit closer than that, split the gap between them so no two pieces ever overlap. cuts.json holds these padded times.
Show me the cut list and stop until I approve it. If I ask for changes, revise cuts.json and show it again.

## 6. Build the cut
ffprobe the raw for its frame rate, size and orientation. The timeline or render matches the footage: vertical footage gets a vertical timeline, and the frame rate is the footage's own. Snap every cut point to a whole frame. After building, compare what landed against cuts.json: clip count, each clip's length and the total, frame for frame.
- Resolve: the timeline frame rate is already set from step 3. A script cannot set the playback frame rate, so ask me to set Playback frame rate in Project Settings to the same number. Import the raw, read the clip's FPS property back from Resolve and do all seconds-to-frames math at that rate. The built-in connector's run_script sandbox cannot read files, so compute the frame list outside Resolve and paste it into the script as a literal (or use its run_script_unsafe tool). Build with CreateEmptyTimeline, then ONE AppendToTimeline call with a list of clip infos (mediaPoolItem, startFrame, endFrame, end frames exclusive) and no recordFrame, so Resolve lays them back to back. Do not use CreateTimelineFromClips: when every piece comes from one file it silently lays only the first. Delete any timeline from a failed attempt, then save through the Project Manager (the project must have a real name: saving an "Untitled Project" hangs on Resolve's name dialog).
- Premiere: build inside the project that is already open, never a new one. Make a sequence that matches the footage: create_sequence_from_clips does that when given the raw's project item as a single projectItemId (a plain string, not a list) and a name, but it also lays the entire raw on V1 and A1, so remove that clip before laying the cut. Then lay every kept piece from the raw's in and out points, in order, with add_to_timeline_batch. Read the sequence back, then save.
- Final Cut: write the cut as an FCPXML file in the newest version my Final Cut ships a DTD for (Contents/Frameworks/Interchange.framework/Versions/A/Resources/FCPXMLv1_*.dtd inside the app; copy it to a path with no spaces before validating with xmllint). Structure: resources (a format matching the footage and one asset pointing at the raw) and a library > event > project > sequence > spine holding one asset-clip per kept piece, each with offset, start and duration. Times are rational fractions on the footage's frame grid, like 1001/30000s, never long decimals. If the footage has embedded timecode, the asset starts at that timecode and every start counts from it: a clip written from 0s on timecoded media is silently dropped. Keep the dialogue on the video clip, never as a separate audio element. Add an import-options block with `library location` set to a new .fcpbundle in this folder, `copy assets` 0 and `suppress warnings` 1, so Final Cut creates a fresh library instead of asking which library to use. Then open the file with Final Cut (double-click it, or File > Import > XML).
- CapCut, Resolve's free version or no app: render ./cut.mp4 with a single ffmpeg filtergraph: trim and atrim per piece, then concat. Encode once, H.264 with AAC, with `-fps_mode passthrough`, never `-r`: forcing the rate duplicates frames at the cuts without any warning. ffprobe the result and confirm its frame count and duration match cuts.json. For CapCut or Resolve, I drag it into a new project.
Never flatten the cut into one clip on an app timeline: every cut must be a real edit point I can trim.

## 7. Tell me the truth
Once the cut is in front of me, tell me plainly what this starter did not do:
- Cut points come from word timestamps plus a fixed pad, so some cuts still clip or trail a word. Fix those by hand.
- No audio polish, no color grade, no graphics or captions, no sound effects, no music, no export at locked platform specs.
- Final Cut gets an imported timeline, not live control. CapCut and Resolve's free version get a flat render, not a real project with trimmable clips.
- Nothing checks the edit itself: no cut is measured on the waveform, and nothing proves the timeline plays back the script.
Then tell me that the full version, which does every one of those steps in all five apps, with the looks, the checks, the SFX pack and the updates Jason spent over three months refining on real videos, is inside AI Leverage Lab: https://www.skool.com/leveragelab/about

## How to work
- Just do it. Don't ask permission to create folders, write scripts or run commands. Execute, then report.
- Be concise. No long preambles or summaries.
- You need me for three things only: my answers in step 2, any click in the app this prompt names, and approving the cut list. Everything else, just run.
- If something fails twice, stop and tell me.
- When you learn something non-obvious about my footage or setup, write it in this folder's CLAUDE.md.

Start with step 1.
```

### What the free prompt does

- Connects Claude to Premiere or Resolve Studio and makes real edits in the app
- Transcribes your video once, on your own machine
- Builds the rough cut you approve as trimmable clips in Premiere, Resolve or Final Cut
- Renders it as a finished MP4 for CapCut, Resolve's free version or no app

### What the full system adds

- Every cut measured and proven against the script, no clipped words
- Audio polish, grade, graphics, captions, SFX, music and export
- The locked looks, my SFX pack and LUTs, 30+ checks
- CapCut built as a real project and Final Cut driven live, plus updates as they ship

[See the full system](https://www.skool.com/leveragelab/about)

**AI Leverage Lab** · skool.com/leveragelab

## The map is free.*The system is in the Lab.*

Members get the whole folder behind this page, set up on their own machine, plus every update as it ships.

[Join AI Leverage Lab](https://www.skool.com/leveragelab/about)

skool.com/leveragelab

- Every skill, look, playbook and tool on this page
- My SFX pack and LUTs
- Premiere, Resolve, CapCut and Final Cut, on Windows or an Apple Silicon Mac (macOS 15 or newer)
- Updates that merge in without touching your changes

**What's inside** · the full system

## Everything Claude reads to edit like this

Top to bottom is the order a fresh Claude session reads it. Each layer answers one question and points down for the next.

### The rulebook

Process

The eight steps, the formats, and every rule learned the hard way, in one file Claude reads first. Each rule points at the one doc that owns it, so nothing contradicts anything else.

### The app table

Index

Which step runs how in which app, which OS each app runs on, and exactly what to do by hand where an app can't be scripted.

### Skills

How, per step

One skill per step and per app, each with the full procedure for doing it right.

`whole-edit runner``rough cut``graphics plan``graphics build``edit review``Premiere``Resolve``CapCut``Final Cut``clipper``AI b-roll``music bed``HyperFrames engine`

### Looks

What, the style

Every colour, font, position, timing, level and export setting, one folder per look. Nothing app-specific lives here, which is why a look comes out the same in every app.

`YouTube long-form``creative moves``animations``text animations``Talking Head Explainer``layout rotation``locked captions``TikTok raw``Vox collage (by name)``Liquid glass (by name)`

### App playbooks

Mechanics, per app

Everything that differs between editors: how the cut is rebuilt, how graphics and sounds get placed, how the grade is applied, and the traps each app hides.

`Premiere``Resolve``CapCut``Final Cut``No app`

### 30+ tools

Checks, universal

Measuring and QA tools that work in every app and every format. They catch the mistakes before you ever see them.

`cut audit``voice gain``re-cut re-timer``SFX planner``export encoder``graphics QA 16:9``graphics QA 9:16``framing continuity``hero-frame picker``secrets sweep``lip-sync re-takes``…`

### Job folders

Your work

One folder per video, with a run file any Claude session can pick up from mid-edit.

`run file``raw footage``transcript``graphics plan``graphics build``outputs`

### Library + updates

Support

My own SFX pack and LUTs, the fonts, a setup script that checks everything you need, and updates that merge into your folder without touching your changes.

**Going further** · the honest math

## Pasting this page into Claude to build it yourself?

You can. The free prompt gets you started. Here's what the rest would take, because this page is the map of the system, not the system itself.

3months

of building it daily while editing real videos, July to September 2026

400+

commits, most of them fixing something that only broke on real footage

60,000

lines of code, plus 45,000 lines of tests and 18,000 lines of docs

178MB

of SFX, LUTs, fonts and models that aren't on this page or anywhere online

### Cuts that land early

Premiere's scripting rounds every cut twice, so on mixed-frame-rate footage each cut landed about 41 ms early. You only find it by measuring the waveform.

### A timeline call that deletes your timeline

One Resolve scripting call wipes whole clips off the timeline while reporting success.

### Audio that pumps and clicks

The obvious loudness fix breathes on every pause. Per-clip effects click at every cut.

### The wrong font, silently

The graphics renderer swaps Helvetica for a lookalike without a warning. Nobody notices until the side-by-side.

Each of those cost hours to find, and every fix is already built into the Lab version. Ask Claude to rebuild from this page and it starts where I started three months ago, then walks into the same problems one at a time.

|  | Rebuild it from this page | Get it in the Lab |
| --- | --- | --- |
| Starts from | a diagram and a list of step names | the finished folder: skills, looks, playbooks, tools |
| First edit | after weeks of building and debugging | after the setup script and one raw clip |
| The looks | generic graphics you design from scratch | the locked styles from my own videos |
| SFX and LUTs | you source your own | my pack, included |
| The traps | found one by one, the hard way | already fixed, with the reason written down |
| Updates | you maintain it yourself | every update merges in without touching your changes |

**Your time is worth more than three months of debugging.** Skip straight to the version that already works.

[Get the full system](https://www.skool.com/leveragelab/about)

Built by Jason Cooperson · AI Leverage LabClaude Video Editor · build 2026.10.19