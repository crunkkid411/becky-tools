# research/qwen38-max-video.md — Qwen3.8-Max as becky's frontier eyes, paid for with Qoder credits

> Deep research, 2026-08-17, cloud agent. Question from Jordan: *"the ability to ingest
> 100 hours of livestream video and put it into a searchable database caught my eye — I pay
> for Qoder and it's 90-99% discounted. How can I be using this?"*
>
> Method per `STANDARDS-ENGINEERING.md` research-depth + `SPEC-DEEP-RESEARCH.md` discipline:
> a claim is **corroborated** only when ≥2 independent sources agree; a single source is a
> **candidate**; arithmetic built on published medians is labelled **estimate**, not fact.
> Sources at the bottom, numbered, every claim tied to one.

---

## 0. The bottom line, in five sentences

**Yes — use it, but not the way the press release implies, and not at the discount you
remember.** Qwen3.8-Max is a genuine frontier vision model (#2 on Vision Arena) reachable
from a CLI you already pay for, and it is the first credible answer to the wall
`SPEC-VIDEO-ANALYSIS.md` §2 hit in June: *there is no local video-LLM in 8 GB that can do
semantic review*. **But the "100-hour livestream" capability is not one API call** — it is
chunk-index-retrieve, and the chunking/indexing half is **becky's existing
`becky-embed` + `becky-search`**, not something Qwen hands you. **And the 90–99% discount
expired on 3 August**; the live rate for Qwen3.8-Max is 50% off, off-peak only. The actual
"almost free powerhouse" today is **Qwen3.7-Plus at 0.04x off-peak (96% off)** for the bulk
pass, with Qwen3.8-Max at 0.25x reserved for the hard calls — which is becky's
cheap-then-escalate doctrine landing exactly on the price ladder.

Two corrections to the premise, up front, because they change the plan:

| You believed | Reality (verified) | Consequence |
|---|---|---|
| Qoder gives 90–99% off Qwen3.8-Max | That was the **Preview campaign, 19 Jul → 3 Aug 2026** [7]. Live rate: **0.5x standard, 0.25x off-peak** (50% off), off-peak = **14:00–00:00 UTC** [8] | Budget ~5x higher than you assumed for Max. Use 3.7-Plus (0.04x) for volume. |
| It ingests 100 h of video in one go | Context is **1M tokens** [2][3]. 100 h at 720p ≈ **32M visual tokens** (§3) — 30x over. The "video memory graph" is a **product**-level index [4][5] | becky must chunk + index. becky **already has** that layer. |

---

## 1. What the model actually is (corroborated)

| Property | Value | Source |
|---|---|---|
| Architecture | 2.4T params, **95B active**, sparse MoE | [1][2] |
| Context | **1,000,000 tokens**; max input 991,808 (983,616 thinking) | [3] |
| Max output | 131,072 tokens; chain-of-thought up to 262,144 | [3] |
| Modalities | **in: text + image + video** · out: text only | [3] |
| Ranking | **#2 Vision Arena**, #5 Text Arena, #4 Frontend Code Arena | [1] |
| API pricing (intl) | **$2.00 / M input, $6.00 / M output**, implicit cache $0.25 / M | [2] |
| API pricing (CN) | ¥12 / ¥36 per M; **batch ¥6 / ¥18**; explicit cache create ¥15, read ¥1 | [3] |
| Regions | Beijing, **Singapore**, Frankfurt, Virginia, Tokyo | [3] |
| Interfaces | OpenAI-compatible, DashScope, **and Anthropic-compatible** | [2] |
| Announced | 3 August 2026 | [1][6] |

**Open weights — and why they don't help this machine.** Alibaba released `Qwen3.8-27B`
and `-27B-FP8` (Apache-2.0, `image-text-to-text` — i.e. the *vision* model) on 5/13 Aug, and
`Qwen3.8-2.4T-A95B` on 8 Aug [13]. Note the split: **the 2.4T flagship weights are
text-only**; the open *vision* model is the 27B. A 27B VL at Q4 is ~16 GB — the RTX 3070
Laptop has **8 GB** (`SPEC-OCR.md`, `research/gemma4-qat-upgrade.md`). So local is out for
this class, and `SPEC-VIDEO-ANALYSIS.md` §2's conclusion stands unchanged: **frontier vision
is a hosted-only capability for us.** That is precisely why the Qoder subscription matters.

### 1a. Video input mechanics (corroborated, one gap)

- **Two input modes** [10][11]: pass the video itself (`video_url` OpenAI-style / `video`
  DashScope) and let the service extract frames; **or pass a list of pre-extracted image
  frames** with an `fps` field telling the model their spacing.
- `fps` range **0.1–10.0, default 2.0**; `max_frames` exists **only in the DashScope SDK**
  and evenly re-samples when exceeded [10].
- **Token math**: Qwen3-VL-family compression is **32** — each 32×32 px block is one visual
  token — plus **2× temporal compression for true video**; per-video visual budget is
  tunable 256–16,384 tokens [11].
- **Duration limits**: Model Studio's vision docs give **2 hours / 2 GB** for recent models
  (`qwen3.7-plus`, `qwen3.6-*`, `qwen3.5-*`) and 1 hour for `qwen3-vl-plus/flash` [10]. A
  secondary source states Qwen3.8-Max takes **up to 2 h / 2 GB and as many as 64 videos**
  [2] — the official model card itself does **not** state video limits [3].
  → **CANDIDATE, not fact.** Verify with one real call before designing around 64 videos.

**The mode that matters for becky is the image-list mode.** becky already extracts frames
deterministically (`internal/avlm`, `becky-motion`, `becky-clip`); feeding *our* chosen
frames keeps frame selection in becky's deterministic layer where it belongs, and it is the
only mode that works through Qoder (§4).

---

## 2. The "100-hour livestream" claim, deflated honestly

The claim is real but it is **product marketing for an indexing pipeline**, not an API
capability. What the sources actually say: the model can "organize people, events,
timestamps, and scenes into a **video memory graph**, continuously building connections
across long time spans to reconstruct event progressions, character relationships, and
critical moments" [4][5], turning long footage into "a searchable, traceable, and
interactive knowledge structure" [5]. Delivery vehicle: **QwenWork**, the workplace agent
platform in public beta since 2 Aug — **China-only website**, subscription + credits, four
model tiers [12].

Do the arithmetic against the 1M context (§3 below) and the shape is forced: **something
must chunk the footage, describe each chunk, embed the descriptions, and retrieve the
relevant ones on demand.** That something is a pipeline around the model, not the model.

**This is the single most important finding in this document, and it is good news.** becky
already owns every piece of that pipeline:

| Video-memory-graph part | becky's existing part | State |
|---|---|---|
| Chunk long footage | `becky-clip`, `internal/avlm` frame sampling | built |
| Transcribe + timestamp | `becky-transcribe`, `becky-diarize` | built |
| Who is present | `becky-identify`, `becky-presence`, `internal/facetrack` | built |
| Embed + store | `becky-embed` (Qwen3-Embedding-0.6B, 1024-d) + `beckydb` sqlite-vec | built |
| Retrieve | `becky-search` (FTS5 + vec + OCR **RRF**) | built |
| **Describe what is SEEN in a chunk** | `becky-validate` (Gemma-4 E4B/12B, 8 GB-class) | **the weak link** |

**becky is missing exactly one thing: good eyes.** Everything else in the "video memory
graph" already exists and is deterministic. Qwen3.8-Max is not a replacement for becky's
pipeline — it is a **drop-in upgrade for one stage of it**, and the stage that
`HANDOFF-SHORTS-PIPELINE.md` §2.3 already named as the root cause.

---

## 3. Cost, computed — where "almost free" is true and where it isn't

### 3a. Visual token arithmetic (estimate, from documented compression [11])

At 32× spatial compression, a frame costs `(W × H) / 1024` tokens:

| Frame resolution | Tokens/frame | Frames in 1M ctx | Video (1 frame / 10 s) |
|---|---|---|---|
| 1280×720 | **900** | ~1,110 | **~3.1 hours** |
| 960×540 | 506 | ~1,975 | ~5.5 hours |
| 640×360 | **225** | ~4,444 | **~12.3 hours** |

So even at 360p and one frame per ten seconds, **100 hours needs ~8 separate calls minimum**
— and in practice far more, because you want each call scoped to a scene, not to a context
limit. Confirms §2: chunk-and-index is mandatory.

### 3b. Straight API cost (Model Studio, Jordan's money)

100 hours at 1 frame/10 s = 36,000 frames:

- at 640×360 → 8.1M tokens → **~$16** input
- at 1280×720 → 32.4M tokens → **~$65** input

Cheap in absolute terms — but it is **pay-per-token, i.e. Jordan's money**, which
`CLAUDE.md` forbids without an explicit guard. There is a **free tier**: 1,000,000 tokens
**per model**, 90 days, **Singapore/International region only**, real-time inference only
(no batch, no context caching, no fine-tuning) [9]. That is ~1.2 hours of 360p footage on
Max — enough to *evaluate*, nowhere near enough to *use*.

### 3c. Qoder credit cost — the actual recommendation (estimate)

Qoder does **not publish a token→credit formula**; it publishes median task costs [15]:
Ask @50K ctx ≈ **3 credits**, Agent @50K ≈ 7, Agent @200K ≈ 12, Quest Agent ≈ 50, Quest
Experts ≈ 75, Repo Wiki ≈ 50/repo. Plans: Pro **$20/mo**, Pro+ $60, Ultra $200, add-on
credits **$20 per 1,000**; Teams $40/seat @ 3,000 credits [14]. *Pro's quota is reported as
2,000 credits/mo — single source, **candidate**; check your own account page.* [14]

Live multipliers, off-peak = **14:00–00:00 UTC daily** [8]:

| Model | Standard | Off-peak | Off-peak discount |
|---|---|---|---|
| **Qwen3.7-Plus** | 0.1x | **0.04x** | **96% off** |
| Qwen3.7-Max | 0.5x | 0.1x | 80% off |
| **Qwen3.8-Max** | 0.5x | **0.25x** | 50% off |
| Kimi-K2.7-Code | 0.3x | — | — |
| MiniMax-M3 | 0.2x | — | — |

> **Off-peak 14:00–00:00 UTC is 10:00–20:00 US Eastern / 07:00–17:00 US Pacific.** The
> cheap window is your *working day*, not the middle of the night. Schedule bulk passes
> inside it; `becky-eyes` should refuse-and-wait outside it by default.

**A `becky-eyes` call** = ~20 frames @640×360 (4,500 image tokens) + a short prompt + a
~200-token answer ≈ **5K context** — well under the 50K Ask median, so ≤3 credits at 1.0x is
conservative. 20 frames at 1 frame/10 s covers **200 seconds** of footage.

100 hours = 360,000 s ÷ 200 = **1,800 calls**:

| Model / window | Credits per call | 100 h total | As % of a 2,000-credit Pro month |
|---|---|---|---|
| **Qwen3.7-Plus off-peak (0.04x)** | ~0.12 | **~216 credits** | **~11%** |
| Qwen3.7-Plus standard (0.1x) | ~0.30 | ~540 | ~27% |
| **Qwen3.8-Max off-peak (0.25x)** | ~0.75 | **~1,350** | ~68% |
| Qwen3.8-Max standard (0.5x) | ~1.50 | ~2,700 | 135% — **over quota** |

**Read that table as the architecture.** Bulk-indexing 100 hours on Qwen3.8-Max costs more
than a month of Pro; on Qwen3.7-Plus off-peak it costs a *ninth* of one. So: **3.7-Plus
sweeps everything, 3.8-Max is the escalation for windows the sweep flags** — the same
E4B→12B ladder `becky-validate` already implements, with two new rungs on top. Money is now
just another corroboration budget.

*Every number in 3c is an estimate built on Qoder's published medians and multipliers; the
credit-per-call figure is the one to measure first (§5, step 2) because everything scales
off it.*

---

## 4. Can Qoder credits actually see video? — the decisive question

**No to video. Yes to images — which is all becky needs.** Three independent signals:

1. Qoder CLI's own overview lists **"Multimodal Input (e.g., images, PDFs) to generate code
   from design drafts or screenshots"** [16].
2. CLI release notes, v0.1.14 (3 Dec 2025): **"Added attachments flag in headless mode for
   image attachments"** — and the CLI reference documents **`--attachment` (string,
   repeatable)** [17][18].
