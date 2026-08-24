# Becky → VEGAS Pro

Two scripts live here. They do different jobs:

| Script | What it does |
|---|---|
| **`BeckyCaptions.cs`** | Captions the edit you already have open — transcribes with becky and lays one text event per caption on a "Becky Captions" track. **Start here for captions.** |
| `BeckyReviewTimeline.cs` | Builds a *review* timeline from a list of forensic hits (path + in/out), each as a named Region. Nothing to do with captions. |
| **`BeckyRoughCut.cs`** | The unattended rough-cut assembler: reads `BECKY_ROUGHCUT_JSON` (written by `becky-roughcut`), builds video+audio tracks with paired events, markers and regions, saves the `.veg`, exits. `vegas180.exe -SCRIPT:<path>` + the env var = fully headless. |
| `BeckyVerifyProject.cs` | Reads a `.veg` back headless (`BECKY_VERIFY_VEG=<path>`) and writes `<path>.verify.txt` with track/event/marker/region counts and length — the proof a delivery actually landed. |

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
