# Becky → VEGAS Pro

**Start with the Becky Search panel** (section 6): transcript search of your timeline and of any
footage folder, inside VEGAS, plus the `becky-vegas` control channel that lets Claude Code, becky
tools and Whoretana drive the open VEGAS. The scripts below still do their own jobs and the panel
has buttons for the two you use most.

| Piece | What it does |
|---|---|
| **`BeckyVegas/` (extension)** | **View ▸ Extensions ▸ Becky Search.** Type what was said, find every place it is spoken - on the timeline or in a folder - double-click to jump there or pull that line onto the timeline. Also the `\\.\pipe\becky-vegas-<pid>` control channel (`becky-vegas.exe`). **Section 6.** |
| **`BeckyCaptions.cs`** | Captions the edit you already have open — transcribes with becky and lays one text event per caption on a "Becky Captions" track. **Start here for captions.** |
| **`BeckyCut.cs`** | Cuts the dead air out of the events you have **selected**, in place, on the timeline you already have open. becky-cut decides where; this splits, deletes and ripples the gap closed. **One click - no dialog, no knobs, same answer every time.** One Ctrl+Z puts it back. **Start here for jump-cutting a selection.** |
| `BeckyReviewTimeline.cs` | Builds a *review* timeline from a list of forensic hits (path + in/out), each as a named Region. Nothing to do with captions. |
| **`BeckyRoughCut.cs`** | The unattended rough-cut assembler: reads `BECKY_ROUGHCUT_JSON`, builds video+audio tracks with paired events, markers and regions, saves the `.veg`, exits. `vegas180.exe -SCRIPT:<path>` + the env var = fully headless. It does NO thinking - all of it happens upstream. **Do not drive it by hand: run `Build Rough Cut.bat` (or `scripts/roughcut.py --launch-vegas`), which writes the JSON and launches this.** The JSON now comes from `scripts/build_roughcut.py`, not the older `becky-roughcut` Go path. Recipe and calibration targets: `SKILL.md` `# ROUGH CUT`. |
| `BeckyVerifyProject.cs` | Reads a `.veg` back headless (`BECKY_VERIFY_VEG=<path>`) and writes `<path>.verify.txt` with track/event/marker/region counts and length — the proof a delivery actually landed. |

---

# 0. Before you touch any script here — VEGAS Pro 18 gotchas

Every one of these cost real time on a real run. Read this before editing, not after the
next one bites. Machine is VEGAS Pro 18 (`ScriptPortal.Vegas`), scripts are plain C# compiled
by VEGAS itself at launch — there is no build step, and no error until a full launch + run.

**Compile-check before you ever run it:** `vegas\check-vegas-script.ps1`. One wrong character
and VEGAS reports nothing until it launches, runs your script, and pops a dialog — the checker
catches that in seconds instead of a full VEGAS boot. It has already caught a stale call site.

**Probe the scripting API, never guess a method exists because it "should".** Jordan's own
words: "one wrong character breaks these scripts completely." Confirmed traps, all found by
trial:
- `Tracks.Insert(0, t)` **compiles** and throws `NotSupportedException` at **runtime**. To add a
  topmost track: `new VideoTrack(project, 0, name)` then `Tracks.Add(t)`.
- `vegas.SaveSnapshot()` returns fully transparent (blank) frames when VEGAS is driven by
  `-SCRIPT` (headless). You cannot measure on-screen size/appearance this way in headless mode.
- `Timecode` has **no `.Seconds` property.** Use `.Nanos * 1e-7` to get seconds as a double
  (`BeckyVerifyProject.cs` and `BeckyRoughCut.cs` both do this).
- `Vegas.ScriptArgs` **does not exist** in this API surface. Pass data in with environment
  variables instead (`Environment.GetEnvironmentVariable(...)`) — every script here does this:
  `BECKY_ROUGHCUT_JSON`, `BECKY_VERIFY_VEG`, `BECKY_REVIEW_LIST`.
- `AudioTrack.Volume` is a **float**, linear (not dB) — convert with
  `(float)Math.Pow(10.0, db / 20.0)`. Per-clip `Normalize` on individual audio events was tried
  once and **hung VEGAS for 25+ minutes** on a real project; a single `AudioTrack.Volume` set at
  the measured gain is the one that actually ships.
- **A `.cs` that fails to compile pops the exact same dialog as a corrupt project file** — if a
  headless `-SCRIPT` run silently produces nothing, check the script compiles before suspecting
  the project or the data.

**A VEGAS script is compiled against a SMALL assembly set, and `csc` will lie to you about it.**
VEGAS references roughly what a bare console app gets: `mscorlib`, `System`, `System.Core`,
`System.Drawing`, `System.Windows.Forms`, `System.Xml`, `ScriptPortal.Vegas`. Anything else -
`System.Web.Extensions.dll` (`JavaScriptSerializer`), `System.Net.Http`, etc. - is **not** there.
The trap: `csc.exe` silently reads `csc.rsp` from the .NET Framework folder, which references about
thirty extra assemblies **including** `System.Web.Extensions.dll`. So a script can compile-check
perfectly here and still die inside VEGAS with:

```
BeckyCut.cs(39) : The type or namespace name 'Script' does not exist in the
namespace 'System.Web' (are you missing an assembly reference?)
```

That false green is exactly what shipped two unusable scripts to Jordan (2026-09-10) - the checker
said OK, so nobody looked again. `check-vegas-script.ps1` now passes **`/noconfig`**, so it sees the
same small set VEGAS does and reproduces that error in about a second. **Do not remove
`/noconfig`.** If you need an assembly VEGAS does not load, either (a) don't - `BeckyCaptions.cs`
and now `BeckyCut.cs` both read becky's JSON with a plain `Regex` and stay single-file - or
(b) ship a `<script>.cs.config` sidecar next to the `.cs`:

```xml
<?xml version="1.0" encoding="UTF-8" ?>
<ScriptSettings>
  <AssemblyReference>System.Web.Extensions.dll</AssemblyReference>
</ScriptSettings>
```

