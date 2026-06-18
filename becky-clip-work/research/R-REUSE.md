# R-REUSE — Reuse vs Build for the Forensic Compilation Tool

**Date:** 2026-06-18
**Question:** For a NARROW forensic "supercut" tool (search transcripts → preview a moment →
double-click to append a clip → arrange/ripple → burn unobtrusive lower-thirds →
export MP4 + EDL + .srt + stills, **originals never modified, functional TODAY**),
do we REUSE an existing OSS video editor (kdenlive / shotcut), reuse a COMPONENT, or
reinvent a thin tool on ffmpeg?

---

## VERDICT (decisive)

**Reinvent thin on ffmpeg for the becky engine + GUI — and OPTIONALLY adopt MLT's
`melt` as a second, drop-in render backend for the lower-thirds/timeline burn, because
it is already installed on the PC.** Do **not** reuse kdenlive or shotcut as
applications, and do **not** try to drive either one headlessly.

In one line: **the lean in the brief is CONFIRMED.** A becky-native Go engine
(deterministic ffmpeg ops + AI router) + a thin GUI shell is the right call. The only
adjustment to the lean: MLT is *already present* on this machine and is a genuinely
useful, license-clean render backend for the text-burn step — so the engine should be
written with a backend seam (`ffmpeg` path is the default/portable one; `melt` path is
an optional, higher-fidelity burn-in) rather than hard-wiring ffmpeg-only.

### Why (scored against the four constraints)

| Constraint | ffmpeg-thin (default) | `melt` backend (optional) | Reuse kdenlive/shotcut app |
|---|---|---|---|
| **Functional TODAY** | YES — ffmpeg/ffprobe present, verified cut+concat+burn live | YES — `melt.exe 7.37.0` already installed, verified end-to-end live | NO — would need to launch a GUI app a detective can't script; no headless "assemble from N sources + burn + render" entry point |
| **No heavy toolchain** | YES — nothing to build | YES — nothing to build (bundled with kdenlive) | NO — building kdenlive/shotcut from source on Windows = KDE Frameworks + Qt6 + MLT; days, not hours; Qt6 not even installed |
| **License cleanliness** | CLEAN — ffmpeg is LGPL/GPL but invoked as a separate process (becky already does this) | ACCEPTABLE — MLT core is LGPL-2.1; we call `melt.exe` as a subprocess and must avoid the GPL `frei0r` plugins (we don't need them) | RISKY — kdenlive is **GPL**; bundling/shipping it with becky is contaminating |
| **Single-tool becky philosophy** | PERFECT — one tool, file/JSON in → MP4/EDL/SRT/stills out → exit code | PERFECT — same shape, just shells to a different binary for the burn | BAD — an editor app is the opposite of "one small composable tool" |

---

## The load-bearing discovery: MLT (`melt`) is ALREADY on this PC

This reframes the whole question. The brief assumed kdenlive/shotcut/MLT are *not*
installed. They are not installed **as editors you'd open**, but the **kdenlive install
bundles the entire MLT runtime**, and it is fully usable from the command line:

```
$ which melt
/c/Program Files/kdenlive/bin/melt        (melt.exe 7.37.0, Meltytech LLC)
```

`C:\Program Files\kdenlive\bin\` ships a **self-contained** media stack:
`melt.exe`, `libmlt-7.dll`, `libmlt++-7.dll`, its **own** `ffmpeg.exe` + `avcodec-62.dll`,
and Qt6 DLLs. (Total kdenlive install ≈ 631 MB, but it's already there and we'd only
shell out to one binary.)

**Verified live, this session (all on the real PC):**

1. **`melt` is callable from any working directory via absolute path** and still finds
   its modules — i.e. it behaves exactly like the `ffmpeg` we already exec from Go:
   ```
   $ cd / && "/c/Program Files/kdenlive/bin/melt.exe" -version   → melt.exe 7.37.0, exit 0
   ```
2. **MLT renders to MP4** (the `avformat` consumer) and **reads any source** (the
   `avformat` producer). 617 filters available, including the ones this use case needs.
3. **The full forensic core ran in ONE command** — multi-source cut + a running
   timecode lower-third burned in + render to a valid MP4:
   ```
   melt -profile atsc_720p_30 \
     srcA.mp4 in=30 out=75 \           # clip = SOURCE-original frames 30..75 (non-destructive)
     srcB.mp4 in=15 out=60 \           # next clip, different source
     -filter dynamictext argument="#timecode#" geometry="20 200 280 30" \
             size=24 bgcolour="0x00000080" \
     -consumer avformat:OUT.mp4 vcodec=libx264 acodec=aac
   # → OUT.mp4, 3.072 s, valid. Verified with ffprobe.
   ```

### Why MLT's `dynamictext` filter is a gift for the lower-thirds spec

The use case wants lower-thirds with **source filename, running ORIGINAL-file timecode,
date, person, location**. MLT's `dynamictext` filter does the hard part natively —
quoting its own schema (`melt -query filter=dynamictext`):

- `#timecode#` / `#smpte_df#` / `#smpte_ndf#` — SMPTE timecode of the frame, computed
  from the source's own framerate+position (i.e. the **original-file running timecode**,
  exactly what a detective must cite).
