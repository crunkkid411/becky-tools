# SPEC-BECKY-CREATOR.md — Becky Review becomes the full autopilot creator-editing suite

> **STATUS: DESIGN ONLY (2026-07-01, revised after real research + two rounds of
> correction), awaiting Jordan's go/no-go on §6.** This is the umbrella/vision spec — each
> numbered component in §3 gets its own dedicated `SPEC-BECKY-<NAME>.md` when it's greenlit.
> Grounded in a direct code audit of `gui/BeckyReview/`, `cmd/clip/bridge.go`,
> `internal/assistant`, `internal/edl`/`internal/reel`/`internal/footage`, `cmd/cut`, and
> `internal/otio`; a competitive audit of clipwith.ai, aiaicaptain.ai, and wisecut.ai; and six
> real cited research passes (academic papers, engineering docs, vendor manuals — see §11)
> covering highlight detection, editing-style learning, video reframing, B-roll retrieval,
> and real-time multi-track playback engines. **This revision exists because the first draft
> got two things wrong from unexamined assumptions, and Jordan's own corrections (he has
> never used Becky Review for creative editing — his real edit history is in VEGAS Pro 18;
> and a Shotcut-engine adoption was already tried last week and abandoned for timeline lag)
> changed the plan materially. Where a claim below is sourced, it says so; where it's still
> an open bet, it says that too — see §10.** Companion doc: `SPEC-BECKY-NLE.md` (§6 decision
> #1 — this spec now has *evidence*, not just a hunch, that the Shotcut plan should stay paused).

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
- **No default answer for "which tool judges this" — it's decided per-subtask against real
  evidence, not a blanket rule.** The first draft of this spec claimed the AVLM (Gemma-4)
  should always be the primary judge for anything requiring "understanding." Real research
  (§3.2, §3.4) shows that's wrong as a blanket rule: the published state of the art for both
  highlight detection and B-roll retrieval is purpose-built, specifically-trained models —
  not a general multimodal LLM prompted to judge. So the actual rule is narrower: **cheap
  deterministic signals narrow candidates for time budget; a purpose-built or benchmarked
  approach makes the call where one exists and is checkable against real footage; the AVLM
  is one candidate approach to test empirically, not an assumed winner.** Per-frame
  geometry/tracking (reframe-crop), signal processing (beat detection, ducking), and plain
  statistics (style-profile mining) are a different kind of problem entirely and stay on the
  lightweight deterministic tools already in the repo regardless.
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
| `internal/footage` | Folder discovery, 5-tier transcript-sidecar matching, `GrepTranscripts()` | Index a personal B-roll library the same way (§3.4); no new discovery code needed |
| `cmd/cut` (becky-cut) | auto-editor + a real Silero-VAD post-pass; CLI only today | Wrap as a bridge verb (§3.1) — zero new detection logic |
| `internal/assistant` | Cost-tiered (Tier 0 regex / Tier 1 local / Tier 2 frontier) clip-picking, `Proposal{Actions,...}` contract, map-reduce funnel over transcripts | The exact shape "Autopilot" reuses — a new deterministic Tier-0 "build a draft cut" action instead of only responding to typed asks |
| `cmd/clip/bridge.go` | 35-verb NDJSON dispatch over one warm `*App`; adding a verb = one method + one `case` | Add verbs per stage: `autocut_silence`, `detect_highlights`, `style_profile`, `caption_style`, `reframe_vertical`, `suggest_broll`, `sync_music` |
| `internal/edl` / `internal/reel` | Flat `Clip{ID,Source,In,Out,Label,Meta}` list; linear ffmpeg concat + forensic drawtext overlay; `proxy.go`'s `ScrubProxy` already proves intra-frame/CFR proxy media for single-track | Single-track stages (silence cut, captions) need nothing new; B-roll/music/multi-caption need **§3.9's Option A/B decision** — this is now a real playback-engine question, not a filter-graph rewrite |
| `internal/otio` | Full export: OTIO/EDL/FCPXML/MLT/VEGAS, all real | No new work — already ahead of every competitor researched, and it's the mechanism Option B (§3.9) would hand off through |
| `gui/BeckyReview/ui/app.js` | Drag-reorder, trim, split-at-playhead, undo/redo, save/load, multi-select | Add an "Autopilot" button + per-stage progress + (only if Option A is chosen) additional timeline lanes |
| becky-identify / InsightFace (elsewhere in the suite) | Face detection already used for forensic naming | Reuse as the face-signal input to an AutoFlip-style weighted-fusion reframe (§3.5), not a standalone naive crop |
| `internal/orchestrate` (self-regulation / corroborate-then-conclude engine) | Forces ≥2 independent signals before a forensic conclusion | Still the right ethos for how conservatively Autopilot should surface highlights (§3.2, §10) — but the actual highlight-detection mechanism itself is an open empirical question (§3.2), not something this pattern settles on its own |
| becky-imagegen (Krea-2) | Deterministic local text→image | Fallback B-roll generator when no matching real footage exists (§3.4) |
| `internal/audioengine` (DAW sidechain/ducking) | Real sidechain ducking already built for the drum-machine mixer | Reuse verbatim for music-under-speech ducking (§3.8), if Option A is chosen |
| `internal/arrange` / becky-beat | Tempo/beat detection for composition | Reuse for cutting music to a beat grid (§3.8) |
| `vegas/BeckyReviewTimeline.cs` | Working proof-of-concept of driving VEGAS Pro's `ScriptPortal.Vegas` scripting API from this repo | Direct template for Phase 2's history-extraction script (§3.6) |

---

## 3. The pipeline stages — what's built, what's missing, and what to research first

### 3.1 Silence / filler cut — **BUILT, needs wiring**
`cmd/cut` already does this well (auto-editor + Silero-VAD false-keep correction). It's a
standalone CLI today, not reachable from Becky Review. **Work:** add an `autocut_silence`
bridge verb that shells out to it and returns keep-segments as `edl.Clip`s. No research
needed — this is the cheapest, most-proven win and should be Phase 1.

### 3.2 Highlight / best-moment detection — **MISSING** (becky-hits only places an
externally-supplied list; it detects nothing itself)

