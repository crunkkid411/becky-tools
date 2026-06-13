# SPEC — `becky-ask` — the natural-language chat front-door to becky-tools

> ## STATUS — shell + first brain slice BUILT (drag-drop, quick actions, local intent, act-vs-discuss)
> The **shell** (the TUI chat window) is BUILT under `becky-go/cmd/ask/` →
> `bin\becky-ask.exe`. As of the **2026-06-08 upgrade**, four user-requested
> capabilities are now BUILT on top of it (see §2.5 "BUILT — the usable-assistant
> upgrade"): (1) **drag-and-drop / argv target**, (2) **quick-action buttons**
> (selectable rows / number keys), (3) the **local Qwen3.5-4B intent model** wired
> via llama.cpp (`llama-server`, the `internal/avlm` transport pattern), and (4) the
> **act-vs-discuss routing gate** (run a clear action on a target; otherwise discuss
> / clarify — never execute-by-default). The remaining brain pieces (workflow
> assembly/execution §3.3, new-tool pitch handoff §3.4) stay DESIGN-ONLY. The
> 2026-06-08 upgrade touched ONLY `becky-go/cmd/ask/` (+ this spec); it does not edit
> `cmd/becky`, `internal/*`, or sibling tools.
> Re-verify any model-id / pricing / flag claim past its cited date before building
> (the `claude-api` skill is the in-repo re-verification path).

---

## 0. TL;DR

`becky-ask` is the **lightweight, double-click chat window** a non-developer (Jordan,
the detective) opens to *talk* to becky-tools in plain English. It is the front-door;
it is **not** another forensic tool. It does exactly two useful things once its brain
lands:

1. **"Can becky do X?" / "I want to do ___"** → it **ASSEMBLES a workflow** from the
   existing becky tools and shows clear, copy-pasteable usage instructions (and, later,
   offers to run them).
2. **"I have a workflow/tool idea: ___"** that no existing tool covers → it **drafts a
   `becky-new-tool` pitch** (the structured intake the factory pipeline expects) and,
   **on the user's approval**, invokes `becky-new-tool` to actually build it.

It asks the **minimum** number of questions — only on a genuine decision or when the
use-case context is missing — never an interview.

The SHELL already shipped today does a thin, honest slice of #1 offline (keyword
catalog match) and clearly marks where the brain plugs in (`router.go`'s
`routeQuestion`, the `TODO(brain)` seam).

---

## 1. The relationship: `becky-ask` vs `becky.exe` vs `becky-new-tool`

This is the part the user is fuzzy on, so it is stated first and plainly.

```
   ┌──────────────────────────────────────────────────────────────────────────┐
   │  A HUMAN (plain English, fuzzy intent)                                      │
   │        │                                                                    │
   │        ▼                                                                    │
   │  becky-ask.exe   ── the NATURAL-LANGUAGE CHAT FRONT-DOOR  (this spec)       │
   │   • a TUI chat window (bubbletea), double-clickable                         │
   │   • understands the tool catalog + plain requests                          │
   │   • DECIDES: existing tools can do this  ──►  assemble a workflow           │
   │              no existing tool fits        ──►  draft a new-tool pitch       │
   │        │                                   │                                │
   │        ▼ (drives, for the human)           ▼ (on approval, hands off)       │
   │  becky.exe   ── the COMMAND ENGINE         becky-new-tool ── the TOOL       │
   │   • structured ops, NO fuzziness:             FACTORY (SPEC-BECKY-NEW-      │
   │     becky profile "John Clancy"               TOOL.md): a deterministic     │
   │     becky find "affair" --db ...              build pipeline that produces  │
   │     becky appearances "Shelby" ...            a brand-new becky-*.exe       │
   │   • each op chains the sharp becky-*.exe    • becky-ask supplies its INTAKE │
   │     tools; plain headline + JSON out          JSON; the human approves gates│
   └──────────────────────────────────────────────────────────────────────────┘
```

In one sentence each:

- **`becky-ask`** = *talk to it.* The chat layer for a human. Fuzzy in, a plan (or a
  pitch) out. It never does forensic compute itself — it routes.
- **`becky.exe`** = *the command engine.* Structured, non-fuzzy operations
  (`becky <verb> "<arg>" --flags`) that a human or agent runs directly. `becky-ask`'s
  assembled workflows are *mostly `becky` commands*. (See `cmd/becky` — the 6 named ops
  `enroll-wiki / index / profile / appearances / find / corroborate`, plus the
  natural-language `becky "this is <name>" <clip>` teach.)
