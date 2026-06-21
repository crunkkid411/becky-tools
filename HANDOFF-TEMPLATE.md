# Handoff Template — copy this for every cloud→local handoff of runtime work

**Use this whenever a branch hands the local agent anything that needs hardware cloud
can't touch (audio, a GUI window, the GPU, a real device, ffmpeg/media).** It is the
standard that ended the "I researched it, none of it got wired up" failure: prose
handoffs got merged-and-skipped, so every handoff now ships (1) a one-command proof
cloud already ran, and (2) an ordered, checkboxed work order with the exact command per
step. Copy the skeleton below into `LOCAL-WORK-ORDER.md` (or a `HANDOFF-<topic>.md`) and
fill it in. See `LOCAL-WORK-ORDER.md` for a worked example (the canvas drum machine).

## The two non-negotiables

1. **A one-command, no-hardware PROOF that cloud RAN and pasted evidence for.** Before a
   runtime branch is "ready", cloud must add an offline verification entry point — a
   `--render` / `--selftest` / `--dry-run` / `--export` flag that exercises the SAME code
   path the GUI/device will, writes an artifact, and is measurable (ffprobe, a byte count,
   a hash, an enumerated result). Cloud runs it and pastes the numbers. "It compiles" and
   "the model exists" are NOT proof. *If you can't hand over a one-command proof, you
   haven't finished your half.*
2. **An ordered, checkboxed work order — commands, not prose.** Each step has: the exact
   command, the expected output, DONE-WHEN (an observable result), and what to paste back.
   No step is "wire it up" — name the files/functions to connect, since cloud already
   built the pieces.

The §6 handoff entry points the local agent at the work order with a "do NOT merge-and-stop"
banner, and the branch is not marked done until every box is checked WITH evidence.

---

## Skeleton (copy + fill)

```
# LOCAL WORK ORDER — <feature>: <the observable goal, e.g. "make X play/show/render">

Branch: claude/<topic>. Run from becky-go/ unless noted. <OS/shell.>

## Step 0 — deterministic layer green
    go build ./... ; go vet ./... ; go test ./...
- [ ] DONE WHEN: all pass. Paste the last line.

## Step 1 — PROVE it offline (cloud already proved this) — no GUI/device needed
    <the one command: e.g. becky-<tool> --render-<x> <in> --out <out>>
    <the measure: e.g. ffprobe <out>  /  ffmpeg ... volumedetect  /  a hash/count>
- [ ] DONE WHEN: <the measurable threshold, e.g. "mean above −40 dB / N items / non-empty">.
  Cloud's measured result: <paste the numbers cloud got>. Paste yours.
- If it fails: <the most likely NON-code cause, e.g. "the asset/path", and how to fix it>.

## Step 2 — build the REAL binary (the step that silently gets skipped — spell it out)
    <exact build, incl. any CC / build tags / ldflags, e.g.
     $env:CC="C:\msys64\mingw64\bin\gcc.exe"
     go build -tags audio -o bin\<exe>.exe ./cmd/<x>>
- [ ] DONE WHEN: the exe exists and <a quick non-GUI check of it> works. Paste `dir bin\*.exe`.

## Step 3 — the hardware check ONLY a human/device can do
    <launch + the exact clicks/inputs>
- [ ] DONE WHEN: <heard it / saw it / a screenshot>. Report: did it / a screenshot / the error.

## Step 4 — the remaining REAL feature (connect existing proven pieces)
    Source: <file:func that already does it elsewhere>. Target: <file to wire it into>.
- [ ] DONE WHEN: <observable result> + evidence.

## Step 5 — report back honestly
Update CLAUDE.md §6: which boxes are checked, the pasted evidence, and any failure with the
exact error. Do NOT write "LEFT FOR LOCAL: nothing" unless every box is checked with evidence.
A stuck step reported honestly beats a green checkmark that isn't true.
```