**This section has been wrong twice already, in opposite directions — recorded honestly
because the correction matters more than looking consistent.** First draft: cheap heuristics
decide, AI watching is a rare expensive double-check. Second draft (after Jordan's first
pushback): AI watching is the primary judge because it's cheap now. Real cited research
says neither is right as a blanket answer.

**What's actually sourced:** the published state of the art for this exact task — video
highlight detection / moment retrieval, benchmarked on datasets built for it (QVHighlights,
TVSum, Charades-STA) — is **purpose-built, specifically-trained detection architectures**,
not a general multimodal LLM prompted to judge a clip. UMT (arxiv.org/pdf/2203.12745),
QD-DETR (ar5iv.labs.arxiv.org/html/2303.13874), TR-DETR (arxiv.org/pdf/2401.02309), and
VideoLights (openreview.net/forum?id=F1cN3aoAty) are all transformer architectures trained
specifically to localize and score salient moments — the authors of UMT explicitly frame it
as "the first scheme to integrate multi-modal (visual-audio) learning" for this, i.e. a
trained model, not a prompted one. Notably, VideoLights *does* fold in a vision-language
model (BLIP-2) — but as a **feature extractor feeding a purpose-built architecture**, not as
the thing making the final call. That's a meaningfully different shape than "ask Gemma-4 if
this is a highlight."

There's also a specific, directly relevant documented failure mode for the
prompt-the-AVLM-directly approach: a 2026 paper (arxiv.org/html/2606.06926) found that
feeding a long video to a multimodal LLM with a fixed/uniform frame-sampling budget causes
**severe information loss** — sampling "a frame every 75 seconds" on a two-hour video misses
short salient events entirely. This is a real, cited risk specifically for the "just let the
big model watch it" approach on anything longer than a short clip.

**What this means for becky, honestly:**
1. Cheap deterministic signals (silence/transcript gaps, already computed by `internal/
   footage`/becky-cut) narrowing which windows get evaluated is *more* important given the
   frame-sampling failure mode above, not less — it keeps each evaluated window short enough
   that the sampling problem doesn't bite.
2. Within a narrowed window, **which approach actually judges best is an open, testable
   question, not a settled one** — a specialized trained detector (harder to build, matches
   the academic SOTA) vs. prompting Gemma-4 directly on a short clip (cheaper to build, no
   published evidence either way at this scale) are both real candidates that need
   evaluating against real labeled examples, not chosen from priors.