and make sure the installer actually copies it. `BeckyRoughCut.cs.config` has existed all along and
was being left behind, which is exactly why BeckyRoughCut ran from the repo path and failed from the
Tools menu with the same `System.Web` error.

**Scripts do NOT have to live in `C:\Program Files`, and should not.** VEGAS looks in seven folders
(its own FAQ, "1.10: How do I add a script to the Scripting menu?" in
`vegasprodata\VEGASScriptFAQ.html`). The per-user one - `C:\Users\<you>\Documents\Vegas Script Menu\`
- needs **no administrator rights**, so there is no UAC prompt on every update. Measured on VEGAS
Pro 18, 2026-09-10:

- Tools > Scripting lists **one entry per script NAME**, not one per copy.
- The **per-user copy wins**. A stale `BeckyCut.cs` that could not even compile was sitting in
  `Program Files` at the time; once the good copy was in Documents, the menu item ran that one.
- `Tools > Scripting > Rescan Script Menu Folder` picks up a change **without restarting VEGAS**.

`Install Vegas Scripts.bat` installs there and no longer elevates at all.

One consequence to know before you "fix" it: **`check-vegas-script.ps1` reports COMPILE FAILED for
`BeckyRoughCut.cs`, and that is correct.** The checker deliberately does not know about `.cs.config`
sidecars, so it sees the bare reference set. BeckyRoughCut is fine in VEGAS (confirmed 2026-09-10:
it opens its "Becky: choose vegas_cut.json" picker from the Tools menu). Every other script here
must be COMPILE OK.

**Never force-kill VEGAS** (Task Manager "End Task", killing the PID, etc). It leaves a
process holding port 2015, and every later launch shows a blocking "VegasAIBridge failed to
start HTTP server" dialog that has to be dismissed before anything else can run. Close it with
`WM_CLOSE` (or the normal window close) and answer the save prompt with **No** if the project
was only a headless throwaway.

**UPDATE 2026-09-14: VegasAIBridge is RETIRED.** It was not third-party - it was an earlier
homemade attempt (source: `X:\Videos\video_tools\vegas-ai-agent\vegas-extension\src\`), and it
never worked for its own reason, not VEGAS's (section 6). `Install Vegas Scripts.bat` moves both
copies to `%LOCALAPPDATA%\BeckyVegas\retired\<date>\` (with a note saying where each came from),
so the port-2015 dialog below no longer happens on this machine. The history is kept because it
explains old handoff notes.

**A "VegasAIBridge" plugin was installed on this machine** and threw its own
port-conflict error dialog on startup. Measured 2026-08-25: on a machine with any earlier VEGAS
session still around, this fires on EVERY fresh `vegas180.exe -SCRIPT:` launch, not just
"sometimes" - and it is a REAL modal dialog, not suppressed by launching from Go with
`proc.NoWindow(cmd)` (that flag only affects console-window allocation; it does nothing for a
GUI app's own windows). A `-SCRIPT` launch is therefore never actually invisible - it always has
a normal window, and if this dialog is sitting on top of it unattended, the script never gets to
run: no buildlog.txt, no .veg write, and the calling process may exit "successfully" anyway
because `launchVegasPro`/`cmd.Start()` returns as soon as the process SPAWNS, not when it
finishes (see `-vegas-only`/`-narrative-trim` below) - this produced a silent, no-error failure
that looked identical to success in the calling tool's exit code. It is unrelated to any becky
script - dismiss it and move on, don't debug becky's code because of it. To dismiss
programmatically instead of by hand: `EnumWindows` for a visible window on the target PID whose
title contains "VegasAIBridge", `PostMessage(hWnd, 0x0010 /*WM_CLOSE*/, 0, 0)`. After a headless
launch, always confirm the actual work happened (buildlog.txt / verify.txt mtime, not just the
launcher's exit code) before trusting it - the launch call finishing is not the build finishing.

**Scripts only appear in the Tools ▸ Scripting menu from VEGAS's own Script Menu folder**
(`C:\Program Files\VEGAS\VEGAS Pro <ver>\Script Menu`) — a `.cs` sitting in this repo is
invisible to VEGAS until copied there. `Install Vegas Scripts.bat` (repo root) self-elevates
and copies every `.cs` here into every installed VEGAS version's Script Menu folder in one
click — re-run it after editing any script.

**VEGAS Pro 18 cannot import OTIO or FCPXML.** Confirmed by trying: its only interchange
imports are AAF and Final Cut 7 XML (export-only for FCPXML/OTIO). This is why
`BeckyReviewTimeline.cs`/`BeckyRoughCut.cs` build the timeline directly through the scripting
API instead of writing a file for VEGAS to import. Its own FCP7 XML importer is also not fully
robust — a hand-rolled FCP7 export previously crashed it (`Fcp7Importer.ImportFrameRate` NPE);
prefer the scripting-API route over any file-import route for anything becky generates.

**If you ever run any of these on VEGAS Pro 13 or older** (Sony branding, not ScriptPortal):
change `using ScriptPortal.Vegas;` to `using Sony.Vegas;` at the top of the file. His
UltraPaste extension is built against ScriptPortal.Vegas **22**, not this machine's 18 — don't
assume its patterns transfer; probe fresh against 18.

**The caption STYLE lives in a VEGAS preset, not in code.** `becky-captions` is a Titles & Text
preset Jordan built by hand in the VEGAS UI; apply it with `effect.Preset = "becky-captions"`
and only swap the words in the RTF. Never rebuild the look from OFX parameters directly — that
is what made an earlier attempt's captions behave unlike every other title in the project. Also
never put a colour in the RTF itself — Titles & Text renders the RTF in its edit box on a white
background, so a baked-in `\cf` white made captions invisible while editing; colour belongs in
the `TextColor` OFX parameter instead.

---

# 1. `BeckyCaptions.cs` — captions on the timeline, timed by becky

Adapted from **[louismathy/vegas-script](https://github.com/louismathy/vegas-script)**
(`WhisperAutoSubtitles.cs`). His style dialog with the live preview, the progress
window, the RTF text handling and the placement loop are kept as they were.

## What changed, and why

1. **Transcription → becky.** The original shells out to `whisper`. This calls
   `becky-subtitle`, which asks **`becky-captions`** first whether a trustworthy
   official transcript already exists for the media — and refuses one that is
   short because the stream was YouTube-edited — before spending an ASR run.
2. **Chunking → becky.** The original splits every N words, which is why lines
   break mid-thought. becky's chunker (`internal/subs`) breaks where the speaker
   actually **pauses**, caps a line at **22 characters**, never ends a line on a
   dangling "a"/"the"/"to", floors every caption so none is a one-frame flash, and
   closes every gap so nothing blinks off between two captions.
3. **Your edit is read, not ignored.** This is the real fix. The original
   transcribes one media file and lays captions from 0, so the moment you cut
   anything the words drift away from the picture. This hands becky **every event
   on the track** — the source file, the `[in,out]` of that source, and where the
   event sits on the ruler — so captions are snapped to *your* cuts and placed
   back at the right ruler position, **gaps included**.
4. Whisper's model / language / split-mode prompts are gone; becky decides those.
   The style dialog is untouched.

## Before you run it

`becky-subtitle.exe` and `becky-transcribe.exe` must exist — run
`build-all-tools.bat`. The script finds them by, in order: `BECKY_SUBTITLE`,
`..\becky-go\bin\` relative to this script, then `PATH`.

## Install it (do this once, and after every update)

**VEGAS only lists scripts that live in its own `Script Menu` folder**
(`C:\Program Files\VEGAS\VEGAS Pro <ver>\Script Menu`). A script sitting in this
repo will not appear in the menu.

Double-click **`Install Vegas Scripts.bat`** at the repo root and click **Yes** on
the Windows prompt. That folder is under `C:\Program Files`, so the copy needs
administrator rights — the prompt is Windows asking for them, and it is the only
click involved. The installer finds every installed VEGAS Pro version and copies
every `.cs` in this folder into each one.

## Run it

1. Open your edit in VEGAS.
2. **Select the events you want captioned.** Select nothing and it captions the
   whole first video track that has media. Empty timeline → it asks for a file.
3. **Tools ▸ Scripting ▸ BeckyCaptions**.

That is the whole interaction. **No style dialog, no "done" box** — one click,
then captions on the timeline. A progress window counts up while it works (the
first run on a file has to transcribe it; after that the transcript is cached
beside the clip and a re-run is near-instant) and closes itself. The only box you
will ever see is an error, if something actually fails.

Captions land on a track called **Becky Captions**, created as the **topmost**
track so nothing hides them.

## Notes

- **Re-running replaces, it does not stack.** The script clears the existing
  "Becky Captions" track rather than adding a second one.
- **The `.srt` lands beside the clip**, as `<clip>.becky.srt` — same folder as the
  video, so you can burn it later or hand it to another tool. It is deliberately
  **not** named `<clip>.srt`: becky-captions treats `<stem>.srt` next to a video
  as an *official* transcript, so writing there would make the next run mistake
  becky's own output for an official subtitle, skip transcription, and collapse
  each cue into a single caption. If the clip's folder cannot be written (a
  read-only or protected evidence drive) it falls back to the temp work folder.
- **The style is fixed**, matching becky-review-3: Proxima Nova, white text, thin
  black outline, no shadow. Change the constants at the top of the style section
  in `BeckyCaptions.cs` — `BeckyFontPointSize` is the one to touch if the
  captions come out too big or too small.
- **becky's model review pass is OFF**, matching becky-review-3 (Jordan
  2026-07-24, "pause the llm step"). The deterministic pace chunker already
  honours every caption rule. Leaving it on is what made the first VEGAS run sit
  through two 90-second OpenCode Zen timeouts before falling back to exactly the
  same captions. Set **`BECKY_CAPTIONS_REVIEW=1`** to opt back in.
- **Speed-changed events** are handled (the in/out is scaled by the playback
  rate), but heavy time-stretching will still drift — captions are timed off the
  source audio.
- **VEGAS 13 or older:** change `using ScriptPortal.Vegas;` to `using Sony.Vegas;`.

## The seam, if you are debugging it

The script writes a timeline JSON, becky writes a cues JSON, the script places
the cues. Both files are left in `%TEMP%\BeckyVegasCaptions\<guid>\` along with
`becky.log`.

```bat
becky-subtitle --timeline timeline.json --cues cues.json --out captions.srt --verbose
```

`timeline.json` in, `cues.json` out — and the times in `cues.json` are **Vegas
ruler seconds**, already mapped back through the gaps in the edit, so the script
places each event at `cue.start` with no arithmetic of its own. The contract is
`becky-go/internal/edl/vegastimeline.go`; the placement maths is proven offline
by `becky-subtitle --selftest`.

---

# 2. `BeckyReviewTimeline.cs` — the forensic review timeline

`BeckyReviewTimeline.cs` lets you **review becky's forensic clip hits immediately in
VEGAS Pro 18** — the editor you already know — while we decide the long-term host.
becky finds the moments; this script lays them end-to-end on a VEGAS timeline, each
as a named **Region** you can jump between.

This is the same pattern becky already uses for REAPER (becky emits a project the host
opens). VEGAS Pro 18 **cannot import OTIO or FCPXML** (confirmed — its only interchange
imports are export-only AAF / Final Cut 7 XML), so the script builds the timeline
directly through VEGAS's scripting API instead of relying on a file import.

---

## 1. The "review list" — the one thing you feed it

A plain text file (`.txt`), one clip per line:

```
# path                  | in        | out       | label (optional)
C:\Videos\cam1.mp4      | 65.0      | 73.5      | cat closeup - chipped tooth?
C:\Videos\cam2.mp4      | 00:02:00  | 00:02:08  | cat near camera
E:\evidence\clip.mov    | 1320.25   | 1331.0    |
```

- **path** — full Windows path to the source video. The original is only ever READ.
- **in / out** — either **plain seconds** (`73.5`) or **colon time** (`MM:SS`,
  `HH:MM:SS`, optional decimals like `HH:MM:SS.250`). Mix freely.
- **label** — optional; shown as the Region name. Blank → the file name is used.
- Lines starting with `#`, and blank lines, are ignored.

