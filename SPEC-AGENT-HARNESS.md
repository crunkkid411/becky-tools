# SPEC — `becky-harness` — the per-request agent harness that drives a Pi agent over a declared set of becky tools

> ## STATUS — SPEC, NOT BUILT, AWAITING APPROVAL
> Author: cloud/web research agent, drafted **2026-06-14**. Pi (`earendil-works/pi`,
> a.k.a. `badlogic/pi-mono`) was researched live this session; every external claim
> carries an inline source + date in §11. The local agent must re-verify Pi flags
> and the RPC/JSON record shapes against the installed version before wiring code
> (they are pre-1.0 and move) — items flagged **ASSUMPTION (local agent must
> verify)** are the ones most likely to drift. Nothing here is built yet; the cloud
> agent ships the Go scaffolding + a faked-Pi unit suite, the local agent installs
> Pi (Node) and does the one real run. This SPEC obeys the same house rules as
> `SPEC-BECKY-NEW-TOOL.md` / `SPEC-BECKY-ASK.md`: JSON in / JSON out, exit-coded,
> offline-capable, degrade-never-crash, reuse `internal/`, never modify source
> inputs.

---

## 0. TL;DR — one line, then the place in the catalog

`becky-harness` is the tool that, **per request**, spins up a configured Pi agent,
hands it exactly the becky tools that request declared (and nothing else), and lets
the agent run a repetitive, multi-step becky workflow to completion — then returns a
single JSON result plus a full logged transcript.

It does **ONE** thing (the single-tool principle): **run a declared agent over a
declared tool allowlist to execute one workflow.** It is *universal per request* —
the workflow, the tools, the model/provider, and any skill/prompt template are all
chosen by the request, not baked in. The agentic reasoning ("what step next?") lives
in the **Pi runtime**; becky owns the deterministic boundary around it (the request
schema, the tool manifest, the transcript, the result normalizer, the safety gates).

**Where it sits — and how it differs from the two front-doors we already have:**

| Tool | What it is | Who/what decides the next step | Determinism |
|---|---|---|---|
| `becky` (orchestrator) | the **command engine** — `becky <verb> "<arg>" --flags`, chains the sharp `becky-*` tools | a **human/script** types fixed ops; no model picks steps | fully deterministic |
| `becky-ask` | the **chat front-door** — fuzzy English → a *plan* or a new-tool pitch; classifies with a cheap local model, runs **only on opt-in** | a human approves each plan; the model classifies intent **once**, it does not loop | deterministic control flow; model at two narrow points |
| **`becky-harness`** (this spec) | the **agent harness** — a Pi agent **loops** (reason → call a becky tool → read result → repeat) until a declared goal is met | **the model loops autonomously** inside Pi, over a fixed tool allowlist | non-deterministic *inside* the loop; deterministic *around* it (§7) |

So the progression is: `becky` = you drive; `becky-ask` = you talk, it plans, you
approve; `becky-harness` = you declare a goal + a toolbox + guardrails once, and an
agent grinds through the repetitive workflow on its own. `becky-harness` is the right
tool when the same fiddly multi-step job ("for every clip in this folder, transcribe
it, identify speakers, check who's named in the transcript, and flag mismatches")
must be run again and again and is too branchy to hard-code as a `becky` recipe but
too mechanical to babysit in `becky-ask`.

**It does NOT replace either.** `becky-ask` can *offer* to run a templated workflow by
shelling `becky-harness` (§9); `becky-harness` never reimplements a sharp tool — it
only *calls* the existing `becky-*.exe` through Pi.

---

## 1. Why Pi, and what Pi actually is (verified this session)

Pi is an MIT-licensed TypeScript monorepo by Mario Zechner (badlogic; creator of
libGDX), now under the `earendil-works` org (moved there April 2026 when Zechner
joined Earendil, the PBC co-founded by Armin Ronacher); the old path
`badlogic/pi-mono` still resolves. [src: GitHub repo + the launch write-ups, §11]
Its packages:

- **`pi-ai`** — a unified LLM API across 25+ providers (Anthropic, OpenAI, Google,
  Azure, DeepSeek, AWS Bedrock, Mistral, Groq, …), with streaming, tool-calling,
  thinking levels, and token/cost tracking. [§11]
- **`pi-agent-core`** — the agent runtime: the tool-calling loop, argument validation,
  state management, event streaming.
- **`pi-tui`** — a terminal-UI library (differential rendering). Not used headlessly.
- **`pi-coding-agent`** — the interactive CLI (`pi`), built on a **4-tool core**
  (`read`, `write`, `edit`, `bash`) and **self-extending at runtime** via TypeScript
  **Extensions**, **Skills**, **Prompt Templates**, and **Themes**. It authenticates
  either by **subscription `/login`** (Anthropic Claude Pro/Max, OpenAI ChatGPT
  Plus/Pro, GitHub Copilot — OAuth, no API key) **or** by API key / OpenAI-compatible
  base URL. [§11]

**Why Pi is the right runtime for this tool (not a hand-rolled loop, not the Claude
Agent SDK):**

1. **Subscription auth = minimal fuss for Jordan.** `pi` then `/login` uses an
   existing Claude Pro/Max (or ChatGPT/Copilot) subscription — no API key to mint,
   store, or leak. That matches the becky goal of "Jordan does as little plumbing as
   possible." (The `becky-new-tool` build agent already leans on subscription-credit
   conservation; Pi extends the same logic to general workflows, on a *different*
   metering surface — see §7.) [src: coding-agent README `/login`, §11]