3. **Calibrate against Jordan's own past keep/cut decisions**, not a generic "virality"
   notion — whatever can be recovered from his VEGAS Pro edit history (§3.6) is the closest
   thing to ground truth for what *he* considers a keeper.
Needs its own spec (`SPEC-BECKY-HIGHLIGHTS.md`) that starts from the cited architectures
above and actually tests at least two approaches against Jordan's real footage before
picking one — that's the real research task, not a one-line model pick.

### 3.3 Auto-captions with real styling — **PARTIAL**
`cmd/captions` today only decides whether to trust existing subtitles vs. re-run ASR — it
doesn't style anything. The forensic lower-third overlay (`drawtext.go`, driven through
mpv's ASS `osd-overlay`) is real infrastructure worth reusing: **libass/ASS already
supports per-syllable karaoke tags (`\k`)**, and word-level timestamps already come out of
Parakeet ASR. Work needed: a caption-style renderer (a handful of presets — bold pop-on,
karaoke-highlight, minimal) built on the same ASS pipeline, exposed as a `caption_style`
verb. Low research risk — this is mostly engineering, not model selection.

### 3.4 B-roll suggestion / insertion — **MISSING, nothing in code**

**Also corrected from the last draft.** The previous version of this section proposed
"Gemma-4 describes the B-roll library once, then keyword-match the transcript against those
descriptions" as the primary mechanism, dismissing embedding search as an unnecessary later
optimization. The actual text-video retrieval literature says the opposite: **joint
text-video embedding retrieval is the benchmarked state of the art**, and
caption-then-match is used in real research only as a *supplementary* signal layered on top
of embedding retrieval, never as a standalone replacement. CLIP4Clip
(semanticscholar.org/paper/CLIP4Clip...) and X-CLIP (arxiv.org/pdf/2207.07285) both report
concrete state-of-the-art retrieval numbers on standard benchmarks (MSR-VTT, DiDeMo,
ActivityNet) using joint embeddings. Cap4Video (arxiv.org/pdf/2301.00184) — the paper
closest to "generate captions and use them for retrieval" — explicitly frames generated
captions as an *auxiliary* branch that "supplements the original Query-Video matching
branch," not a substitute for it. So a caption-then-keyword-match approach, on its own, is
the weaker method per the literature that actually tested this, not a reasonable shortcut.

Needs three things, none built yet:
1. A personal B-roll library, indexed the same way `internal/footage` already indexes case
   folders (that discovery code is directly reusable).
2. **A matching mechanism — a small local text-video (or text-image, if B-roll is treated as
   keyframes) embedding model should be the primary candidate**, with an LLM-generated
   description as a *secondary/supplementary* signal if it measurably helps, not the primary
   mechanism. This means real model research is needed here after all (which family of
   embedding model, how large, does it need fine-tuning on Jordan's own footage style) —
   this was wrongly waved off as unnecessary in the previous draft.
3. A generated-footage fallback when nothing in the library matches — **becky-imagegen
   (Krea-2) already exists and is exactly this**, no new model work required for that half.
Needs `SPEC-BECKY-BROLL.md`, starting from the CLIP4Clip/X-CLIP family as the reference
point for the matching model, not skipping straight to "just describe it with the AVLM."

### 3.5 Auto-reframe for vertical — **MISSING, but this one has a strong, real reference
architecture to copy — Google's AutoFlip**
No 9:16/portrait auto-crop exists in becky today. Unlike highlights/B-roll above, this
sub-problem has a well-documented, open, purpose-built reference implementation:
**Google's AutoFlip** (opensource.googleblog.com/2020/02/autoflip..., research.google/blog/
autoflip...), which is not naive single-signal face-following — it's a **weighted multi-signal
fusion**: each detected feature type (face, object, "salient region") gets an importance
weight, a `SignalFusingCalculator` combines them, and the crop path is chosen from three
behaviors per shot (stationary / constant pan / continuous track) after buffering the whole
shot, then smoothed via Euclidean-norm optimization against a low-degree polynomial camera
path — specifically to avoid jittery frame-to-frame crop jumps. A separate academic approach
("Watch to Edit: Video Retargeting using Gaze," arxiv.org/pdf/1807.03125) uses real gaze data
and dynamic programming to decide where to cut, with the crop path built from piecewise
constant/linear/parabolic segments — a more research-heavy alternative worth knowing about
but not necessary for v1.
**Revised plan:** don't build a naive "follow the face centroid" crop — replicate AutoFlip's
actual shape (weighted signal fusion + shot-buffered behavior choice + smoothed path
optimization), reusing becky's existing InsightFace detection as the face signal input
rather than reimplementing detection. This directly avoids AutoFlip's own documented reason
for existing: raw per-frame tracking is jittery and loses secondary subjects; the fusion +
smoothing is what fixes that.
Needs `SPEC-BECKY-REFRAME.md`, built directly against AutoFlip's published methodology.

