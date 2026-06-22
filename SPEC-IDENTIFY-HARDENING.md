# SPEC-IDENTIFY-HARDENING.md — kill the confident WRONG-name in becky-identify voice-ID

> **BUILT 2026-06-22 (cloud) — decision logic implemented + value-tested; numbers in §10
> remain Jordan's to confirm on real audio.** The threshold/margin/cast logic is pure-Go,
> offline, and FULLY cloud-verified with synthetic similarity scores (see §6 — every box is
> checked with the test that proves it). The real CAM++ run on the actual small KB is the
> only LEFT-FOR-LOCAL step. This documents the exact code path and the shipped tests.
>
> Authored 2026-06-22. Closes README's single **Critical** known issue (pending local
> real-audio re-validation of the 0.75 / 0.06 defaults).

---

## 1. The problem, stated precisely (with the real numbers)

`becky-identify` attaches a **confident WRONG name** to a voice on a small knowledge
base. A wrong name in a forensic tool is the worst possible failure — it violates the
load-bearing invariant **"recall is for DETECTION, not NAMING"** (`README.md` "Non-obvious
decisions"; `FORENSIC-OUTPUT-PHILOSOPHY.md` §1). This is the highest-value accuracy fix
in the suite.

**The measured failure (from README "Critical" + "Voice threshold" notes):**
- CAM++ **same-person** cosine similarity runs **0.76–0.91**; **different-person** ~0.03.
- The flag default is **`--voice-threshold 0.45`** (`cmd/identify/main.go:90`).
- A real **0.73** match sits **below the 0.76 same-person floor** but **above the 0.45
  default**, so it is accepted.
- On a small KB (e.g. 3 enrollees, all male), **any male voice that isn't a strong match
  for its true speaker lands on the next-nearest male** and is asserted with a confident
  name. There is no "none of the enrolled people" outcome between 0.45 and the real
  same-person floor.

**Why the current code can't catch it — the exact path:**
1. `voice.go:188` `bestMatch(emb, enrolled)` returns ONLY the **top-1** name and its
   cosine. The runner-up is never computed, so the engine cannot tell "0.73 vs a 0.71
   runner-up" (a coin-flip between two males) from "0.73 vs a 0.04 runner-up" (a real,
   if weak, single candidate). **The discriminating signal is thrown away.**
2. `voice.go:151-153` `matchSpeakers`: `if bestName == "" || bestSim < threshold {
   continue }`. With the 0.45 default, 0.73 clears it → a named `Identification` with
   `Confidence: 0.73`.
3. `voice.go:170-186` `unmatchedDescriptions`: a below-threshold speaker becomes a
   generic `"unidentified speaker, unknown identity"` with **no candidate name and no
   reason** — a downstream human/agent can't see it was a 0.44 near-miss vs a 0.04 nothing.
4. The fusion pass (`fuse.go:37` `voiceSoloFloor = 0.62`) is a partial backstop — a lone
   voice below 0.62 is demoted to a candidate. **But it does not help the failure case:**
   0.73 is **above** `voiceSoloFloor`, so fusion happily emits a `soloVoiceEntry` for the
   wrong person. Fusion also never sees a margin, so a 0.73-over-0.71 ambiguity passes
   straight through.

**This spec hardens the voice-naming decision so a weak-or-ambiguous match becomes a
named CANDIDATE or `unknown`, never a confident assertion** — and emits the evidence
(top-2 margin, why-unnamed reason) so a downstream step can catch what slips through.

It does NOT touch the face path (`--face-threshold 0.55`, already conservative per
`main.go:91`) or location.

---

## 2. The three precise changes

### 2.1 Raise the lone-voice NAMING bar to ~0.75 (default `--voice-threshold` stays 0.45 for DETECTION)

Keep two separate bars, because they do two different jobs (this is the
detection-vs-naming invariant in code):

- **`--voice-threshold` (DETECTION / scoring floor) — unchanged default 0.45.** Below
  this a speaker is genuinely "unidentified speaker, unknown identity" (no candidate worth
  surfacing). This preserves backward compatibility for every existing test and consumer.
