---
name: system-one-intake
description: How to use a small "System One" typed-decision model (Laya, the open Jev alternative) to route work cheaply, with code owning every fact and every safety rule. Use this whenever a tool or agent must pick between a few fixed options (which route, which tool, which department, is this a short repo pointer or a real tutorial) and the choice should not cost an LLM call, GPU memory or generated text, even if the user never says "System One", "Jev" or "Laya".
source_type: video
source_path: https://www.youtube.com/watch?v=GeYevz27gyc
source_sha256: 67a334c37a03022e97111c6f9ae4a3e99323f41efb54605e368a246a26463bd7
extraction_date: 2026-09-25
status: source-grounded
---

# System One intake

A System One model answers typed questions (pick one of these options, score this, does this apply)
with calibrated probabilities in one forward pass. It writes no text, so it is fast (about 0.5 s for
three questions on CPU here) and costs no GPU memory. It is a router, not a thinker.

The video is a roundup of 35 repos, not a tutorial; the skill below is the pattern that several of
them demonstrate independently (shapeshift, JevRouter, arc-cua, fast-browser-use, laya-mlx).

## The one rule: the model decides, code computes

- The model picks **which** option. Code computes **every value**, every count, every date, every
  permission. ("Jev decides (which card, which variant). Code computes (every value on it)." -
  shapeshift.) Laya cannot count or do arithmetic, reads wording literally, and gets worse when the
  state holds irrelevant text. So measure facts in code and give the model only what it judges.
- **Decision layer vs policy layer** (JevRouter). The model owns the probabilities. Code owns
  availability, permissions, risk and confirmation, filters the answer afterwards, and never
  re-normalises the filtered probabilities. Log every decision.
- **Legal action set** (arc-cua, fast-browser-use). Build the candidate list in code from what
  really exists (visible controls, repos actually linked), so the model cannot invent an option.
  When it is stuck or unsure, hand back to the bigger model or to the human.
- **Asymmetric guard.** Decide which wrong answer is expensive, and require two agreeing signals
  (a code fact AND model confidence) before taking that branch. Every doubt goes to the safe branch.

## How to run it locally (becky)

- `becky-decide` takes a Jev-shaped request on stdin and returns JSON probabilities. Model:
  `models/laya/` (receptron/laya-onnx rev 68f27dfe, ONNX, CPU). `becky-decide --selftest` must print
  three passing cases before you trust a new install.
- In Go, call `internal/systemone`: `systemone.Choice(question, options...)`, `Runner.Decide`.
- Worked example: `becky-intake` (the "ai-useful" playlist). Code measures length, links, GitHub
  repos and chapters. Laya answers one question: is the value in the linked projects or in what is
  said? Code allows "links" only with at least 3 repos AND Laya at 70% or more; otherwise it
  transcribes. A wrong "links" loses the video; a wrong "speech" only costs a transcription.

## What it is bad at (measured here, 2026-09-25)

- Nuanced relevance ("is this repo useful to Jordan?") saturated: it said yes to nearly everything
  (AUC 0.45-0.59 across three prompt designs). Use embeddings for similarity instead (Qwen3-Embedding
  0.6B on llama.cpp, AUC 0.64, about 5 s on CPU).
- The receptron README's example numbers did not reproduce (0.48 vs 0.94 for its billing case), even
  with a byte-exact input sequence. Trust your own selftest, not published numbers.

## How to tell it worked

- `becky-decide --selftest` exits 0 and prints three cases with the expected winners.
- For a new routing question: before wiring it in, run it over 10+ real inputs you have labelled by
  hand (a dry run, nothing written) and check both branches appear where you expect. `becky-intake
  --dry-run` is this trial for the playlist.
