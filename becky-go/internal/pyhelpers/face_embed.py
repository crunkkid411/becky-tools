#!/usr/bin/env python3
"""becky-identify / becky-events helper: face detection + ArcFace embedding.

Uses InsightFace's `buffalo_l` pack (SCRFD detector + w600k_r50 ArcFace, 512-dim).
InsightFace handles detection + 5-point alignment + recognition, which matters:
ArcFace embeddings are only meaningful on ALIGNED crops, so we do not hand-roll
the crop. Reads one or more image paths (video frames OR enrollment face JPGs) and
emits, per image, the L2-normalized 512-d embedding of the most prominent face
(largest bbox area x detection score) plus the count of faces found.

Output (stdout JSON, one line):
  {"dim": 512, "faces": [
     {"path": "...", "found": true, "n_faces": 1,
      "vector": [<512 floats>], "bbox": [x1,y1,x2,y2], "det_score": 0.87}]}

Embeddings are normed_embedding, so cosine similarity == dot product. Matching math
lives in the Go caller (kept deterministic + in one place), as with voice_embed.py.

On any failure prints {"skipped": true, "reason": "..."} and exits 0 so the Go
caller surfaces a clean note instead of a stack trace (graceful degrade).

Requires: insightface + onnxruntime + opencv (or Pillow). The Go caller sets
PYTHONPATH so these resolve; --model-root points at the dir holding models/<name>/.
"""
import argparse
import json
import os
import sys


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("images", nargs="+", help="image paths (frames or enrollment faces)")
    ap.add_argument("--model-root", required=True, help="insightface root (holds models/<name>/)")
    ap.add_argument("--model-name", default="buffalo_l")
    ap.add_argument("--device", default="cpu")  # cpu | cuda
    ap.add_argument("--det-size", type=int, default=640)
    ap.add_argument("--min-det-score", type=float, default=0.5)
    args = ap.parse_args()

    import numpy as np
    from insightface.app import FaceAnalysis

    try:
        import cv2

        def read(p):
            # cv2.imread fails SILENTLY (returns None) on non-ASCII paths on Windows
            # — e.g. a name with "é" — so read the bytes via numpy and decode. This
            # handles unicode paths, which is required for accented enrolled names.
            data = np.fromfile(p, dtype=np.uint8)
            if data.size == 0:
                return None
            return cv2.imdecode(data, cv2.IMREAD_COLOR)
    except Exception:
        from PIL import Image

        def read(p):
            return np.array(Image.open(p).convert("RGB"))[:, :, ::-1].copy()

    if args.device == "cuda":
        providers = ["CUDAExecutionProvider", "CPUExecutionProvider"]
        ctx_id = 0
    else:
        providers = ["CPUExecutionProvider"]
        ctx_id = -1

    # InsightFace prints model-discovery banners to stdout; redirect to stderr so
    # our stdout stays a single clean JSON line for the Go caller to parse.
    real_stdout = sys.stdout
    sys.stdout = sys.stderr
    try:
        app = FaceAnalysis(name=args.model_name, root=args.model_root, providers=providers)
        app.prepare(ctx_id=ctx_id, det_size=(args.det_size, args.det_size))

        def prominence(f):
            x1, y1, x2, y2 = f.bbox
            return (x2 - x1) * (y2 - y1) * float(f.det_score)

        out = []
        dim = 0
        for path in args.images:
            rec = {"path": path, "found": False, "n_faces": 0,
                   "vector": None, "bbox": None, "det_score": None}
            if not os.path.exists(path):
                out.append(rec)
                continue
            img = read(path)
            if img is None:
                out.append(rec)
                continue
            faces = [f for f in app.get(img) if float(f.det_score) >= args.min_det_score]
            rec["n_faces"] = len(faces)
            if faces:
                best = max(faces, key=prominence)
                emb = [float(x) for x in best.normed_embedding]
                dim = len(emb)
                rec.update(found=True, vector=emb,
                           bbox=[float(v) for v in best.bbox],
                           det_score=float(best.det_score))
            out.append(rec)
    finally:
        sys.stdout = real_stdout

    print(json.dumps({"dim": dim, "faces": out}))


if __name__ == "__main__":
    try:
        main()
    except Exception as e:  # noqa: BLE001 - report cleanly to the Go caller
        print(json.dumps({"skipped": True, "reason": f"{type(e).__name__}: {e}"}))
        sys.exit(0)