- **`--voice-name-threshold` (NAMING floor) — NEW, default ~0.75.** A lone voice match is
  asserted as a NAME only when `best ≥ 0.75`. Between the detection floor and the naming
  floor (0.45 ≤ best < 0.75) the speaker is surfaced as a **named CANDIDATE** (`candidate:`
  + reason), never a confident `Identification`.

**Justification for 0.75:** README states same-person CAM++ runs **0.76–0.91**. 0.75 sits
just under the bottom of that measured band, so a genuine same-person match is named while
the 0.73 wrong-person case (and everything below the band) is demoted. This is the exact
"use the measured distribution, not the flag default" guidance from README, made the
DEFAULT instead of a thing Jordan has to remember to pass.

The fusion `voiceSoloFloor` (`fuse.go:37`, currently 0.62) is **raised to track the naming
threshold** so the two agree (today they don't: 0.62 < 0.75). After this change a lone
voice clears fusion only if it also cleared naming, removing the second path to a wrong name.

### 2.2 Emit the TOP-2 candidate MARGIN; below a minimum margin → candidate, not a name

`bestMatch` is extended to return the **top-2** enrolled names and their cosines. Define:

```
margin = best - runnerUp        // 0 enrollees behind #1 → margin = best (no rival)
```

A lone voice is named ONLY when **both** hold:
- `best ≥ voice-name-threshold` (§2.1), AND
- `margin ≥ --voice-name-margin` (NEW flag, default ~0.06).

If `best` clears the naming bar but `margin` is too small (two enrollees nearly tied — the
"next-nearest male" failure), the match is **ambiguous**: demote to a CANDIDATE naming both
contenders, never assert one. This is the corroborate-then-conclude rule applied to the
single modality: an unresolved coin-flip is `unknown`/candidate, not a confident name.

**Justification for ~0.06:** different-person similarity is ~0.03, so two genuinely
different enrollees competing for one speaker differ by only noise-level amounts; a real
same-person match (0.76–0.91) beats the runner-up by a wide gap. 0.06 is comfortably above
the ~0.03 noise floor and well below a real gap. (Exact value is an Open Decision, §10.)

The margin is **always emitted in the JSON** (even on a confident name) as the audit trail,
per the philosophy's "show the basis."

### 2.3 `--cast "Name1,Name2"` plausibility guard

A comma-separated list of enrollees **known to be present** in this corpus. When set,
naming is **restricted to that cast**: an enrollee NOT in `--cast` can never be asserted as
a name (it is suppressed at the match-selection step, before thresholding). This directly
kills "the absent third male got picked": if Jordan knows only Shelby and John are in this
footage, `--cast "Shelby,John"` guarantees the absent enrollee is never the answer.

Behavior:
- Names match case-insensitively against the enrolled **display name** and **dir key**
  (the same `displayName`/`Key` pairing in `kb.go:225`), and against entity `aliases`
  (`kb.go:37`), so `--cast "shelby"` matches the `Shelby` entity.
- An unknown name in `--cast` (matches no enrollee) is reported in `notes.cast` as
  ignored-with-reason — degrade, never crash, never silently drop.
- `--cast` filters the CANDIDATE SET used for the top-1/top-2 selection, so the margin is
  computed **among plausible enrollees only** (an absent enrollee can't even be the
  runner-up that suppresses a real match).
- Empty/unset `--cast` → current behavior (all enrollees eligible).

---

## 3. CLI flags + JSON output additions

### 3.1 New / changed flags (`cmd/identify/main.go`)

| Flag | Type | Default | Meaning |
|---|---|---|---|
| `--voice-threshold` | float64 | `0.45` (unchanged) | DETECTION floor: below → "unidentified speaker, unknown identity". |
| `--voice-name-threshold` | float64 | `0.75` | NAMING floor for a LONE voice: below → named candidate, not an identification. |
| `--voice-name-margin` | float64 | `0.06` | Minimum top-1 − top-2 gap to assert a lone name; below → ambiguous candidate. |
| `--cast` | string (CSV) | `""` | Restrict naming to this expected cast; absent enrollees can never be named. |

`--voice-threshold` keeps its meaning so no existing invocation changes outcome on a clean
strong match. The two new floors only gate the **naming** decision.

### 3.2 JSON additions

`Identification` (named, voice or corroborated) gains an always-present margin audit on the
voice signal:

```json
{
  "type": "voice",
  "name": "Shelby",
  "confidence": 0.84,
  "match": "cosine",
  "speaker_id": "SPEAKER_01",
  "voice_margin": 0.79,          // best 0.84 − runner-up 0.05
  "runner_up": "John",           // the #2 enrollee (audit; omitempty if no rival)
  "runner_up_confidence": 0.05
}
```

`Unidentified` (the demoted / unmatched case) gains a machine-readable reason so a
downstream step can catch a weak match (`Candidate` already exists at `main.go:68`):

```json
{
  "type": "voice",
  "speaker_id": "SPEAKER_02",
  "candidate": "John",                  // the near-miss top-1 (when above detection floor)
  "candidate_confidence": 0.73,
  "runner_up": "Mike",
  "runner_up_confidence": 0.71,
  "voice_margin": 0.02,
  "why_unnamed": "ambiguous: 0.73 for John vs 0.71 for Mike (margin 0.02 < 0.06)",
  "description": "possible John (voice 0.73) — too close to Mike to name; unconfirmed",
  "confidence": 0.0
}
```

`why_unnamed` is one of a small closed set (so consumers can branch on it):
- `below-detection` — `best < voice-threshold` → generic unknown (today's behavior).
- `below-name-threshold` — `detection ≤ best < voice-name-threshold` → weak candidate.
- `ambiguous-margin` — `best ≥ name-threshold` but `margin < voice-name-margin`.
- `not-in-cast` — top-1 was suppressed because it's absent from `--cast`.

The human-facing `description` stays plain English per `FORENSIC-OUTPUT-PHILOSOPHY.md` §1
("an unidentified man (candidate: John, voice match 0.71)").

---

## 4. Deterministic / offline / degrade behavior

- **Deterministic.** The new logic is pure float comparison over CAM++ cosines — same
  embeddings in → same naming decision out. No new randomness, no model added. (The CAM++
  embedding itself is unchanged.)
- **Offline.** No network. No new dependency. The thresholding/margin/cast code is plain Go
  in `cmd/identify`.
- **Degrade, never crash** (`README.md` "Conventions"):
  - 0 enrollees → today's "all speakers → unidentified" (unchanged); margin is `best` with
    no runner-up.
  - 1 enrollee → no runner-up; `voice_margin = best`, `runner_up` omitted; naming still
    gated by the naming threshold (a single enrollee at 0.73 is still demoted).
  - `--cast` names matching nothing → noted in `notes.cast`, ignored, run continues.
  - `--cast` excludes ALL enrollees → every speaker is `not-in-cast`/unknown + a clear
    `notes.cast` explaining it (no crash, no empty mystery output).

---

## 5. Cloud-vs-local split

| Cloud (build + UNIT-TEST here) | Local (Jordan's hardware) |
|---|---|
| All threshold/margin/cast decision logic: extend `bestMatch` to top-2, the new flags, `matchSpeakers`/`unmatchedDescriptions` rewrite, the `why_unnamed`/margin JSON, raise+align `voiceSoloFloor`, `--cast` parsing/filtering. | Run real CAM++ on a real small-KB clip and confirm the 0.73 wrong-name case now reports candidate/unknown, and a genuine same-person ≥0.76 still names. |
| Value-asserting tests with **synthetic similarity scores** (no model, no audio) — the entire decision is just numbers, so it's 100% testable on the cloud box. | `build-all-tools.bat` (auto-discovers `cmd/identify`); spot-check on the actual 3-person KB that produced the wrong name. |

This is the right split because the bug is **in the decision math, not the embedding** — and
the decision math needs no GPU, model, or audio to test exhaustively.

---

## 6. Build plan + tests (every box ships a value-asserting test)

Tests live in `cmd/identify/` (e.g. `naming_test.go`), table-driven, asserting VALUES — per
`STANDARDS-ENGINEERING.md` (assert values, not truthiness; every fixed bug ships a regression
test). Existing `fuse_test.go` / `identify_test.go` must stay green.

> **STATUS 2026-06-22 (cloud, branch `claude/subagent-deployment-scaling-4hptv9`): BUILT + TESTED.**
> The entire threshold/margin/cast decision logic is implemented in `becky-go/cmd/identify/`
> and is fully cloud-verified with value-asserting tests (no model/audio needed — it is pure
> float math). `go build ./cmd/identify/...` + `go vet` + `gofmt -l cmd/identify/` clean;
> `go test ./cmd/identify/...` green (existing 17 + 17 new = 34 tests). LEFT FOR LOCAL is only
> running real CAM++ on a real clip to re-validate the exact numbers — see §6 "Local" boxes.

**Decision logic**
- [x] Extend `bestMatch` (`voice.go`) → `topTwo(emb, enrolled) (best, runnerUp namedScore)` returning both names + cosines (`bestMatch` retained as a top-1 shim). **Tested:** `TestTopTwoThreeEnrollees` {0.73,0.71,0.20}→best 0.73/runnerUp 0.71; `TestTopTwoSingleEnrollee`→runnerUp empty; `TestTopTwoNoEnrollees`→empty/empty, sim 0.
- [x] Added `--voice-name-threshold` (default 0.75) and `--voice-name-margin` (default 0.06) flags in `main.go`; threaded into `voiceOptions` (`voice.go`).
- [x] Rewrote `matchSpeakers` (now via `decideSpeaker`) to name only when `best ≥ nameThreshold && margin ≥ nameMargin`; otherwise the candidate path. **Tested (THE regression):** `TestNotNamedWhenAmbiguousNextNearest` best=0.73/John, runnerUp=0.74/Mike → **NOT named** (`len(ids)==0`); emitted as a candidate.
- [x] **Tested:** `TestNotNamedBelowNameThreshold` best=0.73 / runnerUp=0.04 → candidate "John", `why_unnamed=="below-name-threshold"`, `candidate_confidence==0.73`, NOT named.
- [x] **Tested:** `TestNotNamedAmbiguousMarginAboveFloor` best=0.80 / runnerUp=0.78 (margin 0.02 < 0.06) → `why_unnamed=="ambiguous-margin"`, candidate "John"+runner "Mike", `voice_margin==0.02`, NOT named. (Note: 0.73/0.71 from the §7 example resolves to `below-name-threshold` because 0.73 < the 0.75 naming floor — `ambiguous-margin` requires `best ≥ name-threshold` per the §3.2 enum; both outcomes are "not named". The 0.80/0.78 case exercises the ambiguous-margin branch exactly.)
- [x] **Tested:** `TestStrongUnambiguousMatchNamed` best=0.84 / runnerUp=0.05 → NAMED "Shelby", `voice_margin==0.79`, `runner_up=="John"`, `runner_up_confidence==0.05`, confidence 0.84.
- [x] **Tested:** `TestBelowDetectionGenericUnknown` best=0.40 (< 0.45 detection) → generic `"unidentified speaker, unknown identity"`, `why_unnamed=="below-detection"`, no candidate name.

**Margin + reason emission**
- [x] Added `VoiceMargin`, `RunnerUp`, `RunnerUpConfidence` to `Identification`; `CandidateConfidence`, `RunnerUp`, `RunnerUpConfidence`, `VoiceMargin`, `WhyUnnamed` to `Unidentified` (`main.go`). **Tested:** named match serializes non-zero `voice_margin` (`TestStrongUnambiguousMatchNamed`); demoted ones serialize the matching `why_unnamed` enum (every not-named test asserts it). All new fields are `omitempty` for back-compat.

**`--cast` plausibility guard**
- [x] Parse `--cast` CSV (`resolveCast` in `main.go`); resolve each against enrolled display name / dir key / aliases (case-insensitive); `filterCast` restricts the set used by `topTwo`; `castSuppressedWinner` detects a suppressed real winner. **Tested (regression):** `TestCastSuppressesAbsentWinner` {Shelby,John,Mike}, speaker best for Mike 0.80 + `--cast "Shelby,John"` → Mike suppressed; `why_unnamed=="not-in-cast"`, NOT "Mike", no Mike candidate.
- [x] **Tested:** `TestCastDoesNotBlockPresentEnrollee` `--cast "Shelby"` + genuine Shelby 0.84 → still NAMED Shelby.
- [x] **Tested:** `TestResolveCastUnknownNameIgnored` `--cast "Nobody"` → ignored (run proceeds as unset) + a `notes.cast` note; partial known/unknown keeps the list + notes the ignored name; `TestResolveCastMatchesKeyNameAlias` resolves key/name/alias.

**Fusion alignment**
- [x] Raised `voiceSoloFloor` (`fuse.go`) from 0.62 to 0.75 to equal the naming threshold, with a documenting comment. **Tested:** `TestFuseStrongSoloVoiceStands` updated deliberately — a 0.74 lone voice is now DEMOTED to a candidate (the second wrong-name path closed), while a 0.80 lone voice still stands. `TestFusePreservesUnidsAndSortsByConfidence` updated (its 0.63 solo voice raised to 0.77 to stay a valid solo case). All other `fuse_test.go` cases green.

**Gates**
- [x] `go build ./cmd/identify/...` + `go vet ./cmd/identify/...` + `go test ./cmd/identify/...` + `gofmt -l cmd/identify/` all green (cloud). Whole-module `go build ./...` green.
- [ ] **LEFT FOR LOCAL:** `build-all-tools.bat` (auto-discovers `cmd/identify`) on Jordan's PC; run real CAM++ on the actual 3-person small KB that produced the wrong name — confirm the 0.73 wrong-name case now reports candidate/unknown and a genuine ≥0.76 same-person still names; tune the exact 0.75 / 0.06 numbers on real evidence if needed (the logic + flags are done; only the *values* may want real-audio calibration).

---

## 7. Worked example (the exact failure, after the fix)

Input: a male voice that is really an un-enrolled person; KB has {Shelby, John, Mike}.
CAM++ gives John 0.73, Mike 0.71, Shelby 0.20.

- **Today:** 0.73 ≥ 0.45 → `Identification{name:"John", confidence:0.73}`. Confident WRONG name.
- **After:** 0.73 < 0.75 naming floor **and** margin 0.02 < 0.06 → demoted:
  `unidentified[]: candidate "John" 0.73 (runner-up Mike 0.71, margin 0.02),
  why_unnamed: ambiguous-margin`. Plus `description: "possible John (voice 0.73) — too
  close to Mike to name; unconfirmed"`. No name is asserted; a human/agent sees exactly why.
- **With Jordan's knowledge** that the absent enrollee shouldn't even compete:
  `--cast "Shelby,John"` removes nobody here, but had the wrong pick been an absent
  enrollee, `not-in-cast` would have removed it outright.

---

## 8. Accessibility

Output stays plain linear JSON + plain-English `description`/`why_unnamed` strings — no
tables, no symbols carrying meaning, screen-reader-friendly per `ACCESSIBILITY.md`. The
`why_unnamed` enum reads as a sentence in `description`; nothing requires sight.

---

## 9. Files touched (all under `becky-go/cmd/identify/`)

- `main.go` — new flags; new `Identification`/`Unidentified` fields; `notes.cast`.
- `voice.go` — `bestMatch` → top-2; `matchSpeakers` + `unmatchedDescriptions` rewrite; `--cast` filter; `voiceOptions` fields.
- `fuse.go` — raise/align `voiceSoloFloor`.
- `naming_test.go` (NEW) — the value-asserting tests in §6; minor update to `fuse_test.go`.

No other tool changes (cmd packages never import each other — `README.md` "Architecture").

---

## 10. Open Decisions for Jordan (the exact numbers)

1. **`--voice-name-threshold` default — 0.75?** README's same-person band is 0.76–0.91, so
   0.75 names a genuine match while demoting the 0.73 case. Could go 0.74 (slightly more
   permissive) or 0.76 (demote borderline same-person too — safer against wrong names, but
   risks demoting a real quiet match to candidate). Recommend **0.75**.
2. **`--voice-name-margin` default — 0.06?** Different-person ~0.03, so 0.06 is ~2× the noise
   floor. 0.05 is tighter; 0.08 demotes more borderline pairs. Recommend **0.06**.
3. **Should `voiceSoloFloor` exactly equal `--voice-name-threshold`,** or stay a hair below
   (e.g. naming 0.75 / fusion 0.72) so a corroborating second signal can still rescue a 0.73
   voice? The corroboration path (`corroborateMinPerSignal = 0.45`) already lets a weak voice
   count when a face agrees, so I recommend **equal (0.75)** for the lone path and let
   corroboration handle the rest.
4. **Default `--cast` off** (opt-in) — confirmed correct? It changes naming when set, so it
   should never be implicit. Recommend **off by default**.
