# SPEC-BECKY-CREATOR.md — Becky Review becomes the full autopilot creator-editing suite

> **STATUS: DESIGN ONLY (2026-07-01), awaiting Jordan's go/no-go on the open decisions in
> §6. This is the umbrella/vision spec — each numbered component in §3 gets its own
> dedicated `SPEC-BECKY-<NAME>.md` when it's greenlit for build.** Grounded in a direct
> code audit of `gui/BeckyReview/`, `cmd/clip/bridge.go`, `internal/assistant`,
> `internal/edl`/`internal/reel`/`internal/footage`, `cmd/cut`, and `internal/otio` (2026-07-01),
> plus a competitive audit of clipwith.ai, aiaicaptain.ai, and wisecut.ai. Companion doc:
> `SPEC-BECKY-NLE.md` (see §6 decision #1 — this spec proposes that plan be paused).

---

## 0. TL;DR — what this is, in plain terms

Becky Review started as a fast way to review clips the forensic pipeline flagged. It has
quietly become the video editing GUI Jordan actually uses — it already has a real
single-track timeline (drag-reorder, trim, split, undo/redo, save/load) and an assistant
that can drop a multi-video clip list into it from one typed command. **That's the
product now.** This spec does not propose replacing it or building a second app. It maps
out the missing pieces that turn it from "a good manual timeline with a smart search box"
into what clipwith.ai / aiaicaptain.ai / wisecut.ai sell: **drop in raw footage, get a
usable first cut back automatically, then do your final pass in the same timeline you
already know.**

The single biggest missing piece is not any one AI feature — it's that **today nothing
proposes a full draft cut on its own.** Everything below is in service of one button:
**"Autopilot."** You point it at a folder of raw footage, it runs every stage that can run
without you, and it hands you back a `.reel.json` already loaded in the timeline you use
today — cut, captioned, maybe reframed, maybe with B-roll and music — ready for your eye
and your final trims. Nothing about the tool you've already learned changes. It just
starts you 80% of the way there instead of at 0%.

---

## 1. Architecture — where the pipeline sits today, and where it needs to grow

```
                 ┌─────────────────────────────────────────────────────────┐
Jordan ────────► │  Becky Review (gui/BeckyReview) — WPF shell + WebView2   │
  (mouse/kbd)    │  ┌───────────┐ ┌────────────┐ ┌──────────────────────┐  │
                 │  │ FIND       │ │ VIDEO       │ │ TIMELINE (#track)    │  │
                 │  │ file list  │ │ native mpv  │ │ single flat clip list│  │
                 │  │ + search   │ │ overlay-hwnd│ │ drag/trim/split/undo │  │
                 │  └─────┬──────┘ └─────┬──────┘ └──────────┬───────────┘  │
                 └────────│──────────────│───────────────────│─────────────┘
                          │ NDJSON stdio (35 verbs today)     │
                          ▼
                 becky-clip bridge (cmd/clip/bridge.go) — one warm *App
                 index · reel · undo/redo · internal/assistant · export
                          │
        ┌─────────────────┼──────────────────────────────────────────────┐
        ▼                 ▼                  ▼               ▼           ▼
  internal/footage   internal/assistant  internal/edl    internal/reel  internal/otio
  (folder+transcript (clip-picking, Tier  (flat Clip list) (linear      (export-only:
   index, grep)       0/1/2 cost ladder)                    ffmpeg      OTIO/EDL/FCPXML/
                                                             concat +    MLT/VEGAS)
                                                             overlay)

  ── NEW, this spec ────────────────────────────────────────────────────────
  becky-cut (silence)  highlight-detect   style-profile    caption-style   reframe
  wired as a verb  ►   (new)          ►   (new, mines   ►  render (new) ► (new, reuses
                                           saved .reel.json)                face-track)
                                                    │
                                                    ▼
                                          B-roll match (new) + music/duck (new)
                                                    │
                                                    ▼
                                    multi-track Reel (structural prerequisite)
```

**Design rules (load-bearing, carried over from the rest of the suite):**
- Every new stage is its own becky-shaped unit (a package + a bridge verb, or a standalone
  CLI the bridge shells out to) — never a monolith bolted onto `bridge.go`. Single-tool
  principle holds.
- **Autopilot proposes, it never silently commits.** It produces a `.reel.json` you load
  and can undo/redo/trim exactly like a manually built one. This is the same
  propose-then-apply contract `internal/assistant` already uses for chat commands — extend
  it, don't invent a second one.
- Forensic mode and creator mode are **the same app, same data model.** The overlay/
  provenance/corroboration behavior for evidence review must not regress. New stages are
  additive and can be toggled off (a forensic review session never needs highlight-detect
  or B-roll).
- Offline-first stays true for every stage where it's feasible; anything that needs a
  network call (e.g., a licensed music library) is flagged explicitly, not assumed.

---

## 2. Reuse map — what already exists and what each new stage stands on

| Existing package | What it gives today | Reuse for this spec |
|---|---|---|
| `internal/footage` | Folder discovery, 5-tier transcript-sidecar matching, `GrepTranscripts()` | Index a personal B-roll library the same way (§3.6); no new discovery code needed |
| `cmd/cut` (becky-cut) | auto-editor + a real Silero-VAD post-pass; CLI only today | Wrap as a bridge verb (§3.1) — zero new detection logic |
| `internal/assistant` | Cost-tiered (Tier 0 regex / Tier 1 local / Tier 2 frontier) clip-picking, `Proposal{Actions,...}` contract, map-reduce funnel over transcripts | The exact shape "Autopilot" reuses — a new deterministic Tier-0 "build a draft cut" action instead of only responding to typed asks |
| `cmd/clip/bridge.go` | 35-verb NDJSON dispatch over one warm `*App`; adding a verb = one method + one `case` | Add verbs per stage: `autocut_silence`, `detect_highlights`, `style_profile`, `caption_style`, `reframe_vertical`, `suggest_broll`, `sync_music` |
| `internal/edl` / `internal/reel` | Flat `Clip{ID,Source,In,Out,Label,Meta}` list; linear ffmpeg concat + forensic drawtext overlay; no transitions/speed | **Needs the multi-track restructure (§3.9)** before B-roll/music/caption tracks can layer over the main cut |
| `internal/otio` | Full export: OTIO/EDL/FCPXML/MLT/VEGAS, all real | No new work — this is already ahead of every competitor researched |
| `gui/BeckyReview/ui/app.js` | Drag-reorder, trim, split-at-playhead, undo/redo, save/load, multi-select | Add an "Autopilot" button + per-stage progress + (once §3.9 lands) additional timeline lanes |
| becky-identify / InsightFace (elsewhere in the suite) | Face detection already used for forensic naming | Reuse for reframe (§3.5) instead of a new model |
| `internal/orchestrate` (self-regulation / corroborate-then-conclude engine) | Forces ≥2 independent signals before a forensic conclusion | The same pattern fits highlight-detection (§3.2) almost exactly — "is this moment worth keeping" via corroborating signals instead of "is this person on screen" |
| becky-imagegen (Krea-2) | Deterministic local text→image | Fallback B-roll generator when no matching real footage exists (§3.6) |
| `internal/audioengine` (DAW sidechain/ducking) | Real sidechain ducking already built for the drum-machine mixer | Reuse verbatim for music-under-speech ducking (§3.8) |
| `internal/arrange` / becky-beat | Tempo/beat detection for composition | Reuse for cutting music to a beat grid (§3.8) |

---

## 3. The pipeline stages — what's built, what's missing, and what to research first

### 3.1 Silence / filler cut — **BUILT, needs wiring**
`cmd/cut` already does this well (auto-editor + Silero-VAD false-keep correction). It's a
standalone CLI today, not reachable from Becky Review. **Work:** add an `autocut_silence`
bridge verb that shells out to it and returns keep-segments as `edl.Clip`s. No research
needed — this is the cheapest, most-proven win and should be Phase 1.

### 3.2 Highlight / best-moment detection — **MISSING** (becky-hits only places an
externally-supplied list; it detects nothing itself)
This is the one every competitor sells as the core value (Wisecut's "viral moments,"
ClipWith's visual+transcript scoring). becky's own corroborate-then-conclude philosophy is
actually a strong fit here — treat "is this clip worth keeping" the same way `internal/
orchestrate` treats "is this person on screen": don't trust one weak signal.
Candidate signals to corroborate, cheapest first:
1. **Deterministic, zero-model v1:** transcript-grep for reaction/emphasis phrases
   ("no way," laughter markers if the ASR captures them), audio RMS/energy peaks, speech
   pace changes — all things `internal/footage` and the transcript pipeline already expose.
2. **Tier-1/Tier-2 LLM scoring:** reuse `internal/assistant`'s existing cost ladder to score
   a transcript window for "quotable/engaging," the same funnel shape already used for
   quote search.
3. **Visual corroboration:** becky already has frame-level watching (Gemma-4 via
   becky-vision/becky-validate) elsewhere in the suite — sampling frames for
   motion/expression energy as a *third* independent signal, never the only one.
Needs its own spec (`SPEC-BECKY-HIGHLIGHTS.md`) to pick the exact scoring thresholds —
flag this as "research a class, then verify" per CLAUDE.md, same as the TTS/vision model
picks.

### 3.3 Auto-captions with real styling — **PARTIAL**
`cmd/captions` today only decides whether to trust existing subtitles vs. re-run ASR — it
doesn't style anything. The forensic lower-third overlay (`drawtext.go`, driven through
mpv's ASS `osd-overlay`) is real infrastructure worth reusing: **libass/ASS already
supports per-syllable karaoke tags (`\k`)**, and word-level timestamps already come out of
Parakeet ASR. Work needed: a caption-style renderer (a handful of presets — bold pop-on,
karaoke-highlight, minimal) built on the same ASS pipeline, exposed as a `caption_style`
verb. Low research risk — this is mostly engineering, not model selection.

### 3.4 B-roll suggestion / insertion — **MISSING, nothing in code**
Needs three things, none built yet:
1. A personal B-roll library, indexed the same way `internal/footage` already indexes case
   folders (that discovery code is directly reusable).
2. A matching mechanism — candidates to research, cheapest first: (a) keyword/tag matching
   against transcript topics (deterministic, controllable, good v1), (b) embedding-based
   semantic search over library metadata (needs picking a small local embedding model —
   research-a-class-then-verify applies), (c) LLM keyword extraction reusing the assistant's
   Tier-1 ladder.
3. A generated-footage fallback when nothing in the library matches — **becky-imagegen
   (Krea-2) already exists and is exactly this**, no new model work required for that half.
Needs `SPEC-BECKY-BROLL.md`.

### 3.5 Auto-reframe for vertical — **MISSING**
No 9:16/portrait auto-crop exists. But becky already runs face detection (InsightFace, used
for forensic naming) — a speaker-centered crop that tracks the detected face centroid is a
straightforward reuse rather than a new model pick. Saliency-based crop (no face required,
for B-roll/establishing shots) would be the one piece needing a small research pass.
Needs `SPEC-BECKY-REFRAME.md`.

### 3.6 Style-learning from Jordan's own past edits — **MISSING, but the cheapest,
highest-differentiation win in this whole spec**
Of the three competitors researched, only aiaicaptain.ai claims this, and it doesn't
disclose the mechanism. **Becky Review already has exactly the training data for free**:
every finished edit is saved as a `.reel.json` (`save_reel`/`load_reel` already exist).
v1 needs **no model at all** — pure statistics mined from Jordan's own saved reels:
average clip length, cuts-per-minute, keep-ratio against source footage length, which
caption style he ends up using most, how often he adds B-roll. Store this as a
`style_profile.json` and feed it as *defaults/priors* into every other autopilot stage
(bias highlight-detection's target clip length toward his historical average, default
caption style to his most-used preset, etc.). This should be one of the very first
components built — it's deterministic, offline, low-risk, and it's the feature that makes
the output feel like *his* edit instead of a generic auto-cut. Needs
`SPEC-BECKY-EDIT-STYLE.md`, but the core logic is almost build-ready as spec'd here.

### 3.7 Multicam / best-take selection — **MISSING, low priority**
No competitor researched does this convincingly either, and it's unclear it matches
Jordan's actual workflow (solo social-media editing is usually single-camera). Recommend
parking this — revisit only if a real multicam need shows up.

### 3.8 Music sync + auto-ducking — **MISSING in the video pipeline, but the DAW side
already has the hard parts built**
`internal/audioengine` already has real sidechain ducking (built for the drum-machine
mixer) and `internal/arrange`/becky-beat already do tempo/beat detection for composition.
The video-side gap is just wiring: pick a music bed (from a licensed/personal library —
**this needs Jordan's input, see §6 decision #3**), align cuts or at least drops to the
beat grid, duck under speech using the existing sidechain logic. Needs
`SPEC-BECKY-MUSICBED.md`.

### 3.9 Multi-track timeline data model — **STRUCTURAL PREREQUISITE, not optional**
`edl.Reel.Clips` is a flat, single-track list today, and `internal/reel`'s renderer is a
linear ffmpeg concat with no overlay/mix support. B-roll (needs to sit over the main cut),
a music bed (needs to run under the whole timeline), and styled captions (arguably their
own lane) all require **actual layering**, not just more items in one list. This is real,
non-trivial engineering — `Reel.Clips []Clip` → `Reel.Tracks []Track{Kind, Clips}`, plus a
rewrite of `internal/reel`'s ffmpeg filter graph to composite tracks instead of
concatenating one, plus a second lane in `gui/BeckyReview/ui/app.js`'s `#track` UI. This
should be **Phase 0** — nothing in §3.4/§3.8/§3.3-styled-lane can land cleanly without it.

### 3.10 Export to standard NLE formats — **already fully built, no work needed**
`internal/otio` covers OTIO/EDL/FCPXML/MLT/VEGAS with real writers and passing selftest
assertions. This is already ahead of all three competitors researched (all three are
closed-web/MP4-out or, at best, native-plugin-only). Nothing to do here except keep it in
sync once the multi-track model (§3.9) lands — the export writers will need multi-track
awareness too.

---

## 4. What Jordan actually sees — the Autopilot walkthrough

1. Open Becky Review (same shortcut as today, nothing new to install).
2. Click **Autopilot** (new button next to the existing search box).
3. Pick a folder of raw footage (same folder-picker pattern the FIND column already uses).
4. A progress list runs down the stages that are wired (silence cut → highlights →
   captions → reframe → B-roll → music), each togglable off before it starts if he only
   wants some of them.
5. It lands him in the **same timeline he already knows** — `.reel.json` loaded, clips
   trimmed, captions burned in preview, everything still draggable/trimmable/undo-able.
6. He does his normal manual pass (this part never changes) and exports through the
   existing `export`/`write_edl` verbs — including full OTIO/EDL/FCPXML if he wants to
   finish in Premiere/Resolve/VEGAS instead.

No new mental model, no CLI, no jargon dialogs — one new button that does more work before
he has to touch anything.

---

## 5. Build plan — phases (each phase gets its own dedicated spec before it's built)

- [ ] **Phase 0 — multi-track data model** (§3.9). Prerequisite plumbing; a spike first to
      confirm the ffmpeg filter-graph rewrite is tractable without destabilizing the
      existing forensic render path.
- [ ] **Phase 1 — wire becky-cut as a bridge verb** (§3.1) + an "Autopilot: cut silence"
      button. Reuses 100% existing, proven code. Cheapest possible first slice.
- [ ] **Phase 2 — style-profile from saved `.reel.json` history** (§3.6). Deterministic,
      no models, high differentiation. `SPEC-BECKY-EDIT-STYLE.md`.
- [ ] **Phase 3 — highlight/best-moment detection** (§3.2). `SPEC-BECKY-HIGHLIGHTS.md`,
      research the scoring approach before building.
- [ ] **Phase 4 — auto-captions with styling** (§3.3). `SPEC-BECKY-CAPTIONS-STYLE.md`.
- [ ] **Phase 5 — auto-reframe vertical** (§3.5), reusing existing face-detection.
      `SPEC-BECKY-REFRAME.md`.
- [ ] **Phase 6 — B-roll suggestion/insertion** (§3.4). `SPEC-BECKY-BROLL.md`.
- [ ] **Phase 7 — music sync + ducking** (§3.8), pending decision #3 below.
      `SPEC-BECKY-MUSICBED.md`.
- [ ] *(parked)* Multicam/best-take (§3.7) — revisit only on real need.

Each phase ships the same way the rest of the suite does: deterministic Go core + tests
first, model/hardware boundary named explicitly, offline proof before any "local agent"
handoff.

---

## 6. Open decisions for Jordan

1. **Becky Review is the product now, not the Shotcut fork.** `SPEC-BECKY-NLE.md` proposed
   forking Shotcut as the real NLE; that work never happened in this repo (confirmed —
   zero QML/Shotcut references exist here) and you've since started actually using Becky
   Review as your daily editor. This spec assumes Shotcut adoption is **paused**, the same
   way `CANVAS-NORTH-STAR.md` pinned becky-canvas over REAPER after that direction
   ping-ponged for weeks. **Confirm this, or say if you still want the Shotcut path — this
   is exactly the kind of flip-flop CLAUDE.md warns about, better to pin it once now.**
2. **Phase order** — the plan above front-loads the cheapest/highest-value wins (silence
   cut wiring, then your own style profile) before the model-research-heavy stages
   (highlights, B-roll, reframe). Confirm that ordering matches what you'd actually use
   first, or reorder.
3. **Music bed source** — do you have a personal/licensed royalty-free music library
   already, or does this need a licensing decision before §3.8 can start? This blocks
   Phase 7 specifically, nothing else.
4. **B-roll library** — same question as above for §3.4: is there an existing folder of
   your own B-roll footage to index, or does Phase 6 start from becky-imagegen-generated
   stills/clips only?
5. **Forensic/creator mode split** — should Autopilot and its new buttons be hidden by
   default in a case-review session (so evidence review never looks cluttered), surfaced
   only when a folder isn't tagged as a forensic case? Or should everything just always be
   visible? Low-stakes, but worth deciding before the UI work in Phase 1.

---

## 7. Non-goals (for this spec)

- Not building a second app — everything here extends Becky Review in place.
- Not replacing `internal/otio`'s export (already best-in-class vs. researched
  competitors).
- Not doing multicam/best-take (§3.7) — parked pending real need.
- Not standing up a cloud render service or a closed web editor — offline-first stays.
- Not touching the forensic corroboration/overlay behavior — new stages are additive and
  toggle off cleanly for review sessions.

---

## 8. Build plan — cloud vs. local baton (pre-build; this section becomes the shipped-vs-left report once work starts)

**Cloud can build without hardware/models:**
- Phase 0's multi-track `Reel`/`Track` data model + a rewritten (still cuts-only, now
  multi-track) `internal/reel` filter graph, fully unit-tested.
