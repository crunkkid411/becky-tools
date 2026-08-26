#!/usr/bin/env python3
"""build_roughcut.py - turn measured speech spans into a VEGAS Pro timeline.

Reads:
  speech_spans.json          from speechcut.py (the audio-measured keep spans)
  <clip>.words.json          Parakeet word timings, for locating the quotes
  fbi_video_script_outline.md the outline whose "quotes" become markers

Writes vegas_cut.json in the schema vegas/BeckyRoughCut.cs already assembles.

ORDERING. Clip order is the camera's own creation timestamp read out of the
file with ffprobe - NOT the filesystem's. On this footage every file's Windows
CreationTime is the moment it was copied to X: (all within 40 minutes of each
other), which would order the timeline almost backwards.

QUOTES. Every quoted string in the outline becomes a marker titled with the
quote verbatim. Each is located in the spoken narration by IDF-weighted content
word overlap - purely lexical, no model. A quote that cannot be located lands in
a labelled block after the end of the cut rather than being guessed onto a
position, which is what Jordan's own instructions ask for.

NOTHING IS TRANSCODED. Events point at the untouched source files with an
in/out; the originals are never read for anything but measurement.
"""
from __future__ import annotations

import argparse
import json
import math
import os
import re
import subprocess
from collections import Counter

STOP = set("""a an and are as at be been but by can cause could did do does doing done for from get got
had has have he her here hers him his how i if in into is it its just like me my no not of off on one
or our out over own re said say says she should so some such than that the their them then there these
they this those to too up us was we were what when where which who why will with would you your yeah
oh um uh okay ok gonna wanna really very actually thing things going know think want am been being""".split())


def probe_creation(path: str, ffprobe: str = "ffprobe") -> str:
    out = subprocess.run(
        [ffprobe, "-v", "error", "-show_entries", "format_tags=creation_time",
         "-of", "default=nw=1:nk=1", path],
        stdout=subprocess.PIPE, stderr=subprocess.DEVNULL, text=True).stdout.strip()
    return out or "9999"


def tokens(text: str) -> list[str]:
    # Split run-together names the outline writes as one word ("PoptartBarbie",
    # "#GreenNotClean") so they can match the narration, which says them apart.
    text = re.sub(r"(?<=[a-z0-9])(?=[A-Z])", " ", text)
    return [w for w in re.findall(r"[a-z0-9']+", text.lower())
            if len(w) >= 3 and w not in STOP]


def extract_quotes(md_path: str) -> list[str]:
    """Every double-quoted run in the outline body, in document order.

    Skips the instruction header and markdown headings - the legend calls the
    bracketed notes on CAPS headers scaffolding, not content.
    """
    lines = open(md_path, encoding="utf-8").read().split("\n")
    seen, out = set(), []
    for ln in lines[20:]:
        if ln.lstrip().startswith("#"):
            continue
        for q in re.findall(r'"([^"\n]{12,})"', ln):
            q = q.strip()
            if q and q not in seen:
                seen.add(q)
                out.append(q)
    return out


def load_timeline_words(clips, spans_by_src, work):
    """Words that SURVIVE the cut, stamped with their TIMELINE time."""
    words, tl = [], 0.0
    per_clip = []
    for path in clips:
        base = os.path.splitext(os.path.basename(path))[0]
        wj = os.path.join(work, base + ".words.json")
        src_words = json.load(open(wj, encoding="utf-8"))["words"] if os.path.exists(wj) else []
        start_tl = tl
        for s, e in spans_by_src[path]:
            for w in src_words:
                if s <= w["start"] < e:
                    words.append((tl + (w["start"] - s), w["word"]))
            tl += e - s
        per_clip.append((os.path.basename(path), start_tl, tl))
    return words, tl, per_clip


def locate_quotes(quotes, tl_words, min_mass=8.0):
    """Locate each quote in the spoken narration by IDF-weighted word overlap.

    Score is the ABSOLUTE IDF mass of the quote's distinct words found in a
    window, not the fraction of the quote matched: Jordan introduces these
    quotes in his own words rather than reading them, so a long quote will
    never match most of its own tokens. What actually identifies the spot is
    hitting the RARE words ("extraditable", "penguin", "florida"), which is
    exactly what IDF mass measures. min_mass is calibrated so that roughly two
    distinctive words, or one very rare one, is enough.
    """
    doc = [tokens(w)[0] if tokens(w) else "" for _, w in tl_words]
    times = [t for t, _ in tl_words]
    n = len(doc)
    df = Counter(t for t in doc if t)              # true document frequency
    idf = {t: math.log(n / df[t]) for t in df}     # common words -> near 0

    placed, unplaced = [], []
    for q in quotes:
        want = set(tokens(q))
        if not want:
            unplaced.append((q, 0.0))
            continue
        win = max(14, 2 * len(want))
        best, best_i, best_hit = 0.0, -1, set()
        for i in range(0, max(1, n - 1), 3):
            hit = want & set(doc[i:i + win])
            if not hit:
                continue
            mass = sum(idf.get(t, 0.0) for t in hit)
            if mass > best:
                best, best_i, best_hit = mass, i, hit
        if best >= min_mass and best_i >= 0:
            j = next((k for k in range(best_i, min(best_i + win, n)) if doc[k] in best_hit), best_i)
            placed.append((q, times[j], round(best, 2)))
        else:
            unplaced.append((q, round(best, 2)))
    return placed, unplaced