3. CLI release notes, v1.1.19 (11 Aug 2026): *"Fixed errors when switching to a **non-vision
   model with image context**"* — vision models with image context are a live, maintained
   path in the CLI [17].

Against that: the IDE chat box takes **up to 20 images and 20 files** [19], and a forum
feature-request shows image upload is **not** exposed for *custom/BYOK* models [20]. No
source anywhere shows **video** input to Qoder. → **Design for image lists, cap batches at
20 frames, use built-in model ids (not BYOK).**

This is not a compromise. Qwen's own image-list mode with an `fps` hint [10] is the
documented way to do video understanding from pre-extracted frames — and it keeps frame
selection inside becky's deterministic layer, which is where `FORENSIC-OUTPUT-PHILOSOPHY.md`
wants it.

**The seam, concretely:**

```bash
qoder --print --output-format json --model qwen3.7-plus \
      --attachment f0001.jpg --attachment f0002.jpg ... \
      --no-session-persistence --disallowed-tools "Write,Edit,Bash" \
      "These 20 frames are 10s apart from <clip> starting at 00:14:20. ..."
```

`--print` is non-interactive and CI-safe; `--output-format json` is parseable;
`--no-session-persistence` keeps forensic runs off disk; `--disallowed-tools` stops a
*coding* agent from touching the repo while it is being used as an eyeball [18]. That is a
becky-shaped subprocess call — identical in kind to how `becky-vision` shells out to
`llama-mtmd-cli`.

