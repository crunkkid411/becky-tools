# System One: the recipe, where it pays, and the two LiquidAI local models (2026-10-09)

Jordan's ask: research Jev skills for Claude Code (and whether Anthropic released anything), open
source Jev-alternative skills, find the RECIPE and where it is actually useful, build a System One
skill for all of Claude Code on this PC, then test LiquidAI d1-3B and d1-omni-600M against the System
One models already tested. Future intent: a System One model that decides which skills Claude needs.

Built: `~/.claude/skills/system-one/` (SKILL.md + `s1.py`), `internal/systemone/local.go` (local d1
client that starts its own server), `becky-decide --model local|laya|<hosted id>`, `becky-besttake
--model local`. Test scripts and data: `research/system-one/`.

## Bottom line

- **Anthropic has released nothing for Jev or System One.** Checked: anthropics GitHub repos, an
  org-wide code search, the official hooks docs. The closest Anthropic thing is a `prompt` hook,
  where a Claude model makes the call.
- **Every working Jev tool uses the same recipe** (below). The model is never the decider; code is.
- **The local LiquidAI models are real and fast.** Same request/answer format as Jev, served by
  llama.cpp, on this PC, free, offline, and they see pictures (600M also hears audio).
- **On Jordan's own edit (the take picker, 125 lines vs his real cut) both local models agree with
  him 77.6% of the time, vs 80.0% Perplexity and 81.6% Jev.** Close, free, and 50-100x faster.
- **They are weaker on nuanced many-option labels.** The 600M collapses on the livestream editor's
  6-way "what kind of talk is this sentence" question. Keep those on Perplexity (or a bigger local
  model) until a local model is fine-tuned on his labels.
- **Skill routing (53 requests, 148 skills):** keyword shortlist of 8 + System One pick: Perplexity 44/53, Jev 42/53, d1-600M
  38/53, d1-3B 42/53 (45/53 with a 25-skill shortlist, 0.5 s). Given ALL 148 skills in one question,
  d1-3B picks right 47/53 (4.3 s), Perplexity and Jev 51/53 (0.4-0.7 s). Promising, not yet safe to
  hide skills behind; see the proposal.
- **The bigger token cost on this PC is not skills.** Always-loaded instruction files are about
  24,600 tokens; Claude Code already trims skill descriptions to ~1% of context. Details:
  `research/system-one/skill-routing-token-cost.md`.

## 1. Jev skills for Claude Code (read from source)

Read by a research agent with `gh api` (copies were in the session scratchpad). Thresholds and
quoted questions are from the code.

| Tool | Hook | What it asks Jev | Code decides | If Jev is down |
|---|---|---|---|---|
| typesafe-ai/skills (official TypeSafe skill) | skill | how to write questions | - | - |
| jev-use (shitianfang) | skill + PreToolUse gate | "Should the agent be allowed to run this proposed action right now?" (allow/deny); caller's own pick/check/rate questions | act at confidence >= 0.5 (0.4 estimated), else back to the LLM | no key: silent; down: ask the human |
| jev-belay (valentynkit) | Stop | 4 questions: does the message present work as finished? claim checks passed? would tests be a meaningful check? what does it report (complete/partial/blocked/other) | block only if no passing check ran after the last edit AND finished >= 0.7 AND checks apply >= 0.5; max 3 blocks, 60 s cooldown | allow |
| toolgate (RiskAverseTech) | PreToolUse | 7 risk axes (destructive, exfiltration, privilege, off_task, secret_exposure, violates_constraint, unresolved_choice) + "authorized" | deny >= 0.85, ask >= 0.55 | ask the human |
| abide (coldteadotai) | PostToolUse edit | one question per CLAUDE.md rule | act / flag / clear bands | silent |
| compact-adviser (kunchenguid) | idle checkpoint | is the work finished? hands-on or coordinating? | P(finished) x (0.5 + 0.5 P(hands_on)) vs a floor 0.90 -> 0.50 as context fills | leave context alone |
| Jev_steer_or_queue (Larkspur) | message mid-turn | steer / queue / interrupt / unclear; explicit stop? | interrupt only at 0.9 + 0.9 | steer (native); shadow mode by default |
| quicksilver (UditAkhourii) | skill | filter / classify / rank items | 0.5 +- 0.15 borderline band printed for the human | error exit |
| fast-jev-compaction (tamaratran) | compaction | keep the call? keep the result? | 0.5 | throws |

