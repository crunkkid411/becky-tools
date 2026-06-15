# SPEC — `becky-omnigent` (binary `becky-omni`) — the governance / observation / composition layer ABOVE becky's agents

> ## STATUS — SPEC, NOT BUILT, AWAITING APPROVAL
> Author: cloud/web research agent, drafted **2026-06-15**. Omnigent
> (`github.com/omnigent-ai/omnigent`, docs `omnigent.ai`, Apache-2.0, **alpha**,
> open-sourced by Databricks on **2026-06-13** — Matei Zaharia / Kasey Uhlenhuth /
> Corey Zumar, built with Neon) was researched live this session; every external
> claim carries an inline source + date in §11. Omnigent is **pre-1.0 alpha** and the
> CLI/REST/sandbox-config shapes will move — items flagged **ASSUMPTION (local agent
> must verify)** are the ones most likely to drift, and the local agent must
> re-confirm them against the installed version before wiring code. Nothing here is
> built yet: the cloud agent ships the Go scaffolding (the YAML/policy generator, the
> run/status/collect wrapper, the transcript+result normalizer) + a faked-Omnigent
> unit suite; the local agent installs Omnigent (Python 3.12+), does the one real run,
> and pins the shapes. This SPEC obeys the same house rules as
> `SPEC-AGENT-HARNESS.md` / `SPEC-BECKY-NEW-TOOL.md` / `SPEC-BECKY-ASK.md`: JSON in /
> JSON out, exit-coded, degrade-never-crash, reuse `internal/`, never modify source
> inputs.

---

## 0. TL;DR — one line, then the place in the catalog

`becky-omnigent` is the tool that runs **becky's agent(s) under Omnigent governance** —
it takes a becky workflow request, emits an Omnigent **agent YAML + policy file**, and
launches a **sandboxed, policy-controlled, watchable/shareable** Omnigent session that
drives the declared becky tools to a goal, then normalizes the session's result +
transcript into one becky JSON document.

It does **ONE** thing (the single-tool principle): **run a declared becky agent under
Omnigent's governance + observation layer, and return a deterministic result.** It does
**not** reason, it does **not** reimplement a becky tool, and it does **not** replace
the agent loop — Omnigent (and the harness inside it) owns the loop; `becky-omni` owns
the deterministic boundary around it (the request → YAML/policy generation, the launch,
the transcript capture, the result normalization, the offline/safety enforcement
*expressed as Omnigent policy + sandbox config*).

**Where it sits — the governance tier above the harness tier:**

| Tool | Tier | What decides the next step | Who enforces the rules |
|---|---|---|---|
| `becky` (orchestrator) | command engine | a human/script types fixed ops | n/a (no model loop) |
| `becky-ask` | chat front-door | human approves each plan; model classifies once | Go control flow |
| `becky-harness` | **agent harness** | a Pi agent loops over a tool allowlist | becky's Go shell (`internal/pirun`) + Pi `--tools` |
| **`becky-omnigent`** (this spec) | **governance / observation / composition** | the agent (Pi/Claude/Codex) loops *inside Omnigent* | **Omnigent's meta-harness layer**: stateful policies + the Omnibox OS sandbox, enforced *outside* the prompt |

So the progression is: `becky` = you drive; `becky-ask` = you talk, it plans, you
approve; `becky-harness` = you declare a goal + toolbox + guardrails once and an agent
grinds through it; **`becky-omni` = you run that same declared agent, but now wrapped in
a sandbox + contextual policies you can WATCH and STEER from your phone, with the offline
invariant enforced by the OS network proxy rather than by trust.** It is the right tool
when a run is **long, unattended, or sensitive** and Jordan wants to (a) leave the
machine and still watch/steer it from his iPhone, and (b) have becky's invariants
(offline; no destructive writes; cost cap) enforced by a layer the model *cannot talk
its way past*, not by a prompt.

**It does NOT replace `becky-harness` — it consumes its output.** See §2 (the central
reconciliation): `becky-harness`'s job collapses to **producing a becky agent definition
(Omnigent YAML) + the becky tool manifest**, and `becky-omni` **runs that agent under
Omnigent**. They compose; they do not duplicate.

---

## 1. What Omnigent actually is (verified this session)

Omnigent is an Apache-2.0, alpha, **meta-harness** open-sourced by Databricks on
2026-06-13. It "sits above agents you already use (Claude Code, Codex, Pi, or custom
agents)" and wraps each in a uniform API — **"messages and files in, text streams and
tool calls out."** [src: Databricks blog; GitHub README, §11] It is **Python-first**
(~83% Python / ~16% TypeScript), installs via `curl … | sh` or `uv tool install
omnigent`, requires **Python 3.12+**, and has **no Databricks dependency** — it runs
locally or self-hosted (Docker Compose, Render, Fly.io, Railway, Hugging Face Spaces,
Modal). [§11]

**Two layers** (the architecture that matters for us):
- A **RUNNER** — wraps any agent in a **sandboxed, uniform session** (runs on your local
  machine, or on Modal / Daytona cloud sandboxes). This is the piece that actually
  executes the agent loop under the Omnibox sandbox.
- A **SERVER** — adds **policies + shared history** and **exposes every session** over
  the terminal, the web UI (`http://localhost:6767` by default), the macOS native app,
  mobile browser, and a **REST API** (OpenAPI). [§11]

**Three pillars** (Databricks' framing — these map 1:1 onto becky's needs in §3):
1. **COMPOSITION** — one uniform API over harnesses; swap/mix harnesses or models with a
   **one-line YAML change**; multi-agent teams (a supervisor delegating to sub-agents,
   each on a different harness). An agent is a **short YAML**: prompt + executor harness
   + tools + optional sub-agents. [§11]
