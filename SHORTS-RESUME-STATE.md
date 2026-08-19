# SHORTS WORK — RESUME STATE

Auto-written by the orchestrator so any session can pick this up with no input from Jordan.
Overwrite this file as things land. Newest facts win.

## The two jobs, in order

1. `HANDOFF-SHORTS-PIPELINE.md` §7 OPEN — items 1..6.
2. `shorts-user-feedback.md` — research 21 repos + the iPhone browser folder + the Reka-Edge
   question, then rebuild the pipeline to a standard Jordan would not fire an intern over.

## Recon done 2026-08-19 02:45 (facts, not plans)

- Branch `master`, clean tip `cf58c39` ("docs(shorts): bring the handoff up to date").
- `research/` has 34 files. **None** of the 21 repos in `shorts-user-feedback.md` has a file yet
  — that whole research pass is untouched.
- **OpenCode Zen key is NOT set on this machine.** Only `OPENROUTER_API_KEY` is present
  (user env registry + process env). `shorts-user-feedback.md` says to use Zen for the repo
  research; that is not currently possible. This matches `HANDOFF-SHORTS-PIPELINE.md` §6.1,
  which found the same thing on 2026-08-18.
  **=> Route the repo research through OpenRouter `:free` ids, or the local Gemma-4.**
  Free-only is enforced in code (`cmd/subtitle/openrouter.go` `isFreeModel`) — reuse that guard,
  do not write a new client.

## Next actions (ordered, no re-derivation needed)

1. Repo research: fetch each of the 21 GitHub repos (README + the files that carry the
   pipeline logic), summarise into `research/<repo>.md`. One file per repo. The question each
   file must answer is **"what step do they do that we do not, and do we need it?"** — build it
   or state why not. Cheap models only; this is summarisation, not judgement.
2. Reka-Edge-2603 vs gemma-4-12b-it-qat: is it worth adding as a second VL? GGUF exists
   (`Vastined/reka-edge-2603-GGUF`).
3. `C:\Users\only1\Documents\Obsidian\browser_data\iPhone` — anything AI+video, document + use.
4. Then the §7 OPEN items, hardest first: score saturation (#2) gates everything else, because
   until it is fixed no extra signal can move the ranking.

## Standing constraints for this work

- 29.97/30 fps analysis, never coarser. Sub-frame-rate analysis is why past tools were unusable.
- Test footage is `test-for-clips.mp4` (his content). `E:\TakingBack2007` is the CRIMINAL CASE
  — never use it for editing work.
- Compare cut timing against `becky-cut`'s jumpcut timing; that comparison is explicitly what
  the last output failed to do.
- Free APIs or the Claude Max OAuth session only. Never a paid endpoint.
