# Workflow diagrams · becky-livestream, all variants

Four pipelines, one binary. This page exists so you can point at a number
("step 4b is wrong") instead of typing a novel. The numbered labels below are
the same numbers used in `plan/becky-worktree-plan.md` (the worktree layout) and in
`plan/BUILD-PROMPTS.md` (one build prompt per missing step). **Step numbers are the
canonical vocabulary — use them everywhere.**

```
                    ┌──────────────────────────────────────────────┐
 any variant ──────►│ SHARED SPINE  S1–S9, S10–S11 (checks+markers)│
                    └──────────────────────────────────────────────┘
        --model qwen ──► content decision = D-QWEN  (vote every sentence)
        --model systemone-stack ──► D-QWEN + System One notes fed to Qwen
        --model systemone ──► three-model majority vote + live-chat signal
        --video (YouTube-Edit.bat) ──► content decision = D-VIDEO (coarse,
                            exceptions only) + polish + censor + assets + FX
```

Legend: 🟢 deterministic code · 🔵 local model · 🟣 paid/hosted model · 🟠 human
· 🔴 MISSING (Jason's description kept verbatim) · 🟡 present but wired differently

---

## S · Shared spine (every variant runs these, in this order)

| # | Name | Engine | What it does | Artifacts (`becky-edit\`) |
|---|------|--------|--------------|---------------------------|
| S1 | Intake | 🟢 | Pick the video (dragged / only-in-folder / ask; OBS mkv+mp4 dedupe). Read-only guarantee: footage never modified; everything written goes to `becky-edit\` + the new `.veg`. ffprobe fps + duration. | folder layout |
| S1b | OBS sync *(OBS files only)* | 🟢 | `obs.go`: audio steps run on `<stem>.synced.mkv` (mic moved by Prep-OBS-Footage's measured delay); VEGAS gets the original with per-stream delays (~43 ms AAC offset accounted). | `*.synced.mkv`, `sync.json` read |
| S2 | Transcript | 🟢🔵 | `becky-transcribe`: Parakeet pass 1 + WhisperX second opinion on low-confidence words + Jordan's word list. Cached forever. | `<stem>.transcript.json` |
| S3 | Silence map | 🟢 | **`becky-cut --dry-run`** (auto-editor, margin 0.04 s / 0.25 s, adaptive threshold from the file's own level + VAD post-pass). Produces keep-spans = the dead-air plan. Cached. Also: 16 kHz mono WAV extract. | `becky-cut.json`, `source16k.wav` |
| S4 | Whole-stream analysis | 🟢🔵 | `analyze.go`: sound labels (breaths/laughs/coughs every 40 ms) + picture signals (MediaPipe pose/gestures/face, insightface, every 0.1 s), measured ONCE, reused by every later run/model/guidance. | `sound-*.json`, `pic-*` cache |
| S5 | **Content decision** | 🔵/🟣 | *Variant-specific — see D-QWEN / D-VIDEO / D-S1 / D-STACK below.* Output of every variant: a per-sentence keep list. | `selection-<tag>.json` etc. |
| S6 | Cut points | 🟢 | `edges.go`: intersect keep-list × becky-cut keeps → ranges; edge provenance tagged (`becky-cut` / model boundary); loud edges (above cut threshold inside speech) flagged for markers. Predicted pieces computed on the frame grid. | `plan-<tag>.json` (ranges + predicted pieces) |
| S7 | Breath check | 🟢🔵 | `breath.go`: sound labeler + picture cross-check; every confirmed breath is cut out of the predicted pieces. | breath spots in plan |
| S8 | VEGAS build | 🟢 | `vegas.go`: launch/attach Vegas Pro 18, `new_project`, `BeckyKeepList.cs` lays kept pieces as real trimmable events (dead air already out — direct placement since 2026-10-06), save as `<folder>-<tag>.veg`; existing hand-saved project is never overwritten. | `.veg`, `vegas-job.txt` |
| S9 | Edit check | 🟢🔵 | `verify.go`: timeline vs plan event-by-event (frames, gaps, A/V pairing), then rebuild the edit audio, re-transcribe, align planned words by timeline time. Word lost only when BOTH signals agree (guards against Parakeet's ~0.3 s early timestamps). | verification JSON |
| S10 | Markers/regions | 🟢 | `BeckyMarks.cs`: unsure calls, chat cuts, loud edges, publish findings, verify misses → regions on the timeline. | MARKERS (livestream variants) |
| S11 | Report | 🟢 | `report.go` / `videoreport.go`: what ran, what each model said, timings. | report txt |

Shared spine ends here. Everything below is variant-specific.

---

## D-QWEN · `--model qwen` ("Livestream Edit - Qwen.bat")

The vote-every-sentence design. **This is NOT the YouTube pipeline** — see the
comparison at the bottom.

| # | Step | Engine | Detail |
|---|------|--------|--------|
| Q1 | Lead pass | 🔵 Qwen3.5-4B | Reads transcript in windows (`windows(ss)`), labels EVERY sentence keep/cut with confidence + note, names topics/outline first. |
| Q2 | Review pass | 🔵 Gemma-4 E4B | Reviews only `reviewTargets()`: unsure calls, both sides of every keep/cut boundary. `conclude()` merges: reviewer overrides where it disagrees on a target. |
| Q3 | Chat-answer signal | 🟢🟣 | `chat.go` → `becky-livechat` downloads the chat replay, System One (0.7+) finds which sentences answer chat. An answer is ONE signal — never cuts alone. *(Only feeds posture-based "looking down" corroboration in `context.go`.)* |
| Q4 | Context pass | 🔵 | `context.go`: whole-edit read (topic start/end sanity). |
| → | then shared S6–S11. Plus 4b visual-moments (below) and face zooms after S9. | | |

### 4b Visual moments — livestream variants ONLY (`moments.go`)
For every stretch the edit cuts next to his words: small vision models measure
motion/gesture/face-lost per 0.1 s; anything seen is WATCHED by Gemma-4 12B
(frames+audio, told the surrounding words); reaction/gesture/acting-out label +
a small-model signal (two agreeing) puts the picture back on the frame grid.
Never cuts, only puts back. Face zooms then ride these moments through
`BeckyFX.cs pipzoom`.
**Explicitly disabled for `--video`** (Jordan 2026-10-13: put-back gestures read
as "random movements").

## D-STACK · `--model systemone-stack`
Same as D-QWEN plus: **System One answers two typed questions per sentence
FIRST** (kind-of-talk + belongs-in-edit, hosted, $5/mo cap); its verdict is
printed into each sentence Qwen and Gemma read, with a brief saying "trust it
unless context shows a reason not to." Then plain runLocal(qwen, gemma).
→ Your hunch is right: System One is currently a *hint generator* for exactly
one use (chat separation feeding the voters). See UNDERUTILIZED section in
BUILD-PROMPTS.md.

## D-S1 · `--model systemone` (together.go)
Three independent voters (System One, Gemma-4, Qwen3.5) decide every sentence;
majority stands; split vote while looking-down-at-screen (posture) → cut; then
contextPass reads the whole edit. Reuses saved votes from prior runs when they
exist (free).

## D-VIDEO · `--video` ("YouTube-Edit.bat") — the coarse redesign

Rewritten 2026-10-13 "the openshorts way" after your verdict that voting every
sentence was *"WORSE than simply removing silences."* Structurally different
from D-QWEN — not the same pipeline minus chat:

| # | Step | Engine | Detail |
|---|------|--------|--------|
| V1 | Exceptions pass | 🔵 Gemma-4 **12B** | Reads transcript in 40-sentence parts (8 overlap), names ONLY exceptions: `not_for_viewers` ids + `retakes` pairs. Default: everything he says stays; becky-cut (S3/S6) is the base edit. |
| V2 | Retake confirmation | 🟢 | Code checks every claimed retake against the actual words — the redo must repeat it, or it stays. Mid-sentence restarts found in words with NO model. |
| V3 | Hesitation filter | 🟢 | Pure-hesitation lines ("Um") cut deterministically; "Yeah." never (it was an answer). |
| V4 | Instructions | 🔵🔵 | `instructions.go`: Qwen+Gemma find every on-camera editor instruction (union); Gemma-12B lead + panel understand (move/cut/other, which part, anchor); code applies + regions what was done. |
| V5 | Thinking islands | 🔵 | `polish.go`: a word or two said alone while he finds his next line → cut (fresh-cached). |
| V6 | Show-intent | 🔵 | `assets.go`: lines where he means something to be SHOWN (2-4 word label); picks only from provided files; else marker + MARKERS.txt. |
| V7 | Viewer read | 🔵 | 12B reads the edit like a viewer, every CUT shown in place; may ONLY put cuts back (`restore`), never add cuts. Protected lines (confirmed retakes/not-for-viewers) can't be restored. |
| V8 | Second listen | 🟢 | Wordless stretches (breath cuts split pieces) get a second ASR listen; no word heard → drop. |
| V9 | Tighten | 🟢 | Every edge moved onto the first/last frame of his voice (relative to nearby voice peak — mic-independent). Fixes the "cut 4 frames early on crackle" complaint. |
| → | shared S6–S9, then the finishing block below. | | |

### Finishing block (`videovegas.go`, after edit check S9, each a BeckyFX.cs job)
| # | Step | Detail |
|---|------|--------|
| F1 | hj-starter grade | Beauty Box → AutoLooks → Color Corrector → Color Curves ×2 on the primary video track. 🟡 Runs AFTER cuts/assets (Jason: grade BEFORE graphics, under them). You say this is deliberate (compute + grading doesn't affect cut decisions) — valid because graphics go on separate tracks above. |
| F2 | Censor | every "fuck": audio-edge-found word span, Censor 2.mp3 −12.1 dB, ±1 frame, gap-cut into speech event. 🟢 better than blind placement. |
| F3 | Looks/motion | creep 1.11× 8-12 s / still crop 1.12× 3-8 s, ~once a minute, starting ≥20 s, spanning whole pieces. ⚠️ THE "childish" one — cadence-driven, not story-driven. Build-prompt #8 replaces it. |
| F4 | Publish check (kept) | OCR patterns + Gemma vision on change frames → only postability regions on the video timeline (no editorial markers — your 2026-10-13 rule). |
| F5 | Upload copy | ytcopy.go → titles/description/chapters. |
| ❌ | Audio polish | 🔴 MISSING (see BUILD-PROMPTS.md #3) |
| ❌ | SFX from placements | 🔴 MISSING — only the censor exists (#6) |
| ❌ | Music bed | 🔴 MISSING (#7) |
| ❌ | Graphics QA + fresh-eyes converge | 🔴 MISSING — THE most important gap (#5) |
| ❌ | Locked export specs | 🔴 long-form has no render path (vertical has Voukoder "TikTok NVENC") (#9) |
| ❌ | Run file / mid-job resume | 🔴 caches exist per-step but no run state file (#10) |

---

## Side-by-side: does `--model qwen` differ from `--video`? YES — meaningfully.

| Aspect | `--model qwen` (D-QWEN) | `--video` (D-VIDEO) |
|---|---|---|
| Core philosophy | Model votes on **every sentence** | Model names **exceptions only**; code+silence-map is the base |
| Lead model | Qwen3.5-4B leads, Gemma-E4B reviews targets | **Gemma-4 12B** single reader, parts |
| Live chat | becky-livechat + System One + posture signal | none (by design — pre-recorded) |
| Visual moments 4b | ✅ runs (gestures put back + face zooms) | ❌ explicitly disabled |
| On-camera instructions | not parsed | ✅ full move/cut/apply machinery |
| Polish (thinking/second listen/tighten) | ❌ | ✅ |
| Censor / looks / assets / upload copy | ❌ | ✅ |
| Grade (hj-starter) | ❌ | ✅ |
| Markers | editorial (unsure/chat/loud) | minimal (publish + missing-asset only) |
| Verdict | Same spine S1–S9, **different decision engine + different finishing half**. Claude did NOT rewrite the spine — select/edges/breath/vegas/verify are literally the same functions. The rewrite is confined to step 5 (decision) and the post-build block. No wheel reinvented in the spine; the decision layer was replaced wholesale (with your approval, 2026-10-13). |
