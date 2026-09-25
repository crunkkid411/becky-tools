# video_events — GitHub Trending Weekly #50 (Github Awesome)

## Source
- url: https://www.youtube.com/watch?v=GeYevz27gyc  (on Jordan's "ai - useful -" playlist)
- duration: 911 s, 720p avc1, sha256 67a334c37a03022e97111c6f9ae4a3e99323f41efb54605e368a246a26463bd7
- perception: qwen3.8-omni-flash via read_native_av (event_log template), 2026-09-25.
  The whole-video call failed ("Backend buffer overflow", 400); coverage split into 3 contiguous
  ranged reads (0-310, 310-620, 620-911). Chunk 2 true start 309.52 s (keyframe snap); times below
  are absolute. Fields condensed from the raw log: `shown` shortened, `said` kept verbatim.
- shape: 35 x ~25 s segments, each = narrator reads a summary over a scroll of one repo README.
  No step is performed on screen; it is a roundup, not a tutorial.

### [00:00:00.000-00:00:09.000] framing — Channel intro; GitHub Trending switched Today -> This week
- said: "Welcome back to Github Awesome. This is GitHub Trending Weekly number 50. 35 trending open source projects on GitHub right now. Let's go."
- highlights: the list's provenance is github.com/trending with Date range = This week.

### [00:00:09.000-00:00:36.000] explanation — OpenMuse (CopilotKit/openmuse): personal agent with a visible computer
- said: "...persistent browser, an optional Linux terminal in an isolated container, and read-write access to your Gmail and Google Calendar... If it's heading the wrong way, you take over the browser or terminal mid-task. It runs on iOS, Android, and the web from one Expo app."
- highlights: observability + human takeover mid-task; worker separation (browser / isolated terminal / durable task worker).

### [00:00:36.000-00:01:00.000] explanation — Search (driceroland/Search): ~3 MB WebKit Mac browser
- said: "...uses the WebKit already on your machine, keeping the app around 3 megabytes... Passwords live in the macOS Keychain. There's no account or sync..."
- highlights: macOS 14+ only; size is a consequence of reusing system WebKit.

### [00:01:00.000-00:01:23.000] explanation — Open Glean (hydra-db/open-glean): cited Q&A over HydraDB
- said: "...searches your files, notes, and connected apps through HydraDB, then uses your chosen model to write answers with source citations. Its deep research mode splits a question into smaller searches, runs independent branches in parallel..."
- highlights: depends on the hosted api.hydradb.com; keys held server-side in an encrypted HttpOnly cookie.

### [00:01:23.000-00:01:51.000] explanation — Unreal Agent (unreallabsai/unreal-agent): async Go harness with input dedup
- said: "If an agent receives the same event twice, you don't want it doing the job twice... deduplicates inputs, persists sessions and operation state, and supports recovery and forks. Tool calls become serializable operations that execute outside the coordinator's event loop... There's a record to resume from."
- highlights: tool execution outside the coordinator loop so a half-failed run is resumable.

### [00:01:51.000-00:02:16.000] explanation — ZCode (zai-org/ZCode): one agent runtime behind desktop, web and terminal
- said: "...the interface you're in doesn't change what the agent knows. It works over SSH and WSL for remote projects, and the CLI piece runs standalone..."

### [00:02:16.000-00:02:43.000] explanation — BongoCat (vladelaina/BongoCat): desktop pet reacting to input
- said: "...Completely unnecessary, pretty charming... C, SDL3, and OpenGL... Windows, macOS, and Linux..."
- highlights: the narrator flags it as frivolous.

### [00:02:43.000-00:03:07.000] explanation — laya-mlx (mizorewww/laya-mlx): Laya typed decisions on Apple Silicon
- said: "Laya MLX handles small decisions that don't need a chatbot response. Give it a support message and candidate departments, and it returns probabilities for each choice without generating text... 13.4-millisecond median for a short English decision."
- on_screen_text: checkpoints Laya (ModernBERT-large, 421M, 512 ctx, English), Laya-multilingual (mmBERT-base, 322M, 1,024), Laya-typed-decisions (ModernBERT-large, 421M, 1,024); "Port fidelity: ... 378/378 comparisons".
- highlights: "typed decisions" = one bidirectional forward pass, zero output tokens; Apple-only port, not official.
- asset: clip@00:02:43.000-00:02:48.000 — Snake demo: live probability bars, OUTPUT TOKENS 0, NETWORK OFFLINE.

