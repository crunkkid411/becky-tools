# BeckyCut.cs — STATUS: the cut WORKS (Jordan confirmed); the regrouping is on its third attempt and UNTESTED

**Round 1 (the cut itself) is confirmed working on his real project** — 2026-09-11: *"I tested the
script, and it does work ... It actually DOES WORK!!! WELL DONE!!"*

**Round 2 (rebuilding the clip grouping, plus a VAD-skipped warning) has never been run.** He asked
twice not to test while he was editing — *"do not test. I'll keep editing and test when i'm ready
for a break."* — so the only evidence for round 2 is a clean compile against VEGAS's real assembly
set and members confirmed by reflecting over `ScriptPortal.Vegas.dll`. Read "How to verify it" at
the bottom before trusting it.

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

## Round 2 — Jordan tested it, it worked, two follow-ups (2026-09-11)

> "I tested the script, and it does work ... It actually DOES WORK!!! WELL DONE!!"

**1. "No padding" — clarified, and verified in becky-cut's source.** It means the VEGAS script adds
none of its own. becky-cut's margins are untouched and still apply: `cmd/cut/main.go:53` declares
`--margin "0.04s,0.25s"` as the flag default (0.04s before, 0.25s after) and passes it straight to
auto-editor. The script runs `becky-cut <file> --dry-run` with **no other flags**, so every default
holds. And `--dry-run` is not a shortcut path — `main.go` runs step 1 (auto-editor detection) and
step 2 (the Silero VAD post-pass) exactly as a real render does, and only skips step 3, the encode.
`report["decisions"]` is built from the same `chunks` the render would use, *after* the VAD pass has
flipped its segments. So the dry-run answer is byte-for-byte the edit becky-cut would have rendered.
**Answer to his question: yes, it follows becky-cut's rules.**

One hole that was closed rather than assumed: becky-cut **skips the VAD pass with only a stderr
warning** if `silero_vad.onnx` is missing (`main.go:204-207`). That would silently produce a
silence-only edit. The script now reads `vad_applied` out of the JSON and puts a warning on screen
if it is false. It is a warning, not a question — the one-click rule stands.

**2. The grouping quirk — fixed natively.** Jordan:

> "After running becky-cut, all events that were affected are now grouped together in a DIFFERENT
> way ... if I try to delete a clip, it deletes ALL the clips."

> "if I highlight all the clips which were affected by becky-cut and use the 'remove from group'
> feature, it ungroups them as a single event, then allows me to use my 'Make Groups' script."

Root cause: VEGAS keeps **both halves of a split in the original group**, so N cuts turn one
video+audio pair into one group of 2N+2 events. The fix does his two manual steps in code — strip
the old membership, then build one fresh group per column — so Vegasaur is not needed and nothing
has to be called out to. Confirmed against the DLL: `new TrackEventGroup(Project)`,
`Project.TrackEventGroups.Add(...)`, and `BaseList<T>.Add/Remove/RemoveAt/Count` all exist.

Scope is strict. A lineage list follows every fragment through every split, so only pieces descended
from **his selection** are regrouped — "just make sure it only applies to the clips I had selected on
the timeline - not the entire timeline". Clips that were not grouped before stay ungrouped. The
regrouping is inside the same `UndoBlock`, so one Ctrl+Z still restores everything.

Two identity facts confirmed by reflection, because both could have caused silent damage:
`TrackEvent` **and** `Track` each override `Equals`, `GetHashCode` and `op_Equality`. So list lookups
match the same underlying timeline object rather than the wrapper instance — and `AffectedTracks`
genuinely de-duplicates, which matters, because rippling one track twice would double-shift it.

**Still his footage, not the script:** he noted some non-speaking audio surviving inside single
clips and reasoned it is because he was loud before those statements. Nothing in the script filters
becky-cut's answer, so if that needs changing it changes in becky-cut's VAD settings, not here.

## Round 3 — the regrouping threw E_FAIL, fixed (2026-09-11)

Jordan ran round 2 and got:

> "Error HRESULT E_FAIL has been returned from a call to a COM component."

That is VEGAS's COM layer refusing a grouping call. The round-2 code had three things in it that
VEGAS's own stock script does not do — any one of them could be the culprit, and all three are gone:

