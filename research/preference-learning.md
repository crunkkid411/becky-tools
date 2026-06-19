# Preference learning + "show me, don't do it" for becky's drum machine & piano roll

Research + concrete design for the two becky-specific things that make the drum machine
**learn the user** while keeping him **in control**. Grounded in the existing becky code
(`internal/canvas`, `internal/habits`, `internal/drumcmd`) so the design is a *generalization
of what already ships*, not a parallel system.

Bottom line:
- **Part 1** — becky already has a clean, immutable, propose→approve loop in
  `internal/canvas/transform.go` (`Proposal` + `Transformer` + pure `Apply`/`RejectScene`).
  The drum machine and piano roll should reuse it, not reinvent it. The missing piece is a
  **typed patch** (`GridPatch`/`NotePatch`) so the preview can render a real before/after
  diff and `Apply` is a genuine structural merge rather than text-only.
- **Part 2** — becky already has the *whole* learner: `internal/habits` with
  `CorrectionRecord`, the `MinEvidence=2` corroborate-then-conclude threshold, scalar +
  structured canonicalization, and the `Usual()`/`UsualField()` "my usual X" recall API.
  The missing piece is the **emit side**: every drum/piano/mixer edit (and every *approval*
  of an AI proposal) must append a structured `CorrectionRecord`. The `internal/canvas`
  hooks (`MapDrumToggle`, `Apply`→`Correction`) already prove the pattern; it just needs to
  be wired through and extended from step-toggles to *parametric* edits (gain, swing,
  sidechain, tuning).

---

## Part 1 — The "show me, don't do it" overlay (reusable Proposal / preview / apply)

### What the literature says (and why becky is already aligned)

The human-in-the-loop "Review → Approve" pattern is the standard for any agent action that
mutates user state. The consensus guidance maps exactly onto becky's design north star
("show me, don't do it"):

