# HANDOFF-ROUGHCUT-2026-08-24-NIGHT.md — recovering and finishing the overnight rough-cut session

Qoder CLI (`qodercli-1.1.28`) worked this rough-cut tool on and off for ~18 hours (2026-08-24
00:55–18:28 local) and ran out of Qoder platform credits (`error 112`, "You've reached your
credit usage limit") mid-edit, seconds after Jordan gave his most detailed feedback of the
session. Its own terminal couldn't respond, summarize, or save anything further — the CLI was
still resident in memory but every model call bounced. Nothing was lost: Qoder's own on-disk
session transcript (`C:\Users\only1\.qoder\projects\X--AI-2-becky-tools\<session-id>.jsonl`,
2737 lines, 5.4MB) plus its per-run SDK logs and a still-running Vegas/quotes state gave a
complete, byte-exact record of the whole session. This file is that recovery: what Qoder's
session actually said and did, what was already fixed and verified, what was claimed-fixed but
wasn't, and what Claude Code (Sonnet 5) finished, fixed, and re-verified afterward.

**Read `SKILL.md`'s `# ROUGH CUT` section for the current canon/recipe.** This file is the
story of one night, not the reference doc.

---

## 1. How the recovery worked (useful pattern for next time this happens)

Any AI coding CLI that behaves like a Claude-Code-style agent (Qoder, in this case) persists its
full conversation as an append-only JSONL transcript, one JSON object per line, at a path keyed
by the tool's install + the project's absolute path — the exact same shape Claude Code itself
uses. For Qoder specifically:

- **Full transcript**: `C:\Users\only1\.qoder\projects\X--AI-2-becky-tools\<session-uuid>.jsonl`
  — every message, tool call, and tool result, plus synthetic `last-prompt` entries that cache
  the most recent literal thing the human typed (handy: no JSON-content-block parsing needed to
  recover just that).
- **Per-run SDK logs**: `C:\Users\only1\.qoder\logs\runs\<timestamp>-<pid>\qodercli.log` — one
  folder per subprocess Qoder's CLI spawns (including short-lived control-plane calls like model
  catalog refreshes, which are NOT the main session and will confuse you if you grab the wrong
  one — match on the actual PID from `Get-Process`, not just "most recent").
- **The actual failure**: found by reading the raw JSON, not the rendered text. The assistant
  turn's `message.content[0].text` was a generic "credit limit" string, but the SAME object also
  carries `"error": "authentication_failed"`, `"displayErrorCode": "112"` — i.e. this was never a
  rate limit in the retry-after-a-minute sense; it's a Qoder platform BILLING wall. Jordan's own
  `/model` and `/effort` retry attempts (visible in the transcript) couldn't route around it for
  the same reason.
- **A live background task**: a "Poll until all sweeps ready (max 40 min)" command had been
  kicked off and its output survived at
  `C:\Users\only1\AppData\Local\Temp\qoder-cli\...\tasks\<id>.output` — worth checking for any
  agent CLI, since background jobs often outlive the parent process's ability to report on them.
- Reading 2737 lines of mixed JSON/tool-noise directly would blow a context budget fast. The
  approach that worked: index first (one line per message: line#, timestamp, type, length),
  filter for `role=user` messages with real typed text (skip `isMeta`/tool-result noise), THEN
  read only the ~4 substantive human messages in full. That is the entire signal in an
  18-hour, 2737-line session — everything else is machinery.

## 2. What Jordan actually said (verbatim, chronological)

1. **[2026-08-24 01:18 local] Kickoff.** Full text lives in the transcript (line 18); summary:
   build a Vegas Pro 18 rough-cut tool, inspired by lessons from a failed DaVinci Resolve MCP
   experiment (`WE_TRIED.md`) and a ButterCut comparison (`buttercut_proposal.md`), both in
   `X:\Videos\2026\08_august\23_hj-fbi-recap\`. CLI-driven, walk-away-and-come-back. Add a
   rough-cut section to `SKILL.md` when done.
2. **[03:57] Frustration checkpoint.** The first (Resolve-based) attempt still had "way too much
   silence" after getting under an hour. Named the fix direction himself: **"zero point
   crossings don't exist, audio normalization doesn't exist in any meaningful way... becky-cut
   is the only viable solution we've been able to come up with because if I dial in the volume,
   then it cuts at exactly zero-crossing points... it does NOT use Parakeet's transcription
   time, but rather, relies upon auto-editor's algorithm."** Also asked for quote clips placed
   in the timeline with manual verification of context.
3. **[16:17] The final, most detailed feedback (L2199 in the transcript) — the one this
   session had to finish:**
   > "did you utilize 'VAD' at all? the timeline is littered with clips that are mostly room
   > noise where I'm just adjusting myself preparing to deliver the line - I can't use this - it
   > would literally be faster to edit the thing from scratch. There should only be 4 tracks on
   > the timeline (my video track, my audio track, quotes video track, quotes audio track), but
   > instead there are 19 tracks because each time a quote clip was inserted, it caused my audio
   > track to be moved down, creating an additional audio track. Also they should not play at the
   > same time, so; I mention what he said, then it plays a clip of what he said (the quote
   > clip), then it returns to me talking - they are meant to be sequential because the listener
   > can't listen to 2 videos at the same time. try again, and check your work for christ's sake"
4. **Hand-edited `WE_TRIED.md` and `SKILL.md` directly himself** with the canonical definition
   of a rough cut vs. AI-slop (now quoted verbatim in `SKILL.md`'s `# ROUGH CUT` section — read
   it there, not paraphrased here) and corrected `buttercut_proposal.md` in several places,
   most importantly rejecting "word-level ASR timing (WhisperX) fixes cut boundaries" in favor
   of his own repeated point: a *calibrated* volume threshold is what a human editor's
   zero-crossing cut actually corresponds to, not a transcript timestamp.