### [00:03:07.000-00:03:33.000] explanation — mini-AGI (volotat/mini-AGI): continual-learning byte LM on an 8 GB GPU
- said: "...byte-level language model you train from scratch on an 8-gigabyte GPU... pages its experts in from disk 32 at a time... 99.84 percent retention... The author calls it a toy model despite the name."
- highlights: parameter count bounded by disk, not VRAM; author's own "toy-level" caveat.

### [00:03:33.000-00:03:57.000] explanation — laya-coreml (mizorewww/laya-coreml): Laya on the Neural Engine
- said: "...4.98-millisecond median for one multilingual decision and a 2.78-fold improvement in system energy per decision over compiled MLX."
- highlights: honest negative result on screen: "the requested 10x improvement was not achieved". Apple-only.

### [00:03:57.000-00:04:24.000] explanation — Bespoke Nimble (bespokelabsai/nimble): open Qwen3.5-9B "open Jev"
- said: "...answers typed questions about text with no reasoning text... scores only the candidate answers, returning JSON with a probability for each option... 90.12 percent agreement, up from 66.36 for the base model."
- on_screen_text: results ladder Jev 1.13.0 93.2%, Nimble-9B 90.1%, Qwen3.5-9B 66.3%, Qwen3.5-4B 61.4%, Qwen3.5-0.8B 45.4%, Gemma 3 270M 28.7%; "Without quantization, the 9B weights alone take about 18 GB"; "probabilities are not a guarantee that an answer is correct".
- highlights: one-token-per-answer scoring with KV-cache prefill; not distilled from Jev; 18 GB unquantized = over the 8 GB rule.

### [00:04:24.000-00:04:48.000] explanation — VModal Swift SDK: multimodal video search for iOS/macOS
- said: "...search for moments inside video... VModal's backend does the indexing and search, and you'll need an API key."
- highlights: cloud backend + API key; Swift/Apple only.

### [00:04:48.000-00:05:10.000] explanation — magpie (yetone/magpie): one screen to set every coding agent's model
- said: "...covers nine of them, including Claude Code, Codex, Gemini CLI, and Cursor, and rewrites each config file in place without wrecking your comments or indentation. A local gateway speaks OpenAI's and Anthropic's APIs..."
- on_screen_text: gateway http://127.0.0.1:3425/v1; "magpie never reads keys from your shell environment"; macOS, Linux and Windows.
- highlights: surgical atomic config edits; subscription logins reused as providers.

### [00:05:09.520-00:05:15.520] explanation — magpie (cont.): profiles snapshot every agent's settings
- said: "...Profiles snapshot every agent's settings, so you can switch the whole setup at once."

### [00:05:15.520-00:05:44.520] explanation — NullMotion (blixvip/NullMotion): finished ad over frame-synced drafts
- said: "...FFmpeg scene detection splits the reference into three to eight sections, and each draft is HTML and GSAP on a paused timeline driven by the film's clock. Export renders the whole thing to MP4 in the browser with WebCodecs... it runs entirely local."
- highlights: drafts are frame-synced to the film's clock; FFmpeg scene detection for sectioning.

### [00:05:44.520-00:06:10.520] explanation — shapeshift (anishfn/shapeshift): one text box that morphs into the right tool
- said: "...Jev can identify the intent, while ordinary code handles the dates and math. It also works offline with a built-in classifier..."
- on_screen_text: "Jev: one call, 14 typed questions, ~150ms"; "Jev decides (which card, which variant). Code computes (every value on it)."; flowchart keystroke -> 120ms debounce -> Jev online / offline keyword classifier -> decide() -> gatesAndLids() -> parseFor() -> Card.
- highlights: THE division-of-labour pattern: model picks intent/variant, deterministic code computes every value; offline fallback classifier.

### [00:06:10.520-00:06:36.520] explanation — riso-windowseat (sevenevesai/riso-windowseat): procedural risograph films
- said: "...each living in a single HTML file... Every frame depends on time alone, so you can jump to any moment and inspect it exactly."