2. **CONTROL** — **stateful, contextual policies** that track per-session state and
   enforce guardrails **at the meta-harness layer, not via prompts** (cost budgets,
   permission gates like "after an agent downloads an npm package, require human approval
   before `git push`", tool-call caps, PII blocking, model routing). Plus the **Omnibox
   OS sandbox**: a default-deny **filesystem + network** isolation that can **intercept
   and transform network requests** (an egress proxy) and **broker credentials** (the
   agent sees a placeholder token; the proxy swaps in the real one only on approved
   egress, so the real secret never enters logs/transcript/model context). [§11]
3. **COLLABORATION** — sessions are **shareable via URL**; teammates (or just Jordan, on
   another device) can **view, comment, command, steer, attach (co-drive), or fork** a
   live session from terminal, web, mobile, or the macOS app. [§11]

**The CLI surface (verified):** `omni`/`omnigent` are interchangeable. Subcommands:
`omni setup` (credential wizard), `omni run path/to/agent.yaml` (run a custom agent),
`omni claude` / `omni codex` / `omni debby` (built-in harness shortcuts), `omni server
start` (launch the local server at `:6767`), `omni host` (register a machine as an
execution host), `omni login <url>` (auth to a remote server), `omni attach
<session_id>` (co-drive an existing session), `omni run --fork <session_id>` (clone a
conversation), `omni stop`. [§11]

**Auth (verified, and load-bearing for Jordan):** Omnigent accepts **API keys**
(Anthropic, OpenAI), **subscriptions** (Claude Pro/Max, ChatGPT Plus/Pro — the same
low-fuss path `becky-harness`/Pi already lean on), **OpenAI/Anthropic-compatible gateways**
(OpenRouter, LiteLLM, **Ollama**, **vLLM**, Azure), and Databricks workspaces. Local
runs need **no server auth** by default; a shared server enables multi-user accounts via
`OMNIGENT_AUTH_ENABLED=1` + OIDC. [§11]

**Why Omnigent is the right governance tier for becky (not a hand-rolled wrapper):**
1. **It enforces becky's invariants *outside the prompt*.** becky-harness can *ask* a Pi
   agent to stay offline and not write files; Omnigent's **Omnibox** makes "offline" and
   "no writes outside this dir" **hard OS boundaries** the model cannot override. That is
   a categorical upgrade for forensic, sensitive material (§3.1).
2. **It already wraps Pi** (and Claude Code, Codex) under one API — so it sits *exactly*
   where becky-harness wants a governance layer, with no new integration per harness.
3. **Watch/steer from the iPhone is a first-class feature**, not something becky has to
   build. The product brief (Jordan is often away from the PC, on his phone, and loves
   watching/steering agents) is *literally Omnigent's COLLABORATION pillar* — share URL +
   mobile + attach to co-drive. (`SPEC-BECKY-ASK.md` §6 explicitly punts true mobile/GUI
   steering to "a separate, later track" — Omnigent *is* that track, for free.) [§11]
4. **Subscription auth = minimal fuss**, same posture as becky-harness (§1 there): `omni
   setup` detects the existing Claude Pro/ChatGPT login; no API key to mint or leak.
5. **Offline-capable model path exists.** Ollama / vLLM / any OpenAI-compatible
   `base_url` backs the agent for fully-offline forensic runs — the same local-model
   escape hatch becky-ask and becky-harness already mandate for sensitive content.

---

## 2. THE RECONCILIATION WITH `becky-harness` (the central design decision)

This is the #1 thing to get right, because **Omnigent already wraps Pi, and
`becky-harness` (`SPEC-AGENT-HARNESS.md`) exists to drive a Pi agent.** Left unreconciled
they overlap and we violate the single-tool principle (two things that "run an agent over
becky tools").

### 2.1 The clean framing (recommended): becky-harness becomes a generator; becky-omni becomes the runtime

The two tools split along the **definition vs runtime** seam, not the harness boundary:

```
  becky workflow request  (goal + tool allowlist + model + skill + limits + sensitivity)
            │
            ▼
   becky-harness  ──►  EMITS two artifacts (deterministic, no model, no Node, no Python):
       (generator)        (1) a becky AGENT DEFINITION  = an Omnigent agent YAML
                          (2) a becky TOOL MANIFEST     = the allowlisted becky-* tools,
                                                          each as an Omnigent `tool`
            │
            ▼
   becky-omni     ──►  CONSUMES those two artifacts and:
       (runtime)          • generates the Omnigent POLICY file from the request's safety knobs
                          • generates the Omnibox SANDBOX config (offline + write-gating)
                          • LAUNCHES `omni run <agent.yaml>` under that policy+sandbox
                          • prints the SHARE URL (watch/steer from the iPhone)
                          • streams the transcript; on completion NORMALIZES the result
                            into ONE becky JSON doc (same shape family as harness/result@1)
```

So `becky-harness`'s existing job — "build the per-run agent + the tool manifest from a
declared request" — is **exactly the generator half**. Today `SPEC-AGENT-HARNESS.md` §4
generates a **Pi TypeScript extension** (`registerTool(...)`) and §3 launches `pi --mode
rpc` itself. Under this framing, the *generation* stays (now emitting **Omnigent YAML**
instead of, or in addition to, a Pi extension) and the *launching/governing* moves to
`becky-omni`. The Pi runtime is then reached **through Omnigent's `executor.harness: pi`**
rather than driven directly — so the harness logic isn't duplicated, it's **promoted** to
run under governance.

### 2.2 The two options Jordan must choose between (Open Decision #1)

- **Option A (recommended) — Omnigent becomes THE runtime; `becky-harness` becomes a
  YAML/manifest generator.** `becky-harness` stops launching Pi directly; its
  `internal/pirun` "drive a headless Pi" code is retired in favour of **emitting an
  Omnigent agent YAML** (`executor.harness: pi`) + the tool manifest, which `becky-omni`
  runs. One runtime, one governance layer, one place transcripts/policies live. The tool
  manifest (`SPEC-AGENT-HARNESS.md` §4, derived from the shared `internal/catalog`) is
  **reused verbatim** — it just renders to Omnigent `tools:` entries instead of Pi
  `registerTool` calls. *Pro:* no duplication; every run is governed/watchable; the
  iPhone-steering brief is satisfied. *Con:* every run now needs Omnigent (Python 3.12+)
  installed; loses the "Node-only, no extra layer" quick path; depends on an alpha
  project for the core runtime.

- **Option B — keep BOTH, split by use-case.** `becky-harness` stays a **standalone,
  offline, local quick-run** path (drives Pi directly via `internal/pirun`, no Omnigent
  needed — good when Omnigent isn't installed, or for a fast headless batch on the PC).
  `becky-omni` is the **governed / watchable / shareable / sandbox-enforced** path for
  long, unattended, or sensitive runs. They share the **same generator core**
  (`internal/catalog` + a new `internal/agentdef` that emits *both* a Pi extension and an
  Omnigent YAML from one request), so the request schema and tool manifest are identical
  and a run can be moved between them with no rewrite. *Pro:* degrades gracefully if
  Omnigent is absent; keeps a pure-local floor (mirrors how `becky-palantir` keeps a
  `cooccur-only` Go floor under an external project). *Con:* two run paths to maintain.

**Recommendation: start with Option B, design toward Option A.** Build `becky-omni` as
the governed runtime and refactor `becky-harness` into a generator that *also* keeps its
direct-Pi quick path; once Omnigent proves stable past alpha and Jordan confirms the
iPhone-steering workflow is what he wants by default, collapse to Option A (retire
`internal/pirun`'s launch half, keep the generator). This preserves a deterministic local
floor while making governed/watchable the headline path — and it never leaves the suite
with two tools that *secretly do the same thing*: the seam (generate vs govern-and-run)
is explicit and load-bearing.

### 2.3 The contract between them (what crosses the seam)

`becky-harness --emit-omnigent <request.json>` writes, into the run dir:
- `agent.yaml` — the Omnigent agent definition (§5.2): `name`, `prompt` (goal +
  `system_append` from `FORENSIC-OUTPUT-PHILOSOPHY.md`), `executor.harness`, and `tools:`
  (one entry per allowlisted becky tool, each a `type: function` callable that shells the
  real `becky-*.exe`, or a thin Python tool-shim that does).
- `manifest.json` — the machine-readable list of allowlisted tools + their `fixed_flags`
  (the same allowlist `SPEC-AGENT-HARNESS.md` §2.1 defines), so `becky-omni` can build the
  policy/sandbox config without re-parsing the YAML.

`becky-omni run --agent <agent.yaml> --manifest <manifest.json> --request <request.json>`
consumes exactly those three and produces the governed session + the normalized result.
**Nothing else crosses the seam** — the request schema (`SPEC-AGENT-HARNESS.md` §2.1) is
the single shared contract; `becky-omni` adds only governance knobs on top of it (§5.1).

---

## 3. Mapping becky's invariants onto Omnigent's primitives

The whole point of running becky agents *through* Omnigent is that becky's hard-won
invariants stop being prompt-level requests and become **enforced primitives**.

### 3.1 Offline + deterministic → the Omnibox network proxy

becky's load-bearing invariant is "**No network at runtime; the only AI in the loop is an
explicit local model call.**" Omnibox's network layer is a **default-deny egress proxy
with an explicit allow-list of methods/hosts/paths**; private IPs and cloud-metadata
endpoints are blocked by default. [§11] `becky-omni` therefore:

- **Default sandbox = fully offline.** The generated Omnibox config grants **no egress
  hosts** — the agent and every becky tool it calls run with the network hard-closed at
  the OS level. A becky tool that tried to reach the network would simply fail (and
  degrade), exactly as the offline invariant demands — now *guaranteed*, not trusted.
- **An explicit, logged research step is the ONLY exception.** When a request opts into a
  networked step (the same "opt out of offline via an explicit, logged network step"
  carve-out `CLAUDE.md` §6 grants `becky-research`/`becky-palantir`/`becky-harness`),
  `becky-omni` adds **only the specific allow-listed hosts** that step needs to the egress
  allow-list, and the proxy **logs every approved request** into the transcript. The
  network opening is thus *narrow, named, and auditable* — the becky standard for the new
  online tool class.
- The model that backs the agent is chosen by the request: `auth: local` →
  Ollama/vLLM/OpenAI-compatible `base_url` so **no case text leaves the machine**;
  hosted/subscription is allowed only for explicitly non-sensitive runs (§5.1, mirrors
  `becky-ask`/`becky-harness`/`new-tool` privacy rules).
- **Determinism around a non-deterministic core:** as in `SPEC-AGENT-HARNESS.md` §7, the
  agent loop is not deterministic, and `becky-omni` does not pretend it is. It guarantees
  a deterministic **shell**: the becky tools' own JSON output is unaltered, the **full
  transcript** is always captured (Omnigent's shared session history + the per-tool
  `becky-*` JSON), and any finding can be re-checked by re-running the *same becky
  commands* the transcript records.

### 3.2 Degrade-never-crash + default-deny → policies + sandbox grants

becky's "**Degrade, never crash**" and the harness "**default-deny tool allowlist**" map
onto two Omnigent primitives at once:

- **Tool allowlist (default-deny):** the agent can call **only** the becky tools the
  request's manifest listed — they are the *only* `tools:` in the generated `agent.yaml`,
  so an unlisted becky tool simply does not exist to the agent (same guarantee as
  `SPEC-AGENT-HARNESS.md` §7.1, now enforced by the YAML the runtime loads).
- **Destructive/file-writing tools are gated by policy + sandbox, two ways:**
  (a) the **Omnibox filesystem grant** is read-only on the case dir by default (Omnibox
  makes the CWD read-only and masks dotfiles/secrets unless explicitly granted [§11]), so
  a write *outside an explicitly-granted output dir* is impossible at the OS level; and
  (b) a **policy** (`omnigent.policies.builtins.safety.ask_on_os_tools` or a becky-written
  handler) makes any shell/write/destructive call **ASK** for approval — which, on the
  iPhone, is a tap. The "corroborate, then conclude" instinct applied to safety: the
  *sandbox* is the hard floor, the *policy* is the gate, two independent layers.
- **Cost cap = a policy, not a prayer.** `omnigent.policies.builtins.cost.cost_budget`
  with `max_cost_usd` + `ask_thresholds_usd` enforces the request's budget at the
  meta-harness layer (pause + ask at a threshold, hard-stop at the cap) — replacing the
  Go-side budget counting `SPEC-AGENT-HARNESS.md` §7.4 had to do by hand.
- **Degrade, not crash, at the boundary:** if `omni` isn't installed, no model is
  reachable, or `omni setup` hasn't been run, `becky-omni` emits a JSON result with
  `degraded:true` + a one-line plain-English fix and exits `3` — never a panic, never
  half-JSON (§6). Same posture as `agentrun.ResolveBin()`/`pirun.ResolveBin()` returning
  `""` so callers degrade.

### 3.3 Watch/steer from the iPhone → the COLLABORATION pillar

This is the becky-specific *payoff*. Omnigent's collaboration layer is exactly what the
brief asks for and what `becky-ask` §6 deferred:

- `becky-omni run` launches the session **and prints the share URL** to stderr (a
  plain-English headline) and into the JSON result. Jordan opens it on his iPhone browser
  (or the macOS app), **watches the agent work in real time**, comments on files, and
  **steers / co-drives** (Omnigent `attach`) — pausing, answering an ASK gate with a tap,
  redirecting the goal — without being at the PC.
- For long unattended forensic sweeps (the harness's bread and butter — "for every clip
  in this folder, transcribe/identify/flag mismatches"), this means Jordan can kick it
  off, leave, and **supervise from his phone**, approving the occasional policy ASK
  (e.g. "write the exhibit file?") remotely. That is a genuinely new capability for becky,
  delivered by integration rather than by building a GUI.
- **Privacy caveat (load-bearing, see Open Decision #4):** a shareable session URL is a
  *network exposure of forensic material*. **Default = local/private only** — the share
  URL points at `http://localhost:6767` (or Jordan's own self-hosted server on his LAN),
  reachable from his phone on the same network, **not** a public Omnigent-hosted URL. A
  publicly-reachable share is **opt-in, logged, and forbidden for sensitive case
  material**, identical in spirit to the offline/local-only rule for the model.

---

## 4. The Go-orchestrator ↔ Omnigent-runtime boundary

```
  HUMAN / becky-ask / becky-harness(--emit-omnigent)
            │   request.json (+ agent.yaml + manifest.json)
            ▼
  becky-omni.exe   (Go orchestrator — THIS tool; deterministic, owns the boundary)
   ┌─────────────────────────────────────────────────────────────────────────────┐
   │ 1. PARSE + VALIDATE the request (schema, allowlist, limits, sensitivity)      │
   │ 2. GENERATE the governance config:                                            │
   │      • policy.yaml   (cost cap, ask-on-os-tools, write-gate, host allow-list) │
   │      • omnibox.* config (filesystem grants: case dir RO + output dir RW;      │
   │        network: default-deny, or the named research hosts only)               │
   │    (the agent.yaml + tool manifest come FROM becky-harness — §2.3 — not here) │
   │ 3. RESOLVE the omni binary; if absent/unauthed -> DEGRADE (exit 3)            │
   │ 4. LAUNCH: `omni run <agent.yaml> --policy <policy.yaml> --sandbox <cfg> ...` │
   │      capture the SHARE URL; stream session events to the TRANSCRIPT log       │
   │ 5. COLLECT: poll/stream to completion (or limit stop); NORMALIZE the session  │
   │      result + transcript into ONE becky JSON doc (becky-omnigent/result@1)    │
   └─────────────────────────────────────────────────────────────────────────────┘
            │
            ▼
  Omnigent RUNNER + SERVER  (Python; the meta-harness — NOT becky's code)
   • RUNNER wraps the chosen harness (pi / claude-sdk / codex) in an Omnibox sandbox
   • enforces POLICIES (cost, ask-gates) + the SANDBOX (offline FS/net) at its layer
   • the agent loops: reason -> call a becky tool (shells becky-*.exe) -> read JSON -> repeat
   • SERVER exposes the session over web/mobile/macOS/REST -> the SHARE URL
```

**Boundary rule (load-bearing):** `becky-omni` (Go) is **deterministic and owns every
becky guarantee that can be expressed as config** — the policy file, the sandbox config,
the request validation, the transcript capture, the result normalization. **Everything
non-deterministic (the loop) and everything Python (Omnigent itself) lives behind the
`omni` CLI / REST boundary.** `becky-omni` never embeds Python; it **shells out** to the
`omni` binary (or talks to a running `omni server` over REST). This keeps the Go layer
Node-free *and* Python-free and fully unit-testable against a **faked `omni`** (§5).

### 4.1 Invocation mode — CLI subprocess (default), REST (when a server is up)

- **CLI subprocess (default).** `becky-omni` shells `omni run <agent.yaml> …` via Go
  `exec.CommandContext`, resolving the binary with `LookPath("omni","omnigent")`. This is
  the lowest-fuss path: no server to stand up for a one-shot governed run. **ASSUMPTION
  (local agent must verify):** the exact flags for passing a policy file + sandbox config
  + a non-interactive/headless output stream to `omni run` — the README documents the
  subcommands but not every flag; the local agent confirms `--policy`/`--sandbox`/output
  flags (or their config-file equivalents) against the installed version and pins them in
  `internal/omnirun` (§5).
- **REST (when `omni server start` is running).** For watch/steer-from-phone runs the
  server must be up anyway (it serves the web/mobile UI at `:6767`), so `becky-omni` can
  instead **create the session via the REST API**, get the **share URL**, stream events,
  and read the final result. **ASSUMPTION (local agent must verify):** the REST endpoints
  for create-session / send-message / stream-events / get-result and the OpenAPI shape
  (`openapi.json`) — these are not fully documented in the alpha README; the local agent
  reads the live `openapi.json` off the running server and pins the client in
  `internal/omnirun`. The Go code treats CLI vs REST as two backends behind one interface
  so the choice is a flag, not a rewrite.

---

## 5. The integration stub contract (so the local agent only installs + wires Omnigent)

Everything Omnigent-touching is isolated behind ONE small package, **`internal/omnirun`**
(deliberately parallel to `internal/pirun` and `internal/agentrun`). The cloud agent
ships it with a **faked `omni`** so the whole Go layer is unit-tested with no
Python/model; the local agent installs Omnigent and flips it to the real binary/REST.

### 5.1 The governance knobs `becky-omni` adds on top of the harness request

`becky-omni` consumes the **same request schema** as `becky-harness`
(`SPEC-AGENT-HARNESS.md` §2.1 — `goal`, `target`, `tools[]` allowlist, `model`, `auth`,
`skill`, `system_append`, `limits`, `result_contract`) and adds a `governance` block:

```jsonc
{
  "schema": "becky-omnigent/request@1",
  "extends": "becky-harness/request@1",      // all harness request fields apply
  "governance": {
    "sandbox": "offline",                     // offline (default) | research | full  -> Omnibox net policy
    "allow_hosts": [],                        // research-only: the named, logged egress hosts
    "fs_writes": "deny",                      // deny (default) | output-dir-only | full  -> Omnibox FS grants
    "output_dir": "X:\\cases\\smith\\out",    // the ONE writable dir when fs_writes=output-dir-only
    "ask_on": ["os_tools", "git_push"],       // actions that PAUSE for approval (a tap on the phone)
    "max_cost_usd": 2.00,                     // -> cost_budget policy
    "ask_thresholds_usd": [1.00],             // -> cost_budget ask points
    "share": "private",                       // private (default, localhost/LAN) | public (opt-in, logged, FORBIDDEN if sensitive)
    "sensitive": true                         // true -> forces auth:local, sandbox>=offline, share:private (overrides convenience)
  }
}
```

`sensitive:true` is the master forensic switch: it **forces** `auth:"local"` (no hosted
model sees case text), `sandbox:"offline"` (no egress), and `share:"private"` (no public
URL) — and `becky-omni` refuses (exit 2) any request that sets `sensitive:true` alongside
a hosted model or a public share, the same "refuse hosted models when `--sensitive`"
discipline `SPEC-AGENT-HARNESS.md` Q2 mandates.

### 5.2 The generated Omnigent agent YAML (what `becky-harness` emits, §2.3)

```yaml
# GENERATED by becky-harness --emit-omnigent. One `tools:` entry per ALLOWLISTED becky tool.
name: becky_name_mismatch_sweep
prompt: |
  For every .mp4 in the target folder, transcribe it, identify the speakers, and report
  any clip where a name SAID in the transcript is not among the identified speakers.
  Recall is for DETECTION; attach a NAME only when corroborated. Plain words, never jargon.
  Output a JSON array of { clip, said_names[], identified_names[], mismatch[] }.
executor:
  harness: pi            # or claude-sdk / codex / claude-native — swap with ONE line (composition)
tools:
  becky_transcribe:
    type: function
    callable: becky_tools.shims.becky_transcribe   # thin Python shim shells the REAL becky-transcribe.exe
  becky_identify:
    type: function
    callable: becky_tools.shims.becky_identify      # fixed_flags (--kb kb-final) baked into the shim
```

The `callable` shims are a tiny, generated `becky_tools.shims` Python module (one function
per allowlisted tool) that does nothing but `subprocess.run(["becky-<tool>.exe", ...])` and
returns the tool's JSON **verbatim** — no interpretation, exactly as
`SPEC-AGENT-HARNESS.md` §4.3 requires the agent to read what the tool said. (The shim is
the Omnigent equivalent of the Pi `registerTool` `execute()` that shelled the binary; it
is generated, never hand-maintained — it derives from the shared `internal/catalog`.)

### 5.3 The generated policy file (what `becky-omni` emits)

```yaml
# GENERATED by becky-omni from request.governance. Enforced at the meta-harness layer.
policies:
  budget:
    type: function
    handler: omnigent.policies.builtins.cost.cost_budget
    factory_params:
      max_cost_usd: 2.00
      ask_thresholds_usd: [1.00]
  approve_os:
    type: function
    handler: omnigent.policies.builtins.safety.ask_on_os_tools   # write/shell/destructive -> ASK (tap to approve)
  cap_calls:
    type: function
    handler: omnigent.policies.builtins.safety.max_tool_calls_per_session
    factory_params:
      limit: 200
```

Plus the **Omnibox sandbox config** (`sandbox:"offline"` → empty egress allow-list +
case dir read-only; `fs_writes:"output-dir-only"` → add the one `output_dir` as the sole
RW grant; `sandbox:"research"` → add `allow_hosts` to the egress allow-list, logged).
**ASSUMPTION (local agent must verify):** the exact Omnibox config file format/keys for
filesystem grants and the egress allow-list — the docs describe the *behavior*
(default-deny proxy; CWD read-only; dotfile masking; credential brokering) but the alpha
docs I fetched did not show the literal config keys; the local agent confirms the schema
(`omnibox/configuration` reference) and pins the generator's template.

### 5.4 The stub interface (`internal/omnirun`)

```go
// internal/omnirun — the ONE place that knows how to drive a governed Omnigent session.
// Mirrors internal/pirun + internal/agentrun so callers and reviewers recognize the shape.

type Governance struct {
    Sandbox        string   // "offline" | "research" | "full"
    AllowHosts     []string // research-only egress allow-list (logged)
    FSWrites       string   // "deny" | "output-dir-only" | "full"
    OutputDir      string
    AskOn          []string // "os_tools","git_push",...
    MaxCostUSD     float64
    AskThresholds  []float64
    Share          string   // "private" | "public"
    Sensitive      bool
}

type OmniSpec struct {
    AgentYAMLPath string      // FROM becky-harness (§2.3)
    ManifestPath  string      // FROM becky-harness (§2.3)
    Goal          string
    Target        string
    Provider      string      // anthropic | openai | openai-compatible | databricks | ...
    Model         string
    BaseURL       string      // for Ollama/vLLM/OpenAI-compatible (offline)
    Auth          string      // "subscription" | "api-key" | "local"
    Gov           Governance
    Backend       string      // "cli" | "rest"
    ServerURL     string      // for rest backend (default http://localhost:6767)
    RunDir        string      // generated policy/sandbox + transcript live here
}

type OmniResult struct {
    FinalText      string
    Structured     json.RawMessage // validated vs result_contract when JSON
    ToolCalls      []ToolCallRecord
    ShareURL       string          // the watch/steer URL (printed for the iPhone)
    SessionID      string
    CostUSD        float64
    Stopped        string          // "" | "max_turns" | "budget" | "timeout" | "policy_deny" | "error"
    Events         []json.RawMessage
    Degraded       bool
    DegradeReason  string
}

func ResolveBin() string                                  // "omni"/"omnigent" or "" -> degrade
func GeneratePolicy(g Governance) (policyPath string, sandboxPath string, err error)
func Run(ctx context.Context, spec OmniSpec, onEvent func(json.RawMessage)) (OmniResult, error)
// nil error means the session RAN and produced a parseable result; check Stopped/Degraded
// for success (same discipline as pirun.Run / agentrun.Run).
```

**The faked Omnigent (cloud-agent deliverable).** An `omnirun_test.go` builds a tiny fake
`omni` (a Go test binary, resolved via `OMNI_BIN` env override) that: accepts `run
<agent.yaml> --policy … --sandbox …`, prints a canned share URL, emits a small stream of
canned events (one `tool_execution` for a becky tool, then a final result with a known
JSON answer), and exits 0. This lets the cloud agent unit-test request parsing, the
governance→policy/sandbox generation, share-URL capture, event→transcript mirroring,
limit/policy-stop handling, result-contract validation, and every degrade path (no `omni`,
no auth, sensitive+hosted conflict) — **all without Python or a model.** The live test
(`//go:build omnigent`) runs only on Jordan's machine.

**The local agent's whole job at the boundary:** (1) install Omnigent
(`curl -fsSL https://omnigent.ai/install.sh | sh` or `uv tool install omnigent`,
Python 3.12+); (2) `omni setup` once (subscription detected, or local Ollama/vLLM
configured); (3) confirm `ResolveBin()` finds `omni`; (4) **pin the four ASSUMPTION items**
— the `omni run` policy/sandbox/output flags, the Omnibox config keys, the REST
endpoints/`openapi.json`, and the event/result record shapes — in `internal/omnirun` only;
(5) run the `//go:build omnigent` live test + one real governed workflow on a real folder,
watch/steer it from the iPhone, confirm the offline-by-default sandbox actually blocks
egress and the write-gate actually pauses. The contract above is everything they need; no
Go logic should require changing.

---

## 6. JSON in / JSON out, exit codes, transcript

Output discipline is the becky norm + `FORENSIC-OUTPUT-PHILOSOPHY.md`:

- **stdout** = exactly ONE JSON document — the normalized run result:
  ```jsonc
  {
    "schema": "becky-omnigent/result@1",
    "goal": "...", "target": "...",
    "harness": "pi", "model": { "provider": "openai-compatible", "id": "local" },
    "tools_offered": ["becky-transcribe", "becky-identify"],
    "governance": { "sandbox": "offline", "fs_writes": "output-dir-only", "share": "private" },
    "share_url": "http://localhost:6767/s/abc123",   // the watch/steer URL
    "answer": { /* validated vs request.result_contract, or null */ },
    "degraded": false, "degrade_reason": "",
    "stopped": "",                          // "" | max_turns | budget | timeout | policy_deny | error
    "tool_calls": [ { "tool": "becky-identify", "args": {...}, "exit": 0, "degraded": false } ],
    "turns": 23, "cost_usd": 0.41,
    "transcript_path": "X:\\...\\.omni\\<slug>-<date>\\transcript.jsonl"
  }
  ```
- **stderr** = plain-English headlines/progress, never JSON — and **the share URL on its
  own line** so Jordan can copy it to his phone ("Omnigent: watch/steer at
  http://localhost:6767/s/abc123 · 12/40 clips done, 2 mismatches so far"). The chat-facing
  caller (`becky-ask`) renders these.
- **A full transcript** of the session is always written to the run dir as JSONL (every
  Omnigent event, every becky tool call + its verbatim JSON, every policy ASK/ALLOW/DENY
  decision). This is the determinism/audit anchor for a non-deterministic, governed
  runtime: you can see exactly what the agent did, which policies fired, and reproduce the
  tool calls by hand.
- **Exit codes:** `0` = the session completed (even if it stopped at a limit/policy with a
  partial answer — `stopped` says which); `2` = a request/usage error caught *before*
  launch (bad schema, unknown tool, `sensitive:true` + hosted model, `sensitive:true` +
  public share); `3` = `omni`/model unavailable and the run could not start (degrade).
  Never a panic.
- **Degrade, never crash:** if `omni` isn't installed → `degrade_reason:"omnigent CLI not
  found — run: curl -fsSL https://omnigent.ai/install.sh | sh"`; if not set up →
  `"omnigent not configured — run: omni setup"`; if the offline sandbox can't be
  established → refuse rather than run un-sandboxed on sensitive material. A plain stderr
  line tells Jordan the one command to fix it; exit `3`. Never half-JSON.

---

## 7. Determinism & safety — what becky guarantees around a governed, non-deterministic runtime

`becky-omni` does not make the agent loop deterministic; it makes the **shell** around it
deterministic *and* moves the safety floor from "prompt-level request" to "OS/meta-harness
enforcement." The guarantees, in order:

1. **Offline by default, enforced by the OS.** The default Omnibox config is a **closed
   network** (default-deny egress proxy). The becky offline invariant is now a hard
   boundary, not a hope. A networked step is a *named, host-allow-listed, logged*
   exception only.
2. **No destructive capability unless explicitly enabled, enforced two ways.** Omnibox
   makes the case dir **read-only** (and masks secrets/dotfiles); a write needs an explicit
   `output_dir` grant **and** passes the `ask_on:["os_tools"]` policy (a tap to approve).
   Source media is never modified (becky tools work on copies; `becky-omni` adds no write
   path). Default-deny tool allowlist: only the manifest's becky tools exist to the agent.
3. **Credentials never enter the model context.** If a networked step needs a token,
   Omnibox **brokers** it — the agent sees a placeholder; the proxy injects the real secret
   only on approved egress; the real token never lands in logs/transcript/model context.
   For forensic chains this is a meaningful upgrade over a raw harness.
4. **Hard cost + call caps as policy.** `cost_budget` (+ ask thresholds) and
   `max_tool_calls_per_session` stop a runaway at the meta-harness layer; hitting one stops
   cleanly with `stopped` set and a partial result.
5. **Full transcript + policy decisions = reproducibility-by-audit.** Every tool call,
   result, and ASK/ALLOW/DENY is logged; a finding is re-checkable by re-running the same
   becky commands the transcript records, outside the agent. The agent's *judgement* obeys
   `FORENSIC-OUTPUT-PHILOSOPHY.md` via the `prompt`/`system_append`; the *evidence* under it
   is the deterministic tools' own JSON, unaltered.
6. **Watch/steer = a human in the loop, remotely.** The share URL + attach mean Jordan can
   supervise and approve gates from his iPhone — the human-review requirement of the
   philosophy doc satisfied even when he's away from the PC. **Private/LAN by default**;
   public share is opt-in, logged, and forbidden for sensitive material (§3.3, Open
   Decision #4).
7. **Dry-run.** `--dry-run` builds the request, the agent.yaml (via the harness emit), the
   policy + sandbox config, prints the exact `omni run` invocation + the egress allow-list +
   the FS grants, and **runs nothing** — how Jordan (or `becky-ask`) previews a governed
   workflow safely before any model call.

---

## 8. Build plan — cloud agent vs local agent (the baton)

Split along the model/runtime boundary, exactly per `CLAUDE.md` §4.

**Cloud / web agent (Go, deterministic, no Python/Node/model needed):**
1. `cmd/omni/` — request parse + validation (the `becky-omnigent/request@1` schema, the
   `governance` block, `sensitive` conflict checks, `--goal`/flag → request synthesis),
   JSON-out result, exit codes, transcript writer, `--dry-run`.
2. `internal/omnirun/` — the `OmniSpec`/`OmniResult`/`Governance` types, `ResolveBin`,
   `GeneratePolicy` (governance → Omnigent policy YAML + Omnibox sandbox config),
   `Run` (CLI-subprocess backend driving `omni run`; a REST backend stub for the
   server path; capture share URL; mirror events; enforce/observe limits + policy stops).
3. `internal/agentdef/` (the generator half of §2) — reuse the shared `internal/catalog`
   (lifted from `becky-ask`/`becky-harness`) to **emit the Omnigent agent YAML + the
   `becky_tools.shims` Python module + manifest.json** from a harness request. Add the
   `becky-harness --emit-omnigent` flag that writes these artifacts (Option B: this lives
   beside the existing Pi-extension generator; Option A: it replaces it).
4. The **faked-Omnigent** unit suite (`OMNI_BIN` override): governance→policy/sandbox
   generation, agent-YAML/shim/manifest generation, share-URL capture, event→transcript,
   limit/policy-stop handling, result-contract validation, every degrade path (no `omni`,
   no setup, sensitive+hosted, sensitive+public). `go build/vet/test/gofmt` green on
   Ubuntu+Windows. Use `internal/pathx` for any `C:\…` path handling (CLAUDE.md §2).
5. Wire `omni` into `build-all-tools.bat` TOOLS; add its row to `PROGRESS.md`; a one-line
   `SKILL.md` mention; link this spec in `CLAUDE.md` §5 Doc map. Push to the `claude/*`
   branch; open a **draft PR**.

**Local agent (Jordan's Win10 PC — Python + real model):**
1. Install Omnigent (`curl -fsSL https://omnigent.ai/install.sh | sh` or `uv tool install
   omnigent`; Python 3.12+); `omni setup` once (subscription auto-detected, or wire a local
   Ollama/vLLM OpenAI-compatible endpoint for sensitive runs).
2. Confirm `go build/test ./...` green on Windows; run the `//go:build omnigent` live test.
3. **Pin the four ASSUMPTION items** against the installed alpha (the `omni run`
   policy/sandbox/output flags; the Omnibox config keys for FS grants + egress allow-list;
   the REST endpoints + `openapi.json` if using the server backend; the event/result
   record shapes) — all inside `internal/omnirun`/`internal/agentdef`, one place each.
   **Critical caveat:** Omnibox's hard OS sandbox is **bubblewrap/seccomp (Linux) /
   Seatbelt (macOS)** — confirm the **Windows** sandbox story (likely WSL2 or a cloud/Modal
   host, or a documented no-OS-sandbox fallback that must DOWNGRADE to a clear warning, not
   silently run un-sandboxed). This is the single biggest local-verification risk and may
   gate Option A.
4. Run ONE real governed workflow on a real folder (the name-mismatch sweep), **watch and
   steer it from the iPhone**, and verify the floor: offline sandbox actually blocks egress
   (try a tool that reaches the net → it fails+degrades); the write-gate actually pauses for
   a tap; the cost cap stops a runaway; the transcript + share URL + JSON result are right.
   Report what passed, what degraded, what needs Jordan. Commit to the same branch.

---

## 9. How it composes with the rest of the suite (no new orchestrator)

- **`becky-harness` → `becky-omni`.** The harness becomes the **generator** of the agent
  definition + manifest `becky-omni` runs (§2). Per Option B they stay two binaries sharing
  a generator core; per Option A the harness's launch half retires into `becky-omni`.
- **`becky-ask` → `becky-omni`.** Exactly as `becky-ask` can shell `becky-harness`
  (`SPEC-BECKY-ASK.md` §3.3, `SPEC-AGENT-HARNESS.md` §9), it can shell `becky-omni` for
  "run this repetitive workflow, governed, and give me a link to watch it on my phone."
  `becky-ask` stays the only conversational surface; it just streams `becky-omni`'s stderr
  headlines (including the share URL) into the chat.
- **`becky` (orchestrator) stays separate.** `becky` = fixed ops a human/script types;
  `becky-omni` = governed autonomous loops. They do not overlap and `becky-omni` never
  reimplements a `becky` verb — it *calls* the same sharp `becky-*.exe` through the agent.
- **`becky-omni` vs `becky-new-tool`.** Different jobs, different runtimes:
  `becky-new-tool` (via `internal/agentrun` → headless **Claude Code**) *builds a new tool*;
  `becky-omni` (via `internal/omnirun` → **Omnigent**) *runs existing tools under
  governance*. Three thin runtime wrappers (`agentrun`, `pirun`, `omnirun`) rather than one
  mega-harness keeps the single-tool principle: when one breaks it's obvious which, and the
  others keep working. (Omnigent can itself drive `claude-sdk`/`codex`/`pi` harnesses — so
  in principle `becky-new-tool`'s build agent *could* later run under Omnigent governance
  too; out of scope here, noted as a future composition.)

---

## 10. Open decisions for Jordan (need a human decision)

- **#1 — The harness reconciliation (the big one).** **Option A** (Omnigent becomes THE
  runtime; `becky-harness` becomes a pure YAML/manifest generator, `internal/pirun`'s launch
  half retires) vs **Option B** (keep both — `becky-harness` for offline/local quick runs
  *and* `becky-omni` for governed/watchable runs, sharing one generator core). **Rec:
  build B now, design toward A** once Omnigent is past alpha and the iPhone-steering
  workflow is confirmed as the default you want. This is the decision that shapes the build.
- **#2 — Self-hosted vs hosted sandbox/server.** Local Omnibox sandbox + local
  `omni server` (private, on your PC/LAN) vs a cloud host (Modal/Daytona/Fly.io) for
  heavier or always-on runs. **Rec: local/private by default** (forensic material stays on
  the machine); a self-hosted server on your own LAN for phone access; **never a third-party
  hosted sandbox for sensitive case material.** Confirm. (Also: confirm the **Windows**
  sandbox path — §8 local step 3 — since Omnibox's hard sandbox is Linux/macOS-native.)
- **#3 — Subscription vs local model.** Subscription (Claude Pro/ChatGPT, via `omni setup`)
  is the lowest-fuss default for **non-sensitive** runs; **local (Ollama/vLLM,
  OpenAI-compatible) is mandatory for sensitive case material** (`sensitive:true` forces it).
  Confirm `becky-omni` defaults to subscription but **refuses hosted models when
  `sensitive:true`** (mirrors `becky-harness` Q2 / `becky-ask` Q2).
- **#4 — Public shareable session vs private only.** A share URL is a network exposure of
  forensic material. **Rec: private/LAN-only by default** (the URL points at your own
  `localhost`/self-hosted server, reachable from your phone on the same network); a
  public/third-party-hosted share is **opt-in, logged, and forbidden for sensitive
  material**. Confirm this is the posture (it is the offline/local-only rule applied to the
  collaboration layer).
- **#5 — Which workflow to template first.** The same candidates as the harness
  (`SPEC-AGENT-HARNESS.md` Q3): (a) folder-wide transcribe+identify name-mismatch sweep —
  the best first target because it's long/unattended (so watch-from-phone earns its keep)
  and read-only (so the offline+no-write floor is easy to prove); (b) "find every clip a
  named person appears in and summarize what's said around it"; (c) a corroboration sweep
  (identify + validate + osint → combined confidence call) that writes an exhibit
  (exercises the write-gate + ASK-on-phone path). **Rec: (a) first**, then (c) to prove the
  governed-write path.
- **#6 — Alpha pinning + drift.** Omnigent is **alpha** and Databricks-new; CLI flags, the
  Omnibox config schema, and the REST/OpenAPI shapes will move. **Rec: pin an exact
  Omnigent version in the local install, keep every version-sensitive shape in
  `internal/omnirun`/`internal/agentdef`, and re-verify on each bump** (same policy as Pi
  pinning, `SPEC-AGENT-HARNESS.md` Q5). Confirm the pinning policy.

---

## 11. Sources (live, verified 2026-06-15)

- **Databricks announcement blog** — Omnigent as a meta-harness over Claude Code / Codex /
  Pi / custom agents; the three pillars (composition / control / collaboration); "messages
  and files in, text streams and tool calls out"; stateful contextual policies enforced at
  the meta-harness layer (cost-pause, npm-then-git-push approval, "write only docs it
  created"); the Omnibox OS sandbox locking down OS access + intercepting/transforming
  network requests + the GitHub-token egress-injection example; share-via-URL collaboration;
  runs on Fly.io/Railway/Modal/Daytona, many LLM providers, no Databricks dependency;
  Apache-2.0 alpha:
  https://www.databricks.com/blog/introducing-omnigent-meta-harness-combine-control-and-share-your-agents
  (fetched 2026-06-15)
- **GitHub repo / README** (`omnigent-ai/omnigent`) — the runner-vs-server split; install
  (`curl … install_oss.sh | sh`, `uv tool install omnigent`, Homebrew; Python 3.12+); the
  CLI subcommands (`omni`/`omnigent`, `run path/to/agent.yaml`, `claude`, `codex`, `debby`,
  `server start`, `host`, `setup`, `login <url>`, `attach <session_id>`, `run --fork`,
  `stop`); the **agent YAML** (`name`/`prompt`/`executor.harness: claude-sdk|codex|
  codex-native|claude-native|openai-agents|pi`/`tools:` with `type: function|agent`); the
  **policy YAML** (`policies:` → `type: function`, `handler`/`function.path`,
  `factory_params`/`arguments`; builtins `cost.cost_budget`, `safety.ask_on_os_tools`,
  `safety.max_tool_calls_per_session`); three policy levels (server/agent/session, stricter
  session first; ALLOW/ASK/DENY, first decision wins); interfaces (terminal/web `:6767`/
  mobile/macOS app/REST + `openapi.json`); deployment (Docker Compose/Render/Fly.io/Railway/
  HF Spaces/Modal); providers (Anthropic, OpenAI, Pi, OpenAI/Anthropic-compatible base_url —
  OpenRouter/LiteLLM/Ollama/vLLM/Azure — Databricks); auth (local none by default; server
  `OMNIGENT_AUTH_ENABLED=1` + OIDC); license Apache-2.0:
  https://github.com/omnigent-ai/omnigent  and
  https://raw.githubusercontent.com/omnigent-ai/omnigent/main/README.md  (fetched 2026-06-15)
- **omnigent.ai docs** — homepage (runner "wraps any agent in a sandboxed, uniform session";
  server "adds policies and shared history, and exposes every session"; built-in agents
  Polly/Debby; built with Databricks AI + Neon); **policies overview** (the policy YAML at
  both Omnigent-config and server-wide levels; ALLOW/ASK/DENY; the three stacking levels);
  **Omnibox** (`bubblewrap`+`seccomp` on Linux / Seatbelt `sandbox-exec` on macOS;
  filesystem isolation with CWD read-only + dotfile/secret masking; **default-deny egress
  proxy with an explicit allow-list of methods/hosts/paths**, private-IP + metadata blocked;
  credential brokering = placeholder in context, real secret injected only on approved
  egress); **quickstart/install** (`curl -fsSL https://omnigent.ai/install.sh | sh`, `omni
  setup`, `omni debby`, server at `http://localhost:6767`, macOS app connects by server URL):
  https://omnigent.ai/ , https://omnigent.ai/docs/policies/overview ,
  https://omnigent.ai/docs/omnibox , https://omnigent.ai/quickstart/install
  (fetched 2026-06-15)
- **Provenance / announcement** (Databricks open-sources Omnigent 2026-06-13; Matei Zaharia
  with Kasey Uhlenhuth + Corey Zumar; alpha): MarkTechPost
  https://www.marktechpost.com/2026/06/13/databricks-open-sources-omnigent-a-meta-harness-that-composes-governs-and-shares-ai-agents-across-claude-code-codex-and-pi/
  and Matei Zaharia's announcement https://x.com/matei_zaharia/status/2065827057624605146
  (fetched 2026-06-15)
- **In-repo grounding:** `SPEC-AGENT-HARNESS.md` (the Pi harness this spec reconciles with —
  its request schema §2.1, tool manifest §4, determinism/safety §7, baton §8, and
  `internal/pirun` shape `internal/omnirun` mirrors), `SPEC-BECKY-ASK.md` (the conversational
  front-door + its deferred mobile/GUI steering §6 that Omnigent fulfils),
  `SPEC-BECKY-NEW-TOOL.md` (`internal/agentrun` parallel; the deterministic-shell-around-an-
  agent pattern; privacy/local-model + metered-credit rules), `FORENSIC-OUTPUT-PHILOSOPHY.md`
  (the output contract baked into the agent prompt/`system_append`), `CLAUDE.md` §2/§4/§6
  (invariants; the cloud↔local baton; the explicit-logged-network carve-out for the new
  online/agentic tool class).

> Re-verify every Omnigent CLI flag, the Omnibox config schema, the REST/OpenAPI shapes, and
> the agent/policy YAML against the installed Omnigent version before writing code — it is
> **alpha** and Databricks-new. Items marked **ASSUMPTION (local agent must verify)** (the
> `omni run` policy/sandbox/output flags §4.1/§5.3; the Omnibox config keys §5.3; the REST
> endpoints §4.1; the Windows sandbox path §8) are the ones most likely to have drifted.
