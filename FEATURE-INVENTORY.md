# FEATURE-INVENTORY.md — the canonical "definition of functional"

This is the exhaustive checklist of every basic/standard feature a user expects from each
class of tool becky-tools is building. It is the **complete target list** — the bar a
creative coming from Ableton Live, FL Studio, Cubase, Maschine, Hydrogen, Audiomodern
Playbeat, Premiere, or DaVinci Resolve already takes for granted. It deliberately does NOT
assess becky's current implementation state; a separate gap analysis compares against it.
If a feature here is missing, that tool class is not yet "functional" — only when every box
that applies is real, visible, and working does the tool count as done.

---

## 1. DAW / transport core

### Transport
- [ ] Play — start playback from the playhead/cursor position.
- [ ] Stop — halt playback and (optionally) return the playhead to its start.
- [ ] Pause/resume — suspend playback without resetting position.
- [ ] Loop / cycle — repeat a defined region continuously during playback.
- [ ] Set loop region — define the loop start/end from the timeline or a selection.
- [ ] Record — capture incoming audio/MIDI to the armed track(s).
- [ ] Record-arm per track — enable/disable a track to receive a recording.
- [ ] Metronome / click — audible tempo click that can be toggled on/off.
- [ ] Count-in — a lead-in bar (or bars) of clicks before recording starts.
- [ ] Playhead positioning — click/scrub the timeline to move the play position.
- [ ] Return-to-start / go-to-end — jump the playhead to the project boundaries.
- [ ] Punch in/out — auto-arm recording only within a defined region.

### Tempo & time
- [ ] Set tempo (BPM) — global project tempo, numeric and tappable.
- [ ] Tempo automation / tempo map — tempo changes over the arrangement.
- [ ] Time signature — set the meter (e.g. 4/4, 3/4, 6/8).
- [ ] Time-signature changes — different meters at different bars.
- [ ] Tap tempo — derive BPM by tapping in time.

### Project / session management
- [ ] New project — create an empty session with sane defaults.
- [ ] Save project — persist the full session state to disk.
- [ ] Save-as / versioning — save a copy under a new name.
- [ ] Load / open project — restore a previously saved session.
- [ ] Recent projects — quick-open list of recently used sessions.
- [ ] Auto-save / crash recovery — periodic background save and recovery on restart.
- [ ] Project settings — sample rate, bit depth, output device, default paths.

### Editing
- [ ] Undo — revert the last action.
- [ ] Redo — reapply a reverted action.
- [ ] Multi-level undo history — step back/forward through many actions.
- [ ] Copy — copy selected clips/events to the clipboard.
- [ ] Cut — remove and copy selected clips/events.
- [ ] Paste — insert clipboard contents at the playhead/selection.
- [ ] Duplicate — copy a selection in place to the next position.
- [ ] Delete — remove selected clips/events.
- [ ] Select all / range select — select everything or a time range across tracks.

### Tracks
- [ ] Add track — create audio/MIDI/instrument/bus tracks.
- [ ] Delete track — remove a track and its contents.
- [ ] Reorder tracks — drag tracks up/down to change order.
- [ ] Rename track — set a human-readable track name.
- [ ] Color track — assign a color for visual identification.
- [ ] Track height / resize — expand/collapse a track lane.
- [ ] Freeze / flatten track — render a track to audio to save CPU.

### Arrangement & navigation
- [ ] Arrangement timeline — horizontal bar/beat ruler with clips placed in time.
- [ ] Markers — named points on the timeline for navigation.
- [ ] Regions / sections — named arrangement spans (intro/verse/chorus).
- [ ] Loop browser / clip launcher — optional non-linear clip triggering surface.
- [ ] Snap / grid — constrain edits to a musical grid resolution.
- [ ] Quantize (clip/event) — align audio/MIDI to the grid.
- [ ] Zoom (horizontal/vertical) — change the visible time/track span.
- [ ] Scroll / pan — move the view across the arrangement.

