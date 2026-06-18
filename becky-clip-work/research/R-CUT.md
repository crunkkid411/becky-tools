# R-CUT — The Cutting & Rendering Engine for becky-clip (decided with live tests)

**Date:** 2026-06-18
**Question:** What engine does becky-clip use to CUT [in,out] clips from MULTIPLE source
videos and RENDER one frame-accurate compilation MP4 with an unobtrusive
running-original-timecode lower-third? Plus: should we integrate lossless-cut? And what
exactly does the user's "one ffmpeg frame ≠ one video frame" mean?

Every claim below was settled with LIVE ffmpeg / melt / auto-editor runs on Jordan's PC.
Test artifacts + proof screenshots live in `becky-clip-work/cut-tests/`.

---

## DECISION (decisive)

**DEFAULT engine = raw ffmpeg (accurate-seek re-encode + concat + `drawtext`), one pass,
`h264_nvenc` per becky config.** It is the only engine that does the whole forensic job —
frame-accurate multi-source assembly **and** the running **original-file** timecode
lower-third — in a single, fastest, deterministic pass, with nothing to install beyond the
ffmpeg becky already shells.

**OPTIONAL/secondary = auto-editor for the CUT step only** (it is already wired into
becky-cut/becky-export and renders a hand-authored multi-source v3 timeline frame-accurately
with nvenc). But it has **no text/timecode overlay**, so it always needs a second ffmpeg
`drawtext` pass for the lower-third — which is why ffmpeg is simpler end-to-end.

**`melt` (MLT) is NOT recommended as the primary render path** despite being installed and
despite R-REUSE's enthusiasm. Live testing found a disqualifying limitation for THIS use
case: **melt's `dynamictext #timecode#` cannot show the original-file timecode** — it shows
either the compiled-timeline position (global filter) or the clip-relative position
(per-clip), never "frame 315 of the original." It is also ~14× slower on short jobs. Keep
melt only as an optional `.mlt` interchange/debug export, not the burn engine.

**lossless-cut: do NOT integrate.** It is a standalone GPL-2.0 Electron desktop app, not a
component. Borrow only its idea (stream-copy for instant preview). See §1.

### Reconciling with the user's auto-editor suggestion (honest)
Jordan's instinct — "auto-editor commands directly integrated might be the simplest" — is
**half right and worth honoring**: auto-editor genuinely CAN assemble arbitrary multi-source
[in,out] clips (proven below), it's already in becky, and it's frame-accurate. **But it
cannot burn the lower-third**, which is a hard requirement, so it can't be the *whole*
engine. The simplest correct design is therefore: **ffmpeg does it all in one pass by
default**; auto-editor stays available as the cut/assembly backend behind the same becky
timeline JSON when you want to reuse the proven becky-cut path, with ffmpeg adding the burn.
Net: adopt ffmpeg as primary, keep auto-editor as a first-class alternative cut backend —
don't make melt the burn engine.

### Reconciling with R-REUSE (which recommended melt for the burn)
R-REUSE was right that melt is installed, license-clean, and frame-accurate for multi-source
assembly. It was **wrong on the single load-bearing point**: it claimed `#timecode#` gives
"original-file running timecode for free." Live test refutes that (see §4, screenshots).
melt's timecode resets per clip/timeline — useless for "verify against the original file."
ffmpeg `drawtext timecode=<source-in>` produces the correct original timecode; melt does not.
**This is the deciding evidence and it flips the burn engine from melt to ffmpeg.**

---

## 1. lossless-cut — assessment & verdict