- Phase 1's `autocut_silence` bridge verb (pure wiring, no new detection logic).
- Phase 2's style-profile miner (pure statistics over existing `.reel.json` files, no
  models) — this can ship essentially complete from cloud alone.
- The bridge-verb scaffolding for Phases 3-8 (deterministic cores, degrade-never-crash
  stubs where a model is required) — same pattern as the rest of the SPEC-FACTORY tools.

**Needs local (hardware/model/GUI):**
- Wiring `gui/BeckyReview/ui/app.js`'s new "Autopilot" button + progress UI + (once Phase 0
  lands) the additional timeline lane(s) — needs the real window to click through.
- Any model selection/tuning for Phases 3-6 (highlight scoring thresholds, B-roll
  embedding model if that path is chosen, reframe crop tuning) on Jordan's real footage.
- Phase 7's music-bed integration once decision #3 above is settled.

---

## 9. Composition — no new orchestrator

Every new stage stays a separate deterministic package + bridge verb, invoked the same way
`internal/assistant` already invokes existing tools. Autopilot is a sequencer over existing
verbs, not a new monolith — it calls `autocut_silence`, then `detect_highlights`, etc., the
same way a human could call them one at a time from chat. This preserves the single-tool
principle: any one stage can be improved, disabled, or replaced without touching the others.

