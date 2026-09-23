# SPEC-BECKY-ITERATIVE-VIDEO-LOOP.md — Iterative AI-Driven Video Editing Loop on VEGAS Pro Timeline

> **STATUS: SPEC PHASE** — Architecture only. No implementation yet. This spec defines WHAT the loop is and WHERE the boundaries are. HOW is for the design phase.

---

## 0. TL;DR — The Problem and the Shape

**Current state:** `becky-roughcut` and `becky-clip` are **one-shot** tools. They run detection once, emit a `vegas_cut.json`, and optionally launch VEGAS to assemble it. There is no way for an AI agent to:
- Read the *current* VEGAS timeline state after a human edit
- Propose a change, apply it, verify the result, and iterate
- Run the existing LLM review passes (watch, triage, narrative trim) **on the live timeline** mid-session

**What this spec adds:** A **long-running control loop** that connects an AI agent to the *live* VEGAS Pro 18 timeline via the existing `BeckyVegas` named-pipe bridge. The agent can:
1. **Read** the full timeline state (`becky-vegas timeline`)
2. **Propose** a batch of edits (add/remove/trim/reorder/split/marker) via a structured action schema
3. **Apply** them atomically to VEGAS (single undo step)
4. **Verify** the result (read back + optional LLM watch pass)
5. **Iterate** until the cut is acceptable — or hand back to Jordan

This turns the existing bridge into a **shared-state editing session** where the AI acts as a junior editor: it drives the timeline, runs the existing LLM passes as needed, and produces an audit log of every decision.

---

## 1. User Stories

> Max 7 stories. Each has single responsibility.

- **US-1**: As a **detective/editor (Jordan)**, I want an AI agent to **read my live VEGAS timeline** so that it knows what I've already cut without me exporting anything.
- **US-2**: As a **detective/editor**, I want the AI to **propose and apply a batch of edits** (cut silence, add markers, reorder clips, trim ends) **as one undoable step** so that I can accept or reject the whole batch with one Ctrl+Z.
- **US-3**: As a **detective/editor**, I want the AI to **run the existing LLM review passes** (watchpass, triage, narrative trim) **on the current timeline** mid-session, not just on a one-shot artifact, so that flags appear on my timeline in real time.
- **US-4**: As an **AI agent (Claude Code / local model)**, I want a **structured action schema** (default-deny allowlist) to drive VEGAS so that I cannot execute arbitrary commands — only the verbs the bridge exposes.
- **US-5**: As a **detective/editor**, I want the loop to **produce an audit log** of every AI proposal, application, and verification result so that I can review what the AI did and why.
- **US-6**: As a **detective/editor**, I want to **pause/resume/stop** the loop at any time and **resume later** with the same session state so that I don't lose context across breaks.
- **US-7**: As a **detective/editor**, I want the loop to **degrade gracefully** when VEGAS is busy (rendering, dialog open) or the bridge is unavailable so that my timeline is never corrupted and I never lose work.

---

## 2. Acceptance Criteria

> 2-5 criteria per story. All verifiable — no subjective language.

### US-1: Read Live Timeline
- GIVEN VEGAS Pro 18 is running with `BeckyVegas` extension loaded
- WHEN the loop starts (or agent calls `status`)
- THEN the agent receives a JSON object containing: `project_path`, `cursor_seconds`, `selection_start`, `selection_length`, `tracks[]`, `events[]` (each with `source`, `in`, `out`, `timeline`, `track`, `rate`, `selected`, `grouped`), `markers[]`, `regions[]`, `rendering_flag`, `playing_flag`
- AND the response latency is < 2 seconds

### US-2: Propose & Apply Batch Edits
- GIVEN the agent has read the timeline
- WHEN the agent emits a `Proposal` containing a list of `EditOp` verbs from the allowlist (see §4)
- THEN the bridge applies all ops in a single `UndoBlock` (one Ctrl+Z reverts all)
- AND the reply contains the new `TimelineView` (clips, duration, cursor) reflecting the changes
- AND if VEGAS is rendering or showing a dialog, the bridge refuses with a clear error naming the dialog and its message — no partial edit is committed

