# research/qwen38-max-video.md — Qwen3.8-Max native video: what it actually takes, and how to use it

> Deep research, 2026-08-17. **Rewritten after the first version got the headline wrong.**
> Question: *"it says it can watch entire shows and livestreams — how can I be using this?"*
>
> **Correction notice.** The first draft of this file claimed "100 hours is not one API call"
> and built everything on feeding the model 20-image batches through the Qoder CLI. **Both
> were wrong.** The first was a token-math error (§2); the second was letting a distribution
> channel's limitation dictate the architecture instead of researching the capability that
> was actually asked about. The corrected findings are below.

---

## 0. Bottom line

**Qwen3.8-Max takes video natively, and the "entire shows and livestreams" claim is
literal, not marketing.** A single API request accepts **up to 64 videos, each up to 2 hours
or 2 GB** — **128 hours of footage in one call** — alongside a 1M-token context. There is no
frame-extraction step required and no 20-image ceiling; those were artifacts of trying to
route this through a coding-agent CLI, which cannot take video at all.

**Two things to know before relying on it:**

1. **Audio is not a listed input modality.** Text, image and video in; text out. The
   observed behaviour of it understanding speech on the Qwen website is real, but the API
   surface does not document an audio pathway (§3) — which changes how you'd reproduce it.
2. **Fidelity and coverage trade against each other.** 128 hours in one request is real, but
   the per-video token budget is what decides whether it *watched* the footage or *glanced*
   at it (§2).

---

## 1. The capability, corroborated

| Property | Value | Source |
|---|---|---|
| Architecture | 2.4T params, 95B active, sparse MoE | [1][2] |
| Context | **1,000,000 tokens**; max input 991,808 | [3][4] |
| Max output | 131,072 tokens (~128K) | [3][4] |
| Input modalities | **text + image + video** (no audio) | [3][4][8] |
| **Videos per request** | **up to 64** | [2][4] |
| **Per video** | **up to 2 hours or 2 GB** | [2][4] |
| Images per request | up to 2,048 image URLs / 250 base64, 16 MP each | [4] |
| Ranking | **#2 Vision Arena**, #5 Text Arena | [1] |
| API price | **$2.00 / M input, $6.00 / M output**; implicit cache $0.25 / M | [2] |
| Interfaces | OpenAI-compatible, DashScope, Anthropic-compatible | [2] |

**64 videos × 2 hours = 128 hours in a single request.** That is the mechanism behind the
"full television series / 100-hour livestream" line in the announcement [1] — it is a
documented input limit, not a product abstraction over chunking.

The **video memory graph** is the layer on top: the model organises "people, events,
timestamps, and scenes… continuously building connections across long time spans to
reconstruct event progressions, character relationships, and critical moments" [5][6]. That
is what makes 100+ hours *useful* rather than merely *accepted*.

---

## 2. The token math I got wrong the first time

**The error:** I costed video at the default per-frame image budget — a 720p frame at 32×
spatial compression ≈ 900 tokens, so an hour at 1 fps ≈ 3.2M tokens — and concluded 100
hours could not fit. That applies to *pasting frames in as images*. It is not how the video
path works.

**The correction, from Qwen's own model card** for the open sibling `Qwen3.8-27B` [8]:

> set the `longest_edge` parameter in the video_preprocessor_config file to **469,762,048
> (corresponding to 224k video tokens)** … to enable higher frame-rate sampling for
> **hour-scale videos** and thereby achieve superior performance

So the video token budget is a **configurable cap for the whole video**, not a per-frame
cost. At the recommended hour-scale setting an entire long video costs **~224K tokens** —
roughly **14× less** than my frame-by-frame arithmetic implied. Defaults are `fps=2` with
`do_sample_frames=True`, tunable via `mm_processor_kwargs` [8].

**What that means in practice:**

| Approach | Token cost | Input cost @ $2/M |
|---|---|---|
| One 2-hour video, hour-scale config (224K budget) | ~224K | **~$0.45** |
| 100 hours as 50 × 2-hour videos, high fidelity | ~11.2M | **~$22** |
| 128 hours in one request, budget split across 64 videos | ≤1M | **~$2** (and coarse) |

