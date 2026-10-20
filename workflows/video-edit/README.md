# Video edit pipeline

**To continue the work: double-click `00_CONTINUE - click me.bat`** (also on the desktop as
"Continue Video Edit Work"). It starts Claude Code on the next job in `NEXT.md`.

The numbered folders are the steps, top to bottom in the order they run. Each has:
- `README.md` - what the step does and which code runs it
- `JORDAN.md` - your notes for that step, in plain words
- `PROMPT - <name>.txt` (steps that ask a model) - exactly what the model is told. Edit it in
  Notepad and save: the next run asks that step again with your words, everything else is reused.
  Words in {CAPITALS} are filled in by the tool - leave them in. Delete the file to get the
  original words back.

**To redo one step: double-click `00_REDO ONE STEP - click me.bat`**, drag the video in, pick the
step. Only that step works again. A run that crashed: same button, pick "nothing" - it carries on.
`FOLDER-CATEGORIES.txt` = which category each folder belongs to (for the folder colors).

Folders ending in NOT-BUILT are steps that don't exist yet.
`RULEBOOK.md` = the rules. `NEXT.md` = the to-do list. `plan/` = background for Claude (Qwen's
comparison with Jason Cooperson's editor, the build specs, the diagrams).
