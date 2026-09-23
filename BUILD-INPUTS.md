# BUILD-INPUTS.md — late-breaking inputs for the native Becky Review build

Standing file. Jordan (or the orchestrator) appends here at any time; every
build-protocol agent MUST read this file fresh at the start of its round. Newest
entries first. These are inputs to evaluate, not orders to blindly follow —
"open source and free does not equal the best implementation" (Jordan). Decide on
evidence and record the decision + reasoning in the spec.

---

## 2026-07-17 — Jordan's product thesis for the AI integration (verbatim, load-bearing)

"we're building a better version [than VideoAgent] because the ai can manipulate the
timeline with me WITHOUT heavy mcp servers and giant tool call / code lists. quick
and simple, with human review optional right on the timeline instead of burning it
all together as an .mp4"

Design implications: the AI's interface to the editor is the SAME lean shared-state
JSON / engine-verb seam the human UI uses — no separate MCP tool surface, no
protocol bloat. AI edits land as ordinary timeline state changes Jordan can see,
tweak, or ignore, live. Review-on-the-timeline is the default posture; rendering is
the last step, never the medium of collaboration.

## 2026-07-17 — Free-fleet research docs (read these when they appear)

Three non-Anthropic agents are producing evidence files in ${'X:\\AI-2\\becky-tools\\research\\'}:
- research\mediaeditor-evaluation.md (GLM 5.2 — source-level separability/decode/cache verdict)
- research\velo-logic-mining.md (Qwen — the coalescing/lanes/unified-model patterns, concretely)
- research\videoagent-integration.md (Hy3 — intent→workflow graph mapped onto our engine verbs)
Treat them as pre-research evidence for the spec; verify load-bearing claims.

## 2026-07-17 — Two OSS editors to use in whole, in part, as logic, or as comparison

Jordan: "if it makes sense to use components or parts or even logic - do it. if we
can build it BETTER - we will (ours is AI first - the shared state json stuff
matters), but these might be useful? ... We tried Shotcut (c++ and python) and it's
timeline was terrible ... i'll let you and the agents decide."

### 1. https://github.com/opencodewin/MediaEditor
Pre-scouted facts (verified 2026-07-17): C++ NLE built on **Dear ImGui** (custom fork
at opencodewin/imgui, SDL2+OpenGL3), LGPLv3, 2,562 commits, multi-track timeline with
waveforms/snapping/thumbnails/frame-step, 45+ filters via a node-graph "blueprint"
system, decode via their **MediaCore** abstraction (H.264/265/VP9/ProRes, hardware
support claimed). **Project SUSPENDED Dec 2024** (funding).
Evaluation angles: this is our exact chosen stack with a mature timeline widget —
strongest fork/mine candidate on the table. Verify against the bake-off standards:
real NVDEC-on-Windows behavior (Shotcut died on this), timeline latency at 2000-5000
clips, whether their custom imgui fork is separable from ours, and split-cost (is
their peak/proxy cache per-source?). Suspended = we own maintenance of whatever we
take; prefer extracting the timeline widget + MediaCore patterns over adopting the
whole app. LGPLv3 is fine for an internal tool.

### 2. https://github.com/notune/velo
Pre-scouted facts: C++20 **Qt 6** NLE, GPLv3, v0.0.1 (June 2026), single contributor.
Premiere-style layout, unlimited tracks, keyframes, razor/ripple, per-track controls.
Evaluation angles: wrong UI stack for us (Qt, and Jordan's Shotcut/Qt timeline
experience was bad) — but its internal LOGIC maps 1:1 onto feedback9's failures and
is worth mining as *design*, not code: (a) decoder "lanes" caching per position,
(b) **coalescing preview system that prevents stale frame queueing during scrubbing**
(directly addresses the rapid-split pile-up), (c) one unified model shared by GUI /
preview thread / audio callback / exporter. GPLv3: mine the ideas, re-implement —
do not copy code into our tree unless we accept GPL for the app.

### Standing decision rule
For each candidate: adopt whole / extract component / re-implement logic / reject —
with measured or cited evidence, recorded in the BUILD doc. Our differentiator stays:
AI-first (shared-state JSON seam, engine verbs, agents as first-class operators).
