# SPEC — `becky-new-tool` — the deterministic tool-factory pipeline

> ## SPEC — BUILT 2026-06-08 (core pipeline + agentrun; see status note)
> Built by the build subagent on **2026-06-08**. Delivered: `cmd/new-tool/` (the
> deterministic S1–S10 state machine over a resumable `state.json`), the shared
> `internal/agentrun/` headless-`claude -p` helper, the `new-tool` entry in
> `build-all-tools.bat`. The Model Verification Protocol from `BUILD-AGENT-BRIEFING.md`
> is enforced at RUNTIME (no hardcoded stale model ids): `hf` CLI + on-disk reuse for
> local, the OpenRouter live free-models API (defaults `poolside/laguna-m.1:free` →
> `moonshotai/kimi-k2.6:free`, both re-confirmed live 2026-06-08), and the
> version-must-be-current rule for hosted APIs. The `claude -p` envelope was verified
> live on this machine — fields `result`, `session_id`, `total_cost_usd`, `is_error`,
> `subtype` confirmed, and **Q6 resolved**: the per-model cost breakdown lives under
> `modelUsage["<model-id>"].costUSD`. Do not trust the EXAMPLE model ids in the body
> below (Phi-4-mini / qwen3-4b / "gemini flash") — they are STALE; the built pipeline
> verifies models at runtime instead. The remaining open questions (Q1–Q5) still need
> human decisions; gates default to stopping unless `--approve`/`--yes`.
>
> _Original design header (superseded): SPEC — NOT BUILT, AWAITING APPROVAL. Author:
> research subagent. All external capabilities were verified live on 2026-06-08._

---

## 0. What this is and why

The becky toolset (27 tools) was built by one repeated, disciplined human+LLM routine.
That routine currently lives in scattered prose: `BUILD-AGENT-BRIEFING.md`,
`AUTORESEARCH-SPEC.md`, `FORENSIC-OUTPUT-PHILOSOPHY.md`, `BECKY-VALIDATE-PROPOSAL.md`
(which shows the Fact-Forcing-Gate + research-first pattern in action), and the
"Decisions/deviations" sections in `PROGRESS.md`. Every new tool re-pays the same
cognitive tax: research the current best library/model, check it isn't already
covered, write a spec in the house structure, build it with a headless agent in a
Ralph loop, test on REAL input, get a second model to review it, finetune its
prompts/settings against a real eval, and integrate it into build-all + docs.

`becky-new-tool` **formalizes that routine as a deterministic PIPELINE**. Control
flow is plain code (a state machine over stages), NOT an LLM deciding what to do
next. An LLM/agent is invoked **only inside specific stages**, and the most
expensive LLM (a headless Claude Code agent) is invoked in as few stages as
possible, because — verified below — headless `claude -p` now bills against a
finite separate monthly credit.

**Design intent (the becky house style applies to this tool too):** JSON in / JSON
out, exit-coded, offline-first, deterministic between stages, graceful degradation,
reuse the shared `internal/` packages, never modify source inputs. This tool is a
*meta-tool*: its "input" is a pain-point and its "output" is a new becky tool plus
its spec, tests, eval result, and integration — staged so a human approves at the
gates that matter.

---

## 1. The routine, restated as deterministic stages

The ten stages from the request, made into a pipeline. Each stage is a pure-ish
function `stage(state) -> state'` that reads a JSON **run-state** file, does its
work, writes artifacts to a run directory, and updates the run-state. The
orchestrator advances stages; it never asks an LLM "what should I do next".

```
                         becky-new-tool  (orchestrator: deterministic state machine)
                         run dir: X:\AI-2\becky-tools\.factory\<slug>-<date>\
                         run-state: state.json   (single source of truth, resumable)

  ┌─────────────────────────────────────────────────────────────────────────────┐
  │  S1 INTAKE ─► S2 RESEARCH ─► S3 REDUNDANCY ─► [GATE A: human/auto go-build?]   │
  │      │             │              │                                            │
  │   (cheap)     (cheap+web)     (det + cheap)                                    │
  │                                                                                │
  │  S4 SPEC ─► [GATE B: human approves spec] ─► S5 BUILD ─► S6 TEST ─┐            │
  │   (CLAUDE      (FACT-FORCING GATE runs        (CLAUDE     (det)    │            │
  │    or cheap)    INSIDE S5's agent)             agent,             │            │
  │                                                Ralph loop)        ▼            │
  │                                            ◄───── S6 fail: loop back to S5 ────┤
  │                                                  (bounded retries)             │
  │                                                                                │
  │  S7 SECOND-AI REVIEW ─► S8 (gate discipline is cross-cutting, not a stage)     │
  │   (DIFFERENT model:                                                            │
  │    cheap API / local)                                                          │
  │      │                                                                         │
  │      ▼                                                                         │
  │  S9 AUTO-RESEARCH FINETUNE ─► S10 INTEGRATE ─► [GATE C: human merge]           │
  │   (det harness = becky-eval;     (det: build-all,                              │
  │    cheap/local model graded       PROGRESS row, SKILL                          │
  │    by det scorer; Claude only     mention)                                     │
  │    if a prompt rewrite needs it)                                               │
  └─────────────────────────────────────────────────────────────────────────────┘

  Legend of who runs each stage:
    (det)          = deterministic Go code only, no model
    (cheap)        = a cheap/free model: local small LLM (llama.cpp/Ollama) OR a
                     cheap/free API (Gemini Flash-Lite / OpenRouter free) — privacy caveat below
    (CLAUDE agent) = a headless Claude Code agent via `claude -p` (the expensive,
                     metered path — used in as few stages as possible)
```

