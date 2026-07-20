# Start here (read this first, every session)

This file exists so you don't have to ask Jordan anything to pick up where the
last agent stopped. Read it, then go straight to work — don't re-ask him what's
already answered below.

## The rules that keep getting broken (do not repeat them)

1. **FREE OR OAUTH ONLY. NEVER A PAID API.** Jordan already pays for Claude Max —
   Sonnet 5 / Opus / Haiku come free through the OAuth session (Agent tool with
   model "sonnet", `claude --model sonnet`, or `fleet-run.ps1`). If you reach an
   Anthropic model through a pay-per-token key instead, you are spending money he
   already spent once. On OpenRouter, the only free model is `tencent/hy3:free`.
   **`m3` / `minimax` is NOT free on OpenRouter** — it's free only through the
   local router. He has corrected this twice. Don't ask him again; just follow it.
   A hook (`~/.claude/hooks/block-paid-apis.ps1`) blocks paid calls automatically,
   but don't rely on the hook as your only check — know the rule.
2. **Prose is not protocol.** If a rule actually matters, it has to be a hook or a
   code guard. Writing another paragraph into a `.md` file is not a fix — Jordan
   considers that worse than doing nothing.
3. **Never relay a subagent's "verified" as fact.** If a subagent says something
   works, pull the actual frame/output yourself and look at it, or count it
   yourself. Trusting a subagent's word here already cost two days.
4. **Never make Jordan run a command or answer a technical question.** He is not
   a developer. Decide things yourself from this file and the other handoff docs.
5. Becky Review 3 was supposed to be grown from `native/becky-timeline`
   (see `BUILD_1.md`, `COLLAB-PROTOCOL.md`) and mostly wasn't — prefer reusing
   `becky-timeline` code over writing more C++ from scratch.
6. **The point of Becky Review 3 is `BUILD_1.md` §4-H and §10. Read them before
   you touch it.** The bug-fixing in `HANDOFF-BECKY-REVIEW-3.md` is maintenance,
   not the product. See "The missing half" below — an agent that reads only the
   handoff will rebuild a video player and think it finished.
7. **Becky Review 3 runs `becky-review-engine.exe`, NOT `becky-clip.exe`.** Both
   are built from `cmd/clip`. On 2026-07-20 the alias was three hours stale and a
   whole night of engine fixes was invisible in the GUI while every test passed —
   the exact shape of "works on my machine". `build-all-tools.bat` now builds the
   alias explicitly. If a Go change does not show up in the app, check this first.
8. **Output goes with the footage that made it.** Never the cwd, never a
   hardcoded drive. A render lands in a `Rendered/` subfolder of its source; a
   proxy/still lands beside its source. This is enforced in
   `internal/reel/reel_output_test.go` because as prose it silently drifted and
   put Jordan's YouTube edits on E:\ — a removable forensic drive holding
   evidence for a criminal case.

## What's already done — MEASURED. Read the scope line first.

**Scope, so this list is not read as more than it is:** everything below is the
RENDER/CAPTION/PLAYBACK pipeline plus timeline input. It is NOT the product.
The product is §"The missing half" (H-4..H-7) and the 120-item
`GUI-ACCEPTANCE-CHECKLIST.md`, and most of that is still unbuilt. An adversarial
audit on 2026-07-20 flagged this exact section for reading like "the app is
done" — it is not, it is the plumbing under the app.

- **A real edit session was driven with mouse + keyboard and passed, 2026-07-20
  ~01:50.** Not a smoke test, not a fixture — his actual 88-clip
  `post_constantly` reel, driven with Win32 mouse clicks and keystrokes, with a
  screenshot examined at every step (kept in the session scratchpad `e2e/`):
  click a clip → selects; `S` → splits at 4.7s; `Ctrl+Z` → **one press** reverses
  the whole split; `Space` → plays, `Space` → pauses; `Ctrl+Right` ×3 → 4.7s to
  13.2s across three edit points. App still alive afterwards, `crash.log` clean
  (0 errors), captions rendering throughout. This is the `CANVAS-NORTH-STAR.md`
  DoD (exercised by mouse + keyboard), actually met.
- **There is a finished, postable video.** `X:\Videos\2025\11_November\Rendered\
  post_constantly.captioned.mp4` — 269MB, 4500 frames at 30000/1001, audio
  present (aac 48kHz stereo, 150.144s). Built 2026-07-20 00:57 from his real
  Vegas `.xml`.
- Captions burn in. EVIDENCE, not a relayed claim: the frame at t=130s was
  extracted with ffmpeg and looked at directly — it reads "27 times a day",
  white bold, black outline, low-centred, and the number stayed with its unit
  (his rule). Re-extract any frame yourself if you doubt it; do not take this
  line's word for it either.
- Render is frame-exact. The reel computes to 150.183s = 4501 frames; the output
  is 4500 because **Jordan's own Vegas render is 4500 frames** — the burn is
  frame-for-frame identical to its input, and audio is `-c:a copy` so it is
  bit-identical. The one-frame delta is in Vegas's render, not in ours.