- **`becky-*.exe`** = *the sharp tools* (transcribe, diarize, identify, validate,
  events, osint, ocr, framematch, …). `becky.exe` chains these; `becky-ask` knows them
  so it can explain or assemble multi-step recipes that go beyond a single `becky` verb.
- **`becky-new-tool`** = *the factory.* When the catalog genuinely can't do the job,
  `becky-ask` writes the pitch and `becky-new-tool` builds the missing tool.

**Why `becky-ask` is a separate binary, not just a `becky ask` verb.** The parallel
`SPEC-BECKY-NEW-TOOL.md` (Open Q1) suggests a thin `becky ask` *verb* could feed intake
JSON to the factory, and asks for a decision. The decision **this** spec takes (and the
reason the shell is a standalone binary) is:

- The product brief is *"the double-click chat front-door."* A non-dev opens a **window**,
  not a sub-command. A standalone `becky-ask.exe` is double-clickable and owns the TUI
  lifecycle (alt-screen, scrollback, input) cleanly; a `becky ask` verb would put an
  interactive TUI inside the otherwise-strictly-JSON-in/JSON-out command engine, which
  violates `becky`'s "no interactive prompts, no TUI" contract (BUILD-AGENT-BRIEFING.md
  rule 1).
- **Both can be true and they reconcile:** `becky-ask` is the human window; under the
  hood, when it decides "new tool," it produces the **same intake JSON** the
  `SPEC-BECKY-NEW-TOOL.md` S1 stage expects and calls `becky-new-tool --intake-file`.
  So the factory still has its clean, scriptable JSON entry point (usable by a `becky
  ask` verb or CI later); `becky-ask` is simply the conversational producer of that
  JSON. They are not competing — `becky-ask` sits *above* `becky-new-tool`, exactly as
  it sits above `becky`.

Net: `becky-ask` is the **only** conversational, human-facing surface. Everything below
it stays deterministic and machine-clean.

---

## 2. What the shell already does (built today)

So the brain spec is grounded in real, running code:

- **`becky-go/cmd/ask/`** (Go, `package main`), built to `bin\becky-ask.exe`:
  - `main.go` — entry point; **TTY guard** (clean exit-1 message with no terminal, so
    it never panics headless); launches bubbletea with alt-screen + mouse scroll.
  - `model.go` — the Model-Update-View: a pink **"Ask Becky:"** title bar, a
    **scrollback viewport** (bubbles/viewport) of the conversation, and a green
    **input line** (bubbles/textinput). Enter submits, `q`/Ctrl+C/Esc quit, PgUp/PgDn
    and the mouse wheel scroll history. Visuals carried from the original becky-ask
    (green-on-black, footer **"Type your question and press Enter | q: quit"**).
  - `catalog.go` — becky-ask's **in-memory knowledge of the tool catalog**: the 7
    orchestrator ops + 15 sharp tools, each with a plain summary, a runnable example,
    and match keywords. Mirrors `cmd/becky` + `SKILL.md`.
  - `router.go` — the **thin first-pass router** (`routeQuestion`). Today it answers
    catalog questions offline ("can becky transcribe?" → names `becky-transcribe` +
    example) and otherwise returns an **honest placeholder** pointing here. The
    `TODO(brain)` block marks the exact seam for the intent step below.
  - `model_test.go` — headless tests (render, submit/echo, catalog hit, placeholder,
    quit keys); `go test -race` green, ~76% coverage.
- Charm stack (verified current-stable 2026-06-08): **bubbletea v1.3.10**,
  **lipgloss v1.1.0**, **bubbles v1.0.0**, plus **golang.org/x/term** for the TTY check.

**The seam:** the brain replaces exactly one branch — `router.go`'s `TODO(brain)` — and
adds the supporting packages below. Nothing else about the window changes.

---

## 2.5 BUILT — the usable-assistant upgrade (2026-06-08)

Four capabilities the user explicitly asked for are now BUILT in `cmd/ask/`,
turning the shell from "echo + catalog" into a usable assistant. All additive;
the window's look (pink "Ask Becky:" bar, green input, amber replies, the
footer copy) is unchanged. New files all live under `becky-go/cmd/ask/`.