### 3.6 Style-learning from Jordan's own past edits — **MISSING; the previous draft's whole
premise was false and has been replaced**
The first draft of this section assumed Becky Review's saved `.reel.json` files were "free
training data" from Jordan's past edits. **That's wrong — Jordan has never used Becky
Review for creative editing, only forensic review, so there is no such history there.**
His real editing history — years of it — lives in **VEGAS Pro 18** project files.

**What's actually recoverable, researched directly:** the `.veg` project file itself is
**not a documented or crackable format** — no official Sony/MAGIX spec exists, and no
maintained open-source parser was found. The real path is VEGAS Pro's own **documented
scripting API** (MAGIX's "VEGAS Pro Scripting API Summary," the `ScriptPortal.Vegas`
namespace), which exposes `Track`, `TrackEvent`, `Effect`/`Transitions`, `Media`, and
`Region` objects — confirmed working in practice by community script repos
(github.com/evankale/VegasScripts) and, directly relevant, **this repo's own
`vegas/BeckyReviewTimeline.cs` already drives this exact API pattern**. VEGAS can be
launched with a script attached from the command line (`vegas210.exe /SCRIPT ... /SCRIPTARGS
...`), so a script that walks `Project.Tracks` → `Track.Events`, dumps structured data, then
calls `Vegas.Exit()`, can in principle batch through a folder of old `.veg` files —
launching the real VEGAS process each time (there is no documented true headless/no-window
mode; this is a real, bounded, but non-trivial engineering task, not a quick win).

**What that recovers vs. doesn't, honestly:** per-project track count, event/clip durations
(→ cuts-per-minute, average clip length), transition types used, text/title-generator
presence, track layout (video/audio/B-roll structure) — all structural. It does **not**
recover footage content/topic or *why* a cut was made — the scripting API surfaces timeline
structure only, never editorial intent. This is consistent with the one directly relevant
academic reference found (arxiv.org/pdf/2105.06988, a video-editing-style-transfer paper),
which models "style" via **framing, content type, playback speed, and lighting per
segment** — a similarly structural, not semantic, representation. Notably, that paper (and
every other academic result found — arxiv.org/pdf/1801.10281, the Tsinghua "Write-A-Video"
paper, arxiv.org/pdf/2108.04294, arxiv.org/pdf/1907.07345) addresses one-shot style transfer
or universal continuity-editing conventions learned from a *general corpus of movies* — **no
academic work was found addressing "learn one specific person's long-term editing style
across many of their own past projects."** That means this remains a genuinely open
engineering bet, not something with an established recipe to copy — the VEGAS-mining path
is the most credible way to get *real* signal, but what to do with it afterward still has
to be worked out empirically against Jordan's actual footage, not assumed.
Needs `SPEC-BECKY-EDIT-STYLE.md`, scoped around VEGAS scripting extraction first — see §6
decision #6 for how much of his history is worth mining before building on top of it.

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

### 3.9 Multi-track timeline data model — **STRUCTURAL PREREQUISITE, and the single riskiest
item in this whole spec — now backed by real evidence, not a hand-wave**

The previous draft treated this as "rewrite `internal/reel`'s ffmpeg filter graph for
multi-track" — a data-structure problem. **Jordan corrected this directly: a Shotcut/MLT
integration was actually built last week (working functionally) and abandoned because
Shotcut's own timeline navigation was too laggy to use.** That's not a hypothetical risk,
it already happened once, on a mature, widely-used engine — and real research now explains
exactly why, which changes what "Phase 0" has to mean.

