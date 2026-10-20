# BUILD-PROMPTS.md — one build prompt per missing component

Hand ONE block to ONE medium-sized coding agent. Each block is self-contained:
context it needs, exact deliverable, acceptance checks. Do not summarize them;
do not let the agent "improve" the spec. Numbering matches the worktree
(`becky-worktree-plan.md`) and step numbers match `WORKFLOW-DIAGRAMS.md` /
`becky-livestream-worktree.html`.

**Repo ground rules (paste into every prompt):** Windows 10/11 + Vegas Pro 18;
Go code lives in `becky-go/` (cmd/livestream = pipeline modules), C# scripts in
`vegas/*.cs` run inside Vegas via script command lines (`-exec <Verb>` args as
in BeckyFX.cs); existing tools: becky-cut (auto-editor wrapper, `--dry-run`
outputs keep-spans JSON), becky-transcribe (Parakeet+WhisperX, JSON words with
times), becky-local (llama.cpp: qwen3.5-4b, gemma-4-e4b-it-qai, gemma-4-12b-it,
vision-capable), becky-systemone (hosted System One API, multiple-choice style,
$5/month cap enforced in-tool). NEVER modify source footage. Everything written
goes under `<video folder>\becky-edit\`. All new steps must be cache-aware
(re-run reuses artifacts) and must update RUN.json (prompt #10's schema). The
repo already has build rules/checks — don't touch those. Jordan is a video
editor, not a developer: every new step gets a folder under `worktree/` with
README.md + JORDAN.md stub, and any LLM prompt as an external .md file.

---

## PROMPT 0 — The Rulebook + Steps-and-Engines table (docs only, do first)

> Create two files at the root of this repo.
>
> **1. `RULEBOOK.md`** — the single entry document for any human or AI agent
> working on becky-tools. Structure: purpose (repurpose Jordan's recorded
> footage into edited VEGAS projects the way he would edit it by hand); hard
> rules stated once each, each pointing at its owning doc: (a) footage is
> read-only, all writes go to `becky-edit\`; (b) never overwrite a project the
> user saved by hand; (c) audio from OBS-recorded videos arrives fully treated
> (pro mic + live noise removal/compressor/EQ/limiter) — loudness passes must
> detect "treated" and skip, because normalization would DEGRADE it; when the
> source is untreated, polish to −17 LUFS speech into a −6 dBFS limiter;
> (d) color grading applies ONLY to the camera/person track, never to graphics
> layers, b-roll, screen recordings or captions (those ride on tracks above,
> ungraded) — grading is applied last in processing order deliberately: it
> never changes edit decisions and saves compute during review; (e) cut points
> come from becky-cut (auto-editor, margin 0.04 s lead / 0.25 s trail, adaptive
> threshold) — captions snap to becky-cut boundaries because they are frame
> accurate and never clip words; (f) the LLM may REVERSE a becky-cut decision
> when context justifies it (example: "Cheers!" + glass raise + sip = silence,
> but the gesture belongs in the edit — put it back); the model can only ever
> PUT BACK, never add cuts beyond its named exceptions; (g) everything that can
> be deterministic MUST be deterministic; models answer narrow questions, code
> enforces answers; (h) local-first: Qwen3.5-4B + Gemma run free on this PC,
> System One is hosted and capped at $5/month — prefer local, ask before paid;
> (i) every step reads its prompts/settings from `worktree/<step>/` if present,
> falling back to embedded defaults. Then index: WORKFLOW-DIAGRAMS.md (the
> pipeline), becky-worktree-plan.md (folder map), STEPS-AND-ENGINES.md below,
> habits.md and ARRANGEMENT-RULES.md (Jordan's taste).
>
> **2. `STEPS-AND-ENGINES.md`** — a table with one row per step id (S1, S1b,
> S2…S11, V1–V9, F1–F9, 4b, D-QWEN/D-STACK/D-S1/D-VIDEO) and columns: what it
> does (one line), engine ∈ {deterministic code, local model, paid model,
> human}, input artifact → output artifact, cache key, owning code file(s),
> owning worktree folder. Derive the rows from the actual source in
> `becky-go/cmd/livestream/` — verify against code, do not invent. Mark the
> five rows that don't exist yet (F02 audio-polish, F06 QA-converge loop, F05
> SFX/music, F09 export, S13 run-file) as PLANNED with their target build-prompt
> number from BUILD-PROMPTS.md.
>
> Acceptance: both files parse as markdown, every step id in
> WORKFLOW-DIAGRAMS.md appears exactly once in the table, and every table row's
> "owning code file" exists on disk (or is marked PLANNED).

---

## PROMPT 1 — Externalize every LLM prompt to disk (enables all later tuning)

> In `becky-go/cmd/livestream/`, the model instructions are embedded Go string
> constants across select.go (qwen lead/reviewer/context briefs), coarse.go
> (exceptions pass), instructions.go, polish.go (thinking islands), assets.go
> (show-intent), moments.go (watch-me pass), together.go (voters), systemone.go
> (the two MCQ briefs). Refactor so each of these prompts loads at runtime from
> `worktree/<step-folder>/<PROMPT-name>.md` relative to the repo root (resolve
> from the exe location upward looking for `worktree/`), falling back to the
> current embedded constant when the file is absent. Keep prompt TEXT byte-for-
> byte identical in the exported files — generate them by dumping the embedded
> strings at first run if the file is missing (write-once bootstrap), then load.
> Add one CLI flag `--prompts-dir <path>` override for tests. No behavior change
> otherwise.
>
> Acceptance: (1) delete a bootstrapped file → tool still runs identically
> (embedded fallback); (2) edit one word in a PROMPT file → next run's report
> shows the changed text was sent (add a sha of each loaded prompt to the step's
> cached decision key so edits invalidate that step's cache automatically —
> important: prompt edit = re-decision, stale cache = bug); (3) unit test:
> loader precedence dir > bootstrap > embedded.

---

## PROMPT 10 — RUN.json: mid-job resume + step runner (do early; everything else uses it)

> Implement the run-state file for `becky-livestream` (all variants).
>
> At job start create/update `<video folder>\becky-edit\<stem>.RUN.json`:
> `{ "video": path, "variant": "qwen|systemone|systemone-stack|video",
> "started": iso8601, "steps": { "<STEP-ID>": {"state":
> "pending|running|done|failed|skipped", "artifacts": [paths], "sha_inputs":
> "...", "error": "...", "finished": iso8601} }, "current": "<STEP-ID>" }`.
> Step IDs are the canonical ones from WORKFLOW-DIAGRAMS.md (S1,S1b,S2,S3,S4,
> S5/Q*/V*, S6,S7,S8,S9,S10,S11, F1..F9 where applicable — write the enum in
> `internal/runmeta`). Every pipeline module wraps its body with
> `run.Begin(id)`/`run.Done(artifacts)`/`run.Fail(err)` which flush the JSON
> after EVERY step (crash-safe: kill the process mid-run, the file says exactly
> where it died). Add flags: `--resume` (skip steps whose state==done AND whose
> sha_inputs still match; re-enter the failed/pending one), `--only <ID[,ID]>`
> (run exactly those steps, warn if inputs missing), `--from <ID>` (rerun from
> ID onward, resetting downstream), `--status` (print the table: done/failed/
> pending with one-line errors, exit 0). `sha_inputs` = sha256 over the step's
> declared input files + loaded prompt shas + variant + guidance.md hash.
> Also add `workflows/Worktree-Status.bat` (calls `--status` on the folder's
> video). This is the mechanism that lets Jordan say "redo F04 only" without a
> Claude Code session.
>
> Acceptance: kill -9 style crash (or Stop-Process) between S6 and S7 →
> `--status` shows S6 done / S7 pending → `--resume` completes the run without
> re-calling any model (verify via llama-server logs: zero new /completions for
> done steps); touch guidance.md → `--resume` correctly re-runs S5 (hash
> mismatch) but not S2/S3.

---

## PROMPT 3 — F02 Audio polish (measured, treatment-aware, per-piece)

> New module `becky-go/cmd/livestream/audiopolish.go` + Vegas-side support in
> `vegas/BeckyFX.cs` (new verb `audiopolish`). Runs AFTER S9 edit-check, BEFORE
> F01 grade in the finishing sequence, for ALL variants (including --model
> qwen).
>
> Behavior:
> 1. **Treated-source gate.** Measure the edit audio (render timeline audio to
>    WAV via the existing render helper): integrated LUFS (build a small EBU
>    R128 meter in Go — ITU-R BS.1770-4; no ffmpeg filter trust), true-peak dBTP,
>    LRA, and inter-sample clipping count. Heuristic for "already professionally
>    treated": |integrated − (−16..−14 LUFS)| band AND peak ≤ −1 dBTP AND LRA ≤ 6
>    AND no clipping AND (source file metadata/folder hint from
>    `worktree/F02-audio-polish/JORDAN.md` says OBS/pro-mic). If treated → log
>    "source pre-treated (measured X LUFS / Y dBTP / Z LRA) — normalizing would
>    degrade it; SKIPPED" and stop. This skip is a FEATURE: Jordan records with a
>    live chain (noise removal, compressor, EQ, limiter) that beats any
>    post-normalization. Never force gain on treated audio.
> 2. **Untreated path:** per kept piece (not whole-project — avoids pumping):
>    measure each piece's speech LUFS using transcript word windows (speech
>    frames only, exclude silences so pauses don't drag the number); compute
>    makeup gain to hit −17 LUFS ±0.5; clamp gain to ±6 dB (beyond that mark a
>    region instead of destroying the take); apply as event volume on the VEGAS
>    audio via BeckyFX.cs verb (per-event gain, NOT a bus compressor — his own
>    trap list warns per-clip FX click at cuts, so use fade-in/out of 5 ms
>    around each event edge when applying gain); place a −6 dBFS true-peak
>    limiter as the LAST FX on the master bus (Sony Limiter or Sound Forge
>    equivalent already installed — enumerate Vegas FX by name with a config
>    override in audiopolish.look.json). Mono must land exactly like stereo
>    (measure L/R separately; if mono source, single gain).
> 3. Re-measure the rendered result; if integrated outside −17±1 or TP > −6,
>    retry once with corrected gains, then fail-visible (region + report line),
>    never silently.
> 4. Settings live in `worktree/F02-audio-polish/audiopolish.look.json`
>    (targets, clamps, treated-band thresholds, limiter FX name) + README.md +
>    JORDAN.md stub ("OBS mic = treated, expect skip; my pre-treat chain notes").
> 5. Register step id `F02` in the run meta (PROMPT 10) so `--only F02` works.
>
> Acceptance: synthetic untreated clip (flat voice, −28 LUFS) → after F02 the
> rendered edit measures −17±1 LUFS, ≤−6 dBTP, zero clicks (visual waveform
> diff at edges shows 5 ms ramps); a copy with `--pretreated` marker file →
> skipped with logged measurements; unit test the R128 meter against ffmpeg
> ebur128 on 3 reference files within 0.3 LUFS.

---

## PROMPT 5 — F06 THE QA GATE + fresh-eyes converge loop (most important step; do after 3 & 10)

> New module `becky-go/cmd/livestream/converge.go` implementing Jason Cooperson's
> step 5c for VEGAS: **after every finishing step (grade, motion/zooms, assets,
> censor, SFX, music, captions), run automated QA → fix deterministically →
> one fresh-eyes review by a model with NO memory of how the items were made →
> fixes re-checked by QA → repeat until zero defects, max 3 rounds, then
> fail-visible.** Neither branch (qwen or --video) currently has ANY post-FX
> verification; this is the single biggest quality gap in the system.
>
> **QA-CHECKS.md** (machine-readable list, lives in `worktree/F06-graphics-qa/`),
> minimum checks, each with a deterministic repair:
> - every graphic/event fully inside frame bounds (read via Vegas ScriptGen
>   enumeration through a new BeckyFX.cs verb `enumerate` that dumps the project
>   tree to JSON: tracks, events, media, positions, FX chains, velocity, opacity);
> - no empty gaps > 1 frame on any used track; no overlapping events on one
>   track unless flagged intentional (crossfade);
> - zoom/crop sanity: pan-crop velocity within configured range (motion.look.json),
>   crop rect keeps BOTH eyes inside frame at every keyframe (reuse the face
>   detector from S4 analysis on sampled frames of the zoomed region — this kills
>   "random zoom" complaints: a zoom that loses the subject FAILS QA);
> - caption/asset text never collides with the face box (face position from S4);
> - censor coverage: every profanity word time covered by a censor event ±1
>   frame, sound level −12.1 dB ±0.5 measured on rendered audio;
> - audio: post-F02 targets hold after later steps (re-measure — FX order bugs
>   caught here);
> - grade containment: hj-starter FX present ONLY on camera-track events,
>   ZERO FX chain entries on asset/graphic/b-roll/caption tracks (this encodes
>   Jordan's invariant: grading never touches graphics layers riding above);
> - SFX events align to their parent graphic start within 1 frame, stacked cues
>   summed peak ≤ −1 dBTP.
> Repairs: deterministic where possible (nudge event, shrink crop, move cue);
> anything not auto-repairable becomes a defect record.
>
> **Fresh-eyes review:** render 1 fps contact-sheet tiles of every region that
> contains finishing work (each zoom, each asset, each caption span: 3 frames
> before/during/after composited into one PNG per item, plus its timeline
> context line from the transcript). Send sheets to the vision model (Gemma-4
> 12B via becky-local, the same path moments.go already uses for watching) with
> `PROMPT-fresh-eyes.md` (externalized per PROMPT 1): *"You did not build this
> edit. For each numbered card judge ONLY: is the subject framed naturally, is
> any text readable and uncrowded, does the effect serve the sentence spoken at
> that moment (given the quoted line), rate keep/fix/remove with one-sentence
> reason."* Model verdicts become defect records; "remove" verdicts on
> zooms/assets get the event stripped and QA re-runs. Cache sheets by content
> hash; budget note: vision calls are local/free.
>
> Loop wiring: each finishing step ends by calling `converge.For(stepID)`; the
> full run ends with `converge.WholeEdit()` once more after export-spec
> validation. Output `defects.json` + report section "objective defects found /
> fixed / remaining". Jordan's review should now start from ZERO objective
> defects — taste only, like Jason's promise.
>
> Acceptance: inject 5 planted defects into a test project (off-frame graphic,
> eye-loss zoom, FX on a caption track, uncovered swear, 3-frame gap) →
> converge finds all 5, repairs 4 deterministically, marks 1 for review; second
> round reaches zero; `--only F06` reruns the loop alone; total added wall time
> on a 10-min edit < 4 min excluding model watch time.

---

## PROMPT 6 — F05a SFX planner (deterministic, from placed events)

> New module `becky-go/cmd/livestream/sfxplan.go` + BeckyFX.cs verb `sfxplace`.
> Sound effects are planned FROM the graphics/motion events as they sit on the
> timeline — exactly Jason's step 6 — and the only existing precedent in our
> codebase is the censor hit, which proves the pattern.
>
> - Read `worktree/F05-assets/JORDAN.md` front-matter for the library root
>   (Jordan is a professional editor and HAS folders of SFX; example structure
>   `D:\Jordan\SFX\whoosh\*.wav`, `D:\Jordan\SFX\pop\*.wav`, …). Fall back to a
>   bundled tiny pack if the path is missing, and MARK every placement "no
>   personal library configured" in that case.
> - `sfx-map.json` (in the same folder) maps event-kind → sound-category →
>   level → offset: zoom-in→whoosh riser at −18 dB aligned −3 frames before
>   velocity start; zoom-out→soft fall; still-crop-in→pop at −15 dB on the first
>   frame; asset insert→choke/dust depending on category tag on the asset file
>   name; censor→existing Censor 2.mp3 −12.1 dB (unchanged). Repeated sounds
>   stay on ONE dedicated SFX track (never scattered); stacked simultaneous cues
>   summed ≤ −1 dBTP (compute overlap, duck later arrivals −3 dB).
> - Placement is deterministic from the enumerated timeline (use the `enumerate`
>   verb from PROMPT 5). After placing, READ BACK every SFX event and verify
>   position/level, then hand to `converge.For("F05a")`.
> - Selection WITHIN a category when several candidates exist: ask System One
>   (cheap MCQ: "which of these three names fits 'clean tech transition' best —
>   whoosh_clean_tech.wav / whoosh_air_swish.wav / whoosh_retro.wav?"), cache
>   per name-set. This is the FIRST expansion of the underused System One.
>
> Acceptance: every zoom/crop/insert event on a test project gets exactly one
> SFX event within tolerance; two overlapping cues measure ≤ −1 dBTP combined;
> rerun is idempotent (no double-placement — check by event count).

---

## PROMPT 7 — F05b Music bed (opt-in flag, locked levels)

> New flag `--music [path|pick]` + module `musicbed.go`, BeckyFX.cs verb
> `musicplace`. Flat bed under the voice on its own track: −20 dB absolute
> floor referenced to the song's sustained entrance (detect first bar after
> intro via RMS plateau, start there), 2 s fade-in, 2 s tail fade-out at edit
> end, sidechain-free (levels are locked, not ducked — ducking is opt-in
> `--music-duck` adding −6 dB during speech windows taken from the transcript
> word times, 150 ms ramps). Runs AFTER SFX (F05a) and BEFORE the final
> converge pass, matching Jason's ordering ("after SFX and before review").
> `--music pick` asks System One MCQ to choose from `worktree/F05-assets/music/`
> by mood tag derived from the topic outline already computed in S5 (free reuse).
> Never clips the limiter (measure combined program again after placement; if
> > −6 dBTP, lower bed 1 dB and re-measure, max 3 tries).
>
> Acceptance: bed absent without the flag; with flag: track exists, measured bed
> −20±1 dB under speech during speech, entrance detected ≥ 5 s into song when
> intro present, final program ≤ −6 dBTP, `--only F05b` works.

---

## PROMPT 8 — F04 Motion/story grammar (replace the "childish" cadence zooms)

> Replace `videovegas.go` looks() cadence logic (creep 1.11×/still 1.12× every
> ~60 s — Jordan calls it unusable and random) with **story-driven moves**, all
> validated by the F06 QA gate. Deterministic skeleton, one narrow MCQ call per
> candidate moment.
>
> Move grammar (encode in `motion.look.json`, extensible):
> - **punch-in**: fires on a KEYWORD (transcript word flagged by System One MCQ
>   "is this word emphatic/new-information/question-word in this sentence? yes/no"
>   — cheap, batched 20 words per call, cached), not on a clock. Zoom to 1.18×
>   centered on face-box from S4 pose data, 6-frame ease-in, hold ≥ 1.2 s or
>   until sentence end minus 4 frames, 8-frame release. NEVER spans a cut.
> - **establish/release**: after any punch longer than 4 s, the NEXT sentence
>   boundary returns to 1.0× (prevents drift-zoom feel).
> - **two-shot emphasis**: when he quotes someone (topic outline from S5 tags
>   speaker switches), slight 1.08× lean toward face side.
> - **creep**: allowed ONLY on stretches > 25 s without a punch (anti-boredom
>   floor), max 1.06× over the stretch.
> - caps: ≤ 1 move per sentence, ≥ 3 sentences apart, none within 20 s of start,
>   none while a full-screen asset covers the camera track.
> - All crops computed from the ACTUAL framing: read face center + shoulder
>   width from S4 samples at that timecode; a punch that would put either eye
>   outside the crop rect is rejected (that failure mode is exactly today's
>   complaint). Apply via existing BeckyFX.cs pipzoom/velocity verbs.
> - After placement: `converge.For("F04")` — a move that fails eye-containment
>   QA gets removed, not retried blindly.
>
> Acceptance: on a 10-min test edit, moves correlate with emphasized words
> (report prints move-time vs keyword-time delta histogram, p95 < 0.4 s), zero
> eye-loss after QA, spacing caps hold, `--only F04` regenerates moves only.

---

## PROMPT 9 — F09 Export specs (locked numbers, one command, long-form parity)

> Long-form currently has NO render path (vertical shorts already render via
> Voukoder template "TikTok NVENC"). Add `becky-go/cmd/livestream/exportspec.go`
> + `--export <preset>` running after picture lock (i.e., after final converge).
> Locked specs in `worktree/F09-export/export.look.json`:
> - `longform`: 1920×1080 (or source res if >1080p downscale to 1080), 30/60 fps
>   matched to source, H.264 High profile via Voukoder NVENC template you create
>   programmatically (mirror the vertical tool's existing Voukoder automation),
>   GOP 2 s, bitrate ladder by duration (<10 min: 12 Mbps VBR 2-pass off NVENC;
>   ≥10 min: 16 Mbps), AAC 320 kbps 48 kHz stereo (mono source → duplicated,
>   never upmix tricks), loudness POST-render check stays −17±1/≤−6 dBTP (fail
>   export, don't ship silent/clipping).
> - `short-landscape` (a future ad cut is one flag): 1920×1080, 20 Mbps.
> - `proxy-review`: 960×540 H.265 fast for phone review of the draft.
> Output naming: final file named from the job folder title, dropped to a
> configured outbox folder; `.veg` untouched. Validate the produced file
> (ffprobe: res/fps/codec/bitrate window/audio channels) — the validator IS the
> checklist, refuse-and-report on mismatch. Update RUN.json step F09.
>
> Acceptance: preset renders, validator green, loudness recheck green on an
> untreated source that went through F02; `--export proxy-review` finishes a
> 10-min edit in < 3 min on this GPU (NVENC confirmed by encoder-name in probe).

---

## PROMPT 11 — Secrets list at intake (move publish-safety upstream)

> Today the publish check (OCR + vision on change frames) runs AFTER the edit —
> too late: sensitive material should be avoided/blurred from the start. Add to
> S1 intake: read `worktree/S01-intake/JORDAN.md` section "NEVER SHOW" (a list
> of patterns: names, faces already tagged by identify DB, address regexes,
> browser tabs, specific apps). Build `secrets-plan.json`: time ranges where the
> desktop/screen region could contain them (screen capture track if present, or
> frames where OCR hits the pattern list — reuse the existing ocr tool on the
> 0.1 s analysis frames from S4, no new sampling). Consequences wired forward:
> (a) S8 lays a blur FX preset on those video event ranges automatically (new
> BeckyFX.cs verb `blurspan`, Gaussian + 8 px safe), (b) S10 marks them anyway
> for review, (c) the old post-hoc publish check remains as the SECOND net
> (defense in depth), reporting "found secrets the plan missed" = a rulebook
> gap Jordan fills in JORDAN.md. Keep the existing minimal-marker philosophy
> for --video (publish findings only) — this prompt upgrades WHERE they're
> caught, not WHETHER they interrupt.
>
> Acceptance: planted fake "address on screen" slide in test footage → blurred
> span exists in the .veg before human review, post-check confirms no raw
> exposure in rendered proxy; empty JORDAN.md → behavior identical to today.

---

## PROMPT 12 — System One expansion (fix the underutilization; do LAST)

> Audit complete: System One today serves exactly two jobs — the stack
> decorator MCQs (systemone.go) and the three-way voter (together.go). Its real
> strength (fast cheap structured MCQ) is unused elsewhere. WITHOUT changing any
> local-model decision authority, add it as the narrow-question engine at:
> 1. F05a SFX name selection (specified in PROMPT 6),
> 2. F05b music mood pick (PROMPT 7),
> 3. F04 keyword emphasis MCQ (PROMPT 8),
> 4. S5-video branch: pre-filter the exceptions-pass candidates — before paying
>    Gemma-12B attention on a part, ask System One per sentence pair "same
>    thought restarted? (yes/no)" to PRE-seed retake pairs which code then
>    confirms against words (it can only PROPOSE, confirmation stays
>    deterministic — protects the false-positive lesson from coarse.go),
> 5. F06 fresh-eyes triage: MCQ "does this card show a visible defect? A/B/C"
>    as a cheap first filter before the 12B look (cuts vision tokens ~70%).
> All calls: batched, cached by content hash under `becky-edit\s1\`, counted
> against the existing $5 cap guard, skippable with `--no-s1` (every consumer
> must have its no-S1 fallback path: default name-match / no prefilter / full
> vision pass). Write `worktree/S05-decision/systemone-stack/JORDAN.md` noting
> what S1 is now used for so future agents don't re-discover.
>
> Acceptance: `--no-s1` produces identical edit decisions on the regression
> fixture (it must only affect cost/latency of helpers, never cut lists);
> spend meter logs per-feature; each feature's cache makes second run 100% free.

---

### Explicitly NOT in scope (keep the agents honest)
- Don't rebuild becky-cut/transcribe/local/systemone (they exist; call them).
- Don't touch build rules/checks.
- Don't convert the pipeline to Python/Premiere/Resolve.
- Don't let any model add cuts. Models propose; deterministic code confirms.
- Don't normalize OBS-treated audio (rulebook 3c) — the skip is the feature.
