# HANDOFF — make becky-tools SELF-REGULATE (wire `internal/orchestrate` into the entry tools)

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
