# Pipeline Comparison — Jason Cooperson's "Claude Video Editor" vs. `becky-livestream --video`

Companion to **`becky-livestream-pipeline.html`** (his page's style, my pipeline's wiring; red overlays = steps I'm missing, amber = present but ordered/wired differently).

Sources: `Claude-Video-Editor(Copy).md` / `.html` (build 2026.10.19) and the actual code in `becky-go/cmd/livestream/*.go`, `workflows/youtube/*.bat`, `vegas/`.

Per your constraints: app integrations, languages, and model choices are ignored except where a step itself differs materially. Transcription (Parakeet + WhisperX second opinion vs. his single WhisperX pass) is already better on my side — not discussed further.

---

## 1. The two pipelines, side by side

| # | His step | My equivalent in `--video` | Status |
|---|----------|---------------------------|--------|
| 1 | Intake (copy, job folder, format read, run file, secrets list) | ffprobe + ffmpeg remux, `becky-edit\` artifacts, OBS clap-sync first, guidance loaded | 🟡 mostly — no run-file state pointer, no upfront "must never show" list |
| 2 | Rough cut (transcribe once, filler/dead-air/false-starts cut, EDL + finished script, proof gate) | Steps 4–13 of main.go: silence map → coarse content decision (Gemma-4 12B exceptions only, retakes confirmed in code) → instructions → thinking-islands → viewer-read put-back pass → BeckyCut.cs lays events → verify() re-transcribes the timeline | ✅ **stronger** (see §3) |
| 3 | Audio polish (−17 LUFS speech into −6 dBFS limiter, measured every time) | **Nothing.** No LUFS/loudness/normalization anywhere in the livestream tool (verified by grep) | 🔴 **MISSING — biggest result gap** |
| 4 | Grade (before & underneath graphics, camera footage only) | hj-starter track FX (Beauty Box, AutoLooks, Color Corrector, Color Curves ×2) — applied **after** censor/zooms/assets, on the video track that later jobs modify | 🟡 **ORDER DIFFERS** |
| 5 | Graphics + captions (storyboard → build → QA gate → one fresh-eyes review round; captions built from the finished script; secrets found **and blurred**; hero-frame stills) | Creep/crop zooms (~1/min), assets/B-roll inserts + MARKERS.txt, censor SFX | 🔴 partial: no graphics QA loop, no long-form captions, secrets marked as regions but never blurred, no hero-frame picker |
| 6 | Sound effects (planned from placed graphics, one layer per repeated sound, stacked cues < −1 dBTP) | Only the censor hit (which has an audio-derived word-edge finder — genuinely better than his blind placement) | 🔴 **MISSING** otherwise |
| + | Music bed (opt-in, flat −20 dB, after SFX before review) | Not in the YouTube workflow | 🔴 MISSING |
| 7 | Review (taste only, draft arrives with zero objective defects, hand edits stay) | Human review on the saved .veg **is** the endpoint ✓; rerun won't overwrite a project you saved yourself ✓ | ✅ concept matches; precondition ("zero objective defects") not fully enforced because 3–6 are open |
| 8 | Export (locked platform specs, one command names final + Downloads copy) | Long-form run stops at the project; vertical shorts render via Voukoder template | 🔴 **MISSING** for the master |
| — | (none) | Breath check · second listen · tighten edges · on-camera editor instructions · publish-safety check · upload copy (titles/desc/chapters on edit times) · MARKERS.txt | ✅ mine-only depth |

## 2. Ordering discrepancies (the ones that change results)

1. **Grade position.** His invariant: grade goes down *before* graphics and *underneath* them so graphic colors never shift. In `videovegas.go` the order is censor → zooms/crops → starter-FX package → markers → report. The B-roll track sits above the video track carrying the grade, and the grade is applied last. Move the starter-FX job ahead of the asset/zoom/censor jobs.
2. **QA loop placement.** He runs an automated graphics-QA gate between build and converge, with fixes machine-rechecked. I have exactly one post-build verification (verify, right after BeckyCut lays the cut); everything added afterward (censor, zooms, FX, inserts) is fire-and-forget. His diagram's rule — *"every step checks its own work before you watch a frame"* — holds for my cut but not my finishing passes.
3. **Secrets detection timing.** He finds on-screen secrets at intake ("a list of what must never show when screens are on camera") and blurs them during graphics. My publish check runs near the end and only drops regions for me to handle manually. Same information, much later, with more human work left over.
4. **SFX follows picture.** His SFX plan is generated from graphics *as placed*, so moving a graphic re-runs SFX. I don't have this dependency at all yet; when I add it, generate the cue list from the completed zoom/insert layout (my BeckyFX jobs already know each event's time and kind — a deterministic table maps kind→sound→level).

## 3. Nuanced detail differences inside shared steps

- **Retake policy:** him "say a line twice, the last take wins"; me "keep the attempt he finishes," and code requires the redo to actually repeat the abandoned words. Different results when the best take isn't the last — worth keeping mine, but note it's a deliberate divergence.
- **Cut padding:** him fixed pads (≈0.08 s pre / 0.12 s post) chosen by the LLM writing cuts.json; he admits in his honesty section "some cuts still clip or trail a word." Me: silencedetect boundaries + `tighten()` snapping every edge onto the first/last frame of voice using mic-relative levels, then verify cross-checking two ASR signals. Mine should win here consistently — this is why my drafts "listen" better.
- **Proof gate:** equivalent in spirit; mine additionally detects *leftovers* (heard-but-not-planned words) and tolerates Parakeet's ~0.3 s early-word drift explicitly.
- **Approval gate:** he stops and shows the cut list to the human before building anything. I never stop mid-run — the human sees the finished timeline. That's a taste call, but his version prevents wasted renders on a bad content decision; see §5 item 1.

## 4. Starter prompt vs. my system

His starter prompt is a *bootstrap spec*: make Claude Code build the pipeline from scratch in an empty folder, connect live via MCP, and operate with guardrails. Structurally different from mine (pre-built Go/C# tools; the model only answers "what stays"). Five things in his prompt my process lacks, ranked by value-per-effort:

1. **"Show me the cut list and stop until I approve it."** A `--approve` flag that prints the keep/cut decisions (with reasons and clock times) and waits before BeckyCut builds. One day of work; kills whole-run re-runs caused by one bad model call.
2. **The honesty report ("Tell me the truth").** My `report-video.md` lists what was cut and why — but never states plainly what the run did *not* do (no loudness pass, no export, no graphics QA). Adding a fixed "not done in this run" block would set review expectations correctly.
3. **"If something fails twice, stop and tell me."** My retry/fallback logic keeps grinding; a hard two-strike stop would cut the frustration tax.
4. **"When you learn something non-obvious, write it in CLAUDE.md."** I do this (user-feedback.md, habits.md) — already solved.
5. **Prove-the-connection handshake** (marker add/read-back in a throwaway timeline). Cheap insurance against Vegas scripting dying mid-job; pairs with #1.

## 5. "What's inside" — what I'm missing, specifically

His layers mapped to my repo (full detail in the HTML's What's-inside section):

- **Rulebook:** I have CLAUDE.md + SPEC docs + excellent per-file docstrings, but no single index where each rule points at the one doc that owns it. Rules are scattered across SPEC-*, HANDOFF-*, user-feedback files.
- **App table:** N/A (one app) — but the same discipline should become a "step × engine" table: which step is deterministic code, which asks a model, which needs a human click. Today that lives only in Go comments.
- **Skills:** my Go modules *are* compiled skills — stronger than prose. Missing skills are for his steps 3–6/8: audiopolish, graphics-QA/converge, SFX planner, export-spec renderer.
- **Looks:** the real gap. Every tuned number (starterFX order, creep 1.11×, looksEvery 60 s, censor −12.1 dB, pads, thresholds) lives in Go constants. He keeps all style numbers in one per-look artifact, nothing app-specific inside it. Extract a `hj-starter.look.json` that becky-livestream loads — enables a second look without touching code, and makes "what have I actually locked?" reviewable.
- **Playbooks/traps:** his four trap stories have my equivalents (GPU sharing between llama servers, mkv remux dedupe, Parakeet early-word drift, Qwen vision hangs) — but scattered in code comments. Consolidate into `vegas/VEGAS-TRAPS.md`.
- **30+ tools:** comparable count for cutting/QA-of-cut; near-zero for finishing (voice gain meter, graphics QA, framing continuity, secrets blur, export validator).
- **Job folders/run file:** my cache-hit design gives resumability; a small explicit `run.json` state pointer ("step 7 of 12, next: secondListen") would survive crashes cleanly and let the bat file resume mid-job.
- **Library:** I have update scripts and the censor sound; no SFX pack, and settings aren't externalized (see Looks).

## 6. "Going further" — his listed problems, scored against me

| His problem | My status |
|---|---|
| Cuts landing early (double rounding, mixed fps) | ✅ **Solved + beyond**: tighten() waveform snapping; verify() re-transcribes the rendered timeline instead of trusting timestamps |
| Timeline call silently deleting clips | 🔴 Open class: mitigate with pre/post event-count + duration snapshots around each BeckyFX job; refuse save on mismatch |
| Audio pumping/clicking from loudness fixes | 🟡 Partially avoided by architecture (silence removal, voice-snapped edges) — but I haven't met it yet because I do no leveling; expect it the day LUFS normalization lands; do it per-piece with measured makeup, never a whole-project compressor |
| Silent font substitution | ✅ N/A today (in-app engines); becomes live if I add burned-in long-form captions — budget a QA check then |

## 7. Recommended build order (highest result-delta per hour)

1. **Audio polish** (`becky-audiopolish` VEGAS pass: measure kept-speech LUFS per piece → event/bus gain into a limiter → verify by measuring the output). This alone explains most of the "his end result looks SIGNIFICANTLY better" gap. Fully deterministic — pure becky-tools philosophy.
2. **Locked export specs + one-command render** for the reviewed master (the Voukoder plumbing already exists in becky-vertical).
3. **Reorder videovegas.go**: starter-FX (grade) first, under everything; then assets/zooms/censor; then a light post-placement check (event counts, durations) = mini QA gate.
4. **`--approve` cut-list gate** before building (his prompt's best idea, adapted to a batch tool: print decisions, wait for Y/N/edit).
5. **SFX planner** from placed motion events (deterministic table), then **music bed** opt-in flag.
6. **Externalize the look** (`hj-starter.look.json`) + **VEGAS-TRAPS.md** consolidation.

## 8. Where I'm already ahead — don't trade these away

Two-pass transcription (per your constraint, skipped), waveform-derived cut edges vs. his admitted fixed-pad clipping, code-confirmed retakes, on-camera instruction following, breath/second-listen passes, publish-safety OCR+vision sweep, chapters timed on the edit rather than the raw, and a model that only ever makes coarse calls with everything else deterministic. His own honesty section concedes three months of debugging to reach parts of this. The comparison should be read as *"add his finishing half to my cutting half"*, not as a reason to rebuild anything that works.