**Why MLT/Shotcut lagged (sourced):** MLT's own architecture docs (mltframework.org/docs/
framework/) describe a Tractor that pulls a frame from *every* track's producer on every
tick and composites in software — an N-track project means N simultaneous decodes with no
built-in GPU compositor. Shotcut's own lead developer, in the project's support forum,
states directly that non-intra-frame ("temporally compressed") source video is "much
slower" to seek, that "performance is already a known problem," and that the documented fix
is a proxy workflow (forum.shotcut.org/t/why-stutter/15132). Kdenlive's own maintainer-
tracked performance issue (invent.kde.org/multimedia/kdenlive/-/issues/439) lists missing
GPU acceleration and inefficient multitrack composition as known root causes. Separately,
a Shotcut-specific writeup (binarytides.com) states plainly that "shotcut does not and
cannot use the gpu decoder... to play the timeline" — CPU-only decode is architectural, not
a bug to be patched.

**Why the obvious fix ("extend `internal/reel`'s ffmpeg concat") would very likely repeat
this exact failure:** ffmpeg's own documentation confirms seeking is fundamentally
decode-forward-from-keyframe-and-discard, not indexed random access — quoting ffmpeg.org's
own docs directly: *"in most formats it is not possible to seek exactly... this extra
segment between the seek point and position will be decoded and discarded."* MLT's own docs
describe themselves as a "pull-based producer/consumer model" **specifically in contrast to**
"linear streaming models where data flows continuously forward through a fixed pipeline" —
i.e. MLT exists because plain ffmpeg filter graphs aren't built for exactly this. And
libmpv — which Becky Review's current smooth single-track preview already depends on — is
documented (mpv's own manual) as **one video output per instance**; `--lavfi-complex` can
combine multiple *inputs* but only into one static, non-runtime-changeable output graph,
never independent live-timed tracks. None of this is a data-model problem; it's a real-time
playback-engine problem, and there is direct local precedent that getting it wrong is
expensive (a week of work, thrown away).

**What actually works, per every pro NLE researched (including the one Jordan already
trusts):** Adobe Premiere's Mercury Playback Engine (GPU-accelerated compositing, not CPU
filter graphs, plus a green/yellow/red disk-cached preview-file system); DaVinci Resolve's
Optimized Media (auto-generated low-res/intra-frame proxies) plus Smart Cache (auto-caches
anything too heavy to play live); and — most relevant, since Jordan already uses this
successfully — **VEGAS Pro's own Dynamic RAM Preview**, which caches ahead-of-playhead
frames into RAM specifically for anything that can't decode/composite in real time
(help.magix-hub.com). The convergent pattern: **proxy/low-res media + GPU compositing + a
real ahead-of-playhead frame cache** — three things MLT's stock pipeline doesn't force by
default, which is the credible root cause of the Shotcut failure.

**Two real options, not one assumed path — this needs Jordan's input (§6 decision #7):**
- **Option A — build a thin custom compositor.** Becky already has half of this proven:
  `internal/reel`'s `ScrubProxy` already generates intra-frame, constant-frame-rate proxy
  media for single-track scrub, and it works. The missing half is compositing multiple
  layers live — the evidence above suggests running one libmpv instance per track (each
  decoding its own proxy stream into its own render-API surface, since mpv's single-stream
  decode+seek is already proven smooth in this app) and adding a genuinely new, small
  GPU-compositing layer above them to blend the surfaces plus caption/overlay text. This is
  real, scoped engineering — not a rewrite of `internal/reel`'s filter graph — but it is a
  **prototype/spike, not a guaranteed win**, and it has a real failure precedent to respect.
- **Option B — don't build a multi-track compositor at all.** Autopilot produces a rough
  cut + highlights + caption preview in Becky Review's existing single-track view (already
  proven smooth), and B-roll/music-bed layering happens **after** export, in VEGAS Pro
  itself — where Jordan already edits multi-track successfully today, and where
  `internal/otio` already has a working VEGAS export path. This sacrifices the "one button,
  fully composited in Becky Review" experience for B-roll/music specifically, but avoids the
  single riskiest engineering bet in this entire spec, and doesn't ask becky to out-engineer
  a problem three professional NLEs solve with dedicated GPU/cache subsystems.

This should be **Phase 0**, but Phase 0 is now "resolve decision #7 with a real prototype/
spike (if Option A), or scope the VEGAS hand-off cleanly (if Option B)" — not a data-model
rewrite assumed to just work.

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

