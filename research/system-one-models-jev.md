# System One models and Jev: what they are, and whether becky should have one

Researched 2026-09-18 by the local agent. Jev launched in early access on 15 September 2026, three
days before this was written, so everything here is young. Sources are at the bottom.

## The short answer

- **Jev is a multiple-choice machine, not a writer.** You give it some text plus questions whose
  answers you have already fixed: yes/no, pick one, or rate 1 to N. It fills in the bubbles in
  0.1 to 0.5 seconds and tells you how sure it is. It cannot write a sentence and cannot answer
  off the menu.
- **The idea belongs in becky. Hosted Jev does not.** Paying TypeSafe would break three becky
  rules at once: becky runs offline, becky spends no money, and evidence never leaves this PC.
- **becky can already do this for free. I tested it on this PC today.** Gemma-4 E4B, the model
  becky already uses to check its own work, answered Jev-style questions in about 0.1 seconds each.
  It ran through the same llama.cpp server becky already starts, with no new model, no download and
  no cost. Qwen3.5-4B did the same thing but gave worse answers (section 5).
- **The payoff is honest confidence.** Today becky guesses how sure a model is by scanning its
  reply for words like "I think" or "possibly". becky-vision's own code says it does this because
  it has no real probabilities. A System One call gives a real probability instead, so "go from
  Gemma E4B to 12B when unsure" can use an actual number.
- **In a dark factory it fits as a lookout, never as the judge.** It can cheaply ask "is this
  agent stuck? off track? does it need a human?" every few seconds, and stop a looping agent before
  it burns your Claude plan. It must never be the thing that approves a merge.

---

## 1. What Jev is, in plain terms

| | A normal AI model (Claude, Gemma chatting) | A System One model (Jev) |
|---|---|---|
| What it does | Writes an answer, one word at a time | Fills in answers you listed in advance |
| Output | Free text, which code then has to parse | A typed value your code can use as-is |
| How sure is it? | You have to ask it, and it may bluff | Every answer carries its probability |
| Speed | Seconds to minutes | About 0.07 to 0.5 seconds |
| Can it go off-script? | Yes | No. It can only pick from your menu |

The name comes from Kahneman's *Thinking, Fast and Slow*. "System 1" is the fast gut reaction and
"System 2" is slow, careful reasoning. Jev is the gut. Claude is the reasoning.

**The three question types:**

| Type | Example | What comes back |
|---|---|---|
| **Noul** (yes/no) | "Is line 42 a retake of line 41?" | One number from 0 to 1: the chance the answer is yes |
| **Choice** | "Which workflow handles this?" plus your options | The pick, a chance for every option, and a confidence |
| **Score** | "How frustrated is this person?" plus levels | A position between the levels, the chances, and a confidence |

You can put many questions about the same text into one request. They are answered side by side,
and one answer cannot see another.

**How it is trained:** "RLCD", reinforcement learning for calibrated decisions. The goal is that
when it says 80%, it is right about 80% of the time. That is TypeSafe's claim; section 3 covers
how it holds up.

**The numbers, from TypeSafe's own docs:**

| | |
|---|---|
| Price | $0.042 per million input tokens ($42 per billion). **Output is free.** |
| Speed | 70 to 500 ms end to end (TypeSafe says 40 to 200 times faster than frontier models on these tasks) |
| Size of input | Up to 64k tokens per request. The text plus the longest single question must fit in 32k |
| Input types | **Text only.** No images, audio or video |
| Language | English is best. Other languages work, but less well |
| Options per Choice | Up to 255 |
| Rate limits | 1,200 requests a minute, and they say limits are "adjusting dynamically" |
| Access | Early access with a waitlist. Also sold on Vercel AI Gateway with no waitlist |
| Customising it | Not possible. Everyone gets the same weights; you steer it only through the wording of your questions |

**The request format matters.** It is one POST to `https://api.typesafe.ai/v1/systemone` with
`state` (the text), `model` and `questions`, and the response comes back as `answers`. The
open-source copies (section 4) use the same format, so a caller can switch between hosted Jev and a
local copy by changing one URL.

**What "can't hallucinate" really means.** Jev cannot invent an option or return a broken answer.
An independent check found 0 format errors in 8,576 answers. It **can** confidently pick the wrong
option. TypeSafe's own guide says so: *"Typed output guarantees the interface, not truth."*

## 2. Where Jev breaks: TypeSafe's own list for version 1.13

TypeSafe publishes a list of known weaknesses (they call it "jaggedness"). Every item below is
theirs:

