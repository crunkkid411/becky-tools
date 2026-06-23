# DAW AI-control research — REAPER vs Ableton Live (vs Cubase) for a PROACTIVE engineer-assistant

**Date:** 2026-06-23 · **Branch:** `claude/ai-daw-integration-hh5y8l` · **Method:** 5-angle deep-research
fan-out (REAPER API, Ableton/AbletonOSC, habit-learning feasibility, Toaster/Strudel/Gurvich,
practicals/Cubase), primary sources, cross-verified. This is a **decision doc**, not a build order.

## The question Jordan actually asked

Not "can becky push MIDI into a DAW" — that's easy everywhere. The real question is whether a mature
host (Ableton, or staying on Cubase) can support becky's **vision**: *proactive, state-aware software —
not a chatbot — that shares the DAW's live state, reacts to events (the moment a vocal take finishes),
acts like an engineer who **learns my habits** (I always trim + lightly fade the head/tail of each take),
cleans the take automatically, and verifies it cut at the right spot.* Music is sloppy and iterative, so
this is "90% deterministic but checked," not pure determinism.

That vision has one load-bearing capability — **observe a recorded audio take → find where the real
sound starts/ends → trim it → add a small fade → re-check** — and it's the capability that splits the
three DAWs cleanly.

---

## Bottom line / recommendation

**REAPER is the clear host for this vision. Ableton cannot do the central use case at all, and Cubase
isn't in the running. Do not switch from Cubase to Ableton expecting AI take-cleanup — Ableton's API
structurally can't trim-and-fade a recorded take.**

The decision turns on one verified, triple-corroborated fact:

| Capability for "AI cleans my vocal take" | REAPER | Ableton Live | Cubase |
|---|---|---|---|
| Read recorded-audio **PCM samples** (find true start/end) | ✅ `GetAudioAccessorSamples` | ❌ LOM exposes no waveform/buffer | ❌ |
| **Trim** item edges from an external script | ✅ `D_POSITION`/`D_LENGTH`/`D_STARTOFFS` | ⚠️ start/end + warp markers only | ❌ |
| Set **fade length + shape/curve** | ✅ `D_FADEINLEN` + `C_FADEINSHAPE` (0–6) + `D_FADEINDIR` (−1..1) | ❌ **no fade member in the LOM at all** | ❌ |
| Full project read/write (MIDI notes, FX params, items) | ✅ full ReaScript API | ✅ via AbletonOSC/LOM (MIDI/session) | ❌ controller-mapping only |
| **Push** events (take finished / transport stop) | ⚠️ defer-poll; true push via small C++ surface ext | ✅ native LOM observers / OSC `start_listen` | ❌ |
| In-app **"becky panel"** | ✅ ReaImGui docked panel | ⚠️ Max-for-Live device (needs Suite/M4L) | ❌ |
| License | $60 personal, one-time | $79–$749; M4L = Suite | closed |
| Open / stable bridge | ✅ open API, low maintenance | ❌ closed; remote scripts break on major Live updates | n/a |
| Already in becky's stack | ✅ `SPEC-BECKY-REAPER`, `internal/reaper` | no | no |

**One sentence:** the only host where becky can *read the recorded audio, decide where to cut, and apply
a real fade with a chosen curve* — the entire point of the "engineer that cleans my takes" idea — is
REAPER, and becky already has a REAPER adapter to grow from.

---

## The five findings

### 1. REAPER — full read/write incl. the audio samples and fade curves *(high confidence — SDK header)*

A persistent external Go program can act as a proactive engineer over REAPER **without recompiling
REAPER**. Confirmed against the SDK header (`reaper_plugin_functions.h`) and `reascripthelp.html`:

- **Read everything:** tracks/items/takes (`CountTracks`, `GetMediaItemInfo_Value`, `GetActiveTake`),
  recorded-take source + offset (`GetMediaItemTake_Source`, `GetMediaSourceFileName`, take `D_STARTOFFS`),
  MIDI notes (`MIDI_GetNote`), FX params (`TrackFX_GetParam`), transport (`GetPlayState` bitmask
  1=play/2=pause/4=record, `GetPlayPosition`), selection/time range, markers/regions.
- **Write/edit everything:** trim via `D_POSITION`/`D_LENGTH` + take `D_STARTOFFS`; `SplitMediaItem`;
  **fades fully settable** — `D_FADEINLEN`/`D_FADEOUTLEN` (length), `C_FADEINSHAPE`/`C_FADEOUTSHAPE`
  ("shape, 0..6, 0=linear"), `D_FADEINDIR`/`D_FADEOUTDIR` (curvature −1..1); take vol/pan; `TrackFX_SetParam`;
  `MIDI_InsertNote`; `AddMediaItemToTrack`.
