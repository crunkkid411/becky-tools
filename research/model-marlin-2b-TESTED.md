# Marlin-2B — actually run, on Jordan's own prank clip

Source: https://huggingface.co/NemoStation/Marlin-2B · licence **Apache-2.0** · base Qwen3.5-2B

The earlier note (`model-marlin-2b.md`) could not evaluate this because the repo
is gated. **Jordan granted access on 2026-08-20.** This is what it actually does,
run here, on the rubber-snake clip he names in RULE 4.

---

## Why it is the right shape for becky

Marlin exposes exactly two methods, and they are the two questions Rule 4 asks:

    marlin.caption(video)              -> Scene paragraph + timestamped Events
    marlin.find(video, event="...")    -> (start, end) in seconds

`find` is *"the very first frame in which it starts to move"* as a single call.
becky has nothing that answers that. Gemma-4 can be prompted toward it and
returns prose that has to be parsed; Marlin returns `From 18.5 to 18.5.` and a
parsed `(18.5, 19.5)` tuple with a `format_ok` flag.

## What it produced

Input: `Prank Clips_Sony AVC-MVC_BEST 30 FPS 1080[4].mp4`, 42.0–64.0s (22s), the
POV stretch plus the reaction. Times below are **clip-relative**; add 42.0 for
source time.

### caption()

> The video takes place in a residential interior, primarily focusing on a
> hallway and a basement area. The hallway features a black rubber mat on the
> floor, a white door, and a wooden staircase with a white railing. A person
> wearing a pink long-sleeved shirt with a PlayStation logo, black pants, and a
> grey and brown fur ushanka hat is the central figure. […] The camera work is
> handheld and first-person, often looking down at the floor or following the
> person's movements as they navigate the space.

    <0.0 - 5.5>   The camera looks down at a yellow rope on the floor.
    <5.5 - 7.0>   A person carries a black metal gate through the hallway.
    <7.0 - 10.0>  The person walks toward the camera on the hallway floor.
    <10.0 - 11.5> The person stands by the railing holding the metal gate.
    <11.5 - 13.0> The camera looks down at the yellow rope on the floor.
    <13.0 - 14.0> The person bends down to pick up the yellow rope.
    <14.0 - 16.0> The person stands up holding the yellow rope.
    <16.0 - 18.5> The person adjusts their hat while looking at the camera.
    <18.5 - 20.0> The person touches their hat with their left hand.
    <20.0 - 22.0> The person continues adjusting their hat while standing still.

The scene read is accurate and specific — the PlayStation shirt, the fur hat,
the staircase and railing, the storage bins, the first-person camera. It calls
the rubber snake a "yellow rope"; Gemma-4 called it a "coiled yellow garden
hose". Both are honest reads of the pixels and both find the right object.

### find() — CHECKED AGAINST THE FRAMES

| query | Marlin | source time | what is actually there |
|---|---|---|---|
| "a person reacts with a shocked facial expression" | `(18.5, 19.5)` | **60.5–61.5s** | **Correct.** Hand going to his head, right after the "FAKE SNAKE PRANK!" caption. This is the payoff shot. |
| "the snake starts to move" | `(1.2, 2.2)` | 43.2–44.2s | **Partly.** That is the snake REVEAL — the coiled snake on the carpet under "DUDE, THERE'S A SNAKE!" — not the motion onset. Right object, right region, wrong verb. |

One exact hit and one near miss, and the exact hit is the harder question: Rule 4
says *"the framing must be on Robby's face 1–3 frames BEFORE he realises"*.
Locating the realisation is the part becky could not do at all.

## 2026-08-21 — A GGUF PAIR EXISTS, AND THE CPU TIMING ABOVE IS NOT A VERDICT

Jordan, on the numbers below: the GPU was unavailable to that session, so 22 minutes for a
22-second clip is a measurement of **this machine's CPU in float32**, not of Marlin. Do not
carry it forward as a reason to skip the model. Standing rule now in `CLAUDE.md`: never
reject a model on a timing measured under the wrong conditions, and never on speed alone.

