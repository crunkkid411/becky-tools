# Becky Canvas — the NORTH STAR

**Read this FIRST, before any becky-canvas / DAW / drum / piano / mixer / audio work —
cloud or local. It exists to stop the two failures that keep happening: (1) the
direction ping-ponging from one session to the next, and (2) the small things
Jordan actually touches (Play/Pause, buttons that work, a window that doesn't
freeze) getting skipped because "it compiled."**

This doc OUTRANKS any contradicting instinct in a single session. If something here
conflicts with what you were about to do, this wins. If you think this is wrong, ask
Jordan — do not quietly pivot.

---

## 1. The decision that is NOT up for re-litigation

- **becky-canvas (the native Go + Gio window) IS the tool Jordan opens.** It is both the
  hub and the instrument. Everything — drum machine, piano roll, mixer, audio/vocal
  tracks, the "ask becky" box — lives **inside that window**.
- **REAPER is NOT the product.** At most it is an *optional export button* ("send this
  to REAPER"). It is never the destination, never a substitute for a native canvas
  panel, and never "the DAW Jordan opens." A REAPER chatbot is **not** becky-canvas.
- **The rule that prevents the whiplash:** if a native canvas feature is hard, build a
  **smaller native version** of it. Do **NOT** swap in a different app (REAPER,
  Hydrogen, kdenlive as a *front-end*) because the native thing is hard. Those tools may
  be used as **headless backends/exports** only — never as the thing Jordan looks at.
- **Why this rule exists (so you don't repeat it):** over several weeks, agents have
  bounced between "hand-build the drum machine in Gio" → "fork Hydrogen" → "drive
  REAPER" → "back to canvas." Each pivot threw away working momentum and left Jordan
  confused about what we're even building. Nothing had *pinned* the direction, so every
  fresh agent re-decided. **It is pinned now. Do not change it mid-stream.**

> One-line test: *"Is the feature I'm building something Jordan will see and click
> inside the becky-canvas window?"* If the answer is "no, it happens in REAPER," you
> took the wrong turn — stop and come back into the window.

### 1.1 What the creative engine actually IS — deterministic MIDI "LEGO", NOT random patterns

Jordan is a professional. The goal is **saving time on the deterministic, mechanical
parts of music** — not generating random patterns a human would never choose. Build to
this, exactly:

- **Layered + context-aware ("LEGO").** becky composes the way Jordan builds a track:
  one layer at a time, where **each new layer is aware of the stems already there.**
  Drums first → the bassline is written to complement **the kick's rhythm** *and* the
  harmony → "add an emo guitar riff" over a trap beat writes a guitar part that follows
  **emo-guitar idiom** *and* locks to the existing trap groove/key. This is the principle
  he likes in **ACE-Step-DAW** (each layer aware of prior stems) — borrow the *principle*,
  not the generative-audio implementation.
- **MIDI-FIRST, always.** MIDI is math, it's deterministic, and Jordan can hand-edit it
  fast. **AI-generated *audio* is rejected** — "you get what you get," no control. Audio
  in becky = **his own samples** playing the MIDI. (His favorited kick on a
  four-on-the-floor, not a synthesized render.)
- **Deterministic + instant + token-free.** "Give me a four-on-the-floor house beat with
  my favorite kick" must be a **template + a sample assignment**, computed instantly — it
  must NOT burn model tokens, and it must NOT make him click every kick by hand. Music
  theory is math; treat it that way. A local model is only for genuinely fuzzy
  natural-language intent, never for the deterministic musical result.
- **Per-genre theory rules.** The conventions for each instrument live in per-genre
  profiles (we already have 13 in `internal/music/profiles/`). "emo guitar," "trap hats,"
  "house kick" each follow documented, deterministic idioms — extend these, don't invent a
  model for them.
- **What this is NOT:** Playbeat-style **random** pattern spraying (random hi-hats nobody
  would pick) is **not** the goal. Seeded randomness is fine as an *optional* spice, never
  the point. The genuinely useful part of the generative engine (`internal/beatgen`) is
  its **deterministic genre templates** (house = four-on-the-floor) and euclidean math —
  lean on those; the dice are background, not the feature.
- **Inspirations (principle, not implementation):** ACE-Step-DAW (LEGO stem-awareness),
  **Strudel** (lightweight, deterministic, math/CLI pattern language). becky stays
  MIDI + his samples.
- **Standard DAW features are expected, built WITHOUT asking** — if Maschine / Cubase /
  Playbeat has it (waveform display, Play/Pause, piano roll, mixer), it's table stakes.
  Don't ask whether to build the obvious; build it and report.

> The unbuilt centerpiece this implies: a deterministic **"add a complementary layer"**
> engine — `add <genre> <instrument>` reads the current `dawmodel.Arrangement` (key,
> tempo, chords, the kick/groove), then writes a new MIDI track that obeys that
> instrument's per-genre idiom **and** fits the existing stems. `becky-compose` is the
> seed of this (per-genre profiles + shared harmony); the gap is making it
> **incremental** and **cross-stem / cross-genre aware**.

---

## 2. Definition of DONE for canvas work (this is the part that keeps getting skipped)

The thing Jordan experiences is **the window**. `go build` passing and `go test`
passing are **necessary but NOT done.** Before anyone says a canvas feature "works,"
the **local** agent (the one with a screen, GPU, and speakers) MUST run this on the
real machine and **report the result of each line** — checked, or "couldn't, because…":

- [ ] The window **OPENS and STAYS open** — no console flash, no instant crash.
- [ ] **▶ Play makes SOUND**, and **⏸ Pause / ■ Stop actually stops it.** *(This is the
      single most-repeatedly-missed detail. Transport is not optional.)*
- [ ] **Every visible button does what its label says** — you clicked each one and saw it
      happen.
- [ ] The window stays **RESPONSIVE during heavy work** (model call, render, ffmpeg,
      file scan) — it never freezes or beach-balls.
- [ ] The change is **VISIBLE on screen** — attach a screenshot.

If you can't check a box, **say so plainly in the handoff.** Never round "it compiles"
up to "it works." A half-working window that *claims* to work is worse than an honest
"the engine is ready; I could not verify Play on hardware yet."

---

## 3. Snappy + responsive is a REQUIREMENT, not a nice-to-have

Jordan is a creative in flow; lag breaks it. So:

- **The UI thread NEVER blocks.** Anything that can take more than a frame (~16ms) —
  model inference, audio render, ffmpeg, a file scan, any subprocess — runs on a
  **goroutine**, and the result is posted back + the window invalidated. A frozen window
  is a **bug**, full stop. (We already hit this: a slow function bound straight to the
  WebView2 UI thread froze becky-clip — same class of mistake.)
- **Transport feels instant.** Pre-render or stream the audio so there's no dead air
  after ▶. Don't make him wait.
- **Visual-first.** Colors and shapes over walls of text. One small box to talk to becky.
  Select → say what you want → see it change in place (the "show me, don't do it"
  overlay). This is settled design — see `CANVAS-INSPIRATION.md`; don't re-research it.

---

## 4. Cloud vs Local — who does what (so the baton stops dropping)

| CLOUD agent (no display / GPU / audio) | LOCAL agent (Jordan's Win10 PC) |
|----------------------------------------|----------------------------------|
| Build + unit-test the deterministic **engine** behind each panel (`beatgen`, `audiotrack`, `ctledit`, `dawmodel` verbs, `canvasbridge`). | **Run the window.** Execute the §2 Definition-of-Done checklist. |
| Compile-gate the GUI (`go build -tags gui ./cmd/canvas`). | Fix the small GUI/audio details (Play/Pause, layout, click targets). |
| **State plainly what it could NOT verify** (render, sound) and hand §2 to local. | **Report against each §2 box** with screenshots / a sound-check. |

Cloud cannot see or hear the window — that's a physical limit, not laziness. So the
checklist in §2 is the contract: cloud writes the tested engine, local proves the
window. If local skips the checklist, nobody catches a missing Play button — that's
exactly how the small details kept slipping.

---

## 5. Current state (2026-06-21) and the immediate local job

**Built + unit-tested by cloud (no REAPER, no model needed):**
- `internal/beatgen` (88 tests) — the generative drum engine (see §6 below / Playbeat).
- `becky-beat` (CLI) — make/randomize/euclid/genre/remix/vary a beat.
- Wired into the canvas drum panel: **[Random] [House] [Trap] [4-Floor]** buttons +
  `<`/`>` bar paging; and the agent box ("make a house beat" / "four on the floor").
- Audio/vocal panel renders waveforms; reachable via the **Audio** dock button.

**NOT yet verified on hardware (the immediate LOCAL job):** open becky-canvas, run the
§2 checklist — especially **▶ Play makes sound** and the drum buttons render + change
the beat. Report against each box.

---

## 6. The bigger picture (so it stops feeling contradictory)

- **Steinberg VST is still the plan.** The native audio stack (a C++ VST3/ASIO host
  *sidecar* → his Steinberg UR12, per `GUI-RULES.md`) is a real later phase, **not**
  abandoned. "VST3 hosting" means **becky's own sidecar** hosts the plugin, driven by the
  canvas — it does **not** mean "let REAPER do it." Building the drum machine now and
  hosting VST3 later are the same roadmap, not a contradiction.
- **One session model.** Everything edits ONE `dawmodel.Arrangement` (see
  `CANVAS-BLUEPRINT.md`). Panels are views onto that one model; they don't spawn parallel
  toys.

When in doubt: **the window is the product; Play has to make sound; don't escape to
REAPER.**
