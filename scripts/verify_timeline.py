#!/usr/bin/env python3
"""verify_timeline.py - acceptance test for the built rough cut.

Reconstructs the TIMELINE audio from vegas_cut.json (decoding each source and
concatenating exactly the in/out the events point at), then reports what a human
scrubbing that timeline would actually hit: how much of it is silence, and the
longest single stretch of dead air.

This measures the assembled result, not the intention. Optionally writes a
waveform PNG of a window and a WAV of it you can listen to.

    python verify_timeline.py vegas_cut.json [--png x.png --start 0 --dur 180 --wav x.wav]
"""
from __future__ import annotations

import argparse
import json
import os
import wave

import numpy as np
from PIL import Image, ImageDraw

from speechcut import HOP, SR, decode_mono, frame_db, pick_threshold


def build_timeline_audio(cut, limit_s=None):
    """Concatenate the exact source slices the events reference."""
    cache = {}
    chunks, marks, tl = [], [], 0.0
    for ev in cut["events"]:
        src = ev["source"]
        if limit_s is not None and tl >= limit_s:
            break
        if src not in cache:
            cache[src] = decode_mono(src)
        x = cache[src]
        a, b = int(ev["in"] * SR), int(ev["out"] * SR)
        seg = x[a:b]
        chunks.append(seg)
        marks.append((tl, tl + len(seg) / SR, os.path.basename(src)))
        tl += len(seg) / SR
    return (np.concatenate(chunks) if chunks else np.zeros(0, np.float32)), marks


def main() -> None:
    ap = argparse.ArgumentParser()
    ap.add_argument("cut_json")
    ap.add_argument("--png")
    ap.add_argument("--wav")
    ap.add_argument("--start", type=float, default=0.0)
    ap.add_argument("--dur", type=float, default=180.0)
    ap.add_argument("--limit", type=float, default=None,
                    help="only assemble this many seconds (default: whole timeline)")
    a = ap.parse_args()

    cut = json.load(open(a.cut_json, encoding="utf-8"))
    print(f"events: {len(cut['events'])}  markers: {len(cut.get('markers', []))}")

    # events must tile the timeline with no gap and no overlap
    gaps = 0
    for prev, nxt in zip(cut["events"], cut["events"][1:]):
        if abs((prev["tl"] + (prev["out"] - prev["in"])) - nxt["tl"]) > 1e-6:
            gaps += 1
    print(f"gaps/overlaps between events: {gaps}")

    x, marks = build_timeline_audio(cut, a.limit)
    dur = len(x) / SR
    db = frame_db(x)
    thr, diag = pick_threshold(db)
    hop = HOP / SR

    quiet = db < thr
    runs, cur = [], 0
    for q in quiet:
        if q:
            cur += 1
        elif cur:
            runs.append(cur * hop)
            cur = 0
    if cur:
        runs.append(cur * hop)
    runs = np.array(runs) if runs else np.zeros(1)

    print(f"\nTIMELINE AUDIO: {dur/60:.1f} min   threshold {diag['threshold_db']}dB "
          f"(floor {diag['noise_floor_db']}, speech {diag['speech_level_db']})")
    print(f"  total quiet             : {quiet.sum()*hop/60:.1f} min "
          f"({100*quiet.mean():.1f}% - normal speech is 30-40% quiet at phoneme level)")
    for bar in (1.0, 2.0, 3.0, 5.0):
        sel = runs[runs >= bar]
        print(f"  dead air >= {bar:>3.0f}s        : {len(sel):5d} stretches, "
              f"{sel.sum()/60:6.2f} min total")
    print(f"  longest single stretch  : {runs.max():.2f}s")

    if a.wav:
        seg = x[int(a.start * SR):int((a.start + a.dur) * SR)]
        with wave.open(a.wav, "wb") as w:
            w.setnchannels(1); w.setsampwidth(2); w.setframerate(SR)
            g = min(8.0, 0.7 / (float(np.abs(seg).max()) or 1.0))
            w.writeframes((np.clip(seg * g, -1, 1) * 32767).astype("<i2").tobytes())
        print(f"  wrote {a.wav} ({a.dur:.0f}s of the timeline, gain x{g:.1f})")

    if a.png:
        t0, t1 = a.start, min(a.start + a.dur, dur)
        W, H = 1900, 320
        img = Image.new("RGB", (W, H), (16, 16, 22))
        dr = ImageDraw.Draw(img, "RGBA")
        seg = x[int(t0 * SR):int(t1 * SR)]
        peak = float(np.abs(seg).max()) or 1.0
        edges = np.linspace(0, len(seg), W + 1).astype(int)
        mid, half = 150, 120
        # shade every stretch of dead air >= 1s so it is impossible to miss
        for s, e in _runs_spans(quiet, hop):
            if e - s >= 1.0 and e > t0 and s < t1:
                px0 = (max(s, t0) - t0) / (t1 - t0) * W
                px1 = (min(e, t1) - t0) / (t1 - t0) * W
                dr.rectangle([px0, 30, px1, H - 30], fill=(255, 85, 85, 90))
        for i in range(W):
            c = seg[edges[i]:edges[i + 1]]
            if c.size:
                dr.line([(i, mid - c.max() / peak * half), (i, mid - c.min() / peak * half)],
                        fill=(127, 209, 255))
        for s, e, name in marks:
            if t0 <= s <= t1:
                px0 = (s - t0) / (t1 - t0) * W
                dr.line([(px0, 30), (px0, H - 30)], fill=(90, 90, 110))
        dr.text((8, 8), f"TIMELINE AUDIO {t0:.0f}-{t1:.0f}s (normalised)   "
                        f"RED = dead air >= 1s   grey = event boundary   "
                        f"longest dead air in whole timeline: {runs.max():.2f}s",
                fill=(235, 235, 240))
        img.save(a.png)
        print(f"  wrote {a.png}")


def _runs_spans(mask, hop):
    d = np.diff(mask.astype(np.int8))
    st = list(np.flatnonzero(d == 1) + 1)
    en = list(np.flatnonzero(d == -1) + 1)
    if mask[0]:
        st.insert(0, 0)
    if mask[-1]:
        en.append(mask.size)
    return [(s * hop, e * hop) for s, e in zip(st, en)]


if __name__ == "__main__":
    main()