### US-3: Run LLM Review Passes on Live Timeline
- GIVEN the loop is running and the timeline has content
- WHEN the agent (or Jordan via a hotkey) triggers `watch_pass`, `triage_markers`, or `narrative_trim`
- THEN the pass reads the **current** `vegas_cut.json` artifact (re-generated from live timeline via `ProjectOps.TimelineDoc`), runs the existing LLM logic (Gemma-4 via llama-server), and writes its report (`watch_report.json`, `marker_triage.json`, `narrative_trim.json`)
- AND any resolved markers are **removed from the live VEGAS timeline** via the bridge; any kept markers are **annotated in place** on the live timeline
- AND narrative trim cuts are applied to the live timeline as a single undoable batch (quotes never cut, markers/regions reflowed)

### US-4: Structured Action Schema (Default-Deny)
- GIVEN the agent wants to act on the timeline
- THEN the ONLY verbs it can invoke are the bridge's exposed commands (see §4.1) — no raw `run_script`, no `command`, no `invokeCommand` from the agent
- AND every verb validates its args (types, required fields) and returns a structured reply (`{ok, data, error}`)
- AND the agent's proposals are **propose-then-apply**: the agent returns a `Proposal{ actions[], preview_text }`; the UI (or a confirmation step) shows the preview; **nothing mutates until approved**

### US-5: Audit Log
- GIVEN the loop runs for a session
- THEN a session log file is written (`%LOCALAPPDATA%\BeckyVegas\iterative_sessions\<session_id>.jsonl`) containing one NDJSON line per step:
  - `{"step": N, "type": "proposal", "actions": [...], "preview": "..."}`
  - `{"step": N, "type": "apply", "result": {...}, "verification": {...}}`
  - `{"step": N, "type": "watch_pass", "report_path": "...", "flags": N}`
  - `{"step": N, "type": "triage", "resolved": N, "kept": N}`
  - `{"step": N, "type": "narrative_trim", "before_sec": X, "after_sec": Y, "cuts": N}`
- AND the log is append-only; a crashed session can be reconstructed by replaying the log

### US-6: Pause/Resume Session State
- GIVEN the loop is paused (Jordan closes the control window or sends `pause`)
- THEN the session state (open project path, timeline snapshot, pending proposals, LLM pass history) is saved to `%LOCALAPPDATA%\BeckyVegas\iterative_sessions\<session_id>.state.json`
- WHEN the loop is resumed with the same `session_id`
- THEN the agent reconnects to the same VEGAS PID (if still running) or the newest VEGAS with the same project, and the timeline state matches the saved snapshot (within tolerance of human edits made while paused)

### US-7: Graceful Degradation
- GIVEN VEGAS is rendering (`rendering=true` from `RenderStarted` event)
- WHEN the agent proposes an edit
- THEN the bridge returns `ok=false, error="VEGAS is rendering - wait until the render finishes"` — **no edit attempted**
- GIVEN VEGAS shows a modal dialog (`OpenDialogs()` returns non-empty)
- WHEN the agent proposes an edit
- THEN the bridge returns `ok=false, error="VEGAS is showing a dialog: 'Save Project?' - it has to be answered first."` — **no edit attempted**
- GIVEN the named pipe connection fails (VEGAS crashed, extension unloaded)
- WHEN the agent calls any verb
- THEN the bridge returns `ok=false, error="cannot open pipe: VEGAS not reachable"` — agent logs and stops, **never crashes VEGAS**

---

## 3. Out of Scope

> Minimum 3 items. Each states WHY it is excluded.