- `#frame#` — frame number.
- `#createdate#`, `#filedate#`, `#localfiledate#` — file dates.
- `#resource#` — **the resource (source filename) that produced the frame.**
- Arbitrary frame metadata: `#meta.media.0.codec.frame_rate#`, etc.
- `strftime` formatting, e.g. `#localtime %I:%M:%S %p#`.

There is also `gpstext` (overlay GPS/location text from a `.gpx`/`.tcx` sidecar — useful
if footage carries location data) and `qtext` (Qt-rendered styled text).

With raw ffmpeg you reproduce this with `drawtext` (`timecode=`, `text=`, `fontfile=`,
`box=1`) — totally doable (ffmpeg here is built `--enable-libass --enable-libfreetype
--enable-fontconfig`), but you compute the running timecode and inject the filename
yourself per clip. **MLT gives "running original-file timecode + source filename"
for free, frame-accurate, per source** — which is the single fiddliest part of the
forensic burn. That is the concrete reason to keep `melt` as an optional backend rather
than dismiss it.

### The MLT XML project = the EDL-equivalent, and it is non-destructive

`melt ... -consumer xml:project.mlt` emits an editable XML project. The clip references
look like this (real output, trimmed):

```xml
<producer id="producer0" in="30" out="75">
  <property name="resource">srcA.mp4</property>     <!-- points at the ORIGINAL file -->
  <property name="length">90</property>
  <property name="mlt_service">avformat</property>
</producer>
```

It references the **original file by frame range** — the original is never written.
This is the canonical "assemble = a list of (source, in, out)" model, already serialized
and diff-able. becky's engine should keep its **own** small JSON timeline as the source
of truth (deterministic, the becky way) and *generate* either an ffmpeg concat plan or a
`.mlt`/CMX3600 EDL from it. The `.mlt` is a nice interchange/debug artifact for free.

---

## kdenlive — architecture & verdict

- **Architecture:** kdenlive is a **GPL** desktop NLE built on **KDE Frameworks + Qt**,
  with **MLT** as its rendering/timeline engine and `melt` as the headless renderer.
  All the actual media muscle is MLT/ffmpeg; kdenlive is the Qt UI + project model on top.
- **Drive it headlessly / reuse a component?** The reusable component *is MLT/`melt`* —
  and we can use that directly (above) **without** kdenlive. kdenlive itself has no
  scriptable "build a supercut from N sources and render" entry point a detective could
  drive; its automation surface is "open `melt` on a `.kdenlive`/`.mlt` project," which
  is just MLT again. So "reuse kdenlive" collapses to "reuse MLT," which we do.