### [00:06:36.520-00:07:05.520] explanation — Flute (webprodigies-org/flute): cinematic 3D shots from real React UI
- said: "...your coding agent writes JSON scenes that point at your real UI... exports MP4 at up to 120 frames per second."

### [00:07:05.520-00:07:30.520] explanation — arc-cua (shhivv/arc-cua): fast action layer for computer-use agents
- said: "...A planner sets the goal, allowed text, constraints and success check. Jev chooses actions from controls the Mac desktop exposes... If it gets stuck or hits its action budget, it hands control back to the planner."
- on_screen_text: "The optimization target is fewer expensive reasoning calls per completed task, not fewer UI actions."; pipeline observe -> AX + local OCR -> build legal action space -> JEV decision -> freshness guard -> execute -> wait for settle; terminal states SUBTASK_COMPLETE / BLOCKED / NEEDS_AGENT.
- highlights: System 1 picks only from a code-built legal action set; budget exhaustion hands back to the System 2 planner.

### [00:07:30.520-00:07:54.520] explanation — Foremerge (naw103/foremerge): intent conflicts before code conflicts
- said: "...When parallel coding agents publish what they plan to touch, it compares those intents... A verification gate runs your tests on a clean commit before anything lands."
- on_screen_text: install is `curl -fsSL https://foremerge.com/install.sh | sh`; state lives inside `.git`.

### [00:07:54.520-00:08:18.520] explanation — fast-browser-use (APUS-AI-Lab/fast-browser-use): local System-1 browser actions
- said: "...scans the controls actually visible on a page, then asks a local model to choose from those options... It runs on your machine, though the model needs substantial memory."
- on_screen_text: "QWEN3.5 9B / MLX"; "79 ms median action decision"; "4 one-token passes"; "zero selector hallucinations".
- highlights: candidate set built from the visible DOM so the model cannot invent a selector; 9B = too big for the 8 GB budget.

### [00:08:18.520-00:08:42.520] explanation — FluidUse (FluidInference/FluidUse): 706K-param on-device form filler
- said: "...The decisions run on your device, and the repo reports about one millisecond per field. You handle anything that needs judgment, like essays, uploads, and clicking submit."
- highlights: autonomy boundary — judgement fields stay with the human; Core ML/Mac only.

### [00:08:42.520-00:09:07.520] explanation — Mr. Mak Workspace (witnesstodark/mr-mak-workspace): Windows desktop app for Codex/Claude Code
- said: "...a Windows desktop app that puts Codex CLI or Claude Code in one window and your project files in the other... ships with 14 project skills, including Blender workflows."
- highlights: voice coordinator needs Codex CLI + your own OpenAI API key (paid).

### [00:09:07.520-00:09:32.520] explanation — AI Manager (OnlistTeam/ai-manager): one app for AI coding CLIs
- said: "...detects ten tools... installs, updates, or repairs them across npm, pnpm, bun, or volta..."
- on_screen_text: "so you never have to touch shell profiles, PATH, or hand-edited configuration files"; AGPL-3.0.

### [00:09:32.520-00:09:56.520] explanation — astra-flash-orchestrator: expensive planner + cheap builder
- said: "...Astra plans the work and reviews the finished patch. A DeepSeek Flash subagent handles implementation, tests, and routine fixes..."
- on_screen_text: "98.9% less Astra input per 1K implementation lines"; "not guaranteed savings".
- highlights: plan/review with the expensive model, volume with the cheap one — same shape as becky's E4B -> 12B ladder.

### [00:09:56.520-00:10:20.520] explanation — JevRouter (BillionsBobby/JevRouter): decision-only routing
- said: "Jev Router decides which tool your agent should reach for, and stops there... asks TypeSafe's Jev a typed choice question, then filters the answer through permission, availability and risk checks. It never runs anything itself, and every decision goes into an append-only log."
- on_screen_text: "Jev owns the decision probabilities; JevRouter owns availability, permissions, risk and confirmation... Filtered candidates are never re-normalized."; "Decision-only by default"; "Receipts by default — append-only decision/plan files".
- highlights: two-layer split Decision Layer (model) vs Policy Layer (code); receipts log.
- asset: frame@00:10:14.520 — the Decision/Policy/Execution layer diagram.
