# HANDOFF — `becky-voice` / becky-whoretana — the ORDERED, CHECKBOXED build (WHAT · HOW · WHY · VERIFY · DONE)

> This is the step-by-step that the spec (`SPEC-BECKY-VOICE.md`) was missing. The spec is the *why and the
> nuance*; **this file is the do-this-then-this.** It exists because Jordan's #1 problem is that agents get the
> concept but wander, stub forever, ask him technical questions, and never *finish*. Every step below is sized so
> a single subagent can take it to a **working, verified** state in one go. Built in the repo's "provable
> handoff" shape (`HANDOFF-TEMPLATE.md` / `STANDARDS-WORKFLOW.md` §7).
>
> **Status:** ready to execute. Phases 0–2 are **cloud-buildable** (pure Go, deterministic, no hardware) and
> should be built to completion by cloud subagents. Phase 3 is the **local runbook** (realtime model + mic +
> GUI + mouse/keyboard) the local agent drives. Model decision is **pinned: Gemini 2.5 Flash realtime FIRST**
> (§ Pin).

---

## RULES FOR THE BUILDING AGENT — read first, they are not optional

These encode the exact behaviors Jordan is tired of. Violating one means the step is **not done**.

1. **Finish to a WORKING tool, not a stub.** "It compiles" / "tests pass on a stub" is NOT done. Done = the
   step's **VERIFY command prints the asserted result** and the **DONE** box is truthfully checkable. Do not
   move on with a placeholder. (Ralph-loop: if you catch yourself stopping early, kick yourself back.)
2. **NEVER make Jordan run a CLI command or answer a technical question that this doc already decided.** All the
   decisions are here and in the spec — use them. If you hit a *genuinely* new fork this doc didn't settle:
   **do NOT dump jargon into the chat and wait.** Either (a) pick the option this doc's principles imply and
   note it, or (b) if it truly needs Jordan, surface it as a **form** (`AskUserQuestion`, chips) or a **one-line
   spoken whoretana prompt** — never "run X and paste the output." (Repo invariant, CLAUDE.md §2.)
3. **Test with mouse + keyboard where applicable.** Anything with a window/audio is done only when it's
   exercised by hand: the window opens, ▶/talk produces sound, every button works, it doesn't freeze
   (`CANVAS-NORTH-STAR.md` Definition-of-Done). Cloud can't do this; it's the local steps' gate.
4. **One step = one finished deliverable. Drive the whole step; don't stop each increment to ask.** Batch your
   questions to the form at the *end* if any remain.
5. **Reuse `internal/`; never reimplement a tool.** Every step says which existing code to build on.
6. **Five gates per step** (`STANDARDS-ENGINEERING.md`): `go build/vet/test ./...` + `gofmt -l` clean; a
   regression test that asserts **values** (not "no error"); ≤3 auto-fix rounds then stop and flag.

---

## PIN — model + scope decisions already made (do not re-open)

- **Gemini 2.5 Flash realtime is the FIRST and ONLY model wired for v1** (`gemini-2.5-flash-native-audio`, the
  one whoretana already uses). Reason (Jordan): if it doesn't work, we KNOW it's the wiring, not the model.
  **LFM2.5-Audio / Gemma-4-local are explicitly LATER** (Phase 4, after v1 works on real use). Do not start with
  a local model.
- **Tool packs / workflows are declarative files, not hardcoded Go** (Phase 0 Step 3). This is the fix for
  "I can't say 'run it like this when I ask for X'."
- **The response map ("trace dataset") is AUTO-GENERATED and fill-in-the-blank** (Phase 0 Step 4). Jordan edits
  strings, exactly like he did in the whoretana Python — he never authors it from scratch or lists tool outcomes.
- becky-voice **IS** becky-whoretana: one agent, per-context tool packs, no per-program clones (spec §1.1).

---

## PHASE 0 — Foundations (CLOUD, pure Go, deterministic). Each step is one subagent.

### Step 0.1 — `internal/catalog` (the one tool registry)
- **WHAT:** Extract the tool catalog out of `cmd/ask/catalog.go` into a new shared package `internal/catalog`,
  and add two fields per tool: `Tier` (`green|yellow|red`, **default `red`**) and `Pack` (which tool-pack(s) it
  belongs to, e.g. `reaper`, `forensic`, `default`).
- **HOW:** Move the catalog struct + entries verbatim into `internal/catalog`. Re-point `cmd/ask` to import it
  (no behavior change). `cmd/harness` (per `SPEC-AGENT-HARNESS.md` §4) imports the same package. Add the two
  fields with defaults; fill `Tier`/`Pack` for the ~10 tools the first packs use (transcribe, diarize, identify,
  pipeline, ocr, search, research, radar, scout, the REAPER bridge verbs).
