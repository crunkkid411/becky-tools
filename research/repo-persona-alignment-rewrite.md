# PeachOrange/persona-alignment-rewrite

Source: https://github.com/PeachOrange/persona-alignment-rewrite | licence: not stated

Read for `shorts-user-feedback.md`: what step does this run that becky does not?
Written by a free model reading the source; the build/skip judgement is not its call.

---

## What it is
A packaged AgentSkill (`.skill` artifact) for an agent framework that triggers workflows around "阿昆-style" short-video copy tasks: persona-aware rewrite, diagnosis, title/keyword/comment-prompt generation, and iterative refinement from real samples. Input is copy tasks; output is rewritten/diagnosed copy plus metadata (titles, keywords, comment prompts). The actual skill source lives in `persona-alignment-rewrite/` and is distributed as `dist/persona-alignment-rewrite.skill`.

## The pipeline, in order
not visible in the files read — only the README and a packaged `.skill` binary are present; the skill source directory (`persona-alignment-rewrite/`) and its internal workflow stages are not provided.

## Models, libraries and services
| What | Stage | Local / Paid API |
|------|-------|------------------|
| not visible in the files read | — | — |

## Prompts
not visible in the files read — no prompt text appears in README.md; the skill source directory was not provided.

## How it decides WHAT to clip
not visible in the files read — the README describes "broad workflow triggering for copy tasks" and "iterative refinement from real samples" but contains no selection/ranking logic, thresholds, or code references.

## How it decides framing / cropping
not visible in the files read — the project is described as copy/workflow oriented (titles, keywords, comment prompts), not a video reframing tool.

## Multi-pass or iteration
README states "iterative refinement from real samples", but the actual iteration mechanism (re-run, self-check, refinement loop) is not visible in the files read.

## Steps here that a transcript-first clipper would MISS
- not visible in the files read — no concrete mechanical steps (silence snapping, visual-scene detection, etc.) are documented in the provided files.

## Worth stealing
- **No** — the only visible artifacts are a README and a compiled `.skill` binary; no source code, prompts, constants, or licence text are available to evaluate or reuse.