### 4a. The rest of the Qoder surface, and what's worth using

| Feature | What it is | Verdict for becky |
|---|---|---|
| **CLI headless `--print`** | non-interactive, json/stream-json out [18] | **This is the integration.** |
| **Agent SDK (TS + Python)** | same agent capability, programmatic [21] | Fallback if `--attachment` disappoints; matches `internal/pyhelpers` shape. |
| **Scheduled tasks + budgets** | cron in plain English; **turn and credit limits** per task (11 Aug) [17][22] | **Use it.** Overnight indexing with a hard credit ceiling = the spending guard, enforced by the platform. |
| **Quest / Goal mode** | long-horizon autonomous coding, self-verifying [23] | For *building* becky, not for footage. ~50–75 credits/task [15] — expensive; don't put footage through it. |
| **Cloud Agents** | managed agent runtime, **API-only** invocation, container sandboxes (28 May) [24] | Skip for now. Adds a hosted dependency for no vision gain. |
| **`--remote`** | submit to a cloud container, survives disconnect [17] | Skip — your footage is local; uploading it is the whole cost. |
| **MCP / subagents / skills** | standard extension points [16] | Later: expose becky's tools *to* Qoder rather than the reverse. |

### 4b. Two risks, stated plainly

1. **Terms-of-use.** A coding subscription used to bulk-analyse 100 hours of video frames is
   not what Qoder sells. No source says it's prohibited; none says it's allowed. Sustained
   1,800-call batches are visible usage. **Start small, watch the account, and keep the
   Model Studio API path as the legitimate fallback** — this is your call to make, but make
   it knowingly rather than discovering it at hour 60.