**1. Drag-and-drop / argv "target context"** (`target.go`, `main.go`).
Dropping a file or folder onto `becky-ask.exe` passes its path(s) as argv;
`resolveTarget` strips Windows' surrounding quotes, makes each path absolute,
keeps only those that exist (a missing path is *recorded*, never faked), and
classifies the result as a single file, a folder, or several files. The window
shows a cyan **`Target: <name>`** bar and, for a folder, a "folder <name>"
label. Pasted/typed paths set the target too (a whole-line path on submit).
*Proof (no-TTY):* `becky-ask.exe X:\…\test.mp4` →
`becky-ask: Target = test.mp4` + the applicable one-key actions, exit 0.

**2. Quick-action buttons** (`actions.go`, `model.go`).
With a target set, the UI shows selectable rows (`[1] Transcribe  [2] Identify
…`) — buttons aren't a native TUI control, so these are number-key-selectable
rows. Each action maps the target to the REAL becky-* command:
Transcribe→`becky-transcribe <f>`, Identify→`becky-identify <f> --kb kb-final`,
Describe→`becky-validate <f> --backend gemma4-local`, OCR→`becky-ocr
--frames-dir <dir>` (folders/images only — OCR reads frames, not raw video),
Cut→`becky-cut <f>`. Pressing the number runs it immediately (the press is the
confirmation — "no typing"). `commandFor(action, target)` is pure + asserted in
tests, so the exact argv each row would run is verifiable.

**3. Local intent model = the user's Qwen3.5-4B** (`llama.go`, `intent.go`,
`config_bridge.go`, `run.go`). The natural-language intent step drives the GGUF
already on disk — `X:\HuggingFace\models\unsloth\Qwen3.5-4B-GGUF\
Qwen3.5-4B-Q4_K_M.gguf` — through llama.cpp's `llama-server` using the SAME
transport pattern as `internal/avlm` (spawn a transient server on a free port →
wait `/health` → POST OpenAI-style `/v1/chat/completions` → read
`message.content`, with `chat_template_kwargs.enable_thinking=false`). It is a
**text-only** classifier (no mmproj/frames/audio). The `llama-server.exe` path
comes from the shared `internal/config` (`cfg.LlamaServer`); the model path is
`resolveIntentModel()` (env `BECKY_ASK_MODEL` override → the verified on-disk
default — **no substitution to another model**, per the Model Verification
Protocol). If the model or binary is absent, `Ready()` reports it and the brain
**degrades to the offline keyword catalog** with an honest note — it never
hard-fails. *Model verification (2026-06-08):* `unsloth/Qwen3.5-4B-GGUF`,
official source `https://hf.co/unsloth/Qwen3.5-4B-GGUF` (updated 2 Mar 2026,
2.3M downloads, `base_model:Qwen/Qwen3.5-4B`, apache-2.0); the conversational/
chat build of the Qwen3.5 "unified" line (no separate `-Instruct` repo exists);
4B Q4_K_M (~2.7 GB) fits the RTX 3070 fully GPU-offloaded. *Proof (live):* the
Go transport spawned `llama-server -ngl 99`, and the model returned
`{"kind":"action","action":"transcribe",…}` for "transcribe this" and
`{"kind":"question",…}` for "can becky figure out where this was filmed?".

