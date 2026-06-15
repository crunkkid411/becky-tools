# SPEC-BECKY-CANVAS.md — a native, lightweight creative GUI canvas for becky

> **STATUS: design only — NOT built (2026-06-15).** From a deep-research pass
> (Space Agent / Agent-Zero, Go-native GUI toolkits, embedded llama.cpp, audio/
> MIDI/video building blocks). One **native** (NOT web) Windows GUI that hosts
> becky-ask chat and morphs, on demand, into a video timeline/player, an audio DAW
> (mixer + deterministic sidechain routing), a MIDI piano roll, and a drum machine.
> Web was explicitly rejected (multiple failed attempts). Awaits go/no-go.

---

## 1. Intent (read carefully)

A single lightweight **native canvas** that is becky-ask by default and becomes a
creative surface when called: "need video editing? it plays in the GUI. need a
DAW? same canvas. piano roll, drum machine — same canvas." Token-cheap and
instant (Space Agent's cost model), inline-AI like Notepad++'s OpenAI plugin but
**more efficient** by embedding inference instead of shelling a server. DAWs
(Cubase/Ableton/FL) are counter-intuitive — basic routing (a sidechain bus) takes
100 clicks; becky's canvas should make deterministic routing **one declaration**.

## 2. Grounding — becky already has the seeds

- **becky-ask already runs a native TUI** (Charm Bubble Tea: `cmd/ask/model.go`,
  `styles.go`) — a real starting point, not greenfield.
- **becky-ask already does local LLM the "NppOpenAI way"**: `cmd/ask/llama.go`
  spawns `llama-server.exe`, waits `/health`, POSTs OpenAI-style
  `/v1/chat/completions`, degrades to a deterministic catalog when absent. So
  "embed llama.cpp to beat llama-server" is a **measurable upgrade**, not a guess.

Two consequences: the GUI must stay **Go-centric** (reuse the CLI/JSON/config
plumbing; each mode is a tool, not a monolith); and "embed llama.cpp" is a
concrete swap of the spawn-server + localhost-HTTP transport for an in-process
C-API call.

## 3. Recommended stack

| Layer | Choice | Why (one line) |
|-------|--------|----------------|
| Window + GPU + immediate-mode canvas | **Dear ImGui via Go (`AllenDang/giu` -> `cimgui-go`)** | Only mature Go option whose purpose *is* custom canvases (timeline, piano roll, mixer) via a per-window `DrawList`. |
| Inline AI | **Embedded `llama.cpp` via cgo** (`tcpipuk/llama-go`), llama-server HTTP as fallback | Kills process-spawn + localhost hop + per-turn JSON; persistent in-process KV cache = "full context cheaply". |
| Real-time audio | **miniaudio** (single-header C, cgo) | Smallest dep; WASAPI/low-latency callback on Win10. |
| MIDI | **RtMidi** (cgo) | One cgo toolchain; full Windows incl. virtual ports (Rust midir lacks them). |
| Routing | **becky-owned deterministic DAG** (Go control plane, C audio callback) | The differentiator: sidechain = one declared edge, not 100 clicks. |
| Video | **libmpv render API** -> shared GL FBO/texture | Frame-accurate; composites under ImGui overlays; ffmpeg stays the offline workhorse. |
| Plugins | **CLAP first** (MIT), VST3 deferred | CLAP is MIT/no-SDK-license, simpler threading + MIDI 2.0; skip VST3 license tax for MVP. |
| Extension model | **becky CLI + JSON contract as the plugin ABI; each mode = build-tagged Go module** | Reuses the single-tool principle; CLIs plug in unchanged. |

**Headline:** Go + cgo around Dear ImGui (giu), one shared GPU surface that draws
chrome + creative canvases, mpv renders video into, and a C audio thread feeds.
One window, one render loop, one GPU context — that's what makes "morphing modes"
cheap and instant.

**Rejected:** Wails/any webview (user-rejected web); Fyne (retained-mode, wrong
for zoomable canvases); Gio (great pure-Go #2, but no first-class draw-list and we
need cgo for audio/mpv/llama anyway); Rust egui/Slint (excellent but orphan
becky's Go CLI/JSON layer).

## 4. Space Agent principles to port (ideas, not the browser)

1. **Action = code-in-the-message, not a tool-call schema.** The model emits a
   tiny inline becky **command DSL** (`tool arg --flag` lines the canvas runs and
   streams back). becky's deterministic CLIs are the verbs -> cuts the per-action
   token tax and composes with the single-tool principle.
2. **Stream-and-intervene.** The render loop redraws every frame; pipe the token
   stream straight onto the canvas (a C callback once llama.cpp is embedded).
3. **Full context cheaply** via the in-process KV cache: system prompt + becky
   tool catalog + project state cached once, reused every turn.
4. **Small core, swappable modules** -> Go build tags + a mode registry.
- **Safety:** "model emits executable text" dispatches ONLY to a default-deny
  allowlist of becky CLIs (same posture as the planned `becky-harness`). No
  free-form code execution.

## 5. Deterministic routing (the DAW differentiator)

Routing is a **declarative deterministic DAG** owned in Go: nodes =
sources/tracks/buses/FX/sends; edges = audio or control/**sidechain** links. The
graph is plain JSON -> content-addressed and reproducible (becky's offline+
deterministic ethos applied to audio). **One-click sidechain** = a single declared
edge `{from: B, to: A.compressor.sidechain}`; the engine auto-creates the send,
detector tap, and routing, named deterministically — no manual bus, no I/O hunt.
A topo-sort -> fixed per-block order -> fixed seeds for stochastic FX -> same
project == same render. The Go control plane edits the graph; a compiled lock-light
schedule (flattened order + preallocated buffers) feeds the C audio callback (no
alloc, no locks on the audio thread).

## 6. MVP — smallest proof

A native window that is becky-ask chat **plus one extra mode** sharing the canvas
and the embedded model. Mode #2 = **drum machine / step sequencer** (not video):
it exercises the hard-new parts (custom immediate-mode grid + real-time audio
callback + deterministic routing) with the *least* dependency weight (no libmpv,
no plugin host) and demonstrates one-click routing in days.

Module layout:
```
becky-go/cmd/canvas/
  main.go        # window, GL context, ImGui frame loop, mode registry
  canvas.go      # shared DrawList helpers (grid, ruler, lanes, zoom/pan)
  ai.go          # embedded-llama transport + inline command DSL parser
  registry.go    # Mode registry + hotkey switching
  modes/chat/    # becky-ask chat as a Mode (reuse cmd/ask, don't fork)
  modes/drum/    # step-sequencer grid + audio playback (MVP mode #2)
internal/
  llm/           # embedded llama.cpp wrapper (build-tagged: cgo vs http-fallback)
  audioengine/   # graph.go (DAG, topo-sort, content-addressed) + sidechain.go + builtins/
  canvasui/      # reusable immediate-mode widgets (timeline, pianoroll later)
```
All cgo (llm/audio/mpv) sits behind **build tags** so `go build ./...` /
`go test ./...` stay green on model-free Linux CI per CLAUDE.md §3.

## 7. Top risks
- **cimgui-go cgo callback-pool exhaustion -> crash** (HIGH): bound/reuse ImGui
  callbacks, no per-frame closures; **re-verify on go 1.26** in a Phase-0 spike;
  Gio stays a live #2. This gates the ImGui-vs-Gio decision.
- **cgo build complexity** (llama.cpp + miniaudio + RtMidi + libmpv): build tags
  per mode; CI stays model/media-free; vendor + pin each C dep.
- **Real-time audio safety**: compile the DAG to a preallocated lock-light
  schedule; audio thread touches only buffers.
- **llama.cpp C-API churn**: pin a commit; HTTP fallback so a break degrades.

## 8. Phased build plan
- **Phase 0 — spike (1-2 days):** giu window on Win10, zoomable grid via
  `DrawList`, stress the cgo callback pool on go 1.26. **Go/no-go: ImGui vs Gio.**
- **Phase 1 — MVP:** chat Mode (port becky-ask) + embedded llama.cpp (HTTP
  fallback kept) + measure tokens/latency vs spawn-server; miniaudio + routing DAG
  + drum machine with one sampler and a one-declaration sidechain demo.
- **Phase 2 — MIDI piano roll + mixer:** RtMidi; piano-roll canvas; mixer over the
  DAG; built-in EQ/compressor/delay; sidechain as a first-class one-click edge.
- **Phase 3 — video mode:** libmpv render API into the shared FBO; frame-accurate
  timeline; lay becky transcribe/diarize/events JSON as annotation lanes (the
  forensic DNA that sets it apart from a generic editor).
- **Phase 4 — extensibility/polish:** formalise the `Mode` ABI + registry; external
  CLIs register as modes via JSON descriptor; optional CLAP host behind the node
  interface; theme/perf pass.

## 9. Open decisions for Jordan
1. **Toolkit gate:** accept Dear ImGui/giu (+ its cgo callback caveat), or prefer
   pure-Go Gio (more code for custom canvases)? Phase 0 decides.
2. **Embed vs server now:** embedded llama.cpp in MVP, or stay on llama-server
   HTTP for v1 and embed in Phase 2?
3. **First creative mode:** drum machine (lightest, proves routing) vs video
   timeline (heavier, closest to forensic core)?
4. **Plugin hosting:** defer all VST3/CLAP past MVP (built-in FX only)?
5. **Network posture:** offline-default; only online surface is a remote AI model
   — confirm offline-default, opt-in + logged (consistent with §6 agentic specs).

> Full citations live in the research transcript that produced this spec (Space
> Agent, giu/cimgui-go, tcpipuk/llama-go, miniaudio, RtMidi, libmpv, CLAP). The
> single biggest technical unknown is the cimgui-go cgo closure-pool limit on go
> 1.26 — validate it in Phase 0 before committing to ImGui over Gio.
