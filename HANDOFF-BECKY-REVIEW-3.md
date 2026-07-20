# HANDOFF — Becky Review 3 (2026-07-20, ~00:00)

Branch: `fix/becky-review-3-audio`. Everything below is committed. Written so the next
agent does not rediscover any of it.

## THE RULES THAT KEEP GETTING BROKEN

**1. FREE OR OAUTH. NEVER A PAID API.** Jordan pays for Claude Max — every Anthropic model
is ALREADY his via the OAuth session (Agent tool / `claude --model sonnet` / fleet-run).
Billing an API key for one spends his money on what he already owns. He calls it theft and
he is right; it drained his OpenRouter balance mid-task on 2026-07-19.
- **OpenRouter free = `tencent/hy3:free`.** Hy3 is the free one. **m3 / minimax is NOT free
  on OpenRouter** — it is free only via the local router (`claude ollama`). Jordan pasted
  these exact names days ago; do not ask him again.
- ENFORCED, not advised: `~/.claude/hooks/block-paid-apis.ps1` is a PreToolUse hook that
  blocks paid endpoints before the command runs (8/8 test cases). `isFreeModel()` in
  `cmd/subtitle/openrouter.go` refuses non-`:free` ids in-process. Two independent layers.

**2. PROSE IS NOT PROTOCOL.** A rule in a .md is a suggestion an LLM rationalises past — one
was written 07-18 and ignored 07-19. If a rule matters, make it a HOOK or a code guard.
Adding another paragraph to a CLAUDE.md is bloat, not a fix.

**3. DO NOT GUESS MODEL NAMES.** I hardcoded `minimax/minimax-m3:free` and
`z-ai/glm-5.2:free`; neither exists. `rotationFor()` now builds the chain from the LIVE
`/models` catalogue, so a fabricated id cannot survive.

**4. VERIFY WITH YOUR OWN EYES.** Never relay a subagent's "verified" as fact — that put
Jordan in front of a broken Load button and a caption-less render. Extract the frame, look
at it, count the frames yourself.

**5. Review 3 was SPECIFIED to be grown from `native/becky-timeline`** (`BUILD_1.md` ~L233,
`COLLAB-PROTOCOL.md` L27) and largely was not: becky-timeline is 1553 lines, becky-review is
4538 with only 3 hits for its distinctive markers. That reinvention is why basic things were
missing. Prefer reusing becky-timeline over writing more.

## FIXED AND VERIFIED (my own measurements, not relayed)

| Thing | Evidence |
|---|---|
| Render frame-exactness | **4501 frames @ 30000/1001** = exactly `round(150.183 × 30000/1001)`. Was +1.269s / 38 frames. |
| Captions burned in | My own frame at t=148s of the render: "if you only post" |
| Cut points | `.txt` and `.xml` imports agree on all 88 clips, **0 µs**, every boundary on a whole frame |
| Frame rate stored | `29.97002997003` (true NTSC) in `clip.meta.source_fps` — was MISSING entirely |
| Audio | mpv plays an EDL; peak meter 0.53 playing vs 0.000 paused; Jordan confirmed by ear |
| Window launch | ~1s (was ~15s blank) |
| Caption pass | 202 cues, ~12s, **$0**, rotates across free models |

## THE FIVE RENDER BUGS (all in `internal/reel`, commit `d137ea3`)
1. Per-clip frame rounding at 88 concat boundaries → each segment now force-trimmed to
   `round(dur×fps)` frames via `trim=end_frame`; audio `atrim`'d to match.
2. `formatSeconds` at 3 decimals rounded a true-NTSC boundary PAST the frame → microseconds.
3. `formatRate` emitted `29.970` (= 2997/100, a DIFFERENT rate) → exact rational `30000/1001`.
4. The bigger filter graph blew Windows' ~32K command-line limit → `-filter_complex_script`.
5. The tool self-reported the container duration, which AAC priming skews ~33ms → now probes
   the video stream.

## STILL OPEN
- **Caption wording**: ~34 single-word lines; breaks like "that in / order to grow" read
  oddly. The rule Jordan cares most about holds (a number never leaves its unit:
  "ten times a day", never "ten" / "times a day").
- `cmd/tts` test fails — pre-existing/environmental, not ours.
- Not merged to master. DO NOT quote a commit count here - the last one went
  stale within hours and an audit caught it (doc said 19, reality was 26).
  Run `git rev-list --count master..HEAD` instead.

## HOW JORDAN MAKES A VIDEO
Open **Becky Review 3**, Load his `.xml` (the dialog now accepts `.txt`/`.xml`/`.json` and
converts automatically), Render. Captions burn in.
CLI equivalent, using his own frame-exact Vegas render:
```
becky-subtitle --edit "X:\Videos\2025\11_November\Rendered\post_constantly.xml" ^
               --burn "X:\Videos\2025\11_November\Rendered\post_constantly.mp4"
```
