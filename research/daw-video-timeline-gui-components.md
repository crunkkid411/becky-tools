# DAW & Video-Timeline GUI Components — what to port into becky-canvas (Gio)

**Date:** 2026-06-22 · **Focus:** the GUI *widget* layer (timeline/track/clip, piano roll, drum grid, mixer, waveform, video NLE timeline).
**Sibling docs:** builds on `research/piano-roll.md` (piano-roll UX, already done) and `research/gui-toolkit.md` (toolkit choice = Gio). This doc is the *component/widget* survey those two don't cover.
**Bottom line:** there is **no reusable Go or Gio DAW/timeline/piano-roll/mixer/waveform *widget*** to drop in. The Go ecosystem has audio *engine/synthesis/MIDI* libs and waveform-*image* generators, but **zero interactive GUI track/timeline widgets**. Every reusable component is in TS/React, C++/Qt, or GTK. So the play is the same one the prior pass reached for the piano roll: **stop hand-building blind — pick ONE proven foreign component per surface and PORT its interaction model into Gio.** Render with Gio's `op`/`clip`/`paint` primitives; you are porting *UX + structure*, not code.

---

## Per-surface recommendation (read this table)

| becky surface | Go/Gio widget exists? | Best UX to PORT into Gio | License | Why |
|---|---|---|---|---|
| **Arranger / song timeline** (clips on track lanes, playhead, snap) | **No** | **Shotcut**'s QML timeline (model/view split: clips = repeated lane items over a time scale) for structure; **Zrythm** arranger for the *region* interaction grammar | Shotcut GPLv3 / Zrythm AGPLv3 (read for ideas; don't copy code) | Shotcut's MultitrackModel→QML-view split maps cleanly onto becky's `dawmodel.Arrangement` → Gio panel. Audio + video arrangers are the *same* widget. |
| **Piano roll** | **No** | **ryohey/signal** (already chosen) | **MIT** | Most complete reference: note grid, velocity lane, selection/drag/resize, ghost notes. Already greenlit — keep going. |
| **Drum grid / step sequencer** | **No** | **ryohey/signal** drum-lane mode + **ptcollab** step grid; interaction is a special case of the piano roll (fixed Y lanes) | signal MIT · ptcollab (C++/Qt, verify) | becky already has `gui_drumpanel.go`; the grid is a piano-roll with lanes, not a new widget. Bar-paging + velocity-per-step are the gaps. |
| **Mixer (channel strips)** | **No** | **LMMS FX-mixer** / **Zrythm mixer** layout conventions (fader+meter+pan+mute/solo+bus send, vertical strip) | LMMS GPLv2 / Zrythm AGPLv3 | becky has `gui_mixerpanel.go`; port the *strip layout + meter ballistics + bus-send affordance*, not code. Standard, well-settled UX. |
| **Waveform / audio-clip** | **Partial (image only)** | **bbc/peaks.js** interaction model (overview + zoomable detail, segment/point markers, scroll-wheel zoom) | peaks.js **LGPL-3.0** (study UX; rendering is yours) | Go has waveform→PNG libs (`mdlayher/waveform`) and becky has `audiotrack.BuildPeaks`. The *missing* piece is interactive zoom/scrub/markers — peaks.js is the canonical UX. |
| **Video NLE timeline** (ripple/roll/slip/trim, snap, thumbnails, multi-track) | **No** | **Shotcut** QML timeline (best-architected FOSS) for trim/ripple/snap grammar; **xzdarcy/react-timeline-editor** for a clean, minimal drag/resize/track widget to mirror | Shotcut GPLv3 · react-timeline-editor **MIT** | Same widget as the audio arranger. react-timeline-editor (~600★, MIT) is the cleanest *small* reference for raw drag/resize/track-row mechanics; Shotcut for the pro editing verbs. |

**One-line synthesis:** the *arranger, drum grid, and video NLE timeline are all the same widget* — a horizontal time-scaled lane stack with clips you drag/resize/snap. Build **one** Gio "track-lane timeline" component well (port Shotcut's structure + react-timeline-editor's drag mechanics), then specialize it per mode. Piano roll (signal) and mixer (LMMS/Zrythm conventions) are the two genuinely separate widgets. Waveform interactivity = peaks.js UX layered on becky's existing peaks data.

---

## 1. Go / Gio-native DAW or timeline widgets — the honest state

