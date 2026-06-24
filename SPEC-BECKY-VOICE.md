# SPEC — `becky-voice` — the always-on, proactive voice + context front-end for the whole becky-tools suite

> ## STATUS — SPEC, NOT BUILT, AWAITING JORDAN'S GO/NO-GO
> **▶ The step-by-step build lives in `HANDOFF-BECKY-VOICE.md`** (ordered, checkboxed, WHAT·HOW·WHY·VERIFY·DONE,
> with the builder-discipline rules). This spec is the *why + nuance*; that file is the *do-this-then-this* a
> subagent executes to completion. Read both.
>
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

### 1.1 `becky-voice` IS "becky-whoretana" — ONE agent, pluggable bridges, per-context tool PROFILES
Jordan's framing (2026-06-23), and it's the right one: rather than rebuild a new agent for REAPER, then another
for the NLE, then another for the next bridge, there is **ONE lightweight agent — the same floating whoretana
GUI Jordan already uses — that plugs into whatever he's working in.** `becky-voice` and "becky-whoretana" are
the same thing from two directions: the realtime voice shell + rules + router (this spec) *is* the reusable
whoretana base. New bridges (REAPER today, others later) are **tool packs it loads**, not new agents.

**The critical nuance (Jordan's own worry, and he's right to have it): never hand a small local model a
gazillion tools at once.** The fix is **per-context tool PROFILES, not clone-per-program.** The agent is one;
the *toolset is scoped to the active context*: in REAPER it loads the REAPER-bridge profile (~a dozen verbs); in
the NLE, the edit profile; idle, a tiny default. This is **exactly** the declared-allowlist mechanism
`becky-harness` already uses (§3.2) — a profile is just a saved allowlist + tier map. So:
- **Don't** clone whoretana per program (that fragments the thing that should stay one).
- **Don't** expose all ~74 tools to the model at once (tool overload, especially on the local 4B).
- **Do** keep one agent + swap a small, context-scoped tool profile as the active program changes.
This keeps becky-whoretana lightweight *and* universal, and it's the same single-tool discipline everywhere.

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

### 3.3 Workflows & tool packs — DECLARATIVE files Jordan controls, not hardcoded Go (the "run it like this when I ask for X" fix)
This is the thing that's currently *missing* and frustrates Jordan: today the "transcribe" workflow is
**hardcoded in `cmd/ask/workflow.go`** and runs `transcribe,diarize,events,osint,ocr` **unconditionally** — so
diarize fires even on a one-speaker clip. There is no file he can point at to say "run it like *this* when I ask
for X." becky-voice fixes that with two declarative file types he can read and edit:
- **A workflow recipe** (`workflows/<name>.json`) = a **named, ordered list of steps with optional conditions**,
  triggered by **phrases**. Steps call existing tools; conditions are **deterministic** (no model) over facts
  earlier steps produced. Example — `process-video` skips diarize for one speaker:
  ```jsonc
  { "name": "process-video", "phrases": ["process this video", "do the usual"],
    "steps": [ { "tool": "becky-transcribe" },
               { "tool": "becky-diarize", "when": "speakers > 1" },     // conditional — no wasted work
               { "tool": "becky-ocr" },
               { "verb": "verify-with-gemma4", "when": "speakers > 1" },
               { "merge": "transcript" } ] }
  ```
- **A tool pack** (`packs/<name>.json`) = a saved **allowlist + tier overrides** scoped to a context (REAPER,
  forensic, default). The active pack is the *only* toolset the model sees — this is the anti-overload guard
  (§1.1) and reuses `becky-harness`'s allowlist mechanism.
**The point:** "run transcribe + diarize + gemma4-check, but skip diarize if one speaker" becomes a **recipe
file**, not a sentence Jordan has to re-explain to an agent every time. Deterministic, repeatable, editable by
him. Build details: `HANDOFF-BECKY-VOICE.md` Phase 0 Steps 0.2 (recipes) + 2.1 (packs).

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

