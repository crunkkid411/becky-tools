# Video edit pipeline - the worktree

One folder per step of the edit, in order. Each has:
- `README.md` - what the step does, whether it is built, which code runs it
- `JORDAN.md` - what Jordan knows that the machine doesn't (write in plain words)

Rules: `RULEBOOK.md`. The line on one page: `WORKFLOW-DIAGRAMS.md`.
The plan (Qwen's review, build prompts, diagrams): `plan/`.

Not wired yet: the tool does not read these folders until BUILD-PROMPTS #1 is built.
The launchers (.bat files) stay in `workflows/youtube` and `workflows/livestream`.
