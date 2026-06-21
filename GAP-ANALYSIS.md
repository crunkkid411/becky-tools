# GAP-ANALYSIS.md — actual implementation vs. FEATURE-INVENTORY.md

This is the honest, evidence-based punch-list. Each item from `FEATURE-INVENTORY.md` is marked
**✅ DONE** (real, working, cite the file), **🟡 PARTIAL** (exists but incomplete — say what's
missing, or model-only / GUI-only-unverifiable), or **❌ MISSING**. Citations are `file:Symbol`.

A guiding rule of this analysis: **a data model with no engine or GUI is 🟡 at best**, and
**GUI-only-unverifiable counts as 🟡 with a note** (cloud cannot render/hear). becky's recurring
pattern is a strong, tested, immutable *model* layer with a thin/absent *runtime* (audible/visible)
layer — so most 🟡s are "the math is there, nothing executes/shows it."

Date: 2026-06-21. Investigated against `/home/user/becky-tools/becky-go`.

---

## 1. DAW / transport core

### Transport
- 🟡 **Play** — `cmd/canvas/gui_play.go:execPlay` shells out to `becky-daw-engine --play-pattern-audio/--play-machine`; plays a whole arrangement/loop. Works, but it renders-then-plays a fixed loop count (`playLoops=16`), not a true playhead-following transport. GUI-only-unverifiable on cloud.
- ❌ **Stop** (true transport stop) — `■` only *kills the engine process* (`runEngine` clears `playProc`); there is no playhead concept to halt/reset.
- ❌ **Pause/resume** — no pause; the engine is a one-shot render-and-play process.
- 🟡 **Loop / cycle** — only "tile the bar N times" (`audioengine.MachineLoopSamples`, `synth_audio.go` loopLen). No user loop region, no continuous cycle.
- ❌ **Set loop region** — no loop-region model on the arrangement or transport.
- 🟡 **Record** — `becky-daw-engine --record <sec> <out.wav>` records the mic to a WAV (`main_audio.go:runRecord` → `MiniaudioBackend.RecordWAV`). This is file capture, NOT record-to-armed-track in the session.
- ❌ **Record-arm per track** — no `RecordArm` field on `Track`/`Strip`; no arm path.
- ❌ **Metronome / click** — no metronome anywhere.
- ❌ **Count-in** — none.
- 🟡 **Playhead positioning** — exists only in the NLE (`cmd/becky-nle`), not the DAW/canvas; the canvas has no scrubable playhead over the arrangement.
- ❌ **Return-to-start / go-to-end** — no playhead, so no boundary jumps in the DAW.
- ❌ **Punch in/out** — none.

### Tempo & time
- ✅ **Set tempo (BPM)** — `dawmodel.Arrangement.BPM`; settable via `ctledit` `OpSetTempo` (`ctledit/types.go:OpSetTempo`, `apply.go` set_tempo) and `becky-arrange`. Range-checked (1..999).
- ❌ **Tempo automation / tempo map** — single global `BPM int` only; no tempo events over time.
- 🟡 **Time signature** — fields exist (`Arrangement.Num`, `Arrangement.Den`, default 4/4); no GUI/op to change them (no ctledit op for time sig).
- ❌ **Time-signature changes** — single meter only; no per-bar meter events.
- ❌ **Tap tempo** — none (BPM is numeric only).

### Project / session management
- ✅ **New project** — `dawmodel.New()` (BPM 120, PPQ, 4/4 defaults); canvas/drummachine starter sessions.
- 🟡 **Save project** — the canvas has **no Save-to-disk path** (verified: only `cmd/canvas/main_test.go` writes JSON; no `saveProject`/`SaveArrangement` in the GUI). `becky-daw edit --out` and `becky-arrange --out` write arrangement JSON/.mid from the CLI, so a session *can* be persisted via CLI, but the GUI cannot save what you edit. Major gap for "functional".
- ❌ **Save-as / versioning** — none.
- ✅ **Load / open project** — `canvasbridge.ArrangementFromProjectFile` loads a becky-compose project.json or .mid into the spine (`cmd/canvas/gui_spine.go:setTarget`); `becky-daw load`, `dawmodel.FromSMF`.
- ❌ **Recent projects** — none.
- ❌ **Auto-save / crash recovery** — none.
- 🟡 **Project settings** — sample rate is engine-side (48k default in `runRecord`); output-device selection exists (`audioengine.selection`, `--list-real`). No unified project-settings surface; no bit-depth/default-paths config.