He also asked that we use GGUF if we use it. **A GGUF pair exists** — found and file-listed
2026-08-21 via the Hub API (not a blog post):

    https://huggingface.co/jadeonrails/marlin-2b-gguf
      marlin-2b-text.gguf   4.79 GB   text tower
      marlin-2b.gguf        0.67 GB   VISION PROJECTOR (mmproj)

The mmproj being present is the thing that matters — its absence is what would make the
whole GGUF useless for video, exactly the trap `reka-edge-vs-gemma4.md` flags. The repo's
README documents a first-class llama.cpp path:

    llama-mtmd-cli -m marlin-2b-text.gguf --mmproj marlin-2b.gguf       --video input.mp4 -p "Describe the scene and events."

**NOT YET VERIFIED HERE.** Unlike the Reka entry, nothing in this section has been run on
this machine. Open questions, in order: (1) does build 9551's `mtmd.dll` carry Marlin's
projector tensors, or does it need a rebuild — check the GGUF header over an HTTP range
request BEFORE downloading 5.5GB, the way the Reka check was done; (2) is
`marlin-2b-text.gguf` at 4.79GB F16 rather than quantized, and does F16 + mmproj fit under
the 8GB ceiling beside nothing else; (3) does `find()`'s second-precise answer survive the
conversion. The repo is a single third-party upload (0 likes, ~395 downloads, 344-byte
README) — treat the conversion itself as unverified until its output is checked against the
float32 answers recorded below.

## The blocker is runtime, not capability

Measured here, **on CPU in float32**, because the discrete GPU is not visible to
this session (`Win32_VideoController` reports only Intel UHD; `nvidia-smi` needs
administrator):

    model load          101s
    caption() 22s clip  548s
    find()  per query   384s, 399s
    resident memory     12.7 GB

**22 minutes for a 22-second clip.** Unusable in the pipeline like this. On the
RTX 3070 a 2B model at bf16 is roughly 4GB of weights and this is seconds, not
minutes — the card's own documented defaults (2 fps, 448×448, 240-frame cap) are
sized for exactly that.

So the verdict is not about the model:

- **Capability: yes.** It answers the Rule 4 questions in the right format.
- **Fit: yes.** 2B, Apache-2.0, 4GB at bf16 — comfortably inside the 8GB budget,
  and it does NOT need to be resident alongside Gemma-4, because it replaces
  Gemma for this particular question rather than assisting it.
- **Blocked on: the GPU being available to the process that runs it.**

## How it would be wired

The join Rule 4 needs is *what* + *where*, and neither model does both:

    Marlin.find("the snake starts to move")  -> WHEN, second-precise
    internal/focal                           -> WHERE, horizontal aim
    two agreeing = commit; either alone = centre crop and say so

That is the corroborate-then-conclude rule applied literally, and it is what
`--focal-point` is waiting for. Marlin supplies the second signal that the
measurement in `focalaim.go` says is missing — with the caveat that Marlin gives
a TIME and focal gives an X, so they corroborate a moment, not a position. The
position still has to come from focal, and focal still has to be able to defend
it.

## Install notes for the next session

A dedicated venv, because the machine's global `PIP_TARGET`/`PYTHONUSERBASE`
redirect breaks native-extension installs (CLAUDE.md's own trap list):

    unset PIP_TARGET PYTHONUSERBASE
    python -m venv X:/AI-2/becky-tools/.venv-marlin
    .venv-marlin/Scripts/python -m pip install "torch>=2.11.0" torchvision \
        --index-url https://download.pytorch.org/whl/cu128
    .venv-marlin/Scripts/python -m pip install "transformers>=5.7.0" torchcodec \
        "qwen-vl-utils>=0.0.14" av pillow accelerate

`torchvision` is NOT in the model card's dependency list but `qwen_vl_utils`
imports it, so the install fails at first use without it.

The card's env vars must be set BEFORE importing transformers. `FPS_MAX_FRAMES`
defaults to 240 (≈2 minutes); dropping it to 64 is what makes a CPU run finish
at all.

The reproduction script is `scripts/marlin_probe.py`.
