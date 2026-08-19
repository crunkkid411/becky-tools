# What the 21 reference projects do that becky does not — build it, or say why not

`shorts-user-feedback.md`: *"If there's a step that we are NOT implementing, either build it, or
declare why we don't need it."* This is that decision list. The per-repo notes are
`research/repo-*.md` (22 files, read by free models); the judgement here is not theirs.

Ordered by what would most change the output Jordan actually watches.

---

## BUILD — 1. Snap every cut edge to the nearest silence trough

**Who does it:** `clippyme` — *"cuts never clip a word's attack/release because edges are nudged to
the nearest `ffmpeg silencedetect` trough (only toward quiet)"*. `video-editor-app` — silence
detection anchored to **word boundaries**, not VAD on raw audio, plus *"padding shrinkage
(0.2s pad, 0.1s min) — prevents over-trimming tight breaths"*. `funasr` — VAD-first segmentation,
cuts on acoustic silence **before any transcript exists**.

**What becky does:** `internal/moment` anchors windows on **transcript cue boundaries**. A cue
boundary is where the ASR decided a line ended — it is not where the sound actually stops. Parakeet
quantises to 0.08s and 49% of its words carry `end == start`, so a cue edge routinely lands on a
consonant.

**Why it matters:** this is Jordan's rule 3. *"becky-cut is paced at my approximate preferred speed
- it gives us the 80% of cuts close to where I would have chosen. But the remaining 20% need deeper
analysis; this is when silence and pauses are used."* An edge that clips a word's attack is audible
immediately and is the difference between an edit and a chop. **Only toward quiet** is the
load-bearing detail — nudging into speech makes it worse.

**Where:** `internal/moment` boundary selection, fed by the audio envelope `internal/audiosig`
already computes. `cmd/clip/autocut.go` already shells to `becky-cut --dry-run` for exactly this
class of signal.

---

## BUILD — 2. Reset the camera smoother at every shot boundary

**Who does it:** `post-fast-main` — *"camera path is optimized **per shot** (PySceneDetect
boundaries), not globally, avoiding jump cuts across scene changes"*. `clip-forge` — a
`FocusKeyframe.cut` flag *"forces hard crop switch at detected shot boundaries; within-shot drift
uses eased pan"*.

**What becky does:** `smooth_zero_phase` filters the crop path **forward and backward over the whole
clip**. Zero lag, which is right, and completely wrong across a hard cut: the filter smears the
framing of shot A into shot B, so instead of an instant re-frame the camera *drifts* into position
over the first half-second of every cut.

**Why it matters, specifically for his footage:** his own example is an already-edited YouTube video
— *"it contains jumpcuts, zooms, and intelligent but chaotic framing"*. And the moment the jumpcut
work now landing in `becky-short` starts dropping dead air, **every short becomes multi-shot**. This
bug does not exist yet on a continuous take and will appear the day jumpcuts ship. It will render
fine and exit 0.

**Where:** `internal/pyhelpers/crop_path.py` — segment the path at shot boundaries and run the
zero-phase filter per segment. Boundaries come free from the jumpcut keep-spans; a real scene-cut
detector is only needed for already-edited sources.

---

## BUILD — 3. Loudness-normalise to −14 LUFS

**Who does it:** `clippyme` — EBU R128 to −14 LUFS via `ffmpeg loudnorm` on **every** output.

**What becky does:** nothing. `crop.RenderArgs` re-encodes audio to AAC 160k at whatever level the
source had.

**Why it matters:** every platform normalises to roughly −14 LUFS on playback. A short that lands
quieter is turned *up* by the platform along with its noise floor; one that lands hotter is turned
down, and it sounds flat next to everything around it. This is a one-line filter and it is the
cheapest quality win on this list.

**Where:** `internal/crop/crop.go`'s `RenderArgs`, `-af loudnorm=I=-14:TP=-1.5:LRA=11`.

---

## BUILD — 4. Make the judge name the hook line, and tell it what kind of video this is

**Who does it:** `AI-Youtube-Shorts-Generator` — the hook sentence is *"an explicit LLM output
field — not derived post-hoc; the model must name the exact opening line"*, and it classifies
**content type + density** first so the prompt context changes (*"podcast | high density"* vs
*"vlog | low density"*).