**The real constraint is not "can it fit" — it is "how much did it actually see."** 128 hours
inside a 1M context means ~15K tokens per video, which is a glance. The honest planning rule:
**budget ~224K tokens per hour-scale video for genuine understanding**, which caps a single
high-fidelity request at roughly **4 hours** of footage, and makes 100 hours a **~50-call
job costing on the order of $20–45** — not a technical barrier, a modest bill.

---

## 3. Audio — the open question, stated precisely

**Every documented model-card surface lists video without audio:**
- Model Studio's `qwen3.8-max` card: input **text, image, video**; output text [3].
- Vercel's model page and FAQ: accepts "Text, images, and video," returns text; **audio is
  not listed** [2][4].
- The open `Qwen3.8-27B` card: `image-text-to-text`, no audio modality [8].
- Model Studio's **omni** model — the one that does document audio — is a **different
  model**, `qwen3.5-omni-plus`, which "integrates understanding and generation across text,
  image, audio, and video," with a realtime WebSocket variant [7].

**But direct observation says the Qwen website understands speech in an uploaded video, to a
high standard.** Both facts can be true. The most likely explanation — **candidate, not
established** — is that the consumer product runs a separate ASR pass and supplies the
transcript alongside the sampled frames. Alibaba ships exactly that in-house: **Qwen3-ASR /
fun-asr**, with timestamps, 11 languages, contextual biasing and long-audio async
transcription [9][10]. Qwen Studio is explicitly a bundle — "chatbots, image and video
understanding, image generation and editing, document processing, tool utilization, voice
and video chat" [11] — i.e. a pipeline, not a single raw model call.

**Practical consequence, and it is a small one:** if you use the **website**, audio
understanding just works. If you drive the **API**, don't assume the audio track is heard —
**pass a timestamped transcript alongside the video**. That is one extra input, and it
reliably reproduces what the website does rather than hoping the endpoint behaves like the
product.

---

## 4. The three ways to actually use it

### 4a. Qwen Studio (chat.qwen.ai) — free, zero engineering, works today
Video understanding is available in the consumer app; **free, no subscription** [11][12],
with video uploads reported up to **~500 MB** [12]. This is the path that matches what was
observed: upload footage, ask questions, get video **and** audio understood without building
anything. Limits are file-size-bound rather than duration-bound, so long footage needs
sensible compression, and there is no batch/automation surface.

**Best for:** the review pass on a specific piece of footage — "watch this and tell me what
I missed." Cost: $0.

### 4b. Model Studio API — the scale path
OpenAI-compatible, DashScope, or Anthropic-compatible endpoints [2]. Video goes in as
`video_url` (OpenAI-style) or `video` (DashScope), with `fps` controlling sampling; the
DashScope SDK additionally exposes `max_frames` [13]. Regions: Beijing, **Singapore**,
Frankfurt, Virginia, Tokyo [3]. **New accounts get 1,000,000 free tokens per model, 90 days,
Singapore/International region only, real-time inference only** [14] — that is ~4 hours of
hour-scale video, free, which is enough to test the whole approach before spending anything.

**Best for:** running the same question across a whole library, unattended. Cost: ~$0.45 per
2-hour video at high fidelity.

### 4c. QwenWork — the productised version
The "video memory graph" as a workplace product; public beta since 2 Aug 2026, subscription
plus credits, four model tiers [15]. **China-only website at time of writing**, which makes
it impractical here, but it is the thing the announcement was actually demoing.

### 4d. What does *not* work: Qoder
Qoder's CLI and IDE accept **images and PDFs**, never video [16][17]. The subscription is a
coding-agent product; routing footage through it would mean pre-extracting frames and
accepting a ~20-image ceiling — which is the corner-cut this document originally, wrongly,
recommended. **The Qoder subscription is not the vehicle for this.** Use the website (free)
or the API (cheap).

---

## 5. What to be careful about

- **Fidelity is a dial, and a coarse setting looks identical to a careful one in the output.**
  A model given 15K tokens for a 2-hour video will still answer confidently. Set the video
  token budget deliberately and record what it was.
- **Non-determinism.** A hosted model gives no seed control; the same footage can yield
  different descriptions run to run. Anything load-bearing needs the response captured and
  kept, not regenerated.
