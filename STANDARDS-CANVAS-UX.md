# Canvas UX Standards — mandatory for any becky-canvas panel work

**Status: MANDATORY.** Pinned from `CLAUDE.md`. Read before touching any `cmd/canvas/gui_*panel.go`
or the dock. Adapted from the ACE-Step-DAW `.claude/references` set (their React/Zustand/Tailwind
DAW) and re-expressed for becky's **Go + Gio + `dawmodel.Arrangement`** reality. Sits beside
`GUI-RULES.md` (architecture) and `CANVAS-NORTH-STAR.md` (the pinned direction + Definition-of-Done).

The headline rule is §3 — internalize it first.

---

## 1. Visual language (no exceptions in panel code)

- **Never hardcode a color in a panel.** All color comes from one Gio theme struct (surface
  ladder `Bg → Surface → Surface2 → Surface3`, `Border`/`BorderStrong`, text tints
  white/90→muted→white/40, one `Accent`). becky's brand is the source: neon-green `#39FF14`
  on black, scene-kid diamond (`hairjordan.yaml`). Literal `color.NRGBA{...}` hexes in
  `gui_*panel.go` are a defect.
- **Fixed state-color table** (theme-independent, always paired with a shape/icon/label so it's
  color-blind safe): **Green = active/armed, Red = recording/error/destructive, Amber = solo/
  warning, Blue/Accent = selected/focused, muted opacity = disabled/bypassed.**
- **Monospace for ALL numerics** — BPM, dB, ticks, note names, times. Sans for labels. ≤3–4
  type sizes per component.
- **Surfaces, not borders, create depth** (3–4 surface levels). Sharp corners; minimal rounding.
- **Match DAW density** — do not add whitespace "for clarity"; producers expect density.
- **Animation is functional only:** playhead, record-pulse, ~75ms value transitions, ~150ms
  panel slide. **No bounce / spring / elastic.**
- **Respect hand-tuned panels** — read the closest existing panel first; don't "fix" a choice
  without a bug or an explicit ask.
- **Anti-patterns (forbidden):** over-spacing, over-bordering, over-rounding, accent-as-
  decoration (accent communicates STATE only), font-size soup, shadows/gradients, importing
  web conventions into a DAW.

The canonical reference panels new work must match for density/spacing/color before deviating:
`gui_pianopanel.go`, `gui_drumpanel.go`, `gui_mixerpanel.go`, `gui_launch.go` (the dock).

## 2. Interaction grammar

- **Snap-to-grid by default; Alt = free.** Applies to piano/drum edits and bar-paging.
- **Zoom anchors to the cursor**, not the center.
- **Ghost preview at the snap position before drop**; show valid (blue) / invalid (red) zones;
  6px resize handles with a cursor change; Esc cancels a drag.
- **Faders/knobs:** vertical drag (up = increase), double-click = reset, right-click = type an
  exact value, scroll = fine adjust.
- **Keyboard-first:** every mouse action has a key path. **Space = play/pause, Enter = stop**
  regardless of focus. Modifiers: Cmd = primary, Shift = extend/multi-select, Alt = bypass-snap.
  The agent overlay keeps Esc = reject / Enter = approve.
- **Every editable element carries a stable string ID** (track/clip/lane/strip/note) — the Gio
  analog of ACE-Step's `data-*`-on-every-target, so the agent layer (§3) can address anything a
  human can click.

## 3. ★ Dual human + agent operability (THE headline rule)

**A canvas feature is not done until it is operable from BOTH the panel AND an agent op,
is undoable, and has a workflow test.** Anything a human can do by clicking, an agent must be
able to name and do via `internal/ctledit`.

becky is already ~80% there — the loop exists, it just needs unifying:

- **`dawmodel.Arrangement` is the single source of truth**; `applyArr` is the only commit path.
- **No panel gesture without a corresponding `ctledit` op.** Today some panels call dawmodel
  verbs directly while the agent box goes through `ctledit` — unify them onto one op vocabulary.
  (Biggest compliance task.)
- **Undo covers agent edits too** — `applyArr` pushes history, so a `ctledit` batch is as
  undoable as a click.
- **Actionable errors** — `ctledit` already drops-illegal-with-a-reason and never panics. Keep it.
- **Introspection / `describe` op** (the ARIA analog becky must add): emit the addressable IDs
  of every track/clip/lane/strip as JSON so an agent can discover targets.
- **Workflow test** — e.g. apply a batch ("make a house beat, add a bass note") and assert the
  resulting `Arrangement`, mirroring "program a beat via the API and assert the steps."

This adds one clause to the `CANVAS-NORTH-STAR.md` Definition-of-Done: alongside *window opens /
▶ Play makes sound / every button works / no freeze*, add **"every button's action also exists
as a ctledit op, and is undoable."**

## 4. What was deliberately NOT adopted

- The ACE-Step `references/skills.md` plugin list is React/TS/Tailwind/Zustand-bound — skipped.
  Its one transferable idea (a periodic dependency/skill review) is already covered better by
  `becky-freshness` + `becky-radar`/`becky-scout`.
- Their 5-theme cross-theme matrix is demoted: becky is single-brand, so the rule is just
  "honor the theme struct, don't bypass it."