**What becky does:** `internal/moment/judge.go` asks for a score and a reason. The in-point comes
purely from the structural pass.

**Why it matters:** two signals are currently answering different questions and neither owns the
first second. Making the model name the opening line gives a second, independent opinion on the
in-point that can be **corroborated against** the structural boundary — which is becky's own
doctrine, applied where it currently is not.

**Where:** `internal/moment/judge.go` prompt + `Judgement` struct. Cheap; no new dependency.

---

## CORRECTED, NOT A BUG — 5. The 68-second ceiling

**Who does it:** `AI-short-creator` applies a hard 58-second cap after cropping.

**What becky does:** `MaxDuration` 60 + `ExtendBudget` 8 = up to **68 seconds**.

**I first wrote this up as a shipping bug and it is not one.** YouTube Shorts has accepted up to
3 minutes since late 2024, Reels 90 seconds, TikTok far longer. 58 is that project's constant from
an older limit, and copying it would be adopting someone else's number with no evidence — the exact
mistake `CLAUDE.md` records from the TTS pick.

**What is left of the finding:** length still costs retention regardless of what the platform
allows, and 68s is a long short. That is a taste call on his content, not a correctness fix, so it
stays a knob and stays where it is until he says otherwise. Recorded so nobody "fixes" it later.

---

## BUILD — 6. Zoom is an editorial device and becky has none

**Who does it:** `clip-forge` — three zoom layers: *"scene-aware punch-ins on high-energy lines,
jump zooms covering cuts, slow creep on static stretches; all remapped when tighten-cuts shortens
the timeline."* `clippyme` — Ken Burns 1.0→1.05× on every clip. `agent-opus` — dynamic zoom
triggered on the hook moment.

**What becky does:** one crop rectangle per frame, scale only. No push-in, ever.

**Why it matters:** *"jump zooms covering cuts"* is a real editor's technique — a scale change at a
cut hides the jump. His own footage has zooms. `internal/audiosig` **already measures** the
loudness spikes and pitch rises that mark a high-energy line, and it is currently only used for
ranking.

**Do it in this order, not all at once:** slow creep on static stretches (safest), then jump zoom at
cuts, then punch-in on an audio spike. This is a taste feature — build one, show him, stop.

---

## BUILD — 7. Frame on the SUBJECT, not on the nearest face

**Who does it:** `post-fast-main` — *"multi-object saliency fusion: faces, bodies, text, logos, and
generic objects are weighted and combined per frame before tracking."*

**What becky does:** every framing signal is a person detector — MediaPipe Pose, InsightFace,
LR-ASD. A frame with no visible face is a MISS and the render honestly refuses.

**Why it matters:** the rubber-snake prank. *"the camera changes to POV style, where NO FACES are
visible - at this point the clip itself is obviously meant to be the focal point."* Refusing is
honest but useless; the shot is not unframeable, it is unframeable *by a face detector*.

**Where:** this is what `research/reka-edge-vs-gemma4.md` recommends Reka Edge for —
`Detect: rubber snake` returns a box, and `internal/crop` already consumes boxes. Do that before
building a saliency stack of our own.

---

## ALREADY BUILT — worth recording that the field agrees

- **Overlap dedupe.** `AI-Youtube-Shorts-Generator` suppresses at *">50% of the **candidate's**
  duration — not a fixed time window, but proportional to each highlight's length"*. That is
  independently the same rule shipped tonight in `internal/moment/suppress.go` (overlap as a
  fraction of the **shorter** window, threshold 0.5). Two projects reaching the same threshold by
  different routes is the best evidence available that 0.5 is right.
- **Active-speaker-locked crop.** `clip-forge` (LR-ASD + UltraFace), `post-fast-main`
  (LoCoNet+TalkNCE), `opusclip-clone` (lip movement + VAD) all lock the crop to the *speaking*
  face rather than the largest one. becky picked LR-ASD independently and it is being wired now.
  **Note:** `clippyme` uses **mouth-aspect-ratio variance** instead — becky rejected MAR on the
  grounds that it never consults the audio, so chewing, laughing and an animated listener all read
  as speech. Seeing a shipped project use it does not change that; it confirms the trap is common.