5. **[18:28] Credit wall hit** mid-edit to `detect.go`'s `refineWordEdges` (see §3below);
   Qoder's session ends there, unresolved.

## 3. What Qoder had already fixed and verified (kept, unchanged)

**The 4-tracks and simultaneous-playback complaints were ALREADY FIXED by Qoder**, in
`splice.go` (new file, `spliceLayout`) and a rewritten `vegas/BeckyRoughCut.cs`: exactly four
tracks are created once (`Rough Cut (video/audio)`, `Quotes (video/audio)`), and quotes are
spliced by advancing a single cursor — the main edit's events after a marker are shifted by the
quote's length, never overlaid. This was independently re-verified this session (see §5) and
is correct. **No changes were needed here.**

## 4. What was claimed fixed but wasn't (the overclaim, corrected)

Qoder's own committed docs (commit `f4f31f8`, and the `HANDOFF-LOG.md`/`STATE-OF-MASTER.md`
entries already on `master`) state: **"3.0 s residual silence >=0.5s in the assembled audio
(0.0%)"** as verification that the room-noise complaint was also resolved. It was not, and the
number was never trustworthy:

`verify_audio.py` (Qoder's own ad-hoc check, left in `/tmp`) ran `ffmpeg silencedetect` over the
re-assembled audio using a noise threshold of `room_db + 3`, where `room_db` came from
`snapKeeps`'s zero-crossing-snap calibration — measured on this footage at **-77 to -92 dBFS**
on the RAW, unboosted audio. A threshold that strict essentially never fires on anything short
of digital silence; it cannot distinguish "the room" from "his voice." The 0.0% figure was a
tautology, not a measurement. (Its own committed comment even carries the raw numbers that
disprove it, if you look: `room -77.2 dB` through `room -91.8 dB` per clip, right next to the
"0.0%" claim two lines later.)

**This is the gap the room-noise fixes below actually close.** The corrected, evidence-backed
number after this session's fixes: **2.5 seconds total, in 2 gaps, across the full ~81-minute
assembled cut (0.05%)** — measured a different way, described in §5.

## 5. What Claude Code found and fixed this session

All in `becky-go/cmd/roughcut/detect.go` and `main.go`; `vegas/BeckyRoughCut.cs` was untouched
(already correct). Every fix below shipped with a regression test in `roughcut_test.go`
(`go test ./cmd/roughcut/...` — 16/16 green) and was re-verified end-to-end against the real
16-source, 2:25:25 hj-fbi-recap footage, not just unit-tested.

### 5.1 `refineWordEdges` had a hard 0.8s ceiling on lead-in trim — the literal bug named in feedback

The function that trims a keep's edges back to the real first/last spoken word only searched a
fixed window (`s+0.15` to `s+0.8`) for that word. A lead-in longer than 0.8s — exactly "adjusting
myself preparing to deliver the line" — matched no word in the window, so nothing trimmed, and
the room noise shipped untouched. Worse: if the TRUE first word started just before the window
(e.g. at `s+0.06`), the search would skip it and lock onto a LATER word instead, clipping real
speech (measured live in Qoder's own session: a 0.79s "You can see" keep collapsed to a
0.06s fragment this way, seconds before the credit wall hit).

**Fix**: search for the first/last word that actually *overlaps* the keep — no floating window,
no artificial ceiling (a generous `capSec=4.0` sanity valve remains, purely against corrupt word
data, not as a real limit). Verified this both fixes the destroyed-keep case and correctly trims
multi-second lead-ins (`TestRefineWordEdgesTrimsLongRoomNoiseLeadIn`,
`TestRefineWordEdgesDoesNotDestroyShortKeep`).

### 5.2 Zero-duration ASR word timestamps broke the overlap test

Real data surprise, not something either agent could have guessed from first principles: a
meaningful fraction of Parakeet's word-level timestamps are literal points, not spans —
`{"word":"And","start":132,"end":132}`. The overlap test in 5.1's fix used strict `>`/`<`
comparisons, which exclude a word sitting exactly at a boundary. That silently repeated the
"skip the true word, lock onto the next one" bug from 5.1, just via a different path, and cost a
transcript cue its QA coverage. **Fix**: overlap tests are now inclusive (`>=`/`<=`).
Regression: `TestRefineWordEdgesHandlesZeroDurationWords`.

### 5.3 New: `splitOnWordGaps` — catches silence hiding INSIDE one transcript cue

Jordan's own commissioned research doc had already diagnosed this exact failure mode before
tonight (`buttercut_proposal.md`: *"I measured a '13s cue' that contained a 6s silence"*) — a
Parakeet cue's own `[start,end]` can span a real silence far longer than the cue-to-cue gap the
merge step ever sees, and the dB-threshold `silencedetect` pass (calibrated for zero-crossing
snap tolerance, not for telling room noise from speech) does not reliably fire on this
footage's noisy-but-quiet room tone. New function: splits a keep wherever two consecutive
*words* (not cues) are more than `-pause` seconds apart — the same signal already proven
reliable for edge-trimming, now applied to the interior too. Regression tests:
`TestSplitOnWordGapsSplitsInteriorSilence`, `TestSplitOnWordGapsLeavesShortGapsAlone`.

### 5.4 `rescueMissedCues` shipped its own padding untrimmed — found AFTER the first fix, by re-measuring

After 5.1–5.3, a real-footage re-run still showed two ~4.5s gaps on one clip (`LTXZ8562`,
investigated below). Root cause: `rescueMissedCues` (which re-adds a keep when a 3+ word cue's
words aren't covered by anything else) pads with its own generous constants
(`rescueBeforeSec=0.6`, `rescueAfterSec=0.5`) and — because it runs AFTER the main
refine/split pass — that padding never got trimmed to the cue's real words.

**First attempt (reverted): re-running `refineWordEdges`+`splitOnWordGaps` over the WHOLE keeps
list a second time, after rescue.** This looked like the obvious fix and initially measured as
one (large gaps way down), but it silently **regressed the QA gate from 1 dropped cue to 7** —
re-processing keeps that were already correct, right before the final zero-crossing snap,
let that snap's own ±0.35s acoustic search drift a boundary past a real word on cues that had
never needed rescuing at all. Caught by re-running the full pipeline and diffing `qa.json`
before/after, not by inspection — **this is exactly why "check your own work end-to-end on the
real footage" is load-bearing and not paperwork.**

**Fix actually shipped**: `rescueMissedCues` now trims *only its own newly-added span* with
`refineWordEdges` internally, before appending it — every other keep in the list is completely
untouched, so nothing that was already correct can regress. Confirmed: QA gate back to exactly
Qoder's own original 1 dropped cue (see §5.6), zero new drops.

### 5.5 A ~4.7s "room noise" gap that never existed — my own measurement script's bug

Worth recording because it cost real time and is a trap for the next agent too: an early version
of my diagnostic script parsed `cut.yaml` by watching for `- source:`/`  in:`/`  out:` lines, and
didn't stop accumulating into the "current" event when it hit a `- quote:` block later in the
same file (quotes are written as a separate section, same `  in:`/`  out:` field names). The
result: the LAST real source event in the file silently inherited the LAST quote clip's `in`/`out`
values. This manufactured a phantom `LTXZ8562` keep at `[0.0, 14.534]` that was never in the
actual timeline — confirmed by instrumenting the Go pipeline directly (temporary `DBG` prints at
every stage, later removed) and by hand-checking the raw YAML block boundaries. **Lesson for
future measurement scripts against this tool's output: `cut.yaml` has TWO sections
(`- source:` then `- quote:`) sharing field names; stop the parser state machine at the section
boundary.**

### 5.6 Final, real-footage numbers (hj-fbi-recap, 16 sources, 2:25:25 raw)

Measured with a corrected script (word-coverage against `words.json`, gap threshold 1.0s — well
above this footage's measured normal inter-word cadence of 0.08–0.4s, so ordinary speech rhythm
is never miscounted as noise):

| | Before this session's fixes | After |
|---|---|---|
| QA gate: dropped 3+ word cues | 1 (`"You can see"`, destroyed by the 5.1 bug) | 1 (different, unrelated case — see below) |
| Residual gaps >= 1.0s in the assembled cut | not meaningfully measurable (verify script was tautological, §4) | **2.5s total, 2 occurrences, in ~4866s of kept audio (0.05%)** |
| Timeline tracks | 4 (already fixed) | 4 (re-verified, unchanged) |
| Quote-track/main-track overlap | 0 (already fixed) | 0 (re-verified against exact millisecond boundaries in `vegas_cut.json`, not eyeballed) |

**The one remaining dropped cue is a genuine, rare, self-documenting edge case, not something
this session's fixes could or should paper over**: `VTNZ3433`'s word-timing JSON reports the
word `"for."` with `start=219.76, end=229.12` — a **9.36-second single word**, which is not
possible. Parakeet's forced alignment corrupted that one timestamp; the real ~9s pause between
"for." and the next word "What" is hidden INSIDE the bad word's reported span, so no
word-gap-based detector (5.3's included) can see it — the gap isn't between two words, it's
inside one. `qa.json` correctly flags this cue for Jordan's own review, which is exactly what
the QA gate is for (`SKILL.md`: *"report what was cut and what was dropped, never a duration-%
vanity number"*). Not fixed by design — a heuristic "cap absurd word durations" patch for one
occurrence out of ~1200 events was judged not worth the added complexity/risk; flag it if it
recurs.

## 6. Verification performed (all real, all this session)

- `go build ./...`, `go vet ./...` — clean, whole module.
- `go test ./cmd/roughcut/...` — 16/16 green, including 8 new regression tests for the bugs
  above.
- `go test ./...` — full module. Two failures, both in packages this session never touched and
  with no code relationship to `cmd/roughcut`: `TestRun_DegradesWhenNoModel` (`cmd/tts` —
  fails because a TTS model WAS found on this machine when the test expects to exercise the
  no-model degrade path; environment-dependent, not a code defect) and `TestHandleTier2Funnel`
  (`internal/assistant` — an external routing/LLM call returned no actions; also
  environment-dependent). Neither is a roughcut regression.
- `build-all-tools.bat` — full rebuild, exit 0, vision-smoke-gate PASS.
- **Ran the actual pipeline against the real 16-source footage** (not a fixture) five times
  end-to-end while iterating, each run 16 clips / ~2:25:25 raw / 25 verified quotes / 36 markers.
- **Vegas Pro 18 launched headless** (`-launch-vegas`), timeline built and saved.
- **`BeckyVerifyProject.cs` headless read-back** of the saved `.veg`: `tracks: 4,
  video_events: 1265, audio_events: 1265, markers: 36, regions: 16, length_seconds: 5192.7`.
- **Vegas Pro opened normally (GUI) and visually inspected** — screenshotted the loaded
  timeline: 4 track headers, dense continuous main-track events, sparse correctly-placed
  quote-track blocks, real media previewing correctly. (An unrelated third-party plugin,
  "VegasAIBridge," threw a port-conflict error dialog on startup — nothing to do with this
  tool; dismissed and confirmed harmless.)
- **Quote-vs-main overlap checked mathematically** against the actual `vegas_cut.json` timeline
  positions for multiple quotes (not just visually): main-track events end exactly where a
  quote's timeline slot begins and resume exactly where it ends, to the millisecond, every time
  checked.
- Left Vegas Pro OPEN on the finished project for Jordan.

## 7. What's still open / worth knowing

- The single remaining dropped cue (§5.6) is a Parakeet data-quality artifact, not a roughcut
  bug. If this class of corrupted word timestamp turns out to be more common than "1 in ~1200"
  on other footage, a sanity cap on implausible single-word durations (e.g. >2.5s) would be the
  right follow-up — not built now because one occurrence didn't justify the added complexity.
- `SKILL.md`'s "why not auto-editor/becky-cut here" section still states the ORIGINAL diagnosis
  (naive auto-editor kept 30% / shredded sentences on this footage) — that measurement is real
  and unchanged, but it predates Jordan's own correction that a *properly calibrated* volume
  threshold is the right mental model for a zero-crossing cut. `detect.go`'s own
  `calibrate()`/`silences()` already IS a per-clip adaptive threshold (not naive auto-editor),
  and this session's fixes (5.1–5.3) push further in that same direction using words.json as a
  second, corroborating signal rather than switching the primary detector. Worth a sentence of
  clarification in `SKILL.md` (added) so a future agent doesn't read the old "why not
  auto-editor" section as license to re-litigate the whole architecture.
- Per-source clip coloring (`INSTRUCTIONS.md`: "If possible, ensure each source video is set to
  a different color on the timeline") is not implemented in `BeckyRoughCut.cs`. Jordan did not
  re-raise this in his final feedback; noting it here since it was in the original brief and
  never addressed by anyone this session.