- **Audio assumption (§3).** Don't infer "it heard the tone of voice" from an API response
  unless you supplied the transcript. On the website, it evidently does; on the API, that is
  undocumented.
- **Sub-second motion.** `fps` caps at 10 [13] and hour-scale configs sample far below that.
  Force, direction, who-initiated — those live between frames at any practical sampling rate.
  This model is a far better *describer*; it is not a motion-analysis instrument.

---

## Sources (verified 2026-08-17)

1. [Alibaba Group — Qwen3.8-Max announcement, 3 Aug 2026 ("hundred-page documents, full television series, or 100-hour livestreams"; Vision Arena #2)](https://www.alibabagroup.com/en-US/document-2021044032125272064)
2. [Vercel AI Gateway — Qwen 3.8 Max model page (**64 videos, each up to 2 h or 2 GB**; $2/$6 per M; OpenAI+DashScope+Anthropic-compatible)](https://vercel.com/ai-gateway/models/qwen3.8-max)
3. [Alibaba Cloud Model Studio — qwen3.8-max model card (1M context, max input 991,808, max output 131,072, **input: text/image/video**, regions)](https://help.aliyun.com/en/model-studio/qwen3-8-max)
4. [Vercel AI Gateway — Qwen 3.8 Max FAQ (**"as many as 64 videos, with each video up to two hours or 2 GB"**; 2,048 image URLs / 250 base64, 16 MP each; **audio not listed**)](https://vercel.com/ai-gateway/models/qwen3.8-max/faq)
5. [Alizila — Alibaba unveils Qwen3.8-Max (video memory graph: people, events, timestamps, scenes)](https://www.alizila.com/alibaba-unveils-qwen3-8-max-most-capable-flagship-model-to-date/)
6. [Alibaba Cloud on Medium — Qwen3.8-Max: A New Bar for Coding and Cowork](https://alibaba-cloud.medium.com/qwen3-8-max-a-new-bar-for-coding-and-cowork-b1ed265e03f0)
7. [Alibaba Cloud Model Studio — supported models (the omni model is **qwen3.5-omni-plus**: text/image/**audio**/video, plus a realtime WebSocket endpoint; fun-asr for speech)](https://www.alibabacloud.com/help/en/model-studio/models)
8. [Hugging Face — Qwen/Qwen3.8-27B model card (`image-text-to-text`, **no audio**; **"longest_edge … 469,762,048 (corresponding to 224k video tokens)"** for hour-scale video; default `fps=2`, `do_sample_frames=True`)](https://huggingface.co/Qwen/Qwen3.8-27B)
9. [Alibaba Cloud Model Studio — non-real-time speech recognition (long-audio async transcription)](https://www.alibabacloud.com/help/en/model-studio/qwen-speech-recognition)
10. [Qwen3-ASR — multilingual recognition, timestamps, contextual biasing, 11 languages](https://qwenasr.com/)
11. [Qwen Studio — chat.qwen.ai (image and video understanding, voice and video chat, document processing; free, no subscription)](https://chat.qwen.ai/)
12. [Qwen Chat vs BibiGPT 2026 — video summary comparison (Qwen Chat video inputs reported up to ~500 MB)](https://bibigpt.co/en/blog/posts/qwen-chat-vs-bibigpt-video-summary-2026)
13. [Alibaba Cloud Model Studio — image and video understanding (`video_url` / `video`, `fps` 0.1–10 default 2.0, `max_frames` DashScope-only)](https://www.alibabacloud.com/help/en/model-studio/vision)
14. [Alibaba Cloud Model Studio — free quota for new users (1M tokens per model, 90 days, Singapore/International, real-time inference only)](https://www.alibabacloud.com/help/en/model-studio/new-free-quota)
15. [Alibaba Group — QwenWork launch (public beta 2 Aug 2026, four model tiers, subscription + credits)](https://www.alibabagroup.com/en-US/document-2021039099929952256)
16. [Qoder — CLI overview (multimodal input: **images, PDFs** — no video)](https://docs.qoder.com/cli/overview)
17. [Qoder — IDE release notes (input box supports up to 20 images and 20 files)](https://docs.qoder.com/release-notes/desktop)