### Editing
- ✅ **Undo** — `internal/undo.History.Undo` over immutable arrangement snapshots; bounded depth 200.
- ✅ **Redo** — `undo.History.Redo`.
- ✅ **Multi-level undo history** — `undo.History` is a full snapshot stack with cursor, `CanUndo`/`CanRedo`. (Wired through the canvas `applyArr` commit path per package doc; runtime GUI-unverifiable on cloud.)
- ❌ **Copy** — no clip/event clipboard in `dawmodel`/`ctledit`/canvas.
- ❌ **Cut** — none.
- ❌ **Paste** — none.
- 🟡 **Duplicate** — drum patterns can duplicate (`drummachine.DuplicatePattern`); no general clip/note duplicate in the arrangement/piano roll (no `Duplicate` in `dawmodel`/`pianoroll`/`ctledit`).
- ✅ **Delete** — notes: `ctledit OpDeleteNotes`, `pianoroll.Delete`; tracks/clips deletable in models.
- ❌ **Select all / range select** — no time-range/cross-track selection model.

### Tracks
- ✅ **Add track** — `dawmodel.AddTrack`, `ctledit OpAddTrack`, canvas drum-machine default adds a drums track (`drummachine_default.go`).
- 🟡 **Delete track** — model supports it but no dedicated `ctledit` op / GUI button (no `OpDeleteTrack` in `ctledit/types.go`).
- ❌ **Reorder tracks** — no track-reorder op.
- 🟡 **Rename track** — `Track.ID` is the name; no rename op/UI.
- ❌ **Color track** — no track color field.
- ❌ **Track height / resize** — no per-track height in the model/GUI.
- ❌ **Freeze / flatten track** — none.

### Arrangement & navigation
- 🟡 **Arrangement timeline** — the model is timeline-true (`Clip.Offset` in ticks, notes at absolute ticks) and the canvas DAW view renders clips (`canvasbridge.SceneFromArrangement`); but there is no bar/beat ruler with scrub/zoom over the whole arrangement. GUI-unverifiable.
- ❌ **Markers** — no marker model.
- 🟡 **Regions / sections** — `internal/arrange` reasons about song sections in analysis (`arrange/rules.go`, `analyze.go` mentions sections) but there is no named-section model on the arrangement.
- ❌ **Loop browser / clip launcher** — none.
- 🟡 **Snap / grid** — quantize snaps to a grid (`dawmodel.Quantize` grid param) and the piano panel snaps draws to ppq/4 (`gui_pianopanel.go:xToTick`), but the snap resolution is fixed (1/16), not user-selectable.
- ✅ **Quantize (clip/event)** — `dawmodel.Quantize` (strength + swing), shared by piano roll + drum grid; `pianoroll.Quantize`/`QuantizeEnds`. Tested.
- 🟡 **Zoom (horizontal/vertical)** — piano panel has horizontal zoom/scroll (`pxPerTick`); vertical is auto-fit once (`autoFitPitch`), not interactive. The arrangement view has no zoom. GUI-unverifiable.
- 🟡 **Scroll / pan** — piano panel horizontal scroll only; no arrangement-wide scroll.