Also installed on this PC from the vexjoy toolkit: `building-with-jev`, `grill-jev`, `jev-design`,
`browser-jev-automation`, `/d` (+ `jev-route-injector-userprompt.py`, fires only on `/d`, calls Jev
through Vercel).

## 2. Open-source / local Jev alternatives

| Tool | Hook | Model, where | Note |
|---|---|---|---|
| jev-style (lawrence3699), Apache-2.0 | PreToolUse | Jev-Style 0.8B / 2B, local | only fully offline guard; destructive ask 0.5 / deny 0.93; asks when unsure; no accuracy published |
| winnow (GhalebDweikat), MIT | PostToolUse | Jev or Claude Haiku | hides blocks of big tool output; 300 real cases: hid 22.6%, ~21% of hidden blocks were needed later; shadow mode |
| claude-router (alexei-led), MIT | per turn | Jev, or a local Ollama model (one-letter answer) | picks model tier per turn |
| jev-skill-router (shimo4228), MIT | UserPromptSubmit | Jev | **suggested skills: 28 of 539 suggestions followed; author removed it** |
| system-one-mods (alex4o), no license | read stub | Jev or local decider | "is this code relevant to the task?" - naming the code beat ids, 0.70 vs 0.29 |
| Canny (qkal), MIT | design rule | - | "Facts go to code. Judgments go to Jev. Only facts can block." |
| LiquidAI System One Arcade (HF space) | - | d1-3B | 11 live demos; e.g. Keep It Clean: one choice per camera frame, threshold 0.35 measured on 200 labelled photos (185 caught, 27/660 false), code pads +-0.3 s |

