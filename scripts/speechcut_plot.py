#!/usr/bin/env python3
"""Draw the waveform with the kept spans shaded, so a human (or an agent with
vision) can check the cut the way an editor checks it: by looking at it.

Green = kept (speaking). Dark = cut (dead air). The waveform is normalised,
which is the same thing as putting +12 dB on the Vegas track to make a quiet
Rode recording visible.

    python speechcut_plot.py spans.json <basename> --out x.png [--start 0 --dur 120]

PIL only - matplotlib is broken in this machine's anaconda env.
"""
import argparse
import json
import os

import numpy as np
from PIL import Image, ImageDraw

from speechcut import HOP, SR, decode_mono, frame_db

BG = (16, 16, 22)
PANEL = (22, 22, 28)
WAVE = (127, 209, 255)
KEEP = (46, 204, 113)
LEVEL = (255, 184, 108)
THR = (255, 85, 85)
FLOOR = (120, 120, 130)
GRID = (58, 58, 68)
TEXT = (230, 230, 235)


def main() -> None:
    ap = argparse.ArgumentParser()
    ap.add_argument("spans_json")
    ap.add_argument("basename", help="which file in the spans json to draw")
    ap.add_argument("--out", required=True)
    ap.add_argument("--start", type=float, default=0.0)
    ap.add_argument("--dur", type=float, default=120.0)
    ap.add_argument("--width", type=int, default=1900)
    a = ap.parse_args()

    d = json.load(open(a.spans_json))
    rec = next(f for f in d["files"]
               if os.path.basename(f["source"]).lower() == a.basename.lower())

    x = decode_mono(rec["source"])
    t0 = a.start
    t1 = min(a.start + a.dur, rec["duration_s"])
    W = a.width
    H_WAVE, H_DB, PAD_TOP = 300, 200, 34
    H = PAD_TOP + H_WAVE + 16 + H_DB + 26

    img = Image.new("RGB", (W, H), BG)
    dr = ImageDraw.Draw(img, "RGBA")
    y_wave = PAD_TOP
    y_db = PAD_TOP + H_WAVE + 16

    def px(t: float) -> float:
        return (t - t0) / (t1 - t0) * (W - 1)

    dr.rectangle([0, y_wave, W, y_wave + H_WAVE], fill=PANEL)
    dr.rectangle([0, y_db, W, y_db + H_DB], fill=PANEL)

    # kept spans, behind everything
    for s, e in rec["spans"]:
        if e > t0 and s < t1:
            dr.rectangle([px(max(s, t0)), y_wave, px(min(e, t1)), y_wave + H_WAVE],
                         fill=KEEP + (60,))
            dr.rectangle([px(max(s, t0)), y_db, px(min(e, t1)), y_db + H_DB],
                         fill=KEEP + (60,))

    # waveform: min/max envelope per pixel column, normalised
    seg = x[int(t0 * SR):int(t1 * SR)]
    peak = float(np.abs(seg).max()) or 1.0
    edges = np.linspace(0, len(seg), W + 1).astype(int)
    mid = y_wave + H_WAVE / 2
    half = H_WAVE / 2 - 6
    for i in range(W):
        c = seg[edges[i]:edges[i + 1]]
        if c.size == 0:
            continue
        lo, hi = float(c.min()) / peak, float(c.max()) / peak
        dr.line([(i, mid - hi * half), (i, mid - lo * half)], fill=WAVE)
    dr.line([(0, mid), (W, mid)], fill=GRID)

    # level curve, on the scale the decision was actually made
    db = frame_db(x)
    tdb = np.arange(len(db)) * HOP / SR
    m = (tdb >= t0) & (tdb <= t1)
    dlo, dhi = rec["noise_floor_db"] - 6, max(rec["speech_level_db"] + 10, -10)

    def ydb(v: float) -> float:
        return y_db + H_DB - (np.clip(v, dlo, dhi) - dlo) / (dhi - dlo) * H_DB

    pts = [(px(t), ydb(v)) for t, v in zip(tdb[m], db[m])]
    if len(pts) > 1:
        dr.line(pts, fill=LEVEL, width=1)
    dr.line([(0, ydb(rec["threshold_db"])), (W, ydb(rec["threshold_db"]))], fill=THR, width=2)
    dr.line([(0, ydb(rec["noise_floor_db"])), (W, ydb(rec["noise_floor_db"]))], fill=FLOOR)

    # one second-tick every 5s
    step = 5 if (t1 - t0) <= 200 else 30
    for t in range(int(t0) - int(t0) % step, int(t1) + 1, step):
        if t < t0:
            continue
        dr.line([(px(t), y_db + H_DB), (px(t), y_db + H_DB + 5)], fill=GRID)
        dr.text((px(t) + 2, y_db + H_DB + 7), f"{t}s", fill=(150, 150, 160))

    dr.text((8, 6), f"{a.basename}   {t0:.0f}-{t1:.0f}s   "
                    f"GREEN = KEPT (speaking)   DARK = CUT (dead air)   "
                    f"threshold {rec['threshold_db']}dB (red)   "
                    f"room tone {rec['noise_floor_db']}dB (grey)   "
                    f"whole clip: {rec['duration_s']:.0f}s -> {rec['kept_s']:.0f}s "
                    f"(cut {rec['removed_pct']}%)", fill=TEXT)
    dr.text((8, y_db - 12), "level (dBFS) - the signal the cut was decided on",
            fill=(150, 150, 160))

    img.save(a.out)
    print("wrote", a.out)


if __name__ == "__main__":
    main()
