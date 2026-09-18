# MiniCPM5-2B (plus Needle, Arch-Router and Plano-Orchestrator): tested on this PC

Researched 2026-09-18 by the local agent. MiniCPM5-2B came out on 12 September 2026. The
companion proposal is `research/proposal-small-model-automation.md`. For the Jev / System One
background, see `research/system-one-models-jev.md`.

## The short answer

- **MiniCPM5-2B is really good at picking tools, and small.** In my tool-calling test it chose the
  right tool with the right details **5 out of 5 times**, the same as Gemma-4 E4B and Qwen3.5-4B.
  It did it about twice as fast, using about 60% of the graphics memory (**~1.8 GB vs ~2.9-3.4
  GB**).
- **It isn't plug-and-play with becky yet.** It writes tool calls in its own XML style, and
  becky's llama.cpp (build 9551) doesn't translate that style. So 4 of the 5 answers came back as
  plain text. The answers inside were right; the plumbing is what failed. That's fixable (see the
  proposal).
- **It is weak at quick "Jev-style" yes/no decisions.** It got 2 of my 4 right, and the
  independent openjev study agrees: **0.686 for MiniCPM5 vs 0.813 for Qwen3.5-4B**. That gap isn't
  "slight". It's about 13 points, and 0.637 vs 0.845 on agreement with Jev. So it's **not** the
  model for becky-decide, unless it's fine-tuned for that job.
- **"Beats the 4B models" is the vendor's claim, measured in thinking mode.** MiniCPM5 was trained
  to reason at length before answering (400B tokens of that), and the headline scores come from
  letting it do so. That's slow. With thinking switched off, which is how becky would call it for
  quick jobs, the independent numbers don't show it beating the 4B models.
- **Needle is interesting but unproven here.** The real model is 121M parameters (a 35 MB file),
  not 29M. The 29M slice is only good after fine-tuning. It runs on a Windows CPU with no graphics
  card, but it **sends telemetry by default**. Not tested.
- **The Katanemo models' training data was never published.** What's worth borrowing is their
  prompt style and how they test routing. The models themselves are older than what becky already
  runs.

---

## 1. MiniCPM5-2B, the facts (verified on the Hugging Face Hub)

