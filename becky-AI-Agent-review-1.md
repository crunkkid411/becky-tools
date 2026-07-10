# becky-AI-Agent-review-1

**Reviewer:** Claude (Fable 5) acting as a consuming AI agent, 2026-07-09 night
**Requested by:** Jordan, after a production failure
**Severity: P0 — the suite's core contract is not implemented in the tool tested**
**Audience:** the cloud review agent + whoever owns becky-tools orchestration

---

## 1. Executive summary

becky-tools' one promise — quoted from the project's own CLAUDE.md — is:

> "becky does ALL the thinking, deterministically, INSIDE the tool call. ...
> validate the result with Gemma-4 E4B when confidence is low; still unclear →
> escalate to Gemma-4 12B ... CORROBORATE ... returns ONE finished,
> corroborated result. The caller never sees the machinery."

Tonight, on a real task, `becky-vision` violated every clause of that promise
in a single call: it ran only the smallest model (LFM2.5-VL **450M**), returned
a **confidently wrong** answer, reported `"degraded": false`, exposed the
machinery (the model path) in its envelope, attached **no confidence score**,
escalated to **nothing**, and corroborated with **nothing** — while the entire
escalation ladder (1.6B, Gemma4-E4B, Gemma4-12B + vision mmproj) sat on disk
in `becky-go\models\`, and while a sibling tool (`becky-ocr`) could read the
correct answer off the same image with 0.97 confidence.

This corroborates the earlier forensic-agent incident. The pattern is the
same: the orchestration the docs describe is **not compiled into the tools**.
An outside agent cannot tell a becky answer from a hallucination, which makes
the suite unusable as a trust boundary — its entire reason to exist.

---

## 2. The reproduction case (all outputs verbatim)

**Task (real, not synthetic):** decide whether a screenshot shows a stuck
terminal. Ground truth: the image is a photo of a MissionControl console with
a Claude Code session frozen on an interactive permission prompt. The pixels
literally contain the words "Use skill \"claude-in-chrome\"?", "Do you want to
proceed?", "? 1. Yes", "Esc to cancel".

Image: `X:\AI-2\hj-mission-control\iCloud Photos\IMG_7725.JPEG`

### 2a. becky-vision, run 1 (default)

```
becky-vision --image "...\IMG_7725.JPEG" --prompt "What state is the terminal
application in this photo? Is anything stuck or waiting?"
```

> "The terminal application in this photo is in the "Finish" state. ... There
> are no visible icons, messages, or other indicators that would suggest the
> terminal is stuck or waiting for input. The terminal appears to be in a
> ready state, ready to accept user input..."
> (produced by becky-vision via local LFM2.5-VL: LFM2.5-VL-450M-Q8_0.gguf)

**Confidently wrong.** The screen is the textbook definition of "stuck
waiting for input."

### 2b. becky-vision, run 2 (`--json`, similar prompt)

```json
{
  "tool": "becky-vision",
  "image": "X:\\AI-2\\hj-mission-control\\iCloud Photos\\IMG_7725.JPEG",
  "model": "X:\\AI-2\\becky-tools\\models\\lfm2.5-vl-450m\\LFM2.5-VL-450M-Q8_0.gguf",
  "prompt": "Is anything on this screen stuck or waiting for input?",
  "description": "Yes, there are a few things ... The green-highlighted \"AI Finish\" button suggests that the script is currently in progress, but the user may need to press it to start.",
  "degraded": false
}
```

Note four things:
1. A **different answer than run 1** on the same image (mushy yes-ish vs
   confident no) — non-determinism with no uncertainty signal.
2. **No confidence field at all.**
3. `"degraded": false` — the envelope actively asserts health while producing
   an unvalidated single-small-model guess.
4. The `model` field leaks the machinery and confirms only the 450M ran.

### 2c. The corroboration that never happened: becky-ocr, same image

```
becky-ocr.exe -frames-dir <dir containing IMG_7725>
```

55 lines extracted. The relevant ones, with becky-ocr's own confidences:

| OCR text                              | confidence |
|---------------------------------------|-----------|
| `Use skill "claude-in-chrome"?`       | 0.99 |
| `Do you want to proceed?`             | 0.97 |
| `? 1. Yes`                            | 0.93 |
| `2.Yes, and don't ask again for`      | 0.98 |
| `Esc to cancel • Tab to amend`        | 0.97 |