This feature does NOT:
- [ ] **Replace `becky-roughcut` or `becky-clip`** — they remain the one-shot "folder → rough cut" and "case folder → forensic compilation" pipelines. This loop is for **iterative refinement on an already-open VEGAS timeline**.
- [ ] **Build a new GUI** — the control surface is the existing `BeckyVegas` bridge + `becky-vegas.exe` CLI. A future GUI (e.g., a "Becky Loop" panel in VEGAS) is a separate spec.
- [ ] **Autonomous overnight editing without human checkpoints** — every batch requires explicit approval (propose-then-apply). Fully autonomous mode is a future capability behind a hard gate.
- [ ] **Multi-track compositing / transitions / effects** — the bridge only exposes timeline mutations that VEGAS's scripting API supports cleanly (add/remove/trim/reorder/split/marker/region). Advanced NLE features are out of scope.
- [ ] **Real-time video preview streaming** — the bridge has `snapshot` (single frame). A live preview stream would require a separate video sink and is high effort for low immediate value.
- [ ] **Cross-project / multi-VEGAS coordination** — one loop instance = one VEGAS PID = one project. Multi-window orchestration is a separate spec.

---

## 4. Architecture — The Loop Components

### 4.1 The Control Plane (Already Exists)
| Component | Role | Location |
|-----------|------|----------|
| **Named Pipe** | `\\.\pipe\becky-vegas-<pid>` — one JSON line in, one out | `vegas/BeckyVegas/Bridge.cs` |
| **Instance Registry** | `%LOCALAPPDATA%\BeckyVegas\instances\<pid>.json` — PID, pipe name, project path, last focus | `vegas/BeckyVegas/Presence.cs` |
| **Bridge Commands** | `status`, `timeline`, `selected`, `jump`, `insert`, `add_marker`, `add_region`, `run_script`, `snapshot`, `search`, `dialogs`, `play/stop/pause`, `transcribe` | `Bridge.cs:Dispatch()` |
| **Safety Guards** | Refuses edits while `rendering=true` or dialog open; every edit = one `UndoBlock` | `Bridge.cs:Guarded()`, `UiThread.cs` |

