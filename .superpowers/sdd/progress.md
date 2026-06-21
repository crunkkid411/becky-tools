# Canvas Convergence (CANVAS-BLUEPRINT Steps 1-3) — progress ledger

Branch: local/canvas-convergence-2026-06-21
Goal: wire the REAL editable models into the becky-canvas Gio window over ONE
dawmodel.Arrangement spine (not a 5th toy). Single-owner cmd/canvas; disjoint
internal/* + gui_*.go panels via subagents.

## Tasks
- [ ] Step 1 SPINE (me, solo): App holds *dawmodel.Arrangement; Arrangement->Scene
      render adapter; project.json->Arrangement import; panel dispatch; stub panel
      files (piano/mixer/audio/drumpanel) defining the contract; play from arr.
- [ ] Step 2a DRUM panel (subagent): 16-pad grid bound to Arrangement drum lane.
- [ ] Step 2b PIANO panel (subagent): note edit over dawmodel.pianoroll verbs.
- [ ] Step 2c MIXER panel (subagent): faders/pan/mute/solo over dawmodel.mixer verbs.
- [ ] Step 3 AI applier (subagent): internal/ctledit BeckyEditBatch->Arrangement.
- [ ] Integrate + final review + build -tags gui + launch-verify + push.

## Ledger
(append "Task X: complete (commits a..b, review clean)" as each lands)
Task 1 (SPINE): complete (commit 83265d7, build+test green)

## Dispatched (parallel, worktree-isolated, sonnet) — awaiting completion
- Task 2a DRUM  -> agent af74924fe28ffcac6 (owns cmd/canvas/gui_drumpanel.go)
- Task 2b PIANO -> agent a2364ae2f81b40b79 (owns cmd/canvas/gui_pianopanel.go)
- Task 2c MIXER -> agent af415bf9e50b83d43 (owns cmd/canvas/gui_mixerpanel.go)
- Task 3  CTLEDIT -> agent a97257c8734312b2d (owns internal/ctledit/)
Mixer dock button (reach ModeDAW) committed 662c569 (single-owner, disjoint from panels).
Integration plan after merges: wire ctledit into the select->ask->transform overlay
(gui.go + gui_overlay), clean dead toy drum code, build -tags gui, launch-verify, push.
Task 2c MIXER: complete (merged 94c6b93, gui build green; loop-guard reviewed OK)
Task 2a DRUM + 2b PIANO + 3 CTLEDIT: complete (this commit, build+test green)
