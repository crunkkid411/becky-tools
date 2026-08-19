# Reka Edge 2603 vs gemma-4-12b-it — is a second VL worth it?

Asked by `shorts-user-feedback.md`. Verified 2026-08-19 against the **model cards and the GGUF
repo file listing**, not a blog post — `CLAUDE.md`'s standing rule after the TTS pick was botched
twice off stale articles.

## The answer

**Yes — add it, for one specific job: telling us WHERE a thing is in the frame.** Not as a
replacement for Gemma-4, and not as a general second opinion.

Reka Edge is a 7B VLM that returns **grounded bounding boxes on demand**, which Gemma-4 cannot do
at all. That is the exact capability the shorts pipeline is missing, and it is missing in the
place Jordan complained about hardest.

**Not yet verified locally.** Nothing below has been run on this machine. The claim that it is
worth adding rests on published numbers and the GGUF file listing; the claim that it FITS rests on
arithmetic against the 8GB budget. See "What still has to be proven".

## The numbers (Reka's own card, so read them as a vendor's claims)

| Benchmark | Reka Edge 7B | Cosmos-Reason2 8B | Qwen 3.5 9B | Gemini 3 Pro |
|---|---|---|---|---|
| MLVU *video understanding* | **74.30** | 37.85 | 52.39 | 80.68 |
| MMVU *multimodal video* | **71.68** | 51.52 | 68.64 | 78.88 |
| RefCOCO-A *grounding* | **93.13** | 90.98 | 93.62 | 81.46 |
| RefCOCO-B *grounding* | **86.70** | 85.74 | 88.83 | 82.85 |
| VideoHallucer *hallucination* | 59.57 | 51.65 | 56.00 | **66.78** |
| VQA-v2 | 88.40 | 79.82 | 83.22 | **89.78** |

| Metric | Reka Edge | Cosmos-Reason2 8B | Qwen 3.5 9B |
|---|---|---|---|
| Input tokens for a 1024×1024 image | **331** | 1063 | 1041 |
| End-to-end latency (local) | **4.69s ± 2.48** | 10.56s ± 3.47 | 10.31s ± 1.81 |

Gemma-4-12b is not in their table, so this is not a head-to-head against our default. Treat the
grounding and token-efficiency numbers as the reason to try it, and the local run as the decision.

## Why this matters HERE, not in the abstract

### 1. It answers the prank-clip problem, which nothing in becky currently can

Jordan's worked example is a rubber snake on a string: *"the clip itself is obviously meant to be
the focal point… the correct framing is to ensure that Robby is shown standing up and walking away,
but making sure the snake is the focal point at the very first frame in which it starts to move."*

Every framing signal becky has is a **person** detector — MediaPipe Pose, InsightFace, LR-ASD. On a
POV shot with no faces visible, the crop path has nothing to aim at and `becky-short` correctly
refuses (`--min-head-frac`, `--max-gap`). Refusing is honest, but the shot is not unframeable — it
is unframeable *by a face detector*.

Reka takes a literal instruction and returns boxes:

    "Detect: rubber snake, string, man in the white shirt"

93.13 on RefCOCO-A is grounding accuracy that **beats Gemini 3 Pro** (81.46) at a twentieth of the
size. A box is exactly what `internal/crop` already consumes — it takes rects and smooths a camera
path over them. So this plugs into the existing renderer with no new architecture: swap the source
of the rect, keep the zero-lag smoother, keep the sendcmd render.

### 2. 331 tokens per image is what makes dense sampling affordable

`research/iphone-ai-video-sweep.md` records the F-16 finding: video LLMs sample at 1-2 FPS and lose
"body movements and micro-expressions", and Jordan's payoff frame is a 1-3 frame window (33-100ms).
Fixing that means feeding a VL a dense burst of consecutive frames — which is a token problem.

