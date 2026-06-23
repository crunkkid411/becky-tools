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
- **PROACTIVE (the Highlight half — the real new work).** A customizable **watcher** ambient-samples context
  (screen, active window, audio, becky tool events) and, on a rule firing, **proposes or acts**: "you just
  recorded a take — trim it?" / auto-attaches the right screenshot/window to a query (Highlight's "task
  detection") instead of you fetching context. *Proactive-on-reads, propose-on-actions* (§4).

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
7. **VISIBLE context, never a silent attach (the specific Highlight bug Jordan hit).** Highlight would attach
   screenshots/browser tabs *invisibly, before he hit enter* — he never knew what was being shared. That is the
   banned anti-pattern. Rule: **every piece of context the watcher stages is shown in a visible tray BEFORE the
   request is sent** (thumbnail of the screenshot, the named window/tab, the take) — Jordan can **strip any item
   with one tap/word**, and **nothing leaves the machine until he sees it staged and confirms** (this is the
   teeth behind §4.3's "nothing leaves unless attached"). No context is ever attached as a side effect of typing
   or speaking; staging and sending are two visible, separable steps. This is becky's "show me, don't do it"
   overlay (`CANVAS-INSPIRATION.md`) applied to context-gathering: the agent **shows** what it would attach, it
   does not silently do it.

---

## 5. The proactive watcher (the Highlight half) — customizable, forensic-safe

Models the *good* Highlight behavior Jordan named, under §4's rules:

- **Context sources (each toggleable in the rules file):** active-window title/app, an on-demand screen
  capture, audio loopback (what's playing), clipboard, and — uniquely becky — **becky tool events**
  (`cmd/events`/`internal/seam`): "a REAPER take just finished recording," "a pipeline stage emitted a finding."
- **The "decide what's relevant" step (Highlight's task-detection, made yours):** when you ask something, the
  watcher auto-assembles the *right* context (the screenshot of the window you're looking at, the take you just
  cut) instead of you fetching it — but only GREEN context-gathering, and it is **staged into the visible
  context tray (§4.7), not silently attached.** Jordan sees the thumbnail/named tab and can strip it before
  anything is sent; nothing leaves the machine until he confirms the staged set. (This is the direct fix for the
  Highlight bug where it shared screenshots/tabs before he hit enter and he never knew.)
- **The proactive trigger model:** `notice (an event/context pattern) → check the rule → if GREEN, do it and
  mention it; if YELLOW/RED, propose by voice and wait.` Example: take finishes → onset analysis (GREEN) →
  "that take has 1.2s of dead air up front, want me to trim + fade it?" → YELLOW confirm → trim via the REAPER
  bridge → re-verify (the research doc's closed loop). This is the "proactive software, not a chatbot" goal made
  concrete, and it reuses the habit-learning trim/fade loop from the research doc, not new musical logic.

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
3. `cmd/becky-voice/` — the driver: NDJSON seam in (faked realtime events), route via `internal/catalog` +
   `internal/intent`, enforce `internal/voicerules`, shell the existing front-doors, write the audit transcript,
   emit spoken-reply text. A **`--selftest`** one-command offline proof (feed a scripted NDJSON intent stream →
   assert the correct front-door command + tier decision + transcript, **runs no model, no mic**).
4. `internal/seam` reuse for the proactive event source; a faked event feed for tests.
5. Gates 1–4 green on Ubuntu+Windows (`go build/vet/test ./...`, `gofmt`); push to the `claude/*` branch; draft PR.

**Local agent (Jordan's Win10 PC — realtime model + hardware):**
1. Stand up **FastRTC** beside `pyhelpers`; wire the mic/cam/screen capture + the NDJSON seam to `cmd/becky-voice`.
2. Wire the realtime model both ways: **Gemini Live** (API) and **local Gemma-4 + NeuTTS**; confirm barge-in +
   no-wake-word feel; confirm the local path degrades gracefully.
3. With Jordan, define + wire the **whoretana-specific** verbs (§7) onto the agent loop under the tiers.
4. The hardware Definition-of-Done (per `CANVAS-NORTH-STAR.md`): you talk → it hears with no wake word → it does
   a GREEN action unprompted once → it *proposes* a YELLOW one and waits → a RED one is refused without explicit
   ok → the kill phrase instantly stops it. Tune the watcher so it helps without nagging. Report back.

---

## 9. Composition — no new orchestrator, no new tool-that-works
- `becky-voice` **calls** `becky` / `becky-ask` / `becky-harness` / the REAPER bridge / Strudel; it implements
  none of them. It is the realtime *skin* + *rules* only.
- `becky-ask` stays the chat front-door; `becky-voice` is what makes it hands-free + proactive. `becky-harness`
  stays the per-request grinder; `becky-voice` can *launch* one by voice ("for every clip in this folder…").
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

## 11. Sources
- becky in-repo: `SPEC-AGENT-HARNESS.md` (the deterministic-shell-around-an-agent pattern, the tool registry +
  default-deny allowlist + audit transcript this reuses), `SPEC-BECKY-ASK.md` (chat front-door + `catalog.go`),
  `internal/ctlagent` · `internal/pirun` · `internal/intent` · `internal/avlm` (Gemma-4) · `internal/tts`
  (NeuTTS) · `internal/seam` · `internal/undo`/`ctledit` (undo history), `GUI-RULES.md` (the NDJSON engine↔front
  seam), `CANVAS-NORTH-STAR.md` (the hardware Definition-of-Done), `FORENSIC-OUTPUT-PHILOSOPHY.md` (no flood of
  maybes), `research/daw-ai-control-reaper-vs-ableton.md` §4 (the realtime model + FastRTC + cloud/local analysis).
- External (live, 2026-06-23): Gemini Live API (duplex audio+video, barge-in, proactive-audio, models incl.
  `gemini-3.1-flash-live-preview`) — Google AI / Vertex docs; **FastRTC** `github.com/gradio-app/fastrtc`
  (v0.0.34, Nov 2025; VAD + turn-taking; Gemini/OpenAI-Realtime/Claude integrations); **Highlight AI** desktop
  companion (local screen/audio capture, task-detection auto-context, Cmd-anywhere hands-free, privacy "nothing
  leaves unless attached") — highlightai.com/assistant + reviews; `VoloBuilds/toaster` (voice→Strudel, the
  realtime-model pattern).