- [ ] **Phase 0 — resolve the multi-track engine question** (§3.9). NOT a data-model
      rewrite — a real prototype/spike (Option A) or a scoped VEGAS hand-off design
      (Option B), decided per §6 decision #7 first. This gates B-roll/music/styled-caption
      layering; it does not gate Phase 1.
- [ ] **Phase 1 — wire becky-cut as a bridge verb** (§3.1) + an "Autopilot: cut silence"
      button. Reuses 100% existing, proven code, needs no multi-track anything. Cheapest
      possible first slice — can ship before Phase 0 is even decided.
- [ ] **Phase 2 — VEGAS history extraction** (§3.6). Batch-script VEGAS Pro's own scripting
      API over Jordan's past `.veg` projects to recover real structural edit data. Real
      engineering (VEGAS must launch per file), scope depends on §6 decision #6. Feeds the
      style-profile that later phases can use as a prior.
- [ ] **Phase 3 — highlight/best-moment detection** (§3.2). `SPEC-BECKY-HIGHLIGHTS.md` —
      test at least a specialized-detector approach and a direct-AVLM-prompt approach
      against real footage before picking one; don't assume either.
- [ ] **Phase 4 — auto-captions with styling** (§3.3). `SPEC-BECKY-CAPTIONS-STYLE.md`.
      Single-track-safe — doesn't need Phase 0 resolved.
- [ ] **Phase 5 — auto-reframe vertical** (§3.5), built directly against AutoFlip's
      published weighted-signal-fusion + smoothed-path method, not naive face-following.
      `SPEC-BECKY-REFRAME.md`.
- [ ] **Phase 6 — B-roll suggestion/insertion** (§3.4). `SPEC-BECKY-BROLL.md`, starting from
      a text-video embedding model (CLIP4Clip/X-CLIP family), not caption-then-keyword-match.
      Needs Phase 0/Option A if B-roll is to layer inside Becky Review; not needed at all
      under Option B.
- [ ] **Phase 7 — music sync + ducking** (§3.8), pending decision #3. Also gated on Phase
      0/Option A under the same logic as Phase 6.
- [ ] *(parked)* Multicam/best-take (§3.7) — revisit only on real need.

Each phase ships the same way the rest of the suite does: deterministic Go core + tests
first, model/hardware boundary named explicitly, offline proof before any "local agent"
handoff. **Phases 1 and 4 no longer depend on Phase 0 at all** — that dependency was an
artifact of the old "everything needs multi-track" assumption; silence-cut and caption
preview both work fine on the existing single-track timeline.

---

## 6. Open decisions for Jordan

1. **Becky Review is the product now, not the Shotcut fork — and this is now backed by a
   real result, not a hunch.** You already tried adopting a mature engine (Shotcut/MLT) and
   it failed specifically on timeline lag, the exact risk this spec's §3.9 is built around.
   `SPEC-BECKY-NLE.md`'s plan should stay paused on that evidence, the same way
   `CANVAS-NORTH-STAR.md` pinned canvas over REAPER. **Confirm, or say if there's more to
   the Shotcut story I should know before this gets pinned harder.**
2. **Phase order** — Phase 1 (silence cut) no longer waits on anything; confirm that's still
   the right first slice, or reorder given Phase 0's new shape.
3. **Music bed source** — personal/licensed library, or does this need a licensing decision
   first? Blocks Phase 7 only, and only matters if Option A (§3.9) is chosen.
4. **B-roll library** — existing folder of your own B-roll, or start from
   becky-imagegen-generated stills/clips only? Only matters if Option A is chosen.
5. **Forensic/creator mode split** — should Autopilot's buttons stay hidden in a
   case-review session, or always visible? Low-stakes, decide before Phase 1's UI work.
6. **How much VEGAS history is worth mining (§3.6)?** The extraction script has to launch
   VEGAS once per old project file — realistically slow over years of work. Do you want
   everything mined, a recent window (e.g. last 1-2 years), or a hand-picked set of projects
   you consider your best work? This changes Phase 2's scope a lot.