### Output
- ✅ **Bounce / render to audio** — `becky-daw-engine --play-pattern-audio`/`--render-machine` render a pattern to WAV via the sampler/synth engine (`audioengine` + `machine_audio.go`); `becky-reaper render` drives REAPER to a real WAV.
- 🟡 **Stem export** — `becky-compose` emits per-stem .mid and `becky-reaper compose` routes stems to bus tracks, but there is no "render each track to a separate audio file" from the DAW engine itself.
- 🟡 **Export range** — exists in the NLE (`cmd/becky-nle:exportRange`), not the audio DAW (can't bounce a time region of the arrangement).
- ❌ **Export format options** (audio) — engine WAV is fixed (16-bit / 48k in paths); no codec/bit-depth/channel choice for audio bounce.
- ✅ **MIDI export** — `dawmodel.ToFile`/`ToSMF` (byte-stable SMF writer, round-trips); `becky-daw edit --out song.mid`.
- ❌ **Normalize / loudness target on export** — no normalize-on-export for audio bounce.

**Top missing for functional (DAW core):**
1. **Save the session from the GUI** — the canvas can load+edit but cannot save; this alone blocks "real software."
2. **A real transport** — playhead, Stop/Pause, loop region, return-to-start (today ▶ is render-and-play-a-fixed-loop).
3. **Metronome + count-in + record-arm** — no click and no record-to-track; recording is mic→WAV only.
4. **Clipboard editing** — Copy / Cut / Paste / Select-range across the arrangement.
5. **Markers / named sections** on the timeline.
6. **Track management** — reorder, rename, color, delete (op/UI).

---

## 2. Drum machine / step sequencer

> Two models coexist: `internal/drummachine` (16 pads, on/velocity, real sampler engine) and
> `internal/beatgen` (rich per-step + generative). They bridge via `ToDrumGrid`/`PatternFromDrumGrid`
> but the round-trip **drops** the rich per-step data, and the rich step fields have **no GUI editor**.

### Pads & lanes
- ✅ **Pad grid** — `drummachine.go:PadCount=16` (4×4); canvas drum panel + `cmd/drummachine` GUI render it.
- ✅ **Lanes** — `beatgen.Lane` (one row per voice); canvas `gui_drumpanel.go` draws lanes×steps.
- 🟡 **Per-lane label / name** — `beatgen.Lane.Name` and canvas labels exist; the `drummachine` pad names are static (`gmDefaults`), not editable.
- 🟡 **Add/remove lane** — `beatgen` supports variable lanes in the model, but `drummachine` is fixed 16 pads and there's no GUI to add/remove a lane.

### Step grid
- ✅ **Step on/off** — `drummachine.ToggleStep`/`SetStep`; canvas toggles cells.
- ✅ **Pattern length** — `drummachine.SetSteps` (16/32/64).
- 🟡 **Step resolution** — 1/16 hard-coded (`DefaultSteps=16`, `validStepCounts`); **no triplet grid**.
- ✅ **Bar paging / scroll** — `cmd/canvas/gui_drumpanel.go:barWindow` + `paging.go` (`<`/`>` bar nav, 6 unit tests).
- ✅ **Per-step velocity** — `drummachine.Step.Vel`; `beatgen.Step.Velocity`. (Editable in model; **no per-step velocity GUI**.)
- 🟡 **Per-step probability** — `beatgen.Step.Probability` only; `drummachine` Step has none, and no GUI.
- 🟡 **Per-step microtiming / nudge** — `beatgen.Lane.TrackDelay` is per-*lane* only; no per-*step* nudge; no GUI.
- 🟡 **Per-step ratchet / retrigger** — `beatgen.Step.Ratchet` + `ExpandStep` (model done); `drummachine` lacks it; no GUI.
- 🟡 **Per-step flam** — `beatgen.Step.Flam` + `flam.go:flamHits` (model done); `drummachine` lacks it; no GUI.
- 🟡 **Per-step pitch / tune** — `beatgen.Step.Pitch` (model); `drummachine` only pad-level `PitchSemitones`; no per-step GUI.
- 🟡 **Per-step pan** — `beatgen.Step.Pan` (model); `drummachine` only pad-level; no per-step GUI; stereo render is Phase-2 (engine is mono).

### Patterns & song
- ✅ **Multiple patterns** — `drummachine.Bank.Patterns` (`MaxPatterns=16`).
- ✅ **Pattern select / bank** — `Bank` + scenes (`Scene.PatternIndex`).
- ✅ **Copy / paste pattern** — `drummachine.DuplicatePattern` (deep copy).
- ❌ **Clear pattern** — no explicit clear method.
- ✅ **Pattern chaining / song mode** — `drummachine.Song`+`SongEntry`, `SetSongOrder`.
- ✅ **Per-pattern length** — each `Pattern.Steps` independent.

### Groove & humanization
- ✅ **Swing** — `Pattern.Swing` (0.5=straight..0.75), `SetSwing`, `swingDelayTicks`; `beatgen.Lane` per-lane swing. Convention documented.
- 🟡 **Global humanize** — exists as an external transform (`internal/drumcmd` "humanize the snare"), not a drummachine op; partial coverage.
- ❌ **Accent** — no explicit accent (velocity simulates it).
- 🟡 **Shuffle/groove templates** — `beatgen` genre profiles weight placement (`genre.go`), but no named groove-template apply on `drummachine`.

### Sounds & kit
- ✅ **Per-pad sample load** — `Pad.SamplePath`/`Pad.Sound`, `SetPadSample`, `WithPadSound`.
- ✅ **Kit load** — `drummachine/kitload.go:LoadKitFromSFZ`/`LoadKitFromFolder` (.sfz/.dspreset/folder).
- ✅ **Kit save** — `drummachine/io.go:SaveMachine` (machine.json with full Kit).
- 🟡 **Sample browser** — `cmd/drummachine/gui_kit.go` + canvas browser (samplelib search/scan). GUI-only-unverifiable on cloud.
- ✅ **Per-pad tune / pitch** — `Pad.PitchSemitones`, `SetPadPitch`.
- 🟡 **Per-pad envelope (attack/decay)** — `Pad.Decay` is decay-only; full ADSR only via imported `Sound.AmpEnv` (`sampler` A/H/D/S/R, applied by `sampler_engine.go`). No per-pad attack control in the pad model.
- ✅ **Per-pad reverse** — `sampler.Variant.Reverse`, applied in `sampler_engine.go`.
- ✅ **Velocity layers / round-robin** — `sampler.Layer`/`Variant`, `SelectVariantRandom` (seeded), engine picks layer-by-velocity then variant-by-RR (`sampler_engine.go`).

### Performance & mixing
- ✅ **Mute per lane** — `Pad.Mute`, `MutePad`.
- ✅ **Solo per lane** — `Pad.Solo`, `SoloPad`, `AudiblePads`.
- 🟡 **Per-lane volume / gain** — `Pad.Level` (drummachine, applied in render); `beatgen` lanes have no volume. OK on the playable model.
- ✅ **Per-lane pan** — `Pad.Pan`, `SetPadPan` (carried to `MachineEvent.Pan`; stereo render Phase-2 — currently mono).
- ✅ **Choke groups** — `Pad.ChokeGroup` + SFZ `group`/`off_by` (`ResolveChokes`, `applyChokeCutoffs`).
- ❌ **Live record / step input** — no live MIDI/real-time input; sequencer is click-to-toggle only.
- ❌ **Note repeat / roll** — none (ratchet ≠ note repeat).

### Generative / variation
- ✅ **Randomize** — `beatgen.Generate` (role-weighted), `GenerateGenre`; canvas [Random] button.
- ✅ **Mutate / vary** — `beatgen.Mutate`, `Remix`.
- 🟡 **Fills** — no dedicated Fill type; `Busier` increases density; no end-of-phrase fill op/UI.
- ✅ **Euclidean fill** — `beatgen.ApplyEuclidean` (Bjorklund); `ctledit OpEuclidLane`.
- ✅ **Density control** — `beatgen.Lane.Density`, `SetDensity`/`Busier`/`Sparser`.
- ✅ **Rotate / shift** — `beatgen.Rotate`.
- ✅ **Genre presets** — `beatgen.GenerateGenre` (house/trap/dnb/straight…); canvas [House]/[Trap]/[4-Floor].

**Top missing for functional (drum machine):**
1. **Per-step detail GUI** — velocity/pitch/pan/probability/ratchet/flam exist in `beatgen` but **no UI edits them**; and the `ToDrumGrid` round-trip drops them.
2. **Live record / step input** + **note repeat/roll** — no real-time playing into the grid.
3. **Triplet / finer step resolution** — only 1/16.
4. **Clear pattern** + dedicated **Fills**.
5. **Unify the two models** — the playable `drummachine` and the rich `beatgen` should be one, or the rich step data is unreachable in practice.

---

## 3. Piano roll / MIDI editor

> Strong immutable model (`internal/pianoroll`) + a real Gio panel (`gui_pianopanel.go`). No audible
> playback to verify edits; several editor staples are model-missing.

### Note editing
- ✅ **Draw note** — `pianoroll.Add`; `gui_pianopanel.go:handlePress` (double-click creates).
- 🟡 **Select note(s)** — click + shift selection (`selectedIDs`, `hitTestBody`); **no marquee/lasso**.
- ✅ **Move note** — `pianoroll.Move`; GUI body-drag (`prDragBody`).
- ✅ **Resize note** — `pianoroll.Resize`; GUI edge-drag (`prDragEdge`).
- ✅ **Delete note** — `pianoroll.Delete`; GUI Delete key.
- ❌ **Duplicate notes** — no `Duplicate` in `pianoroll`/`dawmodel`/`ctledit`; no UI.
- ❌ **Mute note** — `Note` has no mute field.

### Velocity & expression
- 🟡 **Velocity editing** — `pianoroll.SetVelocity` + a velocity lane is *drawn* (`drawVelLane`), but the lane drag-to-set is **not wired** (set only via op).
- ❌ **Velocity ramp / scale** — no `ScaleVelocity`/`RampVelocity`.
- ❌ **Note-off / release velocity** — `Note` has no release-velocity field.

### Timing & pitch tools
- ✅ **Quantize (with strength)** — `pianoroll.Quantize` (0..1 strength).
- ✅ **Quantize start/end independently** — `pianoroll.QuantizeEnds`.
- ✅ **Transpose** — `pianoroll.Transpose`/`TransposeNotes`; `ctledit OpTranspose`.
- ✅ **Humanize** — `pianoroll.Humanize` (seeded).
- ✅ **Legato / fill gaps** — `pianoroll.Legato`.
- ❌ **Glue / merge** — no `Glue`/`Merge`.
- ✅ **Split / cut note** — `pianoroll.Split`/`SplitAll`.

### Musical assistance
- 🟡 **Scale highlighting** — keyboard rows draw black/white (`drawRows`, `prBlackKey`), but they do **not** highlight a chosen scale (`musictheory.InScale` exists but is **never called** from the piano roll).
- ❌ **Snap-to-scale** — `musictheory.InScale` is orphaned; no `SnapToScale` constraint.
- ❌ **Chord tools** — no insert/strum chords in the piano roll.
- ❌ **Note repeat / arpeggiator** — none.
- ❌ **Strum / flam offset** — flam is drums-only (`beatgen/flam.go`); none in piano roll.

### Automation & control
- ❌ **CC lanes** — no CC event model.
- ❌ **Pitch-bend lane** — none.
- ❌ **Automation lanes** — none.

### Navigation
- 🟡 **Horizontal zoom/scroll** — `pxPerTick` + scroll handling. GUI-unverifiable.
- 🟡 **Vertical zoom/scroll** — pitch range auto-fit once (`autoFitPitch`); **not interactive** after.
- ❌ **Keyboard preview** — gutter keys drawn but no click-to-audition (no audio path).
- 🟡 **Grid / snap resolution** — fixed 1/16 (`xToTick` ppq/4); no user selector.
- ❌ **Follow playhead** — no playhead tracked in the piano panel.

(MIDI I/O: ✅ `pianoroll.FromSMF`/`ToFile`, round-trip byte-stable.)

**Top missing for functional (piano roll):**
1. **Keyboard preview / any audible playback** — you can't *hear* the notes you edit.
2. **Snap-to-scale + real scale highlighting** — the theory exists (`musictheory.InScale`) but is unwired.
3. **Duplicate, Mute note, Glue/merge** — basic editor staples absent in the model.
4. **Velocity lane drag** (drawn but not interactive) + **velocity ramp/scale**.
5. **Chord tools / arpeggiator** and **marquee selection**.
6. **CC / pitch-bend / automation lanes** (entirely absent).

---

## 4. Mixer

> Split into a strong **data layer** (faders/pan/mute/solo/bus/sidechain/FX as a routing DAG) and an
> almost-empty **runtime layer**: the Go playback engine is synthesis-only — **no per-channel
> gain/pan/EQ/FX DSP, no metering**. FX chains and EQ are *declared manifest metadata* that nothing
> renders in real time; VST hosting is offline-render only.

### Channel strip
- ✅ **Volume fader** — `dawmodel/mixer.go:Strip.Gain`; `gui_mixerpanel.go` fader → `SetGain`.
- ✅ **Pan control** — `Strip.Pan`; GUI slider → `SetPan` (audiotrack mixdown applies constant-power pan, but the live synth engine is mono).
- ✅ **Mute** — `Strip.Mute`; GUI toggle → `SetMute` (respected by what notes play).
- 🟡 **Solo** — `Strip.Solo`, `SoloedTracks`, GUI toggle; **no solo-safe**, and the engine doesn't truly isolate audio (no per-channel render).
- ❌ **Record-arm** — no `RecordArm` field; no recording-to-channel.
- ❌ **Phase / polarity invert** — no field, no DSP.
- ✅ **Input/output routing** — `Strip.Bus`, `RouteTo`; track→bus→master DAG.

### Metering
- ❌ **Per-channel level meter** — no live level display; no per-channel level capture.
- ❌ **Master / output meter** — none live.
- 🟡 **Peak / clip indicator** — only `audiohost.RenderResult.Peak`/`PeakDb` from **offline** VST renders; no real-time clip detection.
- ❌ **Loudness (LUFS/RMS) metering** — no LUFS; RMS only measured on offline render output, not live per channel.

### Routing & structure
- ✅ **Buses / group tracks** — `dawmodel/mixer.go:Bus`; `music.ProjBus`; `mixplan` canonical bus IDs.
- ❌ **Sends / returns (aux)** — no send/return field; only bus routing and sidechain edges.
- ❌ **Pre/post-fader sends** — no sends at all.
- 🟡 **Sidechain routing** — `Bus.Sidechain` ([]source IDs), `ProjEdge{Kind:"sidechain"}` — a **declared control edge only**; no real audio sidechain tap/ducking in the engine.
- ✅ **Master bus** — `mixplan.BusMaster` + `masterChain` (eq→glue→limiter as data) + master strip fader.
- ✅ **Sub-bus nesting** — `ProjBus.Out` chains buses (e.g. gtrRhythm→master→out.main).

### Processing & staging
- 🟡 **Insert FX chain** — `mixplan.FXNode` + role chains (`drumsChain`, `kickChain`…) and `ProjBus.FX`; **declared as data only — no runtime DSP** runs the eq/comp/sat.
- ❌ **FX bypass / reorder** — no bypass flag; chain order is read-only.
- 🟡 **Gain staging / trim** — `Strip.Gain` + `studio` "gain stage" adds a `gain:<dB>` FX node (data); no real-time dB trim DSP / display.
- 🟡 **EQ per channel** — EQ declared as FX-node params (`mixplan`); **no audible IIR/FFT EQ** in the Go engine.
- 🟡 **Plugin / VST insert** — `internal/audiohost` (C++ sidecar) scans/loads/renders VST3 **offline only** (`client.Render` → WAV); no live VST hosting during playback.
- ❌ **Preset save/recall for the strip** — no strip-preset concept.

**Top missing for functional (mixer):**
1. **A real-time mix engine** — per-channel gain/pan/EQ/FX actually applied to audio during playback (today nothing renders the routing DAG).
2. **Metering** — per-channel + master level meters, peak/clip, LUFS/RMS (none live).
3. **Sends / returns (aux)** + pre/post-fader — entirely absent.
4. **Audible sidechain ducking** — currently only a declared edge.
5. **FX bypass/reorder** and **strip preset save/recall**.
6. **Record-arm + phase invert**.

---

## 5. Video editor / NLE

> Two tools: **becky-clip** (forensic transcript-based compilation — genuinely production-ready) and
> **becky-nle** (a single-source mark-in/out wedge). The compile/render engine (`internal/reel`) is
> strong; true multi-track NLE editing is largely absent.

### Media & project
- ✅ **Import media** — `cmd/clip/app.go:AddClip`; `footage/index.go:Index` walks the folder.
- 🟡 **Media bin / browser** — becky-clip has a read-only transcript-searchable index; no drag-drop bin for becky-nle.
- ✅ **Proxy media** — `internal/reel/proxy.go:Proxy` (H.264 proxy for non-web-safe codecs).
- 🟡 **Project save/load** — becky-clip saves reel JSON (`edl/io.go`); becky-nle writes a `.kdenlive`/MLT XML project (`kdenlive/xml.go:WriteProject`) but has no in-memory session persistence across runs.
- ✅ **Metadata / probe** — `mediainfo.Probe` (ffprobe: duration/fps/res/HasVideo/HasAudio); frame grab `reel/frame.go:GrabFrame`.

### Timeline
- ❌ **Multi-track timeline** — `kdenlive/xml.go` hard-codes one video track; no audio/V2/V3 layers.
- ❌ **Add/remove/reorder tracks** — single-track design; no track ops.
- 🟡 **Drag clip to timeline** — becky-clip reorders programmatically (`Reorder`) and the GUI supports chip→timeline drag (per CLAUDE.md), but becky-nle has only scrub drag; GUI-unverifiable.
- ❌ **Audio waveform on clips** — no waveform rendering on timeline clips.
- ❌ **Video thumbnail on clips** — timeline shows a plain duration block, no frame thumbnails.
- ❌ **Track lock / enable / mute** — no per-track controls.

### Editing operations
- ❌ **Cut / razor** — no split-at-playhead.
- ✅ **Trim** — `cmd/clip/app.go:SetTrim`; `cmd/becky-nle:exportRange` for marked range; clip-edge drag in becky-clip (per CLAUDE.md).
- ❌ **Ripple edit** — none.
- ❌ **Roll edit** — none.
- ❌ **Slip / slide** — none.
- ✅ **Move / reorder clips** — `cmd/clip/app.go:Reorder`.
- 🟡 **In/out points** — `edl.Clip{In,Out}` (per-clip source window) ✅; becky-nle timeline in/out marks `SetIn`/`SetOut` are playhead boundaries for export, not full insert in/out.
- ❌ **Insert / overwrite** — no insert/overwrite modes.
- 🟡 **Delete / lift / extract** — `RemoveClip` deletes; no lift-vs-extract (gap-leave vs ripple-close) distinction.

### Playback & navigation
- ✅ **Playhead / scrub** — `cmd/becky-nle/gui_timeline.go:captureScrub`; `nle.go:SetPlayhead`.
- ✅ **Frame-accurate seek** — `videopreview.Frame` (sidecar GPU decode), frame-accurate ffmpeg `-ss` before `-i`.
- 🟡 **Preview / program monitor** — single-frame preview from the sidecar; no split program/source monitors.
- ❌ **Source monitor** — none.
- ✅ **Timecode display** — `becky-nle:formatTC` (H:MM:SS.mmm), `edl/timecode.go` SMPTE.
- ❌ **Zoom / scroll timeline** — fixed duration-proportional scale; no zoom/scroll.

### Effects & enhancement
- ❌ **Transitions** — cut-only; no dissolve/wipe.
- ❌ **Speed / retime** — none.
- 🟡 **Titles / text overlays** — no general titler; but `reel/drawtext.go:lowerThirdFilter` burns a forensic lower-third on export.
- ✅ **Forensic / burn-in overlay** — `reel/drawtext.go` (running original-file timecode + filename/date/person/location/link) via ffmpeg drawtext. becky's signature feature.
- ❌ **Basic color / brightness** — no color correction.
- 🟡 **Crop / scale / position** — no per-clip crop; export uniformly scales (aspect-preserve+pad) via `reel/args.go`.
- 🟡 **Audio level per clip** — no per-clip gain; overall loudness measured post-render (`mediainfo.MeanVolume`).

### Export
- ✅ **Export / render** — `reel/reel.go:Render` (multi-source frame-accurate compile); `kdenlive/render.go:Render` (melt).
- ✅ **Codec selection** — `reel.Options.Codec` (h264_nvenc→libx264 fallback).
- ✅ **Resolution / frame-rate options** — `reel.Options{Width,Height,FPS}` + scale/fps filters.
- ✅ **Bitrate / quality options** — `reel/args.go:codecQualityArgs` (CQ/CRF or explicit bitrate).
- ✅ **Export range** — `becky-nle:exportRange`; becky-clip exports the reel.
- ✅ **Audio in export** — `reel/args.go` interleaves per-clip audio, silence-fills gaps, AAC 192k.
- ✅ **Export verification** — `cmd/clip/export.go:verifyExportAudio` (ffprobe + volumedetect).

**Top missing for functional (NLE):**
1. **Multi-track timeline** (audio + V2/V3) — the single-track design blocks real editing.
2. **Razor/cut, ripple, roll, slip/slide, insert/overwrite** — core NLE edit ops absent.
3. **Waveform + thumbnail on timeline clips** and **timeline zoom/scroll**.
4. **Transitions + speed/retime + per-clip color/crop/audio gain**.
5. **Source/program monitors**.
(becky-clip's forensic compile→render→EDL/SRT path is the production-ready piece.)

---

## 6. Audio / sample editor

> Strong analysis + immutable multitrack-region model; the *destructive sample-editor* surface is
> thin. Pitch/time engines are **stubs** (the math is labeled, not implemented).

### Display & navigation
- 🟡 **Waveform view** — `audiotrack/peaks.go:BuildClipPeaks`/`BuildRegionPeaks` produce min/max columns (data only); the canvas audio panel renders them (GUI-unverifiable).
- 🟡 **Zoom / scroll** — peaks support variable column width; no time-scale zoom math in the engine (UI concern).
- ❌ **Spectral view** — no spectrogram display (FFT exists for analysis, not as a view).
- 🟡 **Selection** — `Region.SourceIn/SourceOut` source window exists; no discrete time-range selection object.
- ❌ **Snap to zero-crossing** — no zero-crossing detection.

### Destructive / corrective edits
- ✅ **Trim / crop** — `audiotrack/edit.go:TrimRegionStart`/`TrimRegionEnd` (immutable).
- 🟡 **Cut / copy / paste** — split+remove/add exist (`SplitRegion`+`RemoveRegion`+`AddRegion`); no clipboard data structure.
- 🟡 **Silence / delete** — delete = `RemoveRegion`; no "silence a range within a region".
- ✅ **Fade in / fade out** — `Region.FadeInFrames`/`FadeOutFrames`, `gainAt` linear ramp, `SetRegionFades`, applied in `mixdown.go`.
- ❌ **Crossfade** — no crossfade; overlapping regions hard-sum.
- 🟡 **Gain / normalize** — gain ✅ (`Region.Gain`, `SetRegionGain`); **audio normalization not implemented** (`stemscan`/`refmatch` only *suggest* gain).
- 🟡 **DC offset removal** — measured (`stemscan.fillLevels` DCOffset, `refmatch`) but **not removed**.
- 🟡 **Reverse** — `sampler.Variant.Reverse` (metadata, engine applies for sampler playback); no in-audiotrack reverse op.

### Pitch & time
- ❌ **Time-stretch** — not implemented; `vox/pitch.go` labels engines (WORLD/TD-PSOLA/Rubber Band) as **stubs**.
- ❌ **Pitch-shift** — not implemented; `vox` decides the move (`MoveCents`), the renderer is a stub.
- 🟡 **Tempo detect / set** — detect ✅ (`dsp.OnsetTimes` + `hum/tempo.go:EstimateTempo`); no time-scale "set tempo" edit (that needs time-stretch).

### Slicing & looping
- ❌ **Slice to grid** — no slice-to-grid math.
- 🟡 **Transient detection** — `dsp.OnsetTimes` (spectral-flux peaks) exists but is **not wired to slicing**.
- 🟡 **Loop points** — `sampler.Variant.LoopMode`/`LoopStart`/`LoopEnd` (model only; engine playback of loops is external/stubbed).
- ❌ **Loop crossfade** — no loop-seam crossfade.
- 🟡 **Export slices / region** — `audiotrack/mixdown.go:MixdownWAV` bounces a project to WAV; one region exports only by building a one-region project (no direct slice export).

**Top missing for functional (audio editor):**
1. **Pitch-shift + time-stretch** — labeled stubs; the headline DSP is not implemented (blocks VocALign/Melodyne-class work).
2. **Normalize, DC-offset removal, reverse** as real corrective ops on a waveform.
3. **Crossfade** (region + loop seam).
4. **Slice-to-grid** (transient detection exists but isn't wired).
5. **Real selection + zero-crossing snap + spectral view**.
6. **A waveform editor surface** that actually executes destructive edits (today it's region-arrange + analysis).

---

## BIGGEST GAPS — build these next (prioritized across all sections)

Ordered by how much each blocks "this is functional software a creative would trust."

1. **A real-time playback/mix engine.** The single biggest hole. The DAW/mixer can model faders,
   pan, EQ, FX, sidechain — but **nothing renders that DAG in real time**; the engine is synth/sampler
   one-shot render-and-play. Without it there is no metering, no audible EQ/FX, no true transport.
2. **Save the session from the GUI.** The canvas loads + edits an arrangement but **cannot save it**.
   Edit-with-no-save is not software. (Plus Save-as, recent, auto-save.)
3. **A real transport** — scrubable playhead, Stop/Pause, loop region, return-to-start. Today ▶ is
   "render a fixed-loop and play"; ■ kills a process. This underpins every other tool.
4. **Metronome + count-in + record-arm-to-track.** Recording is mic→WAV only; there is no click and
   no way to record into the session. Core to any DAW/drum workflow.
5. **Mixer metering** (per-channel + master level, peak/clip, LUFS/RMS) — there is *zero* live metering.
6. **Per-step drum GUI + model unification.** `beatgen` has velocity/pitch/pan/probability/ratchet/flam
   but **no UI touches them** and the `drummachine`↔`beatgen` bridge drops them — so the richness is
   unreachable in practice.
7. **Piano-roll audibility + scale tools.** No way to *hear* edited notes (keyboard preview); and
   snap-to-scale/scale-highlighting are unwired despite `musictheory.InScale` existing.
8. **Clipboard editing everywhere** — Copy / Cut / Paste / Duplicate / Select-range are largely absent
   across arrangement, piano roll, and audio editor.
9. **NLE multi-track + core edit ops** — single-track only; no razor/ripple/roll/slip/insert,
   no waveform/thumbnail on clips, no timeline zoom. (becky-clip's forensic compile path is the
   one production-ready exception.)
10. **Audio pitch-shift + time-stretch** — the labeled stubs in `vox`/`sampler` block the
    Melodyne/VocALign-class audio editing becky aims for.