You can write this by hand, or have becky emit it (next section).

## 2. Getting the list from becky

becky-clip already produces a `Reel` JSON / EDL for a set of hits. The planned
`becky-otio` tool (see `SPEC-BECKY-OTIO.md`) adds a `--format vegas-list` output that
writes exactly this file from a Reel. Until that ships, you can convert any Reel JSON
by hand or with a one-liner — the only fields needed are each clip's `source`, `in`,
`out`, and `label`.

## 2b. The repeatable, agent-driven flow (no human clicking)

This is the loop for "the forensic agent hands becky a list of videos + timestamps and it
lands on the Vegas timeline":

1. **Agent produces the list.** Either it has a Reel JSON and runs
   `becky-otio --reel findings.json --format vegas-list --out C:\case` (writes
   `findings.review.txt`), or it writes the `path | in | out | label` text file directly.
2. **Agent points the script at the list and launches Vegas — no dialog:**
   ```bat
   set BECKY_REVIEW_LIST=C:\case\findings.review.txt
   "C:\Program Files\VEGAS\VEGAS Pro 18.0\vegas180.exe" -SCRIPT "C:\...\vegas\BeckyReviewTimeline.cs"
   ```
   The script reads `BECKY_REVIEW_LIST`, skips the picker, and builds the timeline automatically.
