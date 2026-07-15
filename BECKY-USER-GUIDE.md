# Becky user guide

Plain guide to what becky-tools can do and how to run it. No fluff. If a command
here doesn't work, it's almost always a missing model (see the bottom), not a bug.

There was no user guide before this one. This is it. Keep it up to date.

---

## The one call: becky-case

For a forensic answer about a video — who is in it, what they say, who is on screen —
the agent makes **ONE call** and becky does the rest:

```
becky-case --file "video.mp4"                     corroborated who + what
becky-case --file "video.mp4" --subject "Name"     + locate that person on screen
```

That's it. No flags to chain, no protocol to remember. Inside, becky runs the plan
itself (transcribe, diarize only if there's more than one speaker, identify, and the
Gemma-4 watch ladder), corroborates every finding, and returns a FINAL report: a name
is stated only when two signals agree, an on-screen moment only where a model actually
watched it, and everything uncertain is HELD — never dumped as a pile of maybes.

This is the standard engine (`internal/forensicrun`) — the fix for "the agent ignored
all the flags and protocols." There are no flags for the agent to ignore.

**Why not just have Opus watch the raw video?** Not about money — about accuracy. No
model, local OR Opus, ingests hours in one pass; they all sample frames and hand back a
confident-sounding GUESS with no corroboration. For a warrant, a wrong ID is a real
person wrongly placed. becky-case gives you corroborated-or-held. That's the point.

### On a long video (hours)

- **Who is in it (by voice) and everything said** — covered over the WHOLE video,
  corroborated. `becky-case --file "long.mp4"` handles the full length.
- **On-screen presence** — today becky watches the key motion moment(s), not every
  minute of a multi-hour timeline (the local watch model sees ≤60s at a time). Sweeping
  the whole timeline for on-screen presence is the one real build still open — see
  "What's still open" at the bottom.

---

## The individual tools

**`becky-case` chains these for you.** Reach for a single tool only when you want one
specific piece, not the full corroborated pass. All run offline; `--json` on most gives
machine-readable output.

**See / read a video**
| Tool | Does | Example |
|---|---|---|
| `becky-transcribe` | Speech → text + timestamps | `becky-transcribe "v.mp4"` |
| `becky-validate` | Watch a short clip: seen/heard/said | `becky-validate "clip.mp4"` |
| `becky-diarize` | How many speakers, who talks when | `becky-diarize "v.mp4"` |
| `becky-vision` | Describe / read one image | `becky-vision --image "x.png" --prompt "..."` |
| `becky-ocr` | Read on-screen text (needs frames) | `becky-ocr --frames-dir frames/` |
| `becky-motion` | Find WHEN something moved (no model) | `becky-motion "v.mp4"` |
| `becky-identify` | Match KNOWN people vs your KB (voice reliable; face is weak) | `becky-identify "v.mp4" --kb kb-final` |

**Search / find**
| Tool | Does | Example |
|---|---|---|
| `becky-pipeline` | Full pass over a folder (transcribe+diarize+identify+events), resumable | `becky-pipeline "folder" --kb kb-final --out ingest-out` |
| `becky-search` | Keyword + meaning search over indexed corpus | `becky-search "affair" --db forensic.db` |
| `becky find` | Search what was said, with timestamps | `becky find "money" --db forensic.db` |
| `becky profile "Name"` | One-card summary of a person + everywhere they appear | `becky profile "John" --kb kb-final --corpus "folder"` |

**OSINT / research**
| Tool | Does | Example |
|---|---|---|
| `becky-osint` | Export full-res frames + hashes for a detective (no AI) | `becky-osint "v.mp4" --events events.json` |
| `becky-palantir` | Who co-occurs with whom, where, when (link graph) | `becky-palantir --corpus "folder" --query "who is with John"` |
| `becky-web-search` | Live web search, no browser | `becky-web-search "query"` |
| `becky-research` | Deep research: search + verify + cited answer | `becky-research "question"` |
| `becky-radar` | Surface stuff you flagged in Chrome history | `becky-radar --list` |

**Reverse-image search / operating a browser is NOT solved yet** — Chrome blocks it.
See `SPEC-BECKY-CHROME.md` for why and the fix.

---

## Workflows — the .json file

