#!/usr/bin/env python3
"""roughcut.py - ONE CALL: a folder of raw takes -> a populated VEGAS Pro timeline.

    python roughcut.py --folder "X:\\Videos\\...\\23_hj-fbi-recap" --launch-vegas

Everything the caller has to know is the folder. This runs, in order:

  1. speechcut.py      measure each clip's own audio, keep only the speaking parts
  2. build_roughcut.py order the clips by camera timestamp, place quote markers
  3. verify_timeline.py measure the assembled result and print it
  4. BeckyRoughCut.cs  drive VEGAS Pro 18 headless to build and save rough_cut.veg

Nothing is transcoded and no source file is ever written to. Output lands in
<folder>\\_roughcut\\.

WHY THE DETECTION IS ADAPTIVE (read this before "improving" it)
--------------------------------------------------------------
These clips were recorded on a RODE WIRELESS GO II. That matters: it has a
ridiculously low noise floor (measured -75 to -93 dBFS across this footage) and
a quiet programme level (-30 to -42 dBFS). An iPhone 13 in the same room reads
completely differently. Every FIXED threshold that has been tried here failed on
one mic or the other, so the threshold is derived per file from that recording's
own dB histogram and there is deliberately no mic profile to pick. Do not
re-introduce a constant.
"""
from __future__ import annotations

import argparse
import glob
import os
import subprocess
import sys

HERE = os.path.dirname(os.path.abspath(__file__))
REPO = os.path.dirname(HERE)
VIDEO_EXT = (".mp4", ".mov", ".mkv", ".avi", ".m4v", ".mts", ".mxf")


def run(cmd, **kw):
    print(f"\n$ {' '.join(os.path.basename(c) if c.endswith('.py') else c for c in cmd[1:3])} ...",
          flush=True)
    r = subprocess.run(cmd, **kw)
    if r.returncode != 0:
        sys.exit(f"FAILED: {' '.join(cmd)}")
    return r


def main() -> None:
    ap = argparse.ArgumentParser(description="folder of raw takes -> VEGAS Pro rough cut")
    ap.add_argument("--folder", required=True)
    ap.add_argument("--outline", default=None,
                    help="markdown whose \"quoted strings\" become markers "
                         "(default: the first *.md in the folder with quotes in it)")
    ap.add_argument("--fps", type=float, default=30.0)
    ap.add_argument("--launch-vegas", action="store_true",
                    help="drive VEGAS Pro headless to build and save the .veg")
    ap.add_argument("--veg-name", default="rough_cut.veg")
    ap.add_argument("--no-vad", action="store_true",
                    help="skip the Silero pass that drops confident non-speech clips")
    ap.add_argument("--audio-gain-db", type=float, default=12.0)
    ap.add_argument("--place-quote-markers", action="store_true",
                    help="also drop a marker per quote at its best text match (a GUESS - off by default)")
    ap.add_argument("--no-open", action="store_true",
                    help="do not open QUOTES.md when finished")
    ap.add_argument("--pad-post", type=float, default=0.08)
    ap.add_argument("--vad-pct", type=float, default=20.0)
    a = ap.parse_args()

    folder = os.path.abspath(a.folder)
    if not os.path.isdir(folder):
        sys.exit(f"not a folder: {folder}")
    work = os.path.join(folder, "_roughcut")
    os.makedirs(work, exist_ok=True)

    vids = sorted(p for p in glob.glob(os.path.join(folder, "*"))
                  if p.lower().endswith(VIDEO_EXT))
    if not vids:
        sys.exit(f"no video files in {folder}")
    print(f"{len(vids)} clips in {folder}")

    # Word timings are optional but make two things better: quote markers get
    # located in the narration, and a non-speech drop needs a SECOND opinion
    # before it is allowed to delete anything.
    have_words = sum(1 for v in vids if os.path.exists(
        os.path.join(work, os.path.splitext(os.path.basename(v))[0] + ".words.json")))
    if have_words < len(vids):
        print(f"NOTE: word timings present for {have_words}/{len(vids)} clips "
              f"(run becky-transcribe first for the rest). Clips without them keep "
              f"every span - a drop is never made on the VAD alone.")

    spans = os.path.join(work, "speech_spans.json")
    cut = os.path.join(work, "vegas_cut.json")

    cmd = [sys.executable, os.path.join(HERE, "speechcut.py"), *vids,
           "--out", spans, "--fps", str(a.fps), "--words-dir", work,
           "--pad-post", str(a.pad_post), "--vad-pct", str(a.vad_pct)]
    if a.no_vad:
        cmd.append("--no-vad")
    run(cmd)

    outline = a.outline
    if outline is None:
        for m in sorted(glob.glob(os.path.join(folder, "*.md"))):
            if '"' in open(m, encoding="utf-8", errors="replace").read():
                outline = m
                break
    if outline:
        print(f"quote markers from: {os.path.basename(outline)}")
    else:
        outline = os.path.join(work, "_no_outline.md")
        open(outline, "w", encoding="utf-8").write("\n" * 21)
        print("no outline found - building without quote markers")

    run([sys.executable, os.path.join(HERE, "build_roughcut.py"),
         "--folder", folder, "--spans", spans, "--outline", outline,
         "--out", cut, "--save-veg", os.path.join(work, a.veg_name),
         "--audio-gain-db", str(a.audio_gain_db), "--fps", str(a.fps)]
        + (["--place-quote-markers"] if a.place_quote_markers else []))

    run([sys.executable, os.path.join(HERE, "verify_timeline.py"), cut])

    if a.launch_vegas:
        script = os.path.join(REPO, "vegas", "BeckyRoughCut.cs")
        exes = sorted(glob.glob(r"C:\Program Files\VEGAS\VEGAS Pro *\vegas1*.exe"))
        if not exes:
            sys.exit("no VEGAS Pro install found under C:\\Program Files\\VEGAS")
        env = dict(os.environ, BECKY_ROUGHCUT_JSON=cut)
        print(f"\nbuilding the timeline in {os.path.basename(exes[-1])} ...")
        subprocess.run([exes[-1], "-SCRIPT:" + script], env=env)
        log = cut + ".buildlog.txt"
        if os.path.exists(log):
            print(open(log, encoding="utf-8", errors="replace").read())
    else:
        print(f"\nvegas_cut.json ready: {cut}\n"
              f"re-run with --launch-vegas to build the .veg")



    # DELIVER, DO NOT POINT. A document Jordan still has to go and find is not
    # delivered - see ACCESSIBILITY.md. There is no default .md handler on this
    # PC, so Start-Process on the file silently does nothing; launch MarkText.
    quotes_doc = os.path.join(folder, "QUOTES.md")
    if not a.no_open and os.path.exists(quotes_doc):
        opened = False
        for mt in (os.path.expandvars(r"%LOCALAPPDATA%\Programs\MarkText\MarkText.exe"),
                   r"C:\Program Files\MarkText\MarkText.exe"):
            if os.path.exists(mt):
                subprocess.Popen([mt, quotes_doc])
                opened = True
                break
        print(f"\nyour quote list: {quotes_doc}"
              + ("  (opened for you)" if opened else
                 "  (could not find MarkText - open it yourself)"))

if __name__ == "__main__":
    main()