2. **Non-determinism.** Everything else in becky is fixed-seed reproducible. A hosted model
   is not, and Qoder exposes no seed/temperature control. Treat every Qoder answer as a
   **`candidate` signal requiring corroboration**, never as a conclusion — and cache every
   response keyed by frame-set hash so a re-run is reproducible *over the snapshot*, the
   same trick `SPEC-DEEP-RESEARCH.md` §6 uses for the web.

---

## 5. What this unlocks for your two use cases

### 5a. The documentary (use case 1) — "handle the human review before I review it"

The honest framing: this does **not** edit your documentary. It removes the part you
described as the bottleneck — *watching everything to find what matters*.

Pipeline, reusing what exists:

```
footage → becky-transcribe/diarize (built) → becky-identify (built)
        → becky-eyes sweep @ Qwen3.7-Plus, 1 frame/10 s, off-peak (NEW)
        → becky-embed → beckydb (built)
        → becky-search "the confrontation at the door" (built)
        → becky-eyes --escalate @ Qwen3.8-Max on the ~40 windows that matter (NEW)
        → becky-moment (built) → becky-reel / VEGAS captions (built)
```

What changes: `becky-search` stops being transcript-only. Today "who said X" works and
**"show me where he pushes past her"** does not, because nothing in the DB describes what
is *seen*. A 3.7-Plus visual sweep at ~216 credits per 100 hours fixes that. Then you search
your own footage the way the press release describes — except becky owns the index, offline,
and you can audit it.