---

## 10. Honest open questions

- Exactly how much of the multi-track render rewrite (§3.9) can reuse `internal/reel`'s
  existing overlay/drawtext code vs. needing a genuine new filter-graph builder — needs a
  spike, not a guess, before Phase 0 is scoped in detail.
- Whether highlight-detection (§3.2) should default to conservative (fewer, high-confidence
  highlights) or generous (more candidates, human trims down) — probably conservative,
  matching the corroborate-then-conclude ethos, but worth confirming against how Jordan
  actually wants to review Autopilot's output.
- Whether style-profile (§3.6) should be one global profile or per-content-type (e.g. a
  different rhythm for vlogs vs. gaming clips) if Jordan edits more than one style of video
  — affects the profile schema, cheap to decide either way before Phase 2 starts.

---

## 11. Sources

- Internal: `gui/BeckyReview/*`, `cmd/clip/bridge.go`, `cmd/clip/app.go`,
  `internal/assistant/{router,funnel,classify,assist}.go`, `internal/edl/*`,
  `internal/reel/*`, `internal/footage/*`, `cmd/cut/main.go`, `internal/otio/*`,
  `internal/orchestrate`, `internal/audioengine`, `internal/arrange`, becky-imagegen
  (`SPEC-BECKY-IMAGEGEN.md`), `GAP-ANALYSIS.md` §5 (video/NLE gaps), `SPEC-BECKY-NLE.md`,
  `CANVAS-NORTH-STAR.md` (the precedent for pinning a direction after ping-ponging).
- External (competitive reference, researched 2026-07-01): clipwith.ai/how-it-works
  (transcript + visual-index + tiered-LLM edit reasoning), aiaicaptain.ai (NLE-native
  plugin, style-learning "Presets" from past projects), wisecut.ai (Autopilot: silence cut
  + highlight detection + reframe + captions + music/ducking).
