"""Picture test: 14 frames where Jordan was about to read chat aloud vs 28 ordinary talking frames.
usage: gaze_test.py <url> <label>. Prints how well P(yes) separates the two (AUC) and accuracy at 0.5."""
import json, sys, glob, os, base64, time, urllib.request
S = os.path.dirname(os.path.abspath(__file__))
url, label = sys.argv[1], sys.argv[2]
Q = {"down": {"type": "noul", "instructions": "Is the man looking down below the camera, for example reading a phone or screen, rather than looking into the camera?"},
     "camera": {"type": "noul", "instructions": "Is the man looking straight into the camera?"}}
rows = []
for f in sorted(glob.glob(S + "/gaze/*.jpg")):
    img = "data:image/jpeg;base64," + base64.b64encode(open(f, "rb").read()).decode()
    t = time.time()
    a = json.load(urllib.request.urlopen(urllib.request.Request(url + "/v1/systemone", json.dumps({"state": "A frame from a livestream.", "images": [img], "questions": Q}).encode(), {"Content-Type": "application/json"}), timeout=300))["answers"]
    rows.append((os.path.basename(f).startswith("read"), a["down"]["noul"], a["camera"]["noul"], time.time() - t))
def auc(pos, neg): return sum((p > n) + .5 * (p == n) for p in pos for n in neg) / (len(pos) * len(neg))
for i, name in ((1, "looking down"), (2, "NOT looking at camera")):
    pos = [r[i] if i == 1 else 1 - r[i] for r in rows if r[0]]; neg = [r[i] if i == 1 else 1 - r[i] for r in rows if not r[0]]
    acc = (sum(p >= .5 for p in pos) + sum(n < .5 for n in neg)) / len(rows)
    print(f"{label} '{name}': AUC {auc(pos, neg):.2f}  accuracy@0.5 {acc:.0%}  (reads >=0.5: {sum(p>=.5 for p in pos)}/14, ordinary >=0.5: {sum(n>=.5 for n in neg)}/28)")
print(f"{label} median {sorted(r[3] for r in rows)[len(rows)//2]*1000:.0f} ms per frame (2 questions)")