1. **It takes wording literally.** It answers the question you wrote, not the one you meant.
2. **It is not a calculator.** Counting and arithmetic are unreliable.
3. **It reads dates as text.** "Which came first?" and "how far apart?" are unreliable.
4. **Multi-step reasoning is weak.** "A property of a property" costs accuracy.
5. **Irrelevant text hurts it.** Accuracy drops as the input fills with material that has nothing
   to do with the question.
6. **Text written to fool it can move it.** It does not treat its input as hostile.
7. **It gets confused when a question and its options disagree.**
8. **Its numbers don't add up across questions.** In their example, "refund?" scored 0.72 and
   "not a refund?" scored 0.47, which sums to 1.19.
9. **It cannot write.**

Their rule of thumb: code does the math, dates, counting and filtering, and the model does only
the judgement.

## 3. What independent testers found

| Test | Result |
|---|---|
| 4 public datasets vs OpenAI's small models (16,000 calls) | Jev won on spam (98.7%), sentiment (95.7%) and news topic (91.3%). It lost on 77-way banking intent (76.0% vs 78.7% and 81.7%). Median time was 0.8 to 0.9 s. One OpenAI mini call cost the same as 23 to 56 Jev calls |
| The same author's conclusion | **"A filter, not a classifier."** Answers at 0.9 or above were right 90 to 100% of the time; below 0.9 that fell to 55 to 72%. Trust the very sure answers and send the middle to something smarter. In their production use this cut expensive calls by 25 to 74% |
| ASSAY-001 (test design fixed in advance) | No format errors at all. Calibration **passed** on CLINC150 (ECE 0.02) and **failed** on Banking77 (ECE 0.09, overconfident). So it depends on the task: measure it on your own data |
| Phishing, 2,000 emails | **Claude Haiku 4.5 beat Jev** on accuracy |
| Search reranking, 9,831 pairs | Jev alone did not beat ordinary embedding search. Combining the two won |
| Zenn article (Japanese) | Reproduced the core trick on an ordinary small model (Gemma 3 270M), 77 times faster than having it write JSON. The trick can be copied. TypeSafe's calibration training cannot |

**Bottom line:** it really is fast and cheap. "As smart as frontier models" is only partly true.
It works best as a first-pass filter with an escape hatch to a smarter model or a human.

## 4. The open-source "Jev-like" options

