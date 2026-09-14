# ADR: VEGAS Pro 18 integration = Application Extension + per-process named pipe (2026-09-14)

## Status
Accepted. Built, verified live in VEGAS Pro 18 build 527, installed on Jordan's PC.

## Context
Jordan wants becky-tools usable inside VEGAS Pro 18 - transcript search of the timeline and of
footage folders - and wants Claude Code / Whoretana able to drive the running VEGAS. He suggested
an OFX plugin. A homemade HTTP bridge (VegasAIBridge, localhost:2015) existed and never worked.

## Decision
1. **A VEGAS Application Extension** (`vegas/BeckyVegas/`, `ICustomCommandModule` + `DockableControl`)
   hosts the Becky Search panel and the control channel.
2. **A named pipe per VEGAS process** (`\\.\pipe\becky-vegas-<pid>`), one JSON line each way, with an
   instance file in `%LOCALAPPDATA%\BeckyVegas\instances\`, and a thin client `becky-vegas.exe`.
3. **Every VEGAS call runs on VEGAS's main thread** through a `Control` created in
   `InitializeModule` (`BeginInvoke` + timeout that cancels queued work).
4. **The extension decides nothing a becky tool decides:** search = `becky-review-index --timeline`,
   transcription = `becky-transcribe`, cuts/captions = the existing `.cs` scripts.
5. Claude Code agents use the user-level `vegas-pro` skill, which points at `becky-vegas`.

## Rejected
- **OFX plugin:** the VEGAS OFX kit (Sony Vegas Video Plug-in SDK, `ofxSonyVegas.h`) exposes
  parameters, an HWND panel and gotoTime but no project, tracks, events or media paths - it cannot
  know what is on the timeline.
- **HTTP listener on a fixed port:** a leftover VEGAS process holds the port and every later launch
  pops a blocking "failed to start HTTP server" dialog (measured in the old bridge's log).
- **Fixing VegasAIBridge in place:** its whole request path ran on thread-pool threads; its "UI
  thread" helper silently ran on the calling thread when `SynchronizationContext.Current` was null.
  Replaced and retired (moved, not deleted).
- **Driving VEGAS by UI automation only:** fragile against Jordan's floating layouts and dialogs;
  kept for testing and dialog answering, not as the control path.

## Consequences
- Extension changes need a VEGAS restart; test with `-CMDMODULE` on an Untitled project.
- There is deliberately no save/open/close verb - Jordan's projects are only changed by explicit,
  undoable edits.
- Reference: `vegas/README.md` section 6, `SKILL.md` `# VEGAS PRO 18`.