| Round 2 did | VEGAS's stock `Group Video and Audio Events.cs` does | Now |
|---|---|---|
| `new TrackEventGroup(project)` | `new TrackEventGroup()` — parameterless | parameterless |
| `group.Remove(event)` per event | never removes anything | **dissolves** the old group instead |
| `TrackEventGroups.RemoveAt(i)` sweep for empties | no sweep | removed entirely |

The stock script lives in this very Script Menu folder and has shipped with VEGAS since 2016 — it is
the authority on this API, and it should have been read before the first attempt rather than after
the error. (Same lesson as always: use the specialist's tool for the mechanics.)

Detaching now dissolves the old group, which is also the closer match to the "remove from group"
button Jordan actually presses — an event cannot be in two groups at once, so nothing can join a new
group until the old one lets go. A group is dissolved **only if every member is one of our
fragments**; if it also holds something he did not select it is left completely alone.

**And the grouping can no longer cost him the cut.** The cut is the product; the regrouping is a
tidy-up. `Regroup` is now best-effort: every step guarded, and a failure returns a message instead of
throwing. The cut still applies and he gets a warning telling him to use his Make Groups button.
Round 2's version let a COM refusal escape into the top-level handler, which is why he saw a bare
`E_FAIL` box rather than something he could act on.

## Round 4 — E_UNEXPECTED, the dead-wrapper bug (2026-09-11)

Round 3 got past `E_FAIL` and then hit:

> "The cut worked, but rebuilding the clip grouping did not: Catastrophic failure
> (Exception from HRESULT: 0x8000FFFF (E_UNEXPECTED))"

**The guard did its job** — the cut survived and he got an actionable message instead of losing the
edit. That part of round 3 was right.

**Root cause: dead COM wrappers.** The regrouping tracked every fragment as a `TrackEvent` reference
collected during the cut. But **VEGAS deletes grouped events together** — removing one takes its
partner on the other track with it, and the partner's own `track.Events.Remove(ev)` then returns
`false`. The code only dropped a fragment from its tracking list when `Remove` returned `true`, so
cascaded-away events stayed in the list as references to events that no longer existed. Reading
`.Track` off one of those is exactly what `E_UNEXPECTED` means.

**Fix: carry nothing across the edit.** Each grouped clip's ruler range is recorded *before* the
cut, translated forward by the spans that were removed (`MapForward`), and the fragments are found
afterwards by **re-reading the tracks**. Every event object the regrouping touches is fresh and
alive by construction. The whole lineage mechanism — `Piece`, `FindPiece`, `RemovePiece`, and the
scope list threaded through `SplitAt`/`DeleteInside` — is deleted, which also makes the cut itself
simpler than it was.

Two more hardening changes, because a third blind round trip would be unacceptable:

- **Every stage is named, and the name comes back in the message.** "reading the tracks",
  "ungrouping the clips", "making the new groups", and so on. If VEGAS refuses again, the next
  report says which call it was instead of a bare HRESULT.
- **Dissolving a group no longer walks its member list.** It compares `group.Count` against how many
  of its members are ours. That list can still name events the cut deleted, so walking it would have
  been the same dead-wrapper bug by another route.

## NOT verified — what could still be wrong

The cut itself is confirmed. Everything below is round 2 or an untested edge of it.

1. **The round-4 regrouping has never run.** Round 2 threw E_FAIL, round 3 threw E_UNEXPECTED; this
   is the fix for the second one, and it is again untested — he is still editing. If it fails again
   the message will now name the stage, which is the thing to report.
2. Whether the rebuilt groups are the shape he wants on a timeline where an audio event spans more
   than one video event — overlapping fragments become one group, which is correct but bigger than a
   pair.
3. Whether `Timecode.FromFrames(1).Nanos` returns a sane frame length on his project (it is read at
   run time from VEGAS; the script falls back to no snapping if it comes back `<= 0`).
4. Whether pulling in grouped partners picks up exactly the right events on a project with more
   complex grouping than video+audio pairs.
5. `becky-cut` itself was **not re-run** in round 2 — no ffmpeg or GPU work was started, to avoid
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
