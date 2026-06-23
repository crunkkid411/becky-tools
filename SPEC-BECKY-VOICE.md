# SPEC — `becky-voice` — the always-on, proactive voice + context front-end for the whole becky-tools suite

> ## STATUS — SPEC, NOT BUILT, AWAITING JORDAN'S GO/NO-GO
> Author: cloud/web agent, drafted **2026-06-23** on `claude/ai-daw-integration-hh5y8l`, as the
> follow-on to `research/daw-ai-control-reaper-vs-ableton.md` (the realtime-interface conclusion) and
> Jordan's stated end goal: *"I just talk when I need my computer to do something — not just for REAPER,
> for becky-tools."* Design-only; nothing here is built. It obeys the house rules of `SPEC-AGENT-HARNESS.md`
> / `SPEC-BECKY-ASK.md`: JSON/NDJSON seams, exit-coded, **offline-capable**, degrade-never-crash, **reuse
> `internal/`**, never modify source inputs, and — load-bearing here — **never reimplement a tool.**
>
> **Two references Jordan named are the design inputs:** (1) **whoretana** — his existing Python/all-API
> realtime assistant that already controls his PC + writes code; the **whoretana-specific behaviors are
> explicitly LEFT TO THE LOCAL AGENT** (§7) — Jordan will direct those, and they're the chance to rebuild
> clean the parts of whoretana he "doesn't like or understand." (2) **Highlight AI** — the always-on local
> desktop companion that proactively gathers context (auto-attach a screenshot, pick which browser
> tabs/window to feed the model) and is hands-free; becky copies the *good proactive behavior* but makes it
> **customizable and forensic-safe** (Highlight is non-customizable + corporate-geared — Jordan's complaint).

---

## 0. TL;DR (read this part, Jordan)

`becky-voice` is the **one always-on thing you talk to** that drives *every* becky tool. You speak (no wake
word, interrupt anytime); it hears + sees (screen/active window/audio), figures out which becky tool(s) you
mean, **asks before anything destructive, just does the safe stuff**, and talks back. It also *watches* — like
Highlight — so it can offer ("want me to trim that take?") instead of only answering.

**It is NOT a new brain and NOT a new tool that does work.** It's a thin **realtime skin + a rules layer** over
the tools and agent-loops you already have (`becky`, `becky-ask`, `becky-harness`, the REAPER bridge, the
Strudel jam). The musical/forensic *work* stays in the deterministic tools; this just becomes the ears, eyes,
mouth, and the **guardrails** around an always-on agent. The hard part — every capability already being a clean
CLI tool — is done; this is the glue + the rules.

The one thing that makes it safe to leave "always waiting around to do something": **the rules/harness layer
(§4)**. That's the heart of this spec, because you said so.

---

## 1. Where it sits — the fourth front-door, above the other three

becky already has three ways in; `becky-voice` is the always-on realtime layer **over** them. It owns no work.

| Tool | What it is | Who picks the next step | Determinism |
|---|---|---|---|
| `becky` (orchestrator) | command engine — `becky <verb> "<arg>"` | a human/script types fixed ops | fully deterministic |
| `becky-ask` | chat front-door — English → a plan, on opt-in | human approves each plan; model classifies once | deterministic flow, model at narrow points |
| `becky-harness` | per-request agent harness — a Pi agent loops over a declared toolbox to a declared goal | the model loops, inside a fixed allowlist | non-det *inside*, det *around* |
| **`becky-voice`** (this spec) | **always-on realtime voice + proactive watcher** — talk anytime; it routes to the right front-door, and *notices* things to offer | **you** (reactive) **and** a customizable proactive watcher (it proposes) | det shell + rules around a realtime model |

**The progression:** `becky` = you drive · `becky-ask` = you talk, it plans, you approve · `becky-harness` =
you declare a goal+toolbox once and it grinds · **`becky-voice` = it's always there, you just talk, and it also
watches and offers.** `becky-voice` reaches its goals by **calling the existing front-doors**, never by
reimplementing them — when a tool breaks it's still obvious which one, and the rest keep working (the
single-tool principle, preserved).

---

## 2. The two halves

`becky-voice` is exactly two loops sharing one rules layer and one tool registry:

- **REACTIVE (the "I just talk" half) — close to done.** Mic/cam/screen in → realtime model → intent → route
  to a becky front-door → result → spoken + on-screen reply. No wake word; **barge-in** (you talk over it, it
  stops). This is the "how computers should work" feel.
