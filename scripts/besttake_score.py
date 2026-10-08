"""besttake_score.py - score becky-besttake picks against Jordan's real edit.

  python besttake_score.py <project>.veg.edits.json <media name>=<picks.json> [...]

A line counts as KEPT by Jordan when at least half of it lies inside a piece
he kept on the timeline (audio track). Prints agreement, and how many of his
cuts becky also cut (recall) and how many of becky's cuts he also made
(precision), next to the do-nothing baseline (keep every non-noise line).
"""
import json
import ntpath
import sys


def spans_by_source(edits):
    for t in sorted(edits["tracks"], key=lambda t: t["type"] != "audio"):
        evs = [e for e in t["events"] if e.get("source") and not e.get("generator")]
        if evs:
            out = {}
            for e in evs:
                out.setdefault(ntpath.basename(e["source"]), []).append((e["offset"], e["offset"] + e["len"] * e.get("rate", 1)))
            return out
    return {}


def kept_by_jordan(line, spans):
    dur = max(line["end"] - line["start"], 1e-6)
    inside = sum(max(0.0, min(b, line["end"]) - max(a, line["start"])) for a, b in spans)
    return inside / dur >= 0.5


def score(name, lines, spans):
    lo, hi = min(a for a, _ in spans), max(b for _, b in spans)
    lines = [l for l in lines if l["end"] >= lo - 5 and l["start"] <= hi + 5]  # ignore talk before/after the part he used
    j = [kept_by_jordan(l, spans) for l in lines]
    b = [l["keep"] for l in lines]
    base = [not l.get("noise") for l in lines]

    def stats(pred):
        agree = sum(p == x for p, x in zip(pred, j)) / len(j)
        jcuts = [i for i, x in enumerate(j) if not x]
        pcuts = [i for i, p in enumerate(pred) if not p]
        rec = sum(not pred[i] for i in jcuts) / len(jcuts) if jcuts else 1.0
        prec = sum(not j[i] for i in pcuts) / len(pcuts) if pcuts else 1.0
        return agree, rec, prec, len(pcuts)

    print(f"{name}: {len(lines)} lines, Jordan cut {j.count(False)}")
    for label, pred in (("becky-besttake", b), ("baseline (noise only)", base)):
        a, r, p, n = stats(pred)
        print(f"  {label:22s} agree {a:5.1%}  catches {r:5.1%} of his cuts  {p:5.1%} of its {n} cuts are right")
    for l, x in zip(lines, j):
        if l["keep"] != x:
            print(f"    {'KEEP' if x else 'CUT '} by Jordan, becky {'kept' if l['keep'] else 'cut'}: {l['text'][:90]}")
    return j, b


def main():
    edits = json.load(open(sys.argv[1], encoding="utf-8"))
    spans = spans_by_source(edits)
    for pair in sys.argv[2:]:
        name, _, path = pair.partition("=")
        score(name, json.load(open(path, encoding="utf-8"))["lines"], spans[name])


if __name__ == "__main__":
    main()
