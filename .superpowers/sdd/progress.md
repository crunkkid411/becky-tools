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