| Project | What it is | Fit for this PC |
|---|---|---|
| **Gemma-4 E4B through becky's own llama-server** (what I tested) | Reads the odds for each option from the model becky already has | **Tested today. About 0.1 s per question** |
| [decider-2b](https://github.com/Mapika/decider) | Qwen3.5-2B fine-tuned for calibrated decisions. Apache-2.0, 1.9B parameters, about 4 GB. Same request format as Jev. 0.81 accuracy on 69 tasks, vs 0.62 for the same model untrained | **Not tested here.** Needs a PyTorch + CUDA install on Windows, plus kernels specific to Qwen3.5, which is a real install risk. Its fastest mode needs a newer GPU than the 3070 |
| [LitJev](https://github.com/zhengxuyu/litjev) | Any Qwen through Hugging Face transformers, same request format, no training. Probabilities are not calibrated | Tested by its author on a 27B model on an H100, far too big for this PC. The small-model version is the same trick I used |
| openjev, JEVfire (vLLM), jevmlx and PocketJev (Apple) | The same trick on other runtimes | vLLM and Apple's MLX do not fit this Windows PC |
| [jevlike](https://github.com/vinnylarouge/jevlike) | Train your own small scorer | Only useful if we ever want to train one |

## 5. My test on this PC (2026-09-18)

**What I did.** I started llama-server (build 9551) with each model and asked four Jev-style
questions. For each question the model gets one token, the options are labelled A, B, C, and so on,
and a grammar forces that token to be a valid letter. The probabilities come from the model's own
odds for each letter. Script: `research/system-one-probe.py`.

| Case | Right answer | Qwen3.5-4B | Gemma-4 E4B |
|---|---|---|---|
| Line 42 re-says line 41 | yes | 0.40 yes (wrong way, and unsure) | **0.83 yes** |
| Line 42 is a new sentence | no | 0.08 yes | **0.006 yes** |
| "cut the dead air out of the fbi recap clips" | roughcut | shorts at 46% (wrong) | **roughcut at 99%** |
| "do the thing with the stuff from yesterday" | none, so ask Jordan | search 38% vs none 37% (unsure, which is the right reaction) | search at 77% (**too sure: a warning**) |
| Time per question | | 155 to 200 ms | **83 to 112 ms** |
| Model load | | 5.6 s | 5.6 s |

**What this proves.** The mechanism works on this machine, with what is already on disk, and it
is fast. **Gemma-4 E4B is the right local model for it.** Qwen3.5-4B, which becky-ask uses for
routing today, is weak at this.

**What this does not prove.** Four hand-written cases are a smoke test, not a benchmark. The last
row is the warning: a model not trained for this can be confidently wrong about a vague request.
That is why thresholds have to be set on your real data (step 2 below).

**Checked:** when I let Qwen answer the "dead air" question in words, it wrote "C) shorts" and
argued for it. So the probability reader reports the model faithfully, and the model itself was
wrong.

## 6. Would a System One model help becky? Yes, in these specific places

Each item points at code that exists today:

1. **Honest confidence for the escalation ladder.** `cmd/vision/ladder.go` works out confidence
   from a fixed number per model (0.50 / 0.65 / 0.80 / 0.92), minus 0.25 if the reply contains
   hedge words. Its own comment says this is because there are "no real logprobs". A real
   probability fixes this. The same fix applies to CLAUDE.md's "use Gemma-4 E4B when confidence is
   low, still unclear then 12B" rule in every workflow.
2. **Spotting retakes in the rough cut.** Only literal retakes may ever be cut. "Is line B a retake
   of line A?" for each pair of neighbouring lines is exactly a yes/no question. Code finds the
   candidate pairs, the model judges them, and only the very-sure ones are acted on. The unsure ones
   go on a list for you and are never cut automatically. That follows the rule that a marker is an
   assertion.
3. **becky-ask and Whoretana routing.** "Which workflow is this?" is a Choice with a "none of these"
   option. If it's confident, becky runs the workflow. If not, it asks you a one-line question
   instead of guessing.
4. **A pre-filter for becky-judge, which saves your Claude plan.** Stage 2 sends every search
   candidate to Claude. A local yes/no first ("could this window be about <query>?") drops the
   obvious noise, so Claude reads fewer windows. **It must not replace the judge.** Coded language
   and nicknames ("green hair" = ...) need multi-step reasoning, which is weakness #4. It only drops
   what it is very sure is irrelevant, and that cut-off gets measured on real hits first.
5. **Fuzzy labels in becky-route.** `internal/autoroute/autoroute.go` already says "a model is
   optional gravy for fuzzy labels later." That would be a Choice over your buses for labels no rule
   matched, acted on only when confident.
6. **Clipping critic sub-questions.** The critic already watches the render with Gemma. Its verdict
   could be split into typed questions (is the subject in frame? is the crop sitting on a poster?)
   so the render check reads numbers instead of parsing prose. This needs image input: Gemma E4B
   has it and hosted Jev does not. Untested.

**Where it must not be used:**

- **Who is on screen.** A transcript-only yes/no can never prove someone is on screen. The existing
  rule stands.
- **Anything numeric.** Timestamps, durations and counts stay in code.
- **As a verdict.** One probability is a signal, never the final answer, the same as "a detector is
  a signal, never a verdict."

## 7. Proposal: build `becky-decide`, local, free and offline

**Guiding idea: reuse what exists.** `internal/llmlocal` already starts llama-server and sends it
chat requests. The one new thing is reading the odds on each answer instead of the words.

### Step 1: the tool

- A new package, `internal/systemone`. `Decide(state, questions)` returns answers in **Jev's exact
  JSON format**: state, questions, choice / noul / score, answers. Using the same format means we
  can later point it at decider-2b or hosted Jev by changing one URL, with no rewrite.
- **How it works:** one request per question. The state goes first so llama.cpp reuses it from
  memory across questions. Options are labelled with letters, the reply is capped at 1 token, a
  grammar allows only valid letters, and the letter odds are read and turned into probabilities.
  A Noul is a yes/no Choice. A Score is the expected level. Confidence is 1 minus the normalised
  entropy, which in plain terms means "how concentrated the odds are."
- **Model:** Gemma-4 E4B through `config.GemmaAVLM()`, text path only with no projector needed.
  It's the model becky already checks its work with. Temperature 0 and a fixed seed make it
  deterministic.
- **Degrade, never crash:** if there's no model, it returns a typed error and the caller keeps its
  current behaviour.
- **A `becky-decide` command:** JSON in, JSON out, exit code, plus `--selftest`, which runs the four
  cases above as regression checks. That's the house single-tool style.
- **Unit tests for the math** (odds to probabilities, confidence, expected level), which run with
  no model.
- **The usual gates:** go build, vet, test, gofmt, and `build-all-tools.bat`.

### Step 2: calibrate it on your data (this is what makes it trustworthy)

- Build a small labelled set from decisions you have already made: kept vs removed lines in past
  rough cuts, the 216-to-37 Gemma marker triage, and real becky-judge hits vs rejects.
- Measure accuracy and calibration for each kind of question. Fit one correction per question type
  and one threshold per action. jevcal's rule: find the lowest probability given to any real
  positive, then halve it.
- Thresholds live in code. If it's unsure, it lists the item for you and never acts on it.

### Step 3: wire it in one place at a time, each proven on real footage

In order: (a) the vision ladder's confidence, (b) rough-cut retake candidates, as a list only at
first, (c) becky-ask routing, (d) the becky-judge pre-filter.

### Later, only if step 2 shows Gemma's odds aren't good enough

Try decider-2b (Apache-2.0, trained for calibration). It needs a PyTorch install on Windows, which
is a real install risk and untested here.

### Hosted Jev: not proposed for becky

It would need your explicit OK to spend money (house rule). It can't see images or hear audio, it
needs internet, and evidence would leave this PC. For scale, if you ever want it for non-evidence
work: one check with about 8,000 tokens of input costs about $0.0003, so 1,000 checks is about
$0.34.

### Graphics card memory: measure this in step 1

Gemma-4 E4B is the same model Whoretana's brain runs. Loading a second copy while Whoretana is up
takes memory under the 8 GB ceiling. The option is to let becky-decide use an already-running
llama-server by URL instead of starting its own. I did not measure memory in today's test.

## 8. System One and the build-dark-factory skill

**What that skill is, in plain terms.** It's a recipe for turning a repo into one that takes a
GitHub issue in and ships tested code out, with nobody reviewing it. It builds in this order: rule
files, then a validation harness (the real work), then workflows, then deployment, then the timer
that switches it on. Its firm rules:

- The scheduler that picks the next job is dumb, predictable code, not an AI.
- At least two gates must be code the AI cannot talk its way past.
- The target is "level 3", where the code merges itself only when every one of those code gates is
  green.

**Where System One fits: the fast gut between the dumb scheduler and the slow, expensive agents.**

- **A lookout during a run.** The Foreman project already does this with Jev and Codex. Every few
  seconds it asks: is the worker stuck? drifting off the job? making progress? needing a human?
  That lets it stop or steer a looping agent early. **This is the biggest win for you:** runaway
  loops are what eat your Claude plan.
- **Triage.** "Is this issue inside the project's written scope?" gets in, out or unclear.
  Unclear waits for you.
- **Picking the model.** Easy tasks go to cheap or free models and hard ones go to Claude (the
  jev-router pattern).
- **Risk flags.** It can act as an extra tripwire on a diff, on top of the code guard and never
  instead of it.

**Where it must not go:**

- **Not the scheduler.** The skill forbids an AI there, because an AI "hallucinates dispatches for
  work that does not exist."
- **Not a merge gate on its own.** A probability can be confidently wrong, and gates must be code.
  It may only **block or escalate**, never **approve**.

**Honest note:** I have not run build-dark-factory. It refuses to start without a written product
brief (a PRD). becky-decide would be a component a future factory could call: the local, free way
to add those lookout checks.

---

## Sources

- TypeSafe launch post: https://typesafe.ai/blog/introducing-system-one-models-and-jev
- Jev 1.13 weaknesses: https://docs.typesafe.ai/model-jaggedness/jev-1.13
- API reference: https://docs.typesafe.ai/api.md
- Models, pricing and limits: https://docs.typesafe.ai/models.md
- Confidence: https://docs.typesafe.ai/confidence.md
- Official agent skill: https://github.com/typesafe-ai/skills/blob/main/skills/typesafe-ai/SKILL.md
- Community list: https://github.com/AnotiaWang/awesome-jev
- Independent test, "classifier or filter?": https://amankumar.ai/blogs/jev-measured
- ASSAY-001 calibration check: https://donttrustme.ai/assay-001.html
- The logit trick on Gemma (Japanese): https://zenn.dev/nwn/articles/824026c76116e0
- decider-2b: https://github.com/Mapika/decider and https://huggingface.co/Mapika/decider-2b
  (verified on the Hub: 1.88B params, Apache-2.0, base Qwen3.5-2B-Base)
- LitJev: https://github.com/zhengxuyu/litjev
- Foreman (Jev as a software-factory supervisor): https://github.com/thruwire/foreman
- build-dark-factory skill: `~/.claude/plugins/cache/cole-medin/skills/1.3.1/.claude/skills/build-dark-factory/SKILL.md`

## Jargon, one line each

- **Token:** a chunk of text, roughly three quarters of a word. Pricing and limits are counted in
  these.
- **Logprob / odds:** the model's own internal score for each possible next token, before it picks
  one. This is what a System One call reads instead of letting the model write.
- **Calibrated:** when it says 80%, it is right about 80% of the time.
- **ECE:** a single number for how far off the calibration is. 0 is perfect; 0.02 is good and
  0.09 is overconfident.
- **Grammar:** a rule given to llama.cpp that limits which tokens the model is allowed to produce.
