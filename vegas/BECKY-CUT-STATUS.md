# BeckyCut.cs — STATUS: REWRITTEN, COMPILES, **NOT TESTED IN VEGAS**

2026-09-10, late evening. Jordan was editing in VEGAS and said, verbatim:

> "please do so now but DO NOT test it; I need to actually use Vegas Pro for editing right now and I
> do not want you interfering."

So this was **not run**. It compiles against VEGAS's real assembly set and it was reviewed against
the official scripting docs and the VEGAS assembly itself — that is the whole of the evidence. Read
"How to verify it" at the bottom before trusting any of it.

## What Jordan reported, in his words

> "first of all, it creates a popup box and asks me a question about numbers (like how much of a gap
> or something) - that violates the entire premise of what becky-cut does. one click, and done. same
> result every time, deterministic. I already provided the VERY SPECIFIC, DIALED IN, TESTED OVER 9
> MONTHS numbers - and becky-cut ABSOLUTELY should never ask me for those numbers again."

> "The audio and video were grouped; a cut needs to affect both of them the same. ... there are
> moments on the timeline where the video footage plays but there is no audio, and there is also
> moments of audio with no video. There are also gaps on the timeline (the cuts need to utilize
> auto-ripple so the gaps do not exist). becky-cut removes silent parts, then does a second pass with
> a vad filter and whatever noise has no actual talking also gets cut. that's it - it's very simple
> and already robust. If you are trying to re-create it from scratch, that is part of the reason
> almost nothing you ever do works - i gave you the solution, we built becky-cut. becky-cut works.
> you're deviating again."

He is right on every count. Three separate failures, one root cause.

## Root cause of the desync (the important one)

**The old version analysed each selected event's OWN source file.**

On his timeline the picture is a camera file (`IURJ0280`) and the sound is a **different file** from
a separate recorder (`2026-09-09 02-00-04 [Stream 3]`), grouped together. So becky-cut was run twice
on two different recordings and produced two different edits: the picture was cut where the camera
mic was quiet, the sound was cut where the recorder was quiet. Applying each to its own track
guarantees video with no sound and sound with no video. It is not a rounding bug or an off-by-one —
per-event decisions can never be safe on a dual-system timeline.

**Fix:** the cut points are decided **once, from the audio**, and the resulting ruler-span list is
applied to every affected track. `DecidingClips()` picks the audio events when any are selected;
everything else is cut by their answer.

## The other two

**The dialog is gone.** No `AskOptions`, no min-gap field, no close-gaps checkbox, no env overrides
for them. becky-cut's spans are applied verbatim — this script now invents no threshold, no padding
and no minimum gap. It is an applicator, not a second opinion. The old `0.25s` minimum gap was
invented here and had no business existing. **Do not add a dialog back.**

**Auto-ripple is now unconditional.** Removing a span closes it: everything at or after the span's
end on the affected tracks moves left by exactly the span's length. Gaps cannot be left behind
because leaving them was never an option anyone wanted.

## What changed, mechanically

| Area | Before | Now |
|---|---|---|
| Who decides the cut | every selected event, from its own source | the **audio** events only, once |
| Applied to | each event's own track | **one ruler-span list**, every affected track |
| Grouped partners | only what you clicked | clicking either half pulls the other in |
| Gaps | left behind unless a checkbox was ticked | **always rippled closed** |
| Dialog | min gap + close gaps | **none** |
| Frame grid | `seconds * fps` rounded | VEGAS's own `Timecode.FrameCount` — no division, no drift |
| Ripple | `ev.Start = ev.Start - by` | **absolute** targets read before anything moves |
| Locked event in the way | split threw part way through the edit | refused up front, nothing changed |

Order is load-bearing in three places, and each is commented in the code: spans are applied
**last-first** so the remaining ones keep the ruler positions they were measured at; every affected
track is split at **both edges before anything is deleted**; and the ripple moves every affected
track by the **same delta**, assigning absolute positions so it does not matter whether setting
`TrackEvent.Start` drags a grouped partner along (which the API reference never says either way).

## What was verified, and how (no VEGAS was launched)

- **Compiles** against VEGAS Pro 18's real reference set: `check-vegas-script.ps1` (which passes
  `/noconfig`, so it sees the same small assembly list VEGAS gives a script) reports `COMPILE OK`.
- **API members confirmed by reflecting over `ScriptPortal.Vegas.dll`**, not by guessing:
  - `TrackEvent.Split(Timecode offset)` returns `TrackEvent`.
  - `TrackEvent.Start` — `get` **and** `set`.
  - `TrackEvent.IsGrouped` (get), `TrackEvent.Group` → `TrackEventGroup`, which derives from
    `BaseList<TrackEvent>` and implements `IEnumerable<TrackEvent>` — so a group enumerates, which
    is what lets the script pull in a grouped partner that was never clicked.
  - `TrackEvent.Track`, `.Locked`, `.Selected`, `.End`, `.Length`, `AdjustStartLength`.
  - `Timecode` has `FromFrames(Int64)`, `FromSeconds`, `FromNanos`, `Nanos`, `FrameCount`,
    `CompareTo`, and operators `+ - < > <= >= == !=`. (Still no `.Seconds` — use `.Nanos * 1e-7`.)