- **Read the actual audio samples in-script:** `CreateTakeAudioAccessor` + `GetAudioAccessorSamples`
  returns interleaved PCM. **So onset/silence detection can run inside the ReaScript itself** (scan the
  buffer for a threshold), then the same script trims and fades. Heavier DSP can instead read the source
  file path and analyze in Go/Python — but it isn't required; REAPER hands you the samples.
- **External control channel — the realistic architecture:** OSC and the web/HTTP interface are
  **action-limited** (fixed parameter set; OSC passes at most one arg) and **cannot call arbitrary API**.
  The full API is reachable only from a ReaScript. So the pattern is a **persistent in-REAPER ReaScript
  "broker"** running in a `reaper.defer()` loop, exchanging coarse-grained intents with becky's Go process
  over **file-IPC** (proven by `total-reaper-mcp`, `Reaper-MCP`) or a **socket via the free
  `js_ReaScriptAPI` extension** (a loadable plugin — *not* a REAPER recompile). `reapy` offers a
  Python-over-network variant. Keep messages coarse (one intent = many in-REAPER API calls); the bridge
  throughput is ~30–60 calls/s when driven chattily from outside, so do the work *inside* REAPER.
- **Events:** pure ReaScript is **defer-polling** (30–60 Hz easily catches record-stop: watch
  `GetPlayState` → 0 and a new item appearing). **True push** (record-stop callback) needs a thin C++
  `IReaperControlSurface` extension (`SetPlayState`, `SetTrackListChange`, `OnStopButton`) — again a
  loadable DLL, not a recompile. Start with polling; add the C++ surface later only if latency demands.
- **License:** $60 personal / $225 commercial, one-time, 60-day full eval. Open, stable API; lowest
  multi-year maintenance risk of the three.

### 2. Ableton — great for MIDI/session/events, **structurally blind to recorded-audio editing** *(high confidence — triple-corroborated)*

AbletonOSC (`github.com/ideoforms/AbletonOSC`, **actively maintained — commits through Nov 2025**,
"requires Live 11 or above" so Live 12 is covered) is a drop-in Remote Script (no recompile, UDP 11000/11001).
It's genuinely good at a lot:

- **Read/write** tracks, clips, **MIDI notes** (`/live/clip/get|add/notes`), devices/params, transport,
  tempo, scenes, selection; **create** clips/tracks; arm/record; fire clips.
- **Push events (better than REAPER here):** the LOM is observer-based (`add_value_listener`), surfaced by
  AbletonOSC as `start_listen`/`stop_listen` — you're *pushed* tempo/transport/recording-state/param/
  selection changes. (Clip-level listener coverage in the README is narrow — `playing_position` — so
  "clip added"/"recording stopped" may need song/track-level listening or a small Remote-Script fork.)

