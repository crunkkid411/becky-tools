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