2. **A clean machine-driving surface already exists.** Pi ships `--mode rpc` and
   `--mode json` (LF-delimited JSONL over stdin/stdout) **plus** `-p/--print` one-shot
   — purpose-built for "embed the agent in another program," which is exactly the Go
   orchestrator → Pi boundary we need. We do not have to fork a TUI. [§11]
3. **Custom tools are first-class and language-light.** A becky tool is exposed to the
   agent by an Extension that `pi.registerTool({...})` — name, description, a Typebox
   parameter schema, and an `execute()` that shells the real `becky-*.exe`. We
   *generate* these from becky's own CLI catalog (§4), so the manifest never drifts
   from the binaries.
4. **Offline / local models are supported.** `pi-ai` speaks OpenAI-compatible
   endpoints, so a local `llama.cpp` `llama-server` (already used by `becky-ask` and
   `internal/avlm`) or any OpenAI-API-shaped local server can back the agent for
   sensitive forensic runs with no network. [src: pi-ai provider list incl. OpenAI-
   compatible, §11 — **ASSUMPTION (local agent must verify)** the exact `--provider`/
   `--base-url` form for a bare llama.cpp endpoint.]
5. **Tight tool allowlisting is built in** (`--tools <list>`, `--no-builtin-tools`,
   `--exclude-tools`) — so a request can give the agent *only* the becky tools it
   declared and **disable Pi's own `write`/`edit`/`bash`** when the workflow is
   read-only. That is the lever behind our per-request safety guarantee (§7). [§11]

---

## 2. The "universal per request" idea — the request spec

Every run of `becky-harness` is configured by **one request document** (a small JSON
file, or `--goal "..."` + flags for the common case). The request is the *whole* point
of the tool: it declares, for this run only, **which tools, which model, which
skill/template, and what the goal is.** Nothing is global; two requests can give the
agent totally different toolboxes and models. This is what "universal per request"
means — `becky-harness` is a generic engine; the request specializes it.

### 2.1 Request schema (the contract)

```jsonc
{
  "schema": "becky-harness/request@1",
  "goal": "For every .mp4 in the target folder, transcribe it, identify the speakers, and report any clip where a name SAID in the transcript is not among the identified speakers.",
  "target": "X:\\cases\\smith\\clips",          // file | folder | list; passed to the agent as context, never assumed to exist silently
  "tools": [                                       // the ALLOWLIST — the ONLY becky tools the agent can call
    { "tool": "becky-transcribe" },
    { "tool": "becky-identify", "fixed_flags": ["--kb", "kb-final"] },
    { "tool": "becky-search",   "allow": false }   // explicit deny example (default for unlisted tools is deny)
  ],
  "builtin_tools": "read-only",                    // none | read-only | full  (maps to Pi --no-tools / read+no-write+no-bash / full) — default: none
  "model": { "provider": "anthropic", "id": "sonnet", "thinking": "low" }, // or {"provider":"openai-compatible","base_url":"http://127.0.0.1:8080/v1","id":"local"}
  "auth": "subscription",                          // subscription (/login) | api-key | local  — default: subscription
  "skill": "X:\\becky-tools\\harness\\skills\\name-mismatch.md",   // optional Pi Skill (repeatable as "skills":[...])
  "prompt_template": null,                         // optional Pi prompt template path
  "system_append": "Recall is for DETECTION; attach a NAME only when corroborated. Plain words, never jargon.", // appended to Pi's system prompt; bakes in FORENSIC-OUTPUT-PHILOSOPHY.md
  "limits": { "max_turns": 40, "max_budget_usd": 2.00, "timeout_sec": 1800 },
  "dry_run": false,                                // true = build manifest + print the plan, run NOTHING
  "result_contract": {                             // what the agent must emit as its final answer (validated)
    "format": "json",
    "shape": "array of { clip, said_names[], identified_names[], mismatch[] }"
  }
}
```

Field rules (deterministic, enforced by the Go orchestrator before Pi is launched):

- **`tools`** — the request's tool **allowlist**. *Default-deny:* a becky tool the
  request does not list is **not** exposed to the agent, full stop. Each entry may pin
  `fixed_flags` (always passed, e.g. `--kb kb-final`) and may carry `allow:false` for
  an explicit, documented denial. A tool whose name isn't in becky's generated catalog
  (§4) is a hard request error (exit 2) — no silent drop.
