# RUN-BUILD-PROTOCOL.md — exact instructions for running Jordan's build protocol

For any future agent. The protocol (from `X:\AI-2\PROTOCOL.md`) is implemented as
deterministic Claude Code workflow scripts. Loops, retry caps, and pass/fail gates
are enforced by code — never by model judgement.

## The workflow files (JS, not JSON/YAML)

| Project | Script | Purpose |
|---|---|---|
| mission control | `X:\AI-2\hj-mission-control\.claude\workflows\build-protocol.js` | full protocol (spec→GLM gate→build) |
| mission control | `X:\AI-2\hj-mission-control\.claude\workflows\build-protocol-phase2.js` | build phases only (post-approved spec) |
| becky-tools | `X:\AI-2\becky-tools\.claude\workflows\build-protocol-becky.js` | full protocol, becky context |
| becky-tools | `X:\AI-2\becky-tools\.claude\workflows\brn-phase1.js` | (historical) BRN phase1+waveforms |

Viz sidecar format for the future native dashboard: `<name>.viz.json` next to the
script (spec: `X:\AI-2\hj-mission-control\WORKFLOWS-NATIVE-SPEC.md` §5).

## To START a run

Call the Workflow tool:
```
Workflow({
  scriptPath: "X:\\AI-2\\becky-tools\\.claude\\workflows\\build-protocol-becky.js",
  args: { "request": "<Jordan's ask, in his words, with the load-bearing context>" }
})
```
Runs in background; you get a task-notification on completion/failure. Watch live
with /workflows. The run journal (`wf_<id>.json` + journal.jsonl, path in the launch
result) records every agent's prompt and full return value — read it before
diagnosing anything.

## To RESUME after a rate limit (the common case)

Rate-limit deaths are DESIGNED: `agent()` returns null for a dead agent and the
scripts throw "resume this run when limits reset". When the reset time passes
(error shows it, America/Los_Angeles = local PST):
```
Workflow({ scriptPath: "<same script>", resumeFromRunId: "<wf_... from the failure>", args: <same args> })
```
Completed agents replay FREE from the journal; only dead/new ones run. CAVEATS:
same session only; editing any prompt re-runs everything from the first changed call.

**Currently paused (2026-07-17): the full-native Becky Review spec run.**
⚠️ resumeFromRunId works ONLY from the session that launched it. From a NEW session,
just START FRESH with the same scriptPath + args below (nothing of value was cached —
the run died on its very first agent). From the original session, after 6:30 PM PT:
```
Workflow({ scriptPath: 'X:\\AI-2\\becky-tools\\.claude\\workflows\\build-protocol-becky.js',
  resumeFromRunId: 'wf_0ff873a1-ef5',
  args: {"request": "Re-do Becky Review as a FULL NATIVE app (no browsers): one fast window where Jordan — one of the fastest video editors in the world, a content creator first and forensic reviewer second — can search 500GB of footage, review and edit clips frame-exact on a timeline that keeps up with world-class editing speed (see feedback9: rapid splits froze the current native build for 20s and every new clip lags the whole system), build reels, and hand 90-100% of the editing to AI (VideoAgent-style intent-to-timeline) while keeping the instant human flare pass — with every basic that made him abandon the previous two versions actually present and working, on an architecture grounded in researched NLE best practices and an evidence-based adopt/extract/mine/reject decision on the OSS candidates in BUILD-INPUTS.md"} })
```

## To FEED new inputs mid-build (no restart needed)

Append to `X:\AI-2\becky-tools\BUILD-INPUTS.md` — every protocol agent re-reads it
at the start of its round. (Restarting the run to change the CONTEXT block itself
costs the cached prefix — only do it for spec-shaping changes.)

## Non-negotiable design rules baked into the scripts (keep them when editing)

1. Null-guard every `agent()` call → throw "resume when limits reset"; a review that
   never ran must return `reviewed:false` and THROW, never consume an attempt.
2. GLM gate: run ONCE (no verdict-shopping); GLM READS files itself
   (`--allowedTools Read Write`, short -p — long inline prompts get mangled and the
   cmd line caps ~8K); GLM WRITES its review to a file (stdout truncates); FAIL_n.md
   on disk is mandatory for a real fail.
3. Pass criterion is Jordan's: new table-stakes findings legitimately fail a spec;
   token cost / spec length never do; the enemy is proof-of-concept-ware.
4. Reviews are independent agents that rebuild/relaunch themselves and use REAL
   input (drags excepted: machine-wide broken, CODE-REVIEW-ONLY) at case scale
   (2000+ clip reels in native\timeline-bench); unverifiable = say
   "I could not verify this", never guess.
5. Caps (attempts/rounds) are named constants at the top of each script — Jordan
   tunes them there.
6. becky-tools: fact-forcing hook errors the FIRST Write/Edit (retry passes);
   master is hook-blocked (branch, FF only after a PASSED review); build-all-tools.bat
   after Go work; stop exes before builds.
7. Free-fleet delegation is MANDATORY and goes through ONE wrapper:
   `pwsh -File X:\AI-2\fleet\fleet-run.ps1 -Mode <mode> [-Slot <slot>] -OrderFile <o.md> -OutFile <r.md>`
   (fixes prompt-mangling/stdout-truncation/hedging; only ok:true + the OutFile count).
   Roster + routing: `X:\AI-2\hj-mission-control\docs\research\free-model-launchers.md`
   (GLM 5.2 = nvidia default; Kimi K2.7 Code = hf opus, best free coder; MiniMax M3
   vision = ollama; Hy3 = opencode opus). Qwen CLI benched 2026-07-17 (billing unverified).
