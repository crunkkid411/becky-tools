# CLOUD HANDOFF - REAPER AI-first DAW (2026-06-20)

Jordan wants a functional AI-first DAW. DECISION (built + proven locally): becky drives
**REAPER** (already installed, fully scriptable via ReaScript/Lua, plain-text `.rpp`,
hosts all his VSTs). becky is the AI BRAIN over a real DAW. llama.cpp is the standard;
**Ollama is banned.**

## Where the work is
Check out branch **`claude/becky-reaper-daw`** (everything is there):
- `becky-go/internal/reaper` + `becky-go/cmd/becky-reaper` - deterministic `.rpp` writer
  from `dawmodel.Arrangement` (tracks, Cubase-style bus folders, MIDI, render cfg). 8 tests green.
- `SPEC-BECKY-REAPER.md` - full spec; **read section 5b** (the live blocker).
- `README.md` "AI-first DAW (REAPER)" section; `CLAUDE.md` section 6 handoff entry.
- `Open Becky DAW.bat` + `open-becky-daw.ps1` - one-click: becky authors a session, opens REAPER.
- `becky-reaper-work/` - proof artifacts (reference/demo/jordan_template .rpp + logs).
- `reaper1.jpg` (sibling of this file on master) = Jordan's screenshot of the live state.

## Proven locally (pasted evidence in SPEC section 3)
REAPER rendered an audible 24-bit/48k WAV (mean -13.7 dB); a becky-generated 17-track
session opened in REAPER with the full Cubase bus tree (DRUMS/GUITARS/BASS/VOCALS/FX), 132 BPM.

## The live blocker (from reaper1.jpg)
His **REAPER Chat** extension fails: `Failed to connect to http://localhost:11435/v1/chat/completions`.
FIX = run llama.cpp **`llama-server -m <model.gguf> --port 11435 --host 127.0.0.1`** (becky
standard; NOT Ollama). That endpoint serves OpenAI-compatible `/v1/chat/completions`. It is a
LOCAL command (cloud cannot run it), but cloud should build the launcher for it.

## What cloud can build now (pure code, no hardware)
1. One-click "Start Becky's REAPER brain" launcher: boot `llama-server` on :11435 with a
   resident GGUF so REAPER Chat connects.
2. ReaScript `.lua` emitter from `dawmodel.Arrangement` that loads his REAL VSTs
   (Serum 2, TAL-Drum, Maschine 2, Ozone) via `TrackFX_AddByName`.
3. Routing fidelity in `internal/reaper.FromArrangement` (nested sub-busses + sends, audio-clip paths).
4. Pipe `becky-compose`/`becky-drum`/`becky-wire` output into `becky-reaper build`.