| | |
|---|---|
| Maker | OpenBMB (the MiniCPM team) |
| Size | 2.52B parameters (1.98B without the vocabulary table). The "2B" label rounds down |
| Design | Standard Llama layout, so llama.cpp loads it with no special support |
| Context | 131,072 tokens |
| License | **Apache-2.0**, fine for anything |
| Official GGUF | `openbmb/MiniCPM5-2B-GGUF`. Q4_K_M is **1.56 GB** (downloaded to `X:\HuggingFace\models\openbmb\MiniCPM5-2B-GGUF\`) |
| For comparison | Gemma-4 E4B QAT is 4.2 GB on disk; Qwen3.5-4B UD-Q4_K_XL is 2.9 GB |
| Thinking mode | On by default. `chat_template_kwargs.enable_thinking=false` turns it off, the same switch becky already uses for Qwen |
| Tool calls | Writes XML-style calls. The maker recommends SGLang, whose `minicpm5` parser turns them into standard calls. SGLang is a Linux server, which doesn't suit this PC |
| llama.cpp tip from the card | Set `min_p=0.0`. llama.cpp's default of 0.05 can make it repeat itself |
| Open training data | Yes: `openbmb/UltraData-SFT-2605`, `UltraData-SFT-Agent-2609`, `UltraData-RL-2609`, and more |

**The vendor's own table (thinking mode, self-reported):**

| | MiniCPM5-2B | Qwen3.5-4B | Gemma-4-E4B-it |
|---|---|---|---|
| Average of their suite | **53.9** | 51.1 | 31.2 |
| BFCL v4 (tool calling) | **66.6** | 56.8 | 47.0 |
| τ²-Bench Telecom (agent tool use) | **97.1** | 92.1 | 20.8 |
| LiveCodeBench v6 (code) | **69.1** | 56.4 | 53.9 |
| AIME 2025 (math) | **86.5** | 78.8 | 37.1 |
| MMLU-Pro (knowledge) | 70.8 | **78.0** | 68.3 |
| IFEval (following instructions) | 86.7 | **90.2** | 44.4 |

Read this with care:

- Every number was run by the maker.
- Gemma-4 E4B scoring 44.4 on instruction-following is surprising for an instruction-tuned model,
  which suggests the test setup did not suit every model equally.
- Qwen3.5-4B still wins on knowledge and on following instructions.

## 2. My tests on this PC (RTX 3070 Laptop, llama.cpp build 9551, all Q4 GGUFs)

### Test A: quick Jev-style decisions (same four cases as `system-one-models-jev.md` §5)

| Case | Right answer | MiniCPM5-2B | Qwen3.5-4B | Gemma-4 E4B |
|---|---|---|---|---|
| Line 42 re-says line 41 | yes | 0.41 (wrong way) | 0.40 (wrong way) | **0.83** |
| Line 42 is a new sentence | no | 0.24 (right, but weak) | 0.08 | **0.006** |
| "cut the dead air..." | roughcut | search at 47% (wrong) | shorts at 46% (wrong) | **roughcut at 99%** |
| "do the thing with the stuff..." | none | **none at 93%** | search at 38% | search at 77% |
| Time per decision | | **48-82 ms** | 155-200 ms | 83-112 ms |

MiniCPM5 was the only one to say "none" on the vague request, which is a genuinely good sign. It
missed both the retake and the routing case, though. For becky-decide, Gemma-4 E4B stays the pick.

### Test B: tool calling (`research/small-model-toolcall-probe.py`)

Four becky-style tools: transcribe, roughcut, search_footage, and schedule (which has a fixed
task list and a 24-hour time). Five requests, including one two-step request and one that no tool
fits.

| | MiniCPM5-2B | Qwen3.5-4B | Gemma-4 E4B |
|---|---|---|---|
| Right tool and right details | **5/5** | 5/5 | 5/5 |
| Usable through becky's llama.cpp today | **1/5** (the XML calls come back as plain text) | 5/5 | 5/5 |
| Time per request | **180-305 ms** | 538-854 ms | 317-542 ms |
| Load time | **1.6 s** | 3.1 s | 3.1 s |
| GPU memory in use (includes ~0.8 GB idle) | **2.6 GB** | 4.2 GB | 3.7 GB |

What MiniCPM5 actually wrote for the two-step request:
`name="roughcut"> name="folder">X:\footage\24_new  name="transcribe"> name="path">X:\footage\24_new\a.mp4`.
Both calls are right and in the right order. It also turned "11pm" into `23:00` correctly.
llama.cpp strips the special tokens around the calls, so becky would need either a llama.cpp that
parses MiniCPM5's format or its own small reader. I did not check whether a newer llama.cpp adds
that.

**Honest limits:** 4 + 5 hand-written cases is a smoke test, not a benchmark. All runs used
temperature 0 and one quantisation (Q4_K_M). I did not test thinking mode, long inputs or
Chinese.

## 3. What the Jev-reproduction tracker adds

`huggingface.co/spaces/multimodalart/jev-reproductions-tracker` lists about 32 open projects that
copy Jev's interface. Its own summary:

- **Nobody has Jev's weights or its training method (RLCD).** Every "openjev" is either a stock
  model with clever decoding or a head trained independently.
- **The best local study agrees with Jev about 74% of the time**, against Jev's own 87%, and finds
  that confidence "does not reliably flag errors."

The MiniCPM comparison you saw is **SemIf (formerly openjev), `TheoLeeCJ/openjev`**, tested on an
RTX 3090:

| | Authored decisions | Perturbed | Agreement with Jev |
|---|---|---|---|
| Qwen3-0.6B | 0.440 | 0.528 | 0.407 |
| MiniCPM5-2B | 0.686 | 0.693 | 0.637 |
| **Qwen3.5-4B** | **0.813** | **0.766** | 0.845 |
| Jev (published) | - | - | **0.883** |

Their speed result is also useful: reading the answer odds directly was **5.2 times faster** than
having the same model write a short JSON array, and sharing one state across 21 questions reached
20 decisions per second.

## 4. Would training it for the job change the result?

**Probably, yes.** decider-2b shows the size of the effect: fine-tuning a 2B base model took it
from **0.62 to 0.81** accuracy on 69 decision tasks. MiniCPM5 also publishes its own training data,
which decider did not have. But fine-tuning is a real project (PyTorch on Windows, a GPU for hours,
then evaluation). It isn't worth starting until becky-decide on Gemma has been measured on your
data and found lacking.

## 5. Needle (Cactus Compute), the "29M tool-calling model"

| | |
|---|---|
| What it is | A tiny model built only for tool calls, form extraction and embeddings. No chat |
| Real size | Full model **121M parameters, a 35 MB file**. Smaller slices can be cut from it: 98M, 52M, 29M |
| The 29M claim | Untrained, the 29M slice scores **11.7%** on phone actions. "Passes DeepSeek V4 Flash" applies only after fine-tuning on one app's tools |
| Runs on | CPU. Has a Windows x64 build. No GPU build. Their docs say "inference never touches the network" |
| License | Apache-2.0 |
| **Trap** | **Telemetry is ON by default.** It must run with `NEEDLE_TELEMETRY=0` and `DO_NOT_TRACK=1` to respect becky's offline rule |
| Where it is strong | Phone-action tool calls: 86.0%, beating models up to 1.2B |
| Where it is weak | General function calling (BFCL v4): 50.2%, behind LFM2.5-1.2B (62.0) and Qwen3.5-0.8B (56.8). Form extraction is well behind them too |

**Verdict:** it's worth trying only for a job where **no GPU is available**, such as picking an
action while a render holds the card. Even then it would need fine-tuning on becky's own tools.
Not tested here.

## 6. Arch-Router-1.5B and Plano-Orchestrator-4B (Katanemo)

- **What they are:** routing models. You give them a list of routes plus the conversation, and
  they return the route name(s) or "other" / an empty list. Arch-Router is Qwen2.5-1.5B tuned;
  Plano is Qwen3-4B-Instruct-2507 tuned and handles multi-turn, multi-intent requests and "no route
  needed". Both date from April 2026.
- **License:** Katanemo Community / Research License, based on the Llama 3.2 terms. It's usable
  locally, but it's not Apache.
- **Datasets:** **not published.** Both `katanemo` and `katanemolabs` have zero datasets on the
  Hub, and a Hub search found none either.
- **What's worth borrowing:** their prompt shape (routes as JSON lines, the conversation, "reply
  with exact route names, or an empty list"), and their test design. Plano was tested on 1,958
  messages across 605 conversations with 130+ agents, split into general, coding and long-context,
  with each message tagged for multi-intent, context-dependence and follow-up type. That's the
  shape a becky routing test set should have.
- **Adopt the models?** No. Gemma-4 E4B, Qwen3.5-4B and MiniCPM5 already routed 5/5 in Test B.

## Sources

- https://huggingface.co/openbmb/MiniCPM5-2B (card, table, llama.cpp and tool-calling notes)
  and https://huggingface.co/openbmb/MiniCPM5-2B-GGUF
- https://huggingface.co/spaces/multimodalart/jev-reproductions-tracker
- https://github.com/TheoLeeCJ/openjev (SemIf)
- https://github.com/cactus-compute/needle and https://huggingface.co/Cactus-Compute/needle3,
  https://cactuscompute.com/blog/needle-supported-devices
- https://huggingface.co/katanemo/Arch-Router-1.5B, https://huggingface.co/katanemo/Plano-Orchestrator-4B
- decider-2b numbers: https://github.com/Mapika/decider
