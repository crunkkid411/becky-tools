# becky-tools — forensic video toolkit — **START HERE**

This is THE one file for *using* becky-tools. Agents and humans: read this; ignore the other
scattered `.md` (PROGRESS / OVERNIGHT / *-SPEC / NOTE-* are build & working notes, not usage).

## What it is
Offline command-line tools that ingest a video/audio file and tell you **WHO is in it, WHAT is
said (with timestamps), WHAT happens on screen, and WHERE** — for human-reviewed investigation.
Everything runs locally; nothing is uploaded. It does **not** conclude guilt — but it DOES reach
**confident, corroborated conclusions** when multiple signals agree (voice + face → "it's Shelby"),
instead of dumping maybes for you to sort. A lone weak signal stays "unknown." (See
`FORENSIC-OUTPUT-PHILOSOPHY.md`.)

## Where it is
- Tools: `X:\AI-2\becky-tools\becky-go\bin\becky-*.exe` (24 binaries; new: `ocr`, `cluster`, `motion`).
- Friendly entry point: **`becky.exe`** (the orchestrator). Call by full path or add `bin\` to PATH.
  (`becky-ask` — a double-click chat front-door — is being added; see `SPEC-BECKY-ASK.md`.)
- The case "knowledge base" of known people (built from the wiki): `X:\AI-2\becky-tools\becky-go\kb-final`
  (layout: `voice-prints/<Name>/*.wav`, `face-prints/<Name>/*.jpg`). John, Shelby, Hair Jordan enrolled.
- Search commands (`find`, `index`, `corroborate`) need the embedding server running once:
  `X:\AI-2\becky-tools\start-embed-server.bat`. Transcribe/diarize/identify/enroll do **not**.

## The easy way — the `becky` command (plain language)
```
becky enroll-wiki --wiki "C:\Users\only1\Documents\Obsidian\llm-wiki-CLANCY-TRIAL\wiki" --kb kb-final
becky profile "John Clancy" --kb kb-final --corpus "<video-or-folder>"
becky appearances "Shelby" --kb kb-final --corpus "<folder>"
becky find "affair" --db "<forensic.db>"
becky corroborate "<claim>" --kb kb-final --corpus "<folder>"
becky "this is Shelby" "<clip>"            # teach a new person from a clip — no "enroll" jargon
```
- `enroll-wiki` reads the case wiki and **auto-builds the known-people KB** (voice + face) — no
  manual clip-making. To add ONE person from a clip, just tell it in plain words:
  `becky "this is <name>" <clip>` (single-person clip = safe; multi-person uses the dominant speaker).
- Use a **fuller name** ("John Clancy", not just "Clancy") — a lone surname is ambiguous when
  several people share it.
- Every `becky` command prints a plain-English headline **plus** the full JSON.

## Ingest ONE video — the forensic pass
Each prints JSON (or use `--format srt|txt|vtt` on transcribe):
```
becky-transcribe "<video>" --format srt          # what's said + timestamps
becky-diarize    "<video>"                        # how many speakers + when each talks
becky-identify   "<video>" --kb kb-final          # which KNOWN people (by voice + face)
becky-validate   "<video>" --backend gemma4-local # plain-language description of on-screen actions
```
All-in-one (transcript + speakers + identities + events + on-screen OSINT + OCR; resumable):
```
becky-pipeline "<video-or-folder>" --kb kb-final --steps transcribe,diarize,identify,events,osint,ocr --out ingest-out
```

## Find text, locations, and recurring strangers
```
becky-ocr "<frame-or-osint-dir>" --db forensic.db   # read signs/IDs/chat/timestamps off frames -> searchable
becky-motion "<video>"                                # localize sub-second movement (aims validate at the moment)
becky-cluster --db forensic.db                        # group recurring UNKNOWN people: "Person A appears in N clips"
```
On-screen text is searchable in the SAME `becky find` as speech (addresses live in signage — your clips carry no GPS).

## Output style (for the description/validate tools)
Governed by `FORENSIC-OUTPUT-PHILOSOPHY.md`: **plain words** (butt/hips/waist, not "iliac
crest"), **name what we know** (write "John Clancy", not "speaker_1"), describe the **act and
its force/resistance dynamics**, flag only genuine uncertainty. Clarity = recall.

## Honest status (what works, what to watch)
- **Reliable:** transcribe; diarize (single-speaker → 1; hardened); **identify by VOICE** + the
  corroborated voice+face fusion; search (now incl. OCR text); enrollment incl. natural-language
  `becky "this is X"`; OCR; motion; the `becky` orchestrator. Portrait-video faces + accented
  names now work (both were bugs, now fixed — Shelby IDs at 0.94).
- **Watch:** **face alone** is the weakest signal — trust a NAME most when it's *corroborated*
  (voice + face). A lone face match below 0.55 is reported as "unknown," never a guessed name.
- **Recall = detection, not naming:** every person/face is surfaced; a NAME is attached only when
  confident. Unknowns are trackable via `becky-cluster` ("Person A") until you name them once.

## For agents
JSON-in / JSON-out, exit-coded, offline. Chain the `becky-*` tools or drive `becky`. Never modify
source videos. `--bin <dir>` overrides where the `becky-*.exe` live; `--verbose` for progress;
`--json` (on `becky`) suppresses the headline for machine parsing.