**The forensic guard-rail is unchanged and non-negotiable:** a Qwen description is one
signal. `CLAUDE.md`'s invariant — *a transcript mention or a motion burst is NEVER presence*
— now reads *a frontier-model description is NEVER presence either*. It narrows; `becky-identify`
+ `becky-presence` still corroborate before a name or a timeline entry is asserted.

### 5b. Shorts / repurposing (use case 2)

`HANDOFF-SHORTS-PIPELINE.md` says stages 1–3 are built and **4–6 (find subject → who's
speaking → reframe 9:16) are missing**, root cause: the vision layer (§2.3). Frontier eyes
address 4 and 5 directly — *"which of these three faces is the one talking at 00:04:12,
and where in frame are they"* is a question a #2-Vision-Arena model answers well from a
20-frame strip, and badly-or-not-at-all on an 8 GB Gemma.

It does **not** fix stage 6 (reframe/render) — that is MediaPipe/OpenCV geometry Jordan
already ratified, and §2.1's silent-failure warning still governs: every stage reports what
it did and how sure it is.

For back-catalogue indexing and livestream highlight-finding, `becky-eyes` + `becky-moment`
+ `becky-search` is the whole answer, and the credit table says a full back-catalogue sweep
is affordable inside one Pro month.

### 5c. What this does NOT solve — filed so nobody re-chases it

`SPEC-VIDEO-ANALYSIS.md`'s actual problem — **sub-second motion** (who initiated, who pulled
away) — is untouched. Qwen samples frames like every other video-LLM; `fps` caps at 10 [10]
and the practical budget is ~1 frame/10 s (§3a). A frontier model gives you a *much better
describer of the same stills*, which is exactly what §2 predicted of Qwen3-VL in June. The
two-tier motion pipeline in that spec remains the answer for evidence-critical windows.

---

## 6. Recommendation

1. **Build `becky-eyes`** — one tool, one job: *frames in → grounded visual description out*,
   with pluggable backends (`qoder` default, `modelstudio` guarded, `mock` for CI). Spec:
   `SPEC-BECKY-EYES.md`.
2. **Add a rung to the existing ladder** rather than a parallel system: `becky-validate
   --backend qoder` escalates above Gemma-4 12B. No new escalation logic, no second protocol.
