# Becky → VEGAS Pro 18 review timeline

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

## 3. Run it (no compiling, ~10 seconds)

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
