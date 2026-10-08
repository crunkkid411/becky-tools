# Jev in becky and the rest of Jordan's system: integration plan (2026-10-07)

Written by the local agent after becky-intake processed the 8 Jev videos Jordan saved on 2026-10-07
(notes + transcripts in `C:\Users\only1\Documents\Obsidian\browser_data\YouTube`). Nothing below is
built yet. Jordan said he is happy to pay for Jev and open to training a local copy.

## Bottom line

- **Jev fits becky.** becky is full of small "pick one / yes or no / how good" calls that today go
  to Gemma, which writes JSON and *guesses* its own confidence (for example the livestream editor
  asks Gemma for "confidence 0-100"). Jev answers those calls with real, calibrated odds, in under a
  second, for fractions of a cent.
- **Use three decision models, each where it is best:**
  | Model | Where it runs | Use it for |
  |---|---|---|
  | **Jev** (paid, TypeSafe) | Cloud | Text decisions about Jordan's own content and his agents. Cannot be trained; give it rich context instead. |
  | **Laya, trained on Jordan's own decisions** | This PC, CPU, 0 VRAM | The same calls once there are enough labeled examples, and anything that must not leave the PC (forensic evidence). |
  | **ImaJev 4B** | This PC, llama.cpp, ~3.4 GB VRAM | Decisions straight from a picture (Jev cannot see images). |
- **Start with ONE pilot and measure it** (below), not a rewrite.

## Update after two research passes (same evening)

Jordan, after reading the first draft: Jev is not necessarily the best model; workflows must run
unattended ("Requiring claude code to oversee it indefinitely defeats the purpose"); copy how others
use Jev before inventing; $5/month; use his OpenRouter key. Done: `internal/systemone` `Hosted`
calls `~typesafe/jev-latest` with the cap in code (live-tested 0.5 s, $0.000015).

