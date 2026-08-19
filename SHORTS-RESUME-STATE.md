# SHORTS WORK — RESUME STATE

Written so any session can pick this up with no input from Jordan. Overwrite as things land.
Last updated 2026-08-19 ~04:00.

## Landed on master tonight

| Commit | What |
|---|---|
| `4f62d0d` | **Overlap suppression.** `--top 10` was returning ten re-cuts of ONE 68s stretch of a 5-minute video. Now four distinct regions spanning 9.1s-300.1s. 91 candidates -> 11 distinct. |
| `a5985d5` | **Captions burned into shorts**, and only this clip's words — the word-rescue in `WordsPerSegment` was dragging the whole 5-minute transcript onto a 28s clip. 110 captions -> 18. |
| `ed33917` | **The 21-repo research + the build/skip decisions.** `research/repo-*.md` (22), `shorts-gap-decisions.md`, `iphone-ai-video-sweep.md`, `reka-edge-vs-gemma4.md`. |
| `387df0f` | **Cut edges snap to real silence** instead of ASR cue boundaries. 21/91 candidates moved on his footage. |
| `17a0699` | **The judge names the opening line**, and a verbatim quote from mid-clip now holds the clip as DISPUTED. Fired for real on his footage. |

## In flight

- **Agent — `becky-speaking`**: HANDOFF §7 item 1. faceembed multi-face -> facetrack -> asd.py, in a
  command. Owns `internal/faceembed`, `internal/facetrack`, `cmd/becky-speaking`.
- **Agent — jumpcuts**: renders the short as becky-cut would have cut it. Owns
  `cmd/becky-short/*`, `internal/crop/*`.
- **Download**: Reka Edge Q4_K_M + mmproj Q8_0 -> `models/reka-edge/` (~5.5GB).

## Reka Edge — the de-risking is DONE, the verification is not

- `clip.projector_type = "yasa2"`; the text side exports as plain `llama`.
- **This machine's llama.cpp (build 9551) ALREADY supports it** — `mtmd.dll` contains
  `yasa2_emb`, `yasa2_patch_conv_out`, `yasa2_patch_ln_out`. No rebuild needed. (Checked by
  reading the GGUF headers over an HTTP range request, before downloading anything.)
- Still unproven: that it loads inside the VRAM budget, that `Detect: <thing>` returns boxes that
  are right ON HIS FOOTAGE, and real tokens-per-image against the 512 `n_ubatch` ceiling.
  Checklist is at the bottom of `research/reka-edge-vs-gemma4.md`.
- Run it with `--reasoning off`: the model has no reasoning mode.

## What is left, in the order that matters

1. **The review pass** — nothing on this list re-watches its own output, and Jordan's rule 5 says
   ours must. Cheapest shape: render -> sample the rendered short back at true fps -> ask the VL
   three questions answerable from frames (is the subject framed, does the caption match the audio,
   does it end on a completed thought) -> fail the clip and say which question failed. This is where
   every defect so far was actually found. `becky-short --review`, not a new tool.
2. **Reset the camera smoother at every shot boundary** (`crop_path.py`). Currently the zero-phase
   filter runs over the whole clip, so framing smears across a cut. **Harmless today, bites the
   moment the jumpcut agent lands** — do this straight after it merges.
3. **Loudness to -14 LUFS** (`crop.RenderArgs`, `-af loudnorm=I=-14:TP=-1.5:LRA=11`). One line.
4. **Moment picking is still blind to whether he is on screen** (HANDOFF §7 item 3). Needs the
   face-coverage plumbing the becky-speaking agent is building.
5. **Zoom** — becky has none; `clip-forge` plans punch-ins, jump zooms over cuts, and slow creep.
   `audiosig` already measures the energy that would drive it. Build ONE, show him, stop.
6. **Framing taste pass** (HANDOFF §7 item 5) — every framing constant is set to an agent's eye,
   not his. **Only Jordan can close this.** Surface it as a question, never guess.
7. Retire the last degrade paths (HANDOFF §7 item 6): `cmd/motion` frame-diff -> optical flow,
   `cmd/framematch/decor.go` census -> ORB+RANSAC, and delete the false comment at
   `cmd/events/main.go:14`.

## Facts worth not rediscovering

- **OpenCode Zen has no key on this machine.** Only `OPENROUTER_API_KEY`. Repo research ran on
  OpenRouter `:free` ids via `scripts/research-repos.py` (resumable, validates the answer has real
  sections before writing — the first run wrote four files of a model's chain-of-thought).
- **`nvidia/nemotron-3.5-lightning:free` writes its reasoning into `content`.** Do not use it for
  structured output. `nemotron-3-ultra-550b-a55b:free` and `nemotron-3-super-120b-a12b:free` are
  clean. `dots-3-note-preview:free` returns empty content; `laguna-s-2.1:free` ignores instructions.
- **Do not put `\n` escapes inside a python heredoc in the Bash tool here** — they arrive as real
  newlines and produce unterminated string literals in generated Go. Build the file with the Write
  tool, or avoid the escape.
- **Never chain `git commit` inside a compound Bash command** — it trips the master-branch hook.
  Separate calls: branch -> add -> commit -> checkout master -> `merge --ff-only` -> push.
- Two test failures on master are PRE-EXISTING and not ours: `cmd/tts TestRun_DegradesWhenNoModel`
  and `internal/assistant TestHandleTier2Funnel`.
- Test footage is `test-for-clips.mp4`. `E:\TakingBack2007` is the CRIMINAL CASE — never for editing.
