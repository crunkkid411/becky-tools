# becky-tools — forensic video toolkit — **START HERE**

This is THE one file for *using* becky-tools. Agents and humans: read this; ignore the other
scattered `.md` (PROGRESS / OVERNIGHT / *-SPEC / NOTE-* are build & working notes, not usage).

## What it is
Offline command-line tools that ingest a video/audio file and tell you **WHO is in it, WHAT is
said (with timestamps), WHAT happens on screen, and WHERE** — for human-reviewed investigation.
Everything runs locally; nothing is uploaded. It does **not** conclude guilt — but it DOES reach
**confident, corroborated conclusions** when multiple signals agree (voice + face → "it's Shelby"),
instead of dumping maybes for you to sort. A lone weak signal stays "unknown." (See
`FORENSIC-OUTPUT-PHILOSOPHY.md`.)

## Where it is
- Tools: `X:\AI-2\becky-tools\becky-go\bin\becky-*.exe` (24 binaries; new: `ocr`, `cluster`, `motion`).
- Friendly entry point: **`becky.exe`** (the orchestrator). Call by full path or add `bin\` to PATH.
  (`becky-ask` — a double-click chat front-door — is being added; see `SPEC-BECKY-ASK.md`.)
- The case "knowledge base" of known people (built from the wiki): `X:\AI-2\becky-tools\becky-go\kb-final`
  (layout: `voice-prints/<Name>/*.wav`, `face-prints/<Name>/*.jpg`). John, Shelby, Hair Jordan enrolled.
- Search commands (`find`, `index`, `corroborate`) need the embedding server running once:
  `X:\AI-2\becky-tools\start-embed-server.bat`. Transcribe/diarize/identify/enroll do **not**.

## ARCHITECTURE — becky is SELF-ORCHESTRATING (Jordan, 2026-06-26; the load-bearing decision)

> This file is for the agent **BUILDING** becky-tools, not for the forensic agent **USING** them. The
> forensic agent carries a mountain of legal context and must spend **zero** of it on becky internals.

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
   claims (the models live on the PC);
4. returning `orchestrate.Resolve(...)` — the final corroborated output. The forensic agent sees only this.
The engine + its rules are cloud-built and proven; steps 1–4 are the local model wiring.

**Two protocols are already enforced in RUNNING tools (the proven pattern — extend it, don't reinvent):**
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
frames+audio with Gemma-4 and returns per-frame observations in ~30s — the capability is real; use it.)

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
windows that waste a human's review time are a TOOL-USE FAILURE, not a near-miss.

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

- **`becky-vision`** — describe / read text off ONE still image. Default = the fast LFM2.5-VL **450M**
  via **`llama-mtmd-cli.exe`** (image-only, deterministic). Add **`--gemma`** to instead run the strong
  Gemma-4 on the SAME still via `llama-server` — use it for fine detail the tiny model gets wrong.
- **`becky-validate`** — Gemma-4 **audio-visual** pass over a short VIDEO CLIP via **`llama-server.exe`**
  (it ffmpeg-samples frames + 16 kHz mono audio, then asks cross-modal questions). This is the ONLY tool
  that understands AUDIO, and the one that WATCHES a segment. (Do not point the default LFM path
  `llama-mtmd-cli` at Gemma — it hard-crashes 0xC0000409; that is why `--gemma` uses `llama-server`.)

### Models on disk (full paths)
| Model | Role | GGUF | mmproj |
|---|---|---|---|
| LFM2.5-VL **450M** (default for `becky-vision`) | fastest still-image describe/OCR | `X:\AI-2\becky-tools\models\lfm2.5-vl-450m\LFM2.5-VL-450M-Q8_0.gguf` | `…\mmproj-LFM2.5-VL-450m-Q8_0.gguf` |
| LFM2.5-VL **1.6B** | higher-quality still image | `X:\AI-2\becky-tools\models\lfm2.5-vl-1.6b\LFM2.5-VL-1.6B-Q8_0.gguf` | `…\mmproj-LFM2.5-VL-1.6b-Q8_0.gguf` |
| **Qwen3-4B-Instruct** (text/reason; handles images via a VL mmproj if supplied) | text reasoning helper | `X:\AI-2\becky-tools\models\Qwen3-4B-Instruct-2507-Q4_K_M.gguf` | (none bundled) |
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