**Models (verified: OpenRouter model list + Decision Index v0.3 data file, 2026-10-07):**
| Job | Hosted pick | Local pick (fits next to Whoretana) |
|---|---|---|
| Text decisions | Jev (`typesafe/jev-1.13`, DI 60.11, #3, most proven) and test Perplexity Decider v1.1 side by side (`perplexity/pplx-decider-v1.1-27b`, DI 62.75, #1, $0.02/M, released today) | JevK5 4B or Decider-4B GGUF (DI ~41) until a fine-tuned Laya exists |
| Picture yes/no | Perplexity Decider v1.1 (takes images) | ImaJev 4B (best open image decider) |
| Untrained Laya | - | DI 4.43 (#106): do not use untrained |
Pin a version id, not `-latest`, once thresholds are tuned. The installed llama-server (build 9551,
June) has no `/v1/systemone`; local decision models need b11361+ in a SEPARATE folder (Whoretana's
brain uses the current binary).

**What the other editors actually ask (from their code, quoted in the research):**
- Paul Borg (best take): Noul per line pair "`later` repeats the opening words of `earlier`, as a
  retake of the same sentence" (6 lines back, up to 240 questions in one request), then Noul per
  attempt "Does `take` finish its thought...?"; code keeps the LAST finished attempt, flags the rest
  `?`. He dropped a "clean delivery" question: on unpunctuated Whisper text it separated nothing.
- jevclip (keep/cut by value): 20 s segments, state = title + segment only; Choice `kind` (insight,
  evidence, method, concept, story, small-talk, promo, filler), Score `substance` (4 levels), Nouls
  `hype`, `standalone`, per-focus relevance. Cutting needs MORE certainty than keeping. A clip that
  opens on "and / second" pulls in the segment before it. Answers cached by request hash.
- jev-shorts-editor (vertical shorts): per 1-5 s beat, Nouls `needs_visual`, `punch_in`, Choices for
  template, emphasis word, transition, sound effect; never two punch-ins in a row (code).
- HyperEdit: Jev routes editing commands (one Choice over 15 workflows); dead air is plain ffmpeg.
- jevmeter: every Noul has yes/no definitions plus near-miss examples: held-out 94.5% -> 99.0%.

**Steps they do that becky does not (check each):** group retakes before choosing; re-transcribe
each kept span to prove no word was lost; a reason stored with every cut; answers cached so a
threshold change costs no calls; per-video thresholds; "failed = not judged", never "cut".

**How they stay unattended:** pinned model, retries, cache, a log of every decision with its odds,
shadow mode before a gate acts, fixed code rules first and the model only on the remainder, and a
plain fallback when the model is down.

**Training data format (Laya, `laya-train`):** one JSONL line per case:
`{"state": "...", "questions": {...same as a Jev request...}, "expected": {"dept": "billing", "urgent": true}}`.
1,200 cases took Laya from 0.36 to ~0.78; calibration needs 10+ held-out items per question type.
Full fine-tune is ~6-7 GB VRAM (estimate): run with Whoretana off, or on Kaggle's free GPUs.
Every `Hosted` call is already logged in this shape (`research/jev/log-YYYY-MM.jsonl`); Jordan's
corrections become `expected`.

**First trial (recommended): best take, Paul Borg's two questions, in shadow mode** on a recording
Jordan already edited, scored against his own cuts, Jev vs Perplexity Decider vs today's Gemma
labels. Then the jevclip value rubric for livestream keep/cut.

## What we had wrong before (corrects `system-one-models-jev.md`, 2026-09-18)

| We believed | Verified now |
|---|---|
| "Hosted Jev does not belong in becky: it costs money" | Jordan, 2026-10-07: he is happy to pay for Jev. The money rule needs a narrow, capped exception (see "Needs Jordan"). |
| "Laya says yes to everything, so it is bad at relevance" | Laya's own card: base checkpoints are near chance zero-shot (0.36 vs 0.77 after training on the task). It is "a fast base to specialise, not a zero-shot decision engine". becky-intake runs it untrained. |
| "No image decisions possible" | Jev is still text only (docs.typesafe.ai/models, 2026-10-07), but open picture decision models exist on Hugging Face: `mohit67890/imajev-4b` (Apache-2.0, Qwen3.5-4B LoRA) with GGUFs at `mindchain/imajev-4b-GGUF` (Q4_K_M 2.71 GB + mmproj 0.67 GB). `akhilaaa3/Jev-Omni` is Gemma-4-12B based, 7.38 GB at Q4: too big next to Whoretana. |
| Fine-tuning a decision model is a big project | The "tiny model beats Jev" video (ICtPrhMBUKA): Laya trained in about 4 minutes of GPU time went from 12% to 82%, beating Jev with thin context (67%); Jev with rich context won narrowly (87.4% vs 86.1%). Laya's card links a free Kaggle notebook that also fits the calibration temperature. |

Jev facts checked on docs.typesafe.ai today: $0.042 per million input tokens, output free; 64k
tokens per request; 100k tokens/s and 80 requests/s; text only; same weights for everyone (no
fine-tuning); not trained on customer requests.

## The 8 videos, one line each

| Video | What becky can take from it |
|---|---|
| I trained a tiny model to beat Jev (ICtPrhMBUKA) | Train Laya on our own decisions; calibrate with a temperature; rich state matters more than anything for Jev. |
| Jev is insane for video editors (lCMYkAxP3x0) | Clipfast: type a topic, get every moment about it as a captioned clip. = becky-moment by query. |
| Best Jev use cases for AI coding (qwnJJMNGwgY) | Jev as a pre-tool-use guardrail, as a test player, as a browser clicker, as a task router. Repo: `shitianfang/jev-use` (Claude Code plugin, hands no-text steps to Jev). |
| Jev edited this video (RkVIuEzAm7Q) | Jev picks the good take among repeated takes from the transcript; a big model only finishes the edit. 30 min -> 9.5 min. |
| Fixed your Claude Code setup's biggest flaw (jlEMo6Dsh9E) | Ready-made Jev hooks: `toolgate` (tool-call firewall), `jev-belay` (Stop hook that blocks an unverified "done"), `Jev_steer_or_queue`, `quicksilver`. |
| Jev changed video editing forever (xH2_VGvM8PI) | HyperEdit: every editing command hits Jev first, which picks the tool (cut dead air, captions, find media) and only hands odd requests to Claude. |
| JEV finally solved AI video editing (ZlICPWwgmmg) | Transcript -> Jev decides scenes (face vs screen) -> code builds -> a check loop -> render section by section. |
| Image decision models for RPA (L8YxigQoLaM) | ImaJev 4B answers yes/no and pick-one questions from a raw image, with an "unknown" option; models differ by question type, so test each question. |

## Proposals, best first

### Video editing (Vegas pipeline)

1. **PILOT: livestream sentence labels with Jev.** `cmd/livestream/select.go` asks Gemma to label every
   sentence (narrative, chat reply, super chat, break, retake, meta) and pick its topic, then write a
   0-100 confidence. Replace with one Jev request per window of sentences: a Choice for the label and
   a Choice for the topic, with the guidance and neighbouring sentences as state. Code keeps the keep
   rule (`keeps()`); low-odds sentences go to the existing second model, never onto the timeline as a
   guess (the marker rule in becky's CLAUDE.md). Labels to score it against already exist: Jordan's
   own finished edits of the 27-livestream and the apology livestream. Cost: a 1-hour stream is about
   900 sentences x ~2k tokens = ~1.8M tokens = **about 8 cents**.
2. **Best take among repeats** (Paul Borg's pattern). Code groups repeated attempts by text
   similarity; Jev picks the finished one (a Choice over the group). Feeds becky-cut / roughcut.
3. **Picture questions with ImaJev 4B, locally.** `picture.go`'s "is he holding something up?" and
   `publish.go`'s frame check now ask Gemma for JSON. ImaJev returns calibrated yes/no from the frame.
   Used WITH MediaPipe / Falcon / insightface, never instead of them (LESSONS, 2026-10-06).
4. **"Find every moment about X"** (Clipfast). becky-moment: Jev scores each transcript passage
   against Jordan's query; code merges the hits into clips; a model watches the result before it ships.
5. **Editing commands by voice.** "Whoretana, cut the dead air" -> Jev picks the becky workflow from a
   fixed list (HyperEdit's pattern), with a confidence gate that hands anything unclear to Claude.

### Agents and the rest of the system

6. **Claude Code hooks Jordan already wants in prose today:** `jev-belay` blocks "done" without
   evidence (his rule 2: it must actually work); `toolgate` screens risky tool calls. Hooks, not
   reminders (LESSONS, 2026-10-05). Try each in shadow mode first (log what it would block).
7. **becky-ask / becky routing.** "Which workflow handles this request?" is a Choice; today it is an
   LLM call or keyword rules.
8. **Dark-factory lookout.** Every few minutes: is this agent stuck, looping or off task? Stops a run
   before it burns the Claude plan. A lookout, never the judge of a merge.
9. **becky-intake itself.** (a) Route with Jev or a trained Laya instead of untrained Laya. (b) Add
   the step Jordan asked for: Jev picks which becky area each saved video could improve and scores
   how directly; only high scorers get a written proposal (Gemma or Claude writes it). Today
   becky-intake cannot write integration proposals; this plan was written by hand.
10. **Browser agent.** `jev-browser` / `browser-use/jev-ultrafast` pick the next click with odds; a
    candidate for the agent Firefox's stuck-page helper. Lower priority: the current setup works.

### Training our own (when a pilot has labels)

Every Jev call becky makes is logged with its answer and, once Jordan reviews the edit, the right
answer. Those pairs are the training set for a local Laya per job (split train / check / calibrate /
final-test, as in ICtPrhMBUKA). Forensic work stays local-only from day one: no evidence goes to Jev.

## Needs Jordan (the only blockers)

1. ~~TypeSafe key~~ DONE: his OpenRouter key works (`/api/alpha/decisions`), no TypeSafe account needed.
2. ~~Money exception~~ DONE: $5/month, enforced in `internal/systemone` `Hosted`, written in CLAUDE.md.
3. **An answer key for the first trial:** a recording Jordan cut himself (not one becky cut), so the
   best-take and keep/cut questions are scored against HIS choices.
4. **Allowing a 2nd paid model:** the code accepts Jev only. Testing Perplexity Decider v1.1 side by
   side needs his OK to widen the guard (same $5 cap).