**What it is (confirmed via GitHub API):** `mifi/lossless-cut`, TypeScript/**Electron**
desktop GUI, **GPL-2.0**, 41k stars, "swiss army knife of lossless video/audio editing." It
is a front-end over `ffmpeg -c copy` — it cuts/joins/remuxes **without re-encoding**, so it's
near-instant and pixel-identical to the source, but it can only cut on **keyframes** (it
exposes this in its UI as "smart cut" when you need a frame-exact point).

**Can it be a component?** No. It is a packaged Electron application (its own Chromium +
Node bundle, ~150–200 MB), with no headless "assemble from N sources + burn a lower-third +
render" entry point a detective could script. There is nothing inside it to link or embed
that we don't already have — the actual muscle is `ffmpeg`, which becky already drives.

**License:** GPL-2.0 → bundling/redistributing or linking it contaminates becky's
distribution. Calling it would mean shipping/launching a separate GPL app — exactly what
becky avoids.

**Verdict: do NOT integrate lossless-cut.** It is an *idea*, not a component. The idea worth
keeping — **`ffmpeg -ss … -t … -c copy` for instant, lossless PREVIEW/scrub and a fast rough
EDL** — is already in the plan (R-STACK's `<video>` preview + this doc's proxy path). For the
authoritative forensic OUTPUT we re-encode (frame-exact), because lossless copy can't hit a
non-keyframe cut point (proven next).

---

## 2. The frame-accuracy truth — DEMONSTRATED LIVE (the user's core point)

The user is **exactly right**: *"one 'frame' according to ffmpeg is NOT one video frame."*
Two distinct gotchas are folded into that statement; both were reproduced on the PC.

### The test rig
A 20-second, 30 fps clip with a **burned-in frame counter + timestamp**, encoded with a
**long GOP (`-g 250`)** so keyframes (I-frames) sit only at **0.000s, 8.333s, 16.667s**
(verified with ffprobe packet flags). Source: `cut-tests/test_longgop.mp4`.

```bash
ffmpeg -f lavfi -i testsrc=size=640x360:rate=30 -t 20 -pix_fmt yuv420p -c:v libx264 -g 250 \
  -vf "drawtext=text='FRAME %{n}':...,drawtext=text='t=%{pts\:hms}':..." test_longgop.mp4
```

### Gotcha A — keyframe-aligned copy seeking (the big one)
**Ask:** cut starting at **t=10.000s = FRAME 300** (mid-GOP, 1.667s past the 8.333s keyframe).

| Method | Command shape | Result | Frames / duration |
|---|---|---|---|
| **Lossless copy** | `-noaccurate_seek -ss 10.0 -i src -t 2.0 -c copy` | **SLIPPED to the 8.333s keyframe** | **62 frames / 3.734s** (asked for 60/2.0) |
| **Accurate re-encode** | `-ss 10.0 -to 12.0 -i src -c:v libx264` | **landed on the exact frame** | **60 frames / 2.000s** ✓ |

**PROOF SCREENSHOTS** (`cut-tests/`):
- `slip_PROOF_copy_starts_frame250.png` — the lossless copy-cut's actual first stored
  frame reads **`FRAME 250 / t=00:00:08.333`**. We asked for frame 300; it gave frame **250**
  — a **50-frame / 1.667-second slip backward** to the previous keyframe.
- `accurate_PROOF_starts_frame300.png` — the accurate re-encode's first frame reads
  **`FRAME 300 / t=00:00:10.000`** — exactly what was requested.

So "1 frame to ffmpeg" in copy mode = "1 GOP" (here 250 frames / up to 8.3s). On real
forensic footage with 2–10s GOPs, a lossless cut can be **seconds off** — unacceptable for
evidence. **You must re-encode for a frame-exact cut.** (Caveat worth knowing: on a *short*
GOP file — `test_shortgop.mp4`, keyframe every 0.5s — the same copy slip is bounded to ≤0.5s;
the error scales with GOP length, which is unknown/uncontrolled in evidence.)

> Subtlety the test also exposed: ffmpeg 4.3.1's `-ss` *before* `-i` with `-c copy` writes an
> mp4 **edit list** so a *strict* player still presents frame 300 first — but the extra
> ~1.7s of pre-roll is physically in the file (the 62-frame / 3.734s container), and any
> player or `<video>` path that **ignores the edit list** shows the slip (that is how
> `slip_PROOF_copy_starts_frame250.png` was produced: `ffprobe/ffmpeg -ignore_editlist 1`).
> Evidence integrity means the *file* must be exactly the clip — so: re-encode.

### Gotcha B — input-seek vs output-seek (`-ss` before vs after `-i`)
- **`-ss` BEFORE `-i` (input seek):** fast — ffmpeg seeks to the nearest keyframe *before*
  decoding. Frame-accurate **only when re-encoding** (it then decodes+drops to the exact
  frame). With `-c copy` it cannot, hence Gotcha A.
- **`-ss` AFTER `-i` (output seek):** decodes from 0 and discards — always frame-exact but
  slow on a late start. With `-c copy` it is also **broken**: our `-i src -ss 10 -t 2 -c copy`
  produced a **262-byte file with no playable stream** (you cannot copy a GOP from a
  non-keyframe). Output-seek is only for re-encode.

**Takeaway:** for becky's authoritative cut, **always re-encode**, and prefer **input-seek
`-ss <in>` before `-i` with `-to <out>`** (fast + frame-exact). Output-seek is the slow
fallback only if a specific file mis-seeks.

### The CORRECT frame-accurate seek pattern (tested)
```bash
# Frame-exact single-clip extract, re-encoded (input-seek + -to). 60 frames / 2.000s exactly.
ffmpeg -ss 10.0 -to 12.0 -i SRC.mp4 -c:v h264_nvenc -pix_fmt yuv420p \
       -avoid_negative_ts make_zero OUT.mp4
```
`-to` is an absolute source timestamp here. To be build-independent, becky should compute and
pass **`-t (out-in)`** instead of `-to` when there's any ambiguity; both were tested and both
yield the exact 60/2.0 result on this build. `-avoid_negative_ts make_zero` cleans the
edit-list/PTS so the output starts cleanly at 0.

---

## 3. Engine bake-off (multi-source forensic assembly) — live results

Job for every engine: **clip A = `test_longgop.mp4` frames 300–360** (src-in 10.0s) **then
clip B = `test_srcB.mp4` frames 90–150** (src-in 3.0s) → one 4.0s / 120-frame MP4.

| Engine | Frame-accurate? | Multi-source assemble? | Per-source running ORIGINAL timecode burn? | h264_nvenc? | Speed (4s job) | Deterministic? | Functional today? | Integration simplicity |
|---|---|---|---|---|---|---|---|---|
| **raw ffmpeg** (re-encode+concat+drawtext) | **YES** (60/2.0 exact) | **YES** (concat demuxer / `concat` filter) | **YES** — `drawtext timecode=<src-in>` per clip → **proven `00:00:10:15` on frame 315** | **YES** (verified codec_name=h264) | **313 ms** libx264 / **370 ms** nvenc | YES (fixed params) | YES | **HIGHEST** — becky already shells ffmpeg; one binary, one pass |
| **auto-editor** (v3 multi-source timeline) | **YES** (60/2.0 exact, frames 315 / 105) | **YES** — hand-built v3 timeline (`src`+`offset` per clip) renders | **NO** — zero text/overlay capability (help shows only edit/speed/zoom/invert) → needs a 2nd ffmpeg drawtext pass | **YES** (`-c:v h264_nvenc`, codec_name=h264) | **455 ms** (overlay extra) | YES | YES (already in becky) | HIGH for cut; MEDIUM overall (2 passes for the burn) |
| **melt (MLT)** | **YES** (120 frames, frames 315 / 105) | **YES** (positional `src in=N out=N`) | **NO (disqualifying)** — `#timecode#` shows TIMELINE pos (global) or CLIP-relative pos (per-clip), **never the original-file TC**; `#resource#` (filename) DOES work | YES (its own bundled ffmpeg) | **4315 ms** (~14× slower; Qt/MLT startup) | YES | YES (installed via kdenlive) | MEDIUM — extra binary, depends on kdenlive install present |
| **avcut** | n/a | n/a | n/a | n/a | **NOT INSTALLED** | n/a | **NO** | NONE — not on PATH; a niche C tool that needs a build toolchain. Skip. |

**Proof screenshots** (`cut-tests/`):
- ffmpeg assembly correct frames: `asm_ff_clipA.png` (FRAME 315), `asm_ff_clipB.png` (SRC-B FRAME 105).
- auto-editor multi-source correct frames: `ae_multi_A.png` (FRAME 315), `ae_multi_B.png` (SRC-B FRAME 105).
- melt assembly: `asm_melt_clipA.png` / `asm_melt_clipB.png` — note the burned TC reads
  `00:00:00:15` (timeline/clip-relative), NOT the source frame 315/105 → the melt limitation.
- ffmpeg ONE-PASS full forensic result (assemble + original-TC lower-third): `ff_full_B.png`
  shows SRC-B FRAME 105 with burned **`00:00:03:15`** = srcB original frame 105. ✓

**Notes on availability discovered live:**
- Two ffmpegs on the machine: **anaconda `ffmpeg 4.3.1`** (becky's config default,
  `C:\ProgramData\anaconda3\Library\bin\ffmpeg.exe`) and a full gyan build (2025-05-05). **Both
  have `drawtext`+libfreetype and `h264_nvenc`/`hevc_nvenc`.** The 4.3.1 build emits a benign
  `Fontconfig error: Cannot load default config file` and falls back to a default font — to
  pin the forensic font deterministically, becky should pass an explicit `fontfile=`.
- auto-editor here is **v29.8.1** (modern; v3 timeline). Its v3 JSON (`"v":[[{src,start,dur,
  offset,stream}]]`) IS multi-source — each clip names its own `src` + source-in `offset`.

---

## 4. Lower-third with running ORIGINAL timecode — tested recipes

The forensic lower-third needs: **source filename + running ORIGINAL-file timecode** (so a
detective can scrub to that timecode in the original) **+ date + person + location**, all
unobtrusive.

### 4a. ffmpeg path (RECOMMENDED — produces the correct original timecode)
Per clip, set `drawtext`'s `timecode=` start to that clip's **source-in timecode**; ffmpeg
advances it one frame at a time at `rate=`. Add a second `drawtext` for the metadata line.

```bash
# Clip starts at SOURCE frame 300 (=00:00:10:00). Burned TC then reads the ORIGINAL file TC.
ffmpeg -ss 10.0 -to 12.0 -i SRC.mp4 -an \
  -vf "drawtext=timecode='00\:00\:10\:00':rate=30:timecode_rate=30:\
text='ORIG TC':x=20:y=h-60:fontsize=22:fontcolor=white:box=1:boxcolor=black@0.6:fontfile='C\:/Windows/Fonts/consola.ttf', \
       drawtext=text='SRC.mp4 | J.DOE | KITCHEN | 2026-06-18':\
x=20:y=h-30:fontsize=18:fontcolor=white:box=1:boxcolor=black@0.55:fontfile='C\:/Windows/Fonts/consola.ttf'" \
  -c:v h264_nvenc -pix_fmt yuv420p OUT.mp4
```
**PROVEN:** `cut-tests/ff_lower3rd_A.png` — burned **`ORIG TC 00:00:10:15`** on the frame the
source counter independently labels **FRAME 315** (300 + 15 = 10s 15f). The metadata line
`SRC.mp4 | J.DOE | KITCHEN | 2026-06-18` renders on the bottom row, semi-transparent box,
unobtrusive. **For each clip, becky computes the `timecode=` start string from that clip's
`src_in` frame** — that's the whole trick, and it's deterministic.

Windows `drawtext` quoting gotchas (becky already handles the cousin of this in
`cmd/export/escapeSubsPath`): escape the colons in `timecode='HH\:MM\:SS\:FF'`, and use a
forward-slash, colon-escaped `fontfile='C\:/Windows/Fonts/consola.ttf'`. Pin a monospaced
font (Consolas) so the timecode digits don't jitter.

### 4b. melt path (works, but timecode is NOT the original file TC — use only `#resource#`)
```bash
melt -profile atsc_720p_30 \
  SRC.mp4 in=300 out=359 -attach-clip dynamictext:"src=#resource#  TC=#timecode#" \
        geometry="20 300 600 30" size=26 fgcolour="0xffff00ff" bgcolour="0x000000cc" \
  -consumer avformat:OUT.mp4 vcodec=libx264 acodec=aac
```
**PROVEN:** `cut-tests/melt_perclip_A.png` — `#resource#` correctly prints `test_longgop.mp4`,
but `#timecode#` prints **`00:00:00:15`** (15 frames into the clip), **not** the original
`00:00:10:15`. `melt_perclip_B.png` shows the same reset on srcB. **Therefore melt's
dynamictext is unsuitable for the original-file-timecode requirement.** (If melt were ever
used for assembly, the original TC would still have to be drawn by an ffmpeg pass — so just
use ffmpeg.)

### 4c. Frame → PNG (frame-accurate still export)
```bash
# By exact frame NUMBER (forensic-precise, no time rounding) — RECOMMENDED:
ffmpeg -i SRC.mp4 -vf "select=eq(n\,437)" -vsync 0 -frames:v 1 still.png
# By time (convenient, but -ss rounds to nearest displayed frame):
ffmpeg -ss 14.5667 -i SRC.mp4 -frames:v 1 -update 1 still.png
```
**PROVEN:** `cut-tests/still_exact437.png` reads exactly **FRAME 437 / 14.567s** (the
frame-number form is exact); the time form landed on 438 (rounded). Use `select=eq(n,N)` for
evidence stills.

### 4d. Proxy transcode (for `<video>` preview of exotic/CCTV codecs)
```bash
ffmpeg -i EXOTIC.mkv -c:v libx264 -preset veryfast -pix_fmt yuv420p \
       -movflags +faststart -c:a aac proxy_websafe.mp4
```
**PROVEN:** transcoded an HEVC source → H.264 in **297 ms** (449 fps), `codec_name=h264`,
**`moov` atom before `mdat`** (faststart confirmed) so the WebView2 `<video>` can range-seek
instantly. The durable Go engine keeps cutting from the **original** for frame accuracy; the
proxy is preview-only (R-STACK's plan, now verified).

---

## 5. Copy-paste TESTED command templates (the becky-clip render kit)

```bash
# ── Vars becky fills per clip from its timeline JSON ──
#   SRC, IN (seconds), OUT (seconds), SRC_IN_TC ("HH:MM:SS:FF"), FPS, CODEC=h264_nvenc
#   META = "filename | PERSON | LOCATION | DATE"
#   FONT = 'C\:/Windows/Fonts/consola.ttf'

# 1) FRAME-ACCURATE single-clip extract (re-encode, input-seek). Use -t (OUT-IN) for safety.
ffmpeg -ss $IN -i "$SRC" -t $(awk "BEGIN{print $OUT-$IN}") \
  -c:v $CODEC -pix_fmt yuv420p -avoid_negative_ts make_zero clipN.mp4

# 2) ONE-PASS multi-source assemble + ORIGINAL-TC lower-third (THE default render).
#    (filter_complex: per-input drawtext + scale/fps normalize + concat. Shown for 2 clips.)
ffmpeg \
  -ss 10.0 -to 12.0 -i A.mp4  -ss 3.0 -to 5.0 -i B.mp4 \
  -filter_complex "\
   [0:v]drawtext=timecode='00\:00\:10\:00':rate=30:x=20:y=h-30:fontsize=20:fontcolor=white:box=1:boxcolor=black@0.6:fontfile=$FONT,\
        scale=640:360,fps=30,setpts=PTS-STARTPTS[a];\
   [1:v]drawtext=timecode='00\:00\:03\:00':rate=30:x=20:y=h-30:fontsize=20:fontcolor=white:box=1:boxcolor=black@0.6:fontfile=$FONT,\
        scale=640:360,fps=30,setpts=PTS-STARTPTS[b];\
   [a][b]concat=n=2:v=1:a=0[out]" \
  -map "[out]" -c:v $CODEC compilation.mp4

# 2-alt) Assemble pre-cut, pre-burned clips via the concat DEMUXER (fast, no re-encode of
#        the already-normalized clips). Each clipN.mp4 must share fps/res/codec.
printf "file 'clip0.mp4'\nfile 'clip1.mp4'\n" > list.txt
ffmpeg -f concat -safe 0 -i list.txt -c copy compilation.mp4

# 3) LOWER-THIRD with original TC + metadata line (single clip; see §4a). PROVEN.
ffmpeg -ss $IN -to $OUT -i "$SRC" -an \
  -vf "drawtext=timecode='$SRC_IN_TC':rate=$FPS:timecode_rate=$FPS:text='ORIG TC':x=20:y=h-60:fontsize=22:fontcolor=white:box=1:boxcolor=black@0.6:fontfile=$FONT,\
       drawtext=text='$META':x=20:y=h-30:fontsize=18:fontcolor=white:box=1:boxcolor=black@0.55:fontfile=$FONT" \
  -c:v $CODEC -pix_fmt yuv420p OUT.mp4

# 4) FRAME → PNG (exact frame number; forensic still). PROVEN.
ffmpeg -i "$SRC" -vf "select=eq(n\,$FRAMENO)" -vsync 0 -frames:v 1 still.png

# 5) PROXY for <video> preview of exotic codecs. PROVEN.
ffmpeg -i "$SRC" -c:v libx264 -preset veryfast -pix_fmt yuv420p -movflags +faststart -c:a aac proxy.mp4

# 6) (optional) auto-editor multi-source CUT backend — feed a hand-built v3 timeline.
#    Frame-accurate + nvenc, but NO overlay → still pass result through template #3 for the burn.
#    ae_multi.v3: {"version":"3","timebase":"30/1",..,"v":[[{"src":"A.mp4","start":0,"dur":60,"offset":300,"stream":0},
#                                                            {"src":"B.mp4","start":60,"dur":60,"offset":90,"stream":0}]],"a":[]}
auto-editor ae_multi.v3 -o assembly.mp4 --progress none -c:v h264_nvenc
```

---

## 6. Recommended shape for the becky-clip engine

1. **Source of truth = becky's own timeline JSON** (`[{source, src_in, src_out, fps,
   lowerthird:{person,location,date}}]`). The AI router + GUI edit this; everything is
   generated from it. (Same posture as becky-cut's v1 timeline and R-REUSE's recommendation.)
2. **Preview/scrub = `<video>` element** on the original (R-STACK), with an **ffmpeg `-c copy`
   sub-second rough-extract** for audition and an **ffmpeg proxy** when the codec isn't
   web-playable (§4d). Keyframe-approximate is fine for preview.
3. **Authoritative render = raw ffmpeg, one pass** (template #2): per-clip accurate-seek
   re-encode + per-clip `drawtext` original-TC lower-third + `concat`, `h264_nvenc`. This is
   the default and is the fastest path that also burns the correct forensic timecode.
4. **Backend seam (optional):** keep an `auto-editor` cut backend (reuse the proven becky-cut
   v3-timeline render) that produces the un-burned assembly, then run template #3 for the
   lower-third. Keep a `melt` **`.mlt` export** only as interchange/debug. **Do not** make
   melt the burn engine. **Do not** integrate lossless-cut.
5. **Exports:** final MP4 + CMX3600 EDL (hand-rolled Go, per R-REUSE) + re-based SRT +
   frame-exact stills (template #4). All deterministic, fixed encode params.
6. **Invariants honored:** offline; deterministic (pinned params, explicit `fontfile`, fixed
   fps/res); degrade-never-crash (proxy if codec unplayable; skip-with-reason on a missing
   source); **originals only ever READ** — verified: after every cut/assemble/still op this
   session, `test_longgop.mp4` / `test_srcB.mp4` mtimes were unchanged (created 14:22 / 14:25,
   never rewritten).

---

## 7. Honest risks

- **Re-encode is mandatory for frame accuracy → it costs CPU/GPU time and is lossy vs the
  original.** That is the correct trade for evidence (the *file* must be exactly the clip).
  Use a high-quality nvenc setting (e.g. `-rc vbr -cq 19` or a high `-b:v`) so the compilation
  isn't visibly degraded; never `-c copy` for the authoritative output.
- **`h264_nvenc` needs the NVIDIA GPU + driver.** Present and verified on Jordan's PC. becky
  must **degrade to `libx264`** if nvenc init fails (it's only ~20% slower on these short jobs
  and identical in correctness) — but note becky-export currently *forbids* libx264. For
  becky-clip, allow a libx264 fallback guarded by "nvenc unavailable," or it will hard-fail on
  a GPU-less machine. (libx264 is also the safer deterministic default across machines.)
- **`drawtext` fontconfig warning on the anaconda ffmpeg.** Cosmetic, but it means the font is
  non-deterministic unless you pass an explicit `fontfile=`. **Always pass `fontfile`** (e.g.
  Consolas) for reproducible, jitter-free forensic text.
- **melt depends on the kdenlive install being present** (`C:\Program Files\kdenlive\bin`).
  Since we're NOT using melt for the burn, this risk is mostly retired — only relevant if the
  optional `.mlt` export is enabled.
- **`-to` vs `-t` ambiguity** across ffmpeg builds when combined with input `-ss`. Both gave
  the exact 60-frame result on 4.3.1, but becky should compute and pass **`-t (out-in)`** to be
  build-independent.
- **Multi-source normalization:** clips from different cameras will differ in fps/res/SAR/pixel
  format. The assembly **must** normalize (`scale`, `fps`, `setsar`, `format=yuv420p`,
  `setpts=PTS-STARTPTS`) before `concat`, or the join glitches. Template #2 does this; the
  concat **demuxer** (#2-alt) requires the clips already share parameters.

---

## TL;DR for Jordan

Your instinct was right twice. **(1)** "One ffmpeg frame isn't one video frame" — confirmed and
photographed: a lossless cut I asked to start at **frame 300** actually started at **frame 250**
(1.7 seconds early) because it can only cut on keyframes (`slip_PROOF_copy_starts_frame250.png`
vs `accurate_PROOF_starts_frame300.png`). So for evidence we **re-encode** to hit the exact
frame. **(2)** auto-editor *can* assemble clips from multiple videos (I proved it) and it's
already in becky — but it **can't draw the timecode caption**, so it can't be the whole tool.

The simplest correct engine is **plain ffmpeg, one command**: it cuts the exact frames from
each source, stamps each clip with its **original-file timecode** (so a detective scrubs the
original to that exact spot — proven, `00:00:10:15` on frame 315), joins them, and renders on
your GPU — and it's the **fastest** (0.3s vs melt's 4.3s on a 4-second test). **melt** (from
kdenlive) looked great but its timecode caption shows the *wrong* number (the position in the
compilation, not in the original file), so it's out for the caption. **lossless-cut** is a
separate GPL app, not a building block — skip it. Originals were never touched in any test.
