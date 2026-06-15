# SPEC-BECKY-VOX.md — multi-take vocal alignment (timing + pitch), the deterministic VocALign/Melodyne replacement

> **STATUS: design only — NOT built (2026-06-15).** Deep-research pass on matching
> N recorded vocal takes to a guide in **timing** (DTW time-warping of onsets) and
> **pitch** (note-segmented, formant-preserving correction), plus **comping** the
> best bits — the Synchro Arts VocALign / Revoice Pro / Melodyne / Cubase VariAudio
> job, done becky-style: deterministic, transparent, per-phrase human review, never
> a black-box autotune. This is the **align/comp recorded takes** layer; it sits
> beside `SPEC-BECKY-HUM.md` (pitch-detect a *hummed* melody) and `SPEC-BECKY-DAW-ENGINE.md`
> (playback), and surfaces as a **vocal mode** inside `SPEC-BECKY-CANVAS.md`. Awaits
> Jordan's go/no-go. **Citations inline; open decisions at the end.**

---

## 0. The brief, in Jordan's words

Jordan is a pro producer (15+ yrs). His exact framing: making multiple vocal takes
**similar in timing + pitch is CRITICAL and the MOST TIME-CONSUMING part of audio
engineering**. Many VST plugins try to solve it and "**none are adequate**." Cubase
ships a built-in Melodyne-style editor (**VariAudio**); becky needs this.

The job, precisely: a **guide / lead take** plus **N alternate takes** (doubles,
stacks, harmonies, punch-ins) that must be made to match the guide in:

1. **TIMING** — syllable/phoneme onsets line up so a double sounds like *one*
   thick voice, not two voices flamming a few milliseconds apart.
2. **PITCH** — intonation matches the guide (for doubles/stacks) or snaps to the
   intended scale/melody (for tuning), without the chipmunk/robot artifact.
3. **COMPING** — pick the best bit of each phrase across takes (or align-and-stack
   all of them for a deliberate double), with clean crossfades.

This is the one becky audio feature where the *competition is mature and still not
good enough*, so the spec is opinionated about **why** the incumbents fall short and
**what becky does differently** (the "none are adequate" fix is §5).

---

## 1. Why the incumbents fall short (the gap becky fills)

The mechanics are solved; the **trust model** is not. Every incumbent is either a
slider that warps the whole phrase opaquely, or a manual per-syllable editor that
*is* the time sink Jordan is complaining about.

- **VocALign (Synchro Arts)** time-warps the "Dub" onto the "Guide" by matching
  time-varying energy patterns and applying a warp path — fully automatic, one
  "Matching" tightness knob. Great when it works; when it mis-warps a syllable you
  get a smear and there is **no per-syllable accept/reject** — you re-run with a
  different tightness and hope. citeturn0search0turn0search6
- **Revoice Pro (APT)** adds manual pitch+time warping and tunable "how much
  timing/tuning to apply" — powerful but **manual**, i.e. exactly the per-syllable
  hand-tweaking that eats the day. citeturn0search3
- **Melodyne / VariAudio** are blob editors: superb control, but you are dragging
  blobs one at a time — the canonical "most time-consuming part." Pros pair Melodyne
  (pitch) **with** VocALign (timing) precisely because no single tool does both
  trustworthily. citeturn3search3
- **Auto-Align Post / phase aligners** fix *gross delay/phase* between mics, not
  syllable-level intonation+timing — a different, narrower job. citeturn4search0
- **The shared failure:** a **wider warp/search range → more artifacts**; experts
  warn to under-process and keep natural variation, but the tools give you a global
  knob, not localized confidence. citeturn4search1 So you either over-correct
  (lifeless, smeared) or babysit every syllable (slow). **Neither is "show me what
  you're confident about, flag the rest, I'll approve per phrase."**

becky's whole ethos *is* that missing middle: **corroborate then conclude, show the
work, the human reviews confident results — never a pile of maybes** (CLAUDE.md §2,
FORENSIC-OUTPUT-PHILOSOPHY). A vocal aligner built on that ethos is the differentiated
product. The DSP below is well-trodden; the **UX in §5 is the actual invention**.

---

## 2. Timing alignment — DTW warp of the alt onto the guide

### 2.1 The method (what VocALign does, made explicit and deterministic)