7. **The big one: Option A or B for multi-track (§3.9)?** A = build a real (but genuinely
   risky, prototype-first) compositor inside Becky Review so B-roll/music/captions layer in
   one app. B = keep Becky Review single-track (proven smooth) for the rough-cut/highlights/
   caption-preview part of Autopilot, and hand off to VEGAS Pro itself — where you already
   edit multi-track successfully — for B-roll and music layering, via the OTIO/EDL/VEGAS
   export that already works. B sacrifices some of the "one button" experience but carries
   far less engineering risk, given Shotcut already showed what this kind of bet can cost.
   **This decision should be made deliberately, not defaulted into** — it changes what
   Phase 0 even means.

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
- Phase 1's `autocut_silence` bridge verb (pure wiring, no new detection logic, no
  dependency on Phase 0 anymore).
- Phase 2's VEGAS-scripting-API extraction script skeleton (the `ScriptPortal.Vegas`
  traversal pattern, following `vegas/BeckyReviewTimeline.cs`'s existing shape) — but it
  cannot be *run* or verified without a real VEGAS Pro install, so cloud can only write it,
  not prove it.
- The bridge-verb scaffolding for Phases 3-6 (deterministic cores, degrade-never-crash
  stubs where a model is required) — same pattern as the rest of the SPEC-FACTORY tools.
- Option A's data-model half of Phase 0 (`Reel.Tracks` structure) *if* Option A is chosen —
  but NOT the playback-engine prototype itself, which needs real hardware/GPU to validate.

**Needs local (hardware/model/GUI/real footage) — this list grew after the research above,
not shrank:**
- The Phase 0 prototype/spike itself (Option A) — needs a real GPU, real footage, and
  Jordan's own eyes on whether it actually feels smooth, exactly the gate that caught
  Shotcut's failure. This cannot be proven from cloud; a green build is not evidence here.
- Running Phase 2's VEGAS extraction against Jordan's real project history (needs VEGAS Pro
  installed and his actual `.veg` files).
- Wiring `gui/BeckyReview/ui/app.js`'s new "Autopilot" button + progress UI + (if Option A)
  the additional timeline lane(s).
- Testing Phase 3's highlight-detection candidates (specialized model vs. direct AVLM
  prompt) against Jordan's real footage — this is now an explicit empirical comparison, not
  a model pick.
