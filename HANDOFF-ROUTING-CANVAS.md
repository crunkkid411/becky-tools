# Handoff — wire deterministic routing into becky-canvas (+ the VST/bounce reality)

Built per `HANDOFF-TEMPLATE.md`. Cloud built + PROVED the deterministic routing engine
(`internal/autoroute`); this is the ordered work order for wiring it into becky-canvas
and Jordan's plugin/bounce workflow. Jordan's vision, in his words: WRITING stays fast +
lightweight; the deterministic routing (his rules, his plugins) is applied at the END
(or as a lightweight routed default), so he never re-routes 16 channels by hand and the
inspiration doesn't die waiting for a heavy template to load.

## FACT CHECK FIRST — Hydrogen does NOT host his VSTs (read this)
Jordan asked "Hydrogen can use VSTs, correct?" — **no, not the way he needs.** Hydrogen
is a sampler with its own drumkits (+ LADSPA/LV2 effects on Linux); it does **not** host
VST *instruments* like **Superior Drummer 3** or **Serum**. So his SD3 / Serum / 16-bus
routing workflow does NOT run through Hydrogen. The VST-hosting paths are:
- **REAPER** — hosts ALL his VSTs, fully scriptable; `internal/reaper` already authors his
  bus tree (`JordanTemplate`). **This is the primary "load my plugins + route + bounce" target.**
- **becky's C++ VST3 host** (`native/audio-host` + `internal/audiohost` + `becky-vst`) — for
  hosting a VST *inside* becky-canvas later.
- **Hydrogen** stays what `DRUM-MACHINE-DECISION.md` says: an optional FOSS drum-export, NOT
  the VST host. Don't route his SD3 workflow through it.

## ARCHITECTURE — DECIDED (Jordan ratified): becky-canvas HOSTS the FX
Jordan: "I prefer to keep everything in becky-canvas when possible — loading FX, rendering
in place." That's the target and it's the right AI-native design (no round-trips, the AI
sees everything):
- **Each bus in becky-canvas hosts its own VST FX** (comp/EQ/etc.) via becky's **C++ VST3
  host** (`native/audio-host` + `internal/audiohost`). The routing JSON gives the buses;
  the per-bus FX-chain prefs (from `becky-cubase`, below) give the plugins.
- **Render/bounce-in-place happens IN becky-canvas** (render a track through its bus FX →
  bake to audio → disable the heavy VSTs). The offline core exists (`--render-song`); the
  per-track + VST-FX version uses the C++ host.
- **REAPER-CLI is an OPTIONAL bridge, not the home** — for "process this WAV through a chain
  and hand it back" when a plugin is easier to drive there. Use it as a workflow, don't make
  it the destination. (`becky-reaper` can render a stem and return the WAV to a canvas track.)

The routing JSON is engine-agnostic (buses + assignments); the plugins load onto those buses
INSIDE becky-canvas. WRITING stays lightweight (engine synth/sampler); "finalize" loads the
heavy VSTs once.

## Step 0 — DISCOVER his chains first (he doesn't remember them)
Jordan doesn't know his plugin chains — they're saved in Cubase 11. `becky-cubase` finds them:
```
go run ./cmd/cubase scan          # scans the default Cubase template/preset folders
go run ./cmd/cubase scan --dir "C:\Users\<him>\Documents\Cubase Projects"
```
It ranks the VST plugins across his .cpr/.trackpreset files (most-used first) + a per-file
breakdown. **Have Jordan confirm the critical few** → those become the per-bus FX-chain prefs
(`~/.becky/fxchains.json`, the next thing to build) that Step 3 loads onto the buses.

## What cloud already built + PROVED (the deterministic half — done)
- `internal/autoroute`: `BusFor(label)` (the rule decision) + `Apply(arr, ruleset)` (routes
  every track, ensures the bus tree, wires sidechains). Ruleset is **user-editable JSON**
  (`~/.becky/routing.json`), so Jordan's rules are set once. 7 tests.
- `becky-route` CLI + `becky-song --route`. PROVEN offline (one command, inspectable):
  ```
  go run ./cmd/route bus "serum bass"      → "serum bass" → BASS
  go run ./cmd/song "dark trap" --route    → drums→DRUMS bass→BASS chords/melody→SYNTH, 7 buses, kick ducks BASS
  ```

---

## THE WORK ORDER (drive to completion; paste evidence into §6)