- **PROACTIVE (the real new work) — a background analyst, NOT a suggestion firehose.** A customizable **watcher**
  ambient-samples context (screen, active window, audio, becky tool events), runs becky's **corroborate-then-
  conclude protocol on its own findings** (≥2 signals / consult a second model — §5.2), and accumulates only
  what clears the bar into a **visual briefing you review on your time** (HyperFrames/diagram in becky-canvas —
  §5.3), instead of nagging you out loud. It runs cheap and always-on (LFM2.5), and you **stay in control** of
  what it looks at (§4.7). *Proactive-on-reads, propose-on-actions, silent-unless-corroborated.*

Both halves are a **thin driver** over: the **realtime seam** (§3), the **tool registry** (§3.2, reused), the
existing **agent loop** (`internal/ctlagent` / `internal/pirun`), and the **rules layer** (§4).

---

## 3. Architecture — the realtime seam (reuse, don't reinvent)

```
  mic / camera / screen / active-window / audio-loopback / clipboard
        │
        ▼
  ┌───────────────────────────────────────────────────────────────────────┐
  │ FastRTC transport  (Python, gradio-app/fastrtc — in research doc §4)   │
  │   • WebRTC/WebSocket duplex, built-in VAD + turn-taking + barge-in     │
  │   • lives beside becky's existing Python pyhelpers                      │
  └───────────────────────────────────────────────────────────────────────┘
        │  NDJSON seam (the SAME deterministic engine↔front seam GUI-RULES.md mandates)
        ▼
  ┌───────────────────────────────────────────────────────────────────────┐
  │ REALTIME MODEL  (the ears+mouth; brain-swappable on one transport)     │
  │   • CLOUD:  Gemini Live (2.5 Flash native-audio / 3.1 / 3.5) — true    │
  │            duplex, proactive-audio timing, video-in                     │
  │   • LOCAL:  internal/avlm (Gemma-4 QAT, audio+vision IN) + internal/tts │
  │            (NeuTTS Air OUT) — offline; non-duplex (honest caveat)       │
  └───────────────────────────────────────────────────────────────────────┘
        │  produces: an INTENT (text) + any attached context the watcher chose
        ▼
  ┌───────────────────────────────────────────────────────────────────────┐
  │ becky-voice DRIVER (Go, deterministic) — THIS tool. Owns the RULES.    │
  │   1. RULES/SAFETY gate (§4)   2. ROUTE intent → front-door (§3.1)      │
  │   3. registry lookup (§3.2)   4. transcript/audit   5. spoken reply     │
  └───────────────────────────────────────────────────────────────────────┘
        │ shells the EXISTING front-doors — never reimplements a tool
        ▼
   becky · becky-ask · becky-harness · the REAPER bridge · the Strudel jam
```

**Boundary rule (load-bearing, same as `becky-harness`):** the Go driver is deterministic and owns every
guarantee; the non-determinism is contained in the realtime model. The model **proposes**; the **rules layer
disposes** (§4). becky's deterministic tools do the actual work and emit their own JSON, unaltered.