**Stage 8 (Fact-Forcing-Gate discipline) is deliberately NOT a pipeline node.** It
is a cross-cutting guardrail that fires *inside* any stage where the Claude agent
writes/edits files (today: S5 build, and S4 if Claude writes the spec). See §6.

---

## 2. One tool or several? — RECOMMENDATION

**Recommendation: ONE orchestrator binary (`becky-new-tool`) that drives sub-stages,
several of which are existing becky tools it shells out to — NOT a new constellation
of sibling binaries.** This mirrors how `becky` (the orchestrator, tool #26) already
works: a thin driver that shells `becky-*.exe` and never reimplements them.

Why one:
- The becky house pattern is already "one thin orchestrator + many sharp tools"
  (`becky` → `becky-enroll`/`becky-pipeline`/`becky-identify`/...). `becky-new-tool`
  is the same shape one level up: it orchestrates the *build* of a sharp tool.
- The pipeline is inherently sequential with shared run-state; splitting it into N
  binaries would just move the state machine into a `.bat` and lose resumability,
  typed state, and the single Fact-Forcing-Gate enforcement point.
- It **reuses, not duplicates**: S9 finetune IS `becky-eval` (tool #24) invoked as a
  subprocess; S5/S7 shell `claude -p`; S6 shells the freshly built tool + `go`/`go
  vet`. The orchestrator owns only the glue + gates.

What stays separate (do NOT fold in):
- `becky-eval` remains its own tool. `becky-new-tool` calls it; it does not absorb it.
- The headless-Claude invocation is a small shared helper, not a new public tool. It
  should live in a new `internal/agentrun/` package (see §5.4) so `becky-review`,
  `becky-validate`, and `becky-new-tool` share ONE verified invocation instead of
  three copies. (Today only `cmd/review/backend.go` has it.)

Shared intake with `becky-ask`: there is **no `becky-ask` tool today** (verified:
`cmd/` has no `ask/`). If a conversational front-end is later desired, the right move
is a thin `becky ask "<pain point>"` *verb on the existing `becky` orchestrator* that
hands its captured intent JSON to `becky-new-tool --intake-file <json>`. Do not build
a separate `becky-ask` binary just for this. (Open question Q1.)

**Net:** 1 new binary (`becky-go/cmd/new-tool/` → `bin\becky-new-tool.exe`) + 1 new
shared internal package (`internal/agentrun/`) + reuse of `becky-eval`, `claude`,
`go`, and the new tool's own binary. Optionally 1 new `becky` verb (`ask`) later.

---

## 3. Stage-by-stage spec

For each stage: **what it does**, **who runs it** (det / cheap / Claude), **input**,
**output JSON written to run-state**, and **why it can or cannot be offloaded off
Claude**.

### S1 — INTAKE  ·  runner: deterministic + (optional) cheap
- **Does:** capture a plain pain-point ("I need a tool that does ___") and normalize
  it into a structured intake record: a slug, a one-line capability statement,
  declared input shape (video / json / text / url), declared output shape, hard
  constraints (offline? which existing models?), and a "definition of done" stub
  pre-filled from `BUILD-AGENT-BRIEFING.md`'s non-negotiables.
- **Who & why offload:** pure capture is **deterministic** (flags / a small intake
  template). Turning a vague sentence into the structured record is a *tiny*
  classification/extraction task → **cheap/local model** (Qwen3-4B / Phi-4-mini
  local, or Gemini Flash-Lite). **Never Claude** — this is the cheapest possible NLP.
- **In:** `--pain "<text>"` (or `--intake-file intake.json`, or piped stdin).
- **Out (`state.intake`):**
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

### S2 — DEEP RESEARCH  ·  runner: deterministic web fetch + cheap synthesis
- **Does:** for the capability, find the *current* best library/model/approach as of
  the build date, and check package registries. Produces a sourced research brief in
  the exact spirit of `BECKY-VALIDATE-PROPOSAL.md` (live-verified, dated sources, a
  modality/feasibility matrix, a VRAM/cost note, a recommended path + a degrade
  fallback). MUST NOT rely on training data; every claim carries a URL + date.
- **Who & why offload:** the *fetching* is deterministic (HTTP GET to package
  registries + a web-search API + doc pages). The *synthesis* of fetched text into
  the brief is a summarization task → **cheap/local model first**; escalate to Claude
  ONLY if the cheap model's brief fails the S2 quality check (see below). Registry
  checks are pure HTTP, no model:
  - npm: `https://registry.npmjs.org/<pkg>` and the search endpoint.
  - PyPI: `https://pypi.org/pypi/<pkg>/json`.
  - crates.io: `https://crates.io/api/v1/crates/<q>`.
  - Go: `https://proxy.golang.org/<module>/@latest` and `pkg.go.dev`.
  - HF models: the Hugging Face Hub API (model search + repo details).
  - General web: a pluggable search backend (the same place the existing
    `openrouter`/web path is configured); offline runs skip web with a note.
- **S2 quality check (deterministic):** the brief must contain >= N sources each with
  a URL **and** an ISO date, a named recommended primary, and a named fallback. If
  not, the orchestrator escalates this single stage to Claude (one call) or stops at
  GATE A for a human. This keeps Claude out of the loop unless research is genuinely
  hard.
- **In:** `state.intake`.
- **Out (`state.research`):** the brief markdown (saved to run dir) + a structured
  `recommended:{library,model,runtime,license,vram_note}` + `fallback:{...}` +
  `sources:[{url,date,claim}]` + `registry:{npm:[...],pypi:[...],...}`.

### S3 — REDUNDANCY CHECK  ·  runner: deterministic + cheap
- **Does:** answer "can an existing becky tool (plus flags) already do this?" — the
  extensibility question from `BECKY-VALIDATE-PROPOSAL.md`'s Fact-Forcing answer ("no
  existing file serves this, Glob-verified"). Produces a verdict: `new_tool` |
  `extend:<existing-tool>` | `duplicate:<existing-tool>`.
- **Who & why offload:** the *evidence gathering* is **deterministic** — enumerate
  `cmd/*`, read each tool's top-of-file doc comment + its `--help`/flags + its
  PROGRESS row, and grep for capability keywords. The *judgment* over that evidence
  (is "redact" already covered by `becky-osint` + `becky-identify`?) is a small
  reasoning task → **cheap/local model**, fed ONLY the gathered tool summaries (cheap,
  bounded context). **Claude only** if the cheap model is low-confidence AND the
  decision is expensive to get wrong; otherwise surface to a human at GATE A.
- **In:** `state.intake`, the live `cmd/` inventory, `PROGRESS.md`.
- **Out (`state.redundancy`):**
  ```json
  {
    "verdict": "new_tool",
    "closest_existing": ["becky-osint","becky-identify"],
    "why_not_covered": "no tool re-encodes a redacted MP4; osint only exports frames; identify only labels",
    "extend_candidate": null,
    "confidence": 0.78,
    "decided_by": "local:qwen3-4b"
  }
  ```
- **GATE A (after S3):** STOP unless go-build is approved. Default policy:
  auto-proceed only if `redundancy.verdict == "new_tool"` AND `confidence >= 0.7`
  AND the S2 brief passed; otherwise require human `--approve build`. (Config Q3.)

### S4 — SPEC  ·  runner: Claude agent (default) OR cheap (toggle)
- **Does:** write the new tool's spec **in the project's proven structure** — the
  same skeleton as `BECKY-VALIDATE-PROPOSAL.md` / `BECKY-ORCHESTRATOR-SPEC.md`:
  Fact-Forcing-Gate block (the 4 facts), TL;DR/verdict, what-it-is, CLI contract
  (flags table), backends/degradation, JSON output contract with a *synthetic*
  example, where-it-fits-the-pipeline diagram, honest limits, dated sources. The spec
  must bake in `FORENSIC-OUTPUT-PHILOSOPHY.md` for any human-facing-finding tool.
- **Who & why offload:** spec-writing is the *first* place real reasoning pays off,
  so **default = one Claude `claude -p` call** with a strict `--json-schema` so the
  spec's machine-readable parts come back in `structured_output` deterministically.
  BUT it is **togglable to a cheap model** (`--spec-backend cheap`) for simple tools —
  a cheap model writing a spec a human then approves at GATE B is often enough, and
  conserves the Claude credit. The spec is cheap to human-review, so erring toward the
  cheap model here is low-risk.
- **In:** `state.intake`, `state.research`, `state.redundancy`, the spec skeleton, the
  three philosophy docs.
- **Out (`state.spec`):** `spec_path` (a `SPEC-<NAME>.md` written to the run dir, NOT
  to the repo root) + a structured `cli:{flags:[...]}`, `output_schema:{...}`,
  `degrade_plan:[...]`, `reuse:[internal/...]`, plus the S9 inputs `tunable_surface`
  and `answer_key_facts` (so finetune has a target).
- **GATE B (after S4):** human approves the spec before any code is written. This is
  the becky norm (every existing tool had a spec/proposal approved first). Default:
  always stop here unless `--yes` for a trusted simple tool.

### S5 — BUILD  ·  runner: Claude agent (Ralph loop), Fact-Forcing-Gate INSIDE
- **Does:** delegate the actual coding to a **headless Claude Code agent** that runs
  the Ralph loop from `BUILD-AGENT-BRIEFING.md`: build → run on real input → inspect
  → fix → repeat until it compiles, `go vet` is clean, runs on `test.mp4` (or the
  upstream tool's real JSON), emits one valid JSON doc, exits 0, stderr quiet without
  `--verbose`. The agent is handed the approved spec + the briefing + the shared-
  package list and told to write `cmd/<name>/` + reuse `internal/`.
- **Who & why offload:** **this is irreducibly Claude.** Writing correct, idiomatic,
  shared-package-reusing Go that passes a real Ralph loop is exactly the
  expensive-reasoning task the briefing was written for. A cheap/local model is not
  reliable enough to leave unattended here. This is THE stage the whole pipeline
  exists to protect the budget for. (Cost-control levers in §5.5.)
- **Headless invocation:** see §5 — verified `claude -p` with
  `--append-system-prompt-file` (the briefing as the system layer), prompt (the spec +
  task) on **stdin**, `--output-format stream-json --verbose` for live progress to the
  run log, `--permission-mode acceptEdits` + a tight `--allowedTools` so it can write
  the new package and run `go`/`ffmpeg`/the new binary without prompts, `--add-dir
  becky-go`, `--max-turns` and `--max-budget-usd` as hard stops, `--session-id <uuid>`
  fixed per run so a retry resumes deterministically.
- **In:** `state.spec`, the briefing, the shared-package inventory, `test.mp4` path.
- **Out (`state.build`):** `package_dir`, `binary_path`, `agent_session_id`,
  `turns_used`, `cost_usd` (from the `claude -p` JSON envelope), `degradations:[...]`
  (what the agent reported as gracefully degraded), `agent_final_report` (text),
  `fact_forcing:{callers,no_dup,data_shape,verbatim}` (the 4 facts parsed from the
  transcript — see §6).

### S6 — ITERATIVE TEST ON REAL INPUT  ·  runner: deterministic
- **Does:** independently verify the built tool against the becky definition of done —
  *"valid JSON is not proof."* Deterministically: `go build`, `go vet`, `go test
  -race` (if tests exist), then RUN the binary on the real asset and assert:
  (1) stdout parses as a single JSON document; (2) exit 0; (3) stderr is 0 bytes
  without `--verbose`, non-empty with it; (4) for tools that produce findings, a
  content-sanity check that the JSON isn't empty/placeholder on real input;
  (5) idempotence where applicable. Re-run on the real upstream JSON for chained
  tools. Save the real stdout snippet as evidence.
- **Who & why offload:** **fully deterministic.** Build/vet/test/run/parse/assert are
  code. The only optional model touch is a *cheap* "does this output look like real
  content vs a stub?" check on the JSON — and even that can be a deterministic
  heuristic (non-empty arrays, timestamps in range, no "TODO"/"example"/"lorem"). No
  Claude.
- **Loop-back:** on failure, write the failing assertions + captured stderr into
  `state.build.feedback` and **return to S5** (same Claude session via `--resume`),
  bounded by `--max-build-iterations` (default 3). After the cap → GATE (human).
- **In:** `state.build`, real test assets.
- **Out (`state.test`):** `{built:true,vet:true,tests:"3 pass",ran_on:"test.mp4",
  json_valid:true,exit_code:0,stderr_quiet:true,real_output_snippet:"...",
  iterations:2,passed:true}`.

### S7 — SECOND-AI FEEDBACK  ·  runner: a DIFFERENT model (cheap API / local)
- **Does:** a *different* model/agent reviews the built tool + its real output + its
  spec and reports issues (correctness, house-rule violations, philosophy
  violations, missed degradations, security). Mirrors the project's existing
  "second-AI feedback" habit and the `code-review`/`codex-code-review` pattern. The
  output is a structured review report attached to the run; it does NOT auto-edit.
- **Who & why offload:** **explicitly NOT Claude** (the point is a second, independent
  model — and it conserves the Claude credit). Use a cheap/free reviewer: a local
  model for offline/private runs, or a cheap API (e.g. an OpenRouter model, or
  `codex`/Gemini) for a genuinely different vendor's eyes. Findings above a severity
  threshold loop back to S5 once (optional, `--apply-review`), else are surfaced to
  the human at GATE C. The existing `becky-review` `openrouter` backend already proves
  this transport works.
- **Privacy caveat (load-bearing for forensic context):** for sensitive forensic
  tools, the reviewer must be **local-only** (the diff/output may contain case
  context). A remote reviewer is allowed ONLY for non-sensitive utility tools, and
  Gemini's free tier may train on prompts (verified — see sources) so it is
  forbidden for anything sensitive. Default reviewer = local; remote is opt-in.
- **In:** `state.spec`, `state.build` (the diff/source), `state.test.real_output_snippet`.
- **Out (`state.review`):** `{reviewer:"local:qwen3-4b|openrouter:<model>",
  findings:[{severity,category,file,line,issue,suggestion}], blocking_count, note}`.

### S8 — FACT-FORCING-GATE DISCIPLINE  ·  cross-cutting (not a node)
- See §6. Enforced *inside* S5 (and S4 if Claude writes the spec) via a PreToolUse
  hook passed to the Claude agent. The orchestrator also records, in run-state, the
  4 facts the agent stated, so the gate is auditable after the fact.

### S9 — AUTO-RESEARCH FINETUNE  ·  runner: deterministic harness (becky-eval) + cheap model graded
- **Does:** tune the new tool's settings/prompts against a **real eval**, exactly the
  `AUTORESEARCH-SPEC.md` pattern, by **reusing `becky-eval` (tool #24)** — do not
  reimplement scoring. The orchestrator: (1) emits a `becky-eval` manifest pairing
  real input(s) with answer-key FACTS for the new tool's capability (for forensic
  tools, recall-weighted, false-positives lightly penalized, with a holdout split);
  (2) defines the config/prompt SEARCH SPACE for the new tool; (3) runs `becky-eval`,
  which executes the new tool once per (case × config) and scores output vs the
  answer key deterministically; (4) records the best config/prompt and writes it back
  as the tool's default (a deterministic edit to the tool's prompt/config constant or
  a `config/<tool>.prompt.md`), then re-runs `becky-eval` to confirm the win
  generalizes on the holdout.
- **Who & why offload:** the **harness + scoring are deterministic** (`becky-eval` is
  offline, deterministic, resumable). The *tool under test* may itself call a cheap/
  local model (e.g. a Gemma/validate-style tool) — but that's the tool's own model,
  not a `becky-new-tool` cost. **Claude is invoked here ONLY** in the rare case a
  *prompt rewrite* between eval rounds needs real authoring help (e.g. the forensic
  prompt-engineering "gymnastics" that tuned `becky-validate`); even then it's one
  bounded call, optional (`--finetune-author claude|cheap|off`). For tools with no
  meaningful tunable surface, S9 is skipped with a note.
- **In:** `state.spec` (declares the tunable surface + the answer-key facts),
  `state.build.binary_path`, real corpus.
- **Out (`state.finetune`):** `{manifest_path, eval_report_path, best_config:{...},
  train_recall, holdout_recall, applied:true, generalization_caveat:"tuned on N clips"}`.

### S10 — INTEGRATE  ·  runner: deterministic
- **Does:** the becky integration checklist, all single-line/append edits per the
  briefing: append `<name>` to the `set TOOLS=` line in `build-all-tools.bat`; add the
  tool's row to `PROGRESS.md` (its row only); optionally a one-line mention in
  `SKILL.md` if it's user-facing; run `build-all-tools.bat` and confirm green;
  re-run the tool once post-integration. Produces a PR-ready summary (analyze full
  build, draft summary, test-plan TODOs — per the git-workflow rule), but does NOT
  commit/push unless asked.
- **Who & why offload:** **fully deterministic** string-append edits + running the
  build. No model. (The PR *body* prose can be a cheap-model summary of the run-state;
  optional.)
- **In:** all prior state.
- **Out (`state.integrate`):** `{build_all_green:true, progress_row_added:true,
  tools_line_updated:true, post_integrate_run:"exit 0", pr_summary_path}`.
- **GATE C (after S10):** human reviews the whole run (spec, real output, second-AI
  findings, eval result) and approves merge/commit. Commit/push only on explicit ask
  (per git-workflow rule + the global "commit only when the user asks").

---

## 4. Run-state, artifacts, resumability

- **Run directory:** `X:\AI-2\becky-tools\.factory\<slug>-<YYYY-MM-DD>\` holding
  `state.json` (the typed run-state), `research.md`, `SPEC-<NAME>.md`, the agent's
  stream-json log, `eval-manifest.json`, `eval-report.json`, `second-ai-review.json`,
  `pr-summary.md`. The tool **never** writes into `cmd/`/`internal/` itself except via
  the S10 deterministic integration edits and the agent's own writes during S5.
- **`state.json`** is the single source of truth, one top-level key per stage
  (`intake`,`research`,`redundancy`,`spec`,`build`,`test`,`review`,`finetune`,
  `integrate`) plus `meta:{slug,started,stage_cursor,gates_passed,claude_cost_usd_total}`.
- **Resumable + deterministic control flow:** each stage checks "am I already done in
  state.json?" and skips if so (like `becky-pipeline`'s per-step resume and
  `becky-eval`'s per-(case,config) cache). `--resume` continues from the cursor;
  `--force <stage>` re-runs one stage; `--from <stage>`/`--until <stage>` bound the
  run. The orchestrator's loop is a fixed sequence — **no model chooses the next
  stage.**
- **Gates** are explicit pause points (`A` after S3, `B` after S4, `C` after S10).
  Default = stop and require `becky-new-tool --approve <gate>` (or `--yes` to
  auto-pass a gate for trusted runs). Gate policy is config (§Open Q3).

---

## 5. The headless Claude Code agent (VERIFIED 2026-06-08)

This is load-bearing: stages S5 (build) and optionally S4 (spec) and S7-author drive
a headless Claude Code agent from Go. The exact, current invocation:

### 5.1 Can a Go CLI spin up a headless Claude Code agent as a stage? — YES (verified)
The official docs confirm non-interactive "print mode": `claude -p "<prompt>"` runs
Claude Code with the full agent loop and tools, no interactive terminal, and exits.
It is explicitly intended for "automation, scheduled tasks, and multi-agent
workflows." The becky repo already does exactly this in `cmd/review/backend.go`
(the `claude-code` backend), which is a working in-repo proof — but it predates
several newer flags below and should be upgraded into the shared `internal/agentrun/`
helper. (Sources: Claude Code "Run Claude Code programmatically" + CLI reference,
fetched 2026-06-08.)

### 5.2 The exact command (recommended shape for S5)
```
claude -p
  --append-system-prompt-file <briefing>  # BUILD-AGENT-BRIEFING.md as the build system layer
  --output-format stream-json --verbose   # live progress events -> run log (or `json` for one-shot)
  --include-partial-messages              # (with stream-json) token-level progress if desired
  --permission-mode acceptEdits           # write files w/o prompts; shell still gated by --allowedTools
  --allowedTools "Read,Edit,Write,Bash(go *),Bash(ffmpeg *),Bash(.\\bin\\becky-* *),Glob,Grep"
  --add-dir <X:\AI-2\becky-tools\becky-go>  # grant file access to the build root
  --model <opus|sonnet|full-id>           # pick the build model explicitly (cost lever)
  --max-turns <N>                         # hard stop on agentic turns
  --max-budget-usd <D>                    # hard stop on spend (print mode only)
  --fallback-model sonnet                 # auto-fallback if primary overloaded/retired
  --session-id <fixed-uuid>               # fixed per run -> S6 failures resume via --resume <uuid>
  # PROMPT (the approved spec + the concrete task) is delivered on STDIN, not as a -p arg.
```
- **Prompt on stdin, not argv** (the becky-review lesson, still valid): on Windows the
  CLI is a `claude.ps1`/`claude.cmd` shim and a large multi-line `-p` arg gets mangled
  through cmd.exe. Pipe the prompt to `cmd.Stdin`. (Verified: docs show
  `cat file | claude -p "..."`; stdin is the documented pipe path. Note the 10 MB
  stdin cap as of v2.1.128 — fine for a spec; for anything larger, reference a file
  path in the prompt instead.)
- **`--bare` vs `cmd.Dir=TempDir()`:** the becky-review code runs from `os.TempDir()`
  to dodge project-CLAUDE.md auto-discovery. The current, documented way is `--bare`,
  which skips hooks/skills/plugins/MCP/auto-memory/CLAUDE.md by design. **Caveat:**
  `--bare` also skips OAuth/keychain, so it requires `ANTHROPIC_API_KEY` (or an
  `apiKeyHelper` via `--settings`). For a subscription-auth (Max) build that should
  NOT use an API key, **do not use `--bare`**; instead keep the becky-review approach
  (run from a neutral dir; supply the briefing via `--append-system-prompt-file`) and
  use `--setting-sources` to control what's loaded. (Decision Q2.)
- **Fact-Forcing-Gate** is injected as a PreToolUse hook for the agent (see §6); with
  `--bare` hooks are skipped, which is another reason a subscription-auth build keeps
  non-bare mode + an explicit `--settings`/hook config. (Q2.)

### 5.3 Driving it deterministically + capturing output
- Run via Go `exec.CommandContext(ctx, claudeBin, args...)`, `ctx` with a per-stage
  timeout (the becky-review pattern). Resolve the binary with `LookPath("claude",
  "claude.cmd","claude.exe")`.
- **One-shot capture (`--output-format json`):** parse the envelope. Verified fields:
  top-level `result` (the text), `session_id`, `total_cost_usd` (+ a per-model cost
  breakdown), `is_error`, `subtype` (`"success"` on success). With `--json-schema`,
  the validated object is in **`structured_output`** instead of free-text `result` —
  use this for S4's machine-readable spec parts and any stage where you want a typed
  return with no brittle parsing.
- **Streaming capture (`--output-format stream-json --verbose`):** newline-delimited
  JSON events; the first is `system/init` (model, tools, plugins — assert the build
  model loaded), `system/api_retry` events expose retry/backoff, and a final result
  event carries the same envelope. Use this for S5 so the run log shows live progress
  and so a stuck build can be detected and `--max-turns`/budget enforced.
- **Background-task note:** if the build agent starts a background Bash task (e.g. a
  watcher), it's killed ~5 s after the final result + stdin close — fine for our
  build/test commands which are synchronous.

### 5.4 Where it lives — new `internal/agentrun/`
A small shared package: `agentrun.Run(ctx, AgentSpec) (AgentResult, error)` where
`AgentSpec{SystemPromptFile, PromptStdin, Model, MaxTurns, MaxBudgetUSD, AllowedTools,
AddDirs, SessionID, Stream bool, JSONSchema string}` and `AgentResult{Result,
StructuredOutput json.RawMessage, SessionID, CostUSD, IsError, Subtype, Events}`.
`becky-review` and `becky-validate` should be refactored to call it (removes the
duplicated invocation in `cmd/review/backend.go`). This is the only *new shared* code;
it is not a public tool. (Mirrors how `internal/faceembed` was extracted to share one
face-embed runner across `becky-identify` + `becky-events`.)

### 5.5 Cost control — VERIFIED economics reframe the "conserve Claude Max" goal
**Verified 2026-06-08:** As of **June 15, 2026**, `claude -p` / Agent SDK usage on
subscription plans **no longer counts toward interactive plan limits**; instead each
user gets a **separate monthly Agent-SDK credit** — **$20 (Pro) / $100 (Max 5x) /
$200 (Max 20x)**, per-user, not pooled. When that credit is exhausted, usage spills to
standard **API rates** if usage-credits are enabled, otherwise headless requests
**stop until the credit refreshes**. (Source: support.claude.com article 15036540,
fetched 2026-06-08.)

Implications baked into this spec:
- "Conserve Claude Code Max" now concretely means **conserve the monthly Agent-SDK
  credit** (and avoid spilling to API rates). So the pipeline minimizes Claude-agent
  invocations: **only S5 is mandatory-Claude**; S2/S4/S7 default to cheap/local and
  escalate to Claude only on a quality gate.
- The orchestrator tracks `claude_cost_usd_total` from each call's `total_cost_usd`
  and supports `--claude-budget-usd <D>` across the whole run + `--max-budget-usd` per
  call; exceeding the run budget pauses at a gate rather than silently spending.
- For unattended CI builds, `claude setup-token` (verified command) generates a
  long-lived OAuth token; alternatively `ANTHROPIC_API_KEY` bills the API directly
  (clean separation from the subscription credit). Choice is config. (Q2.)

---

## 6. The Fact-Forcing Gate (cross-cutting, S8)

The project's "Fact-Forcing Gate" is a **PreToolUse hook** that BLOCKS the agent's
first `Write` of a new file (and, per the request, edits/destructive cmds) until the
agent states 4 facts (from `BUILD-AGENT-BRIEFING.md` + demonstrated in
`BECKY-VALIDATE-PROPOSAL.md`; this very spec triggered the same gate when written):
1. **Callers** — who/what runs this file.
2. **No-dup** — confirm checked existing code, naming what was checked.
3. **Data shape** — input read + JSON emitted (1 line each).
4. **Verbatim instruction** — quote the spec line requiring this file.

How `becky-new-tool` enforces it deterministically:
- The hook is provided to the S5 (and S4-if-Claude) agent as a project hook config.
  Because the agent runs the becky build, the gate fires on its new-file writes, and
  the agent must answer before proceeding — exactly as a human-run build would hit it.
- **Interaction with `--bare`:** `--bare` skips hooks. Therefore S5 **must not** use
  `--bare`; it runs non-bare with an explicit hook config (via `--settings` /
  `--setting-sources`). This is the second reason §5.2 prefers non-bare for the build
  stage. (The S2/S4-cheap/S7 stages don't write repo files, so the gate is moot there.)
- **Auditability:** the orchestrator parses the agent's stated 4 facts out of the
  stream-json transcript and records them in `state.build.fact_forcing` so the gate's
  satisfaction is provable after the run, not just enforced live.
- The gate also covers S10's deterministic edits trivially: S10 is code, and the
  4 facts for the integration edits are constant/derivable, so it self-certifies in
  state without a model.

---

## 7. Which stages offload off Claude — summary table

| Stage | Default runner | Can offload to cheap/local? | Claude needed? | Notes |
|---|---|---|---|---|
| S1 Intake | det + cheap | Yes (tiny extraction) | No | local SLM or Gemini Flash-Lite |
| S2 Research | det fetch + cheap synth | Yes; web fetch is det | Only on quality-gate fail | registries = pure HTTP |
| S3 Redundancy | det evidence + cheap judge | Yes | Only if low-confidence + costly | judge sees only tool summaries |
| GATE A | human | — | — | auto-pass policy configurable |
| S4 Spec | **Claude (toggle cheap)** | Yes for simple tools | Default yes; `--spec-backend cheap` | `--json-schema` -> typed spec |
| GATE B | human | — | — | approve spec before code |
| S5 Build | **Claude (Ralph loop)** | **No** | **Yes (mandatory)** | the one irreducible Claude stage |
| S6 Test | **det** | n/a | No | "valid JSON is not proof" asserts |
| S7 Second-AI | **different model: cheap/local** | Yes (must be non-Claude) | No (by design) | local-only for sensitive tools |
| S8 Fact-Gate | hook inside S5/S4 | n/a | rides the S5 agent | non-bare mode required |
| S9 Finetune | **det (becky-eval)** + cheap-under-test | Yes; scoring is det | Only optional prompt-rewrite | reuse tool #24, never reimplement |
| S10 Integrate | **det** | n/a | No | single-line appends + build-all |
| GATE C | human | — | — | merge/commit only on ask |

**Cheap/local options verified 2026-06-08 (pick per privacy + offline need):**
- **Local (offline, private — preferred for forensic):** small instruct models that
  fit the 8 GB RTX 3070 via the existing `C:\llama.cpp` (`llama-server`) or Ollama —
  e.g. Qwen3-class ~4-8B or Phi-4-mini (~3.5 GB at Q4_K_M), ample for intake
  extraction, redundancy judgment, research synthesis, and second-AI review. (Source:
  Local-LLM 8 GB guides, 2026.)
- **Cheap/free API (non-sensitive utility tools only):** OpenRouter free models (e.g.
  DeepSeek/Qwen/Nemotron `:free`, ~20 RPM/200 RPD, may change without notice) or
  Google Gemini free tier (Flash / Flash-Lite, 1500 RPD / 15 RPM). **Privacy caveat:**
  Gemini's free tier may use prompts for training, and OpenRouter free routes vary —
  so both are **forbidden for sensitive forensic content**; those runs use local
  models only. (Sources: OpenRouter free-models + Gemini rate-limit docs, 2026.)

---

## 8. CLI contract (proposed, mirrors the becky house style)
```
becky-new-tool --pain "<text>" | --intake-file <json>      # S1 input
  --run-dir <dir>            # default .factory\<slug>-<date>
  --resume | --from <stage> | --until <stage> | --force <stage>
  --approve <gateA|gateB|gateC> | --yes      # gate control
  --spec-backend claude|cheap                # S4 author (default claude)
  --review-backend local|openrouter|codex    # S7 reviewer (default local)
  --finetune-author claude|cheap|off         # S9 prompt-rewrite author (default cheap)
  --model <build-model>                      # S5 Claude build model
  --max-turns <N> --max-budget-usd <D>       # per Claude call
  --claude-budget-usd <D>                    # whole-run Claude budget
  --max-build-iterations <N>                 # S5<->S6 loop cap (default 3)
  --offline                                  # skip all web/remote; local models only
  --bin <dir>                                # where becky-*.exe (becky-eval) live
  --verbose                                  # progress to stderr
# stdout: the run-state JSON (machine-readable); stderr: plain-English stage headlines.
# exit 0 on a completed run or a clean pause at a gate; non-zero on a hard error.
```
Output discipline follows `FORENSIC-OUTPUT-PHILOSOPHY.md` + the becky norm: JSON to
stdout, headlines/progress to stderr, never half-JSON, degrade-with-a-note over crash.

---

## 9. How S9 reuses `becky-eval` (deterministic finetune) — concretely
1. `becky-new-tool` synthesizes an eval **manifest** in `becky-eval`'s existing
   schema (cases pairing real video/JSON with answer-key FACTS = alias lists +
   weights + categories; a holdout split), derived from the spec's declared
   "tunable surface" + "answer-key facts" fields (S4 must populate these).
2. It defines the **config/prompt search space** (e.g. system-prompt variants,
   threshold values, flag combos) as the manifest's per-case `configs`.
3. It invokes `becky-eval <manifest> --bin-dir <bin> [--server-url ...]` as a
   subprocess. `becky-eval` runs the new tool once per (case × config), flattens
   output to a recall haystack, scores **weighted recall** (false-positives lightly
   penalized — forensic recall-first), ranks on the train split, and re-measures the
   best on the **holdout** (generalization guard). All deterministic, offline,
   resumable — no `becky-new-tool` model cost here.
4. `becky-new-tool` reads the ranked report, applies the best config/prompt as the
   tool's default via a deterministic edit (constant or `config/<tool>.prompt.md`),
   then re-runs `becky-eval` to confirm the holdout win and records the
   generalization caveat ("tuned on N clips — illustrative, expand before trusting").
This is precisely how `becky-validate`'s two-stage forensic prompt was tuned (rows 22
+ 24 of `PROGRESS.md`); the pipeline just makes it a stage instead of a manual pass.

---

## 10. Honest open questions (need a human decision)
- **Q1 — Conversational intake / `becky ask`:** Should S1 get a chat front-end? Rec:
  add a thin `becky ask` *verb* later that emits intake JSON into `becky-new-tool`;
  do NOT build a separate `becky-ask` binary. Confirm.
- **Q2 — Auth model for the build agent:** subscription credit (no `--bare`, no API
  key, hooks work, Fact-Gate enforceable) vs `ANTHROPIC_API_KEY`/`setup-token` for
  unattended CI (allows `--bare`, but `--bare` disables hooks → Fact-Gate must move to
  in-prompt instruction). The spec defaults to **non-bare + subscription credit** so
  the Fact-Forcing Gate and the metered-credit conservation both hold; CI is opt-in.
  Needs sign-off because it interacts with the June-15-2026 credit pool.
- **Q3 — Gate auto-pass policy:** exact thresholds for auto-passing GATE A (redundancy
  confidence >= ?, research sources >= ?) and whether GATE B can ever auto-pass for
  "trivial" tools. Default: A auto-passes on `new_tool & conf>=0.7 & research-ok`; B
  and C always require a human unless `--yes`.
- **Q4 — Cheap-model trust per stage:** which stages are allowed to be cheap-only with
  no human check (intake yes; redundancy/spec only behind a gate?). Tune after a few
  real runs; start conservative (gates on).
- **Q5 — Second-AI vendor:** local model vs `codex` vs OpenRouter for S7 on
  non-sensitive tools — confirm the default reviewer and the severity threshold that
  loops back to S5.
- **Q6 — Could-not-verify:** the `claude -p --output-format json` field set is
  confirmed for `result`/`session_id`/`total_cost_usd`/`is_error`/`subtype`/
  `structured_output`; the precise top-level key for the per-model cost *breakdown*
  (vs the scalar `total_cost_usd`) was described but not field-named in the docs I
  fetched — confirm by running `claude -p "hi" --output-format json` once on this
  machine before relying on it in code. Also re-verify all model ids / free-tier
  limits at build time (they drift fast; the `claude-api` skill is the in-repo
  re-verification path for Claude model ids/pricing).

---

## 11. Sources (live, verified 2026-06-08)
- Claude Code — Run programmatically / headless (`claude -p`, `--bare`,
  `--output-format`, `--json-schema`->`structured_output`, stream-json envelope,
  `--continue`/`--resume`, stdin pipe + 10 MB cap, June-15-2026 credit note):
  https://code.claude.com/docs/en/headless  (fetched 2026-06-08)
- Claude Code — CLI reference (every flag: `--append-system-prompt[-file]`,
  `--system-prompt[-file]`, `--max-turns`, `--max-budget-usd`, `--fallback-model`,
  `--permission-mode dontAsk`, `--session-id`, `--add-dir`, `--setting-sources`,
  `--json-schema`, `claude setup-token`, `claude auth status`):
  https://code.claude.com/docs/en/cli-reference  (fetched 2026-06-08)
- Agent SDK overview (CLI / Python / TS; same harness as Claude Code):
  https://platform.claude.com/docs/en/agent-sdk/overview  (verified 2026-06-08)
- Use the Claude Agent SDK with your Claude plan (the **June 15 2026** separate
  per-user monthly Agent-SDK credit: $20 Pro / $100 Max-5x / $200 Max-20x; spill to
  API rates; not pooled):
  https://support.claude.com/en/articles/15036540  (fetched 2026-06-08)
- Best local LLMs for 8 GB VRAM 2026 (Phi-4-mini ~3.5 GB Q4_K_M; Qwen3 ~6-8 GB;
  router SLM-local-vs-cloud pattern):
  https://localllm.in/blog/best-local-llms-8gb-vram-2025 ;
  https://localaimaster.com/blog/small-language-models-guide-2026  (2026)
- OpenRouter free models (~29 free, ~20 RPM/200 RPD, may change w/o notice):
  https://openrouter.ai/collections/free-models  (2026)
- Gemini API free tier (Flash/Flash-Lite only; 1500 RPD / 15 RPM; **free-tier prompts
  may be used for training** — privacy caveat for forensic content):
  https://ai.google.dev/gemini-api/docs/rate-limits  (2026)
- In-repo working proof of the headless invocation (predates newer flags; to be
  upgraded into `internal/agentrun/`): `becky-go/cmd/review/backend.go`
  (`claudeCodeBackend`).
- Routine sources formalized here: `BUILD-AGENT-BRIEFING.md`, `AUTORESEARCH-SPEC.md`,
  `FORENSIC-OUTPUT-PHILOSOPHY.md`, `BECKY-VALIDATE-PROPOSAL.md`,
  `BECKY-ORCHESTRATOR-SPEC.md`, `PROGRESS.md` (rows 10/22/24/25/26), `SKILL.md`.