3. Vegas opens with the clips on the timeline + a named region per clip.

So `BECKY_REVIEW_LIST` set → fully automatic (agent use). Unset → file picker pops (human use).
Same script, both ways.

## 3. Run it by hand (no compiling, ~10 seconds)

1. Open VEGAS Pro 18.
2. **Tools ▸ Scripting ▸ Run Script…** and pick `BeckyReviewTimeline.cs`.
3. In the file dialog that pops up, choose your review list `.txt`.
4. Done — the clips are on the timeline, each with a named Region. A summary box
   tells you how many were placed and lists anything skipped (missing file, bad
   times, etc.).

**To pin it in the menu** (so it's one click next time): copy `BeckyReviewTimeline.cs`
into `C:\Users\<you>\Documents\Vegas Script Menu\` and restart VEGAS. It then appears
under **Tools ▸ Scripting**.

## 4. What you get

- One **video track** + one **audio track**, named "Becky Review …".
- Every clip trimmed to exactly its `[in, out]` and butted end-to-end in list order.
- A named **Region** over each clip → jump candidate-to-candidate from the Regions
  window or the region markers on the ruler (no blind scrubbing).
- The playhead parked at 0 so you can press play and walk the candidates.

## 5. Notes / limits (honest)

- **Review, not a finished edit.** It assembles candidates for your eyes; trim,
  reorder, or delete in VEGAS as normal — the script never locks anything.
- **Audio-only or silent-video clips are fine** — the script places whichever
  streams exist and skips a line only if neither decodes.
- **Frame rate:** clips are placed by *time* (seconds), so mixed-fps sources line up
  correctly. VEGAS uses its current project frame rate for display; set the project
  to match your main footage if you want the ruler timecode to read cleanly.
- **If you ever run this on VEGAS Pro 13 or older** (Sony branding), change the line
  `using ScriptPortal.Vegas;` to `using Sony.Vegas;`. For VEGAS 14–22 leave it as is.
- **Snappiness:** VEGAS scrubs long-GOP H.264/HEVC the same way every NLE does — if
  a clip stutters, it's the source codec, not VEGAS. See `HANDOFF-PROXY-SNAPPINESS.md`
  for the intra-frame proxy fix (it applies to VEGAS too).

---

# 3. `BeckyRoughCut.cs` — the unattended rough-cut assembler

`becky-roughcut` (`becky-go/cmd/roughcut`) does every editorial decision offline: silence
jump-cuts against the transcript, abandoned-retake detection, quote splices, zero-crossing
snaps, marker placement. It writes `vegas_cut.json` with FINAL timeline positions already
computed. This script does **no thinking at all** — it is the dumb assembler: exactly four
tracks (his video, his audio, quotes video, quotes audio), events placed at the positions the
JSON already says, markers and regions added, project saved, VEGAS exits. Quotes play
**sequentially** — the main edit stops, the quote plays on its own tracks, the main edit
resumes — never layered on top of his voice.

## Run it

**Agent / walk-away (the normal path):** `becky-roughcut <project-dir> --launch-vegas` does
this automatically — sets `BECKY_ROUGHCUT_JSON` and launches `vegas180.exe -SCRIPT:<this
file>` for you. You should not normally invoke this script by hand.

**By hand, for debugging one script in isolation:**
```bat
set BECKY_ROUGHCUT_JSON=C:\path\to\_roughcut\vegas_cut.json
"C:\Program Files\VEGAS\VEGAS Pro 18.0\vegas180.exe" -SCRIPT:"X:\AI-2\becky-tools\vegas\BeckyRoughCut.cs"
```
With no env var set (or the path missing), a file picker asks for `vegas_cut.json` instead.

## What it does, concretely

1. Sets project width/height/fps from the JSON if given.
2. Adds `Rough Cut (video)` + `Rough Cut (audio)` tracks; sets the audio track's `Volume` to
   the JSON's measured `audio_gain_db` (linear conversion — see the gotchas section above).
3. Places every event from `events[]` at its given `tl` (timeline seconds), `in`/`out` sourced
   from the original media — **source files are only ever read, never modified.**
4. If `quotes[]` is non-empty, adds `Quotes (video)` + `Quotes (audio)` tracks and places those
   too — already pre-spliced into the gaps by the Go side, this script just places what it's
   told.
5. Adds every `regions[]` entry as a named Region and every `markers[]` entry as a Marker
   (both wrapped in `try/catch` individually — one bad marker never aborts the whole build).
6. Saves to `save_path` from the JSON, exits.

## Debugging a build that placed fewer events than expected

Every run writes `<vegas_cut.json path>.buildlog.txt` beside the input JSON — a plain-text
log of every event/quote **skip** (with the source file and timeline position) plus the final
`placed: N of M` / `quotes placed: N` counts. Check this file first; it tells you exactly
which source failed to open as `Media` (usually a bad/moved path) rather than making you
compare event counts by eye.

## Marker text can change between runs — `--triage-markers`

Some markers (`CHECK: ...`, `RETAKE? ...`) come from `becky-roughcut`'s own review/retake
detectors, not from a human. Before this script ever sees them, they can be re-reviewed by
Gemma-4 (`becky-roughcut <dir> --triage-markers`, standalone, run once the GPU is free of any
other model) — a marker Gemma-4 can confidently resolve is dropped from `vegas_cut.json`
before `BeckyRoughCut.cs` ever runs; one it can't gets annotated with the model's own read
(`[gemma4: ...]` appended to the title) so you see its take right on the timeline marker
instead of having to ask the same question yourself. This script itself needs no changes for
this — it only ever reads whatever `markers[]` is in the JSON at launch time.

## The cut is finished but still too long — `--narrative-trim`

Jordan, 2026-08-25, after a finished 86-minute cut: "86 minutes is too long - i REFUSE to
human review that until it's less than an hour. build whatever the fuck you need." Dead-air
removal (`speakingConfidentCuts`, `confidentcuts.go`) only removes spans where nothing is
there at all - it cannot shorten a cut that is simply full of genuine but REDUNDANT talking.
`becky-roughcut <dir> --narrative-trim --target-minutes 58` (standalone, run after
`--triage-markers`, GPU free) has Gemma-4 read the whole remaining narration in ~30s beats and
mark ONLY the beats it is confident are a repeated point, a tangent, or filler - never a new
fact, name, date, or accusation - then removes just those, closes the gap, and reflows the
quote clips and surviving markers onto the shorter timeline (`narrativetrim.go`). Every cut is
logged to `narrative_trim.json` (text + reason) so nothing is silent.

**This WILL over-cut if you trust the prompt alone - it needs a hard code-level ceiling.**
Measured on the real footage: with the model told the running total and asked to stay
conservative but never told to STOP, it cut 167 of 191 beats anyway - 86.1min down to 15.5min,
over 3x more than the ~23min actually needed, because nothing forced it to stop once the
target was already long satisfied. `judgeNarrativeBeats` now hard-stops calling the model the
moment `cutSoFar` meets `totalSec-targetSec` - every beat after that point is left at
`cut:false`, untouched, by construction. Don't relax that stop condition to "let the model
decide when it's done."

---

# 4. `BeckyVerifyProject.cs` — headless proof a build actually landed

Opens a saved `.veg` **read-only** and writes `<path>.verify.txt` with track count, video/audio
event counts, marker count, region count, total length in seconds, and the first/last source
filenames referenced — everything needed to prove a delivery landed correctly **without**
opening the VEGAS UI. This is the automated half of "did the build work"; a screenshot + manual
look is still the honest final check for anything visual.

## Run it

```bat
set BECKY_VERIFY_VEG=C:\path\to\rough_cut.veg
"C:\Program Files\VEGAS\VEGAS Pro 18.0\vegas180.exe" -SCRIPT:"X:\AI-2\becky-tools\vegas\BeckyVerifyProject.cs"
```
No env var (or a missing path) → a file picker asks for a `.veg` instead. Any failure (bad
project, VEGAS API exception) is caught and written as a `FATAL: <message>` line in the output
file rather than left as a silent empty result.

## Reading the output

```
project: C:\path\to\rough_cut.veg
tracks: 4
video_events: 1265
audio_events: 1265
markers: 71
regions: 16
length_seconds: 5192.7
first_source: HJOC7106.MP4
last_source: SNOW_20260823170959.mp4
```
`video_events` should equal `audio_events` for a becky-roughcut build (every placed clip has
both streams). `tracks: 4` with quotes present, `tracks: 2` without them (the quotes tracks are
only added when `quotes[]` is non-empty — see `BeckyRoughCut.cs` above). Markers well below the
count in `pending_markers.json` + any caller-supplied markers usually means some didn't map
onto the timeline at all (check `mapToTimeline` in `becky-go/cmd/roughcut/main.go` — this is
exactly the class of bug the 2026-08-24 stem-mismatch fix caught, where every dynamically
generated marker silently vanished and the count never moved off a stale baseline).

---

# 5. `BeckyCut.cs` — cut the dead air out of what you have SELECTED

The jump-cut tool. It works on the edit you already have open, in place: the source files are only
ever READ, nothing is re-encoded, nothing outside the tracks you selected moves.

## Run it

1. Open your edit.
2. **Select the events you want cut.**
3. **Tools > Scripting > BeckyCut**.

That is the whole interaction. **There is no dialog and no knob, on purpose.** One click, the same
answer every time. The thresholds were dialled in over nine months and they live inside `becky-cut`;
a number asked for here could drift away from the tested one and would make the result depend on
what got typed. If a number needs changing it changes in `becky-cut`, once, for every caller.
**Do not add a dialog back.**

There is no "done" box either — the shorter, gapless clips on the timeline are the confirmation.
**One Ctrl+Z puts the whole thing back.** Select nothing and it says so rather than guessing at a
track.

## What it does, exactly

1. `becky-cut <file> --dry-run` on each distinct source file — silence cut against a threshold
   measured from *that* recording's own room tone versus its own speech
   (`becky-go/cmd/cut/level.go`), then a second pass with a Silero VAD so noise with nobody
   actually talking in it is cut too. Nothing is rendered. **That is the entire edit** — this script
   invents no threshold, no padding and no minimum gap. It is an applicator, not a second opinion.
2. Every cut span is mapped from source seconds onto the ruler through the event's own in-point and
   playback rate, snapped to the frame grid, and merged into **one** span list.
3. Each span is removed and the hole is **closed** — auto-ripple, always.
4. Grouping is rebuilt, one group per clip, over just the fragments of your selection.

## The two rules that make it correct

**The cut points are decided ONCE, from the AUDIO, and applied to every selected event at the same
ruler positions.** On Jordan's timeline the picture is a camera file (`IURJ0280`) and the sound is a
*different* file from a separate recorder. An earlier version analysed each event's own source, so
the picture got cut where the camera mic was quiet and the sound got cut where the recorder was
quiet — two different edits on two grouped tracks, producing video with no sound and sound with no
picture. Per-event decisions can never be safe on a dual-system timeline. If any audio event is in
the selection, the audio decides; with no audio selected it falls back to what there is.

**Grouped partners come along whether you clicked them or not.** Selecting one half of a grouped
video+audio pair puts the other half in the edit too, because "a cut needs to affect both of them
the same".

**Only one microphone gets a vote on any given moment.** If two audio events cover the same stretch
of ruler — a camera scratch mic and the real recorder, both grouped to the same picture — their
noise floors differ, so becky returns two different cut lists. Merging them would mean "cut wherever
*either* was quiet", which lets the wrong list delete speech. The ranking is deterministic: an audio
file that is not also a picture source wins (that is the dual-system signal), then the longer event,
then the earlier one, then the path.

## Grouping — why the cut rebuilds it

**VEGAS leaves every fragment of a cut clip in one giant group, and that is not what you want.**
Splitting a grouped event leaves *both* halves in the *original* group, so fourteen cuts turn one
video+audio pair into a single group of thirty events — delete any one of them and the whole edit
goes with it. Jordan, after the first working run:

> "if I try to delete a clip, it deletes ALL the clips because, even though they are separate events
> on the timeline, they are grouped as a single group, which is meaningfully different than grouping
> each clips audio to the corresponding video."

His manual fix was two steps — "remove from group", then Vegasaur's **Make Groups** button — and the
script now does exactly those two steps natively at the end of the cut, so no third-party extension
is needed. Old membership is stripped first, then one fresh group is built per column.

Scope is strict: only fragments descended from **what you selected** are touched, tracked by lineage
through every split. And a clip that was **not** grouped before is left ungrouped — inventing groups
nobody asked for would be its own surprise. The regrouping is inside the same undo block as the
cuts, so one Ctrl+Z restores both.

**Use VEGAS's own grouping idiom, exactly.** A first attempt threw
`Error HRESULT E_FAIL has been returned from a call to a COM component`. It used
`new TrackEventGroup(project)`, detached events one at a time with `group.Remove(event)`, and swept
up empty groups with `RemoveAt`. VEGAS's stock **`Group Video and Audio Events.cs`** — shipped in
this same Script Menu folder — does none of those things:

```csharp
TrackEventGroup grp = new TrackEventGroup();        // PARAMETERLESS
vegas.Project.TrackEventGroups.Add(grp);            // add to the project FIRST
grp.Add(videoEvent);                                // then put events in it
grp.Add(audioEvent);
```

Follow that. To detach, **dissolve the old group** (`Project.TrackEventGroups.Remove(group)`) rather
than picking events out of it — that is also the closer match to the "remove from group" button. An
event cannot be in two groups at once, so nothing can join a new group until the old one lets go.

A group is dissolved **only when every one of its members is one of your fragments**; if it also
holds something you did not select, it is left completely alone. Breaking up someone else's grouping
to tidy up ours would be a worse bug than the one being fixed.

**Never hold a `TrackEvent` across the edit.** A second attempt threw
`Catastrophic failure (E_UNEXPECTED)`. It tracked each fragment as an event reference collected
during the cut — but **VEGAS deletes grouped events together**: removing one takes its partner on
the other track with it, and the partner's own `Remove` then returns `false`, so the tracking list
kept a reference to an event that no longer existed. Touching a dead COM wrapper *is*
`E_UNEXPECTED`.

So nothing survives the edit now. The ruler range of each grouped clip is recorded beforehand,
translated forward by the spans that were removed, and the fragments are found by **reading the
tracks again**. Every event object used in the regrouping is fresh and alive by construction. Do not
"optimise" that back into a list of references.

**The grouping is best-effort and can never cost you the cut.** The cut is the product; the
regrouping is a convenience. Every step is staged and guarded, and **the stage name comes back in
the message** — so a refusal reports *which call* failed instead of just "catastrophic failure". If
VEGAS refuses one, you get a warning telling you to use your Make Groups button — not a lost edit.

## Auto-ripple

Removing a span closes it: everything to the right on the affected tracks moves left by exactly the
span's length. Three ordering rules make that safe, and each one is load-bearing:

- Spans are applied **last first**, so the ones still to come keep the ruler positions they were
  measured at.
- Within a span, every affected track is split at **both edges before anything is deleted**, so the
  tracks stay in step.
- The ripple moves every affected track by the **same delta**. Grouped picture and sound are
  separate objects to the scripting API — group-follow is a UI behaviour, `TrackEvent.Start` moves
  one event only — so both halves must be moved explicitly. Moving one and hoping the other follows
  is exactly what pulled them apart before.

"Affected tracks" are the tracks the selection lives on. A music bed or a title track you did not
select is never cut and never rippled. **Markers and regions are not moved** — the ripple shortens
the timeline under them.

## Before you run it

`becky-cut.exe` must exist (`build-all-tools.bat`). The script finds it via `BECKY_CUT`, then
`..\becky-go\bin\` relative to the script, then `PATH`.

## Verifying it

```bat
REM 1. Does the JSON reader still agree with becky-cut? (~5s, no VEGAS)
powershell -ExecutionPolicy Bypass -File vegas\test-beckycut-parser.ps1

REM 2. Does the whole code path work inside real VEGAS? (~1 min)
set BECKY_CUT_SELFTEST=X:\AI-2\becky-tools\test.mp4
set BECKY_CUT=X:\AI-2\becky-tools\becky-go\bin\becky-cut.exe
"C:\Program Files\VEGAS\VEGAS Pro 18.0\vegas180.exe" -SCRIPT:"X:\AI-2\becky-tools\vegas\BeckyCut.cs"
```

(1) compiles the real `BeckyCut.cs` and calls its real private `ParseCutReport` by reflection, then
checks the decision counts against becky-cut's own JSON counted a different way.

(2) builds a throwaway one-clip project on a video track and an audio track, selects both, runs the
same code a human click runs, writes `<media>.becky-cut-selftest.txt` and exits **without saving**.
Look for `RESULT: OK` — and then look at the two track lines. **The video and audio tracks must have
the same `events=` count and the same `kept_seconds=`**; if they differ, picture and sound have come
apart, which is the failure this script exists to prevent. `gap_seconds=` must be `0` — anything
else means the ripple did not close a hole.

**After any `-SCRIPT:` launch, screenshot within 30 seconds and keep screenshotting.** A `-SCRIPT`
run is never invisible, and a modal dialog on top of it looks exactly like success from the outside
(see the gotchas at the top of this file). Never poll a file blind.

## Design notes worth keeping

- **Every loop works on a fresh snapshot of `track.Events`** — `Split` adds to the collection while
  you walk it, and whether `Remove` cascades to a group partner is not documented. Never hold an
  event reference across a split: VEGAS can split grouped events together, so a held reference can
  silently become the wrong half.
- **The ripple assigns ABSOLUTE positions** — it reads every original `Start` first, then puts each
  event at `original - by`. It deliberately does not do `ev.Start = ev.Start - by`. Whether setting
  `Start` drags a grouped partner along is **not documented anywhere in the VEGAS API reference**;
  an absolute assignment makes the question irrelevant and is idempotent if something else already
  nudged the event. Do not "simplify" it back.
- **A locked event in the path of a cut refuses the whole edit before anything changes.** `Split`
  throws on a locked event, and `UndoBlock` has no Commit — `Dispose` commits whatever happened,
  including a half-finished edit. Checking first turns that into a clean refusal.
- **`becky-cut`'s stderr is drained on a callback, not a second blocking `ReadToEnd`** — sequential
  pipe reads deadlock, and the symptom would be VEGAS hung behind a progress window with no cancel
  button.
- **Fragments are matched with `Equals`, never reference identity.** `TrackEvent` and `Track` both
  override `Equals`/`op_Equality`, so two different wrapper objects can point at the same timeline
  event; matching by reference would silently lose a fragment's lineage. (It also means
  `AffectedTracks` really does de-duplicate, which matters — rippling one track twice would
  double-shift it.)
- **If becky-cut's VAD second pass did not run, the script says so.** becky-cut skips it with only a
  stderr warning when `silero_vad.onnx` is missing, which would quietly turn this into a
  silence-only edit. That is the one thing that can make the cut disobey becky-cut's own rules, so
  it is reported rather than assumed. It is a warning, not a question.
- **The frame grid comes from VEGAS, not from arithmetic on the project frame rate.** One frame is
  `Timecode.FromFrames(1).Nanos`, so a snapped cut is exactly a frame edge rather than a rounded
  number of nanoseconds near one, whatever the ruler format is. Off-grid cut points are what leave
  one-frame slivers behind.
- **It reads becky's JSON with a `Regex`, not `JavaScriptSerializer`** — see the assembly-set gotcha
  at the top. Keep it that way; it is what makes this a single file with no sidecar to lose.

---

# 6. `BeckyVegas/` — the Becky Search panel + the `becky-vegas` control channel

One VEGAS Application Extension DLL (C#, .NET Framework 4.8, `vegas/BeckyVegas/`), loaded by VEGAS
at startup from `Documents\Vegas Application Extensions\BeckyVegas.dll`. Built and verified live in
VEGAS Pro 18 build 527 on 2026-09-13/14 - every behaviour below was clicked through with the mouse
on a throwaway Untitled project and screenshotted.

## Why an extension and not an OpenFX plugin

Jordan asked for "an OFX plugin that allows me to search timeline footage AND folder footage based
on transcript". The OFX kit VEGAS ships (`Documents\Vegas_Assets\openfx\SonyOfxPIDK`: the
*Sony Vegas Video Plug-in SDK.doc* and `ofxSonyVegas.h`) gives a plugin parameters, a custom HWND
panel, progress/messages and the Timeline suite (`getTime`/`gotoTime`) - and **no access to the
project, its tracks, its events or the files on them**. An OFX plugin cannot know what footage is on
the timeline, so it cannot search it. An Application Extension (`ICustomCommandModule` +
`DockableControl`, scripting FAQ section 4) can read every event, move the cursor, add events and
markers, and run becky's tools - so that is what this is. It docks and floats like a native window.

## Install / update

Double-click **`Install Vegas Scripts.bat`** with VEGAS closed. It builds the DLL if the sources are
newer (Visual Studio Build Tools' Roslyn compiler, `vegas/BeckyVegas/build.ps1`), copies it into
`Documents\Vegas Application Extensions`, and retires the old VegasAIBridge (moved, never deleted).
No admin. Then in VEGAS: **View > Extensions > Becky Search** (VEGAS remembers where you put it).

**Test a change WITHOUT installing it:**
`vegas180.exe -CMDMODULE:"<repo>\vegas\BeckyVegas\bin\BeckyVegas.dll" "<a test clip>"` (scripting
FAQ 4.7) loads the DLL for that one session. Pass a media file so VEGAS opens a new Untitled
project, never one of Jordan's. **Do not do this while the installed copy is in
`Documents\Vegas Application Extensions`** - VEGAS would load both. A running VEGAS locks the DLL,
so build to a side folder while it is open: set `BECKYVEGAS_OUT` to another folder before
`build.ps1`.

## Using the panel (Jordan)

- **Search my timeline** - type what was said, Enter. Each result shows where it is on the ruler
  (cyan). Double-click: the cursor goes there and exactly the spoken line is selected, so Space
  plays it. A line that exists in a clip on the timeline but was trimmed away shows as
  **"cut from your edit"** (amber) - right-click it to add it back at the cursor.
- **Search a folder** - **Choose folder...** (the modern Windows picker) or drop a folder on the
  panel. Double-click a result: that line is added at the cursor on two **"Becky Pulls"** tracks
  (video + audio, grouped), and the cursor moves to its end so the next pull lands after it.
  Right-click: jump / add this line / add the whole clip / add a marker / copy the words / show in
  Explorer. Every edit is one Ctrl+Z.
- **Transcribe N clips** appears when clips have no transcript. It runs `becky-transcribe` one clip
  at a time in the background (VEGAS stays usable, **Stop** cancels) and writes
  `<clip>_parakeet_transcription.srt` beside each clip - the same name becky-clip uses, so Becky
  Review finds them too, and an official `<clip>.srt` is never overwritten. The search re-runs when
  it finishes.
- **Cut silence in selection** / **Caption selection** run `BeckyCut.cs` / `BeckyCaptions.cs`
  exactly as the Tools > Scripting menu does (verified: BeckyCaptions put 254 captions on the test
  clip; BeckyCut cut the 5-minute clip to 1:29, picture and sound together).

The search is **not reimplemented** here: it is `becky-review-index` (the engine behind Becky
Review's search pane), which gained `--timeline <json>` for this. The timeline JSON is the same
`internal/edl/vegastimeline.go` contract BeckyCaptions writes, and `VegasTimeline.SourceHits` maps a
spoken line in a file back to every ruler position that shows it (speed changes included; a clip's
picture and sound count once).

## The control channel - `becky-vegas.exe` (Claude Code, Whoretana, becky tools)

The extension listens on `\\.\pipe\becky-vegas-<pid>` (this Windows user only; network clients are
refused) and writes `%LOCALAPPDATA%\BeckyVegas\instances\<pid>.json` so the client can find the
right VEGAS. One JSON request line in, one JSON reply line out. `becky-vegas` wraps it:

```bat
becky-vegas status                         :: project, cursor, selection, counts, rendering flag
becky-vegas dialogs                        :: open VEGAS dialogs: title, message text, buttons
becky-vegas timeline                       :: every media event: source, in, out, timeline, track, rate
becky-vegas search query="beauty mirror"   :: transcript search of the timeline (on_timeline true/false)
becky-vegas search query="scissors" folder="E:\footage"
becky-vegas jump start=45.76 end=50.08     :: cursor there, that span selected, scrolled into view
becky-vegas insert path="X:\clip.mp4" in=10 out=14        :: onto the Becky Pulls tracks at the cursor
becky-vegas add_marker seconds=9.12 label="check this"
becky-vegas snapshot path="C:\tmp\frame.png"              :: the preview frame at the cursor, real pixels
becky-vegas run_script path="X:\...\BeckyCut.cs"          :: any VEGAS script, like Tools > Scripting
becky-vegas command section=Global name=Tools.Video.VideoEventFX   :: a VEGAS command by keyboard.ini name
becky-vegas show_panel | play | stop | pause | markers | selected | transcribe path=... | help
```

Exit code 0 = VEGAS did it, 1 = VEGAS refused or failed (the reason is in the JSON), 2 = no VEGAS
with the extension is running. `--pid N` picks one VEGAS when several are open (default: the one
focused most recently).

**Safety built in:** anything that changes the project refuses while VEGAS is rendering or showing a
dialog (the refusal names the dialog and its message), every edit is one undo step, and there is
deliberately no save / open / close command.

**Whoretana:** whenever VEGAS comes to the front, the extension sends one line to
`\\.\pipe\Whoretana`:
`{"cmd":"active_app","active_app":"vegas_pro","project_path":...,"pipe":"becky-vegas-<pid>","timeline_state":{...}}`
- the SharedState payload from `X:\AI-2\CLAUDE.md`. Verified against a stand-in listener. Whoretana
ignores commands it does not handle yet, so this is harmless until it reads them.

## Why the old VegasAIBridge never worked (so nobody re-diagnoses it)

VEGAS objects may only be touched on VEGAS's main thread (scripting FAQ, application extensions).
VegasAIBridge answered HTTP requests on a thread-pool thread and "fixed" that with
`SynchronizationContext.Current` captured in `InitializeModule` - which is **null** there - and its
helper then fell into `// No UI context - just run directly`, i.e. straight back onto the wrong
thread. Every call died with *"Unable to cast COM object ... IVegasCOM ... E_NOINTERFACE"*, and its
`COM-ISSUE-ANALYSIS.md` concluded VEGAS itself could not run scripts from an extension. **That
conclusion is wrong** - `run_script` works (a probe script added its marker from outside VEGAS).
The working pattern is `UiThread.cs`: create a `Control` in `InitializeModule`, touch `.Handle` so the
window exists on the main thread, then `BeginInvoke` onto it from any thread, with a timeout that
cancels queued work so an edit can never land late.

## VEGAS 18 facts measured while building it (API traps)

- Set `Transport.CursorPosition` **before** `SelectionStart`/`SelectionLength` - setting the cursor
  afterwards clears the selection (it read back 0).
- `Vegas.AppActivated` **never fired** for the extension when VEGAS came to the front; the extension
  polls the foreground window once a second instead.
- `vegas.SaveSnapshot(path, ImageFileFormat.PNG)` returns a **real** frame in a normal session (the
  blank-frame trap in section 0 is `-SCRIPT`-only).
- `vegas.InvokeCommand(section, name)` is undocumented; `section` is the **keyboard.ini context**
  and `name` the full keyboard.ini command name. `("Global", "Tools.Video.VideoEventFX")` works.
  TrackView-context commands (`TrackView.Nudge.RightByFrames`) returned without error and did
  nothing. Opening Video Event FX also adds a tab to Jordan's floating dock, and dock layouts
  persist - close whatever you opened.
- `Project.AddVideoTrack()` puts the track at the **top**, `AddAudioTrack()` at the **bottom**.
- One `UndoBlock` around track creation + events + `new TrackEventGroup()` grouping is one Ctrl+Z.
- The API reference in `VegasProData/` is a 2021 revision; the ground truth for this machine is
  reflection over `C:\Program Files\VEGAS\VEGAS Pro 18.0\ScriptPortal.Vegas.dll`. There is no
  Trimmer API in 18 - that is why folder hits are pulled onto the timeline instead.
- VEGAS scans **subfolders** of its extension folders, so a DLL "disabled" by moving it into a
  subfolder still loads. Move it out of every extension folder.