### 3.1 Routing — intent → the right front-door (no new dispatcher)
The driver classifies each intent to a target, reusing the **same intent/catalog logic** the other front-doors
use (`internal/intent` for music phrases; `cmd/ask`'s classifier for fuzzy English): a **fixed op** → `becky`;
a **fuzzy one-shot** → `becky-ask`; an **iterate-over-many goal** → `becky-harness`; a **take/edit on the open
session** → the REAPER bridge; a **jam pattern** → Strudel. Routing is the *only* new decision, and it picks an
existing door — it never invents a fourth execution path.

### 3.2 The tool registry — REUSE `internal/catalog`, do not build a 4th copy
The router needs one machine-readable list of every tool + its args. **This already has a home:**
`SPEC-AGENT-HARNESS.md` §4 mandates lifting `cmd/ask`'s `catalog.go` into a shared **`internal/catalog`** that
both `cmd/ask` and `cmd/harness` import. `becky-voice` imports the **same** `internal/catalog` — so all
front-doors agree on what tools exist and never drift. *(If `internal/catalog` isn't extracted yet when this is
built, extracting it is a prerequisite step, not a new invention.)* This is the single dispatch source of truth.

---

## 4. Rules & harnessing — the heart of the spec (because it's always waiting to act)

An always-on agent that can drive your whole machine is only as good as its leash. These are non-negotiable and
the driver enforces them deterministically (the model cannot override them):

1. **Action tiers — proactive on reads, ask on writes.**
   - **GREEN (auto, even proactively):** read-only/analytical tools (transcribe, identify, search, analyze a
     take's onset, *propose* an edit) and anything trivially reversible. The watcher may run these on its own.
   - **YELLOW (confirm once, then go):** in-session edits with undo (trim/fade a take, add a clip, change a
     param) — spoken/one-tap confirm; undoable via the existing `internal/undo` / `ctledit` history.
   - **RED (always explicit, never proactive):** destructive/irreversible/outward-facing — delete, overwrite,
     export, anything that leaves the machine, anything touching source media. Requires an explicit, unambiguous
     confirmation every time. Source originals stay immutable regardless (becky invariant).
   This tiering is the single biggest dial; it is **per-tool metadata in `internal/catalog`** (default = RED for
   unknown), so it can't be forgotten when a tool is added.
2. **A hard kill / mute, always.** One hotkey + one spoken phrase instantly stops listening, stops any in-flight
   action, and drops the mic/cam. Always-on must be instantly-off.
3. **Privacy-first capture (mirror Highlight + becky's offline invariant).** Screen/audio/window capture is
   **processed locally and never leaves the machine unless a query explicitly attaches it.** For sensitive
   forensic material the realtime layer runs **local-only** (Gemma-4 + NeuTTS, `PI_OFFLINE`-style), never the
   cloud model — the same rule `becky-harness` §7 / `becky-ask` already enforce. The forensic *core* stays
   offline+deterministic; only the *assistant* layer may use the cloud API, and only for non-sensitive work.
   This is the deliberate, scoped exception recorded in the research doc — not a loosening of the core invariant.
4. **Scope & rate limits on the proactive watcher.** The watcher has a budget: max proposals/minute, quiet-hours,
   and a per-rule on/off. It must never nag (becky's "a flood of maybes a human must sort = tool failure" applies
   to suggestions too). Default posture: **propose rarely, act GREEN-only, escalate to voice only when corroborated.**
5. **Full audit transcript.** Every heard-intent, every routing decision, every tool call + result, every
   proactive proposal (accepted/declined) is logged NDJSON — the determinism/audit anchor for a non-deterministic
   realtime runtime (identical discipline to `becky-harness` §6/§7). You can always see exactly what it did and
   re-run the tool calls by hand.
6. **Customizable rules (the Highlight complaint, fixed).** All of the above — tiers, watcher rules, quiet hours,
   which context sources are allowed, cloud-vs-local per context — live in **one human-editable
   `becky-voice.rules.json`**, not hard-coded. This is the thing Highlight wouldn't let Jordan change.
7. **CONTROL over context — directable, not just visible (the REAL Highlight lesson, corrected by Jordan).**
   Highlight *did* have an indicator showing what it attached — that wasn't the failure. The failure was
   **control**: when Highlight decided a screenshot was needed, Jordan **couldn't redirect it** — he couldn't
   just say *"hey, look at the screen"* on demand the way he can with whoretana. So the binding rule is
   **directability**: (a) Jordan can **summon context any time by voice** ("look at the screen", "read this
   tab") — on-demand, like whoretana; **and** (b) he can **override or cancel** the watcher's automatic context
   choice — the agent never takes the context decision away from him. Visibility is necessary but *not
   sufficient* (Highlight proves it): the staged set is shown AND strippable AND nothing leaves until he
   confirms (§4.3), but above all he can always **steer what it looks at.** The agent proposes context; Jordan
   commands it. ("Show me, don't do it" — `CANVAS-INSPIRATION.md` — with the human holding the wheel.)
8. **Addressee awareness + a real off switch (always-on only).** Always-on must know the difference between
   **Jordan talking TO it** and **Jordan talking to someone else** — it must not act on overheard speech.
   Corroborate the addressee from **vision** (is he facing the camera / the becky window? gaze, lip-activity)
   **plus** audio (direct address, name) before treating speech as a command — same corroborate-then-conclude
   discipline as everything else; when unsure, it stays silent, it does not guess. And a plain **on/off** toggle
   always exists (hotkey + the rules file) — "always-on is nice, but the option to turn it off" is the design,
   i.e. both. Default when addressee is uncertain: **listen, don't act.**

---

## 5. The proactive watcher — the anti-bullshit engine, not a suggestion firehose

> **Jordan's reframe (load-bearing):** *"Proactive should not be a problem — proposing bullshit to me all the
> time is. That's why we have structured protocols, corroborate multiple data points, consult other models."*
> So the watcher is **not** a thing that pipes up with guesses. It is a background analyst that runs becky's
> **existing forensic discipline on itself** and only ever surfaces something that already cleared the bar.

### 5.1 The three jobs (each toggleable in the rules file)
- **Context sources:** active-window title/app, on-demand + event-triggered screen capture, audio loopback,
  clipboard, and — uniquely becky — **becky tool events** (`cmd/events`/`internal/seam`): "a REAPER take just
  finished recording," "a pipeline stage emitted a finding." (On-demand capture is **Jordan-summonable**, §4.7.)
- **Reactive context assembly:** when you ask something, assemble the *right* context (the window you're looking
  at, the take you just cut) — GREEN-only, staged + strippable (§4.7), Jordan-steerable.
- **Ambient analysis (the always-on background job Jordan described):** *"I could just talk with becky for a
  while and the proactive thing to do is pay attention — compare it to what we're working on, find gaps in my
  life/computer, research what other humans are doing with AI, and follow the protocols in the background."*

### 5.2 The anti-bullshit gate — corroborate-then-conclude, applied to PROPOSALS
A finding is **never** surfaced on a single weak signal. The watcher reuses `FORENSIC-OUTPUT-PHILOSOPHY.md`
verbatim, pointed at itself: **≥2 independent signals agree → surface it as a conclusion; a lone weak signal →
hold it as a silent candidate, do not interrupt.** Where it helps, it **consults a second model** (cheap local
vs. a stronger one) and only escalates on agreement. *"A flood of maybes a human must sort = tool failure"*
applies to suggestions as hard as it applies to forensic output. The default posture is **silent unless
corroborated** — the watcher's success metric is *signal*, not activity.

### 5.3 Delivery — a ~30-SECOND NARRATED DEBRIEF VIDEO, in whoretana's voice
> Jordan (decisive): *"I'm not reading a 3-page proposal list… but I'd definitely watch a 30-second debrief /
> proposal video narrated by whoretana."*

This is the locked delivery format. Corroborated findings **accumulate silently** and are delivered as a
**short (~30s) narrated debrief video** — **HyperFrames** renders the visuals (`heygen-com/hyperframes`: agent
writes HTML/CSS → deterministic MP4 via the FFmpeg becky already runs; ideal for "here's what I found" cards /
charts), and the **persona voice narrates** it (NeuTTS / the realtime model — see §5.3a). It plays in
**becky-canvas** when Jordan sits down; he watches 30 seconds instead of reading three pages. A static
**Mermaid** diagram is the even-quicker glance. (Only a GREEN, already-corroborated, time-sensitive item earns a
live spoken mention, under §4.4's budget — everything else waits for the debrief.) **This is an accessibility
feature, not a flourish:** it directly serves Jordan's reading limits — the agent reads so he doesn't have to.

### 5.3a Persona — whoretana is *fun*, and that's a feature
Jordan: *"whoretana is funny — we turn her on sometimes just because it's fun."* The assistant has a **persona**
(voice + manner); the debrief is narrated *in character*, and the conversational replies carry it. This is part
of why an always-on assistant is something he actually *wants* on — engagement is a real design goal, not
decoration. The persona lives in the realtime/TTS layer + a system-prompt style; it never overrides the §4
safety rules or the forensic output contract (a finding's *content* stays honest; only its *delivery* has
personality). whoretana-specific persona tuning is the local agent's lane (§7).

### 5.4 What the ambient job actually does — orchestrate EXISTING becky tools, don't invent
The background analyst is **cheap and always-on** using the small **Liquid LFM2.5-VL** model (already adopted —
`internal/vision` / `SPEC-BECKY-VISION-MODELS.md`; Jordan: *"it's not dumb, and harnessed properly it'd find
all kinds of little stuff I don't have time to prompt for"*). It **does not** reimplement research — it **runs
becky's existing research tools under protocol**: `becky-research` (deep-research harness), `becky-radar`
(Chrome history → flagged models/tools), `becky-scout` (assess sources for things that improve becky). The
ambient loop: *listen/watch → form a candidate (gap in his work, a tool others are using, a mismatch vs what
we're building) → corroborate via the research tools + a second model (§5.2) → if it clears the bar, add it to
the visual briefing (§5.3); else hold it silently.* All GREEN/read-only by tier (§4.1); RED never auto.

### 5.5 The reactive trigger model (unchanged, in-session)
`notice (event/context) → check the rule → GREEN: do it and mention it · YELLOW: propose and wait · RED:
never proactive.` Example: take finishes → onset analysis (GREEN) → "that take has 1.2s of dead air up front —
trim + fade?" → YELLOW confirm → trim via the REAPER bridge → re-verify (the research doc's closed loop). Reuses
the habit-learning trim/fade loop, not new musical logic.

### 5.6 The background-agent harness — how the "little guy" actually runs (this is the part to dial in)
> Jordan: *"give him narrow direction with specific steps, let it think, but the harness has to be dialed in —
> hooks that reroute certain things, a couple places to start, maybe cron/heartbeat like hermes-agent… I'm not
> sure what the best approach is."* And: *"set routines within limitations; if it finds something useful it
> hands off to Qwen or Gemma, they review it and have their own protocol of what happens next."*

The honest answer from the current field (verified 2026-06-23): **don't run one always-thinking loop** (cost +
runaway). Use a **heartbeat scheduler + bounded goal-runs + tiered model handoff**. Three grounded patterns
compose into exactly what Jordan described:

1. **Heartbeat / cron tick (the clock) — model the hermes-agent primitive.** A long-running becky daemon ticks
   on an interval; most ticks are **`no_agent` deterministic checks** (cheap watchdogs: "did a take finish?",
   "new browser history since last sweep?") that **burn no model**. Only when a tick's deterministic trigger
   fires does it wake a model. [hermes-agent: 60s scheduler tick, isolated sessions, file-lock against overlap,
   first-class heartbeat jobs, `no_agent=True`.] This is "routines within limitations" — narrow, scheduled,
   guard-railed, cheap by default.
2. **Bounded goal-run, not an open loop (the stopping condition) — model Anthropic `/goal`.** When a trigger
   warrants thinking, the cheap **LFM2.5** scout runs a **goal-bounded** mini-task ("is there a real candidate
   here? — stop when decided"), not an endless `/loop`. Anthropic shipped exactly this distinction in June 2026:
   `/loop` = keep going on a clock; **`/goal` = keep going until a checkable condition is true** — and their
   "Effective harnesses for long-running agents" post is the canon for the guardrails. becky's existing
   **Ralph-loop** discipline (kick the agent back if it claims done prematurely — already used by
   `becky-new-tool`) is the same idea, in-repo.
3. **Tiered model escalation with per-tier protocols (Jordan's hand-off).** The pipeline is a **corroboration
   ladder**, each rung with its own bounded protocol:
   - **Tier 0 — LFM2.5 scout (always-on, cheap):** watch/listen, form a *candidate*. Discard the obvious noise.
   - **Tier 1 — Qwen / Gemma reviewer (woken only on a candidate):** review it under **its own protocol** —
     corroborate (≥2 signals, §5.2), optionally run a becky research tool, then decide *what happens next*:
     **drop · dig deeper (spawn a `becky-research` goal-run) · escalate to the debrief (§5.3) · propose a YELLOW
     action (§4.1, never auto-RED).** "Their own protocol of what happens next" = a small per-tier rules block
     in `becky-voice.rules.json`.
   - Escalation only ever moves a finding *up* to more scrutiny or to the visual debrief — **never straight to
     an action without the tier gate**. This is "consult other models" (§5.2) made into an explicit ladder.
4. **Hooks that reroute (Jordan's "hooks").** A hook table (mirroring hermes' hook system / Claude Code hooks)
   lets a tick or a finding be **rerouted** before/after a step — e.g. "any finding touching case material →
   force local models + skip the cloud debrief", "a REAPER take event → jump straight to the trim protocol,
   skip the scout." Hooks are declared in the rules file; they are the customizable wiring Jordan asked for.

**Recommendation:** start with **(1) heartbeat + mostly `no_agent` deterministic triggers**, escalate to a
**(2) `/goal`-bounded LFM2.5 scout**, hand confirmed candidates **up the (3) tier ladder to Qwen/Gemma**, and
deliver via the **30s debrief (§5.3)** — all wired by **(4) hooks** in `becky-voice.rules.json`. This keeps it
cheap, bounded, auditable, and dialed-in, which is the whole concern. The exact intervals / tier prompts /
hook table are tuning the local agent + Jordan settle on real use (§10 Q6).

---

## 6. Cloud vs local — one transport, swap the model (from the research doc)
- **Cloud (Gemini Live):** true low-latency duplex + barge-in + video now; for non-sensitive work; Jordan is fine
  with API calls here.
- **Local (Gemma-4 QAT in + NeuTTS out, over FastRTC):** offline/private; mandatory for sensitive forensic
  material; honest caveat — it approximates but does not match native-audio duplex. FastRTC supplies VAD +
  turn-taking either way, so only the model's audio quality differs. Selected per-context by the rules file (§4.6).

---

## 7. The whoretana carve-out — explicitly the LOCAL AGENT's lane
Jordan: *"I'd want some specific things modeled after whoretana but I can have the local agent do that …
whoretana can basically control my computer and create new code already — but it's all Python, all API, and
there's some things I don't like or don't understand."* So:

- **This spec defines the becky-native *skeleton*** — the realtime seam, the router, the **rules/harness layer**,
  the registry reuse, the audit transcript. That is the cloud-shippable, deterministic, testable half.
- **The whoretana-specific behaviors** (the exact computer-control verbs, the code-creation flows Jordan wants
  carried over) are a **LOCAL-AGENT task Jordan directs interactively.** They map onto becky's existing
  agent-loop (`internal/ctlagent` / `internal/pirun`) + the GREEN/YELLOW/RED tiers — they do **not** get a new
  bespoke Python stack. This is the chance to **rebuild clean, in Go under the rules**, the whoretana parts
  Jordan "doesn't like or understand," instead of porting the mess. The spec's job is to give those behaviors a
  safe, rule-governed home; Jordan + the local agent decide the exact verb set.

---

## 8. Build plan — cloud vs local baton (per `CLAUDE.md` §4)

**Cloud / web agent (Go + the deterministic half, no realtime model needed):**
1. `internal/catalog/` — ensure the shared tool registry exists (extract from `cmd/ask` per harness §4 if not
   already), **and add the GREEN/YELLOW/RED action-tier field per tool** (default RED). Value-asserting tests.
2. `internal/voicerules/` — load + validate `becky-voice.rules.json`; the tier gate; watcher budget/quiet-hours;
   cloud-vs-local context policy; **the context-staging gate (§4.7): context is assembled into a `StagedSet`
   that is returned for display and is NEVER auto-sent — `Send` requires an explicit confirm token.** Pure Go,
   fully unit-testable — assert: a RED tool is refused proactively, a GREEN one allowed, budget enforced,
   sensitive-context forces local, **and a staged screenshot/tab is NOT marked sent without an explicit confirm
   (the Highlight-bug regression test)**. This is the safety core — test it hard, assert VALUES.
3. `internal/proactive/` — the anti-bullshit gate (§5.2) + the ambient orchestrator (§5.4): the
   corroboration rule (a candidate needs ≥2 agreeing signals before it's surfaced; a lone signal is **held**,
   not emitted), the proposal budget, and the deterministic plumbing that fans a candidate out to the existing
   `becky-research`/`becky-radar`/`becky-scout` tools and collects their JSON. **Pure Go, value-asserted:** a
   1-signal candidate is HELD (not surfaced), a 2-signal candidate is SURFACED, budget caps enforced. (The
   small-model judgment is faked in tests; the *gate* is deterministic.)
4. `cmd/becky-voice/` — the driver: NDJSON seam in (faked realtime events), route via `internal/catalog` +
   `internal/intent`, enforce `internal/voicerules` + `internal/proactive`, shell the existing front-doors,
   write the audit transcript, emit spoken-reply text. A **`--selftest`** one-command offline proof (feed a
   scripted NDJSON intent stream → assert the correct front-door command + tier decision + transcript, **runs no
   model, no mic**).
5. `internal/briefing/` — render a set of corroborated findings to the **~30s narrated debrief (§5.3)**: emit
   the **HyperFrames HTML** (deterministic, agent-writes-HTML → MP4 via the ffmpeg becky already has) + the
   narration script for the persona TTS, and/or a **Mermaid** diagram. Pure Go templating + an existing-ffmpeg
   call; test asserts the HTML/script/diagram contains the findings (value-asserted); render-to-MP4 is the local step.
6. `internal/heartbeat/` — the harness (§5.6): an interval scheduler with **`no_agent` deterministic triggers**
   (cheap, model-free), a **`/goal`-bounded** scout step, the **tiered escalation ladder** (LFM2.5 → Qwen/Gemma,
   each tier's "what next" protocol from the rules file), and the **hook table** (reroute before/after a step).
   Pure Go + faked models; value-asserted: a `no_agent` tick burns no model, a scout run stops at its goal
   condition, a candidate escalates exactly one tier, a hook reroutes a case-material finding to local-only.
7. `internal/seam` reuse for the proactive event source; a faked event feed for tests.
8. Gates green on Ubuntu+Windows (`go build/vet/test ./...`, `gofmt`); push to the `claude/*` branch; draft PR.

**Local agent (Jordan's Win10 PC — realtime model + hardware):**
1. Stand up **FastRTC** beside `pyhelpers`; wire the mic/cam/screen capture + the NDJSON seam to `cmd/becky-voice`.
2. Wire the realtime model both ways: **Gemini Live** (API) and **local Gemma-4 + NeuTTS**; confirm barge-in +
   no-wake-word feel; confirm the local path degrades gracefully.
3. Wire the **ambient analyst** on **LFM2.5-VL** (`internal/vision`): the always-on background loop that feeds
   candidates into `internal/proactive` and renders the visual briefing into **becky-canvas**. Tune the
   addressee detector (§4.8: vision + audio → "is he talking to me?") on real use.
4. With Jordan, define + wire the **whoretana-specific** verbs (§7) onto the agent loop under the tiers.
5. The hardware Definition-of-Done (per `CANVAS-NORTH-STAR.md`): you talk → it hears with no wake word → it
   knows when you're NOT talking to it → it does a GREEN action unprompted once → it *proposes* a YELLOW one and
   waits → a RED one is refused without explicit ok → the kill/off switch instantly stops it → after a session
   it shows ONE visual briefing of corroborated findings, not a stream of nags. Tune until it helps without
   nagging. Report back.

---

## 9. Composition — no new orchestrator, no new tool-that-works
- `becky-voice` **calls** `becky` / `becky-ask` / `becky-harness` / the REAPER bridge / Strudel; it implements
  none of them. It is the realtime *skin* + *rules* only.
- `becky-ask` stays the chat front-door; `becky-voice` is what makes it hands-free + proactive. `becky-harness`
  stays the per-request grinder; `becky-voice` can *launch* one by voice ("for every clip in this folder…").
- **It drives Jordan's OTHER CLIs too — including Claude Code — and digests them so he reads less.** Jordan's
  goal: *"eventually whoretana interacts with my Claude Code and other CLI tools for me and just tells me what's
  important so I don't have to read so much."* becky already has **`internal/agentrun`** (drives headless
  `claude -p`) — `becky-voice` reuses it to run Claude Code, then **digests the long output into the 30s narrated
  debrief (§5.3)** instead of making Jordan read a wall of agent text. This is the reading-load/accessibility
  payoff, and it's reuse, not new plumbing.
- Keeping the realtime skin thin and the work in the existing tools is what preserves the single-tool principle —
  the explicit guard against this becoming "one fragile mega-project" (`CLAUDE.md` §1).

---

## 10. Honest open questions (need Jordan's decision)
- **Q1 — Default realtime model.** Cloud Gemini Live (best feel, non-sensitive) vs local Gemma-4+NeuTTS (private,
  non-duplex) as the *default*, with the rules file switching per-context? Rec: **local default for anything
  touching case material; cloud default for music/canvas work.** Confirm.
- **Q2 — How proactive, out of the box?** Default watcher posture: **propose-rarely, GREEN-auto-only**. Too quiet
  or too eager? This is the single most "feel"-dependent dial and only Jordan can set it on real use.
- **Q3 — Wake-word-less always-listening vs a push hotkey.** Highlight uses Cmd-anywhere; Gemini Live can be truly
  always-listening. Always-on mic raises the privacy bar (§4.3). Rec: **ship both, default to push-hotkey, let the
  rules file opt into always-listening.** Confirm.
- **Q4 — Which front-door does an ambiguous intent default to?** Rec: `becky-ask` (it already plans + asks before
  acting), so ambiguity degrades to "it proposes a plan," never to a wrong action.
- **Q5 — whoretana verb set (§7).** Which whoretana capabilities to carry over first, and which "things you don't
  like" to deliberately drop/redesign? A local-agent + Jordan working session, not a cloud decision.
- **Q6 — Ambient analyst cadence + corroboration bar (§5.2/§5.4).** How often does the background analyst run its
  research sweeps, and how strict is the ≥2-signal bar before something reaches the briefing? Rec: **strict bar,
  low cadence, batch into one briefing** — err toward silence; a too-eager analyst is the Highlight-proposal
  failure in a new coat. Tunable in the rules file; Jordan sets the comfort point on real use.
- **Q7 — Briefing surface (§5.3).** HyperFrames MP4 vs a live Mermaid/HTML panel in becky-canvas as the *default*
  digest format? Rec: **Mermaid/HTML panel for the quick digest, HyperFrames MP4 only for a richer weekly-style
  roundup** (rendering video for every small finding is overkill). Confirm.
- **Q8 — Addressee detection threshold (§4.8).** How sure must it be that Jordan is addressing it before acting on
  speech, and how much does vision (gaze/facing) weigh vs audio? Rec: **vision-gated by default — act on speech
  only when he's facing the camera/becky window OR uses its name; otherwise listen-only.** A real-use tuning dial.

## 11. Sources
- becky in-repo: `SPEC-AGENT-HARNESS.md` (the deterministic-shell-around-an-agent pattern, the tool registry +
  default-deny allowlist + audit transcript this reuses), `SPEC-BECKY-ASK.md` (chat front-door + `catalog.go`),
  `SPEC-BECKY-VISION-MODELS.md` + `internal/vision` (the LFM2.5-VL small model the ambient analyst runs on),
  `SPEC-DEEP-RESEARCH.md`/`becky-research` · `SPEC-RADAR.md`/`becky-radar` · `SPEC-SCOUT.md`/`becky-scout` (the
  existing research tools the ambient job orchestrates — §5.4), `internal/ctlagent` · `internal/pirun` ·
  `internal/intent` · `internal/avlm` (Gemma-4) · `internal/tts` (NeuTTS) · `internal/seam` ·
  `internal/undo`/`ctledit` (undo history), `SPEC-BECKY-CANVAS.md` (the HUB the briefing surfaces in),
  `GUI-RULES.md` (the NDJSON engine↔front seam), `CANVAS-NORTH-STAR.md` (the hardware Definition-of-Done),
  `FORENSIC-OUTPUT-PHILOSOPHY.md` (corroborate-then-conclude; no flood of maybes — applied to PROPOSALS in §5.2),
  `research/daw-ai-control-reaper-vs-ableton.md` §4 (the realtime model + FastRTC + cloud/local analysis).
- External (live, 2026-06-23): **HyperFrames** `github.com/heygen-com/hyperframes` (open-source HTML→MP4 video
  renderer built for AI agents — deterministic, FFmpeg-backed; the visual-briefing format §5.3); Mermaid (the
  quick-digest diagram format). **Anthropic loops** — Claude Code `/loop` (clock) + `/goal` (checkable stopping
  condition), shipped June 2026, and **"Effective harnesses for long-running agents"** (anthropic.com/engineering)
  — the §5.6 bounded-run canon. **hermes-agent** `github.com/NousResearch/hermes-agent` (60s cron tick, isolated
  sessions, file-lock, first-class **heartbeat jobs**, `no_agent=True` model-free ticks, hook system) — the §5.6
  heartbeat/hook model. The tiered LFM2.5→Qwen/Gemma escalation reuses becky's existing local models.
- External (live, 2026-06-23): Gemini Live API (duplex audio+video, barge-in, proactive-audio, models incl.
  `gemini-3.1-flash-live-preview`) — Google AI / Vertex docs; **FastRTC** `github.com/gradio-app/fastrtc`
  (v0.0.34, Nov 2025; VAD + turn-taking; Gemini/OpenAI-Realtime/Claude integrations); **Highlight AI** desktop
  companion (local screen/audio capture, task-detection auto-context, Cmd-anywhere hands-free, privacy "nothing
  leaves unless attached") — highlightai.com/assistant + reviews; `VoloBuilds/toaster` (voice→Strudel, the
  realtime-model pattern).
