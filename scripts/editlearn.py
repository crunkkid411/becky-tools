"""editlearn.py - turn one of Jordan's finished VEGAS edits into labelled data.

Jordan, 2026-10-07: "we should be able to systematically extract all my edit
decisions and try to reverse engineer them somehow. Please, above all else,
make sure the original files are not altered in any way."

Input (all produced read-only, from copies):
  --edits   <project>.veg.edits.json   (vegas/BeckyDumpProject.cs)
  --transcript <media-file-name>=<transcript.json>   (becky-transcribe; repeat)

For every raw source the edit used, each word is KEPT if its middle falls
inside a kept event (audio track, else video), and each transcript sentence
gets kept_frac. Every stretch of the source he removed between two kept pieces
is logged with its length and the words in it (none = a pause he trimmed).

Output: rows appended to an sqlite database (default
X:\\AI-2\\edit-learning-work\\edits.db, outside the repo) and a one-screen
summary on stdout. Deterministic: same inputs, same rows.
"""
import argparse
import json
import ntpath
import os
import sqlite3
import sys

DEFAULT_DB = r"X:\AI-2\edit-learning-work\edits.db"
KEEP, CUT = 0.8, 0.2  # sentence kept_frac at/above KEEP = kept, at/below CUT = cut, else partial

SCHEMA = """
CREATE TABLE IF NOT EXISTS projects(project TEXT PRIMARY KEY, width INT, height INT, fps REAL, events INT, added TEXT);
CREATE TABLE IF NOT EXISTS sentences(project TEXT, source TEXT, idx INT, start REAL, end REAL, text TEXT,
  words INT, kept_frac REAL, label TEXT, PRIMARY KEY(project, source, idx));
CREATE TABLE IF NOT EXISTS removed(project TEXT, source TEXT, start REAL, end REAL, secs REAL, words INT, text TEXT,
  kind TEXT, PRIMARY KEY(project, source, start));
CREATE TABLE IF NOT EXISTS pieces(project TEXT, track INT, timeline_start REAL, len REAL, source TEXT,
  src_start REAL, src_end REAL, reordered INT, PRIMARY KEY(project, track, timeline_start));
"""


def kept_spans(edits):
    """Kept (source, start, end) from the first audio track with media, else video."""
    tracks = sorted(edits["tracks"], key=lambda t: t["type"] != "audio")
    for t in tracks:
        evs = [e for e in t["events"] if e.get("source") and not e.get("mute") and not e.get("generator")]
        if evs:
            return t["index"], [(ntpath.basename(e["source"]), e["start"], e["len"], e["offset"],
                                 e["offset"] + e["len"] * e.get("rate", 1)) for e in evs]
    return None, []


def label(frac):
    return "kept" if frac >= KEEP else "cut" if frac <= CUT else "partial"


def analyse(edits, transcripts):
    track, pieces = kept_spans(edits)
    by_src = {}
    for src, *_rest, s0, s1 in pieces:
        by_src.setdefault(src, []).append((s0, s1))
    out = {"pieces": [], "sentences": [], "removed": []}
    last_src_end = {}
    for src, t0, ln, s0, s1 in sorted(pieces, key=lambda p: p[1]):
        reordered = int(src in last_src_end and s0 < last_src_end[src] - 0.05)
        last_src_end[src] = max(last_src_end.get(src, 0), s1)
        out["pieces"].append((track, t0, ln, src, s0, s1, reordered))
    for src, spans in by_src.items():
        tr = transcripts.get(src)
        if tr is None:
            print(f"  no transcript for {src}: skipped", file=sys.stderr)
            continue
        spans.sort()
        inside = lambda t: any(a <= t <= b for a, b in spans)
        words = [(w["start"], w["end"], w.get("word") or w.get("text", "")) for w in tr.get("words", [])]
        for i, seg in enumerate(tr.get("segments", [])):
            ws = [w for w in words if seg["start"] - 0.01 <= (w[0] + w[1]) / 2 <= seg["end"] + 0.01]
            frac = sum(inside((a + b) / 2) for a, b, _ in ws) / len(ws) if ws else 0.0
            out["sentences"].append((src, i, seg["start"], seg["end"], seg["text"].strip(), len(ws), round(frac, 3), label(frac)))
        merged = []
        for a, b in spans:  # union of kept spans, so overlaps never count as removed
            if merged and a <= merged[-1][1]:
                merged[-1][1] = max(merged[-1][1], b)
            else:
                merged.append([a, b])
        for (a0, a1), (b0, _b1) in zip(merged, merged[1:]):
            gone = [w for w in words if a1 <= (w[0] + w[1]) / 2 <= b0]
            text = " ".join(w[2].strip() for w in gone)
            out["removed"].append((src, round(a1, 3), round(b0, 3), round(b0 - a1, 3), len(gone), text,
                                   "pause" if not gone else "words"))
    return out