- **`builtin_tools`** — controls Pi's own 4-tool core. `none` (default) → the agent has
  **only** becky tools; `read-only` → Pi's `read` stays, `write`/`edit`/`bash` are
  excluded; `full` → all four (required only for workflows that must, e.g., write an
  exhibit file). This is the single biggest safety dial (§7).
- **`model` / `auth`** — picks the provider+model and how it authenticates.
  `subscription` is the default (no key; Jordan's existing plan via `/login`).
  `local` selects an OpenAI-compatible endpoint for fully-offline forensic runs.
- **`skill` / `prompt_template` / `system_append`** — optional Pi resources layered in
  per request. `system_append` is where the request bakes in the relevant
  `FORENSIC-OUTPUT-PHILOSOPHY.md` lines so the agent's *narration* obeys the house
  output contract even though its *control flow* is autonomous.
- **`limits`** — hard stops (turns, budget, wall-clock). Exceeding any is a clean,
  logged stop with a partial result — never a crash (§7, §6).
- **`result_contract`** — what the agent's final message must be. The normalizer (§6)
  validates the agent's last structured output against it; a miss degrades to
  `result.degraded` with the raw transcript attached, not a hard failure.

### 2.2 The common case stays a one-liner

The full JSON is for saved, repeatable workflows. For an ad-hoc run, flags cover it
and the orchestrator synthesizes the request:

```
becky-harness --goal "transcribe every clip in <folder> and flag name mismatches" \
  --target X:\cases\smith\clips \
  --tools becky-transcribe,becky-identify \
  --model sonnet --auth subscription
# equivalent to writing the request JSON above with defaults filled in.
```

---

## 3. The Go orchestrator ↔ Pi runtime boundary

```
  HUMAN or becky-ask  ──►  becky-harness.exe  (Go orchestrator — THIS tool)
                              │  deterministic, owns the safety boundary
   ┌──────────────────────────┼───────────────────────────────────────────────┐
   │ 1. parse + validate the REQUEST (schema, allowlist, limits)               │
   │ 2. GENERATE the Pi tool manifest from becky's CLI catalog (§4)            │
   │    → writes a per-run Pi EXTENSION (TypeScript) exposing ONLY the         │
   │      allowlisted becky-* tools, each shelling the real becky-*.exe        │
   │ 3. LAUNCH Pi headless: `pi --mode rpc` (or `--mode json`/`-p`) with       │
   │      --tools <allowlist> --no-builtin-tools|... -e <generated ext>        │
   │      --provider/--model, --skill, --append-system-prompt, limits          │
   │ 4. DRIVE Pi over stdin/stdout JSONL: send {type:"prompt", message:goal};  │
   │      stream events to the TRANSCRIPT log; enforce limits/timeout          │
   │ 5. NORMALIZE: validate the final answer vs result_contract → ONE JSON doc │
   └──────────────────────────┼───────────────────────────────────────────────┘
                              ▼
                    Pi runtime (Node, the agent LOOP)
                     • reasons, picks a becky tool, fills its params
                     • executes the tool (the generated extension shells becky-*.exe)
                     • reads the JSON result, loops, until the goal is met
                     • Pi's pi-ai talks to the model (subscription OAuth or local)
```

**Boundary rule (load-bearing):** becky-harness (Go) is **deterministic and owns every
guarantee**; the **non-determinism is wholly contained inside Pi's loop.** The Go side
never lets the model decide anything *outside* the declared toolbox: the allowlist is
enforced by what the generated extension even registers, **and** by Pi's `--tools`
flag, **and** by becky's own per-call check in each tool's `execute()` (defense in
depth — see §7). Results flow back as the becky tools' own JSON (each `becky-*.exe`
already prints one JSON doc to stdout), so nothing is reformatted or interpreted on
the way to the agent.

### 3.1 Why `--mode rpc` (default), with `-p` and `--mode json` fallbacks

- **`--mode rpc`** (default) — full duplex JSONL: the orchestrator sends
  `{"id":"req-1","type":"prompt","message":"<goal>"}`, receives a `response`
  confirmation, then a stream of events (`agent_start`, `message_update` with
  `assistantMessageEvent` deltas, `tool_execution_start/update/end`, `agent_end`). It
  can `abort`, `set_model`, or `steer` mid-run, and answer Pi's `extension_ui_request`
  dialogs deterministically (becky always answers non-interactively). This is the
  richest surface for logging and limit-enforcement. [src: rpc.md, §11]
  **CRITICAL framing note (verified):** split records on `\n` only; strip a trailing
  `\r`; **do not** use Node-style readers that split on Unicode `U+2028/U+2029` — Go's
  `bufio.Scanner` with a plain `\n` split is correct. [§11]
- **`-p` / `--print`** — one-shot: run the prompt, print the response, exit. Used for
  the simplest single-answer workflows and for `--dry-run` smoke tests. Pi also merges
  piped stdin into the initial prompt. [§11]
- **`--mode json`** — event stream to stdout with UI as no-ops; a lighter alternative
  to rpc when the orchestrator only needs to *observe* (no mid-run steering). [§11]