The correct answer was available **inside the same suite, deterministically,
for free**, one corroboration call away. becky-vision never made that call.
"CORROBORATE MULTIPLE DATA POINTS" is a core principle of this suite; the
flagship perception tool corroborates nothing.

### 2d. The escalation ladder exists on disk and was never touched

`X:\AI-2\becky-tools\models\` contains, verified tonight:

- `lfm2.5-vl-450m\LFM2.5-VL-450M-Q8_0.gguf` (what ran)
- `lfm2.5-vl-1.6b\LFM2.5-VL-1.6B-Q8_0.gguf` + `mmproj-LFM2.5-VL-1.6b-Q8_0.gguf` (never ran)
- `gemma4\gemma-4-E4B-it-qat-UD-Q4_K_XL.gguf` + `mmproj-F16.gguf` (never ran)
- `gemma4\gemma-4-12B-it-qat-UD-Q4_K_XL.gguf` + `mmproj-12B-BF16.gguf` (never ran)
- bonus: `Qwen3VL-4B-Instruct-Q8_0.gguf` (never ran)

Every rung of the documented 450M → 1.6B → E4B → 12B ladder is installed.
The tool stops at rung zero, unconditionally, for a prompt that plainly
requires reading on-screen text.

---

## 3. Failure inventory (each one independently breaks the contract)

**F1 — No escalation.** Single fixed model regardless of task, confidence, or
ambiguity. The `--gemma` / `--qwen` flags push model selection onto the
CALLER, which is the exact anti-pattern the CLAUDE.md forbids ("one dumb
call... no flags"). Self-orchestration means the tool decides.

**F2 — No corroboration.** A screen with dense readable text is the canonical
OCR case. becky-ocr was never consulted. No second opinion, no cross-check,
no agreement score between sources.

**F3 — No confidence semantics.** The envelope has no confidence number, no
"validated: yes/no", no models-consulted trail. `degraded:false` is asserted
while the answer is an unvalidated guess — that field is currently worse than
useless because agents will trust it.

**F4 — Non-deterministic single-source answers.** Two runs, two materially
different conclusions, zero signal that the answer is unstable. A consuming
agent cannot detect this without re-running — which is the caller doing
becky's job again.

**F5 — Deployment gaps.** Neither `becky-vision.exe` nor `becky-ocr.exe` was
on PATH (`C:\Users\only1\bin`); they sat in `becky-go\bin\` where no agent
finds them. becky-vision "didn't exist" for every agent on this machine until
tonight, when it was manually copied. The install step belongs in
`build-all-tools.bat`, for every tool, unconditionally.

**F6 — Interface drift.** `becky-vision` takes `--image`; `becky-ocr` takes
`-frames-dir`/`-manifest` and rejects `--image`. Same suite, same input kind,
incompatible conventions. Every inconsistency is a place an agent (or a
workflow compiled into another tool) breaks.

**F7 — No machine-readable inventory.** There is no `becky list --json` that
enumerates installed tools + one-line contracts. Agents cannot discover what
exists; tonight's operator (me) claimed becky-vision didn't exist while it
sat on disk. Discovery must be a tool call, not tribal knowledge.

**F8 — The forensic-agent incident is the same bug.** A prior specialized
agent "ignored almost every becky protocol" (CLAUDE.md's own words). The
conclusion the repo already drew — orchestration must be COMPILED INTO the
tools, not suggested in prose — has not actually been applied to the
perception path.

**What already gets it right (keep this shape):** `search_library` and
`becky-perceive` (both added 2026-07-09) follow one-dumb-call with a JSON
envelope and honest nonzero exits. They still lack confidence/corroboration
fields, but their caller-facing shape is correct. `becky-ocr`'s per-line
confidences are exactly the raw material the orchestrator should consume.

---

## 4. Acceptance criteria for the fix (testable, no interpretation room)

The cloud agent reviewing this should hold becky-vision to ALL of these:

1. **One dumb call:** `becky-vision --image <path> --prompt "<q>"` with NO
   model flags must internally run the policy ladder and return one result.
2. **The canonical regression case:** on `IMG_7725.JPEG` (or a committed
   synthetic recreation - a screenshot of a terminal showing a "Do you want
   to proceed? 1. Yes / 2. No" prompt), the returned description must state
   the screen is WAITING ON A QUESTION / stuck on a permission prompt.
   Tonight's output is the failing baseline.
3. **Envelope (proposed):**
```json
{
  "ok": true,
  "answer": "...",
  "confidence": 0.0-1.0,
  "validated": true,
  "sources": [
    {"kind":"vlm","model":"lfm2.5-vl-450m","agrees":false},
    {"kind":"vlm","model":"lfm2.5-vl-1.6b","agrees":true},
    {"kind":"ocr","engine":"ppocr-v5","agrees":true,"key_lines":["Do you want to proceed?"]}
  ],
  "escalations": 2,
  "degraded": false
}
```
   `degraded` may only be false when the policy actually completed. The
   `sources` trail is not "machinery leaking" — it is the audit trail the
   caller's TRUST requires; the caller still makes exactly one call.
4. **Escalation policy compiled in (not prose):** 450M answers; if the prompt
   implies reading text/UI state OR self-reported certainty is low OR OCR
   disagrees, run OCR + 1.6B; still ambiguous -> Gemma4-E4B (mmproj present);
   still ambiguous -> Gemma4-12B. Budget cap + `"escalations"` count in the
   envelope. Thresholds in one config file, defaults sane.
5. **OCR corroboration is mandatory** whenever text is detected in-frame
   (cheap detector or unconditional for screenshots): key OCR lines feed the
   final answer and the agreement check.
6. **Deployment:** `build-all-tools.bat` installs EVERY tool to the PATH bin.
   Fresh shell smoke test: `becky-vision`, `becky-ocr`, `becky-perceive`,
   `search_library` all resolve.
7. **Discovery:** `becky list --json` (or `becky-ask --tools`) returns the
   installed inventory with one-line contracts. Agents must be able to ask
   what exists.
8. **CLI convention:** every image-taking tool accepts `--image`; every tool
   supports `--json`; exits nonzero with `{"ok":false,"error":"..."}` on
   failure. No exceptions across the suite.
9. **Determinism statement:** same image + same prompt must not flip
   conclusions between runs without the envelope flagging low confidence.
   (Fixed seeds or temperature 0 for the VLM calls unless there is a reason.)

## 5. Suggested regression harness (small, runs in build-all)

- `testdata\vision\` with 3-5 synthetic screenshots (terminal waiting on
  prompt; terminal idle; error dialog; empty desktop) + expected-substring
  assertions per image.
- One scripted run per tool per build: exit nonzero on assertion miss.
- The IMG_7725 case reproduced synthetically so no personal photo needs to
  live in the repo.

## 6. Context for the reviewer

- Jordan is not a developer and is vision-impaired; these tools ARE his
  hands and eyes. A confidently-wrong "nothing is stuck" answer cost him a
  real evening: an agent sat frozen behind that exact prompt for ~2 hours.
- Millions of tokens were spent building this suite specifically so that
  outside agents would NOT need to know its internals. Tonight the internals
  were the only thing that saved the task (manually invoking becky-ocr) —
  the inversion of the design goal.
- Related working docs in hj-mission-control: `docs/RECOVERY.md` (tonight's
  incident + watchdog spec), `docs/HANDOFF.md` (system map + laws).