A workflow is a small file: a name, trigger phrases, and an ordered list of steps.
It's deterministic. It spends **zero AI tokens** unless a step is an `agent` step.

**Run one:**
```
becky-workflow run watch-video --target "clip.mp4"
```

**See what's available:**
```
becky-workflow list
```

Two are built in and always available:
- `watch-video` — transcribe + validate + bundle. No AI.
- `watch-video-ai` — same, then ONE Opus step reasons over it (see below).

**The format** (this is a whole workflow):
```json
{
  "name": "watch-video",
  "phrases": ["watch this video", "what is in this video"],
  "steps": [
    { "tool": "becky-transcribe" },
    { "tool": "becky-validate" },
    { "merge": "report" }
  ]
}
```

**Step kinds** — each step is exactly one of:
- `"tool"` — run a becky tool on the target file.
- `"merge"` — bundle the tool outputs so far into one JSON blob.
- `"agent"` — run Opus/Claude over the outputs (opt-in — see below).
- `"verb"` — a forensic op; runs via `becky-transcribe --forensic`, not here.

**Skip a step conditionally** — add `"when"`:
```json
{ "tool": "becky-diarize", "when": "speakers > 1" }
```
(Skips speaker-splitting on a one-person clip.)

**Make your own:** copy a built-in to a file, edit it, and run it by path:
```
becky-workflow run my-recipe.json --target "clip.mp4"
```

**Current limit:** a tool step runs the tool with just the target file. Tools that
need extra flags (`becky-ocr`, `becky-osint`, `becky-identify`) can't be bare steps
yet — run those directly for now.

---

## The agent step — Opus/Qwen, only when YOU want it

This is the part that's different from Archon and everything else: **the AI runs
only when a recipe explicitly asks for it.** A recipe with no `agent` step costs
nothing. No AI every run.

Add a step like this:
```json
{ "agent": "claude-code", "prompt": "Summarize who and where, from the data only." }
```

The agent gets handed everything the tools before it produced, and reasons over
that. So you extract locally (cheap) and reason once (one call) — instead of Opus
watching the whole video.

**Run the built-in AI workflow with your model:**
```
becky-workflow run watch-video-ai --target "clip.mp4" --model claude-opus-4-8 --budget 2.00
```
- `--model` — pick the model (default = your Claude CLI default).
- `--budget` — max dollars for that step (default $0.50, a safety cap).

Only `claude-code` is wired today. `qwen` is planned.

---

## OSINT quickstart (your actual use)

Researching a person's vlogs/livestreams:

1. **What they say** — `becky-pipeline "their-videos/" --kb kb-final --out out/`
   (transcribes the whole folder; resumable).
2. **On-screen places / signs / identifiers** — `becky-osint "v.mp4" --events out/events.json`
   pulls the frames, then `becky-ocr --frames-dir <frames>` reads them.
3. **Who's with whom** — `becky-palantir --corpus out/ --query "who co-occurs with X"`.
4. **Web side** — `becky-web-search "..."`, `becky-research "..."`.
5. **Reverse-image a photo / drive a browser** — not working yet; see `SPEC-BECKY-CHROME.md`.

Then hand the JSON to Opus to connect the dots — don't have Opus watch the videos.

---

## What's still open

**Sweeping a long video for on-screen presence.** `becky-case` corroborates *who is in*
a video (by voice) and *everything said* over the whole length — but the on-screen WATCH
currently looks at the key motion moment(s), not every window across hours, because the
local watch model sees ≤60 seconds at a time. The fix is to have becky-case walk the
whole timeline internally — pick candidate windows across the full duration (motion
bursts + mention timestamps), watch each, and corroborate on-screen presence over the
entire video — still one dumb call. That is a real forensic-engine change, not a flag.

---

## When something doesn't work

- **A tool prints a short "degrade" note instead of a real answer** → the local
  model isn't installed. That's the #1 cause. Not a code bug.
- **`becky-validate` won't do a whole video** → by design, ≤ 60-second clips only.
- **Face matching seems off / missing** → face ID is the weak modality. Voice ID is
  the reliable one. Trust voice matches, double-check face ones.
- **Chrome / reverse-image / browser stuff fails** → known; see `SPEC-BECKY-CHROME.md`.

Rebuild all tools after any change: run `becky-go\build-all-tools.bat` (it builds
every tool and installs them so they're on your PATH).