- **Compared against two field-proven silence removers on this machine**
  (`Script Menu\remove_silence.cs`, `1e7_silence_remover.cs`). Both use the same mechanics this now
  use: split at both edges, delete without walking live indices forward, and ripple by walking
  regions **right-to-left**, moving every event on every track. That is the established pattern, not
  something invented here — though those scripts ripple with a relative subtraction, and this one
  deliberately does not (see the review table below).

## What the docs review caught (and what was done about it)

A subagent reviewed the rewrite against `VEGASScriptAPI.html`, `VEGASScriptFAQ.html`, the real
`ScriptPortal.Vegas.dll`, and the field-proven silence removers in the Script Menu. It never
launched VEGAS. Two blockers and four risks, all fixed:

| Finding | What was wrong | Fix |
|---|---|---|
| **BLOCKER** | The ripple did `ev.Start = ev.Start - by`. Whether setting `Start` drags a grouped partner is **not documented anywhere**. If it does, events get moved twice — the exact desync being fixed. | Ripple now reads every original `Start` first and assigns **absolute** targets. Immune either way, and idempotent. |
| **BLOCKER** | Group-partner expansion meant a 3-way group (camera video + camera scratch audio + recorder audio) put **both** mics in the deciding set, and their cut lists were unioned — "cut wherever either was quiet", which deletes speech. | `DecidingClips` now allows **one decider per moment**, ranked deterministically: not-also-a-picture-source, then longer, then earlier, then path. |
| RISK | `DeleteInside` walked live indices while removing; if `Remove` ever cascades to a group partner it would skip or run off the end. | Snapshots first, like every other loop; uses `Remove`'s bool return so a cascade is not double-counted. |
| RISK | `Split` throws on a **locked** event, and `UndoBlock` has no Commit — `Dispose` commits a half-finished edit. | `EnsureNothingLocked` refuses up front, before anything changes. |
| RISK | Snapping divided ruler nanos by an integer frame length (333667 at 29.97) — ~1 frame of drift per ~10 hours of ruler. | Uses VEGAS's own `Timecode.FrameCount`, plus half a frame to round to nearest. No division, no drift. |
| RISK | `ReadToEnd()` on stdout then stderr — classic .NET pipe deadlock. Symptom would be VEGAS hung behind a progress window with no cancel button. | stderr drained on `ErrorDataReceived`. |
| Simplification | `PlaybackRateOf` cast to `VideoEvent` then `AudioEvent`. | `PlaybackRate` is declared on `TrackEvent` itself — both casts deleted. |

It also confirmed the algorithm itself: last-first span order is correct; splitting every track
before deleting on any is correct and load-bearing; `DeleteInside` and `RippleLeft` are disjoint and
complete (no event is both deleted and moved, none is neither); `MergeSpans` is a correct interval
union; and no gap can survive, because the event after a cut was split exactly at the span end and
the ripple butt-joins it at the span start.

## NOT verified — what could still be wrong

Nothing below has been seen to happen on a real timeline. Treat it as untested intent.

1. Whether the edit actually keeps picture and sound together on his real dual-system project.
2. Whether `Timecode.FromFrames(1).Nanos` returns a sane frame length on his project (it is read at
   run time from VEGAS; the script falls back to no snapping if it comes back `<= 0`).
3. Whether pulling in grouped partners picks up exactly the right events on a project with more
   complex grouping than video+audio pairs.
4. Whether any span lands somewhere that leaves an overlap rather than a clean butt-join.
5. `becky-cut` itself was **not re-run** this session — no ffmpeg or GPU work was started, to avoid
   competing with his editing.

## How to verify it (when he is not editing)

```bat
REM 1. ~5s, no VEGAS - does the JSON reader still agree with becky-cut?
powershell -ExecutionPolicy Bypass -File vegas\test-beckycut-parser.ps1

REM 2. ~1 min - the whole code path inside real VEGAS, on a throwaway project
set BECKY_CUT_SELFTEST=X:\AI-2\becky-tools\test.mp4
set BECKY_CUT=X:\AI-2\becky-tools\becky-go\bin\becky-cut.exe
"C:\Program Files\VEGAS\VEGAS Pro 18.0\vegas180.exe" -SCRIPT:"X:\AI-2\becky-tools\vegas\BeckyCut.cs"
```

The self-test now writes the numbers that would have caught this bug. In
`<media>.becky-cut-selftest.txt`, check all three:

- `RESULT: OK`
- the **video and audio track lines must have the same `events=` count and the same
  `kept_seconds=`** — if they differ, picture and sound have come apart
- **`gap_seconds=0` on every track** — anything else means the ripple failed to close a hole

Then, and only then, the real check: a real selection on a real project, and look at it.

**After any `-SCRIPT:` launch, screenshot within 30 seconds and keep screenshotting.** Both launches
on 2026-09-10 parked on a modal dialog. Never poll a file blind.

## Not touched

No VEGAS setting, preference, template or default was changed. VEGAS was never launched. The four
stale copies under `C:\Program Files\VEGAS\VEGAS Pro 18.0\Script Menu` are still there and still
shadowed by the per-user copies in `Documents\Vegas Script Menu` — removing them needs an admin
prompt.