**ASSUMPTION (local agent must verify):** the exact record field names above
(`type`/`id`/`message`, the event-type strings) are from the current rpc.md but Pi is
pre-1.0; pin them against the installed version and adjust the Go structs in one place
(§5).

---

## 4. Surfacing becky's tools to Pi — the generated tool manifest

The agent must be able to *call* becky tools, but we **must not** hand-maintain a
parallel description of every tool (it would rot the moment a flag changes). So
becky-harness **generates** the Pi tool manifest from becky's own catalog at run time:

### 4.1 Source of truth for the catalog

becky already carries a machine-readable tool inventory in two forms the orchestrator
reads (no new metadata to invent):

1. The **`cmd/*` inventory** + each tool's top-of-file doc comment and `--help` flag
   listing — the same evidence `SPEC-BECKY-NEW-TOOL.md` §S3 (redundancy check) already
   enumerates. `becky-harness` reuses that enumeration.
2. `becky-ask`'s structured **`catalog.go`** (the 7 orchestrator ops + the sharp tools,
   each with a plain summary, a runnable example, and match keywords). This is the
   cleanest existing structured description; `becky-harness` should read the **same**
   catalog source so the two front-doors never disagree. (Refactor target: lift the
   catalog into a small shared `internal/catalog` both `cmd/ask` and `cmd/harness`
   import, rather than copying — mirrors how `internal/faceembed` was shared.)

### 4.2 What gets generated, per run

For each **allowlisted** tool the orchestrator emits one `pi.registerTool({...})` entry
in a single generated extension file (`<run-dir>/becky-tools.ext.ts`):

```typescript
// GENERATED per run by becky-harness — do not edit. One entry per allowlisted tool.
import type { ExtensionAPI } from "@earendil-works/pi-coding-agent";
import { Type } from "@sinclair/typebox";
import { execFile } from "node:child_process";

export default function (pi: ExtensionAPI) {
  pi.registerTool({
    name: "becky-transcribe",
    label: "Transcribe",
    description: "Transcribe an audio/video file to timestamped text. JSON in path, JSON out transcript.",
    parameters: Type.Object({
      input: Type.String({ description: "absolute path to the media file" }),
      format: Type.Optional(Type.String({ description: "srt|json|txt (default json)" })),
    }),
    async execute(toolCallId, params, signal, onUpdate, ctx) {
      // Shells the REAL becky-transcribe.exe; fixed_flags from the request are prepended.
      // Path is checked to exist; missing path -> a typed degrade result, never a throw-crash.
      const { code, stdout, stderr } = await runBeckyTool("becky-transcribe",
        ["--input", params.input, ...(params.format ? ["--format", params.format] : []), ...FIXED_FLAGS_becky_transcribe], signal);
      // becky-* already prints ONE JSON doc to stdout + headline to stderr.
      return { content: [{ type: "text", text: stdout || stderr }], details: { exit: code, tool: "becky-transcribe" } };
    },
  });
  // ... one more registerTool(...) per allowlisted tool ...
}
```

