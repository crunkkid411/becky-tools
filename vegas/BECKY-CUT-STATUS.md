# BeckyCut.cs — STATUS: WORKING, VERIFIED IN VEGAS (2026-09-10, evening)

Supersedes the earlier "written, compiles, NOT VERIFIED" version of this file.
The how-to now lives in `README.md` section 5; this file is the evidence record.

## What was actually wrong

One line. `BeckyCut.cs` line 39 was:

```csharp
using System.Web.Script.Serialization;   // JavaScriptSerializer
```

`System.Web.Extensions.dll` is **not** in the small assembly set VEGAS compiles a script against, so
VEGAS refused it at launch:

```
BeckyCut.cs(39) : The type or namespace name 'Script' does not exist in the
namespace 'System.Web' (are you missing an assembly reference?)
```

`BeckyRoughCut.cs` had the identical line. It got away with it only because it ships a
`BeckyRoughCut.cs.config` sidecar that adds the reference — and the installer filtered on `*.cs`, so
that sidecar was never copied into the Script Menu. It therefore worked when launched by full path
from the repo, and failed from the Tools menu. Same error, same day, different cause.

## Why two sessions shipped it anyway

`check-vegas-script.ps1` said **COMPILE OK** — because `csc.exe` silently reads `csc.rsp` from the
.NET Framework folder, which references ~30 extra assemblies *including* `System.Web.Extensions.dll`.
The checker was referencing an assembly VEGAS does not have. It was a false green, and it was
trusted instead of the machine.

## The fixes

| # | Fix | File |
|---|---|---|
| 1 | `/noconfig` — the checker now sees the same assemblies VEGAS does, and reproduces the real error in ~1s | `vegas/check-vegas-script.ps1` |
| 2 | JSON read with `Regex` instead of `JavaScriptSerializer`; BeckyCut is now a single self-contained file with no sidecar to lose | `vegas/BeckyCut.cs` |
| 3 | Installer copies `.cs.config` sidecars, installs to `Documents\Vegas Script Menu` (no admin, no UAC), and reports stale `Program Files` copies | `install-vegas-scripts.ps1`, `Install Vegas Scripts.bat` |
| 4 | A runnable check so the reader cannot silently rot | `vegas/test-beckycut-parser.ps1` |

## The evidence

**Offline — the checker now catches it:**
```
COMPILE FAILED  BeckyCut.cs   (before the fix)
  BeckyCut.cs(39,18): error CS0234: The type or namespace name 'Script' does not
  exist in the namespace 'System.Web'
COMPILE OK      BeckyCut.cs   (after)
```

**Offline — the JSON reader agrees with becky-cut, counted a different way:**
```
BeckyCut.cs parser vs becky-cut on test.mp4
  ok    decisions        29
  ok    keep             11
  ok    cut              18
  ok    fps              29.97
  ok    threshold        -37.3dB (floor -61.2dB, speech -15.2dB, valley 46.0dB x 0.52)
  PARSER TEST PASSED
```
(Note `keep_segments: 14` in becky-cut's JSON is **not** the answer key — it is the count *before*
the Silero VAD pass, and `removed_by_vad: 3` of those are flipped to cut. 14 - 3 = 11.)

**In real VEGAS, headless — `BECKY_CUT_SELFTEST`, which had never once produced a report file:**
```
media: X:\AI-2\becky-tools\test.mp4
clip_length_seconds: 45.111
events_selected_before: 2
becky_decisions: 29
min_gap_seconds: 0.25
close_gaps: False
pieces_removed: 28
track 0 (Becky Cut selftest (video)): events=9 kept_seconds=25.391 first_start=0 last_end=44.778
track 1 (Becky Cut selftest (audio)): events=9 kept_seconds=25.391 first_start=0 last_end=44.778
RESULT: OK
```
Video and audio counts identical — the two stayed in sync.

**In real VEGAS, by hand, driven with mouse clicks and screenshotted at every step** — the path
Jordan actually uses:

1. Tools > Scripting shows **BeckyCut** (one entry, not two, despite the stale copy still sitting in
   `Program Files`).
2. It opens the options dialog — *"2 events selected. becky will cut the dead air out of them and
   leave everything else alone."* — not a compile error.
3. Cut with defaults: the 45s clip became 9 pieces with holes where the silence was, video and audio
   aligned.
4. **Ctrl+Z restored the timeline exactly** — back to one 45.03s clip on each track.
5. Cut again with **Close the gaps** ticked: the pieces butted together, 45s down to ~25s, nothing
   outside the clip moved.
6. `BeckyRoughCut` from the same menu now opens its *"Becky: choose vegas_cut.json"* picker instead
   of erroring — fix #3 confirmed on the second script too.

VEGAS was closed with `WM_CLOSE` and "No" to the save prompt each time, never force-killed.

## The protocol that was broken last session, and was followed this time

> After ANY `vegas180.exe -SCRIPT:` launch, screenshot within 30 seconds and keep screenshotting.
> Never poll a file blind.

It mattered: both VEGAS launches this session parked on a modal dialog ("The last session did not
complete properly", "Do you want to save changes to Untitled?"). Each was seen in a screenshot and
clicked. Blind polling would have looked exactly like the hang that ended the previous session.

## Not touched

No VEGAS setting, preference, template or default was changed. The four stale copies under
`C:\Program Files\VEGAS\VEGAS Pro 18.0\Script Menu` were left alone — removing them needs an admin
prompt and they are shadowed by the per-user copies anyway. The installer says so when it runs.

Still open, unrelated and uninvestigated: Jordan's report of playback jumping back toward the start
of the timeline when the playhead reaches the right edge of the visible area. First things to check
are Loop Playback (Q) with a loop region set, and the auto-scroll preference. No becky script writes
to either.
