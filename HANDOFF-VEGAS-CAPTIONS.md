# HANDOFF — becky captions on the VEGAS Pro timeline

**Branch:** `claude/vegas-becky-captions-70imte`
**Cloud finished:** 2026-08-16. **Left for local:** run it in VEGAS once (below).

---

## WHAT

`vegas/BeckyCaptions.cs` — a VEGAS Pro script that captions the edit already open in
front of you, using becky's transcription and becky's caption chunking.

Adapted from [louismathy/vegas-script](https://github.com/louismathy/vegas-script)
(`WhisperAutoSubtitles.cs`). Its style dialog + live preview, progress window, RTF text
handling and placement loop are kept. Three things were replaced:

| Was | Now |
|---|---|
| shells out to `whisper` | `becky-subtitle`, which asks **`becky-captions`** first whether a trustworthy official transcript already exists before spending an ASR run |
| splits every N words | becky's chunker: pause-driven breaks, 22-char cap, no danglers, min-duration floor, zero gaps |
| captions one media file from ruler 0, ignoring the edit | reads **every event on the track** (source, in/out, ruler position) so captions snap to *your* cuts and land at the right ruler position, **gaps included** |

## WHY

The third row is the one that matters. The original script is fine on an uncut clip and
wrong the instant you cut anything: whisper's timestamps are source-time, so after a cut
the words drift away from the picture. It also had no way to know where an event sits on
the ruler, so a timeline with gaps in it could never be captioned correctly.

## HOW (the seam)

The script writes a timeline JSON; becky writes a cues JSON; the script places the cues.

```
BeckyCaptions.cs  --timeline.json-->  becky-subtitle  --cues.json-->  BeckyCaptions.cs
```

- Contract + placement maths: `becky-go/internal/edl/vegastimeline.go` (`VegasTimeline`,
  `MapSpan`), unit-tested in `vegastimeline_test.go`.
- Cues writer: `becky-go/cmd/subtitle/cues.go`. Field order (`start`, `end`, `text`) is
  part of the contract — the Vegas script reads it with one regex, because a Vegas script
  has no JSON parser it can rely on.
- becky-captions-before-ASR: `becky-go/cmd/subtitle/acquire.go`.
- Both temp files plus `becky.log` are left in `%TEMP%\BeckyVegasCaptions\<guid>\`.

**The honest limit, already coded and warned about:** an official `.srt` is cue-level. becky's
pacing is word-timed, so when the transcript we end up with is an official one, each official
cue becomes one caption (still cut-snapped) and the run warns that these are the official
lines rather than becky pacing. Nothing interpolates fake word times.

## VERIFY — what cloud already ran

`becky-subtitle --selftest` (offline, no media, no models). Output pasted verbatim:

```
becky-subtitle selftest

  built 4 captions from 2 cuts (6.0s of edit)

        1  00:00:00,000 --> 00:00:01,580  "ninety percent of what"
        2  00:00:01,580 --> 00:00:02,900  "it does"
        3  00:00:02,900 --> 00:00:04,000  "is wasted"
        4  00:00:04,000 --> 00:00:06,000  "every single time"

  PASS  captions were produced
  PASS  first caption starts on the cut (0.000)
  PASS  last caption ends on the cut (6.000)
  PASS  no gaps between captions
  PASS  a caption ends exactly on the 4.000 cut point
  PASS  no caption shorter than 0.10s
  PASS  chunked on the speaker's pause
  PASS  SRT serialises with numbered cues
  PASS  default style is white text with a black outline

  placed on a Vegas timeline whose second cut sits at 10.000:
          0.000 ->   1.580  "ninety percent of what"
          1.580 ->   2.900  "it does"
          2.900 ->   4.000  "is wasted"
         10.000 ->  12.000  "every single time"

  PASS  every caption was placed
  PASS  placed cues are on the vegas clock
  PASS  first caption still starts at 0.000
  PASS  no caption lands in the timeline's empty 4.000-10.000 gap
  PASS  last caption ends where the second event ends (12.000)
  PASS  placed captions still cover 6.000s of speech

OK
```

The last block is the whole point: the `.srt` says the fourth caption is at **4.000**
(rendered programme) while the placed cue is at **10.000** (Vegas ruler). Get that
arithmetic wrong and every caption after the first gap sits under the wrong frame.

Also run end to end through the real binary against a hand-written transcript sidecar:
`cues.json` came back on the vegas clock (`0 / 1.567 / 2.9 / 10.0`) while `captions.srt`
stayed on the programme clock, and the C# regex was checked against that exact file — it
parsed all 4 cues identically to a real JSON parser, including escaped quotes/backslashes.

Gates: `go build ./...`, `go vet ./...` clean; `go test ./internal/edl/` green.
Pre-existing on a clean tree, NOT from this branch: `gofmt -l` flags `cmd/kanban/kanban.go`
and `internal/subs/subs_test.go`; `cmd/clip` `TestAudioLevels_ThreadsFpsAndParses` fails
(auto-editor not installed) and `internal/assistant` `TestHandleTier2Funnel` fails. All four
reproduce with the branch stashed.

Gate 5 (`build-all-tools.bat`) is local's — cloud has no Windows.

## WORK ORDER — local agent

- [x] `git fetch origin && git checkout claude/vegas-becky-captions-70imte` — merged to `master`
      (`b2840f6`, 2026-08-16) alongside the other open cloud branch; both branches deleted.
- [x] `cd becky-go && go build ./... && go test ./internal/edl/` — build + vet clean, `edl` green.
- [x] `becky-go\build-all-tools.bat` — gate 5 done. Every `.exe` built, `becky-review-engine.exe`
      alias included, and the becky-vision smoke gate PASSed (4/4).
- [x] `becky-go\bin\becky-subtitle.exe --selftest` — reproduced the block above verbatim,
      all 15 PASS lines, exit 0, ending `OK`.
- [x] **Generator + style params verified against the real VEGAS install** (see "Known gaps"
      below — both of cloud's flagged unknowns are now retired, no code change needed).
- [ ] Open VEGAS Pro with a **real edit that has at least one cut and one gap**.
- [ ] Select two or three events. **Tools ▸ Scripting ▸ Run Script… → `vegas\BeckyCaptions.cs`**
- [ ] Style dialog appears → press OK. Progress window counts up while it transcribes.
- [ ] **Check with your eyes, this is the acceptance test:** captions land on a "Becky
      Captions" track, and a caption under a cut is under *the right frame* — scrub across
      each cut and confirm no caption blinks on or off for a frame, and nothing sits in a
      gap where there is no picture.
- [ ] Run the script a **second** time — confirm it REPLACES the captions rather than
      stacking a second track.
- [ ] Report: caption count, whether any landed wrong, and paste `becky.log` from
      `%TEMP%\BeckyVegasCaptions\<guid>\` if anything failed.

## DONE means

Captions from a real cut edit, verified by eye at the cuts, with a second run replacing
rather than stacking. "It compiles" is not done.

## Known gaps / next

- ~~**The generator name is probed, not known.**~~ **RETIRED 2026-08-16 (local).** Probed the
  real install — **VEGAS Pro 18.0 build 527**, 101 generators. `FindTextGenerator` resolves on
  the *third* preferred name: `"Titles & Text"` → not found, `"Legacy Text"` → not found,
  **`"VEGAS Titles & Text"` → FOUND**, and that node instantiates with a video stream and
  `IsOFX = True`. No pinning needed, the probe order already works. Two near-miss names to be
  aware of if this ever moves machines: this install spells legacy text `"(Legacy) Text"` — with
  the parentheses — so the `"Legacy Text"` entry can never match it, and it also ships
  `"VEGAS ProType Titler"`, which the text/title substring fallback would grab if the preferred
  names ever stopped matching. Neither bites today because the preferred name hits first.
- ~~**Style is set through the OFX params by name matching.**~~ **RETIRED 2026-08-16 (local).**
  Checked every parameter the real generator exposes against the matching code:
  `Text | Text` matches and already **holds RTF**, so the RTF branch is the one taken;
  `OutlineWidth | Outline width` (double) and `OutlineColor | Outline color` (RGBA) both match
  `ApplyOutlineSettings`; `OutlineGroup` matches "outline" but is neither double nor RGBA and is
  skipped harmlessly. **`ApplyGeneratedFont` is a no-op on this generator** — there is no
  font/typeface choice parameter at all (the only choice params are `collection | Animation` and
  `Alignment | Anchor Point`). That is fine, not a bug: the font name and size travel inside the
  RTF font table (`\fonttbl … \f0\fs<half-points>`) that `ConvertToRtf` writes, which is the path
  actually used. If a font ever fails to take, fix the RTF, not the OFX scan.
  Probe log: `%TEMP%\becky-vegas-probe.txt`.
- Heavy time-stretching will still drift; captions are timed off the source audio.
- becky's caption style sidecar (`.capstyle.json`, the height Jordan drags in the review
  apps) is not read by the Vegas placement yet — the Vegas track position governs there.