**But the decision-maker (#4) is a hard NO, corroborated by three independent signals** — the LOM Clip
reference (docs.cycling74.com), the cycling74 forum, and the absence of any fade endpoint in AbletonOSC:

- **No fade handles anywhere in the Live Object Model.** There is no `fade_in`/`fade_out` member on the
  Clip class. The only fades Live has are the automatic 0–4 ms anti-click edge fades — a UI behavior, not
  an API property. You **cannot set a fade on a clip from any external script or even from Max for Live.**
- **No waveform/sample access.** The LOM gives metadata only (`sample_length`, `sample_rate`, `file_path`,
  `gain`) — no audio buffer, no way to read where a recorded take's sound actually starts/ends. You can
  touch **warp markers** and `crop`, but not fades or raw audio.
- **Max for Live does NOT unlock this** — it rides the same Python Live API; the limit is in the LOM, not
  the transport. (M4L is Suite-only / a paid add-on; AbletonOSC alone covers the MIDI/session/event layer
  without M4L.)

**So "trim and fade my vocal take" is not achievable through Ableton's API.** You'd have to do it in Live's
UI by hand (exactly the manual step Jordan wants automated) or render the WAV and edit it in becky's own
engine outside Live — at which point Live isn't buying you the feature.

Also: closed source, and **remote scripts break across major Live updates** (Live 11's Python 2→3 killed
old scripts; Live 12's Python 3.11 broke tooling). That's a recurring maintenance tax you'd inherit.

### 3. The "engineer that learns my habits" use case is realistic — and there's a near-exact precedent *(medium-high confidence)*

- **DAWZY (arXiv 2512.03289, 2025)** is almost exactly becky's proposed architecture: natural-language/agent
  → "precise, context-aware, reversible **ReaScript** actions in REAPER," queries live session state, and
  **closes the loop by re-analyzing the result** (BasicPitch/Whisper) to check the action did what was
  intended — built on Anthropic's Claude Agent SDK + MCP, voice-first/minimal-GUI. This validates the whole
  observe→act→verify loop *against REAPER specifically*. (Caveat: single source; its undo/habit internals
  aren't detailed — the habit-learning part is still greenfield.)
- **REAPER already has the primitives** to drive: native "Auto Trim/Split (remove silence)", Dynamic Split,
  automatic edge-fades, and scriptable fade-length setting (X-Raym ReaScripts). becky's value-add is making
  the cut land *where Jordan would put it*, not where a blunt threshold gate fires.
- **Onset/silence detection** to find true start/end: `librosa.effects.trim` returns the non-silent
  `(start, end)` sample indices directly (the fast deterministic default), **corroborated by Silero VAD** at
  the head where breaths/sibilants fool an energy gate — matching becky's "corroborate, then conclude" rule.
  becky already has Python onset helpers.
- **Habit-learning is the genuinely novel part — no shipping product does it.** iZotope Neutron/RX, sonible
  smart:EQ etc. learn *the signal*, not *the user*. The realistic, becky-shaped pattern: log each manual
  correction as `(detected_point, jordan's_actual_point, fade_ms, fade_curve)`, fit a tiny **deterministic
  bias/offset** on top of the onset detector + his typical fade, apply automatically, let him override. Fully
  offline, deterministic, value-assertable — fits becky's invariants exactly. (Treat as greenfield: no
  reference implementation to copy.)
- **The verify step** is straightforward and matches becky's "assert VALUES" standard: after the ReaScript
  trim/fade, re-run the onset analysis on the edited item and assert residual head/tail silence ≤ tolerance
  (a few ms); nudge if off. No model needed for the check — it's a deterministic closed loop.

### 4. The Toaster/Strudel "talk to it while jamming" model — real, voice-driven, and built on Strudel *(corrected 2026-06-23 per Jordan; partly unverified — see caveat)*

- **The project Jordan meant is `github.com/VoloBuilds/toaster`** (Volo's YouTube build "I Built an AI
  That Codes Music," 28 Feb 2026), demoed as **a voice-controlled instrument that turns speech into
  live-coded music — "jam with your computer just by talking to it."** So the "talk to it like a producer
  while you play" loop is **real and has been built — on Strudel.** This *strengthens* the doc's
  conclusion: the cleanest surface for conversational/voice live control is a live-coding engine, and
  someone has now shipped a demo proving it.
  - **The voice is NOT a separate STT→LLM→TTS pipeline (per Jordan, 2026-06-23).** It's a **single
    multimodal *realtime* model** — **Gemini Live (2.5 Flash native-audio; now `gemini-3.1-flash-live-preview`,
    3.5)** — doing native speech-in / speech-out + live video over a WebSocket, with **barge-in interrupt**
    (you talk over it and it stops), **no wake word, no typing**, and a **"proactive audio"** mode that
    controls *when* the model chooses to speak. Jordan runs the same model for his own realtime assistant
    ("whoretana") incl. **video streaming** (it has watched a YouTube video with him while he filmed a
    reaction). The public `VoloBuilds/toaster` repo only documents the text-prompt → Strudel path (React /
    Cloudflare Workers); the realtime-voice layer is the Gemini Live model, not a documented STT engine.
    "How computers should work" — no wake word, interruptible — is the target interaction model.
  - **Correction of an earlier error in this doc:** the first research pass found a *different* repo,
    `github.com/vanities/toaster-strudel` (Claude-Code agents editing `.strudel` files, file-watch +
    cycle-boundary crossfade reload — text/agent-driven, not voice) and wrongly concluded "Toaster isn't
    voice." Two different "Toaster" projects exist; **VoloBuilds/toaster is the voice one.** Spoken
    live-jam control is still a thin/under-served niche overall, but it is no longer "nobody built it."
- **Strudel** (strudel.cc, JS port of TidalCycles) is the cleanest programmatic surface for conversational
  live control: push a code string → `setCode()`/`evaluate()` → it's audible next cycle; and it's
  **embeddable** (`@strudel/repl`/`@strudel/embed` web components, or as a library) — so it could live
  *inside becky-canvas* rather than as a piloted external app. It's deterministic, offline-friendly, OSC/MIDI
  capable.
- **In-session AI control precedents:** WavTool's "Conductor" (GPT-4 acting on a browser DAW; **acquired by
  Suno 2025 → Suno Studio**) and **Ableton-MCP** (NL → MCP → Remote Script → LOM, live param/clip control).
  All confirm the same three-stage loop — STT(optional) → LLM intent → programmatic surface — differing only
  in how clean the surface is. Strudel's is the cleanest; a DAW's is richer but a wider stateful API to mediate.
- **Stanley Gurvich's** CONDUCTION (Session-view looper that commits takes to Arrangement, via a custom Remote
  Script) proves Ableton is *deeply* externally controllable for **performance/looping** — but that's MIDI/
  session/transport control, the layer Ableton is good at, **not** audio-take editing.

- **The realtime multimodal model IS the interface layer — for BOTH lanes (per Jordan).** A Gemini-Live-class
  realtime model (native duplex audio+video, barge-in, no wake word, proactive-audio timing) is the
  conversational front-end for the *whole* becky assistant, not just the jam mode: the same "talk to it, it
  watches/listens, it acts, you interrupt" loop wraps the **REAPER take-cleanup engineer** ("that trim's too
  tight, back it off") *and* the **Strudel jam**. The model isn't the musical brain — becky's deterministic
  `arrange`/ReaScript layer is — it's the *ears, eyes, and mouth*.
- **Cloud vs local for that layer — an explicit, scoped trade Jordan has already made:**
  - **Cloud (Gemini Live 2.5 Flash / 3.1 / 3.5):** delivers the true low-latency duplex + barge-in + video
    *today*. Jordan is **fine using API calls for the realtime assistant layer** (it already runs his
    "whoretana" incl. live video). This is a deliberate, **scoped** exception to becky's offline invariant:
    the **forensic core stays offline + deterministic**; the *conversational/canvas assistant* may be online.
    A future agent should NOT "fix" this back to offline-only — it's intended.
  - **Local (already in becky's stack):** **Gemma-4 QAT** takes **audio + vision inputs**, and **NeuTTS Air**
    is the existing local **TTS out** — so a fully-local approximation exists. Honest caveat: chaining
    Gemma-in + NeuTTS-out does **not** match Gemini Live's *native-audio duplex / barge-in* — true
    interrupt-while-speaking, sub-second turn-taking is the realtime model's special sauce and is the hard
    part to replicate locally. Use local for offline/private sessions; reach for the API when he wants the
    seamless "how computers should work" feel.
  - **Transport/plumbing = FastRTC (`github.com/gradio-app/fastrtc`, maintained, v0.0.34 Nov 2025).**
    "Turn any Python function into a real-time audio/video stream over WebRTC/WebSockets," with **built-in
    VAD + automatic turn-taking (barge-in)** and demonstrated integrations with **Gemini, OpenAI Realtime,
    and Claude**. It fits becky's existing Python `pyhelpers` layer and is **brain-swappable on one
    transport**: FastRTC → Gemini Live (cloud duplex) **or** FastRTC → local Gemma-4 QAT + NeuTTS (offline).
    This de-risks the "duplex is hard locally" caveat above — FastRTC supplies the VAD/turn-taking either
    way; only the realtime model's native-audio quality differs.

**Implication for becky:** the take-cleanup engineer (REAPER) and the jam (Strudel) are *separate lanes*, but
they **share one front-end** — a realtime multimodal model as the always-listening, interruptible voice. For
the jam specifically, the embeddable surface is **Strudel inside becky-canvas** (not Ableton, not coupled to
the REAPER cleanup work). For both, the voice/eyes layer is a Gemini-Live-class model (API now; Gemma-4 QAT +
NeuTTS as the local, non-duplex fallback).

### 5. Cubase — confirmed out for AI control *(high confidence — Steinberg's own docs)*

Steinberg's MIDI Remote API is explicitly a layer "to bridge MIDI controller hardware and Cubase" — it maps
knobs/faders to **existing** host functions (`mHostAccess`: selected track, vol/pan/mute/solo, mixer) and has
**no API to create/edit MIDI notes, clip contents, or items**. No ReaScript/LOM equivalent, no native OSC.
External bridging is possible only as crude virtual-MIDI/SysEx controller-mapping. **Cubase cannot be the AI
host.** (This doesn't mean Jordan must abandon Cubase for *mixing* — it means becky's AI workflow can't live
there. becky drives REAPER; Cubase stays whatever he wants it to be.)

---

## What this means for becky (recommendation)

1. **Target REAPER for the proactive-engineer vision.** It's the only host that can do the central
   take-cleanup loop, it's open/cheap/stable, it can host a docked **becky panel** (ReaImGui), and becky
   **already has `SPEC-BECKY-REAPER` + `internal/reaper`** (the `.rpp` writer). This is an *increment on an
   existing, proven direction*, not a pivot — which is exactly the kind of low-risk move the repo's
   anti-flip-flop rules favor.
2. **Don't switch Cubase → Ableton for AI cleanup.** It literally cannot trim+fade a take via its API. Keep
   Cubase for mixing if he likes it; that's orthogonal.
3. **The new build = a live bidirectional bridge** (sibling to the existing `.rpp` writer): a persistent
   in-REAPER ReaScript broker (`defer` loop) ⇄ becky Go over file-IPC or `js_ReaScriptAPI` socket; becky
   sends coarse intents, the broker runs the full API and streams state/events back. Optional later: a thin
   C++ control-surface extension for true record-stop push. The agent loop is becky's existing
   `internal/ctlagent`; the audio analysis is the existing Python onset/VAD helpers.
4. **Habit-learning is the differentiator and it's greenfield** — log (detected vs actual) trim/fade deltas,
   fit a tiny deterministic bias, apply + let him override, verify by re-analysis. No product does this; it's
   the becky-shaped contribution and it fits the deterministic-but-checked framing exactly.
5. **Keep the "talk-to-it-while-jamming" idea as a separate, optional track** — best served by embedding
   **Strudel** in becky-canvas, decoupled from the REAPER engineer work. Spoken control is an open niche if
   he ever wants to own it.

## Confidence + flags

- **High:** Ableton's no-fade/no-waveform gap (3 independent sources); REAPER's read/write incl. audio
  accessor + fade shape/curve (SDK header); Cubase controller-only (Steinberg docs).
- **Medium-high:** DAWZY architecture (single arXiv source; corroborates the loop but not habit-learning).
- **Corrected (per Jordan):** `VoloBuilds/toaster` is voice-driven, and the voice is a **Gemini-Live realtime
  multimodal model** (native duplex audio+video, barge-in), NOT a separate STT/TTS chain. The earlier doc
  claims ("Toaster isn't voice"; "STT engine unverified") were both wrong — the first cited the wrong repo,
  the second assumed a pipeline that doesn't exist. Gemini Live API capabilities (barge-in, native audio,
  WebSocket duplex, proactive-audio, models incl. `gemini-3.1-flash-live-preview`) verified via Google docs.
- **Verify in-app:** the 1–6 fade-shape numeric→label mapping (header gives range + `0=linear` only);
  AbletonOSC clip-level "recording stopped" listener may need a tiny Remote-Script fork; REAPER/OSC exact
  latency (only "low-latency UDP" is established — don't quote a number).

## Key sources

- REAPER: `reaper.fm/sdk/reascript/reascripthelp.html`, `reaper.fm/sdk/plugin/reaper_plugin_functions.h`,
  `reaper.fm/sdk/reascript/reascript.php`, `reaper.fm/purchase.php`; `github.com/juliansader/js_ReaScriptAPI`,
  `github.com/shiehn/total-reaper-mcp`, `github.com/RomeoDespres/reapy`, `github.com/cfillion/reaimgui`;
  `radugin.com/posts/2024-07-07/control-reaper-via-osc/`.
- Ableton: `github.com/ideoforms/AbletonOSC`, `docs.cycling74.com/apiref/lom/clip/`,
  `help.ableton.com/.../209069969` (UI-only fades), `ableton.com/en/live/compare-editions/`,
  `help.ableton.com/.../installing-third-party-remote-scripts`, NIME 2023 AbletonOSC paper.
- Cubase: `steinbergmedia.github.io/midiremote_api_doc/`, `steinberg.help` MIDI Remote API.
- Habit-learning / verify loop: DAWZY arXiv 2512.03289; `librosa.effects.trim`/`split`; Silero VAD;
  iZotope/sonible assistant docs; PBD arXiv 1909.00031.
- Toaster/Strudel/Gurvich: **`github.com/VoloBuilds/toaster`** (the voice one) + Volo's YouTube
  "I Built an AI That Codes Music" (`youtube.com/watch?v=hLDEElYO6M0`, 28 Feb 2026, voice demo);
  `github.com/vanities/toaster-strudel` (the *other*, text/agent one — not what Jordan meant);
  `strudel.cc` + technical manual, `npmjs.com/package/@strudel/repl`, `stanleygurvich.com`,
  `github.com/uisato/ableton-mcp-extended`, `suno.com/blog/suno-acquires-wavtool`.