- The UI pattern is **Review → Approve with *minimal reading*: show the diff and the reason,
  not the whole output.** ([cordum.io](https://cordum.io/blog/human-in-the-loop-ai-patterns),
  [Vectorlane / Medium](https://medium.com/@jickpatel611/human-approval-is-the-feature-not-the-bug-4471771a09b4))
- The workflow **pauses at a decision point, saves the state needed to resume, and applies
  the action ONLY if approved** — otherwise the original state is untouched
  ([orkes.io](https://orkes.io/blog/human-in-the-loop/),
  [permit.io](https://www.permit.io/blog/human-in-the-loop-for-ai-agents-best-practices-frameworks-use-cases-and-demo)).
- Human approval is **the feature, not the bug** for creative/high-stakes edits — keep the
  human as the final decision-maker
  ([Maven HITL design patterns](https://maven.com/p/a83305/human-in-the-loop-design-patterns),
  [aiuxplayground](https://www.aiuxplayground.com/pattern/human-loop)).

The reliability mechanism underneath is the **Command pattern**: encapsulate the requested
change as an *immutable* object that can be applied and reversed; immutability is what makes
apply/undo/redo trustworthy ([refactoring.guru](https://refactoring.guru/design-patterns/command),
[codinghelmet](https://codinghelmet.com/articles/does-the-command-pattern-require-undo),
[Anubhav Gupta / Medium](https://anubhav-gupta62.medium.com/undoable-command-design-pattern-30ca60b445cd)).
becky's `Proposal` is exactly an immutable Command, and `Apply` already returns a *new* model
value rather than mutating in place — so undo is just "keep the previous value."

### What becky already has (the base to generalize)

`internal/canvas/transform.go` is the canonical loop and should be the *only* one:

```
Selection           -- WHAT is selected (region/track/clip/note/stroke)
Transformer.Propose -- (scene, selection, instruction) -> *Proposal   [no mutation]
Proposal            -- Kind, Summary, Before, After, Delta, *ScenePatch
Apply(scene, p)     -- returns a NEW scene (immutable); logs a Correction
RejectScene(...)    -- returns the original scene unchanged
StubTransformer     -- deterministic offline keyword classifier (CI-safe)
PickTransformer()   -- returns the real model impl if the GGUF resolves, else the stub
```

Key properties that are *already correct* and must be preserved:
- **Nothing mutates until approval** — `Propose` is read-only; mutation only happens in
  `Apply`, which a GUI calls on ✓.
- **Immutable update** — `Scene` is a value type; `Apply` copies. This gives free undo/redo
  (push the pre-apply value on a stack).
- **Deterministic + degrade-never-crash** — stub is seeded only from inputs; nil proposal →
  wrapped error, never a panic.
- **Approval auto-logs a `Correction`** — `Apply` already appends to `scene.Corrections`,
  which is the bridge to Part 2.

`internal/drumcmd` independently grew the *same* shape (`Result{Before, After, Variants,
Summary}` with immutable transforms) — confirming the pattern but **duplicating it**. The
design below unifies them.

### The gap to close

1. `Proposal.ScenePatch` is "an optional partial Scene"; the stub never emits one, so today a
   drum/piano proposal can only carry text Before/After, not a real structural diff. A piano-roll
   "transpose these 4 notes up a semitone" needs a **note-level patch** the overlay can render
   green/red.
2. `drumcmd.Result` is a *separate* preview type. The GUI would have two preview code paths.

### Proposed reusable design: `Proposal[Patch]` over a typed patch

Keep the existing `Proposal` flow; make the patch **typed and model-specific** while the
loop stays generic. Concretely, three small additions:

```go
// 1) A patch is anything that can be applied to a model value and described.
//    (drum grid, note set, mixer params each implement this.)
type Patch interface {
    Describe() (kind ChangeKind, before, after string, delta float64, summary string)
}

// 2) Proposal carries a typed Patch instead of only a *Scene.
type Proposal struct {
    ID          string
    Sel         Selection
    Instruction string
    Kind        ChangeKind
    Summary     string
    Before, After string
    Delta       float64
    Patch       Patch        // <- the structural diff (nil => text-only, as today)
}

// 3) Each editor supplies a typed patch + an apply func. The LOOP is generic.
type GridPatch struct {              // drum machine
    Cells   []CellDelta              // {Lane, Step, WasOn, IsOn}
    Params  []ParamDelta             // {Lane, Field:"velocity"|"swing"|"tune", From, To}
}
type NotePatch struct {              // piano roll
    Notes   []NoteDelta              // {Idx, FromPitch,ToPitch, FromTick,ToTick, FromVel,ToVel, Added,Removed}
}
type MixPatch struct {               // mixer
    Edits   []MixDelta               // {Bus, Field:"gain_db"|"sidechain"|"fx_chain", From, To}
}
```

`GridPatch`, `NotePatch`, `MixPatch` each implement `Patch.Describe()` (for the one-line
summary + before/after badge) and a typed `Apply(model) model` that does the structural merge
**immutably** (return a new grid/notes/params). The drum machine reuses `drumcmd`'s existing
immutable transforms to *build* a `GridPatch` (diff old vs new grid) rather than mutating
directly — so `drumcmd.Result` becomes a `GridPatch` producer and the duplicate preview type
goes away.

### Flow (identical for drum machine, piano roll, mixer)

```
1. SELECT      Jordan selects cells / notes / a bus  -> Selection
2. ASK         types plain language OR clicks a transform button ("half-time", "humanize")
3. PROPOSE     Transformer/transform builds a *Proposal with a typed Patch  [READ-ONLY]
4. PREVIEW     overlay renders:
                 - drum: ghost cells (to-add = neon green, to-remove = dimmed red)
                 - piano: ghost notes at the proposed pitch/time, original shown faded
                 - mixer: a before->after badge ("kick -3 dB -> -7 dB", "+ sidechain bass<-kick")
                 - one plain-English Summary line + the Delta
5. DECIDE      Esc / ✗  -> RejectScene (original kept, nothing logged)
               Enter / ✓ -> Apply (new model value) AND emit a CorrectionRecord (Part 2)
6. UNDO        previous model value is on a stack; redo re-applies the Patch
```

The overlay is **global**: it takes any `*Proposal`, reads `Patch.Describe()` for the text,
and dispatches to a per-kind ghost renderer for the visual diff. One overlay, three editors.

### Where the AI plugs in (unchanged boundary)

`PickTransformer()` already returns the model impl when the GGUF resolves, else the
deterministic stub. The model's job is only to turn free-form language into a `Proposal`
(`drumcmd`'s `modelParser` does this for drums today, degrading to keywords). **The model
never mutates the project** — it only proposes. This keeps the offline/deterministic invariant
intact for the apply path and makes the model fully optional.

---

## Part 2 — Preference learning from manual corrections

### What the literature says

Learning a user's latent preferences from their **edits** (rather than asking them) is an
active research area and the right model for becky:

- User edits are a rich but **implicit** signal: the edit shows *what* changed, not *why*,
  so a system must infer the latent preference and is prone to over-reading a one-off
  ([Aligning LLM Agents by Learning Latent Preference from User Edits / PRELUDE, NeurIPS 2024](https://proceedings.neurips.cc/paper_files/paper/2024/file/f75744612447126da06767daecce1a84-Paper-Conference.pdf),
  [arXiv 2404.15269](https://arxiv.org/pdf/2404.15269)).
- Implicit feedback (editing behavior, corrections) is a **scalable, less-intrusive**
  alternative to explicit preference menus
  ([Learning User Preferences Through Implicit Feedback](https://www.researchgate.net/publication/393802914_LEARNING_USER_PREFERENCES_THROUGH_IMPLICIT_FEEDBACK)).
- **Programming/Learning by Demonstration**: generalize from a user's concrete demonstrations
  into a robust rule that fires in new situations — the exact "you always pull the kick to
  -7, so do it next time" behavior becky wants
  ([Learning from Demonstration, EPFL](https://infoscience.epfl.ch/entities/publication/9901cb1f-07c4-4533-93a2-a9862ef16b5f),
  [Springer LfD](https://link.springer.com/rwe/10.1007/978-3-642-41610-1_27-1),
  [ALLOY: reusable workflows from demonstration, arXiv 2510.10049](https://arxiv.org/pdf/2510.10049)).

The dominant risk in all of this is **over-fitting to a single edit**. becky's
`MinEvidence=2` "corroborate, then conclude" gate is precisely the mitigation the literature
calls for: don't promote a preference until it recurs.

### What becky already has (the entire learner)

`internal/habits` is complete and tool-agnostic:

- `CorrectionRecord{Scope, Field, Auto, Fixed, Context}` — the generic override any tool emits.
- `Store.Observe` / `ObserveAll` — tallies `Fixed` values per `{scope, field}`.
- **Corroborate-then-conclude**: `recompute` marks a habit `Learned` only when the winning
  value's count `>= MinEvidence (2)`. One-off = CANDIDATE, recurring = DEFAULT.
- `Apply(scope, field, fresh)` — returns the learned default if any, else the caller's fresh
  value (candidates are NOT applied).
- **Structured values**: `canonicalValue()` sorts JSON keys so `{"from":"kick","to":"bus.bass"}`
  and the reverse key order corroborate each other; `ApplyStructured`, `Usual(scope)`,
  `UsualField(scope, field)` are the "my usual X" recall API.
- **On-disk JSONL contract** (`sources.go`): `{tool, scope, field, auto, fixed, ts}` one per
  line; `LoadCorrectionLogs(dir)` reads `*.jsonl` deterministically; `AppendCorrectionLog(...)`
  is the emit helper.

The `internal/canvas` side already proves the wiring template: `gesture.go`'s `MapDrumToggle`
turns a cell toggle into `(scope="kick", field="step/4", auto="off", fixed="on")` and
`AppendDrumEdit` logs it; `transform.go`'s `Apply` appends a `Correction` on every approval.

### The gap to close

The learner and the JSONL contract exist; what's missing is **emit coverage**:
1. Step toggles map to `step/<N>` on/off (done in canvas `gesture.go`) — but the *interesting*
   habits are **parametric**: kick tune, lane velocity, swing amount, sidechain routes, FX
   chains. Those edits need `CorrectionRecord`s too.
2. Approving an AI `Proposal` should emit a correction (canvas `Apply` logs a *canvas*
   `Correction`, but it does not yet call `habits.AppendCorrectionLog`).
3. The drum machine / piano roll / mixer GUIs need a single `recordEdit(...)` choke point.

### Proposed design: a structured correction for every edit

Define a **canonical scope/field vocabulary** so habits are stable and recall is predictable.
`Auto` = becky's value (or the value before Jordan's manual move); `Fixed` = Jordan's value.

| Edit (drum / piano / mixer)        | scope            | field            | auto → fixed (example)                         |
|------------------------------------|------------------|------------------|-----------------------------------------------|
| Pull kick tune down                | `kick`           | `tune_st`        | `0` → `-2`                                     |
| Drop kick level                    | `kick`           | `gain_db`        | `-3` → `-7`                                    |
| Snare velocity feel                | `snare`          | `velocity`       | `100` → `88`                                   |
| Swing amount                       | `pattern`        | `swing`          | `0.50` → `0.58`                                |
| Humanize amount accepted           | `snare`          | `humanize_ms`    | `0` → `12`                                     |
| Sidechain bass to kick (route)     | `routing`        | `sidechain`      | `{}` → `{"from":"kick","to":"bus.bass","amt":0.5}` |
| Drum-bus FX chain                  | `bus.drums`      | `fx_chain`       | `[]` → `[{"fx":"glue","ratio":4},{"fx":"sat"}]`|
| Step toggle (already wired)        | `kick`           | `step/4`         | `off` → `on`                                   |
| Piano note transpose               | `lead`           | `note.transpose` | `0` → `+1`                                     |
| Piano note velocity                | `lead`           | `note.velocity`  | `100` → `112`                                  |

**Canonicalization** (already handled by `habits.canonicalValue`): scalars pass through;
JSON routes/chains are key-sorted so logically-equal preferences corroborate. Numeric scalars
should be emitted in a **canonical string form** (e.g. always `"-7"` not `"-7.0"`, one decimal
for dB) so `"-7"` and `"-7.0"` don't split the count — a small formatting helper on the emit
side, mirroring `canonicalValue`'s intent for scalars.

**Single choke point** (new, ~10 lines, per editor):

```go
// recordEdit is called by every drum/piano/mixer manual edit AND by Proposal approval.
func (a *App) recordEdit(scope, field, auto, fixed string) {
    _ = habits.AppendCorrectionLog(a.logPath, a.toolName, scope, field, auto, fixed)
} // best-effort; never blocks or crashes the edit (degrade)
```

Wire it in two places:
- **Manual edits**: the GUI already knows before/after (it's drawing the drag). Map the gesture
  (extend `canvas/gesture.go` from step-toggle to a `ParamEdit{scope, field, from, to}`) and
  call `recordEdit`.
- **Proposal approval**: in `Apply`, in addition to the existing `scene.Corrections.Append`,
  derive `(scope, field, auto, fixed)` from `Patch.Describe()` and call `recordEdit`. (An
  *approved AI suggestion is also a demonstration* of what Jordan wants — it should feed the
  same learner.)

### Recall: "my usual X" (already built — just call it)

At generation/proposal time, before showing a default, consult the learner:

```go
// scalar: "what level do I usually put the kick at?"
gain := habits.Apply("kick", "gain_db", defaultGain)        // returns -7 once learned

// structured: "set up my usual drum bus"
if pref, ok := store.UsualField("bus.drums", "fx_chain"); ok {
    proposeFXChain(pref.Value)        // a Proposal Jordan still approves
}
for _, p := range store.Usual("routing") { ... }            // all learned routes
```

Crucially, a learned default is **still surfaced as a Proposal** ("I set the kick to -7 like
you usually do — ✓/✗"), not silently applied. This keeps "show me, don't do it" intact and
gives Jordan a natural way to *correct the learned default* (which itself logs a new
correction — the loop closes).

### Corroborate-then-conclude thresholds (keep becky's existing gate)

- `MinEvidence = 2` stays as the promotion floor: a value is a **CANDIDATE** until it recurs
  ≥2×, then becomes the **learned DEFAULT**. This is the literature's anti-overfit guard.
- The winner is always the **most-corroborated** `Fixed` value, ties broken by string order
  (deterministic). A learned default is therefore *self-correcting*: if Jordan starts pulling
  the kick to -6 instead of -7, once -6 reaches 2 it overtakes.
- **Context (`bpm`, `genre`, `tool`) is provenance, not part of the key** today. v1 keeps it
  that way (simple, predictable). See v1-vs-later for genre-scoped habits.

---

## v1-minimal vs later

### v1-minimal (build now; pure-Go, deterministic, no GPU)
1. **Unify the preview type**: add `Patch` interface + `GridPatch`/`NotePatch`/`MixPatch`;
   make `drumcmd` transforms *produce* a `GridPatch` (diff before/after) so the GUI has one
   `Proposal` preview path. (Pure logic, unit-testable on CI.)
2. **Typed structural `Apply`** for each patch (immutable; gives free undo/redo via a value
   stack). Keep `RejectScene`/reject = original unchanged.
3. **Emit coverage**: a `recordEdit` choke point + extend `canvas/gesture.go` to parametric
   edits (`tune_st`, `gain_db`, `velocity`, `swing`, `humanize_ms`, note transpose/velocity).
   Wire it into every manual edit **and** into `Apply` on approval.
4. **Recall at generation time**: call `habits.Apply` / `Usual` / `UsualField` for kick gain,
   swing, sidechain, drum-bus FX chain — surfaced as Proposals, not silent.
5. **Canonical scalar formatting** helper so numeric values don't split the count.
6. Tests: patch round-trips (apply then describe), determinism, emit→load→learn end-to-end at
   the `MinEvidence` boundary (1 obs = candidate, 2 = learned), reject = no log written.

This is entirely within becky's existing offline/deterministic envelope and needs no model.

### Later (needs hardware, real model, or more design)
- **Real `Transformer`/`Parser` model** for free-form language (`PickTransformer`/`PickParser`
  already stubbed; local agent wires the GGUF exec). Still proposes-only.
- **Ghost-diff rendering** in the Gio GUI (green to-add / red to-remove cells; faded original
  notes) — the visual half of the overlay; logic is v1.
- **Context-scoped habits**: promote `bpm`/`genre` from provenance into an *optional* part of
  the key (e.g. "your usual kick gain *in crunkcore*"), with a fallback to the global habit
  when no genre-specific one is learned. Needs careful key design to avoid fragmenting counts.
- **Confidence/decay**: weight recent corrections higher, or surface evidence count in the UI
  ("learned from 4 edits"). The literature warns latent preferences drift over time.
- **Undo/redo UI** + a "forget this habit" affordance (delete a `{scope, field}` from the store).
- **Cross-tool habit sharing**: hum/vox/daw/canvas/drum all emit to the same store so "my
  usual snare" is consistent across the suite (the JSONL contract already supports this; just
  needs every tool emitting).

---

## Sources

- [Human approval is the feature, not the bug — Vectorlane / Medium](https://medium.com/@jickpatel611/human-approval-is-the-feature-not-the-bug-4471771a09b4)
- [Human-in-the-loop for AI agents — permit.io](https://www.permit.io/blog/human-in-the-loop-for-ai-agents-best-practices-frameworks-use-cases-and-demo)
- [Human-in-the-loop in agentic workflows — orkes.io](https://orkes.io/blog/human-in-the-loop/)
- [Human-in-the-loop design patterns — Maven](https://maven.com/p/a83305/human-in-the-loop-design-patterns)
- [Human in Loop pattern — AI UX Playground](https://www.aiuxplayground.com/pattern/human-loop)
- [Human-in-the-loop AI patterns (show the diff and the reason) — cordum.io](https://cordum.io/blog/human-in-the-loop-ai-patterns)
- [Command pattern — refactoring.guru](https://refactoring.guru/design-patterns/command)
- [Does the Command pattern require undo? — codinghelmet](https://codinghelmet.com/articles/does-the-command-pattern-require-undo)
- [Undoable Command design pattern (immutability) — Anubhav Gupta / Medium](https://anubhav-gupta62.medium.com/undoable-command-design-pattern-30ca60b445cd)
- [Aligning LLM Agents by Learning Latent Preference from User Edits (PRELUDE) — NeurIPS 2024](https://proceedings.neurips.cc/paper_files/paper/2024/file/f75744612447126da06767daecce1a84-Paper-Conference.pdf) ([arXiv](https://arxiv.org/pdf/2404.15269))
- [Learning User Preferences Through Implicit Feedback — ResearchGate](https://www.researchgate.net/publication/393802914_LEARNING_USER_PREFERENCES_THROUGH_IMPLICIT_FEEDBACK)
- [Learning from Demonstration (Programming by Demonstration) — EPFL](https://infoscience.epfl.ch/entities/publication/9901cb1f-07c4-4533-93a2-a9862ef16b5f) / [Springer](https://link.springer.com/rwe/10.1007/978-3-642-41610-1_27-1)
- [ALLOY: Generating Reusable Agent Workflows from User Demonstration — arXiv 2510.10049](https://arxiv.org/pdf/2510.10049)