**4. Act-on-clear-intent, discuss-when-unsure** (`intent.go`, `router.go`).
The routing rule is explicit and testable (`classifyDeterministic` →
`classify`/`reconcile`): it is a **corroboration/fact gate** — two signals must
agree before acting. **ACT** only when (i) a target is set AND (ii) the request
is an unambiguous action (a button press, or a clear imperative like "transcribe
this"); the brain then shows the exact command and runs it on a single `y`
(a button press skips the prompt). **DISCUSS / CLARIFY** for everything else —
a question, an idea/new-tool wish, anything ambiguous, or no target — answering
from the catalog or asking exactly ONE clarifying question, and **never firing a
tool call**. The target gate is enforced in Go: the model can downgrade an
action to a question or choose *which* action, but it can never turn a no-target
request into an action. *Proof:* `TestGate_ClearActionWithTarget_Runs`
(transcribe+target → `becky-transcribe <f>`), `TestGate_AmbiguousQuestion_NoToolCall`
("can becky … filmed?" → no command), `TestReconcile_TargetGate_*` (model
"action" with no target → clarify, never act).

**Safety posture (Open Q3):** default is **always show + opt-in** before running
a typed action (`y`/`n`); a quick-action button is itself the opt-in. **Privacy
(Open Q2):** the intent model is **local-only** (no remote API) — case text never
leaves the machine. Build/verify: `go build ./cmd/ask`, `go vet ./cmd/ask`,
`go test -race ./cmd/ask/...` (45+ tests; the live-model check is behind
`-tags=llm` so the default suite stays model-free).

---

## 3. The brain — chat → (workflow | new-tool pitch)

### 3.1 The decision the brain makes (the core flow)

```
 user types a request  ─►  INTENT STEP (cheap model, catalog as context)
                              │
            ┌─────────────────┼───────────────────────────┐
            ▼                 ▼                             ▼
   (a) CATALOG ANSWER   (b) ASSEMBLE WORKFLOW        (c) NEW-TOOL PITCH
   "can becky do X?"    request maps to existing     nothing existing fits
   → yes/no + the       tool(s) → ordered plan       → draft intake pitch
     tool + example       of becky / becky-* cmds      → on approval, call
   (already in shell)     + plain instructions          becky-new-tool
                              │                             │
                              ▼                             ▼
                       offer to RUN it (opt-in)      hand off intake JSON
                       via becky.exe                 to the factory pipeline
```

Plus a fourth, ever-present option the brain uses **sparingly**:

```
   (d) ONE CLARIFYING QUESTION  — only when a meaningful decision or the use-case
       context is genuinely missing (see §3.5). Never an interview.
```

### 3.2 The intent step (who runs it — cheap, local-first)

Following the becky house rule "deterministic where possible; cheap local LLM only for
the narrow NLP," and matching `SPEC-BECKY-NEW-TOOL.md`'s runner philosophy:

- **Classification + extraction is a small task** → a **cheap/local model first**
  (Qwen3-class ~4–8B or Phi-4-mini via the existing `C:\llama.cpp` `llama-server`, or a
  cheap API for non-sensitive use). The model is fed the **request + the tool catalog**
  (already structured in `catalog.go`) and returns a typed intent:
  ```json
  {
    "kind": "catalog | workflow | new_tool | clarify",
    "matched_tools": ["becky-identify", "becky-find"],
    "missing_context": ["which folder of videos?"],
    "confidence": 0.82,
    "rationale": "wants to locate a person across a corpus -> appearances workflow"
  }
  ```
- **Privacy caveat (load-bearing, forensic):** requests may contain case context
  (names, claims). The intent model defaults to **local-only**. A remote/cheap-API model
  is opt-in and forbidden for sensitive content — same rule as the factory's S7 (Gemini
  free tier may train on prompts; OpenRouter free routes vary). See
  `SPEC-BECKY-NEW-TOOL.md` §7.
- **Claude is NOT used for the intent step.** It is the cheapest possible NLP. Claude
  only ever enters the picture *downstream*, inside `becky-new-tool`'s S5 build — and
  only after the human approves the pitch. This conserves the metered Agent-SDK credit
  (verified June-15-2026 separate per-user credit; `SPEC-BECKY-NEW-TOOL.md` §5.5).
- **Determinism / graceful degrade:** if no model is reachable (offline, server down),
  the brain falls back to the **already-built keyword catalog match** (today's
  `matchCapabilities`) and says so plainly — it never hard-fails. The catalog answer
  path (a) needs no model at all.

### 3.3 (b) Assembling a workflow

When the request maps onto existing capability, the brain produces a **plan**, not prose:

- An **ordered list of steps**, each a real `becky` / `becky-*` command with the user's
  specifics filled in (paths, names, the `--kb` / `--db` flags), plus a one-line
  plain-English "what this step gives you."
- The plan is rendered in the chat scrollback (the shell already styles user/becky/
  system blocks) and is **copy-pasteable**.
- **Opt-in execution:** the brain then offers *"Run this now? (y / edit / no)"*. On `y`
  it shells the commands via the existing orchestrator (`becky.exe`, resolved next to
  `becky-ask.exe` exactly as `cmd/becky/runner.go` resolves its tools), streaming each
  step's plain-English headline back into the chat and surfacing the resulting clips/
  paths. Recall-first, candidate-not-conclusion, human-reviewed — the workflow inherits
  `FORENSIC-OUTPUT-PHILOSOPHY.md` unchanged.