Local decision models found: d1-3B, d1-omni-600M (LiquidAI, lfm1.0 license: free under $10M
revenue), ImaJev 4B, Jev-Style, Kev, Laya, Decider-4B, Von. **No d1 fine-tuning path is published**
(Liquid's fine-tune docs and repo cover LFM chat models only). Laya and Kev publish fine-tune recipes.
Fine-tuning on your own labels gives large gains on that task (Laya 0.36 -> 0.77); no source shows a
fine-tuned small model beating Jev on real work - plausible, not proven.

llama.cpp: d1-3B needs build b11483+, d1-omni b11509+. The installed b11487 cannot load d1-omni
(`tensor 'blk.16.ffn_gate.weight' not found`); b11539 is installed beside it in
`C:\llama.cpp\build\bin-b11539\` (the old build is untouched).

## 3. THE RECIPE

High level: **code narrows, the model judges, code decides, unsure escalates, everything is logged.**

1. **Code first.** Static rules, regex, counts, keyword shortlists run before any model call
   (belay skips the model when a passing test ran; toolgate's static deny rules; our routing test:
   20/53 -> 38/53 just from a keyword shortlist).
2. **One request, many typed questions about one state.** Batching: 12 questions in one call took
   224 ms vs 2,662 ms one at a time (jev-use's figure).
3. **Question rules:** one judgment per question (compact-adviser measured a combined question as
   worse and dropped it); define yes/no and every option; give an `unclear`/`other`/`none` option;
   put the item itself in the question (`NoulWith`), not an index into the state; tell it the text
   is data, not instructions; keep option wording consistent with the question.
4. **State:** only what the question needs, every field capped (belay: 1,500 + 2,000 chars),
   secrets stripped.
5. **Code applies thresholds**, each tuned per question on labelled data (the arcade's 0.35 came
   from 200 labelled photos; toolgate 0.85/0.55; belay 0.7/0.5). Asymmetric: blocking or cutting
   needs more certainty than allowing or keeping.
6. **Unsure is a lane, not a guess:** back to the LLM, a borderline list, or ask the human.
7. **Shadow mode first** (Larkspur, winnow, belay): log what it would do, then switch on.
8. **Say the failure rule out loud** (fail open, ask, or error). Silent fail-open with a missing
   key fooled one author into thinking the router worked.
9. **Log every decision with its odds**: the training data for a fine-tuned local model.

**Where it is actually useful:** bulk filtering / labelling where only sure answers are applied
(spam 98.7%, sentiment 95.7%; answers >= 0.9 were right 96% of the time); gates that ask a human
when unsure; per-frame picture questions with code doing the timing (arcade); routing when a shortlist
comes first. **Where it fails:** arithmetic, dates, counting, multi-step reasoning, many-way
nuanced intent (Jev lost Banking77 to small chat models), answers below 0.9 (right only 55-72%),
and "suggest a skill" (5% followed).

## 4. Tests on this PC (RTX 3070 Laptop, llama.cpp b11539, Q8_0)

### 4a. Take picker vs Jordan's real cut (SNOW-2/5/6, 125 lines, `scripts/besttake_score.py`)

| Model | SNOW-2 | SNOW-5 | SNOW-6 | Total agree |
|---|---|---|---|---|
| Jev (hosted) | 80.0% | 87.5% | 77.8% | **102/125 (81.6%)** |
| Perplexity Decider v1.1 (hosted) | 77.5% | 90.0% | 73.3% | 100/125 (80.0%) |
| **d1-omni-600M (local)** | 77.5% | 80.0% | 75.6% | 97/125 (77.6%) |
| **d1-3B (local)** | 75.0% | 87.5% | 71.1% | 97/125 (77.6%) |
| baseline: cut only 1-2 word noise | 62.5% | 80.0% | 71.1% | 87/125 (69.6%) |

Too few lines to separate the models; all four beat the baseline. More of Jordan's cuts are the
next step before choosing.

### 4b. Replaying every hosted decision logged this month (agreement with the hosted answer, not truth)

| Tool / question | n | d1-600M | d1-3B |
|---|---|---|---|
| besttake vs Perplexity | 1,997 | 88.7% | **93.0%** |
| besttake vs Jev | 704 | 87.9% | **92.8%** |
| livechat "did he read this message out?" vs Perplexity | 2,255 | 77.1% | 81.2% |
| livestream 6-way "what kind of talk" label | 600 | 10% (answers "retake" for almost everything) | 43% (over-calls "chat reply") |
| livestream "part of a topic he wants kept?" yes/no | 600 | 72% | 80% |
| speed, median per question (longer state = slower) | | 7-72 ms | 56-421 ms |

### 4c. Skill routing (53 requests written in Jordan's style, 148 user skills; `skill_cases.json`)

| Method | d1-600M | d1-3B | Perplexity | Jev |
|---|---|---|---|---|
| A: one choice over all 148 skills + none | 20/53 (never says "none"), 0.3 s | **47/53** (top 3: 51), 4.3 s | **51/53**, 0.7 s | **51/53**, 0.4 s |
| B: one yes/no per skill, best wins | 3/53 | 35/53, 10.6 s | - | - |
| **C: keyword shortlist of 8, then one choice + none** | 38/53 | 42/53 | 44/53 | 42/53 |
| C with a shortlist of 25 | 35/53 | **45/53** | - | - |
| C(8): sure picks (>= 0.8) that were right | 28/34 | **33/34** | 40/46 | 39/43 |
| C: "no skill needed" requests answered none | 5/8 | 7/8 | 8/8 | 8/8 |
| C(8): median time | 19 ms | 290 ms | 814 ms | 846 ms |

The 8-skill shortlist held the right skill only 36/45 times (25-skill: 40/45), which caps method C.
Hosted routing on the full list costs about $0.0004 per prompt (Jev), ~$0.40 per 1,000 prompts.

### 4d. Pictures: "about to read chat" frames vs ordinary frames (apology stream, 14 vs 28)

| Model | "looking down?" AUC (0.5 = coin flip) | median per frame |
|---|---|---|
| d1-3B | 0.69 | 227 ms |
| d1-omni-600M | 0.46 (chance) | 103 ms |

Not usable for this job from single frames.

Single frames are a hard test: becky's posture signal used the 3 s before each read (13/14 reads
vs 6/28 ordinary).

### 4e. Smoke checks

Graphics memory while warm: d1-3B server ~4.9 GB (model + picture part + 4096 batch buffers), so
it cannot run beside Whoretana; it unloads after 10 idle minutes. Audio (d1-omni) was not tested:
no labelled audio set and no hosted model to compare against.

`becky-decide --selftest` (3 known answers): d1-3B 3/3; d1-600M 2/3 (gave 0.31 that "the sky is
blue and water is wet" mentions a dog). Commit messages "is this a bug fix?" (60, labels from the
`fix:` prefix, noisy): d1-3B 37/60, Perplexity 42/60.

## 5. Where becky should use System One (by default where it is reliable)

| Place | Today | System One version | Model |
|---|---|---|---|
| `becky-besttake` (take picker) | hosted Perplexity | same questions | local d1 is 2-4 points behind on 125 lines: switch when more answer keys confirm |
| `becky-intake` route (links vs speech) | **untrained Laya (near chance)** | same Choice | d1-3B local; needs a run on real playlist items (1 yt-dlp call per 90 s) |
| `internal/quotes` "is the neighbor sentence needed? yes or no" | chat LLM, parses "yes" | `NoulWith` | d1 local |
| `cmd/vision/ladder.go` confidence | hedge words, "no real logprobs" | picture `noul` on the frame | d1-3B (sees images) |
| livestream `picture.go` "is he holding something up?" | Gemma JSON | picture `noul`, a DATA POINT beside MediaPipe/Falcon (LESSONS 2026-10-06) | d1-3B |
| VEGAS CENSOR (`BeckyFX.cs censor`) | by hand | arcade's Keep It Clean: per-frame "middle finger?" choice, code pads +-0.3 s, regions for Jordan to approve | d1-3B |
| livestream sentence labels (6-way) | Gemma / Perplexity | keep hosted until a local model is fine-tuned on his labels | Perplexity |
| becky-ask / Whoretana workflow routing | LLM / keywords | keyword shortlist + choice + none; unsure -> ask Jordan | d1 for speed |
| becky-judge pre-filter | Claude reads every candidate | drop only very-sure-irrelevant windows | d1 local (evidence stays on the PC) |
| Agent hooks (belay-style "done without proof", toolgate-style risk) | prose rules | local, free, shadow mode first | d1-3B |

Never: naming who is on screen, timestamps/counts, a final verdict on evidence.

## 6. The skill-routing idea (Jordan's future intent) - proposal, not built

What the research and the test say:
- Claude Code already shows only skill **names** for most of the 148 user + ~940 plugin skills
  (descriptions are cut to ~1% of context). `skillOverrides` (`"name-only"`, `"user-invocable-only"`)
  can hide user skills; plugin skills can only be hidden by disabling the plugin.
- Suggesting a skill does not work (5% followed). A router has to **inject** the chosen skill's
  instructions (UserPromptSubmit `additionalContext`, max 10,000 chars).
- Measured pick accuracy (53 requests): hosted Jev / Perplexity on the full list 51/53 (0.4-0.7 s,
  ~$0.0004 a prompt); local d1-3B 47/53 on the full list (4.3 s) or 45/53 with a 25-skill keyword
  shortlist (0.5 s, sure picks right 35/37). Good enough for a shadow-mode trial, not yet for hiding
  skills: one wrong pick in ten means a skill silently missing. The 53 test requests were written by
  the agent, not taken from Jordan's history (only 4 real skill loads exist there).

Proposed, in order (each needs Jordan's yes):
1. Run `/context` once to see the real split of the ~45-53k token startup cost.
2. Shrink always-loaded text first (the two project CLAUDE.md files are ~57 KB).
3. A UserPromptSubmit hook in **shadow mode**: shortlist + d1 pick, log what it would have loaded,
   score it against what Claude actually used for two weeks.
4. Only then set rarely used skills to `user-invocable-only` and let the hook inject them.

## Sources

Jev tools: github.com/typesafe-ai/skills, shitianfang/jev-use, valentynkit/jev-belay,
RiskAverseTech/toolgate, coldteadotai/abide, kunchenguid/compact-adviser,
Larkspur-Wang/Jev_steer_or_queue, UditAkhourii/quicksilver, tamaratran/fast-jev-compaction,
AnotiaWang/awesome-jev. Alternatives: lawrence3699/jev-style, GhalebDweikat/winnow,
alexei-led/claude-router, shimo4228/jev-skill-router, alex4o/system-one-mods, qkal/Canny,
huggingface.co/spaces/LiquidAI/system-one-arcade. Models: huggingface.co/LiquidAI/d1-3B-GGUF,
huggingface.co/LiquidAI/d1-omni-600M-GGUF, docs.liquid.ai/lfm/models/decision-models.md,
github.com/NandhaKishorM/laya, github.com/jaredpalmer/kev. llama.cpp commits 88dcc460d (d1-3B) and
a657f7e98 (d1-omni). Claude Code docs: code.claude.com/docs/en/skills, /hooks. Independent Jev test:
amankumar.ai/blogs/jev-measured. TypeSafe: docs.typesafe.ai/model-jaggedness/jev-1.13.