- **WHY:** Everything downstream (router, rules, response map, packs) needs ONE source of truth for "what tools
  exist + how dangerous + which pack." Today it's trapped inside `cmd/ask`.
- **VERIFY:** `go test ./internal/catalog/... ./cmd/ask/...` green; `go build ./...` green; a test asserts an
  unknown tool defaults to `red`.
- **DONE:** `internal/catalog` exists, `cmd/ask` builds against it with no behavior change, tier/pack fields exist
  and are tested.

### Step 0.2 — `internal/workflowdef` (DECLARATIVE, CONDITIONAL workflows — the big one for Jordan)
- **WHAT:** A package that loads a **workflow recipe file** (`workflows/*.json`) describing a named workflow as
  an **ordered list of steps with optional conditions**, and runs it by calling existing tools. Ship the first
  recipe: `process-video`, which reproduces today's transcribe workflow **but makes diarize conditional**.
- **HOW:** Define the recipe schema (example below). Port the logic in `cmd/ask/workflow.go`
  (`runTranscribeWorkflow`) to **execute a recipe** instead of a hardcoded step string. Implement a tiny,
  **deterministic** condition evaluator (no model) over facts already produced by earlier steps (e.g. a cheap
  speaker-count probe, or `mediainfo`/`diarize --estimate`).
  ```jsonc
  // workflows/process-video.json — Jordan can read AND edit this; THIS is "run it like this when I ask"
  { "name": "process-video", "phrases": ["process this video", "do the usual", "transcribe and check"],
    "steps": [
      { "tool": "becky-transcribe" },
      { "tool": "becky-diarize",  "when": "speakers > 1" },          // <-- skips for a one-speaker clip
      { "tool": "becky-ocr" },
      { "verb": "verify-with-gemma4", "when": "speakers > 1" },      // model check only when it matters
      { "merge": "transcript" }                                       // the existing mergeTranscript output
    ] }
  ```
- **WHY:** This is THE fix for "I can't drag-and-drop components and say run it like this for X, and diarize
  shouldn't run on a one-speaker video." The recipe is a **file Jordan controls**, conditions keep it from doing
  unnecessary work, and it's deterministic so the same phrase always does the same thing.
- **VERIFY:** Two table tests with **faked tool outputs**: a 1-speaker fixture → the run **does NOT call diarize**
  (assert the executed-step list); a 3-speaker fixture → it **does**. `go test ./internal/workflowdef/...` green.
- **DONE:** `process-video.json` drives the real chain; diarize is conditional and the skip is asserted by a
  value test; `cmd/ask` transcribe path uses the recipe (no behavior loss vs the old hardcoded chain).