Worked example (illustrative, synthetic):
> **You:** "I want to find every video where Shelby shows up and see what's said around
> those moments."
> **Becky:** "Here's a 2-step plan over your case folder:
> 1. `becky appearances "Shelby" --kb kb-final --corpus "<your folder>"` — finds the
>    videos Shelby is in (voice + face) with the clips.
> 2. `becky find "<topic>" --db forensic.db` — searches what's said; run after
>    `becky index "<your folder>"` if you haven't built the search index yet.
> I need one thing: **what's the folder of videos?** Then I can run step 1 for you."

That single question is the *only* one needed (the corpus path is genuinely missing) —
see §3.5.

### 3.4 (c) Drafting a new-tool pitch and handing off

When the intent step returns `new_tool` (and a quick **redundancy sanity check** against
the catalog agrees nothing existing fits), the brain:

1. **Drafts the pitch** as the exact **intake record** `becky-new-tool`'s S1 stage
   consumes (`SPEC-BECKY-NEW-TOOL.md` §3 S1), produced by the cheap intent model from
   the conversation:
   ```json
   {
     "slug": "becky-redact",
     "capability": "blur faces + mute named speakers in a clip for safe sharing",
     "input_kind": "video",
     "output_kind": "video+json",
     "constraints": ["offline", "h264_nvenc", "reuse internal/faceembed"],
     "definition_of_done": ["go build clean","go vet clean","runs on test.mp4","single JSON to stdout","exit 0"],
     "captured_at": "2026-06-08"
   }
   ```
2. **Shows the pitch to the human in plain English** — what the tool would do, what it'd
   take in / put out, and that building it runs the (metered) factory pipeline. This is
   a **meaningful decision**, so the brain asks for explicit approval here.
3. **On approval**, invokes `becky-new-tool --intake-file <pitch.json>` (resolved next
   to `becky-ask.exe`). The factory then runs its own staged pipeline with its **own
   human gates** (GATE A build-go?, GATE B spec approval, GATE C merge) — `becky-ask`
   does **not** bypass or duplicate those gates; it just opens the door. Progress
   headlines stream back into the chat; the heavy Claude build stage and the
   Fact-Forcing Gate all live in `becky-new-tool`, not here.
4. If the user declines, the pitch JSON is saved to the run area so they can run the
   factory later by hand — nothing is lost.

**Boundary (important):** `becky-ask` *produces intent and a pitch*; it does **not**
contain the build pipeline. All tool-building logic, cost control, the headless Claude
agent, and the gates are `becky-new-tool`'s job. Keeping that boundary is what stops
`becky-ask` from sprawling into a second orchestrator.

### 3.5 Minimal questions — the rule

Per the brief ("minimal questions: only on meaningful decisions or when use-case context
is genuinely missing"):

- **Ask only when** (i) a required concrete input is missing and can't be inferred
  (e.g. *which* folder/video/db), or (ii) the path forks on a real decision the user
  must own (e.g. "build a new tool? that runs the factory" / "voice-only or also face?"
  when it changes the plan materially).
- **Never ask** for things the brain can default or infer: conventional paths
  (`kb-final`, `forensic.db`, `pipeline-out/`) are tried automatically the way
  `cmd/becky` already does (`defaultDB`, `defaultCorpus`); a fuller name is *suggested*,
  not interrogated ("did you mean **John Clancy**? a lone surname is ambiguous" — one
  nudge, then proceed).
- **Batch, don't drip:** if two facts are genuinely needed, ask both in one turn, not a
  back-and-forth. Default to **proposing a plan with sensible assumptions stated** and
  letting the user correct, rather than front-loading questions.

---

## 4. Architecture of the brain (where the new code goes)

All additive; the window is untouched. Proposed layout under `becky-go/`:

| Piece | Where | Runner | Job |
|---|---|---|---|
| Intent step | `internal/askintent/` (new) | cheap/local model | request + catalog → typed intent JSON (§3.2) |
| Catalog (exists) | `cmd/ask/catalog.go` | det | the tool knowledge fed to the intent model + offline fallback |
| Workflow planner | `cmd/ask/plan.go` (new) | det | intent → ordered `becky`/`becky-*` command plan (§3.3) |
| Workflow runner | reuse `becky.exe` via `os/exec` | det | opt-in execution, streaming headlines (mirror `cmd/becky/runner.go`) |
| Pitch builder | `cmd/ask/pitch.go` (new) | cheap model | conversation → `becky-new-tool` intake JSON (§3.4) |
| Factory handoff | reuse `becky-new-tool.exe` via `os/exec` | det | on approval, `--intake-file <pitch.json>` |
| Model transport | reuse `internal/agentrun/` *(planned by `SPEC-BECKY-NEW-TOOL.md` §5.4)* for any Claude touch; a cheap-model client for local/cheap | — | one shared, verified invocation, not a third copy |

- **Reuse, do not reinvent:** the cheap-model transport and the headless-Claude helper
  should be the **same** `internal/agentrun/` the factory spec introduces — `becky-ask`
  becomes a third caller (alongside `becky-review`, `becky-validate`), never a fourth
  copy of the invocation. The workflow runner reuses `becky.exe` exactly as the
  orchestrator already shells `becky-*`.
- **Determinism between steps:** the brain's control flow is plain Go (classify → branch
  → render/exec). The model is consulted at exactly two narrow points (intent
  classification; pitch drafting) — never to "decide what to do next" in a loop.