### Output
- [ ] Bounce / render to audio — render the mix or a selection to a WAV/MP3 file.
- [ ] Stem export — render each track/bus to a separate audio file.
- [ ] Export range — render only a defined time region.
- [ ] Export format options — sample rate, bit depth, codec, channels.
- [ ] MIDI export — write the arrangement (or a track) to a Standard MIDI File.
- [ ] Normalize / loudness target on export — optional level normalization.

---

## 2. Drum machine / step sequencer

### Pads & lanes
- [ ] Pad grid — a bank of triggerable pads (e.g. 4×4 / 16 pads).
- [ ] Lanes — one row per drum voice in the step grid.
- [ ] Per-lane label / name — show which sound each lane plays.
- [ ] Add/remove lane — change the number of voices in the kit.

### Step grid
- [ ] Step on/off — toggle a hit at each grid step.
- [ ] Pattern length — set the number of steps (e.g. 16/32/64).
- [ ] Step resolution — 1/8, 1/16, 1/32, triplet grids.
- [ ] Bar paging / scroll — view and edit patterns longer than one bar.
- [ ] Per-step velocity — set the loudness of each individual hit.
- [ ] Per-step probability — chance a step actually triggers.
- [ ] Per-step microtiming / nudge — shift a step slightly ahead/behind the grid.
- [ ] Per-step ratchet / retrigger — subdivide one step into N fast repeats.
- [ ] Per-step flam — a quick grace-note before the main hit.
- [ ] Per-step pitch / tune — re-pitch an individual hit.
- [ ] Per-step pan — place an individual hit in the stereo field.

### Patterns & song
- [ ] Multiple patterns — store and switch between several patterns.
- [ ] Pattern select / bank — organize patterns into banks/scenes.
- [ ] Copy / paste pattern — duplicate a pattern to a new slot.
- [ ] Clear pattern — empty a pattern.
- [ ] Pattern chaining / song mode — sequence patterns into a full arrangement.
- [ ] Per-pattern length & tempo feel — independent length per pattern.

### Groove & humanization
- [ ] Swing — shift off-beats for a shuffled feel (with a documented 0%/50% convention).
- [ ] Global humanize — apply subtle timing/velocity variation.
- [ ] Accent — emphasize hits on chosen steps.
- [ ] Shuffle/groove templates — apply a named groove feel.

### Sounds & kit
- [ ] Per-pad sample load — assign an audio file to a pad.
- [ ] Kit load — load a whole drum kit (SFZ/folder/preset) at once.
- [ ] Kit save — save the current pad assignments as a reusable kit.
- [ ] Sample browser — search/preview and drag samples onto pads.
- [ ] Per-pad tune / pitch — re-pitch a pad's sample.
- [ ] Per-pad envelope (attack/decay) — shape a pad's amplitude.
- [ ] Per-pad reverse — play a pad's sample backwards.
- [ ] Velocity layers / round-robin — multiple samples per pad by velocity/rotation.

### Performance & mixing
- [ ] Mute per lane — silence a voice.
- [ ] Solo per lane — isolate a voice.
- [ ] Per-lane volume / gain — balance the voices.
- [ ] Per-lane pan — place a voice in the stereo field.
- [ ] Choke groups — one hit cuts off another (open/closed hat).
- [ ] Live record / step input — record hits in real time into the grid.
- [ ] Note repeat / roll — held repeats at a chosen rate.

### Generative / variation
- [ ] Randomize — generate a random pattern (optionally per-lane).
- [ ] Mutate / vary — produce variations of the current pattern.
- [ ] Fills — insert an end-of-phrase fill.
- [ ] Euclidean fill — distribute N hits evenly across the steps.
- [ ] Density control — make a pattern busier or sparser.
- [ ] Rotate / shift — rotate a lane's hits forward/back.
- [ ] Genre presets — seed a pattern in a named style.