### Step 0.3 — `internal/voiceresp` (the response map — AUTO-GENERATED, fill-in-the-blank)
- **WHAT:** (a) A generator that emits a **pre-populated** `responses.json` skeleton — for **every tool in the
  catalog × every outcome (`ok` / `partial` / `error`)**, a sensible **default line in whoretana's voice** that
  Jordan edits by replacing the string. (b) A deterministic chooser (round-robin/seeded) that picks the line for
  a given tool+outcome. (c) The `fix_verb` mapping ("fix it" → that tool's repair verb).
- **HOW:** Walk `internal/catalog`; for each tool emit `{ "ok": ["…"], "partial": ["…"], "error": ["Ah shit,
  {tool} broke. {short}"], "fix_verb": "becky-new-tool" }`. Pre-fill defaults so the file is **usable on day one
  and editable like the whoretana Python** (where Jordan just replaced the default strings). The chooser is pure
  Go.
- **WHY:** Directly answers Jordan's "I have no idea how to make a trace dataset / what responses we need." He
  doesn't — the catalog defines the outcomes, the generator fills defaults, he **replaces strings**. Same
  experience as `speak_error` in his fork, now data-driven (spec §5.6).
- **VERIFY:** `go test ./internal/voiceresp/...`: every catalog tool has all three outcomes with ≥1 line;
  round-robin returns each variant before repeating; `fix` resolves to the declared `fix_verb`; choosing a line
  calls **no model**.
- **DONE:** `becky-voice gen-responses` writes a complete, pre-filled `responses.json`; the chooser is tested;
  Jordan can open the file and see/replace human-readable lines per tool.

### Step 0.4 — `internal/voicerules` (the safety gate)
- **WHAT/HOW/WHY/VERIFY/DONE:** Exactly as `SPEC-BECKY-VOICE.md` §8 cloud-item 2 (tier gate, staging gate,
  budget, cloud-vs-local policy; the Highlight-bug regression test: a staged screenshot is **not** marked sent
  without an explicit confirm token). Pure Go, value-asserted.

---

## PHASE 1 — The driver (CLOUD, pure Go). One subagent.

### Step 1.1 — `cmd/becky-voice` (the deterministic core of the agent)
- **WHAT:** The Go driver: takes an **NDJSON intent stream** in (so it's testable with no mic/model), routes
  each intent via `internal/intent` + `internal/catalog` to the right front-door (`becky` / `becky-ask` /
  `becky-harness` / a workflow recipe / the REAPER bridge), enforces `internal/voicerules`, picks the spoken
  line via `internal/voiceresp`, writes an audit transcript, and emits the reply text + chosen-clip id.
- **HOW:** Mirror `cmd/harness` structure. The realtime model is **out of process** (Phase 3 feeds the NDJSON);
  here, a `--selftest` reads a scripted intent file and asserts the routed command + tier decision + chosen
  response line + transcript.
- **WHY:** This is the becky-shippable, hardware-free heart. Getting it green means the local agent only has to
  bolt on the mic/model/GUI — shrinking the flaky hardware half to the minimum.
- **VERIFY:** `becky-voice --selftest` exits 0 and prints the asserted routing + tier + line for a scripted
  stream (a GREEN auto-runs, a RED is refused, "fix it" maps to the fix verb, a workflow phrase runs the recipe).
  `go test ./cmd/becky-voice/...` green.
- **DONE:** `--selftest` is the one-command offline proof; routing + rules + response selection all verified with
  **no mic and no model**.

> Phase 1b (proactive: `internal/proactive` + `internal/heartbeat` + `internal/briefing`) is spec §8 items 3/5/6.
> **Defer until v1 reactive works** — Jordan's priority is "make it simply work" first. Listed here so it isn't
> lost, but do NOT block v1 on it.

---

## PHASE 2 — Tool packs wired (CLOUD). One subagent.

### Step 2.1 — the first two packs
- **WHAT:** Define two pack files: `packs/default.json` (a tiny always-loaded set) and `packs/reaper.json` (the
  REAPER-bridge verbs). A pack = a saved **allowlist + tier overrides** (reuse the harness allowlist mechanism).
- **HOW:** `internal/voicerules` loads the pack for the active context; the router only offers the active pack's
  tools to the model. Switching packs = swapping a small list (spec §1.1).
- **WHY:** This is the "tool packs are a great idea" made real, and the guard against handing the model a
  gazillion tools (your worry).
- **VERIFY:** test asserts: with `reaper` active, a non-pack tool is **not** offered; switching to `default`
  changes the offered set. `go test` green.
- **DONE:** two packs exist, the active pack scopes the toolset, asserted.

---

## PHASE 3 — Local runbook (LOCAL Claude Code: realtime model + mic + GUI + mouse/keyboard)

> These need hardware; cloud cannot do them. Each is a tight runbook item, NOT open-ended. Build to the DONE box.

- [ ] **3.1 FastRTC + Gemini 2.5 Flash realtime** beside `pyhelpers`; feed its intents/audio to `cmd/becky-voice`
  over the NDJSON seam. **Cost-critical (spec §6.1): do NOT stream the mic to Gemini continuously** (that bills
  ~$2/hr idle). Run a **local on-device VAD ($0)** always; only forward audio to Gemini when a **gate** fires
  (push-to-talk / push-to-disable / wake gesture / addressee §4.8). Keep the GUI **always resident** so it's
  instant (no relaunch), and show a small **token/cost meter**. **DONE:** you talk → it routes to a real becky
  tool → it speaks a line from `responses.json`; no wake word; barge-in works; **sitting idle for 10 min sends
  ~no audio tokens** (verify on the meter).
- [ ] **3.2 The whoretana floating GUI** (reuse the existing whoretana/Jarvis UI shape) bound to `cmd/becky-voice`.
  **DONE (mouse/keyboard):** window opens, mute toggles, a typed command works, it doesn't freeze.
- [ ] **3.3 Fill in `responses.json`** — Jordan replaces the default lines with his own (the 5-minute job, like
  his Python edit). Render the canned lines to clips (`becky-voice build-audio-cache`). **DONE:** a tool error
  plays Jordan's line instantly from cache.
- [ ] **3.4 Hardware Definition-of-Done** (`CANVAS-NORTH-STAR.md`): talk → GREEN action runs → YELLOW asks once →
  RED refused without explicit ok → "fix it" deploys the coding agent → off switch silences instantly. Report
  what worked / what degraded **via the form or voice**, not a jargon dump.

## PHASE 4 — LATER (only after v1 is used on real work)
- [ ] Swap the realtime model to **LFM2.5-Audio-1.5B** (local E2E) + Gemma-4 for vision (spec §6). Compare feel.
- [ ] Turn on the **proactive background analyst** (Phase 1b) once the reactive half is trusted.

---

## The one-command proof cloud will have produced (for the local agent to see it's real)
`becky-voice --selftest` (Step 1.1) — runs the real router + rules + response selection over a scripted intent
stream, **no mic, no model**, asserts values, exits 0. If that's green, the deterministic half is done and Phase
3 is wiring, not building.
