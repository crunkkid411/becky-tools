"""Replay logged hosted System One requests against a local /v1/systemone server.
usage: replay.py <log.jsonl> <url> <out.json> [tool-filter]
Prints agreement with the hosted answer per tool: noul (same side of 0.5),
choice (same pick), score (|diff| < 0.5), plus median latency."""
import json, sys, time, statistics, urllib.request, collections
log, url, out = sys.argv[1:4]
filt = sys.argv[4] if len(sys.argv) > 4 else None
rows = [json.loads(l) for l in open(log, encoding="utf-8")]
rows = [r for r in rows if not r["model"].startswith("local:") and (not filt or r["tool"] == filt)]
res, agree, n, lat = [], collections.Counter(), collections.Counter(), collections.defaultdict(list)
for r in rows:
    body = json.dumps(r["request"]).encode()
    t = time.time()
    try:
        a = json.load(urllib.request.urlopen(urllib.request.Request(url + "/v1/systemone", body, {"Content-Type": "application/json"}), timeout=300))["answers"]
    except Exception as e:
        print("ERR", r["tool"], e); continue
    dt = time.time() - t
    lat[r["tool"]].append(dt / max(1, len(a)))
    for k, h in r["answers"].items():
        l = a.get(k)
        if not l: continue
        key = (r["tool"], r["model"].split("/")[0])
        n[key] += 1
        if h["type"] == "noul": ok = (h["noul"] >= .5) == (l["noul"] >= .5)
        elif h["type"] == "choice": ok = h["choice"] == l["choice"]
        else: ok = abs(h.get("score", 0) - l.get("score", 0)) < .5
        agree[key] += ok
    res.append({"tool": r["tool"], "hosted_model": r["model"], "hosted": r["answers"], "local": a, "secs": dt})
json.dump(res, open(out, "w", encoding="utf-8"))
for k in sorted(n):
    print(f"{k[0]:18s} vs {k[1]:11s} {agree[k]}/{n[k]} = {agree[k]/n[k]:.1%}")
for t, v in lat.items():
    print(f"{t:18s} median {statistics.median(v)*1000:.0f} ms per question")