def save(db, project, edits, out):
    os.makedirs(os.path.dirname(db), exist_ok=True)
    con = sqlite3.connect(db)
    con.executescript(SCHEMA)
    for stmt in ("DELETE FROM projects WHERE project=?", "DELETE FROM sentences WHERE project=?",
                 "DELETE FROM removed WHERE project=?", "DELETE FROM pieces WHERE project=?"):
        con.execute(stmt, (project,))
    con.execute("INSERT INTO projects VALUES(?,?,?,?,?,datetime('now'))",
                (project, edits.get("width"), edits.get("height"), edits.get("fps"), len(out["pieces"])))
    con.executemany("INSERT INTO pieces VALUES(?,?,?,?,?,?,?,?)", [(project, *p) for p in out["pieces"]])
    con.executemany("INSERT INTO sentences VALUES(?,?,?,?,?,?,?,?,?)", [(project, *s) for s in out["sentences"]])
    con.executemany("INSERT INTO removed VALUES(?,?,?,?,?,?,?,?)", [(project, *r) for r in out["removed"]])
    con.commit()
    con.close()


def summary(project, out):
    s = out["sentences"]
    n = {k: sum(1 for x in s if x[7] == k) for k in ("kept", "partial", "cut")}
    pauses = sorted(r[3] for r in out["removed"] if r[6] == "pause")
    worded = [r for r in out["removed"] if r[6] == "words"]
    print(f"{project}: {len(out['pieces'])} kept pieces, {sum(p[6] for p in out['pieces'])} moved out of order")
    print(f"  sentences: {n['kept']} kept, {n['partial']} partly kept, {n['cut']} cut (of {len(s)})")
    if pauses:
        print(f"  pauses trimmed: {len(pauses)}, median {pauses[len(pauses) // 2]:.2f}s, shortest {pauses[0]:.2f}s")
    print(f"  stretches with words removed: {len(worded)}, {sum(r[4] for r in worded)} words")


def main():
    ap = argparse.ArgumentParser(description=__doc__.split("\n")[0])
    ap.add_argument("--edits", required=True)
    ap.add_argument("--transcript", action="append", default=[], help="<media file name>=<transcript.json>")
    ap.add_argument("--project", help="name stored in the database (default: edits file name)")
    ap.add_argument("--db", default=DEFAULT_DB)
    a = ap.parse_args()
    with open(a.edits, encoding="utf-8") as f:
        edits = json.load(f)
    if "error" in edits:
        sys.exit(f"the export failed: {edits['error'][:300]}")
    transcripts = {}
    for pair in a.transcript:
        name, _, path = pair.partition("=")
        with open(path, encoding="utf-8") as f:
            transcripts[name] = json.load(f)
    project = a.project or os.path.basename(a.edits).replace(".veg.edits.json", "")
    out = analyse(edits, transcripts)
    save(a.db, project, edits, out)
    summary(project, out)


if __name__ == "__main__":
    main()