---

## 3. Piano roll / MIDI editor

### Note editing
- [ ] Draw note — add a note with the pencil/click.
- [ ] Select note(s) — click, marquee, or lasso to select.
- [ ] Move note — drag selected notes in pitch and time.
- [ ] Resize note — drag a note edge to change its length.
- [ ] Delete note — remove selected notes.
- [ ] Duplicate notes — copy a selection in place.
- [ ] Mute note — silence a note without deleting it.

### Velocity & expression
- [ ] Velocity editing — set per-note velocity (lane or drag).
- [ ] Velocity ramp / scale — apply a gradient or scale across a selection.
- [ ] Note-off / release velocity — where supported.

### Timing & pitch tools
- [ ] Quantize — snap notes to the grid (with strength).
- [ ] Quantize start/end independently — align onsets and/or releases.
- [ ] Transpose — shift selected notes by semitones/octaves.
- [ ] Humanize — add subtle timing/velocity randomness.
- [ ] Legato / fill gaps — extend notes to the next note.
- [ ] Glue / merge — join adjacent notes.
- [ ] Split / cut note — slice a note at a point.

### Musical assistance
- [ ] Scale highlighting — shade in-key vs out-of-key rows.
- [ ] Snap-to-scale — constrain drawn/moved notes to a scale.
- [ ] Chord tools — insert/strum chords from a root.
- [ ] Note repeat / arpeggiator — generate repeats/arps from held notes.
- [ ] Strum / flam offset — offset chord notes for a strum feel.

### Automation & control
- [ ] CC lanes — edit MIDI CC (mod, expression, etc.) over time.
- [ ] Pitch-bend lane — draw pitch-bend curves.
- [ ] Automation lanes — per-parameter automation curves.

### Navigation
- [ ] Horizontal zoom/scroll — change the visible time span.
- [ ] Vertical zoom/scroll — change the visible pitch range.
- [ ] Keyboard preview — click the piano keys to audition pitches.
- [ ] Grid / snap resolution — choose the editing grid.
- [ ] Follow playhead — auto-scroll during playback.

---

## 4. Mixer

### Channel strip
- [ ] Volume fader — per-track level control.
- [ ] Pan control — per-track stereo placement.
- [ ] Mute — silence a channel.
- [ ] Solo — isolate a channel (with solo-safe where applicable).
- [ ] Record-arm — arm a channel for recording.
- [ ] Phase / polarity invert — flip a channel's polarity.
- [ ] Input/output routing — select the channel's source and destination.

### Metering
- [ ] Per-channel level meter — visual loudness per channel.
- [ ] Master / output meter — overall output level.
- [ ] Peak / clip indicator — flag overs.
- [ ] Loudness (LUFS/RMS) metering — program loudness readout.

### Routing & structure
- [ ] Buses / group tracks — sub-mix multiple channels together.
- [ ] Sends / returns (aux) — feed a shared effect (reverb/delay).
- [ ] Pre/post-fader sends — choose where the send taps the signal.
- [ ] Sidechain routing — feed one channel's signal to another's processor.
- [ ] Master bus — the final summed output channel.
- [ ] Sub-bus nesting — buses feeding other buses.

### Processing & staging
- [ ] Insert FX chain — per-channel chain of effects in series.
- [ ] FX bypass / reorder — toggle and reorder inserts.
- [ ] Gain staging / trim — input gain ahead of the chain.
- [ ] EQ per channel — at least a basic per-channel EQ.
- [ ] Plugin / VST insert — host an external instrument/effect on a channel.
- [ ] Preset save/recall for the strip — store a channel's settings.

---

## 5. Video editor / NLE

### Media & project
- [ ] Import media — bring video/audio/image files into the project.
- [ ] Media bin / browser — organize imported clips.
- [ ] Proxy media — generate/use low-res proxies for smooth editing.
- [ ] Project save/load — persist and restore the edit.
- [ ] Metadata / probe — read codec, resolution, frame rate, duration.