- **Output discipline:** the chat surface is human-facing, but any machine handoff
  (intake JSON to the factory) is clean JSON; the workflow runner preserves the
  underlying tools' JSON-to-stdout + headline-to-stderr behavior.

---

## 5. Conversation state, safety, honesty

- **Session memory:** the brain keeps the running transcript (the shell already holds it)
  so follow-ups resolve ("run step 1" after a plan). Keep it in-memory per session;
  persisting chat history is a later, opt-in nicety (forensic context → don't write case
  text to disk by default).
- **No silent action:** the brain never runs a `becky` command or launches the factory
  without showing what it will do; execution is always opt-in (`y/edit/no`).
- **Honest about being a router:** if confidence is low or the request is out of scope
  (not a video/audio-case task), it says so plainly and offers the catalog — it does not
  hallucinate a capability. The shell's placeholder already models this tone.
- **Inherits the philosophy:** every finding surfaced through an assembled workflow obeys
  `FORENSIC-OUTPUT-PHILOSOPHY.md` (plain words, name what's known, candidate-not-
  conclusion, corroborate-then-conclude) — `becky-ask` adds no new finding-generation,
  so it can't violate it, but it must render the underlying tools' output faithfully.

---

## 6. Future vision — dashboard / quick-launch / drag-drop (TUI vs GUI)

The brief asks what's feasible in a TUI vs what needs a GUI. Honest split:

**Feasible in the TUI (bubbletea/lipgloss/bubbles) — natural next steps:**
- **Quick-launch menu / command palette.** A `bubbles/list` of the catalog ops; arrow +
  Enter to fill a template. (bubbles ships `list`, `table`, `help`, `key` already.)
- **A status/“dashboard” pane.** Split-view (lipgloss `JoinHorizontal`) with chat on one
  side and, on the other, KB status (people enrolled), last run, embed-server up/down.
  This is the spiritual successor to the old `becky-status`.
- **Recent runs / history list**, re-runnable with Enter.
- **Forms for required inputs** (the "which folder?" question as a `textinput`/file
  picker prompt) instead of a free-text turn.
- **Tab completion of names/paths** in the input line.
- **A file *path* drop** works in many terminals (dragging a file onto a terminal pastes
  its path) — so "drop a video to analyze it" is *partially* feasible in the TUI: the
  pasted path becomes the corpus/clip argument. Good enough for single-file flows.

**Needs a real GUI (out of TUI scope) — name it honestly:**
- **True drag-and-drop workflow *builder*** (drag tool nodes onto a canvas, wire them
  into a pipeline visually) — a node-graph editor is a GUI app, not a terminal UI.