### 5.6 The voice-response model — PRE-AUTHORED, deterministic, per-tool/per-outcome (the whoretana `speak_error` pattern)
This is the heart of what makes whoretana feel instant and *not* like a chatbot, and it is pure becky
determinism applied to speech. In whoretana, every tool has **pre-written things to say** for each outcome —
`speak_error(tool, err) → "Ah shit, {tool} encountered an error. {short}"`, and different lines for success /
partial / "ran out of clicks." It **swears and names the broken tool, full stop** — it does NOT say *"I'm
having a problem, here's what we could do, I can deploy my coding agent if…"* That verbose hedging is the
banned anti-pattern (it's the §5.2 anti-bullshit rule applied to *responses*, not just proposals). Then a bare
*"fix it"* is understood to mean **deploy the coding agent** — no menu, no narration.

becky models this as a **deterministic response map** that ships *with each tool/profile*:
```jsonc
// part of a tool profile — pre-authored, becky-deterministic, NO model needed to choose the line
"becky-identify": {
  "ok":      ["Got 'em.", "Found your guy.", "Done — IDs are in."],          // round-robin (anti-stale)
  "partial": ["Eh, only got a couple. Want me to dig harder?"],
  "error":   ["Ah shit, identify broke. {short}"],                            // {short} = first 120 chars
  "fix_verb": "becky-new-tool"                                                 // what "fix it" means here
}
```
- **Deterministic by default:** the line is *chosen by code* (round-robin/seeded), not generated — so it's
  instant, predictable, and Jordan authored it. The realtime model is only invoked when a response must be
  *dynamic* (it riffs on the error detail — which, as Jordan notes, Gemini already does and it's part of the
  fun). Determinism is the floor; the model's riff is a flavor option, never required.
- **Persona-owned:** the lines live in a per-persona file (whoretana's voice), editable like
  `becky-voice.rules.json`. This is the "**trace dataset**" idea Jordan named — a script the persona reads and
  optionally interprets, except here the *script is authored by Jordan* and the interpretation is bounded.
- **Jordan does NOT author it from scratch — it's GENERATED, then he replaces strings (his explicit ask).**
  Jordan's worry: *"with a trace dataset I have no idea how to create one, what tool-call responses we need."* He
  doesn't have to. The catalog **defines the outcomes** (every tool × `ok`/`partial`/`error`), so a generator
  emits a **pre-filled `responses.json`** with a sensible default line for each — and Jordan edits by
  **replacing the default strings**, exactly like he did in the whoretana Python (`if the tool fails, say
  "blabla"`). Same five-minute experience, now data-driven. (Build: `HANDOFF-BECKY-VOICE.md` Step 0.3.)
- **One word to two sentences.** Almost every response is tiny — which is what makes §5.7 (pre-rendered audio)
  a clean win.

### 5.7 Tiered audio — pre-rendered cache (instant) → local TTS (dynamic) → realtime model (conversation)
Jordan's instinct is correct and it's a real latency win: **for the fixed, tiny set of canned phrases, render
them to audio ONCE and just play the file** — playing a 3-second clip is ~0 ms and ~0 VRAM vs. generating it
every time. So becky uses three tiers, cheapest first:
1. **Pre-rendered clips (default for canned lines).** Each response-map line (§5.6) is rendered once by NeuTTS
   into a small `.mp3`/`.wav`, with **round-robin variants per outcome** so it never goes stale, plus optional
   **sound-effect stings**. Instant playback, deterministic, no GPU. This covers the majority of whoretana's
   talking.
2. **Live NeuTTS (for dynamic text).** When a line has a `{slot}` (the actual error string, a count, a name) or
   is otherwise novel, NeuTTS generates it on the fly. Still local, still fast.
3. **Realtime model (conversation / mind-dump).** Full Gemini Live (or the local E2E model, §6) only for actual
   back-and-forth — the "intake / mind-dump" mode Jordan described. The expensive tier is used least.
A tiny **cache builder** re-renders tier-1 whenever the response map changes (a deterministic offline step,
testable: assert every canned line has ≥1 rendered clip).

### 5.8 The background-agent harness — how the "little guy" actually runs (this is the part to dial in)
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

## 6. Cloud vs local — one transport, three model options (updates the research doc's local caveat)
> **PIN (Jordan, 2026-06-23): v1 uses GEMINI 2.5 FLASH REALTIME ONLY.** Wire and prove the whole system on the
> exact model whoretana already runs (`gemini-2.5-flash-native-audio`) FIRST — so if something doesn't work, we
> *know* it's the wiring, not the model. The local options below (LFM2.5-Audio, Gemma-4) are **Phase 4, after v1
> works on real use.** Do not start local. The transport (FastRTC) + the deterministic Go half are model-agnostic,
> so swapping later is a config change, not a rebuild.

Three brains on the one FastRTC transport, chosen per-context by the rules file (§4.6):
- **Cloud (Gemini Live):** true low-latency duplex + barge-in + video now; for non-sensitive work; Jordan is fine
  with API calls here. This is what whoretana already runs (`gemini-2.5-flash-native-audio`, per his `main.py`).
- **Local E2E speech-to-speech — `LiquidAI/LFM2.5-Audio-1.5B` (the duplex answer I was missing).** A true
  **end-to-end audio→audio** model (no separate ASR/TTS), 1.5B (1.2B LM + 115M audio encoder), **built for
  low-latency real-time conversation**, GGUF/llama.cpp + ONNX, ~32k ctx, English-only, LFM Open License. This
  **directly softens the research-doc caveat** that "local can't match native-audio duplex" — LFM2.5-Audio *is*
  native local duplex. Trade-offs to go in eyes-open: (a) **less reasoning** than Gemma-4 (it's 1.5B) — fine for
  whoretana's *talk + route to tools* job, not for hard analysis; (b) **function-calling isn't documented** on
  the base model — so pair it with becky's **deterministic tool router** (§3.1) and/or the community
  **tool-aware fine-tune** (`matbee/lfm2.5-audio-tool-aware-v4.1`) rather than trusting it to free-form
  tool-call; (c) **no vision** — when a turn needs eyes, hand off to Gemma-4 / LFM2.5-VL (§5.4). VRAM is a
  fraction of even Gemma-4 E2B (Jordan's estimate: ~25×) so it's cheap to keep warm always-on.
- **Local chained (fallback) — Gemma-4 QAT in + NeuTTS out:** when you want Gemma-4's stronger reasoning locally
  and accept non-duplex turn-taking; FastRTC supplies VAD/turn-taking. Use when reasoning > latency.
**Recommended local default:** **LFM2.5-Audio for the conversational/whoretana voice** (best feel, tiny), with
**Gemma-4 only pulled in for vision or harder reasoning** — exactly Jordan's "run whoretana on the small speech
model except when vision is needed." Sensitive forensic material → local only, always.

### 6.1 Standby & cost — NEVER stream to the cloud on idle (answering Jordan's billing worry)
Jordan's worry: *"if it's 'on' but I don't use it for an hour, is that still making significant API calls?"* The
honest mechanics:
- **Gemini Live bills per second of audio you actually SEND, not for the connection being open** (~25 tokens/sec
  ≈ **$0.037/min ≈ ~$2.20/hour of *continuous* audio**, 2.5 Flash). An open WebSocket with **no media streamed**
  costs nothing token-wise (the cost is the audio, not the socket).
- **So Jordan's "ambient, doesn't shoot up" assumption is only true IF we gate the mic.** Today whoretana streams
  the mic continuously whenever it isn't muted (`main.py` sends PCM on every callback while unmuted) — that means
  it **is** billing ~$2/hr even sitting idle. That's the gap in the mental model, and it's the thing to fix.
- **The fix is exactly Jordan's instinct (gate it).** A **local, on-device VAD** (FastRTC/Silero — **$0**, never
  leaves the machine) runs always and decides *when* there's real speech; audio is **only forwarded to Gemini
  when a gate fires** (push-to-talk, **push-to-DISABLE** — default-open, tap to mute when others are around, like
  whoretana's F4 — a wake gesture, or §4.8 addressee detection). Result: **idle standby ≈ $0** (just local
  listening), a quick question ≈ a few cents.
- **This also kills the "launching whoretana is a momentum killer" problem:** the floating GUI stays **always
  resident** with the free local-VAD standby, so it's *instantly there* — no launch — but the **metered cloud
  pipe only opens on a real turn.** Always-available without always-billing.
- **Show the meter.** A tiny running token/cost indicator in the GUI (rules-file budget cap optional) so cost is
  never a mystery. And the **Phase-4 local LFM2.5-Audio path makes idle standby truly $0** (on-device), which is
  the long-run answer for "leave it on all day."
- **Free tier:** the free Gemini tiers (2.0 Flash) are deprecating and the 2.5 native-audio Live model is paid —
  so treat cloud audio as **metered** and design to minimize it via gating; don't rely on a free allowance.

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
6. `internal/voiceresp/` — the pre-authored **response map** (§5.6) + the **pre-rendered audio cache builder**
   (§5.7): choose a line by code (round-robin/seeded) per tool×outcome; render canned lines to clips; resolve a
   `{slot}` to live NeuTTS. Pure Go, value-asserted: every canned outcome has ≥1 clip, round-robin cycles
   without repeat, a bare `fix` maps to the tool's declared `fix_verb`, and **no model is needed to pick a line**.
7. `internal/heartbeat/` — the harness (§5.8): an interval scheduler with **`no_agent` deterministic triggers**
   (cheap, model-free), a **`/goal`-bounded** scout step, the **tiered escalation ladder** (LFM2.5 → Qwen/Gemma,
   each tier's "what next" protocol from the rules file), and the **hook table** (reroute before/after a step).
   Pure Go + faked models; value-asserted: a `no_agent` tick burns no model, a scout run stops at its goal
   condition, a candidate escalates exactly one tier, a hook reroutes a case-material finding to local-only.
8. `internal/seam` reuse for the proactive event source; a faked event feed for tests.
9. Gates green on Ubuntu+Windows (`go build/vet/test ./...`, `gofmt`); push to the `claude/*` branch; draft PR.

**Local agent (Jordan's Win10 PC — realtime model + hardware):**
1. Stand up **FastRTC** beside `pyhelpers`; wire the mic/cam/screen capture + the NDJSON seam to `cmd/becky-voice`.
2. Wire the realtime model three ways (§6): **Gemini Live** (API), **local `LFM2.5-Audio-1.5B`** (the E2E
   speech↔speech default for whoretana — GGUF, evaluate the tool-aware fine-tune + the cookbook), and **Gemma-4
   + NeuTTS** (vision / harder reasoning). Confirm barge-in + no-wake-word feel; confirm graceful degrade and the
   LFM2.5-Audio→Gemma-4 hand-off when a turn needs vision.
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
  — the §5.8 bounded-run canon. **hermes-agent** `github.com/NousResearch/hermes-agent` (60s cron tick, isolated
  sessions, file-lock, first-class **heartbeat jobs**, `no_agent=True` model-free ticks, hook system) — the §5.8
  heartbeat/hook model. The tiered LFM2.5→Qwen/Gemma escalation reuses becky's existing local models.
- External (live, 2026-06-23): Gemini Live API (duplex audio+video, barge-in, proactive-audio, models incl.
  `gemini-3.1-flash-live-preview`) — Google AI / Vertex docs; **FastRTC** `github.com/gradio-app/fastrtc`
  (v0.0.34, Nov 2025; VAD + turn-taking; Gemini/OpenAI-Realtime/Claude integrations); **Highlight AI** desktop
  companion (local screen/audio capture, task-detection auto-context, Cmd-anywhere hands-free, privacy "nothing
  leaves unless attached") — highlightai.com/assistant + reviews; `VoloBuilds/toaster` (voice→Strudel, the
  realtime-model pattern); **`LiquidAI/LFM2.5-Audio-1.5B`** (HF — end-to-end speech↔speech, 1.5B, GGUF/ONNX,
  low-latency real-time, English-only, LFM Open License; the local-duplex option §6) + the voice-assistant
  cookbook `github.com/Liquid4All/cookbook` + community tool-aware fine-tune `matbee/lfm2.5-audio-tool-aware-v4.1`.
- Jordan's reference implementation: **whoretana** (his fork of `FatihMakes/Mark-XXXV`/Jarvis) — `main.py`
  (Gemini 2.5 Flash native-audio Live, the `speak_error` per-tool spoken-response pattern §5.6, F4 mute, tool
  registry) shared 2026-06-23 as the experience becky-voice generalizes. (Repo not in-tree; the uploaded
  `main.py`/README are the grounding.)