### Timeline
- [ ] Multi-track timeline — multiple stacked video and audio tracks.
- [ ] Add/remove/reorder tracks — manage timeline tracks.
- [ ] Drag clip to timeline — place media onto a track at a position.
- [ ] Audio waveform on clips — show the clip's waveform for sync/trim.
- [ ] Video thumbnail on clips — show frames on the clip body.
- [ ] Track lock / enable / mute — control individual tracks.

### Editing operations
- [ ] Cut / razor — split a clip at the playhead.
- [ ] Trim — shorten/lengthen a clip from either edge.
- [ ] Ripple edit — trim and close/shift the gap automatically.
- [ ] Roll edit — move the cut point between two adjacent clips.
- [ ] Slip / slide — change a clip's content or position without changing duration.
- [ ] Move / reorder clips — reposition clips on the timeline.
- [ ] In/out points — mark source/timeline range for an insert.
- [ ] Insert / overwrite — add a clip pushing others or replacing under it.
- [ ] Delete / lift / extract — remove a clip with or without closing the gap.

### Playback & navigation
- [ ] Playhead / scrub — move and drag through the timeline.
- [ ] Frame-accurate seek — step exactly one frame forward/back.
- [ ] Preview / program monitor — watch the edited result.
- [ ] Source monitor — preview a clip before editing it in.
- [ ] Timecode display — current position in HH:MM:SS:FF.
- [ ] Zoom / scroll timeline — change the visible span.

### Effects & enhancement
- [ ] Transitions — cross-dissolve/cut/wipe between clips.
- [ ] Speed / retime — slow/fast motion and reverse.
- [ ] Titles / text overlays — add captions/lower-thirds/titles.
- [ ] Forensic/burn-in overlay — timecode, filename, date, person/location stamp.
- [ ] Basic color / brightness — minimal corrective adjustment.
- [ ] Crop / scale / position — reframe a clip.
- [ ] Audio level per clip — gain/keyframe a clip's audio.

### Export
- [ ] Export / render — produce a finished file from the timeline.
- [ ] Codec selection — choose the output codec (h264, hevc, etc.).
- [ ] Resolution / frame-rate options — set output dimensions and fps.
- [ ] Bitrate / quality options — control file size vs quality.
- [ ] Export range — render only in/out marked region.
- [ ] Audio in export — keep/encode audio with chosen codec/bitrate.
- [ ] Export verification — confirm the output has expected video+audio.

---

## 6. Audio / sample editor

### Display & navigation
- [ ] Waveform view — visual amplitude over time.
- [ ] Zoom / scroll — change the visible sample span.
- [ ] Spectral view — frequency-vs-time display (optional but standard).
- [ ] Selection — select a time range of the sample.
- [ ] Snap to zero-crossing — avoid clicks at edit boundaries.

### Destructive / corrective edits
- [ ] Trim / crop — keep only the selected region.
- [ ] Cut / copy / paste — clipboard edits within the waveform.
- [ ] Silence / delete — zero or remove a region.
- [ ] Fade in / fade out — apply an amplitude ramp at the edges.
- [ ] Crossfade — blend two regions/clips at a boundary.
- [ ] Gain / normalize — apply gain or normalize to a target peak/loudness.
- [ ] DC offset removal — center the waveform.
- [ ] Reverse — play the region backwards.

### Pitch & time
- [ ] Time-stretch — change duration without changing pitch.
- [ ] Pitch-shift — change pitch without changing duration.
- [ ] Tempo detect / set — derive or assign the sample's BPM.

### Slicing & looping
- [ ] Slice to grid — chop a loop into transient/grid-aligned slices.
- [ ] Transient detection — find onset markers.
- [ ] Loop points — set loop start/end for sustained playback.
- [ ] Loop crossfade — smooth the loop seam.
- [ ] Export slices / region — render a selection or slices to files.