- **Build/integrate on Windows TODAY?** Not realistically — KDE Frameworks + Qt6 + MLT
  from source on Windows is a heavy, multi-day build (Qt6 isn't even installed). The
  *binary* is here, but that's the MLT runtime, which we already exploit.
- **License:** **GPL.** Shipping kdenlive *alongside* becky, or linking anything of it,
  contaminates becky. Calling the bundled `melt.exe` as a **separate process** is the
  clean line (see license section). We must not vendor kdenlive or redistribute it as
  part of becky.

**Verdict: do not reuse the kdenlive application. Reuse only its bundled `melt` runtime,
as an optional subprocess backend.**

---

## shotcut / MLT — architecture & verdict

- **Architecture:** shotcut is a **Qt UI over MLT** (same engine as kdenlive). Same
  story: the reusable substance is **MLT**, not the shotcut shell.
- **`melt` as a reusable render/timeline backend we call like ffmpeg?** **Yes —
  confirmed working on this PC** (the entire section above). `melt` is explicitly
  designed for external automation: ordered positional clips with `in=`/`out=`,
  `-filter`/`-attach` for effects, `-transition` for overlaps, `-consumer avformat:...`
  for rendering, and `-serialise` to save the assembled command. The MLT **XML** format
  is its stable project/interchange representation.
- **Windows availability of a prebuilt `melt`/libmlt?** **Already present** via the
  kdenlive install (`melt.exe 7.37.0` + `libmlt-7.dll`). Shotcut's own installer also
  bundles `melt`/libmlt if we ever wanted a standalone copy, but we don't need to install
  anything.
- **License:** MLT core is **LGPL-2.1** (per the `dynamictext` schema header:
  `license: LGPLv2.1`, and the project states LGPL-2.1 / GPL-2 / GPL-3). The **`frei0r`**
  plugin pack is **GPL** — we simply **do not use any `frei0r.*` filter** (we don't need
  them; `dynamictext`/`gpstext`/`affine`/`watermark`/`avfilter.*` cover us). shotcut the
  *app* is GPL — irrelevant, we don't ship it.
- **Is MLT meaningfully better than raw ffmpeg `concat`+`drawtext`, or overkill?**
  - **Better at:** per-source running-timecode + filename burn (`dynamictext` keywords),
    a real multi-track timeline with transitions/overlaps, a stable editable XML project,
    and clip-localized effects without hand-computing frame offsets.
  - **Overkill for:** a plain cut-and-join with a simple static lower-third — raw ffmpeg
    does that in one `concat` + `drawtext` pass with zero extra dependency.
  - **Conclusion:** not overkill given the forensic burn requirements, but **not required
    either.** Treat it as the premium burn backend, with ffmpeg as the always-there default.

**Verdict: do not reuse the shotcut application. `melt` (the MLT engine) is a legitimate,
license-clean, already-installed RENDER backend — adopt it behind a backend seam, ffmpeg-first.**

---

## Prior art worth borrowing CODE / IDEAS from

| Repo | Lang | License | Borrow |
|---|---|---|---|
| **antiboredom/videogrep** | Python | **Anti-Capitalist License** (NOT OSS) | **IDEA ONLY — do NOT vendor.** |
| **mli/autocut** | Python | **Apache-2.0** | Code-safe. Transcript-as-edit-list model. |
| **mifi/lossless-cut** | JS/Electron | **GPL-2.0** | IDEA ONLY (don't link). Stream-copy rough-cut UX. |
| **m1guelpf/auto-subtitle** | Python | **MIT** | Code-safe. ffmpeg subtitle burn-in pattern. |

### 1. videogrep — the core technique (borrow the IDEA, not the code)
videogrep is the closest prior art to our core loop: parse `.srt`/`.vtt`/`.json`
transcripts → regex-search → for each hit cut a subclip → concatenate. Its mechanics
(quoting its `videogrep.py`):
```python
cut_clips.append(videofileclips[c["file"]].subclip(c["start"], c["end"]))
final_clip = concatenate_videoclips(cut_clips, method="compose")
final_clip.write_videofile(outputfile, codec="libx264", audio_codec="aac")
```
i.e. **(start,end) per transcript hit → subclip → concat → re-encode to H.264/AAC.**
That's the whole pattern we want, and it's a public-domain idea.

**LICENSE LANDMINE — read this twice:** videogrep's `LICENSE` is the **"Anti-Capitalist
License"**, a *non-open-source* license by Sam Lavigne with use-eligibility restrictions
(it restricts use by for-profit entities / certain employers). For a **forensic /
law-enforcement** tool that is exactly the kind of user such a license is designed to
exclude. **Do not copy any videogrep code into becky.** Re-implement the (trivial,
unencumbered) technique in Go-over-ffmpeg. It also depends on **MoviePy**, which we don't
want anyway — we drive ffmpeg directly for determinism.

### 2. autocut — the cleanest license + the right mental model
autocut (**Apache-2.0**, code-safe to read/adapt) turns editing into **editing a text
file**: it transcribes, you keep/cut by editing the transcript, it renders the kept
spans. That "transcript IS the timeline" model is exactly the detective's flow
(click a quote → that span becomes a clip). Safe to borrow code/structure from.

### 3. lossless-cut — the stream-copy speed trick (idea only; it's GPL-2.0)
The load-bearing idea: **`ffmpeg -ss … -t … -c copy`** extracts a segment **losslessly,
near-instantly** (no re-encode). **Verified live: 0.125 s** to extract a 1.5 s segment by
stream copy. Use this for **preview/scrub and a fast "rough EDL"** path. (Caveat: stream
copy can only cut on keyframes, so frame-exact cuts and the burned-in final still need a
re-encode pass — see the two-tier plan below.) Don't link/embed lossless-cut (GPL-2.0).

### 4. auto-subtitle — MIT ffmpeg burn-in pattern
auto-subtitle (**MIT**) = whisper transcript → `.srt` → ffmpeg overlays subtitles.
Clean reference for the `.srt` sidecar + ffmpeg `subtitles=`/`drawtext` burn path.

---

## EDL / SRT handling

- **.srt** — becky already parses SRT. For the output sidecar, emit a standard SRT where
  each entry's text is the transcript line and timings are **re-based to the compiled
  timeline** (cumulative clip offsets), while the lower-third still shows the
  **original** source timecode. SRT format is trivial (`index\nHH:MM:SS,mmm -->
  HH:MM:SS,mmm\ntext\n`); no library needed.
- **EDL (CMX3600)** — a plain-text list of events; each event = event#, 8-char reel/source
  name, edit type (`V`/`A`/`AA`), transition (`C`=cut), and 4 timecodes (source in/out,
  record in/out), with an `FCM:` header for drop/non-drop. For a cuts-only forensic
  compilation this is a tiny deterministic text emitter (a few dozen lines of Go) — write
  it directly. The 8-char reel-name limit is the one gotcha; map long filenames to a reel
  table and emit `* FROM CLIP NAME:` comments for the full path (standard EDL practice).
- **Don't pull a heavy EDL library.** If you ever need round-trip robustness,
  **OpenTimelineIO**'s `otio-cmx3600-adapter` (Python, Apache-2.0) is the reference
  implementation to consult — but for cuts-only emit, hand-rolled Go is simpler and stays
  single-tool. Treating becky's own JSON timeline as the source of truth (and EDL/MLT/SRT
  as *exports*) keeps it deterministic.

---

## Recommended shape for the becky tool (the confirmed lean, refined)

A single becky tool, JSON-in → artifacts-out, originals read-only:

1. **Source of truth:** a small deterministic **becky timeline JSON**
   (`[{source, src_in, src_out, lowerthird:{person,location,date,...}}]`). The AI router
   and the GUI both edit *this*. Everything else is generated from it.
2. **Preview/scrub (instant):** `ffmpeg -ss -t -c copy` for sub-second, lossless segment
   audition (lossless-cut's trick). Keyframe-approximate is fine for preview.
3. **Render (frame-exact, burned):** a backend seam with two implementations:
   - **`ffmpeg` (default, always present):** per clip, re-encode the exact span and
     `drawtext` the lower-third (timecode computed by becky, filename injected); join with
     the **concat demuxer**. Verified live (453x realtime).
   - **`melt` (optional, premium burn):** emit the assembled `melt` command / `.mlt`
     with `dynamictext` doing per-source `#timecode#` + `#resource#` automatically; render
     via `-consumer avformat`. Verified live end-to-end on this PC.
   Pick `melt` when present for the highest-fidelity forensic timecode burn; else ffmpeg.
4. **Exports:** the final **MP4**, a **CMX3600 EDL** (hand-rolled Go), a re-based
   **.srt**, and **frame stills** (`ffmpeg -ss … -frames:v 1`). All deterministic.
5. **GUI:** a thin shell only. (Spikes already exist in
   `becky-clip-work/spikes/{gio,webview2}` — consistent with this direction.)

This keeps becky's invariants: offline, deterministic (fixed encode params/seeds),
degrade-never-crash (if `melt` is absent, fall back to ffmpeg; if a source is missing,
skip-with-reason), and **originals are only ever read**.

---

## Blunt risk note — GPL contamination

- **kdenlive = GPL. shotcut = GPL. lossless-cut = GPL-2.0.** **Never bundle, redistribute,
  or statically link any of these with becky.** Doing so would force becky's distribution
  under GPL. We don't link them — we either don't use them (kdenlive/shotcut/lossless-cut
  apps) or we shell out to a separate process.
- **`melt.exe` (MLT) — the one we DO use:** MLT core is **LGPL-2.1**. Invoking `melt.exe`
  as a **separate process** (no linking, just argv + files, exactly how becky already
  calls ffmpeg) does **not** impose GPL/LGPL obligations on becky's own code — process
  invocation is the universally-accepted clean boundary (same reason calling `ffmpeg.exe`
  is fine). Two cautions: (a) **do not use any `frei0r.*` filter** — frei0r is **GPL**;
  we don't need it. (b) We must **not redistribute** the kdenlive/MLT binaries *as part of*
  becky; we rely on the user's existing install (it's already on the PC) and degrade
  gracefully if `melt` isn't found. Document MLT/LGPL + ffmpeg attribution in becky's
  NOTICE.
- **videogrep = Anti-Capitalist License (non-OSS, use-restricted).** **Do not copy its
  code.** The technique is free to reimplement; the source file is not free to use,
  especially for a law-enforcement/forensic context. This is the sharpest landmine here
  precisely because videogrep looks like the perfect starting point.
- **Code-safe to read/borrow:** **autocut (Apache-2.0)** and **auto-subtitle (MIT)**.

---

## TL;DR for Jordan

Build the thin becky tool on ffmpeg — that was the right instinct. Bonus: the
**MLT engine (`melt`) is already installed** on your PC (it came with kdenlive) and it
burns the forensic lower-third (running original-file timecode + source filename) better
and more easily than raw ffmpeg, with a clean LGPL license when we just call it like we
call ffmpeg. So becky calls **ffmpeg by default and `melt` when it's there** — both work
**today**, nothing to compile, originals never touched. Don't reuse the kdenlive/shotcut
**apps** (GPL, heavy, not scriptable for this), and **don't copy videogrep's code** (its
license bans the kind of use this tool is for) — just reuse its (trivial) cut-and-join
idea.
