"""edit_habits.py - Jordan's editing habits, measured from his own VEGAS projects.

Jordan, 2026-10-07: "I have years worth of edited video; we should be able to
systematically extract all my edit decisions and try to reverse engineer them."

Input: the read-only exports of scripts/veg_export_all.py (<dir>\\index.jsonl +
<copy>.veg.edits.json). Nothing here opens VEGAS or touches an original.

Per video event it records: length (the cut rhythm), effects, fades, and its
Pan/Crop keyframes classified as a ZOOM (frame size changes: ratio and how long
the move takes) or a PAN (same size, centre moves). Writes table vegas_events
into the edit-learning sqlite DB and prints a one-screen summary; with --md, also
a markdown report. Deterministic: same exports, same numbers.

  python edit_habits.py [--dir X:\\AI-2\\edit-learning-work\\vegs] [--db ...edits.db] [--md out.md]
"""
import argparse
import collections
import json
import os
import sqlite3
import statistics

DEFAULT_DIR = r"X:\AI-2\edit-learning-work\vegs"
DEFAULT_DB = r"X:\AI-2\edit-learning-work\edits.db"
ZOOM_MIN = 1.05  # frame width changing by 5%+ counts as a zoom, not a wobble
PAN_MIN = 0.03   # centre moving 3%+ of the frame width counts as a pan

SCHEMA = """CREATE TABLE IF NOT EXISTS vegas_events(project TEXT, track INT, kind TEXT, start REAL, len REAL,
  effects TEXT, fade_in REAL, fade_out REAL, pc_keys INT, zoom REAL, zoom_secs REAL, pan INT,
  PRIMARY KEY(project, track, start))"""


def motion(keys):
    """(zoom ratio, seconds to reach it, panned) from Pan/Crop keyframes; zoom
    ratio > 1 = pushed in (the frame shows less), < 1 = pulled out."""
    if len(keys) < 2:
        return 1.0, 0.0, False
    width = [k["br"][0] - k["tl"][0] for k in keys]
    if width[0] <= 0 or min(width) <= 0:
        return 1.0, 0.0, False
    tight, wide = min(range(len(width)), key=width.__getitem__), max(range(len(width)), key=width.__getitem__)
    pick = tight if width[0] / width[tight] >= width[wide] / width[0] else wide
    ratio = width[0] / width[pick]
    cx = [k["center"][0] for k in keys]
    panned = (max(cx) - min(cx)) / width[0] >= PAN_MIN
    return round(ratio, 3), round(keys[pick]["t"], 3), panned


def rows_for(project, edits):
    for t in edits["tracks"]:
        for e in t["events"]:
            if t["type"] == "video" and (e.get("generator") or not e.get("source")):
                kind = "title"
            else:
                kind = t["type"]
            fx = ";".join(x["name"] + ("(missing)" if x.get("missing") else "") for x in e.get("effects", []))
            z, zs, pan = motion(e.get("pan_crop", []))
            yield (project, t["index"], kind, round(e["start"], 4), round(e["len"], 4), fx,
                   e.get("fade_in", 0), e.get("fade_out", 0), len(e.get("pan_crop", [])), z, zs, int(pan))


def pct(xs, q):
    xs = sorted(xs)
    return xs[min(len(xs) - 1, int(q * len(xs)))] if xs else 0


def summary(con):
    q = lambda sql: con.execute(sql).fetchall()
    projects = q("SELECT COUNT(DISTINCT project) FROM vegas_events")[0][0]
    lens = [r[0] for r in q("SELECT len FROM vegas_events WHERE kind='video'")]
    zooms = con.execute("SELECT zoom, zoom_secs FROM vegas_events WHERE kind='video' AND (zoom>=? OR zoom<=?)",
                        (ZOOM_MIN, 1 / ZOOM_MIN)).fetchall()
    ins = [z for z, _ in zooms if z > 1]
    pans = q("SELECT COUNT(*) FROM vegas_events WHERE kind='video' AND pan=1 AND zoom<1.05 AND zoom>0.95")[0][0]
    fx = collections.Counter()
    fx_projects = collections.defaultdict(set)
    for proj, s in q("SELECT project, effects FROM vegas_events WHERE effects<>''"):
        for name in s.split(";"):
            fx[name] += 1
            fx_projects[name].add(proj)
    fades = q("SELECT SUM(fade_in>0), SUM(fade_out>0), COUNT(*) FROM vegas_events WHERE kind='video'")[0]
    out = [f"{projects} projects, {len(lens)} video pieces",
           f"piece length (his cut rhythm): median {statistics.median(lens):.2f}s, "
           f"10% under {pct(lens, .1):.2f}s, 90% under {pct(lens, .9):.2f}s" if lens else "no video pieces",
           f"zooms: {len(ins)} push-ins, {len(zooms) - len(ins)} pull-outs; push-in size median "
           f"{statistics.median(ins):.2f}x (10-90%: {pct(ins, .1):.2f}-{pct(ins, .9):.2f}x), reached after median "
           f"{statistics.median([s for z, s in zooms if z > 1]):.2f}s" if ins else "zooms: none",
           f"pans (same size, centre moves): {pans}",
           f"fades: {fades[0]} fade-ins, {fades[1]} fade-outs on {fades[2]} video pieces",
           "effects (uses / projects): " + ", ".join(f"{n} {c}/{len(fx_projects[n])}" for n, c in fx.most_common(20))]
    return out


def main():
    ap = argparse.ArgumentParser(description=__doc__.split("\n")[0])
    ap.add_argument("--dir", default=DEFAULT_DIR)
    ap.add_argument("--db", default=DEFAULT_DB)
    ap.add_argument("--md")
    a = ap.parse_args()
    index = {}
    with open(os.path.join(a.dir, "index.jsonl"), encoding="utf-8") as f:
        for line in filter(str.strip, f):
            r = json.loads(line)
            if r["status"] == "ok":
                index[r["copy"]] = r["veg"]  # latest ok row wins
    con = sqlite3.connect(a.db)
    con.execute(SCHEMA)
    con.execute("DELETE FROM vegas_events")
    for copy, original in sorted(index.items()):
        with open(copy + ".edits.json", encoding="utf-8") as f:
            edits = json.load(f)
        con.executemany("INSERT OR REPLACE INTO vegas_events VALUES(?,?,?,?,?,?,?,?,?,?,?,?)", list(rows_for(original, edits)))
    con.commit()
    lines = summary(con)
    con.close()
    print("\n".join(lines))
    if a.md:
        with open(a.md, "w", encoding="utf-8") as f:
            f.write("# Jordan's editing habits (measured from his VEGAS projects)\n\n" + "\n".join("- " + s for s in lines) + "\n")


if __name__ == "__main__":
    main()