**Gap for this spec:** The bridge currently has **no batch edit verb**. It has `insert` (one clip), `add_marker`, `run_script` (whole script). The loop needs a new verb: `apply_edit_batch` (mirroring `becky-clip`'s `apply_edit_batch`).

### 4.2 The Loop Agent (New)
A **long-running process** (could be a `becky-vegas-loop` Go binary, or a Claude Code session with the `becky-vegas` CLI) that:
1. **Connects** to the bridge (resolves PID via instance registry)
2. **Reads** initial timeline state
3. **Enters loop**: propose → confirm → apply → verify → log → repeat
4. **Exposes** a simple protocol over stdin/stdout (like `becky-clip`'s `cmdBridge`) so any agent can drive it

**Proposed wire protocol (NDJSON over stdio):**
```json
// Agent → Loop (request)
{"id": "r1", "verb": "read_timeline", "args": {}}

// Loop → Agent (reply)
{"id": "r1", "reply": {"ok": true, "data": {...timeline...}}}

// Agent → Loop (proposal)
{"id": "r2", "verb": "propose", "args": {"actions": [...], "preview": "Cut 3 silent gaps on track 1"}}
// Loop → Agent (proposal acknowledged, awaiting confirmation)
{"id": "r2", "reply": {"ok": true, "data": {"proposal_id": "p1", "preview": "..."}}}

// Agent → Loop (confirm)
{"id": "r3", "verb": "apply", "args": {"proposal_id": "p1"}}
// Loop → Agent (applied + verification)
{"id": "r3", "reply": {"ok": true, "data": {"timeline": {...}, "verification": {...}}}}
```

### 4.3 Edit Operation Verbs (Allowlist)
The `apply_edit_batch` verb (new in bridge) and the loop's `propose` verb accept a list of these ops. Each maps to an existing bridge command or a composite:

| Op Verb | Bridge Command(s) | Args | Effect |
|---------|-------------------|------|--------|
| `add_clip` | `insert` | `source`, `in`, `out`, `at?` | Adds clip to "Becky Pulls" tracks at `at` (default cursor) |
| `remove_clip` | `run_script` (BeckyReviewTimeline-style removal) | `source`, `tl_start`, `tl_end` | Removes event(s) matching source+timerange; ripples |
| `trim_clip` | `run_script` (custom trim script) | `source`, `tl_start`, `new_in`, `new_out` | Adjusts in/out of a specific event |
| `reorder_clip` | `run_script` (custom reorder) | `source`, `tl_start`, `new_tl` | Moves event to new timeline position |
| `split_clip` | `run_script` (custom split) | `source`, `tl_start`, `split_at_source_t` | Splits one event into two at source time |
| `add_marker` | `add_marker` | `seconds`, `label` | Adds marker at timeline position |
| `add_region` | `add_region` | `start`, `end`, `label` | Adds region spanning timeline range |
| `set_label` | `run_script` (custom) | `source`, `tl_start`, `new_label` | Updates event label (for region/quote naming) |

**Note:** Complex ops (trim, reorder, split, set_label) currently require `run_script` with a custom C# script. The bridge could expose dedicated verbs for these in a follow-up, but `run_script` with a pre-written script is acceptable for v1.

### 4.4 LLM Review Pass Integration (Existing → Live)
The three existing standalone passes become **loop-internal capabilities**:

| Pass | Current Trigger | Loop Integration | Effect on Live Timeline |
|------|-----------------|------------------|-------------------------|
| **watchpass** (`--watch`) | `becky-roughcut --watch` | Loop verb `run_watch_pass` | Adds `FLAG` markers on live timeline for flagged blocks |
| **triage** (`--triage-markers`) | `becky-roughcut --triage-markers` | Loop verb `run_triage` | **Removes** resolved markers from live timeline; **annotates** kept markers with `[gemma4: ...]` |
| **narrative_trim** (`--narrative-trim`) | `becky-roughcut --narrative-trim` | Loop verb `run_narrative_trim` | **Applies cuts** to live timeline as single batch; reflows quotes/markers/regions |

**Implementation:** The loop re-generates `vegas_cut.json` from the live timeline (`ProjectOps.TimelineDoc` + current quotes/markers/regions), runs the existing pass logic (which reads/writes `vegas_cut.json`), then applies the delta back to VEGAS via `apply_edit_batch`.

### 4.5 Session State & Persistence
```
%LOCALAPPDATA%\BeckyVegas\iterative_sessions\
  <session_id>.jsonl          # append-only audit log (NDJSON)
  <session_id>.state.json     # snapshot for resume: {pid, project_path, timeline_snapshot, pending_proposals, pass_history, created_at, updated_at}
```

On pause: write state. On resume: read state, verify VEGAS PID still alive and project matches, reconnect.

---

## 5. Risks & Assumptions

| Risk/Assumption | Impact | Mitigation |
|-----------------|--------|------------|
| **VEGAS scripting API limits** — complex edits (trim, split, reorder) require `run_script` with custom C#; may hit COM edge cases (E_FAIL, E_UNEXPECTED) | Edit fails, leaves timeline in unknown state | Bridge's `Guarded()` already refuses during render/dialog; `UndoBlock` wraps each batch; test scripts against `check-vegas-script.ps1` first |
| **Gemma-4 VRAM contention** — watchpass/triage/narrative_trim need GPU; LR-ASD sweep also needs GPU | Passes fail with "model unavailable" | Existing passes already degrade to "skipped" verdicts; loop logs and continues; schedule passes when GPU free |
| **Human edits during loop** — Jordan may edit manually while agent is thinking | Timeline drift; agent's proposal based on stale state | Loop re-reads timeline before every `apply`; if drift detected (>5% event count change), aborts proposal and asks for fresh read |
| **Bridge command latency** — `run_script` can take 30+ minutes (timeout in bridge: 30 min) | Agent blocks on long script | Loop runs scripts async; proposal applies only quick ops (`insert`, `add_marker`); long ops are separate "background job" proposals |
| **Session resume after VEGAS restart** — PID changes, instance file updates | Loop reconnects to wrong VEGAS or fails | Instance registry sorts by `last_focused`; loop matches on `project_path` first, then PID |

---

## 6. Estimation

| Dimension | Assessment | Justification |
|-----------|------------|---------------|
| T-shirt size | **L** | New loop binary + bridge verb + integration of 3 existing passes + session persistence + audit log |
| Files changed | ~8 files | `vegas/BeckyVegas/Bridge.cs` (new verb), `vegas/BeckyVegas/ProjectOps.cs` (timeline delta apply), `becky-go/cmd/vegas-loop/` (new), `becky-go/cmd/roughcut/*.go` (pass entry points exposed as library), `build-all-tools.bat` (new tool) |
| Testing complexity | **High** | Requires live VEGAS Pro 18, GPU for Gemma-4, end-to-end loop with human-in-the-middle; CI can only test bridge compile + unit tests |

---

## 7. Implementation Phases (for reference — not part of spec)

**Phase 1 — Bridge Extension (1-2 days):**
- Add `apply_edit_batch` verb to `Bridge.cs:Dispatch()` → calls `ProjectOps.ApplyEditBatch(ops)`
- Add `ProjectOps.ApplyEditBatch` that takes a list of ops, wraps in one `UndoBlock`, applies each via existing primitives (`InsertClip`, `AddMarker`, `RunScriptFile` for complex ops), returns new `TimelineView`

**Phase 2 — Loop Binary (3-5 days):**
- New `becky-go/cmd/vegas-loop/main.go`: connects to bridge, implements stdin/stdout NDJSON protocol, propose-then-apply loop, session state persistence, audit log
- Expose `roughcut` passes as library functions (`runWatchPass`, `runTriagePass`, `runNarrativeTrimPass` callable with `vegas_cut.json` path)

**Phase 3 — Integration & Verification (2-3 days):**
- Wire loop verbs to call passes on current `vegas_cut.json` (re-generated from live timeline)
- Apply pass results back to VEGAS via `apply_edit_batch`
- Screenshot-verified test: agent cuts silence → adds markers → runs watchpass → runs triage → runs narrative trim → final timeline matches audit log

---

## 8. Acceptance Test Scenarios (for validate phase)

| Scenario | Given | When | Then |
|----------|-------|------|------|
| **Basic loop** | VEGAS open with project, bridge loaded | Agent starts loop → reads timeline → proposes `add_marker` at cursor → confirms | Marker appears on timeline; one Ctrl+Z removes it; audit log has 2 entries (propose, apply) |
| **Batch edit** | Timeline has 3 clips | Agent proposes `add_clip` + `add_marker` + `trim_clip` as one batch → confirms | All 3 applied in one `UndoBlock`; one Ctrl+Z reverts all; timeline reflects all changes |
| **Watchpass integration** | Timeline has 5 kept blocks | Agent calls `run_watch_pass` | `watch_report.json` written; any `FLAG` blocks get markers on live timeline; audit log entry |
| **Triage integration** | Timeline has 3 `RETAKE?` markers | Agent calls `run_triage` | Resolved markers removed from live timeline; kept markers annotated `[gemma4: ...]`; `marker_triage.json` written |
| **Narrative trim integration** | Timeline is 86 min, target 58 min | Agent calls `run_narrative_trim target_minutes=58` | Cuts applied as one batch; quotes never cut; markers/regions reflowed; `narrative_trim.json` written; new duration ~58 min |
| **Pause/Resume** | Loop running, session state saved | Jordan closes VEGAS → reopens same project → resumes loop with same session_id | Loop reconnects, reads current timeline, continues from saved state |
| **Degradation** | VEGAS rendering | Agent proposes edit | Bridge refuses with clear error; no edit attempted; loop logs and retries after render |

---

*End of SPEC. Next step: `feature-lifecycle` design exploration.*