def main() -> None:
    ap = argparse.ArgumentParser()
    ap.add_argument("--folder", required=True)
    ap.add_argument("--spans", required=True)
    ap.add_argument("--outline", required=True)
    ap.add_argument("--out", required=True)
    ap.add_argument("--save-veg", required=True)
    ap.add_argument("--audio-gain-db", type=float, default=12.0,
                    help="track gain so a quiet Rode recording is visible/audible; "
                         "12 dB is the value Jordan set by hand in his own screenshot")
    ap.add_argument("--fps", type=float, default=30.0)
    a = ap.parse_args()

    spans = json.load(open(a.spans, encoding="utf-8"))
    work = os.path.dirname(os.path.abspath(a.spans))

    recs = {os.path.abspath(f["source"]): f for f in spans["files"]}
    order = sorted(recs, key=lambda p: (probe_creation(p), os.path.basename(p)))
    spans_by_src = {p: recs[p]["spans"] for p in order}

    print("clip order (camera creation timestamp):")
    for i, p in enumerate(order, 1):
        r = recs[p]
        print(f"  {i:2d}. {probe_creation(p)}  {os.path.basename(p):34s} "
              f"{r['duration_s']:7.1f}s -> {r['kept_s']:7.1f}s")

    events, tl = [], 0.0
    for p in order:
        for s, e in spans_by_src[p]:
            events.append({"source": p.replace("/", "\\"), "in": s, "out": e, "tl": round(tl, 6)})
            tl += e - s

    tl_words, total_tl, per_clip = load_timeline_words(order, spans_by_src, work)
    quotes = extract_quotes(a.outline)
    placed, unplaced = locate_quotes(quotes, tl_words)

    markers = [{"t": round(t, 3), "title": q} for q, t, _ in placed]
    # Quotes the narration never touches still get a marker, verbatim, parked in
    # a labelled block past the end of the cut - never guessed onto a position.
    regions = [{"t": round(s, 3), "len": round(e - s, 3), "label": name}
               for name, s, e in per_clip]
    if unplaced:
        base = total_tl + 5.0
        for i, (q, _) in enumerate(unplaced):
            markers.append({"t": round(base + i * 2.0, 3), "title": q})
        regions.append({"t": round(base - 2.0, 3), "len": round(len(unplaced) * 2.0 + 4.0, 3),
                        "label": "UNPLACED QUOTES - not found in the narration"})

    cut = {
        "version": "2",
        "project": os.path.basename(a.folder.rstrip("\\/")) + " rough cut",
        "fps": a.fps, "width": 1920, "height": 1080,
        "save_path": a.save_veg,
        "audio_gain_db": a.audio_gain_db,
        "events": events, "quotes": [], "markers": markers, "regions": regions,
    }
    with open(a.out, "w", encoding="utf-8") as f:
        json.dump(cut, f, indent=1)

    # Confidence sidecar: every quote, where it landed, how strong the match was.
    # Placement is a best-effort lexical guess; this file is how you check it.
    report = [{"quote": q, "timeline_s": round(t, 2),
               "timecode": f"{int(t//3600):02d}:{int(t//60)%60:02d}:{int(t%60):02d}",
               "match_strength": m, "placed": True} for q, t, m in sorted(placed, key=lambda r: r[1])]
    report += [{"quote": q, "timeline_s": None, "timecode": None,
                "match_strength": m, "placed": False} for q, m in unplaced]
    with open(os.path.join(os.path.dirname(a.out), "quote_markers.json"), "w",
              encoding="utf-8") as f:
        json.dump(report, f, indent=1)

    src = sum(recs[p]["duration_s"] for p in order)
    print(f"\nevents      : {len(events)}  ({src/60:.1f} min source -> {tl/60:.1f} min timeline)")
    print(f"quotes      : {len(quotes)} found in outline")
    print(f"  placed    : {len(placed)}")
    print(f"  unplaced  : {len(unplaced)} (parked after the end, titles verbatim)")
    print(f"regions     : {len(regions)}  (one per source clip + the unplaced block)")
    print(f"wrote {a.out}")
    if unplaced:
        print("\nnot located in the narration:")
        for q, sc in unplaced:
            print(f"   [{sc:.2f}] {q[:100]}")


if __name__ == "__main__":
    main()