**There is no reusable interactive timeline/track/clip, piano-roll, mixer, or waveform GUI widget in Go or Gio.** Confirmed across awesome-go, the Gio community, Fyne extensions, and GitHub topic searches.

What Go *does* have (engine/data, not GUI):
- **Audio engine / synth / MIDI:** `bspaans/bleep` (synth+sequencer), `sinshu/go-meltysynth` (SoundFont MIDI synth, pure Go, no deps), `gpayer/go-audio-service`, `kellydunn/go-step-sequencer`, `faiface/beep`, `hajimehoshi/oto` (becky already uses this family). These make *sound*; none draw a timeline.
- **Waveform → static image (not interactive):** `mdlayher/waveform` (MIT, audio→PNG), `motoki317/go-waveform`, `cettoana/go-waveform`. Useful for precomputing peaks, **not** for scrub/zoom/markers. becky already has its own `internal/audiotrack.BuildPeaks` covering this.
- **Gio widget extensions (general-purpose, no DAW widgets):** `gioui-plugins/gio-plugins`, `jkvatne/gio-v`, `scartill/giox`. Buttons/lists/inputs — nothing timeline-shaped.
- **Fyne:** same — only a custom-widget API (`widget.BaseWidget` + `CreateRenderer`); no timeline/track/waveform widget in `fyne.io/x/fyne`. (And becky is on Gio, not Fyne, per GUI-RULES.)

**Conclusion:** any Go/Gio adoption is "use the audio engine libs becky already uses + write the GUI widget yourself." There is no shortcut at the widget layer in Go. The leverage is **porting a proven foreign interaction model**, not finding a Go package.

---

## 2. Video NLE timeline components (port the interaction model)