3. **Two-model price ladder**: Qwen3.7-Plus (0.04x) sweeps, Qwen3.8-Max (0.25x) escalates,
   both off-peak by default.
4. **Hard spending guard in code, not judgement** — copy `cmd/subtitle/openrouter.go`'s
   `isFreeModel` shape: `becky-eyes` refuses any pay-per-token backend unless
   `--i-am-paying` is passed, and enforces a per-run credit ceiling.
5. **Do not** put footage through Quest, Cloud Agents, or `--remote`. Cost, or a hosted
   dependency, for no vision gain.

Open question only Jordan can answer: **the ToS risk in §4b.1** — is bulk frame analysis on
a coding subscription a line you're willing to walk up to? The architecture works either
way; the backend flag is one string.

---

## Sources (verified 2026-08-17)

1. [Alibaba Group — Qwen3.8-Max announcement, 3 Aug 2026 (2.4T/95B active, 1M ctx, "hundred-page documents, full television series, or 100-hour livestreams", Vision Arena #2)](https://www.alibabagroup.com/en-US/document-2021044032125272064)
2. [Vercel AI Gateway — Qwen 3.8 Max API, pricing & limits ($2/$6 per M, implicit cache $0.25, video ≤2 h/2 GB/64 videos, OpenAI+DashScope+Anthropic-compatible)](https://vercel.com/ai-gateway/models/qwen3.8-max)
3. [Alibaba Cloud Model Studio — qwen3.8-max model card (context 1M, max input 991,808, max output 131,072, CoT 262,144, in: text/image/video, CN pricing, regions)](https://help.aliyun.com/en/model-studio/qwen3-8-max)
4. [Alizila — Alibaba unveils Qwen3.8-Max (video memory graph: people, events, timestamps, scenes across long time spans)](https://www.alizila.com/alibaba-unveils-qwen3-8-max-most-capable-flagship-model-to-date/)
5. [Alibaba Cloud on Medium — "Qwen3.8-Max: A New Bar for Coding and Cowork" (searchable/traceable/interactive knowledge structure; self-observing execution; open weights)](https://alibaba-cloud.medium.com/qwen3-8-max-a-new-bar-for-coding-and-cowork-b1ed265e03f0)
6. [DataCamp — Qwen3.8-Max features, benchmarks, pricing](https://www.datacamp.com/blog/qwen3-8-max)
7. [Qoder — "Qwen3.8-Max-Preview All-Day 90% Off, Off-Peak Up to 98% Off" (0.05x all-day / 0.01x 22:00–08:00 UTC+8; **19 Jul → 3 Aug 2026**; Experts mode excluded)](https://docs.qoder.com/events/qwen-max-preview)
8. [Qoder — Premium Model Discount Rates (live table: Qwen3.8-Max 0.5x→0.25x, Qwen3.7-Max 0.5x→0.1x, Qwen3.7-Plus 0.1x→0.04x; off-peak 14:00–00:00 UTC; from 3 Aug 2026, no end date)](https://docs.qoder.com/events/offpeakrate)
9. [Alibaba Cloud Model Studio — free quota for new users (1M tokens per model, 90 days, Singapore/International only, real-time inference only)](https://www.alibabacloud.com/help/en/model-studio/new-free-quota)
10. [Alibaba Cloud Model Studio — Image and video understanding (video_url vs image list, fps 0.1–10 default 2.0, max_frames DashScope-only, 2 h/2 GB limits per model)](https://www.alibabacloud.com/help/en/model-studio/vision)
11. [Qwen3-VL — cloud API / DashScope (32× spatial compression = 32×32 px per visual token, 2× temporal compression for video, per-video budget 256–16,384 tokens)](https://deepwiki.com/QwenLM/Qwen3-VL/6.6-cloud-api-(dashscope))
12. [Alibaba Group — QwenWork launch (all-in-one workplace AI agent platform; public beta 2 Aug 2026; four model tiers; China website)](https://www.alibabagroup.com/en-US/document-2021039099929952256)
13. [Hugging Face — Qwen3.8-27B / -27B-FP8 (Apache-2.0, image-text-to-text) and Qwen3.8-2.4T-A95B (text-generation, license:other)](https://huggingface.co/Qwen/Qwen3.8-27B)
14. [Qoder — Pricing update: end of discount & Teams changes (Pro $20, Pro+ $60, Ultra $200, add-on $20/1,000 credits, Teams $40/seat @3,000)](https://docs.qoder.com/events/pricing-adjustment-notice)
15. [Qoder — Credits (no public formula; medians: Ask@50K ≈3, Agent@50K ≈7, Agent@200K ≈12, Quest Agent ≈50, Quest Experts ≈75, Repo Wiki ≈50)](https://docs.qoder.com/Credits)
16. [Qoder — CLI overview ("Multimodal Input (e.g., images, PDFs)"; Interactive/Plan/Goal/Headless modes; scheduled tasks; /loop; MCP, skills, subagents, hooks)](https://docs.qoder.com/cli/overview)
17. [Qoder — CLI release notes (v0.1.14 "attachments flag in headless mode for image attachments"; v1.1.19 "non-vision model with image context"; v1.0.0 `--remote`; 11 Aug scheduled-task turn+credit budgets; 5 Aug Agent Teams)](https://docs.qoder.com/release-notes/qoder-cli)
18. [Qoder — CLI commands and parameters (`--print/-p`, `--attachment` repeatable, `--output-format text|json|stream-json`, `--model/-m`, `--allowed-tools`/`--disallowed-tools`, `--no-session-persistence`, `--permission-mode`)](https://docs.qoder.com/cli/cli-reference)
19. [Qoder — IDE release notes (input box supports up to 20 images and 20 files)](https://docs.qoder.com/release-notes/desktop)
20. [Qoder forum — "Enabling image support for custom multimodal models" (feature request: image upload not exposed for custom/BYOK models)](https://forum.qoder.com/t/enabling-image-support-for-custom-multimodal-models-in-qoder/11044)
21. [Qoder — Agent SDK references (TypeScript + Python; headless / ACP / SDK integration modes)](https://docs.qoder.com/en/cli/sdk/references)
22. [Qoder — Scheduled tasks (plain-English scheduling; each run consumes Credits; bounded by balance)](https://docs.qoder.com/qoderwork/scheduled-tasks)
23. [Qoder — Quest agent mode / goal-driven (autonomous long-running delegation, self-verification, task reports)](https://docs.qoder.com/user-guide/quest/agent-mode)
24. [Qoder — Cloud Agents overview (managed agent runtime, container sandboxes, API-only invocation via SSE/polling; launched 28 May 2026)](https://docs.qoder.com/cloud-agents/overview)
25. [Qoder — CLI model selection (`--list-models`; frontier ids + multipliers: Qwen3.8-Max-Preview 0.5x, Qwen3.7-Plus 0.1x, DeepSeek-V4-Flash 0.1x, MiniMax-M3 0.2x; tiers Auto 1.0x / Ultimate 1.6x / Efficient 0.3x)](https://docs.qoder.com/cli/model)

In-repo canon this rests on: `SPEC-VIDEO-ANALYSIS.md` §2 (no local video-LLM in 8 GB),
`HANDOFF-SHORTS-PIPELINE.md` §2.3 (vision layer = root cause) + §2.2 (compounding error),
`FORENSIC-OUTPUT-PHILOSOPHY.md` (corroborate then conclude), `SPEC-DEEP-RESEARCH.md` §6
(determinism over a snapshot), `CLAUDE.md` §4 (never spend Jordan's money; `isFreeModel`
guard), `research/gemma4-qat-upgrade.md` (the 8 GB VRAM ceiling).