(`registerTool` signature — `name`, `label`, `description`, Typebox `parameters`,
`async execute(toolCallId, params, signal, onUpdate, ctx) → { content[], details }`,
throw to signal failure — is quoted from Pi's `docs/extensions.md`, §11.)

### 4.3 How results flow back (and stay honest)

- Each becky tool **already** emits **one JSON document to stdout** and a plain-English
  headline to stderr (the house contract). The generated `execute()` returns that JSON
  verbatim as the tool result text + the exit code in `details`. **No interpretation,
  no reformatting** — the agent reads exactly what the tool said.
- **Degrade, never crash inside a tool call:** if `becky-*.exe` is missing, the input
  path doesn't exist, or it exits non-zero, `execute()` returns a typed degrade result
  (`{ degraded: <reason>, exit: <code> }`) so the *agent* sees the failure and can
  route around it — Pi keeps looping; the harness does not abort the whole run on one
  bad clip. (This mirrors becky's per-tool "typed degrade error + partial result"
  invariant.)
- The orchestrator mirrors every `tool_execution_*` event into the transcript (§6), so
  there is a complete, auditable record of which becky tool ran with which args and
  what it returned.

### 4.4 The flag-mapping question (honest)

becky tools take **positional + `--flag`** CLI args; Pi tools take a **named-parameter
JSON object**. The generated `parameters` schema maps a tool's flags to named params.
For the first cut, the generator derives params from each tool's `--help`/doc comment;
**ASSUMPTION (local agent must verify):** the auto-derivation is good enough that the
agent fills params correctly, and where it isn't, a tool gets a small hand-written
param-map override (a `harness/toolmaps/<tool>.json`) — a bounded, per-tool fix, not a
rewrite. Start with the handful of tools the first templated workflows actually use
(transcribe, identify, validate, search, ocr) and expand.

---

## 5. The integration stub contract (so the local agent only installs Pi + wires the call)

Everything Pi-touching is isolated behind ONE small shared package, **`internal/pirun`**
(deliberately parallel to `internal/agentrun`, which wraps headless `claude -p`; this
wraps headless `pi`). The cloud agent ships the package with a **faked Pi** so the whole
Go layer is unit-tested with no Node present; the local agent installs Pi and flips it
to the real binary.

```go
// internal/pirun — the ONE place that knows how to drive a headless Pi agent.
// Mirrors internal/agentrun's shape so callers and reviewers recognize it.

type ToolSpec struct {                 // one allowlisted becky tool, generated into the Pi extension
    Name       string
    Label      string
    Description string
    Params     json.RawMessage         // Typebox/JSON-schema for the tool's named params
    FixedFlags []string                // always-prepended flags from the request
}

type PiSpec struct {
    Goal           string
    Target         string
    Tools          []ToolSpec          // the allowlist -> generated extension (§4)
    BuiltinTools   string              // "none" | "read-only" | "full"
    Provider       string              // anthropic | openai | google | openai-compatible | ...
    Model          string              // e.g. "sonnet", "sonnet:low", or "provider/id"
    BaseURL        string              // for openai-compatible/local endpoints (offline)
    Auth           string              // "subscription" | "api-key" | "local"
    Skills         []string            // --skill paths
    PromptTemplate string              // --prompt-template path
    SystemAppend   string              // --append-system-prompt (bakes in philosophy)
    MaxTurns       int
    MaxBudgetUSD   float64
    TimeoutSec     int
    DryRun         bool
    RunDir         string              // where the generated extension + transcript live
}

type PiResult struct {
    FinalText        string            // the agent's last assistant message
    StructuredOutput json.RawMessage   // parsed against result_contract when JSON
    ToolCalls        []ToolCallRecord  // {tool, args, exitCode, degraded, resultSnippet}
    Turns            int
    CostUSD          float64           // from Pi's token/cost tracking, if exposed
    Stopped          string            // "" | "max_turns" | "budget" | "timeout" | "error"
    Events           []json.RawMessage // raw rpc/json events, for auditing
    Degraded         bool
    DegradeReason    string
}

// ResolveBin finds a runnable pi entrypoint (pi, pi.cmd) or "" so callers degrade.
func ResolveBin() string

// GenerateExtension writes the per-run TypeScript extension exposing ONLY spec.Tools.
func GenerateExtension(spec PiSpec) (extPath string, err error)

// Run launches `pi --mode rpc` (or -p/--mode json), drives the goal over JSONL,
// enforces limits, mirrors events to the transcript, returns a parsed PiResult.
// A nil error means Pi RAN and produced a parseable result; it does NOT mean the
// workflow succeeded — check Stopped/Degraded (same discipline as agentrun.Run).
func Run(ctx context.Context, spec PiSpec, onEvent func(json.RawMessage)) (PiResult, error)
```

**The faked Pi (cloud-agent deliverable).** A `pirun_test.go` builds a tiny fake `pi`
(a Go test binary, resolved via `PI_BIN` env override) that speaks the JSONL RPC
protocol: it accepts a `prompt`, emits a canned `tool_execution_start/end` for one
becky tool, then an `agent_end` with a known final message. This lets the cloud agent
unit-test: request parsing, allowlist enforcement, extension generation, event→
transcript mirroring, limit/timeout stops, and result normalization — **all without
Node or a model.** The live test (`//go:build pi`) runs only on Jordan's machine.

**The local agent's whole job at the boundary** is then: (1) `npm install -g
@earendil-works/pi-coding-agent` (or the `pi.dev/install.sh` one-liner), (2) `pi` →
`/login` once for the subscription, (3) confirm `ResolveBin()` finds `pi`, (4) run the
`//go:build pi` live test + one real workflow on a real folder, (5) pin any drifted RPC
field names / flags in `internal/pirun` (one file). The contract above is everything
they need; no Go logic should require changing.

---

## 6. JSON in / JSON out, exit codes, transcript

Output discipline is the becky norm + `FORENSIC-OUTPUT-PHILOSOPHY.md`:

- **stdout** = exactly ONE JSON document — the normalized run result:
  ```jsonc
  {
    "schema": "becky-harness/result@1",
    "goal": "...", "target": "...",
    "model": { "provider": "anthropic", "id": "sonnet" },
    "tools_offered": ["becky-transcribe", "becky-identify"],
    "answer": { /* validated against request.result_contract, or null */ },
    "degraded": false, "degrade_reason": "",
    "stopped": "",                          // "" | max_turns | budget | timeout | error
    "tool_calls": [ { "tool": "becky-identify", "args": {...}, "exit": 0, "degraded": false } ],
    "turns": 23, "cost_usd": 0.41,
    "transcript_path": "X:\\...\\.harness\\<slug>-<date>\\transcript.jsonl"
  }
  ```
- **stderr** = plain-English headlines/progress ("Harness: 12/40 clips done; 2 name
  mismatches so far"), never JSON. The chat-facing caller (`becky-ask`) renders these.
- **A full transcript** of the Pi run is always written to the run dir as JSONL (every
  rpc/json event, every becky tool call + its result). This is the determinism/audit
  anchor for a non-deterministic runtime (§7): you can always see *exactly* what the
  agent did and reproduce the tool calls by hand.
- **Exit codes:** `0` = the run completed (even if it stopped at a limit with a partial
  answer — `stopped` says which); `2` = a request/usage error (bad schema, unknown tool
  in the allowlist) caught *before* launching Pi; `3` = Pi/model unavailable and the run
  could not start (degrade — see below). Never a panic.
- **Degrade, never crash:** if `pi` isn't installed, no model is reachable, or `/login`
  isn't done, `becky-harness` emits a JSON result with `degraded:true`,
  `degrade_reason:"pi CLI not found on PATH"` (or `"no model auth — run: pi then /login"`)
  and a plain stderr line telling Jordan the one command to fix it — then exits `3`. It
  never hangs and never emits half-JSON. (Same posture as `agentrun.ResolveBin()`
  returning `""` so callers degrade.)

---

## 7. Determinism & safety — what becky guarantees around a non-deterministic runtime

An agent loop is, by nature, **not** deterministic (same input may pick a different
order of tool calls; same model+seed is not guaranteed identical across a hosted
provider). `becky-harness` does **not** pretend otherwise. Instead it guarantees a hard
deterministic *shell* around the soft *core*:

1. **Declared tool allowlist, default-deny, enforced three ways.** The agent can call
   *only* the becky tools the request listed: (a) the generated extension **only
   registers** those tools; (b) Pi is launched with `--tools <allowlist>` and
   `--no-builtin-tools`/`--exclude-tools` per `builtin_tools`; (c) each generated
   `execute()` re-checks its own tool name against the request allowlist before
   shelling. Three independent gates = a corroborated guarantee (the becky
   "corroborate, then conclude" instinct applied to safety).
2. **No destructive capability unless explicitly enabled.** `builtin_tools` defaults to
   **`none`** — the agent has **no** `write`/`edit`/`bash` and can do nothing but call
   the read-only becky tools it was given. Pi's own file/shell tools are only present if
   the request opts into `read-only` or `full`, and `full` should be reserved for
   workflows that genuinely must write an output file (e.g. an exhibit). Source media is
   never modified regardless (becky tools work on copies; the harness adds no new write
   path).
3. **Dry-run mode.** `dry_run:true` (or `--dry-run`) builds the request, generates the
   manifest/extension, prints the plan + the exact `pi` invocation + the tool allowlist,
   and **runs nothing** (no model call, no tool exec). This is how Jordan (or
   `becky-ask`) previews a workflow safely.
4. **Hard limits.** `max_turns`, `max_budget_usd`, `timeout_sec` are enforced by the Go
   driver (it counts turns/events, watches the clock, and `abort`s the rpc session);
   hitting one stops cleanly with `stopped` set and a partial result — never a runaway.
5. **Full transcript = reproducibility-by-audit.** Because every tool call + result is
   logged, a finding can be re-checked deterministically by re-running the *same becky
   commands* the transcript records, outside the agent. The agent's *judgement* still
   obeys `FORENSIC-OUTPUT-PHILOSOPHY.md` via `system_append` (corroborate-then-conclude;
   recall for detection, name only when corroborated; plain words, no jargon) — but the
   *evidence* under it is the deterministic tools' own JSON, unaltered.
6. **Privacy / offline (forensic).** For sensitive case material, set `auth:"local"` +
   an OpenAI-compatible local endpoint (llama.cpp `llama-server`) and `PI_OFFLINE` so no
   case text leaves the machine — the same local-only posture `becky-ask`'s intent model
   and `SPEC-BECKY-NEW-TOOL.md` §7 already mandate for sensitive content. Subscription/
   hosted models are the convenient default for **non-sensitive** workflows only.

---

## 8. Build plan — cloud agent vs local agent (the baton)

Split along the model boundary, exactly per `CLAUDE.md` §4.

**Cloud / web agent (Go, deterministic, no Node/model needed):**
1. `cmd/harness/` — request parse + validation (schema, default-deny allowlist, limits,
   `--goal`/flag → request synthesis), JSON-out result, exit codes, transcript writer.
2. `internal/pirun/` — the `PiSpec`/`PiResult` types, `ResolveBin`, `GenerateExtension`
   (catalog → TypeScript extension with one `registerTool` per allowlisted tool),
   `Run` (drive `pi --mode rpc`/`-p` over JSONL, enforce limits, mirror events).
3. `internal/catalog/` — lift `becky-ask`'s `catalog.go` into a shared package both
   `cmd/ask` and `cmd/harness` import (single source of truth for tool descriptions);
   add per-tool param-maps for the first 5 workflow tools.
4. The **faked-Pi** unit suite (`PI_BIN` override): allowlist enforcement, extension
   generation, event→transcript, limit/timeout stops, result-contract validation,
   degrade paths (no `pi`, no auth). `go build/vet/test/gofmt` green on Ubuntu+Windows.
5. Wire `harness` into `build-all-tools.bat` TOOLS; add its row to `PROGRESS.md`; a
   one-line `SKILL.md` mention. Push to the `claude/*` branch; open a **draft PR**.

**Local agent (Jordan's Win10 PC — Node + real model):**
1. Install Pi (`npm i -g @earendil-works/pi-coding-agent` or `pi.dev/install.sh`); run
   `pi` → `/login` (Claude Pro/Max) once.
2. Confirm `go build/test ./...` green on Windows; run the `//go:build pi` live test.
3. **Re-verify Pi specifics against the installed version** (flags, the `--mode rpc`
   record field names, the `registerTool` shape, the `--provider/--base-url` form for a
   local llama.cpp endpoint) and pin them in `internal/pirun` only.
4. Run ONE real templated workflow on a real folder (e.g. the name-mismatch example);
   confirm the transcript, the JSON result, and the degrade paths (kill `pi`, drop a
   bad path) behave. Tune the first skill/prompt-template. Report what passed, what
   degraded, what needs Jordan. Commit to the same branch.

---

## 9. How it composes with `becky-ask` and `becky` (no new orchestrator)

- **`becky-ask` → `becky-harness`.** When `becky-ask`'s intent step recognizes a request
  as "run a known repetitive workflow" (vs a one-shot plan or a new-tool pitch), it can
  **shell `becky-harness --request <saved.json>`** the same way it already shells
  `becky.exe` and (per `SPEC-BECKY-ASK.md` §3.4) `becky-new-tool`. `becky-ask` stays the
  only conversational surface; `becky-harness` is just another deterministic engine it
  can drive, on opt-in, streaming the harness's stderr headlines into the chat.
- **`becky` (orchestrator) stays separate.** `becky` is for *fixed* ops a human/script
  types. `becky-harness` is for *autonomous* multi-step loops. A `becky` recipe that is
  getting too branchy/`if`-heavy to maintain is the signal to move it to a
  `becky-harness` request instead. They do not overlap: `becky` never loops a model;
  `becky-harness` never reimplements a `becky` verb (it *calls* the same sharp tools).
- **`becky-harness` vs `becky-new-tool`.** Both can drive an agent, but they are
  different jobs and different runtimes: `becky-new-tool` (via `internal/agentrun` →
  headless **Claude Code**) *builds a brand-new tool* through a deterministic staged
  pipeline with human gates. `becky-harness` (via `internal/pirun` → headless **Pi**)
  *runs existing tools* to do a repetitive workflow. Keeping two thin runtime wrappers
  (`agentrun`, `pirun`) rather than one mega-harness preserves the single-tool
  principle: when one breaks it's obvious which, and the other keeps working.

---

## 10. Honest open questions (need a human decision)

- **Q1 — Pi subprocess vs the pi SDK (embed).** This spec drives Pi as a **subprocess**
  (`pi --mode rpc`) from Go, which keeps the model boundary clean and the Go layer
  Node-free and unit-testable. The alternative is embedding `@earendil-works/pi-coding-
  agent`'s TS SDK (`createAgentSession`) in a small Node sidecar the Go tool spawns —
  more control, but a Node build/runtime dependency in the harness itself. **Rec:
  subprocess RPC.** Confirm. (If the SDK later proves necessary for tighter tool-result
  control, the sidecar is still behind `internal/pirun` — no caller change.)
- **Q2 — Default auth: subscription `/login` vs local model.** Subscription is the
  lowest-fuss default for **non-sensitive** workflows; **local (OpenAI-compatible
  llama.cpp) is mandatory for sensitive case material.** Confirm becky-harness should
  *default* to subscription but **refuse hosted models when a `--sensitive` flag is
  set** (forcing `auth:"local"`), mirroring `becky-ask`/`new-tool` privacy rules.
- **Q3 — Which workflows to template first.** The harness is generic; its value is the
  saved requests/skills. Candidates: (a) folder-wide transcribe+identify name-mismatch
  flag; (b) "find every clip a named person appears in and summarize what's said
  around it" (the `becky-ask` §3.3 example, but autonomous over a big corpus); (c) a
  corroboration sweep that runs identify + validate + osint on a clip and writes the
  combined confidence call. Pick the first 1–2 to harden the param-maps + skill against.
- **Q4 — How `becky-ask` decides "workflow" vs "plan."** Today `becky-ask` plans and
  runs `becky` commands directly. When should it instead hand off to `becky-harness`?
  Rec: when the job is *iterate-over-many + branch on results* (an agent loop earns its
  cost) rather than a fixed short chain. Needs a threshold Jordan is comfortable with.
- **Q5 — Pi version pinning + drift.** Pi is pre-1.0 and the org just moved
  (`badlogic/pi-mono` → `earendil-works/pi`); flags and the RPC schema can change. Rec:
  pin an exact `@earendil-works/pi-coding-agent` version in the local install, keep all
  version-sensitive code in `internal/pirun`, and re-verify on each Pi bump. Confirm the
  pinning policy.
- **Q6 — Cost surface.** Subscription Pi usage draws on Jordan's Claude/ChatGPT plan
  (separate from the metered Agent-SDK credit `becky-new-tool` uses, since that's
  `claude -p`, not Pi). **ASSUMPTION (local agent must verify):** confirm how Pi reports
  per-run token/cost so `result.cost_usd` is populated, and how subscription usage is
  metered/limited in practice, before relying on `max_budget_usd` for hosted models.

---

## 11. Sources (live, verified 2026-06-14)

- **Pi repo / monorepo overview, MIT license, the four packages** (pi-ai unified multi-
  provider LLM API; pi-agent-core runtime + tool calling + state; pi-tui differential
  rendering; pi-coding-agent interactive CLI):
  https://github.com/earendil-works/pi  and the legacy alias
  https://github.com/badlogic/pi-mono  (fetched 2026-06-14)
- **coding-agent README** — `pi` invocation `pi [options] [@files...] [messages...]`;
  install (`npm install -g --ignore-scripts @earendil-works/pi-coding-agent`,
  `curl -fsSL https://pi.dev/install.sh | sh`); **auth** (subscription `/login` for
  Claude Pro/Max, ChatGPT Plus/Pro, GitHub Copilot **or** `--api-key`/env, 25+
  providers); **headless flags** (`-p/--print`, `--mode json`, `--mode rpc`); **model**
  (`--provider`, `--model`, `:thinking` shorthand, `--list-models`); **tools**
  (`--tools`, `--exclude-tools`, `--no-builtin-tools`, `--no-tools`); **resources**
  (`-e/--extension`, `--skill`, `--prompt-template`, `--theme`); **system prompt**
  (`--system-prompt`, `--append-system-prompt`); **session** (`--session`, `--fork`,
  `--no-session`, `-c/--continue`, `-r/--resume`); env (`PI_OFFLINE`,
  `PI_CODING_AGENT_DIR`, `ANTHROPIC_API_KEY`):
  https://github.com/earendil-works/pi/blob/main/packages/coding-agent/README.md
  (fetched 2026-06-14)
- **RPC protocol** (`--mode rpc`) — JSONL framing rule (split on `\n` only, strip
  trailing `\r`, NOT Unicode separators); command records
  `{"id","type":"prompt"|"steer"|"abort"|"set_model"|...,"message"}`; response
  `{"id","type":"response","command","success"[,"error"]}`; events `agent_start`,
  `message_update`+`assistantMessageEvent` deltas, `tool_execution_start/update/end`,
  `agent_end`, `extension_ui_request/response`:
  https://github.com/earendil-works/pi/blob/main/packages/coding-agent/docs/rpc.md
  (fetched 2026-06-14)
- **Extensions / `registerTool`** — default-export `(pi: ExtensionAPI) => {...}`,
  `pi.registerTool({ name, label, description, promptSnippet?, parameters: Type.Object,
  prepareArguments?, async execute(toolCallId, params, signal, onUpdate, ctx) → {
  content[], details, terminate? }, renderCall?, renderResult? })`; Typebox params;
  throw to mark failure; load via `-e/--extension` or `~/.pi/agent/extensions/`:
  https://github.com/earendil-works/pi/blob/main/packages/coding-agent/docs/extensions.md
  (fetched 2026-06-14)
- **SDK** (the embed alternative, Q1) — `createAgentSession`, `AuthStorage`,
  `ModelRegistry`, `SessionManager`, `session.prompt(...)`:
  https://github.com/earendil-works/pi/blob/main/packages/coding-agent/docs/sdk.md
  (referenced 2026-06-14)
- **Org move / provenance** (badlogic → earendil-works April 2026, stays MIT; Zechner =
  libGDX creator; Earendil PBC co-founded by Armin Ronacher): launch/announcement
  write-ups, e.g. https://rywalker.com/research/pi  (2026)
- **In-repo grounding:** `internal/agentrun/agentrun.go` (the parallel headless-`claude`
  wrapper this spec's `internal/pirun` mirrors), `cmd/ask/catalog.go` +
  `SPEC-BECKY-ASK.md` (the tool catalog + the front-door it composes with),
  `SPEC-BECKY-NEW-TOOL.md` (the deterministic-shell-around-an-agent pattern, the
  privacy/local-model rules, the gate discipline), `FORENSIC-OUTPUT-PHILOSOPHY.md`
  (the output contract `system_append` injects), `CLAUDE.md` §2/§4 (invariants + the
  cloud↔local baton this build plan follows).

> Re-verify every Pi flag, the RPC record shapes, and the `registerTool` signature
> against the installed `@earendil-works/pi-coding-agent` version before writing code —
> Pi is pre-1.0 and the org just moved. Items marked **ASSUMPTION (local agent must
> verify)** are the ones most likely to have drifted.
```