| Project | Tech | License | What's portable |
|---|---|---|---|
| **Shotcut** ([mltframework/shotcut](https://github.com/mltframework/shotcut/blob/master/src/qml/views/timeline/timeline.qml)) | C++/Qt + **QML** | GPLv3 | **The reference FOSS NLE timeline.** Clean model/view split: `MultitrackModel` (tracks+clips over MLT) → QML view of repeated lane items on a time scale. Study: ripple/roll/trim verbs, snapping, blank/clip distinction, track headers. QML loaded from disk → easy to read the layout logic. |
| **OpenShot** ([OpenShot/openshot-qt](https://github.com/OpenShot/openshot-qt)) | Python/Qt (new QWidget timeline backend, replacing a legacy web one) | GPLv3 | Newer QWidget timeline; SHIFT+scroll horizontal scroll, etc. Less clean than Shotcut but a second data point on track interaction. |
| **xzdarcy/react-timeline-editor** ([repo](https://github.com/xzdarcy/react-timeline-editor)) | React/TS | **MIT** | ~600★. The cleanest *small* reference for raw mechanics: drag-to-move, edge-drag-resize, multi-row tracks, time scale, snapping. Great to mirror for the becky lane widget with no GPL exposure. |
| **etro-js/etro** ([repo](https://github.com/etro-js/etro)) | TS | GPLv3 | A *render framework*, not a timeline widget — not a UX-port target. Skip for GUI. |
| **designcombo/react-video-editor** ([repo](https://github.com/designcombo/react-video-editor)) | React + Remotion | MIT (but pulls Remotion, which has its own license) | Capcut/Canva-style editor; frame-accurate drag-drop timeline, virtual scrolling for thousands of clips. Good for *aspirational* multi-track UX; the Remotion dependency makes it study-only. |
| Blender VSE / kdenlive timeline | C++ | GPL | Pro-grade but deeply engine-coupled; lower ROI to read than Shotcut's cleaner QML. becky already drives kdenlive headless (`internal/kdenlive`) — its *GUI* timeline isn't what you'd port. |

**Pick:** mirror **react-timeline-editor (MIT)** for drag/resize/track-row mechanics, and read **Shotcut's timeline.qml (GPLv3, ideas only)** for the pro editing verbs (ripple/roll/slip, snapping, blank handling).

---

## 3. DAW arranger / timeline / mixer components (port the interaction patterns)

| Project | Tech | License | Patterns to adopt |
|---|---|---|---|
| **ryohey/signal** ([repo](https://github.com/ryohey/signal)) | React/TS, **WebGL** rendering, MobX stores | **MIT** | Already chosen for piano roll. Its **arrange view** is also worth studying: tracks as horizontal lanes, region blocks, shared time scale with the piano roll. The store split (SongStore / PianoRollStore / ArrangeViewStore / ControlStore) is a clean model to mirror in becky's panel state. |
| **Zrythm** ([codeberg](https://codeberg.org/alextee/zrythm) / [git.zrythm.org](https://git.zrythm.org)) | C (GTK4; v2 moving to Qt/QML+JUCE) | **AGPLv3** | Timeline arranger = regions positioned against time; strong automation-lane and region-edit grammar. AGPL → **read for interaction ideas only, never copy**. |
| **LMMS** ([github](https://github.com/LMMS/lmms)) | C++/Qt | GPLv2 | Song editor + FX mixer; FL-Studio-like conventions. Mixer strip layout (fader/meter/pan/mute-solo/sends) is the well-settled UX to mirror. |
| **Helio** ([helio-fm/helio-workstation](https://github.com/helio-fm/helio-workstation)) | C++/JUCE | GPLv3 | Exceptionally clean, minimal linear/pattern sequencer UI; good reference for an *uncluttered* arranger aesthetic and the pattern-vs-linear duality. |
| **ptcollab** ([yuxshao/ptcollab](https://github.com/yuxshao/ptcollab)) | C++/Qt | (verify) | Collaborative piano-roll sequencer; clean step/note grid — a second piano-roll/drum-grid data point next to signal. |

**becky's `cmd/canvas` arranger/mixer panels should adopt:** (a) a single shared horizontal time scale across arranger + piano roll (signal does this); (b) region/clip blocks with a header lane + drag/resize/snap; (c) standard vertical mixer strips with meter ballistics and a visible bus-send affordance (LMMS/Zrythm); (d) pattern-vs-linear separation for the song view (Helio).

---

## 4. Waveform / audio-clip surface

- **Go reality:** waveform→image only (`mdlayher/waveform` MIT, etc.); becky already computes peaks (`audiotrack.BuildPeaks`). No interactive Go widget.
- **Port target — bbc/peaks.js** ([repo](https://github.com/bbc/peaks.js), **LGPL-3.0**, study UX only): the canonical interactive-waveform UX. Adopt its model — **overview panel + zoomable detail view**, **scroll-wheel/keyboard/touch zoom & scrub**, **point and segment markers** (non-destructive annotation: clip in/out, regions, cue points). Rendering stays yours in Gio over the existing peaks data. The bbc family also has `waveform-data.js` (resample/offset/segment API) and the C++ `audiowaveform` precomputer — useful as *data-shape* references for becky's peaks.

---

## Sources
- Gio: [gioui.org](https://gioui.org/), [widget pkg](https://pkg.go.dev/gioui.org/widget), [gio-plugins](https://github.com/gioui-plugins/gio-plugins), [gio-v](https://github.com/jkvatne/gio-v), [giox](https://github.com/scartill/giox)
- Go audio/waveform: [awesome-go audio](https://awesome-go.com/audio-and-music/), [mdlayher/waveform](https://github.com/mdlayher/waveform), [go-meltysynth](https://github.com/sinshu/go-meltysynth), [bleep](https://github.com/bspaans/bleep)
- Fyne custom widgets: [docs.fyne.io/extend/custom-widget](https://docs.fyne.io/extend/custom-widget/)
- Piano roll: [ryohey/signal](https://github.com/ryohey/signal) (MIT), [github topic: piano-roll](https://github.com/topics/piano-roll), [ptcollab](https://github.com/yuxshao/ptcollab)
- Video NLE: [Shotcut timeline.qml](https://github.com/mltframework/shotcut/blob/master/src/qml/views/timeline/timeline.qml), [OpenShot](https://github.com/OpenShot/openshot-qt), [xzdarcy/react-timeline-editor](https://github.com/xzdarcy/react-timeline-editor) (MIT), [etro](https://github.com/etro-js/etro), [designcombo/react-video-editor](https://github.com/designcombo/react-video-editor)
- DAW arrangers: [Zrythm](https://codeberg.org/alextee/zrythm) (AGPLv3), [LMMS](https://github.com/LMMS/lmms) (GPLv2), [Helio](https://github.com/helio-fm/helio-workstation) (GPLv3)
- Waveform UX: [bbc/peaks.js](https://github.com/bbc/peaks.js) (LGPL-3.0), [bbc/waveform-data.js](https://github.com/bbc/waveform-data.js), [bbc/audiowaveform](https://github.com/bbc/audiowaveform)