### Step 1 — PROVE the routing offline (cloud already did; you confirm)
```
cd becky-go
go run ./cmd/route rules --init          # writes the editable default ruleset to ~/.becky/routing.json
go run ./cmd/route bus "kick"            # → DRUMS
go run ./cmd/route bus "serum bass"      # → BASS   (the load-bearing rule)
go run ./cmd/song "metalcore" --route --out routed
```
- [ ] DONE WHEN: `routed.json` has `buses` + every track's `strip.bus` set. Open
  `~/.becky/routing.json`, confirm it reads like Jordan's rules, tweak one line, rerun —
  the change takes effect. Paste the `becky-route apply` decision lines.

### Step 2 — auto-route on add-track in becky-canvas (lightweight, no plugins)
When a track is added in the canvas, it should land on its bus immediately — zero clicks.
- Source: `internal/autoroute.BusFor` / `Apply`.
- Target: the canvas add-track path (`cmd/canvas` — `applyAddTrack` in `internal/ctledit`,
  and the dock/agent "add a bass track" flow). After adding a track, call
  `autoroute.Apply(a.arr, autoroute.Load())` (or set just the new track's `Strip.Bus =
  BusFor(id)`), then `applyArr`. Make it a `ctledit` op (`route` / `auto_route`) too, so
  the agent box ("route everything", "set up my buses") drives it — dual-operability.
- A new canvas session can START routed: call `autoroute.Apply` on the empty arrangement
  so the bus tree exists (lightweight — buses only, no plugins).
- [ ] DONE WHEN: adding/ generating a track in the canvas shows it on the right bus in the
  mixer panel, with no manual routing. Screenshot the mixer.

### Step 3 — "finalize to REAPER": load his plugins onto the routed buses (the heavy step, ONCE)
This is the deterministic-template recreation, done at the end so writing stays fast.
- Source: `internal/reaper` (`JordanTemplate`, `FromArrangement`) + `becky-reaper`. The
  routed `dawmodel.Arrangement` (buses + assignments + sidechains from Step 1) maps onto
  REAPER tracks/folders.
- Build: a per-bus **FX-chain preference** file (his plugin chains per bus — e.g. DRUMS bus
  → his glue comp + EQ; BASS → his chain), analogous to `~/.becky/routing.json`. See
  `SPEC-BECKY-MIX-JST.md` (per-bus FX chains) + `dawmodel` `ProjFX`. Then `becky-reaper`
  emits ReaScript `TrackFX_AddByName` to load each plugin onto its bus/track.
- [ ] DONE WHEN: `becky-reaper` opens a REAPER session where becky-song's tracks are on the
  right buses AND his real plugins are inserted (SD3 on the drum bus, etc.). Confirm in REAPER.
  (Needs his REAPER + plugins — local only. The routing JSON that drives it is cloud-proven.)

### Step 4 — bounce-in-place (bake the FX, free the CPU)
Standard producer flow: render a track THROUGH its FX chain to audio, replace the MIDI+FX
with the baked audio, disable the heavy VSTs.
- In REAPER: becky drives `Apply FX to items` / render-track-in-place via ReaScript, then
  bypasses the track's FX. (becky-reaper authors the action; REAPER does the render.)
- becky-canvas equivalent (later): `becky-daw-engine --render-song` already bounces the
  whole mix; a per-track bounce + an `audio` track that replaces the MIDI is the in-canvas
  version (uses the C++ VST host for VST FX).
- [ ] DONE WHEN: a track can be bounced to audio and its VSTs disabled, CPU drops. Confirm.

### Step 5 — report back
Update CLAUDE.md §6 with the checked boxes + evidence (the `becky-route` decisions, the
mixer screenshot, the REAPER session). Don't claim done without it.

---

## Two product rules to honor while building
- **No decision menus (anti-fatigue):** ONE result by default. If there are options
  (variations, alternate routings), don't show them — let Jordan cycle ("next"/"another")
  only if he asks. `becky-song` already defaults to one song; keep that everywhere.
- **Labels are the contract (dummy-proof):** routing is by label. Name a track `kick`, it
  goes to DRUMS; `serum bass`, it goes to BASS. The rules live in `~/.becky/routing.json` —
  edit once, applies everywhere. A small local model is OPTIONAL later for fuzzy labels;
  the deterministic table is the default and must always work without it.