- **Rich media review** — thumbnails, frame scrubbing, side-by-side image comparison for
  the framematch exhibits (`FORENSIC-OUTPUT-PHILOSOPHY.md`'s exhibit workflow). A TUI
  can *launch* the existing exhibit/PDF; it can't render/scrub frames inline.
- **Clickable buttons / mouse-first layout** beyond simple wheel-scroll + click-to-focus.

**Recommended path:** grow the TUI (palette → split dashboard → forms → history) since it
preserves the instant, double-click, zero-dependency virtue the whole project is built
on. Treat a GUI (e.g. a thin native or web-local shell over the *same* `becky.exe` +
`becky-new-tool` + intake-JSON contracts) as a **separate, later** track for the visual
workflow-builder and media review — explicitly **not** a rewrite, because everything
underneath is already clean JSON-in/JSON-out, so a GUI is just another front-door beside
`becky-ask`. The contracts this spec keeps (catalog, intent JSON, factory intake JSON)
are exactly what make that future GUI cheap.

---

## 7. Build plan for the brain (phased, after approval)

1. **Phase 1 — intent + catalog answers, no exec.** Add `internal/askintent/` (cheap
   local model, catalog as context) and replace `router.go`'s `TODO(brain)` so requests
   classify into catalog/workflow/new_tool/clarify and render a plan. No command
   execution yet (still copy-paste). Lowest risk, immediately useful.
2. **Phase 2 — opt-in workflow execution.** Add `cmd/ask/plan.go` + reuse `becky.exe`
   to run an approved plan, streaming headlines into the chat.
3. **Phase 3 — new-tool pitch + handoff.** Add `cmd/ask/pitch.go`; on approval call
   `becky-new-tool --intake-file` (requires `SPEC-BECKY-NEW-TOOL.md` to be built first).
4. **Phase 4 — TUI dashboard niceties** (§6): palette, split status pane, history, forms.

Each phase keeps the shell launchable and the model local-first; Claude stays out of
`becky-ask` entirely (it lives only inside the factory's build stage).

---

## 8. Honest open questions (need a human decision)

- **Q1 — Separate binary confirmed?** This spec keeps `becky-ask` as a standalone
  double-click chat binary (the brief's framing) and reconciles it with
  `SPEC-BECKY-NEW-TOOL.md` Q1 by having it *produce* the factory's intake JSON. Confirm
  this over a `becky ask` verb. (If a scriptable verb is *also* wanted later, add it as
  a thin wrapper that emits the same intake JSON — not a competing path.)
- **Q2 — Local intent model choice.** Which cheap/local model backs the intent step
  (Qwen3-4B vs Phi-4-mini via `llama-server`, or a cheap API for non-sensitive)? Default:
  local-only for forensic privacy; confirm the specific model + where it's configured
  (reuse `internal/config`).
- **Q3 — Execute-by-default vs always-confirm.** This spec defaults to **always show +
  opt-in** before running anything. Confirm that's the desired safety posture (vs a
  `--yes`-style auto-run for trusted simple plans).
- **Q4 — How much to infer vs ask.** The minimal-questions rule (§3.5) leans toward
  *propose-with-assumptions*. Confirm the comfort level (e.g. is auto-trying `kb-final`/
  `forensic.db` without asking acceptable? — it matches `cmd/becky` today).
- **Q5 — Chat history persistence.** Default: in-memory only (don't write case text to
  disk). Confirm whether an opt-in saved-session feature is wanted.
- **Q6 — Re-verify externals at build time.** Charm versions, the local-model options,
  and (for the factory handoff) the Claude/Agent-SDK credit terms drift; re-verify
  before building each phase. The `claude-api` skill is the in-repo re-verification path
  for anything Claude-side.

---

## 9. Sources / references (verified 2026-06-08)

- Charm stack current-stable versions (Go module proxy, fetched 2026-06-08):
  bubbletea `v1.3.10`, lipgloss `v1.1.0`, bubbles `v1.0.0`
  (`https://proxy.golang.org/github.com/charmbracelet/<pkg>/@latest`). Note: a
  coordinated **v2 line is now GA** (bubbletea v2.0.7 / lipgloss v2.0.3 / bubbles
  v2.1.0) with a moved module path (`charm.land/bubbletea/v2`) and a changed API
  (`View() tea.View`, `tea.KeyPressMsg`); the shell uses the proven **v1 trio** for
  cohesion and lower risk — revisit on a deliberate v2 migration.
- In-repo grounding: `cmd/becky/{main,ops,runner}.go` (the command engine + how to shell
  tools), `BUILD-AGENT-BRIEFING.md` (house rules, the Fact-Forcing Gate),
  `FORENSIC-OUTPUT-PHILOSOPHY.md` (output contract), `SKILL.md` (the catalog),
  `BECKY-ORCHESTRATOR-SPEC.md` (the brain-vs-tools framing this builds on).
- Companion spec (the factory `becky-ask` hands off to): `SPEC-BECKY-NEW-TOOL.md`
  (intake shape §3 S1; `internal/agentrun/` §5.4; cheap-vs-Claude + privacy §7; metered
  Agent-SDK credit §5.5; Open Q1 on `becky ask`).
- Built shell: `becky-go/cmd/ask/{main,model,styles,catalog,router,model_test}.go`,
  `bin\becky-ask.exe`, `ask` added to `build-all-tools.bat` TOOLS.