Both tools reduce to **Dynamic Time Warping**: convert guide and alt to frame-wise
feature sequences, compute the optimal monotonic alignment (the **warp path**)
between them, then time-stretch/-compress the alt so its features land on the
guide's. DTW computes an optimal alignment between two feature sequences and that
alignment *is* the synchronization result; the warp path is a monotonic sequence of
index pairs with the first/last frames pinned (boundary conditions). citeturn1search2turn1search1
VocALign's "energy pattern matching → warp path → time-warp processor" is this exact
pipeline with a proprietary feature + a tightness knob. citeturn0search0

### 2.2 becky's feature stack (why more than one feature)

A single feature mis-warps on the cases that matter (sibilants, breaths, held
vowels). becky aligns on a **fused, weighted feature frame** so corroboration is
built into the cost matrix — same "≥2 signals agree" principle as the rest of becky:

| Feature | Catches | Source |
|---------|---------|--------|
| **Onset/energy envelope** | consonant attacks, syllable starts (VocALign's signal) | librosa onset strength citeturn1search1 |
| **MFCC** | phoneme identity / spectral shape over time | librosa MFCC citeturn1search1 |
| **Chroma** | pitched-vowel content (keeps vowels from sliding onto the wrong note) | librosa chroma citeturn1search2 |

DTW runs on the **stacked, normalized** feature matrix (librosa `librosa.sequence.dtw`
supports custom cost, step sizes, and step weights). We constrain it with a
**Sakoe-Chiba band** (max local warp) so a syllable can't be stretched to absurdity —
this is becky's deterministic equivalent of VocALign's tightness, but **bounded and
explainable** rather than a blind knob. citeturn1search1turn1search3

### 2.3 Applying the warp (and *showing* it)

- The warp path is a piecewise-linear **time map** `t_alt → t_guide`. We apply it
  with **formant-preserving granular/elastic time-stretch** (Rubber Band offline,
  §4) so vowels don't chipmunk when locally sped up/slowed. citeturn2search0
- **Transparency (the becky differentiator):** the warp map is a first-class output
  — a JSON `warpMap[]` of `{guideOnset, altOnset, shiftMs, localStretch, confidence}`
  per detected syllable, and the canvas draws it as **tie-lines between guide and alt
  syllables** (green = high-confidence, amber = flagged). The user sees *exactly*
  what moved and by how much, instead of a phrase that silently smeared. This is the
  warp-path visualization DTW naturally produces, promoted to UX. citeturn1search0

### 2.4 Determinism

Fixed hop/window, fixed feature weights, fixed DTW step weights and band width →
**same guide+alt → same warp map → same aligned WAV**. No randomness anywhere in the
timing path (CLAUDE.md §2 determinism invariant).

---

## 3. Pitch analysis + correction — note-segmented, formant-preserving

### 3.1 Detect → segment into notes (the Melodyne "blobs", deterministically)

1. **Monophonic F0 track.** Default **pYIN** (probabilistic YIN): a DSP F0 tracker
   with a built-in HMM **note** post-processor (Viterbi over the pitch track) — i.e.
   it already gives note onsets/offsets, not just a raw contour. Deterministic, no
   model weights. It is the natural becky default because it is offline, light, and
   reproducible. citeturn5search8
2. **Optional high-accuracy F0.** **CREPE** (CNN pitch tracker) beats pYIN/SWIPE on
   raw F0 accuracy; **CREPE-Notes** post-processes CREPE into discrete notes
   (state-of-the-art note segmentation). Offered as an opt-in "high accuracy" backend
   (needs a model weight on the local box) — same two-tier pattern as the rest of
   becky (deterministic DSP floor + optional model ceiling). citeturn5search0turn5search1
3. **Cross-check (corroborate-then-conclude).** **Parselmouth/Praat** (`to_pitch_ac`,
   bit-reproducible vs the Praat GUI) gives a second independent F0 estimate; where
   pYIN and Praat agree we mark the note **confident**, where they diverge (octave
   error, breathy onset, voiced/unvoiced edges) we **flag** it rather than silently
   tune it. citeturn6search2 Two independent signals agreeing → conclude; lone
   signal → "uncertain", exactly per the becky philosophy.

### 3.2 The correction targets

- **Stack/double mode:** target = the **guide's** detected note per syllable → the
  alt's intonation is matched to the lead (this is what makes a double glue).
- **Tune mode:** target = nearest note in a **declared scale/key** (or a supplied
  melody/MIDI) → classic tuning, but per-note with confidence, not a global retune.
- **Either way**, becky only proposes a move where a note was confidently detected;
  ambiguous notes carry a *suggestion*, not an automatic edit (§5).

### 3.3 Formant-preserving shift (avoiding the chipmunk/artifact failure)

The artifact problem is real and method-dependent — this is where "none are adequate"
bites — so becky picks the method per case and **says which it used**:

| Method | Strength | Weakness | Verdict |
|--------|----------|----------|---------|
| **Rubber Band** (phase vocoder, **`OptionFormantPreserved`** explicitly tuned for *vocal* formants) | high quality, handles time+pitch together, battle-tested C++ | **GPL** (or paid commercial license) | **Primary** for the offline render — but the GPL boundary is load-bearing (§4.1) citeturn2search0 |
| **WORLD** (pyworld: F0/spectral-envelope/aperiodicity resynthesis) | clean separation of pitch from spectral envelope → strong formant control; permissive license | formant-shift behavior can add audible artifacts; F0-estimate errors at voiced/unvoiced transitions distort | **Default permissive engine** + the cross-check guards the V/UV edges it's weak on citeturn7search0turn7search1 |
| **TD-PSOLA** | time-domain, **preserves formants by construction** (shifts pitch-marks, leaves spectral envelope), tiny + permissive | quality degrades at large shifts / low pitch | good for **small** corrections (the common case for doubles) citeturn7search1 |

**Decision:** offline render uses **WORLD (pyworld) as the permissive default**,
**TD-PSOLA for small shifts**, and **Rubber Band as the quality option behind the
GPL/license boundary** (§4.1). The cross-check (§3.1.3) specifically backstops WORLD's
known V/UV-transition weakness. Whichever ran is recorded per note in the output so
the result is auditable, not opaque.

---

## 4. Open libraries + the Python-helper contract

### 4.1 Library survey (cited) and the license map

| Library | Role | License | Notes |
|---------|------|---------|-------|
| **librosa** | features (onset/MFCC/chroma) + `sequence.dtw` warp path | ISC (permissive) | the timing engine citeturn1search1 |
| **aubio** | fast onset/pitch (alt detector, real-time-ish) | GPL | use as *analysis* cross-check, keep out of redistributed binary if GPL matters citeturn6search0 |
| **pyworld (WORLD)** | formant-preserving resynth (default) | permissive (MIT-style) | F0/SP/AP decompose+resynthesize citeturn7search0 |
| **Rubber Band** | best-quality time-stretch + formant-preserved pitch | **GPL / commercial** | quality option; license-gated like ASIO in DAW-ENGINE §1.2 citeturn2search0 |
| **TD-PSOLA** | small-shift formant-safe pitch | permissive (script) | the common-case engine citeturn7search1 |
| **parselmouth/Praat** | second F0 estimate (`to_pitch_ac`) for the cross-check | GPL (Praat) | analysis-only; reproducible vs Praat GUI citeturn6search2 |
| **pYIN** | default note-segmented F0 | permissive (in librosa) | offline, deterministic, gives notes citeturn5search8 |
| **CREPE / CREPE-Notes** | opt-in high-accuracy F0 + note segmentation | MIT | needs a model weight (local box only) citeturn5search0turn5search1 |

> **License posture (matches DAW-ENGINE §5).** Permissive stack
> (librosa + pyworld + TD-PSOLA + pYIN + CREPE) is the **default, redistributable**
> path. GPL pieces (Rubber Band redistributed; aubio/Praat) are **opt-in, behind a
> build flag, and only invoked by the local helper** — never linked into a shipped
> permissive binary. Same conscious build-vs-license line the DAW engine draws for
> ASIO/JUCE.

### 4.2 The Python helper contract (cloud writes the stub; local plugs in models)

`becky-go/internal/pyhelpers/vox_align.py` — **WAV in → JSON + WAV out**, deterministic:

```jsonc
// INPUT (argv/stdin JSON)
{
  "guideWav": "C:\\...\\lead.wav",
  "altWav":   "C:\\...\\double_take3.wav",
  "mode":     "stack",        // "stack" | "tune"
  "scale":    {"key":"A","mode":"minor"},   // tune mode only
  "timing":   {"bandMs": 80, "weights": {"onset":0.5,"mfcc":0.3,"chroma":0.2}},
  "pitch":    {"engine":"world", "f0":"pyin", "crossCheck":"praat", "maxShiftSemis": 2.0},
  "seed":     0
}
// OUTPUT (stdout JSON) — analysis is separate from the rendered audio
{
  "warpMap": [ {"guideOnsetMs":1234,"altOnsetMs":1300,"shiftMs":-66,"localStretch":1.04,"confidence":0.91,"syllable":7} ],
  "notes":   [ {"startMs":1234,"endMs":1480,"detectedHz":219.8,"targetHz":220.0,"moveCents":-1.6,
                "engineUsed":"world","confidence":0.88,"flagged":false} ],
  "phrases": [ {"startMs":1200,"endMs":3400,"timingTightness":0.92,"pitchStability":0.84,"confidence":0.88} ],
  "alignedWav": "C:\\...\\double_take3.aligned.wav",   // timing+pitch applied
  "alignedMid": "C:\\...\\double_take3.aligned.mid",   // detected notes as MIDI (for canvas/compose)
  "degraded": null
}
```

Contract rules (becky house style): **analysis (warpMap/notes/phrases/confidence)
is emitted separately from the rendered audio**, so a human (or canvas) can accept
per phrase *before* anything is baked; the renderer re-runs with an `accept[]` mask.
**Degrade-never-crash:** no F0 found (whispered/over-driven take), guide+alt lengths
absurdly mismatched, missing engine → typed `degraded` + a partial result (e.g.
timing-only, pitch skipped), never a panic (CLAUDE.md §2).

---

## 5. The deterministic, transparent UX — the "none are adequate" fix

This is the differentiator and the reason the spec exists. becky does **not** ship a
global tightness/retune knob and pray. It ships **per-syllable evidence + per-phrase
approval**:

1. **Analyze, don't apply.** The first pass produces the `warpMap` + `notes` +
   `phrases` with a **confidence per syllable and per phrase** — and renders *nothing*
   permanent yet.
2. **Show the work.** Canvas vocal mode draws guide vs alt with: tie-lines for the
   timing warp (green/amber by confidence), Melodyne-style note blobs with the
   proposed pitch move as a ghost, and a per-phrase confidence chip.
3. **Conclude where confident; flag where not.** A syllable where onset+MFCC+chroma
   agree **and** pYIN+Praat agree on the note → stated plainly as a confident
   correction (becky just does it, shown in green). A syllable where features
   disagree or F0 is ambiguous → **flagged amber with a suggested move**, awaiting a
   click — *not* silently warped. This is the corroborate-then-conclude rule applied
   to vocals: no flood of maybes, no opaque smear. (Directly answers the incumbents'
   "wider range → artifacts, no localized confidence" failure in §1.) citeturn4search1
4. **Accept/reject per phrase, then bake.** The user approves a phrase (or edits one
   amber syllable); becky re-renders only the accepted edits via the helper's
   `accept[]` mask. Under-processing by default (keep natural variation) is the
   recommended-by-pros behavior, made the *default* rather than a discipline the user
   must remember. citeturn4search1
5. **Always reversible / auditable.** Original take untouched; aligned WAV is a new
   stem; the JSON is the record of every move and which engine made it.

---

## 6. Comping — pick the best bits across takes

Two modes, both built on the per-phrase metrics §4 already computes:

- **Comp mode (best-take-per-phrase).** For each phrase, score every take by a
  chosen metric — default a weighted blend of **pitch stability** (low cents
  variance within held notes), **timing tightness** (small DTW residual vs guide),
  and **level/SNR** — then choose the winner and assemble the comp with
  **equal-power crossfades at zero-crossings in the gaps between phrases** (never
  across a diction/sustain, the standard comping rule). The score and the runner-up
  are shown, so a producer can override one phrase without re-auditioning all takes —
  the slow part of manual comping. citeturn8search0turn8search1
- **Stack/double mode (align-all).** Don't choose — align *every* take to the guide
  (timing + optional light pitch-match) and keep them as stacked stems for a
  deliberate thick double. The "make N takes sound like one voice" case.

Default metric weights are exposed and deterministic; same takes + same weights →
same comp. The comp decision list (`{phrase, chosenTake, score, runnerUp}`) is an
output, so the comp is a **declared, repeatable artifact**, not a mouse-history.

---

## 7. Integration — the `becky-vox` CLI + canvas vocal mode

### 7.1 CLI (one tool, JSON in → JSON out, per the single-tool principle)

```
becky-vox align   --guide lead.wav --alt double.wav --mode stack --out double.aligned.wav --json out.json
becky-vox tune    --in lead.wav --key Aminor --max-shift 2 --out lead.tuned.wav --json out.json
becky-vox comp    --guide lead.wav --takes t1.wav,t2.wav,t3.wav --metric balanced --out comp.wav --json comp.json
becky-vox analyze --guide lead.wav --alt double.wav --json out.json     # analysis only, renders nothing
```

`cmd/vox/` (Go: CLI, JSON schema, validation, accept-mask plumbing, comp decision
logic, determinism) → `internal/pyhelpers/vox_align.py` (the DSP/ML). Mirrors every
other becky tool: Go orchestrates + validates + stays CI-green; the heavy DSP is a
thin embedded-Python helper calling local libraries (CLAUDE.md §1).

### 7.2 Canvas vocal mode (`cmd/canvas/modes/vox/`)

Load guide + takes → **align timing** (tie-line view) → **match pitch** (blob view
with confidence) → **comp** (best-take ribbon) → **export aligned stems** (or hand
them to `becky-compose`'s vocal track / the DAW engine for playback). It is a
**Mode** in the one canvas window (shares GPU context + embedded model), exactly like
the DAW editor mode — not a separate binary (CANVAS §6, DAW-ENGINE §7). The detected
notes export as MIDI so a corrected lead can drive a doubler or feed compose.

---

## 8. Cloud-vs-local build split

| Cloud / web agent (here) | Local agent (Jordan's Win10 + GPU) |
|--------------------------|-------------------------------------|
| `cmd/vox/` Go CLI, JSON schema, arg/input validation | run the real DSP on real takes |
| **Comp decision logic** + per-phrase metric blend (pure Go, deterministic) | tune metric weights / band width on real vocals |
| The `vox_align.py` **stub** with the exact I/O contract (§4.2) | wire pyin/librosa/pyworld/TD-PSOLA (+ opt-in CREPE/Rubber Band) |
| **Unit tests** for all deterministic Go logic (warp-map parse, accept-mask, comp scoring, crossfade math, degrade typing) | accuracy/artifact A/B vs VocALign/Melodyne on his catalog |
| Determinism tests (same JSON in → same decision out) | `build-all-tools.bat`; ffmpeg WAV extract on the box |
| Push branch + draft PR | confirm "double sounds like one voice", report degrades |

Every Python boundary the cloud agent can't run is left as a documented stub with the
explicit contract in §4.2, so the local agent only plugs in the model/library calls
(CLAUDE.md §4 baton).

---

## 9. Top risks

- **Octave / V-UV F0 errors** silently mis-tuning a note. Mitigation: the
  pYIN↔Praat cross-check **flags** divergence instead of tuning it; WORLD's known
  V/UV weakness is exactly what the cross-check guards. citeturn7search1
- **Over-warp smear** (the incumbent failure). Mitigation: Sakoe-Chiba band caps
  local stretch; under-process by default; per-syllable confidence so a bad warp is
  *visible and rejectable*, not baked. citeturn1search3turn4search1
- **Chipmunk/formant artifacts** on pitch shift. Mitigation: formant-preserving
  engines only (WORLD/TD-PSOLA/Rubber-Band-`FormantPreserved`); small-shift TD-PSOLA
  for the common case; engine-used recorded per note. citeturn2search0turn7search1
- **Rubber Band GPL** contaminating a permissive binary. Mitigation: permissive
  default stack (WORLD/PSOLA); Rubber Band opt-in behind a build flag, local-helper
  only — same line DAW-ENGINE draws for ASIO. citeturn2search0
- **Determinism vs ML F0 (CREPE).** Mitigation: pYIN DSP default is the deterministic
  floor; CREPE is a labeled opt-in ceiling (pin model weight + seed), consistent with
  becky's two-tier pattern. citeturn5search0
- **Comping picks a "tight but lifeless" take.** Mitigation: show score + runner-up;
  one-click per-phrase override; the metric is a default, not a verdict. citeturn8search0

---

## 10. Open decisions for Jordan

1. **Pitch engine default:** ship **WORLD (permissive)** as default with **TD-PSOLA**
   for small shifts, and keep **Rubber Band** as an opt-in quality build — or do you
   want Rubber Band quality as the default and accept the GPL/commercial-license
   boundary up front?
2. **F0 backend:** **pYIN** deterministic default with **CREPE** as opt-in
   high-accuracy (needs a model weight on your box) — right call, or go CREPE-first
   for accuracy and treat pYIN as the fallback?
3. **Default correction strength:** under-process (keep natural variation, pros'
   recommendation) as the default — agreed? Or do you want fully-glued doubles by
   default with an opt-out?
4. **Comp metric weighting:** default "balanced" = pitch-stability + timing-tightness
   + level. What weighting matches your ear — tighter timing, or more forgiving on
   pitch?
5. **Scope of v1:** **stack/double alignment + tuning + comp** in the first cut — or
   is **align-double-to-lead** alone the must-have and tuning/comp can follow?
6. **Surface:** CLI-first (`becky-vox`) then the canvas vocal mode, or build the
   canvas mode immediately since this is an inherently visual, per-phrase-approval
   task?
7. **Reference targets:** name 2–3 real takes from your catalog (a tight double, a
   sloppy punch-in, a harmony) to A/B becky against VocALign + Melodyne on the local
   box.

---

### Sources

- VocALign / Revoice Pro (energy-pattern → warp path → time-warp; APT manual pitch+time): https://www.synchroarts.com/manuals/VocAlignProjectV5/Manual/HTML/what-is-vocalign-ultra.html , https://www.production-expert.com/production-expert-1/organic-vocal-alignment-with-revoice-pro , https://gearspace.com/board/music-computers/991329-vocalign-revoice-pro-3-a.html
- DTW for audio sync (warp path, boundary conditions, Sakoe-Chiba): https://en.wikipedia.org/wiki/Dynamic_time_warping , https://librosa.org/doc/main/auto_examples/plot_music_sync.html , https://meinardmueller.github.io/synctoolbox/build/html/dtw.html , https://www.mdpi.com/2076-3417/14/4/1459
- librosa features + `sequence.dtw`: https://librosa.org/doc/main/auto_examples/plot_music_sync.html , https://ursinus-cs371-s2021.github.io/CoursePage/Assignments/HW3_DTW_AudioAlignment/
- pYIN note tracking: https://code.soundsoftware.ac.uk/projects/pyin
- CREPE + CREPE-Notes (note segmentation, beats pYIN/SWIPE): https://arxiv.org/abs/1802.06182 , https://github.com/marl/crepe , https://arxiv.org/pdf/2311.08884
- WORLD/pyworld (formant control + V/UV weakness): https://github.com/JeremyCCHsu/Python-Wrapper-for-World-Vocoder , https://arxiv.org/pdf/2204.05753
- TD-PSOLA (formant-preserving by construction; degrades at large shifts): https://github.com/sannawag/TD-PSOLA , https://www.academia.edu/6328692/Voice_Conversion_Using_Pitch_Shifting_Algorithm_by_Time_Stretching_with_PSOLA_and_ReSampling
- Rubber Band (formant-preserved phase vocoder; GPL/commercial): https://breakfastquay.com/rubberband/ , https://github.com/breakfastquay/rubberband
- parselmouth/Praat (`to_pitch_ac`, reproducible) + aubio: https://parselmouth.readthedocs.io/en/stable/examples/pitch_manipulation.html , https://github.com/exirmee/pitchdetector
- Auto-Align Post (phase/delay, not intonation): https://www.soundradix.com/products/auto-align-post/
- Comping (best-take, crossfade-in-gaps, wider-range→artifacts/under-process): https://splice.com/blog/vocal-comping-tips/ , https://www.izotope.com/en/learn/8-tips-for-perfect-vocal-comping.html , https://tracksnapfx.com/blog/posts/how-to-align-vocals
