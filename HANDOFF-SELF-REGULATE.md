# HANDOFF — make becky-tools SELF-REGULATE (wire `internal/orchestrate` into the entry tools)

> **STATUS — DONE + PROVEN on a real clip (2026-06-25, local).** `internal/orchestrate` is now wired
> into BOTH entry verbs through one shared runtime package `internal/forensicrun` (single source; the
> tool->claim mapping stays in `internal/forensic`):
> - **`becky-transcribe --forensic [--subject X] [--speakers N] [--kb dir]`** transcribes, then adds a
>   `"forensic"` block (corroborated names + watched on-screen intervals, maybes HELD). Default off, so
>   every existing consumer (becky-embed/clip/validate) is unchanged.
> - **`becky-ask --question "who is in this?" / "is X on screen?" --target <file>`** (single-shot)
>   routes straight into the same engine and returns ONE corroborated plain-English answer — not a
>   staged becky-identify command to chain. The colored TUI is intentionally untouched (a long model
>   run must never freeze the accessibility AID).
>
> **PROOF (real run on `fixture_2spk.wav`, 2 speakers):** the multi-speaker plan correctly included
> `becky-diarize`+`verify-with-gemma4`; `becky-identify` ran against `kb-final` and matched a single
> voice signal for "Hair Jordan"; the engine HELD it as a candidate (`names: null`) — *"only one signal;
> needs a second independent source to conclude"* — and the audit shows the Gemma-4 ladder fired BOTH
> levels (E4B then 12B) before leaving it held. `becky-ask` returned the honest *"No one could be named
> with corroboration … 2 candidate(s) held."* No false naming from a lone signal — the protocol enforced
> in code. 8 value-asserting tests in `internal/forensicrun` (+ tool tests) green.
>
> **Two real fixes made while wiring (not in the original cloud design):** (1) the executor escalates
> E4B->12B via the **`BECKY_AVLM_VARIANT=12b` env**, NOT a `--variant` flag (becky-validate has none — the
> sibling `becky-resolve` has this latent bug); (2) the runtime now passes **`--kb`** to becky-identify
> (env `BECKY_KB` -> `kb-final` convention), without which naming always degraded.
>
> **Left (the always-local model-boundary tuning, Jordan's footage):** point `BECKY_KB` at a real case KB
> with enrolled faces+voices and confirm a 2-modality match CONCLUDES a name on real video; tune the
> identify thresholds + the validate window-targeting on real footage. The deterministic wiring is done.
> The steps below are the original work order, kept for reference.

> **Goal (Jordan, 2026-06-26):** the forensic agent makes ONE dumb call; becky enforces every protocol
> deterministically and returns the FINAL corroborated output. No "ignoring" protocol — it's forced, in code.
>
> **Cloud already did + PROVED the hard part:** `becky-go/internal/orchestrate` is the deterministic
> protocol-enforcement engine (8 value-asserting tests green). It COMPUTES verdicts so the protocol can't be
> skipped: ≥2 independent signals to conclude, naming = corroborated identity, **presence needs a `KindWatched`
> signal**, and a forced validate→escalate ladder (Gemma-4 E4B → 12B). See SKILL.md "ARCHITECTURE".
>
> **This work order is the LOCAL step:** wire that engine into the entry tools with the real `becky-*.exe`
> outputs + the real Gemma-4 calls. Needs the PC (binaries + models). Build each step to a WORKING, verified
> state — not a stub. Never make Jordan run a CLI or answer a settled question (CLAUDE.md §2).

## What "done" looks like
`becky-transcribe <file>` (and the other entry verbs / `becky-ask`) returns ONE corroborated result with the
diarize/validate/escalate decisions made *internally*. A name only appears when corroborated; an on-screen claim
only when a model watched it. The forensic agent passes a file (and optionally known speakers) and gets the
finished answer — no flags, no chaining.

## Steps

### 1 — Map each becky tool's REAL JSON output to `orchestrate.Signal` / `orchestrate.Claim`
- **WHAT:** a `internal/orchestrate/adapt` (or in the entry tool) that turns each tool's actual stdout JSON into
  signals tagged to claims. You have the real shapes locally (run the tools); do NOT guess.
- **MAP (the evidence hierarchy → kinds):** `becky-identify` match → `KindPrint` (claim `speaker=<name>` or
  `face=<name>`); `becky-validate` per-frame watch → `KindWatched` (claim `onscreen=<subject>@[t0-t1]`,
  `IsPresence:true`); transcript mention → `KindMention`; `becky-motion` burst → `KindMotion`; `becky-ocr`/osint
  text → `KindText`. Carry each tool's confidence through.
- **VERIFY:** unit tests feed REAL captured tool JSON (small fixtures) → assert the right Signals/Claims. Value-
  asserted, no model needed.

### 2 — Implement `orchestrate.Executor.Validate` with the local Gemma-4 ladder
- **WHAT:** level 1 → Gemma-4 **E4B**, level 2 → **12B**. For a presence claim the model **watches** the segment
  (reuse `becky-validate`) and returns a `KindWatched` signal; for an identity claim it re-checks and returns a
  `KindPrint`/corroborating signal with its confidence.
- **VERIFY:** on a real clip, a low-confidence claim triggers E4B, and a still-unclear one triggers 12B (log the
  ladder). Degrade cleanly if a model is missing.

### 3 — Wire the entry tool: workflow → adapt → `orchestrate.Resolve` → final output
- **WHAT:** the entry verb runs the deterministic workflow (`internal/workflowdef`, e.g. `process-video` — diarize
  only if >1 speaker, using caller-supplied speakers when given), maps outputs (step 1), runs `Resolve` (the
  ladder + corroboration), and prints ONLY the corroborated result (+ candidates/unknowns held separately).
- **VERIFY:** `becky-transcribe <real file>` returns the finished corroborated output; a one-speaker clip skips
  diarize; a name shows only when corroborated; an on-screen claim only when watched. Run it; paste the output.

### 4 — `becky-ask` as the catch-all
- **WHAT:** when the forensic agent has no specific verb, `becky-ask "<plain English>"` routes into the same
  orchestrated path. Confirm it returns the same self-regulated output.

## The bar
"That's how this needs to work, or it simply does not work." A tool isn't done until one call returns the final,
validated answer with the protocol decisions made internally — proven on a real file, not "it compiles."
