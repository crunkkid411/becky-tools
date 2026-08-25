# becky-tools — forensic video toolkit — **START HERE**

This is THE one file for *building* becky-tools, not for the forensic agent **USING** them. The forensic agent carries a mountain of legal context and must spend **zero** of it on becky internals.

## What it is
Offline command-line tools that ingest a video/audio file and tell you **WHO is in it, WHAT is
said (with timestamps), WHAT happens on screen, and WHERE** — for human-reviewed investigation.
Everything runs locally; nothing is uploaded. It does **not** conclude guilt — but it DOES reach
**confident, corroborated conclusions** when multiple signals agree (voice + face → "it's Shelby"),
instead of dumping maybes for users to sort. A lone weak signal stays "unknown." (See
`FORENSIC-OUTPUT-PHILOSOPHY.md`.)

## Where it is
- Tools: `X:\AI-2\becky-tools\becky-go\bin\becky-*.exe` (24+ binaries; new: `ocr`, `cluster`, `motion`).
- Friendly entry point: **`becky.exe`** (the orchestrator). Call by full path or add `bin\` to PATH.
  (`becky-ask` — a double-click chat front-door — is being added; see `SPEC-BECKY-ASK.md`.)
- The case "knowledge base" of known people (built from the wiki): `X:\AI-2\becky-tools\becky-go\kb-final`
  (layout: `voice-prints/<Name>/*.wav`, `face-prints/<Name>/*.jpg`). John, Shelby, Hair Jordan enrolled.
- Search commands (`find`, `index`, `corroborate`) need the embedding server running once:
  `X:\AI-2\becky-tools\start-embed-server.bat`. Transcribe/diarize/identify/enroll do **not**.

## ARCHITECTURE — becky is SELF-ORCHESTRATING (Jordan, 2026-06-26; the load-bearing decision)

**How the forensic agent uses becky: ONE dumb call.** It runs `becky-transcribe <file>` (or whatever the
request is), or — if there's no specific tool — asks **`becky-ask "<plain English>"`**. That is the entire
contract. The forensic agent knows **no flags, no tool suite, no protocol, no chaining.** It does not read the
playbook below.

**becky does ALL the thinking, deterministically, INSIDE the tool call.** That single `becky-transcribe` call
internally runs becky's workflow + protocols and decides for itself:
- does this need diarization? if so, **how many speakers** — and did the forensic agent already pass that
  knowledge? (accept caller-supplied facts, else infer them);
- **validate** the result (diarize / transcribe / ocr — whatever was asked) with **Gemma-4 E4B when confidence
  is low**; still unclear → **escalate to Gemma-4 12B**. becky has the LLMs to make these calls *when necessary*;
- return ONE finished, corroborated result. The caller never sees the machinery.

**Why this shape, and not the others (so no agent re-proposes them):**
- **NOT an MCP server / a big tool list.** That forces the forensic agent to know and chain atomic tools — the
  opposite of "one dumb call" — and it's a fragile server. **Rejected.** (Built once by mistake; removed.)
- **NOT "the agent follows the playbook."** Protocols-as-prose are *suggestions* an agent ignores — and the
  forensic agent **did ignore almost every becky protocol**. becky-tools is **deterministic, not a suggestion**:
  the orchestration must be **compiled into the tools**, where it cannot be skipped.
- The **playbook below is the BUILD SPEC** for that internal orchestration — what becky must do *inside* the
  call — NOT a checklist for the forensic agent to run by hand.

**The bar (Jordan): "that's how this needs to work, or it simply does not work."** A becky entry tool is not
done until a single call returns the finished, validated answer with the diarize/validate/escalate decisions
made *internally*.

### The enforcement engine: `internal/orchestrate` (built 2026-06-26 — the protocols are now CODE)

The corroboration protocol is no longer prose anyone can ignore — it is a deterministic, unit-tested
gate in **`becky-go/internal/orchestrate`**. Use it; do not re-implement the rules in each tool.
- `Corroborate(claim, rules)` → a `Verdict` that is **Concluded only with ≥2 independent agreeing
  signals** (same source twice ≠ corroboration; sub-confidence signals don't count), else Candidate/
  Unknown. Naming is the same rule on an identity claim. A **presence** claim never concludes without a
  `KindWatched` signal (a model actually watched it) — a mention or motion burst can't prove presence.
- `ResolveClaim(...)` forces the **confidence ladder**: not-concluded → validate (Gemma-4 E4B) →
  escalate (12B), re-running `Corroborate` each loop, so the ladder can't produce a verdict the protocol
  rejects.
- `Resolve(...)` returns only **Concluded** facts as stated output; Candidates/Unknown are held (no flood
  of maybes).

**WIRING BUILD-SPEC (the local step that makes a tool self-regulate).** An entry tool (e.g.
`becky-transcribe`, or a `becky` orchestrator verb) becomes self-regulating by:
1. running the deterministic workflow (the playbook below) — shell the building-block `becky-*.exe`;
2. mapping each tool's JSON output into `orchestrate.Signal`s tagged to `orchestrate.Claim`s (e.g. a
   `becky-identify` hit → `KindPrint`; a `becky-validate` watch → `KindWatched`; a transcript mention →
   `KindMention`), carrying through any caller-supplied facts (known speakers);
3. implementing `orchestrate.Executor.Validate` to call **Gemma-4 E4B then 12B** locally for low-confidence
   claims — these are the ONLY models that watch VIDEO+AUDIO; Qwen3.5 is text/single-image only and is NOT
   in this ladder (the models live on the PC; `forensicrun.NewGemmaLadder`);
4. returning `orchestrate.Resolve(...)` — the final corroborated output. The forensic agent sees only this.
The engine + its rules are cloud-built and proven; steps 1–4 are the local model wiring.

**The ONE-CALL entry — `becky-case`** (what the forensic agent actually calls): `becky-case --file X
[--subject Y]` returns ONE final corroborated output and nothing else. It decides the plan deterministically
(diarize/gemma4-check only when speakers > 1, from `internal/workflowdef`), runs the tools, and pushes every
result through the gate: a name is stated only when corroborated, an on-screen interval only where a model
watched it, maybes are held. No flags, no chaining, no protocol for the agent to remember. Proven end-to-end
(one speaker → plan skips diarize; Shelby named, John held, cat on-screen at [10-13]). Tool/model runs are local.

**The protocols enforced in RUNNING, tested tools (the proven pattern — extend it, don't reinvent):**
- **`becky-resolve`** (naming): reads becky-identify's real output and STATES a name only when corroborated
  (voice+face → named; a single weak match → held candidate, then escalated through the Gemma-4 ladder, named
  only if the model corroborates). Kills the wrong-person voice-ID at the structure level.
- **`becky-presence`** (on-screen): the cross-tool chain compiled — mentions + motion bursts + a vision-model
  WATCH are grouped by time window; a window is STATED on screen only if a model actually watched it AND ≥2
  sources agree (proven: "cat" concludes where a model saw a cat, NOT where it saw a dog). Tight intervals, no
  smeared blobs, no mention-or-motion-as-presence.
The remaining tools/protocols follow this same pattern (`HANDOFF-SELF-REGULATE.md`); the model calls are local.

## Find or verify a SUBJECT on screen — the corroboration playbook (the BUILD SPEC for becky's internal orchestration)

This is the recipe the suite is BUILT for, and the one most often done wrong. The tools are
deterministic building blocks; **YOU (the agent) chain them.** A job like "find the clip where the cat
shows its chipped tooth" is NOT one tool — it is this chain. **Nothing is "on screen" until a vision
model actually WATCHED the segment and said so.** (Verified 2026-06-24: `becky-validate` watches a clip's
frames+audio with Gemma-4 and returns per-frame observations in ~30s — the capability is real; use it. This is **MANDATORY**)

### Evidence hierarchy — know what each signal actually proves
| Signal | Tool | Proves | Does NOT prove |
|---|---|---|---|
| subject *spoken about* | `becky-transcribe` | a word was said at [t] | that the subject is on camera — people narrate off-screen things constantly |
| *something moved* at [t] | `becky-motion` | motion at frame precision | WHAT moved or WHO — a person gesturing trips it as often as a pet. A burst is a CANDIDATE MOMENT to go look, nothing more. |
| a fast caption | `becky-vision` (LFM 450M) | a rough guess | anything fine — the 450M confuses cat/dog and misses detail. TRIAGE ONLY. |
| a print match | `becky-identify` | a KNOWN voice/face print matched | identity on its own — face alone is weak; trust a NAME most when corroborated |
| **the model WATCHED it** | **`becky-validate` (Gemma-4)** | **frames + audio actually seen/heard** | — **THIS is the step that can say "a cat is on screen at [t1,t2]."** |

### The chain — do it in this order
1. **NARROW (cheap/fast).** Use transcribe / motion / identify / LFM-vision to get a SHORT list of
   high-likelihood windows. Every one is a *candidate*, not an answer.
2. **WATCH each candidate with Gemma-4 — the load-bearing step.**
   ```
   becky-validate "<clip>" --window <seconds> --fps 4 --verbose        # Gemma sees ~4 fps of frames + the audio
   becky-validate "<clip>" --motion motion.json                        # auto-aims Gemma at the top becky-motion burst
   ```
   Add your own plain question: `--question "Is a cat visible in any frame? If yes, describe it."`
   **If Gemma does not confirm the subject, the window is OUT — full stop.**
3. **CORROBORATE before concluding.** Ship a window only when **>=2 independent signals agree** (e.g.
   Gemma says "a cat is visible" AND a print match / the subject persists across frames). One lone signal =
   "candidate", never a result. (becky's core rule — `FORENSIC-OUTPUT-PHILOSOPHY.md`.)
4. **RE-VERIFY close calls with the bigger model.** When the call is marginal, run the SAME watch on
   Gemma-4 **12B**: `BECKY_AVLM_VARIANT=12b becky-validate "<clip>" --window <s> --fps 4`
   (the 12B GGUF must be present — `scripts\get-gemma4-qat.ps1 -Include12B`).
5. **FINE DETAIL on the best single frame** (e.g. a chipped tooth): pick a frame with the mouth OPEN and ask
   the STRONG model about that one still — `becky-vision --image best.jpg --gemma --prompt "Describe this cat's mouth and teeth in detail."`
6. **SHIP only what was verified, TIGHT** — +/- 2-3 s around the confirmed moment, not a 1-3 min window. Record
   which signals confirmed each clip.

> **Batch tip:** `becky-validate` spawns a fresh Gemma server per call (re-loads the model each time). To
> watch MANY windows, start ONE server and point every call at it:
> `llama-server -m <gemma-qat.gguf> --mmproj <mmproj-BF16.gguf> -ngl 99 -c 16384 -fa off --port 8077`
> then `becky-validate "<clip>" --server-url http://127.0.0.1:8077 ...` (also works on `becky-vision --gemma --server-url`).

### The discipline rule this playbook exists to enforce
**If you (or a tool) looked at a window and the subject is NOT there, you DROP it. You never put an
unverified or contradicted clip on a timeline "anyway."** A transcript mention or a motion peak is NEVER
enough to claim the subject is on screen. "Not sure" -> say **unknown**, don't ship it. Wide, unverified
windows that waste a human's review time are a TOOL-USE FAILURE, not a near-miss. Multiple corroborated data points make decisions, never just one.

## Find evidence across the WHOLE corpus — the two-stage judge (`becky-judge`)

When the request is a broad forensic sweep of ALL the transcripts ("find every clip where he tells
viewers to go after Shelby or Hair Jordan"), do NOT read 1,100+ files. Run the two-stage pipeline:

```
becky-judge --folder E:\TakingBack2007 \
  --query "directing or encouraging viewers to harass, dox, mass-report, or go after Shelby or Hair Jordan" \
  --rubric "C:\Users\only1\Documents\Obsidian\llm-wiki-CLANCY-TRIAL\wiki\becky-judge-forensic-rubric-and-alias-map.md"
```

- **Stage 1 — RECALL:** `qmd` hybrid search (BM25 + vector, Vulkan GPU) narrows the 1,136-file index to
  a few dozen candidate windows. becky forces the right GPU/index env, so it just works.
- **Stage 2 — JUDGE:** **Sonnet 5, xhigh reasoning, 1M context** reads ONLY those candidate windows
  against the `--rubric` guide — the case **alias map + rubric** (green hair = Hair Jordan; "my ex-wife"
  resolves to Shelby by the arrest-date arithmetic; the satire-disclaimer + DARVO traps). It resolves the
  coded language, rejects noise, and keeps only genuine hits, labelling each with WHO + why.
- **Output → Becky Review:** survivors are written to `_forensic_hits.json` in the exact
  `{srt, in, out, q}` shape `becky-hits` consumes → double-click **"Open Forensic Hits"** to load them onto
  the timeline. (Or: `becky-hits --hits _forensic_hits.json --folder <dir>`.)

Flags: `--limit N` (candidates), `--window N` (context cues each side), `--dry-run` (Stage 1 only, no
LLM), `--selftest` (offline proof), `--model`/`--effort` (default `claude-sonnet-5[1m]` / `xhigh`). Set
`BECKY_JUDGE_GUIDE=<the rubric path>` once and you can drop `--rubric`. Degrade-never-crash: no Claude →
every candidate is kept with a note, so Stage 1 is never lost. The judge also returns **new_alias**
candidates (coded refs not yet in the map) so the guide grows as new videos are ingested — confirm them,
then append to the guide's changelog with the file + timestamp where established.

**Asking Jordan a question about clips — the human-review Q&A panel.** NEVER put a question you need a
human to answer into a markdown file and expect Jordan to scroll to it (he can't). Instead, add a `"?"`
field to the relevant hits and run **"Open Forensic Hits"** — becky-hits groups the questions into a
sidecar and Becky Review shows each as a clickable card in the right panel. Jordan clicks a question, the
tied clips play in order, he types the answer. Multiple clips (even from different videos) can share one
question — that's corroborating context. Hit-list shape:

```json
[
  {"srt":"FILE_A.srt","in":"00:12:34,560","out":"00:12:41,000","q":"quote","?":"Who is McToy Toy?"},
  {"srt":"FILE_B.srt","in":"00:18:48,650","out":"00:19:09,300","?":"Who is McToy Toy?"}
]
```

Answers are appended to `_forensic_answers.json` beside the reel (`{id,question,answer,answered_at}`). An
agent reads that file next session and routes each answer into the wiki (the GUI never edits markdown).

## The easy way — the `becky` command (plain language)
```
becky enroll-wiki --wiki "C:\Users\only1\Documents\Obsidian\llm-wiki-CLANCY-TRIAL\wiki" --kb kb-final
becky profile "John Clancy" --kb kb-final --corpus "<video-or-folder>"
becky appearances "Shelby" --kb kb-final --corpus "<folder>"
becky find "affair" --db "<forensic.db>"
becky corroborate "<claim>" --kb kb-final --corpus "<folder>"
becky "this is Shelby" "<clip>"            # teach a new person from a clip — no "enroll" jargon
```
- `enroll-wiki` reads the case wiki and **auto-builds the known-people KB** (voice + face) — no
  manual clip-making. To add ONE person from a clip, just tell it in plain words:
  `becky "this is <name>" <clip>` (single-person clip = safe; multi-person uses the dominant speaker).
- Use a **fuller name** ("John Clancy", not just "Clancy") — a lone surname is ambiguous when
  several people share it.
- Every `becky` command prints a plain-English headline **plus** the full JSON.

## Ingest ONE video — the forensic pass
Each prints JSON (or use `--format srt|txt|vtt` on transcribe):
```
becky-transcribe "<video>" --format srt          # what's said + timestamps
becky-diarize    "<video>"                        # how many speakers + when each talks
becky-identify   "<video>" --kb kb-final          # which KNOWN people (by voice + face)
becky-validate   "<video>"                        # AV description of on-screen actions (Gemma-4, default backend)
```
`becky-validate` defaults to `--backend gemma4-local` — you don't need to pass it. See the
**Vision / audio-visual models** section below for which model runs, how to pick 12B vs E4B,
and how to describe a single still image.
All-in-one (transcript + speakers + identities + events + on-screen OSINT + OCR; resumable):
```
becky-pipeline "<video-or-folder>" --kb kb-final --steps transcribe,diarize,identify,events,osint,ocr --out ingest-out
```

## Find text, locations, and recurring strangers
```
becky-ocr "<frame-or-osint-dir>" --db forensic.db   # read signs/IDs/chat/timestamps off frames -> searchable
becky-motion "<video>"                                # MOTION ONLY: WHEN something moved (NOT who/what). Each burst is a CANDIDATE -> route to becky-validate to actually LOOK.
becky-cluster --db forensic.db                        # group recurring UNKNOWN people: "Person A appears in N clips"
```
On-screen text is searchable in the SAME `becky find` as speech (addresses live in signage — your clips carry no GPS).

## Vision / audio-visual models (the eyes + ears)
Two separate tools, two different llama.cpp paths, **one model on the GPU at a time** (8 GB RTX 3070):

- **`becky-vision`** — describe / read text off ONE still image. Default = the fast LFM2.5-VL **1.6B**
  via **`llama-mtmd-cli.exe`** (image-only, deterministic). Add **`--gemma`** to instead run the strong
  Gemma-4 on the SAME still via `llama-server` — use it for fine detail the tiny model gets wrong.
- **`becky-validate`** — Gemma-4 **audio-visual** pass over a short VIDEO CLIP via **`llama-server.exe`**
  (it ffmpeg-samples frames + 16 kHz mono audio, then asks cross-modal questions). This is the ONLY tool
  that understands AUDIO, and the one that WATCHES a segment — **Gemma-4 E4B → 12B are the only models that
  watch video**. (Do not point the default LFM path `llama-mtmd-cli` at Gemma — it hard-crashes 0xC0000409;
  that is why `--gemma` uses `llama-server`.)
- **Qwen3.5-4B orchestrator** (Unsloth `UD-Q4_K_XL`) — becky's GENERATIVE brain + ask-router: it routes
  `becky-ask` (act-vs-discuss), proposes in `becky-scout`, and reasons in `becky-new-tool` (all **TEXT**).
  It is also **image-capable** via its own F16 mmproj, so **`becky-vision --qwen`** gives a SINGLE-STILL
  second opinion (a DIFFERENT family than LFM/Gemma — agreement on one image is real corroboration). It
  does **ONE still at a time — it does NOT watch video and has NO audio**; all video+audio watching stays
  Gemma-4 (`becky-validate`). It is **NOT a "Qwen3.5-VL"** (no such model); reach for the separate heavy
  **Qwen3-VL** only for a dedicated VL job. Resolved by `config.Qwen()` (`BECKY_QWEN_MODEL`); fetched by
  `scripts/get-qwen35.ps1`.

### Models on disk (full paths)
| Model | Role | GGUF | mmproj |
|---|---|---|---|
| LFM2.5-VL **450M** | fastest still-image describe/OCR | `X:\AI-2\becky-tools\models\lfm2.5-vl-450m\LFM2.5-VL-450M-Q8_0.gguf` | `…\mmproj-LFM2.5-VL-450m-Q8_0.gguf` |
| LFM2.5-VL **1.6B** (default for `becky-vision`) | fast higher-quality still image describe/OCR | `X:\AI-2\becky-tools\models\lfm2.5-vl-1.6b\LFM2.5-VL-1.6B-Q8_0.gguf` | `…\mmproj-LFM2.5-VL-1.6b-Q8_0.gguf` |
| **Qwen3.5-4B** (Unsloth) ← *orchestrator + ask-router + SINGLE-IMAGE corroborator; image-capable, NOT a "Qwen3.5-VL"; never video* | routes becky-ask, proposes in becky-scout, `becky-vision --qwen` single still | `X:\HuggingFace\models\unsloth\Qwen3.5-4B-GGUF\Qwen3.5-4B-UD-Q4_K_XL.gguf` | `…\mmproj-F16.gguf` (image) |
| **Gemma-4 E4B-it QAT** ← *default AVLM* | AV clip analysis (vision **+ audio**) | `X:\AI-2\becky-tools\models\gemma4\gemma-4-E4B-it-qat-UD-Q4_K_XL.gguf` | `X:\AI-2\becky-tools\models\gemma4\mmproj-BF16.gguf` |
| **Gemma-4 12B-it QAT** ← *re-verify tier (downloaded + verified 2026-06-24)* | a tier up on reasoning + audio | `X:\AI-2\becky-tools\models\gemma4\gemma-4-12B-it-qat-UD-Q4_K_XL.gguf` *(6.3 GB, present)* | `…\mmproj-12B-BF16.gguf` *(present)* |

QAT = quantization-aware-trained: near-bf16 quality at 4-bit memory. Always the Unsloth **`UD-Q4_K_XL`**
build (a naïve q4_0 throws QAT's benefit away). The **BF16 mmproj is mandatory** for Gemma — other
mmproj quants corrupt the audio encoder. Paths are resolved in `becky-go/internal/config/config.go`
(override per-machine via `~/.becky/config.json`); none are hardcoded in the tools.

### Pick 4B vs 12B (Gemma AVLM)
`becky-validate` resolves the active Gemma via `config.GemmaAVLM()`:
- **Default = E4B-it QAT** (~5 GB, the no-drama fit). Just run `becky-validate "<clip>"`.
- **12B = set the env var** `BECKY_AVLM_VARIANT=12b` (the re-verify tier). **Downloaded + verified working
  2026-06-24**: it loads on the 3070 at full GPU offload (`-ngl 99`) and runs a still in ~8 s, with
  noticeably finer detail than E4B. If the 12B GGUF is ever absent it silently stays on E4B
  (degrade-never-crash); re-fetch with
  `powershell -ExecutionPolicy Bypass -File "X:\AI-2\becky-tools\scripts\get-gemma4-qat.ps1" -Include12B`.
  Use it to RE-CHECK a close call after E4B: `BECKY_AVLM_VARIANT=12b becky-validate "<clip>" --window <s> --fps 4`.

### Describe ONE still image
Fast LFM2.5-VL (default) — image-only triage:
```
becky-vision --image "<frame.jpg>" --prompt "Describe this image factually." [--json]
becky-vision --image "<frame.jpg>" --dir "X:\AI-2\becky-tools\models\lfm2.5-vl-1.6b" --prompt "..."   # use the 1.6B
```
Override the model explicitly with `--model <gguf> --mmproj <gguf>`; `--bin` retargets `llama-mtmd-cli.exe`;
`--ngl 99` = full GPU offload (default).

**Strong Gemma-4 on a still — `--gemma`** (for the fine detail the 450M gets wrong; verified working
2026-06-24, ~4 s/frame):
```
becky-vision --image "<frame.jpg>" --gemma --prompt "Describe this cat's mouth and teeth in detail." [--json]
becky-vision --image "<frame.jpg>" --gemma --server-url http://127.0.0.1:8077   # reuse a warm server for many frames
```
`--gemma` routes the still through `llama-server` (the default `llama-mtmd-cli` hard-crashes on Gemma-4),
disabling thinking + flash-attention for you. It honors `BECKY_AVLM_VARIANT=12b` for the bigger model.
Still image-only (no audio) — for audio + motion across a segment, use `becky-validate` on a clip.

### Analyze a short CLIP (with audio)
```
becky-validate "<clip.mp4>"                          # default Gemma E4B-QAT, vision + audio
becky-validate "<clip.mp4>" --window 30 --fps 1                  # --window = LENGTH in s (<=60); start AT a burst via --motion. caps: <=60s video, <=30s audio @16kHz mono
BECKY_AVLM_VARIANT=12b becky-validate "<clip.mp4>"   # 12B (only if its GGUF was fetched)
```
Audio IS understood by `becky-validate` (Gemma's audio encoder); `becky-vision` is silent/image-only.

### Neutral prompting (forensic discipline)
Drive the model NEUTRALLY: one factual instruction, never primed toward a conclusion. For a possibly
broken/missing tooth, ask **"Describe this cat's face, mouth, and teeth in detail."** — NOT "is the
tooth broken?". Over-prompting a small VLM produces confidently-wrong output. The model only sees what's
in frame: if the mouth is closed it will (correctly) say no teeth are visible — pick frames where the
mouth is open for any dental question.

## Output style (for the description/validate tools)
Governed by `FORENSIC-OUTPUT-PHILOSOPHY.md`: **plain words** (butt/hips/waist, not "iliac
crest"), **name what we know** (write "John Clancy", not "speaker_1"), describe the **act and
its force/resistance dynamics**, flag only genuine uncertainty. Clarity = recall.

## Honest status (what works, what to watch)
- **Reliable:** transcribe; diarize (single-speaker → 1; hardened); **identify by VOICE** + the
  corroborated voice+face fusion; search (now incl. OCR text); enrollment incl. natural-language
  `becky "this is X"`; OCR; motion; the `becky` orchestrator. Portrait-video faces + accented
  names now work (both were bugs, now fixed — Shelby IDs at 0.94).
- **Watch:** **face alone** is the weakest signal — trust a NAME most when it's *corroborated*
  (voice + face). A lone face match below 0.55 is reported as "unknown," never a guessed name.
- **Recall = detection, not naming:** every person/face is surfaced; a NAME is attached only when
  confident. Unknowns are trackable via `becky-cluster` ("Person A") until you name them once.

## For agents
JSON-in / JSON-out, exit-coded, offline. Chain the `becky-*` tools or drive `becky`. Never modify
source videos. `--bin <dir>` overrides where the `becky-*.exe` live; `--verbose` for progress;
`--json` (on `becky`) suppresses the headline for machine parsing.

---

# VIDEO CLIPPING — turning finished videos into vertical shorts

**Read this whole section before proposing ANY change to the shorts pipeline.** It exists because
the same conclusions kept getting re-researched, re-decided and then ignored. Everything below is
either measured on Jordan's own footage or is a direct instruction from him. Where something is
NOT built, it says so — do not claim it.

## CLIPPING vs EDITING — they are different jobs

Jordan's own distinction (2026-08-21), and it changes the defaults:

| | **CLIPPING** | **EDITING** |
|---|---|---|
| Input | footage that is **already edited** — a finished YouTube video, a cut master | **raw** footage, or a specific revision he asked for |
| The cuts | **INHERIT them.** The master's cut points are the edit; keep them | you are deciding the cuts |
| What changes | reframe to 9:16, tighten, caption, pick the window | anything he asked for |
| Tool | `becky-short` (this section) | `becky-cut`, `becky-edit`, VEGAS scripts |

**This section is CLIPPING.** The single most-ignored consequence: on already-edited footage you
do **not** run a silence threshold over it and re-cut. You preserve the existing cuts and tighten
them. `becky-short` does this automatically (`planShotSpans` + `--tighten`); an agent that
"improves" it by re-cutting on silence has broken it.

## The one call

```bat
REM the button Jordan actually uses - drag a video or a folder onto it
"Make Shorts.bat"
```

It runs three tools. Everything else is internal.

```bat
becky-moment.exe --transcript <video>  --top 10 --out moments.json     REM 1. which bits are worth posting
becky-hits.exe   --hits moments.json --folder <folder> --out reel.json REM 2. match them to the footage
becky-short.exe  --reel reel.json --outdir <folder>\shorts            REM 3. render the vertical shorts
```

Single clip, no reel:

```bat
becky-short.exe --video "clip.mp4" --start 22.08 --end 62.0 --out out.mp4
```

## What `becky-short` does inside one call

It is a **loop, not a line**. Nothing ships until a model has looked at the finished file.

```
  WATCH     Gemma-4 sees 25 frames + the transcript and CHOOSES the in/out points.
            Its answer REPLACES the proposed window. Then it shuts down.
     |
  CUTS      Preserves the cuts the footage already has; tightens each by 150ms.
            (Raw footage instead? becky-cut's dead-air spans are used.)
     |
  LADDER    Per shot: where does the 9:16 crop point? Nine rungs, below.
     |
  SPLICE    Keeps the seconds the pose tracker really held him; the ladder fills the rest.
     |
  CAPTIONS  Jordan's look, word-timed, burned in.
     |
  RENDER    ffmpeg: moving crop via sendcmd + captions.
     |
  CRITIC    Gemma-4 WATCHES THE RENDERED FILE. Wrong? It names what should be in
            frame -> that name becomes the grounding target -> re-frame -> RE-RENDER.
            Up to --critic-passes times (default 2).
```

## Every flag, and what it is for

`becky-short` (`--selftest` runs 54 offline checks; run it after any change):

| Flag | Default | What it does / when to touch it |
|---|---|---|
| `--video` / `--start` / `--end` / `--out` | — | single-clip mode |
| `--reel` / `--outdir` | — | render every clip in a becky-hits reel |
| `--aspect` | `9:16` | target aspect |
| `--watch` | `true` | **Gemma-4 chooses the in/out points.** Turning this off puts the transcript back in charge, which is what cut the payoff out of the mouse-trap prank. Only disable when the window is already known-good. |
| `--critic-passes` | `2` | how many times the model may watch the render and send it back. `0` = old feed-forward behaviour. |
| `--jumpcuts` | `true` | preserve existing cuts / cut dead air. `false` = one unbroken take. |
| `--tighten` | `0.15` | seconds trimmed **total** at each preserved cut. **This is Jordan's own measured rate (150ms/cut)**, not a silence threshold. |
| `--caption-style` | `jordan` | his measured look. `cli-cut` is the plain shipped look. |
| `--captions` | `true` | burn captions |
| `--sample-fps` | `0` | 0 = look for the subject on EVERY frame. Do not raise it; 8fps on 30fps footage is what made the crop lag. |
| `--max-gap` | `2.0` | seconds the subject may be undetected before the **pose path** is distrusted. **This does NOT refuse anything** — the ladder answers instead. |
| `--min-coverage` | `0.6` | how much of the window pose must see the subject before its path steers the crop. Same: not a refusal. |
| `--center` | `false` | force a static centre crop. The ONE sanctioned way to get a centre crop, because it is an explicit human instruction. |
| `--focal-point` | `false` | EXPERIMENTAL. Measured two spans better and two worse — a coin toss. Leave off. |
| `--review` | — | measure an already-rendered file (deterministic: faces, caption drift, ending). **Not the critic** — no model call. |
| `--verbose` | — | per-span framing decisions to stderr; the JSON note only carries the first few |

`becky-moment` — which bits are worth posting: `--min 6` / `--max 360` (a 6s–360s safety rail, **not**
a target length — "no hard time limit on a clip, context decides"), `--extend 20` (may run past
`--max` to finish a thought), `--judge` (second independent content pass), `--escalate` (ask the 12B
only about disputed moments), `--max-overlap 0.5`, `--no-face` / `--no-audio` to drop a signal.

`becky-hits` — moments to a reel: `--pad 0.5` lead/tail, `--window 4` fallback when a timestamp
matches no cue.

`becky-speaking` — who is talking: `--boxes` emits per-frame face geometry (that is what the
framing ladder's rung 0 steers with).

## The framing ladder — nine rungs, first confident answer wins

`cmd/becky-short/framing.go`. **It never refuses.** Every rung names itself in the output.

| # | Rung | What it is |
|---|---|---|
| 0 | **SPEAKER** | 2+ tracked faces and LR-ASD says which one is talking (`speakeraim.go` → `becky-speaking`). Silent on 1 face, 0 faces, a tie, or a POV shot. |
| 1 | **POSE** | MediaPipe body tracking, per frame. Best answer when it works. |
| 2 | **PAN** | A **steady** grounded box that MOVES becomes a camera move. |
| 3 | **AIM** | A **steady** grounded box that HOLDS becomes a still crop on it. |
| 4 | **FALCON** | A second, independent detector (ONNX, no torch) looking for a person. |
| 5 | **MOTION** | `internal/focal` — where the movement is, when nothing is nameable. |
| 6 | **HINT** | An **unstable** grounded sighting, held still. Never steers, never out-votes rung 4. |
| 7 | **INHERIT** | What the rest of this short settled on. Continuity is a real signal. |
| 8 | **CENTRE** | Last resort, and it labels itself a guess. |

**Rung 2/3 require `ground.Result.Stable`** — that is `ground.py`'s own contract (`seen >= 2 and
found_frac >= 0.5 and median_jump <= 0.25`), and of an unstable result it says in its own words:
*"treat as a HINT about which region matters, not as a camera path."* Ignoring that panned a short
75% across the frame chasing a Pikachu poster past the person at the desk.

## THE RULES THAT ARE LAW HERE

Each of these was a real failure, and each has been stated by Jordan more than once.

1. **A DETECTOR IS A SIGNAL, NEVER A VERDICT ON THE FOOTAGE.**
   *"tracking a subject does not determine if the clip is good or not... All these data points are
   to help becky conceptually understand what is happening in the video so it can make accurate
   decisions."* A failed detection may change **where the crop points** and nothing else. It may
   never shorten a clip, drop a span, or refuse a render. Concretely: never discard a whole pose
   path because one stretch is dead (`splice.go` keeps the tracked seconds); once a model has
   WATCHED, its out point stands (`deadtail.go`'s `shortWatched`).

2. **AN LLM MUST WATCH THE OUTPUT BEFORE IT SHIPS.**
   *"an LLM needs to verify all of that - we're not picking random dumb data points and rendering
   that shit; quickest way to get someone fired. I re-watch a video clip like 10 fucking times
   before I hit render."* `critic.go` + `internal/watch/critique.go`. A deterministic check over
   the output is **not** this — `--review` counts faces and cannot notice the thing in frame is a
   poster.

3. **NEVER LET THE MACHINE'S WRONG ANSWER BECOME PART OF THE QUESTION.**
   Two bugs, same shape, both measured 2026-08-21. Showing the critic becky's own framing note
   made it reply *"the colorful poster, WHICH THE CLIP IS ABOUT, is not visible"* and demand a
   re-frame onto the poster. And `aboutFromNote` searched too far along the `"; "`-joined note and
   picked up `grounded "colorful poster"` from further down it — so the **yardstick was the
   poster**. Guarded by `Verdict.Usable` + tests.

4. **EDITING IS ITERATIVE. QUALITY IS THE ONLY BUDGET.**
   *"I'm a world class video editor and I don't care if it takes an hour; if the edits look like
   shit, I can't use any of this."* Never list runtime as a weakness, never drop a model or a pass
   for being slow, never reject a model on a timing measured under the wrong conditions. Firing the
   same model up more than once at different steps is explicitly fine. The ONLY real waste is doing
   **identical** work twice for one answer — that is what `groundCache` and `speakerCache` prevent.

5. **ONE MODEL AT A TIME — a hardware fact, not a preference.** 8GB is a tested hard ceiling.
   Gemma-4 E4B is 4.2GB, Reka Edge Q4_K_M + mmproj is ~5.5GB; they do not co-exist. The watch pass
   closes before the ladder starts Reka; `closeGrounder()` runs before each critique and the
   grounder **restarts** afterwards (this is why it is not a `sync.Once`).

6. **Corroborate, then conclude.** One signal is a candidate; two agreeing is a conclusion.

## Jordan's edit standard, MEASURED (`research/jordan-edit-reverse-engineered.md`)

Reverse-engineered from his own vertical short against the master it was cut from (SIFT+RANSAC per
frame, InsightFace on all 915 frames). These are numbers, not taste:

- **He inherited the cuts** — 8 of them frame-exact from the master. He did not choose them.
- **He removes ~10% of the time, not half.** Aggressive silence-cutting is wrong for clipping.
- **His cuts land on WORDS, not on silence.**
- Face **height** median 24.3% of frame; **centre X** 49.6%; **centre Y 29.9%** (90% of frames put
  the face in the **upper 40%**).
- **8.3% of frames contain NO FACE AT ALL, deliberately.** Half his shots are not on the speaker.
- **He frames himself widest** (median face height: Shelby 32%, Allison 27%, Jordan 20%) — when the
  gesture is the point, he frames wide enough to hold the hands.
- **The camera is locked far more often than it moves**: 34% of frames move <2px; 16 of 22 shots
  change scale by less than ±15%. **A move is an event, not a default.**

## Models — what runs, when, and why that one

| Model | Job | Where |
|---|---|---|
| **Gemma-4 E4B QAT** (4.2GB) | the JUDGE: what the clip is, in/out points, and the critic that watches the render | `internal/watch` |
| **Reka Edge 2603 Q4_K_M** + Q8_0 mmproj (~5.5GB) | the EYE: grounded boxes, "where is X" | `internal/ground` → `pyhelpers/ground.py` |
| **MediaPipe Pose** (heavy) | per-frame body tracking | `internal/crop` → `pyhelpers/crop_path.py` |
| **Falcon-Perception** (0.6B ONNX, no torch) | second, independent person detector | `falconaim.go` |
| **LR-ASD** + InsightFace/ArcFace | who is speaking, and whose face is whose | `cmd/becky-speaking` |
| **Parakeet** ASR | word-level transcript | `internal/transcribex` |

Gemma says **WHAT**; Reka says **WHERE**. That split is the reason both exist
(`research/reka-edge-vs-gemma4.md`).

## What is NOT built — do not claim these

- **becky-diarize is not crossed with LR-ASD.** Two of the three speaker signals are joined (lip
  motion + face identity). Diarize's labels are anonymous (`SPEAKER_00`) and binding them to face
  tracks needs an alignment pass that does not exist.
- **becky can move a crop, not WIDEN one.** Measured 2026-08-21: the critic asked for *"the door
  and the speaker"* — two things in one frame, i.e. zoom out — and the re-render came out
  **byte-identical**. `fileFingerprint` now detects that and says so instead of claiming a fix.
  **Giving the critic the power to change the crop WIDTH is the highest-value next piece of work.**
- **The grounding probe names ONE subject for a whole window**, not per shot.
- **`becky-moment` still picks windows from the transcript alone** — blind to physical action. The
  watch pass corrects it inside `becky-short`, which is enough for the button, but the ranker is
  still blind.
- **Marlin-2B is unverified here.** A GGUF pair exists (`jadeonrails/marlin-2b-gguf`, text tower +
  mmproj) with a documented `llama-mtmd-cli --video` path. Its `find()` answers "the first frame in
  which it starts to move" in one call, which nothing else here does.

## Traps that already cost hours

- **`drawtext` with no `fontfile=` HARD-CRASHES one of the five ffmpeg builds on this PC**
  (`C:\Program Files\ffmpeg\...\bin`) with `0xc0000005`, after printing a Fontconfig error. Which
  ffmpeg becky gets is `exec.LookPath`, so it depends on how she was launched. **Always name the
  font.**
- **Gemma-4 QAT has a hidden `reasoning_content` channel** that eats the token budget before any
  answer appears. At `max_tokens=100` it returns `finish_reason=length` with `content:""` — a model
  that looks broken and is not. Give it 2000 and read `content`.
- **Burn timestamps INTO each contact-sheet tile**, and `drawtext` must come AFTER `fps=` and
  `scale=` (before `fps` the label is drawn at full res then shrunk to nothing).
- **Reka needs `--reasoning off`** — its chat template ships `thinking = 1` and free-form answers
  come back as meta-commentary. Its detection output has **mismatched tags**; parse tolerantly with
  a regex, never an XML parser. Coordinates are **percentages**, not pixels.
- **`subs.ShortOptions()` vs `subs.DefaultOptions()`.** `MaxHold` (a caption may not outlive its
  last word by >1.25s) belongs to becky-short, which captions a RAW window with silences in it.
  `DefaultOptions` is cli-cut's look and its captions are **contiguous** — putting MaxHold there
  silently put holes in `cmd/clip`'s captions.
- **`n_ubatch` is 512 and asserts (crashes the server) above it.** One media item must fit one
  micro-batch.
- **Jordan's footage is rotated portrait** — 1920x1080 coded with a -90 display matrix. Anything
  picking an output SIZE must use display dims, not coded dims.

## The research behind all of this — READ BEFORE RE-DECIDING

| File | The conclusion it already reached |
|---|---|
| `research/jordan-edit-reverse-engineered.md` | his measured edit standard (the numbers above) |
| `research/shorts-gap-decisions.md` | what 21 reference projects do that becky does not — build it, or say why not |
| `research/shorts-models.md` | the six-stage chain; becky already has the better model at 3 stages and nothing at 5 |
| `research/reka-edge-vs-gemma4.md` | why both models exist; Reka's measured limits (per-frame grounding of a SMALL target is NOT trustworthy) |
| `research/model-marlin-2b-TESTED.md` | Marlin's real capability, the CPU-only timing that is **not** a verdict, and the GGUF pair |
| `research/paper-2509.10761.md` | EditDuet's Editor/Critic loop — the shape `critic.go` implements |
| `research/paper-2512.14698.md` | TimeLens: interleaved raw-timestamp encoding beats the alternatives |
| `research/iphone-ai-video-sweep.md` | 1–2 FPS sampling loses micro-expressions; a payoff frame is a 1–3 frame window |
| `research/repo-*.md` (22 files) | per-project notes on every reference short-form pipeline |

## Building a new clipping pipeline on this

Reuse, do not rebuild:

- **A new "watch and decide" question** → add a method to `internal/watch` (it already owns the
  Gemma server lifecycle, the contact sheet, the font fix and the reasoning-channel workaround).
- **A new "where is X" question** → `internal/ground` with `Options.Target`.
- **A new framing source** → a rung in `framing.go`. Return `(rects, note, ok)`; return `ok=false`
  to fall through. Never refuse.
- **A new whole-window pass** → follow `groundCache` / `speakerCache`: compute once per short,
  slice per span.
- **A new quality check on the output** → a `Critique`-shaped call. It must NAME what to do
  instead, or it is not actionable.
- **Anything that shells out to a sibling `becky-*`** → copy `resolveCutBin`'s order
  (env var → beside the exe → PATH → degrade to `("", false)`).

Every new tool: JSON in → JSON out → exit code, offline, deterministic, degrades with a typed note
instead of crashing. After any change: `go build ./... && go test ./... && go run ./cmd/becky-short
--selftest` **and** `build-all-tools.bat`.

# ROUGH CUT — raw takes to a populated Vegas Pro timeline, one dumb call

Read this before touching `cmd/roughcut` or `vegas/BeckyRoughCut.cs`. Everything below was
measured on Jordan's hj-fbi-recap session (16 sources, 2:25:25 raw, Rode mic recorded ~35 dB too
quiet, clap tests clipping at 0 dB) on 2026-08-24, or is his direct instruction.

A rough cut is an EDIT, not an inventory (canon: `"X:\Videos\2026\08_august\23_hj-fbi-recap\WE_TRIED.md" in the session folder): it plays
start to finish with nothing in it that isn't content. 80% of its cuts survive to the final edit.
An "AI-slop cut" that makes the editor touch every cut is WORSE than no output.

[EDIT FROM JORDAN]; My definition of a rough-cut is as follows (because the nuances and logic MATTERS in a way that the above summary failed to communicate, repeatedly);
A "rough-cut" is not the same as an "AI-slop" cut. When a human intern produces a rough-cut, every cut was made at meaningful and intentional zero-crossing points (generally relative to speech); 80% of those cuts will never need to be adjusted and will likely remain in the final output. Watching a smooth, thoughful rough-cut gives the Editor a feel for the tempo and pacing, and allows the human Editor to get immersed in the storyline and delivery in realtime. Anything that stands out as requiring improvement is then adjusted to taste, but the fundamental edit is complete. AI-slop, by contrast, makes sloppy, haphazard cuts that completely ignore video editing fundamentals. Generally it cuts off words and phrases, leaves excess silence, the narrative does not yet make logical sense (extra words or phrases left in), and are not cut to zero-crossing points (and the ones that are, lack the precision to trim out non-speech before a line is delivered, such as the speaker adjusting their position before delivering a line - which a human editor then has to go trim). This does not provide a cohesive viewing experience and prevents the human Editor from getting a feel for the pacing and the narrative. Virtually every cut that is made in this way requires a human to adjust (even if only by one or two video frames). This is worse than producing no output at all - because not only is the video editor STILL touching every cut on the timeline, you've also wasted the human's time they spent by watching the incomplete work. A proficient video editor can produce a proper rough-cut in significantly less time than it takes to fix a bad one.

## The one call

```bat
becky-roughcut "X:\Videos\...\session-folder" -markers markers.json -launch-vegas -verbose
```

That is the whole interaction. It orders the takes by EMBEDDED `creation_time` (filesystem times
are lies), cuts silence and re-takes, places quote markers, writes `cut.yaml` / `library.yaml` /
`qa.json` / `vegas_cut.json` into `_roughcut\`, and with `-launch-vegas` starts Vegas Pro 18
headless (`vegas180.exe -SCRIPT:vegas\BeckyRoughCut.cs`), which builds the timeline, saves
`rough_cut.veg` and quits. Jordan walks away; when he comes back the timeline is populated.
Dragging `vegas_cut.json` onto `import-to-vegas.bat` does the same Vegas step by hand.

## Every flag, and what it is for

| Flag | Default | What it does / when to touch it |
|---|---|---|
| `-pause` | 0.45 | a quiet stretch this long (s) is a jump cut. Jordan: anything longer than conversational pace goes; breaths stay. |
| `-vad-threshold` / `-vad-speech-pct` | 0.5 / 0 | opt-in Silero junk-keep filter; **0 = off** — Silero proved untrustworthy on quiet-mic footage (finds nothing raw, everything boosted). |
| `-markers` | none | `markers.json` = `[{source, source_time, title, kind}]`; quote lead-ins in SOURCE time, mapped onto the timeline. A lead-in that got cut away lands at the end, suffixed `[lead-in was cut - review]`. |
| `-launch-vegas` | off | the unattended Vegas build. **One heavy media app at a time** — never with Resolve open. |
| `-out` | `<dir>\_roughcut` | artifact dir. |
| `-quotes` | none | `quotes_verified.json` = `[{q, source, in, out}]`; verified quote clips inserted SEQUENTIALLY at their `-markers` marker (main edit stops, quote plays, main resumes) — never simultaneously. |
| `-watch` | off | **STANDALONE mode, run it separately.** An LLM (Gemma-4) watches every merged block of an EXISTING `vegas_cut.json` and writes `watch_report.json` (PASS/FLAG + reason per block, review-only). Needs the GPU free of any other model — never alongside an LR-ASD speaking sweep on an 8GB card. See `watchpass.go`. Jordan, 2026-08-24: this "watch everything" rule is a `becky-clip` (short-form, jump-cut-heavy) rule that does **not** transfer to roughcut's long-form documentary use case — prefer `-triage-markers` below for roughcut's actual review flags. |
| `-triage-markers` | off | **STANDALONE mode, run it separately.** An LLM (Gemma-4) reviews every pending `CHECK:`/`RETAKE?` marker from an EXISTING run (`pending_markers.json`), watching a window around each one (padding before/after — Jordan: "it will likely need to know what comes before and after the marker with the question"), and answers the SPECIFIC concern already written into that marker. A confidently-resolved marker is dropped from `vegas_cut.json` before Jordan ever sees it; one the model can't resolve stays, annotated with its own read (`[gemma4: ...]`). Never cuts anything — same GPU constraint as `-watch`. See `triage.go`. |

## Corroboration — becky-clip's signals, actually consumed (2026-08-24 night)

`SKILL.md`'s own VIDEO CLIPPING rules ("an LLM must watch the output before it ships",
"corroborate then conclude") were built for `becky-short` and had never reached
`becky-roughcut` — every decision was word-timing/audio only, with real LR-ASD speaking data
sitting computed and unused. Two additions, both REVIEW-ONLY (a detector is a signal, never a
verdict — neither of these ever cuts, shortens, or auto-fixes anything):

- **`speakingCorroboration`** (`dossier.go`) cross-checks every kept span with LR-ASD active-
  speaker data (`%TEMP%\keepspeaking\*.json`, `becky-speaking` output — `loadSpeaking` already
  globs for it). A span with real audio/transcript content but no confidently-visible speaker
  raises a `CHECK:` marker on the timeline for Jordan to look at.
- **`--watch`** (above) is the `becky-short`-style critic pass, ported. Run it once real
  coverage exists and the GPU is free.
- **`--triage-markers`** (`triage.go`, 2026-08-24 later that night) is the direct fix for
  Jordan's follow-up correction: *"There is no reason to ask me to watch the timeline choice
  if gemma4 has not already done so... it ABSOLUTELY can watch up to 30 seconds at a time."*
  Unlike `--watch`'s blanket pass over every kept block, this only re-examines spans someone
  ALREADY flagged (with context padding before/after), and answers that marker's own specific
  question. Still review-only: a marker is only ever dropped on a confident model answer, never
  auto-cut, and a marker the model can't resolve is kept, annotated with the model's own read
  so Jordan sees it already reviewed rather than a blank question.
- **`--narrative-trim --target-minutes N`** (`narrativetrim.go`, 2026-08-25) is for a cut that's
  finished and triaged but still too long to review — dead-air removal can't touch that, it only
  removes spans with nothing in them. Gemma-4 reads the whole remaining narration in ~30s beats
  and cuts only the ones it's confident are redundant/tangential (never a new fact), with a
  HARD code-level stop once the removed total meets the actual deficit — measured proof this
  matters: a prompt-only version with no stop cut 167 of 191 beats (86.1min -> 15.5min, 3x more
  than needed) before the stop was added. Never touches quote clips. Every cut logged to
  `narrative_trim.json` with its reason.

Full story, why it took a second pass to find this gap, and the exact bug that silently ate
every dynamically-generated marker until tonight: `HANDOFF-ROUGHCUT-2026-08-24-NIGHT.md` §8-10.

## What happens inside the call (the measured recipe)

1. **calibrate** — per clip, the 0.4s RMS envelope's p90 is the speaker, p10 the room. Linear gain
   puts speech at -20 dB (clamped 0..+45, so clipping clap-test clips get NO boost). No dynamic
   loudnorm: a loudness pump raises room tone inside the very pauses we cut. The same gains are
   applied in the delivered Vegas project as ONE audio track per clip (track Volume), so the
   timeline is actually audible.
2. **highpass 80 Hz + silencedetect** on the boosted copy, floor = room+3 dB, `d=-pause`.
   Duration is the ONLY thing that separates a word gap from a thinking pause on this footage.
3. **margins 0.2s/0.25s** — measured: detector onsets run up to 0.2s late vs the true word;
   0.2s of room tone is inaudible, a clipped consonant is not. Single-word ad-libs get
   0.5/0.7 (delivered with visual emphasis on purpose).
4. **re-takes** (`badtake.go`) — several signals agreeing, never one hard rule: same statement
   (>=4-word run, may begin several words in) + gather-himself pause (>=1.5s) + earlier attempt
   abandoned mid-flight + later take fuller. Score >=6 CUTS to a fixpoint; two clean alternate
   phrasings become `RETAKE?` markers and Jordan picks in the morning.
5. **word-anchored edge + interior trim** (`refineWordEdges` / `splitOnWordGaps` in `detect.go`,
   2026-08-24 night) — a keep's lead-in/tail trims to the first/last transcript WORD that
   actually overlaps it (no fixed window, no ceiling: this is Jordan's exact complaint, "clips
   that are mostly room noise where I'm just adjusting myself preparing to deliver the line" —
   a multi-second lead-in trims exactly as cleanly as a half-second one), and any word-to-word
   gap inside a keep longer than `-pause` gets split, because a Parakeet cue's own span can
   contain a real silence far longer than the cue-to-cue merge step ever sees
   (`buttercut_proposal.md`: *"a '13s cue' that contained a 6s silence"*). Overlap tests are
   INCLUSIVE — a real fraction of Parakeet word timestamps are literal zero-duration points
   (`{"start":132,"end":132}`), and a strict `>` silently skips the true first word and clips it.
6. **zero-crossing snap** (`zcross.go`) on the ORIGINAL audio at a floor relative to the clip's
   room tone: every boundary moves to the nearest quiet crossing within +/-20ms, else extends to
   a quiet pocket within 0.35s, else stays. No cut pops.
7. **transcript rescue AFTER the snaps and retake cuts** (they erode padding), then snap again:
   a 3+ word Parakeet cue whose first/last 0.25s is not inside a keep comes back with 0.6/0.5
   padding, trimmed to its OWN real words the same way as step 5 before it's added (never
   re-running step 5 over the WHOLE keep list here — that regressed the QA gate from 1 dropped
   cue to 7 by letting this step's zero-crossing snap drift an already-correct boundary past a
   real word; see `HANDOFF-ROUGHCUT-2026-08-24-NIGHT.md` §5.4). The transcript drives; the audio
   arbitrates.
8. **QA gate** — every 3+ word cue must still have its words on the timeline (head and tail in
   keeps; a mid-cue jump cut is fine). `qa.json` lists every dropped cue and every retake cue
   cut, for audit. **0 dropped is the ship condition** (a single occurrence tolerated when the
   cause is upstream ASR data corruption, not a cutting bug — see the night-session handoff).
9. **quotes** — the reviewed reel's clips are pre-cut to small mp4s (`_roughcut\quotes\`), each
   window manually checked against the corpus SRT (full quote present, repeated yelling runs
   extended to the whole run), and placed on `Quotes (video/audio)` tracks above the main edit
   at their marker positions. Multi-candidate quotes sit sequentially at the marker for Jordan
   to pick.

Measured result (iterated 2026-08-24, corrected 2026-08-24 night — see below): 2:25:25 raw ->
**~81 min** rough cut, 1226 main events, 25 quote overlays, 36 markers, 16 regions, 36 retake
cues cut, 1 dropped cue (a corrupted single-word ASR timestamp, self-documented in `qa.json` —
not a cutting bug), **2.5 s total in 2 gaps >=1.0s across the whole assembled cut (0.05%)**.
Verified by reading the actual `words.json` coverage of every kept span (not a dB-threshold
silencedetect pass — see why below), by Vegas headless read-back (`tracks: 4`), and by opening
the built project in Vegas Pro and visually inspecting it.

**On the earlier "3.0 s / 0.0%" claim (retracted):** that number came from a self-check whose
silence threshold (`room_db + 3`, from the zero-crossing-snap calibration — measured -77 to
-92 dBFS on this footage's raw audio) was so far below anything audible that it could not have
fired on real room noise; it measured "is this literally digital silence," not "is this room
noise." It is retracted, not merely superseded — the room-noise complaint it claimed to close
was, per Jordan's own follow-up feedback, still very much open at the time it was written.
**Any tool built to self-verify silence/room-noise on this footage must calibrate its
threshold against the clip's SPEECH level (from `calibrate()`) or against word-timing coverage
— never against the zero-crossing-snap tolerance, which is calibrated for a different job
entirely and will always under-fire.**

## Why not auto-editor / becky-cut / loudnorm here (the traps, measured)

**Read this alongside Jordan's own correction in `WE_TRIED.md`** ("becky-cut is the only viable
solution... it cuts at exactly zero-crossing points... the volume threshold needs to be
adjusted, that is our problem") before concluding "volume-based detection doesn't work here" —
it does, uncalibrated. The measurement below is real and stands: NAIVE becky-cut/auto-editor
(one fixed threshold for the whole file) fails on this footage. `calibrate()`'s per-clip
p90/p10-derived gain + floor is NOT that — it is the calibrated version Jordan asked for, and it
IS the primary silence signal here (`silencedetect` in step 2 of the recipe above). This
session's fixes (word-anchored trim + interior-gap split) are a SECOND, corroborating signal on
top of it, using `words.json` where the calibrated audio signal alone still misses things on
this specific noisy-but-quiet-room footage — not a replacement for it, and not a return to
transcript-cue-timestamps-as-ground-truth (which Jordan separately, explicitly rejected).

**Jordan re-litigated this 2026-08-24 night** (`HANDOFF-ROUGHCUT-2026-08-24-NIGHT.md` §7's
edit): the "30% kept / shredded sentences" number was never confirmed by his own eyes, and he's
right that a claim graded only by the pipeline itself is exactly the failure mode to distrust.
Worth being precise about which parts of this ARE and are NOT that failure mode: the "30%
kept, shredded" number is a plain word-count comparison against `becky-cut`'s OWN naive output
(deterministic, not a model's opinion) — that measurement stands. The **2:2:5:25 -> 81min /
0.05%-gaps** result reported above is likewise a deterministic `words.json`-coverage count, not
an AI's self-grade. What is still **only Claude's read, not Jordan's**: the visual screenshot
inspection of the Vegas timeline. **Nobody has watched or listened to the current build with
human ears yet** — that is the one honest gap left, and it is waiting on Jordan, not on more
engineering. His actual ask — "use auto-editor per-clip / adaptively, not one dumb global
threshold" — is what `calibrate()` already does (a fresh p90/p10 threshold computed per CLIP,
not one number for the whole session); the naive-global-threshold measurement above describes
`becky-cut` run WITHOUT that calibration, a different configuration from what ships. This is a
terminology gap, not an unresolved architecture fork — no rip-and-replace is needed unless
Jordan's own review of the current (already-open) build says otherwise.

**Also tested, also grounded in real measurement, not guessed at:** Jordan's follow-up
suggestion — push the ANALYSIS gain further (~12dB) with a real limiter instead of a compressor
(compressor already measured, separately, to crush ~20dB of dynamic range and was rejected) —
does **not** improve speech/room separation here. Measured on two independent real clips
(`HJOC7106`, `SNOW_20260823122254`; script: `audio_gain_limiter_test.py`, kept for reference):
whole-window p90/p10 separation went from 50.9dB (current gain+softclip) to 45.4dB (current+12dB
via `alimiter`) on clip 1, and 44.9dB to 42.0dB on clip 2 — a real limiter engaging on louder
peaks pulls the loud end down MORE than the already-quiet room tone (which stays under the
limiter's threshold and just scales linearly with the extra gain), so pushing gain further
narrows the gap the detector needs, it does not widen it. Word-boundary onset jumps (the
detection-relevant measure, not just overall stats) told the same story across 15 real onsets
tested: current was equal-or-better on every one, clearly better on the ones with a real jump
(12.3dB->10.5dB, 7.5dB->3.6dB, 19.0dB->19.0dB, 9.2dB->9.2dB). **The mechanism, not just the
number:** pure linear gain cannot change the RELATIVE dB gap between speech and room tone at
all (it shifts both together) — only a nonlinearity can, and a limiter's nonlinearity acts on
the LOUD end, which is the wrong end to compress for this purpose. Jordan's instinct is correct
for its actual home — his own manual mixing of the DELIVERED audio in Vegas, where a human
picks the threshold by ear and a few smashed peaks are an acceptable, deliberate trade — it just
doesn't transfer to the unattended ANALYSIS chain, whose only job is maximizing that gap.
**Left alone, deliberately:** wiring this gain+limiter treatment onto the DELIVERED Vegas audio
track (as an actual FX plugin, separate from the analysis chain) is still open — Jordan
described it as something he dials in "manually... on a per clip basis," so it stays a manual
mixing step in Vegas rather than something this tool should auto-apply.

- becky-cut/auto-editor on this audio kept 30% and shredded sentences; at its -50 dB FLOOR it
  still kept 34%, and at -70 dB 41% — a volume threshold cannot work when speech RMS is -55 dBFS.
- Silero VAD on the raw level found NOTHING; on boosted audio it marks 40-second pauses as speech.
  It is only the junk-keep sanity layer, never the detector.
- The clap test puts true peak at -1.4 dB, so any loudness-derived gain (LUFS) is poisoned; the
  p90/p10 envelope is clap-proof (one clap is one window in hundreds).
- **Vegas Pro 18 cannot import our FCP7 XML** (`Fcp7Importer.ImportFrameRate` NPE) and cannot
  import OTIO. The ONLY reliable seam is the scripting API: `vegas180.exe -SCRIPT:<file.cs>` with
  the input passed as an ENV var (the `Vegas.ScriptArgs` property does not exist in VP18).
- A `.cs` that fails to COMPILE pops the same "error loading the project file" dialog as a bad
  project — the Details button shows the compiler error. Verify scripts headless with
  `vegas/BeckyVerifyProject.cs` (writes `<file>.veg.verify.txt`) before claiming a delivery works.
  `Timecode` has no `.Seconds` in VP18; use `.Nanos * 1e-7`.
- `AudioTrack.Volume` is a **float**: assigning `Math.Pow(...)` (double) is a COMPILE error, which
  manifests as that same misleading dialog and silently blocks the whole headless build.
- Per-event `Normalize` makes Vegas scan every event's audio on the UI thread — the build "hangs"
  for tens of minutes. One audio track per clip with the measured gain is instant and better.
- Force-killing Vegas leaves crash-recovery state that pops a blocking dialog on every later
  start (and blocks `-SCRIPT`). Delete `AppData\Local\Vegas Pro\18.0\*.restored.veg`, or better:
  never kill — let `vegas.Exit()` end it.
- A failed ffmpeg cut leaves a PARTIAL mp4 that `os.path.exists` then trusts forever — probe
  every produced clip (video+audio streams, duration) before handing it to Vegas, or the project
  loads with media-offline dialogs. Some corpus streams need `format=yuv420p,scale=trunc(iw/2)*2:trunc(ih/2)*2`
  before nvenc accepts them.
- Multi-hour corpus files opened via `new Media(path)` make Vegas index them synchronously for
  many minutes. Pre-cut quote clips to small mp4s first (which is also the documented workflow).

## Rules that are law here

- Sources are READ-ONLY; everything is additive artifacts + a .veg.
- Excessive mid-sentence silence (longer than conversational pace) is a jump cut and MUST go;
  breath pauses inside a delivered sentence stay.
- Obvious re-start chains are cut; clean alternates are markers. "Don't make me do the obvious,
  tedious work" — but "if they are clean takes I can decide which to keep in the morning."
- One heavy media app at a time; detection passes first, review app after.
- The QA gate is honesty: report what was cut and what was dropped, never a duration-% vanity
  number.
