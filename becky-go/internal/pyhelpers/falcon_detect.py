#!/usr/bin/env python3
"""becky's SECOND opinion on where the subject is: Falcon-Perception, batched.

WHY A WRAPPER AND NOT THE MODEL'S OWN SCRIPT. The model, its ONNX weights and its
venv live in models/falcon-perception/, which is gitignored so multi-GB weights
stay out of the repo. Logic that lives there is logic git does not have. So the
detector class is IMPORTED from there and every decision becky makes about it
lives here, in the repo, where it is versioned and reviewed.

WHY BATCHED. The 0.6B stack costs ~11 seconds to load and ~6 to run. One frame
per process is 17s a frame, which is why this was never usable as a framing
signal. Loaded once for a whole span it is ~6s a frame, and a span needs a
handful.

WHY IT EXISTS AT ALL. Reka Edge (internal/ground) is a 7B language model doing
grounded detection; this is a 0.6B open-vocabulary DETECTOR on ONNX Runtime with
no torch. Different size, different architecture, different failure modes. becky's
rule is that one signal is a candidate and two agreeing is a conclusion, and the
framing path had exactly one. Jordan asked why this was not wired in (2026-08-20);
the honest answer was that nobody had done it.

Output: one JSON line per frame on stdout (NDJSON), boxes as FRACTIONS of the
frame, which is what internal/crop wants:
  {"ok": true, "frame": "f_00001.jpg", "count": 2,
   "boxes": [{"x":0.1,"y":0.2,"w":0.3,"h":0.6,"confidence":0.99}]}
A frame that fails prints {"ok": false, ...} and the batch CONTINUES - one
unreadable frame must not cost the other seven.
"""
import argparse
import importlib.util
import json
import os
import sys


def load_detector(model_dir):
    """Import FalconPerception from the (untracked) model directory."""
    script = os.path.join(model_dir, "falcon_perception_onnx.py")
    if not os.path.isfile(script):
        return None, "detector script not found: %s" % script
    # The class resolves its ONNX weights relative to its OWN file, so it must be
    # imported from where it lives rather than copied.
    spec = importlib.util.spec_from_file_location("falcon_perception_onnx", script)
    mod = importlib.util.module_from_spec(spec)
    try:
        spec.loader.exec_module(mod)
        return mod.FalconPerception(), ""
    except Exception as e:  # degrade, never crash - the Go caller wants a reason
        return None, "%s: %s" % (type(e).__name__, e)


def main():
    ap = argparse.ArgumentParser()
    # NOT required=True: --selftest must run with no arguments at all, which is
    # the whole point of an offline proof.
    ap.add_argument("--model-dir")
    ap.add_argument("--frames", help="directory of extracted frames")
    ap.add_argument("--query", default="person")
    ap.add_argument("--selftest", action="store_true")
    args = ap.parse_args()

    if args.selftest:
        return selftest()
    if not args.model_dir or not args.frames:
        print(json.dumps({"ok": False, "error": "--model-dir and --frames are required"}))
        return 0

    fp, err = load_detector(args.model_dir)
    if fp is None:
        print(json.dumps({"ok": False, "error": err}))
        return 0

    from PIL import Image  # imported late: only needed once the model loaded

    try:
        names = sorted(n for n in os.listdir(args.frames)
                       if n.lower().endswith((".jpg", ".jpeg", ".png")))
    except OSError as e:
        print(json.dumps({"ok": False, "error": "cannot read frames dir: %s" % e}))
        return 0

    for n in names:
        try:
            im = Image.open(os.path.join(args.frames, n)).convert("RGB")
            # 256x256 is what the reference pipeline uses end to end (incl. AnyUp
            # and the segmentation head); the boxes come back as fractions, so
            # the source resolution never enters into it.
            res = fp.generate(im.resize((256, 256), Image.BILINEAR), args.query, verbose=False)
            boxes = [dict(x=round(d["box"]["x1"], 4), y=round(d["box"]["y1"], 4),
                          w=round(d["box"]["x2"] - d["box"]["x1"], 4),
                          h=round(d["box"]["y2"] - d["box"]["y1"], 4),
                          confidence=round(d["score"], 4)) for d in res]
        except Exception as e:
            print(json.dumps({"ok": False, "frame": n, "error": str(e)}), flush=True)
            continue
        print(json.dumps({"ok": True, "frame": n, "query": args.query,
                          "count": len(boxes), "boxes": boxes}), flush=True)
    return 0


def selftest():
    """Offline proof: no model, no weights, no images."""
    checks = []

    def ck(name, ok, detail=""):
        checks.append((name, ok, detail))

    d, err = load_detector(os.path.join(os.sep, "no", "such", "dir"))
    ck("a missing model directory degrades with a reason, never a stack trace",
       d is None and "not found" in err, err)

    # The JSON contract the Go side parses, asserted as VALUES.
    line = json.loads(json.dumps({"ok": True, "frame": "f_00001.jpg", "count": 1,
                                  "boxes": [{"x": 0.1, "y": 0.2, "w": 0.3, "h": 0.6,
                                             "confidence": 0.99}]}))
    b = line["boxes"][0]
    ck("boxes are FRACTIONS with x/y/w/h, not pixels",
       0 <= b["x"] <= 1 and 0 <= b["w"] <= 1 and b["x"] + b["w"] <= 1, str(b))
    ck("a failed frame is reported per frame, not as a batch failure",
       json.loads(json.dumps({"ok": False, "frame": "f_2.jpg", "error": "x"}))["frame"] == "f_2.jpg")

    print("falcon_detect.py --selftest (offline; no model, no weights, no images)")
    npass = 0
    for name, ok, detail in checks:
        print("  %s  %s%s" % ("PASS" if ok else "FAIL", name, "" if ok else "   <- " + detail))
        npass += bool(ok)
    print("\n%d/%d PASS" % (npass, len(checks)))
    return 0 if npass == len(checks) else 1


if __name__ == "__main__":
    sys.exit(main())