- **Word-level forced alignment.** `video-editor-app` (WhisperX), `funclip` (Paraformer
  character-level), `vcut`, `clipify`. becky has had this since Parakeet.
- **Frame-count arithmetic, not float seconds.** `pavo-engine-py`: *"all times are frame counts
  derived from output.fps; no floating-point drift."* becky fixed exactly this bug
  (+1.27s over 88 cuts) in `internal/reel`.

---

## SKIP — and why

| Not building | Why |
|---|---|
| **Per-word animated caption highlight** (`clipify` opus-style ASS, `agent-opus` yellow highlight, `AI-short-creator` uppercase) | Jordan's caption look is the **cli-cut preset**, and `CLAUDE.md` is explicit that his defaults are the reference and are not to be deviated from unasked. This is a taste change, not a capability gap. **Surface it as a question, never a default.** |
| **Selenium/yt-dlp Shorts bots, view-count scraping, Pushover notifications** (`clipsai2`) | Online, brittle, and outside the brief. becky is offline+deterministic. |
| **AssemblyAI / cloud transcription** (`supoclip`) | Paid API. Parakeet is local and already better for this. |
| **TwelveLabs Pegasus** (`funclip`) | Paid cloud video reasoning. Gemma-4 + Reka Edge cover it locally. |
| **Emoji auto-annotation of captions** (`supoclip`) | Not his style, and nothing in his profile asks for it. |
| **Multi-camera shot lists / style-reference upload** (`clipbot`) | He shoots single-camera. Real feature, wrong user. |
| **Speaker diarisation as a clipping target** (`funclip` CAM++, `funasr`) | Genuinely good, and becky already has diarisation. Not the bottleneck for solo talking-head content — revisit if he starts posting interviews. |
| **TensorRT/DirectML backend ladder, OFX plugin, signed model manifests** (`CorridorKey-Runtime`) | Excellent engineering for a *product*. We are one machine with one GPU; this is infrastructure we would maintain and never use. |
| **GLSL/Fabric.js layer system, transitions** (`editly`, `pavo-engine-py`) | An authoring DSL for hand-built videos. becky's job is to *decide* the edit, not to offer a layer stack. Adding one is how this becomes another half-built NLE. |
| **Motion-energy diarisation from ffmpeg `signalstats` on hand-picked ROIs** (`clipify`) | Clever and cheap, but needs a human to pick the ROIs per video, and we have a real ASD model. |
| **Comfort-mode static crop** (`clippyme`) | It exists to hide bad tracking. Ours does not lag — that was fixed. Adding it would be treating a symptom we no longer have. |
| **Writing-DNA profile distillation** (`video-copywriting-style-learner`, `persona-alignment-rewrite`) | Its evidence-grading and confidence lifecycle are good ideas, but `hair-jordan-personality-profile.md` already exists and was built from a video-understanding dataset. Do not rebuild it. |

---

## The one thing NO project on the list does

Not one of the 21 **re-watches its own output**. Every pipeline is strictly feed-forward:
transcript → pick → crop → render → done. `clip-forge` remaps zooms when the timeline shortens and
`AI-short-creator` patches the composition duration by reading the rendered file — that is
bookkeeping, not review.

Jordan's rule 5: *"Human video editors re-watch their own work MANY TIMES before ANYTHING gets
rendered off the timeline, and this system MUST as well."*

So the review pass is not a step we can copy from anyone — it has to be built, and it is the thing
that would actually separate becky from the tools that disappointed him. It is also where every
defect in this pipeline has been found so far: **by looking at rendered frames**, never by reading
code. The mechanism already exists in becky (`becky-validate` watches a window with Gemma-4); it has
simply never been pointed at becky's own output.

**Proposed shape, cheapest first:** render → sample the rendered short back at its true frame rate →
ask the VL three questions it can answer from frames alone (is the subject framed, does the caption
match what is being said, does the clip end on a completed thought) → fail the clip and say which
question failed. That is a `becky-short --review` pass, not a new tool.