At **331 tokens per image against ~1050**, Reka fits roughly **3× the frames** in the same context.
It also lands inside becky's hard constraint: `CLAUDE.md` records that `n_ubatch` is 512 and
**asserts (crashes the server) on anything larger**, so `--image-max-tokens` is pinned to exactly
512 — 560 was tested and crashed. One media item must fit one micro-batch. 331 fits with room;
a ~1050-token image does not, which is why our current vision path is one-frame-at-a-time.

### 3. It takes video directly, and llama.cpp support is first-party

The card documents `{"type": "video", "video": "media/dashcam.mp4"}` and ships a llama.cpp path
that is **official, not community-reverse-engineered**: `convert_reka_vlm_to_gguf.py` lives in the
llama.cpp repo, with `--mmproj` for the vision encoder and their own quantize scripts.
`Vastined/reka-edge-2603-GGUF` already carries the built artefacts — **mmproj files are present**
(`mmproj-…-Q8_0.gguf`, 756MB), which is the file whose absence would have made the whole GGUF
useless for vision.

Run it the way the card says: `llama-server -m … --mmproj … --reasoning off`. **The model has no
reasoning mode** — leaving it on is the obvious first-run mistake.

## Where Gemma-4 stays

`gemma-4-12b-it` is `any-to-any` — it takes **audio** as well as vision, which is the whole basis
of Whoretana's brain (raw mic WAV in, transcript + reply + intent out, no separate STT). Reka is
image/video+text only. So this is not a swap:

- **Gemma-4 — the judge.** Content, context, "is this actually interesting", anything involving
  audio. Apache-2.0, already downloaded, already wired through `internal/llmlocal`.
- **Reka Edge — the eye.** "Where is the thing", "which frame does the motion start", grounded
  boxes for the crop path. Never the arbiter of taste.

Its weakest published number is **VideoHallucer 59.57** — below Gemini's 66.78. It hallucinates
more than a frontier model, so its output must be treated as a candidate to corroborate, exactly
like every other detector in this repo. `FORENSIC-OUTPUT-PHILOSOPHY.md` already covers this: a lone
weak signal is "unknown", never a conclusion.

## Cost against the 8GB ceiling

`CLAUDE.md`: 8GB is a tested hard ceiling on the RTX 3070 Laptop, with brain (4.3GB) + TTS
(0.7-3.1GB) + the D3D11 orb already at 5.2-7.75GB.

    reka-edge-2603-Q4_K_M.gguf      4.75 GB
    mmproj-reka-edge-2603-Q8_0.gguf 0.76 GB
    ------------------------------------------
    total                          ~5.5 GB

**It does not fit alongside Whoretana's brain.** It fits as an *offline editing* model — which is
what this pipeline is. Jordan's own framing: *"This is OFFLINE EDITING, not a realtime preview…
make it do a SECOND PASS for christ's sake - or 100 fucking passes."* Load it for the framing pass,
unload it, and never hold it resident beside the assistant. `Q3_K_M` (4.17GB) exists if 5.5GB is
still too much.

## Licence

Not Apache. `reka-edge-2603-license`: commercial use permitted **under $1M/year revenue**. Fine for
Jordan, and worth writing down before anyone builds a product on it.

## What still has to be proven — do not mark this done without it

- [ ] Download `Q4_K_M` + `mmproj-Q8_0`, run `llama-server … --reasoning off`, confirm it loads
      inside the VRAM budget and report the actual figure.
- [ ] Feed it one frame of `test-for-clips.mp4` with `Detect: person, microphone` and check the
      boxes against where those things actually are. Grounding accuracy on a benchmark is not
      grounding accuracy on his footage.
- [ ] Measure real tokens-per-image locally against the 512 `n_ubatch` ceiling. 331 is their
      number, at their resolution.
- [ ] Time a dense burst — say 16 consecutive frames — and compare against Gemma-4 on the same
      frames. If it is not meaningfully faster or better here, it is not worth a second model.
- [ ] Confirm this machine's llama.cpp build actually contains the Reka vision op. The GGUF
      existing does not prove the local binary can run it.