- Two different Vegas exports of the same edit produce identical cut points on
  all 88 clips, to 0 microseconds.
- Audio plays during playback.
- The Becky Review 3 window opens in about 1 second.
- Building captions takes about 12 seconds, costs nothing, and rotates across
  free models automatically.
- A scheduled Windows task ("BeckyModelHeartbeat") checks in on the free models
  every 30 minutes and writes `X:\AI-2\fleet\model-heartbeat.json`.

## SETTLED 2026-07-20: native wins. Stop re-opening this.

Jordan, in his own words, going to bed on 2026-07-20:

> "NATIVE MATTERS - WPF and Shotcut literally could not keep up with how fast i
> work the time line (i'm one of the fastest video editors in the world)... the
> becky-review-native app FROZE when i tried touching it (cuz i'm too fast - i
> wasn't even trying; literally my muscle memory broke the entire goddamn
> thing). you gotta make it work, and make it as snappy as Vegas Pro timeline
> (or faster)"

So:

- **`native/becky-review` (C++/ImGui) is the app.** WPF froze under his real
  input rate. That is disqualifying and no amount of layout polish fixes it.
- **`gui/BeckyReviewNative` (WPF) is the REFERENCE for LAYOUT AND FEATURES ONLY** —
  ten rounds of his feedback live in its design. Copy what it looks like and
  what it does. Never copy how it is built.
- **Responsiveness IS correctness here.** He is a professional editor whose
  muscle memory outruns the app. A feature that is right but janky has failed.
  The bar is Vegas Pro's timeline or faster.
- He also said the choice of timeline was the agent's call. This is the call.
  Do not spend another night re-deciding it.

## The missing half — what Becky Review 3 is actually FOR

Jordan caught this on 2026-07-20: *"i had fable 5 do a deep dive on
HKUDS/VideoAgent and the features there. if the opencode model misses what we
decided from that topic, then becky 3 is missing core features."* He was right.

The decision is `BUILD_1.md` §10 (L578-621), from that deep dive. The finding:
VideoAgent renders finished video directly and has **no editable intermediate
representation** — so becky's job is to keep its intent→workflow brain and
**replace its "render the video" terminal step with "emit becky engine verbs"**,
so an AI edit lands on Jordan's timeline instead of rendering around him. His
own words (`BUILD-INPUTS.md:13`): *"human review optional right on the timeline
instead of burning it all together as an .mp4."*

Status, checked 2026-07-20 — **the app half is mostly not built**:

| | Requirement (`BUILD_1.md:477-484`) | State |
|---|---|---|
| H-4 | `apply_edit_batch` — a whole AI pass is ONE undo span | Go side built + tested (`cmd/clip/edit_batch.go`, `bridge.go:223`); **C++ side not wired** — `native/becky-review/main.cpp` mentions it once, in a comment at L3107 |
| H-5 | `event` stream announcing AI activity **without blocking Jordan's editing** | not found |
| H-6 | plain-language intent in chat → timeline edits he can see/adjust/undo, via the existing `ask`/`apply_proposal` seam (**never fork it**) | not found |
| H-7 | forensic path in-app: query → qmd recall → becky-judge → becky-hits reel on the timeline | not found |

Constraints that go with it: **no MCP server, no separate AI tool surface** —
the AI uses the SAME shared-state JSON / engine-verb seam the human UI uses
(`BUILD-INPUTS.md:18-22`). The Go engine is never forked; new capability is an
additive verb (`BUILD_1.md:27-28`).

Also missing: `BUILD-INPUTS.md:29` promised `research/videoagent-integration.md`
(the intent→verb mapping). It was never written. `research/` has only
`velo-logic-mining.md` from that batch.

**Why this section exists:** `HANDOFF-BECKY-REVIEW-3.md` has ZERO mentions of
VideoAgent, §10, or H-4..H-7. It is entirely render/audio/caption bug work. An
agent working from that handoff alone would never learn the product spec exists
— which is exactly what happened.

## What's left — in order

1. **The missing half above (H-4..H-7).** That is the product. Everything below
   it is maintenance.
2. **Caption wording.** Two real bugs are now fixed (a cut landing mid-word
   captioned that word twice — "viral" / "viral", "maybe" / "maybe"; and the
   min-duration floor let two captions overlap so libass stacked them). His real
   edit went 202 cues -> 179. What remains is genuinely wording: some lines are a
   single word and some breaks read awkwardly. The rule he cares about most holds
   — a number never leaves its unit ("ten times a day" never splits into "ten" /
   "times a day").
3. **The branch `fix/becky-review-3-audio` is ahead of master and not merged.**
4. **Re-check Becky Review 3 by actually driving it with a real mouse.** Several
   agents edited `native/becky-review/main.cpp` overnight at the same time, so
   confirm it still works end to end before trusting it.

## Jordan's real footage to test with

`X:\Videos\2025\11_November\Rendered\post_constantly.xml`
`X:\Videos\2025\11_November\Rendered\post_constantly.mp4`
`X:\Videos\2025\11_November\Rendered\post_constantly.srt`