- Any model selection/tuning for Phase 6 (which text-video embedding model, whether it needs
  fine-tuning on Jordan's own B-roll style) and Phase 5 (AutoFlip-style crop tuning) on real
  footage.
- Phase 7's music-bed integration once decision #3 is settled.

---

## 9. Composition — no new orchestrator

Every new stage stays a separate deterministic package + bridge verb, invoked the same way
`internal/assistant` already invokes existing tools. Autopilot is a sequencer over existing
verbs, not a new monolith — it calls `autocut_silence`, then `detect_highlights`, etc., the
same way a human could call them one at a time from chat. This preserves the single-tool
principle: any one stage can be improved, disabled, or replaced without touching the others.

---

## 10. Honest open questions

- **Option A vs. B for §3.9 (decision #7) is the single biggest unresolved question in this
  whole spec**, and it's Jordan's to make, not a default to fall into — everything about
  B-roll/music/multi-caption scope depends on it.
- Whether a specialized highlight-detection architecture or a direct AVLM prompt performs
  better on Jordan's actual footage (§3.2) — genuinely unknown, needs a real empirical test,
  not a prior.
- Whether a small text-video embedding model needs any fine-tuning on Jordan's specific
  B-roll library to retrieve well (§3.4), or works acceptably off-the-shelf — unknown until
  tried against his real library.
- How much VEGAS history is practically worth mining (§3.6, decision #6) given the
  per-file-launch cost of the only viable extraction path.
- Whether style-profile (§3.6) should be one global profile or per-content-type — affects
  the profile schema, cheap to decide either way once Phase 2's real data exists.
- Whether highlight-detection (§3.2) should default to conservative (fewer, high-confidence
  highlights) or generous (more candidates, human trims down) — probably conservative,
  matching the corroborate-then-conclude ethos, but worth confirming against how Jordan
  actually wants to review Autopilot's output.

---

## 11. Sources

**Internal:** `gui/BeckyReview/*`, `cmd/clip/bridge.go`, `cmd/clip/app.go`,
`internal/assistant/{router,funnel,classify,assist}.go`, `internal/edl/*`,
`internal/reel/*` (incl. `proxy.go`'s existing `ScrubProxy`), `internal/footage/*`,
`cmd/cut/main.go`, `internal/otio/*`, `internal/orchestrate`, `internal/audioengine`,
`internal/arrange`, `vegas/BeckyReviewTimeline.cs`, becky-imagegen (`SPEC-BECKY-IMAGEGEN.md`),
`GAP-ANALYSIS.md` §5, `SPEC-BECKY-NLE.md`, `CANVAS-NORTH-STAR.md`.

**Competitive reference:** clipwith.ai/how-it-works, aiaicaptain.ai, wisecut.ai.

**Highlight detection (§3.2):** UMT — arxiv.org/pdf/2203.12745; QD-DETR —
ar5iv.labs.arxiv.org/html/2303.13874; TR-DETR — arxiv.org/pdf/2401.02309; VideoLights —
openreview.net/forum?id=F1cN3aoAty; long-video MLLM frame-sampling information-loss finding
— arxiv.org/html/2606.06926.

**Editing-style learning (§3.6):** style-transfer-from-one-source-video paper —
arxiv.org/pdf/2105.06988; video-story composition — arxiv.org/pdf/1801.10281; Write-A-Video
(Tsinghua) — cg.cs.tsinghua.edu.cn/papers/TOG-2019-Write-a-Video.pdf; continuity-editing
pattern learning — arxiv.org/pdf/2108.04294; imitation-learning cinematography controller —
arxiv.org/pdf/1907.07345; Adobe Vidmento — research.adobe.com/news/vidmento...; Adobe color
-preference patent — image-ppubs.uspto.gov/dirsearch-public/print/downloadPdf/9557829; VEGAS
Scripting API — help.magix-hub.com (MAGIX "VEGAS Pro Scripting API Summary" +
`vegasscriptfaq.html`), github.com/evankale/VegasScripts, github.com/haroldlinke/VEGASPython,
jetdv.com/2024/09/16/finding-all-transitions-in-the-project-in-vegas-pro.

**Auto-reframe (§3.5):** Google AutoFlip — opensource.googleblog.com/2020/02/autoflip...,
research.google/blog/autoflip-an-open-source-framework-for-intelligent-video-reframing,
mediapipe.readthedocs.io/en/latest/solutions/autoflip.html; gaze-based retargeting —
arxiv.org/pdf/1807.03125; seam-carving video retargeting — research.google.com/pubs/
archive/36246.pdf.

**B-roll retrieval (§3.4):** X-CLIP — arxiv.org/pdf/2207.07285; CLIP4Clip —
semanticscholar.org/paper/CLIP4Clip...; Cap4Video — arxiv.org/pdf/2301.00184; dual deep
encoding — arxiv.org/pdf/2009.05381; VideoCLIP-XL — arxiv.org/pdf/2410.00741.

**Multi-track playback engine (§3.9):** MLT framework architecture —
mltframework.org/docs/framework/; Shotcut forum (Dan Dennedy) —
forum.shotcut.org/t/why-stutter/15132; Kdenlive maintainer performance tracking —
invent.kde.org/multimedia/kdenlive/-/issues/439; Shotcut CPU-only decode —
binarytides.com/reduce-timeline-preview-lag-in-shotcut; ffmpeg seek/decode behavior —
ffmpeg.org/ffmpeg.html, ffmpeg.org/pipermail/libav-user/2014-October/007590.html; ffmpeg
filter_complex concat slowdown — trac.ffmpeg.org/ticket/8533; mpv stream-oriented design —
github.com/mpv-player/mpv/blob/master/DOCS/man/vf.rst; mpv single-output-per-instance +
`--lavfi-complex`/`--external-files` limits — mpv `DOCS/man/options.rst`, GitHub issues
#8340/#3854/#10130/#4439/#10454, `overlay-add` in `DOCS/man/input.rst`; Adobe Mercury
Playback Engine — helpx.adobe.com/premiere/.../mercury-playback-engine-gpu-accelerated-in
-premiere.html, blog.adobe.com/en/publish/2011/02/20/red-yellow-and-green-render-bars;
DaVinci Resolve Proxy Media/Smart Cache — steakunderwater.com/VFXPedia (Resolve 18.6
manual mirror), blackmagicdesign.com/products/davinciresolve/fusion; VEGAS Dynamic RAM
Preview — help.magix-hub.com/video/vegas/22/en/content/topics/5-preview/
using_dynamic_ram_previews.htm.
