#!/usr/bin/env python3
"""becky-identify / becky-events helper: face detection + ArcFace embedding.

Uses InsightFace's `buffalo_l` pack (SCRFD detector + w600k_r50 ArcFace, 512-dim).
InsightFace handles detection + 5-point alignment + recognition, which matters:
ArcFace embeddings are only meaningful on ALIGNED crops, so we do not hand-roll
the crop. Reads one or more image paths (video frames OR enrollment face JPGs) and
emits, per image, the L2-normalized 512-d embedding of the most prominent face
(largest bbox area x detection score) plus the count of faces found.

With --all-faces it ALSO emits every face under "all" (same fields per face,
ordered most-prominent-first and deterministically tie-broken). This is additive:
without the flag the output is byte-identical to before, so becky-identify,
becky-enroll and becky-cluster are unaffected. A tracker needs every face --
keeping only the most prominent one is why becky could never follow the second
person in a two-shot.

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
    ap.add_argument("--device", default="cpu")  # cpu | cuda | dml
    ap.add_argument("--det-size", type=int, default=640)
    ap.add_argument("--min-det-score", type=float, default=0.5)
    ap.add_argument("--second-model", default="",
                    help="LVFace ONNX: also emit 'vector2', a second opinion from a different face model "
                         "on the SAME aligned crop (identify names only when both models agree)")
    ap.add_argument("--all-faces", action="store_true",
                    help="also emit EVERY face per image under 'all' (a tracker needs them; "
                         "identify/enroll/cluster do not and are unaffected without this flag)")
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
    elif args.device == "dml":
        # DirectML: works on any DirectX 12 GPU, no CUDA/cuDNN version matching
        # needed (unlike CUDAExecutionProvider) - the same provider already
        # proven on this machine for Parakeet ASR (transcribe_parakeet_dml.py).
        providers = ["DmlExecutionProvider", "CPUExecutionProvider"]
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

        second = None
        if args.second_model and os.path.exists(args.second_model):
            import onnxruntime as ort
            second = ort.InferenceSession(args.second_model, providers=providers)
        from insightface.utils import face_align

        def vec2(img, f):
            # LVFace takes the same 5-point-aligned 112x112 crop ArcFace uses, RGB, scaled to [-1, 1].
            crop = face_align.norm_crop(img, f.kps, 112)[:, :, ::-1].transpose(2, 0, 1)
            x = ((crop / 255.0 - 0.5) / 0.5)[None].astype(np.float32)
            v = second.run(None, {second.get_inputs()[0].name: x})[0][0]
            return [float(t) for t in v / (np.linalg.norm(v) or 1.0)]

        def detect(img):
            """Faces in img; a tight crop (face filling the picture) gets a black border of half its
            size on every side and is tried again, because the detector needs some margin around a
            face. Returned boxes go through box(), which shifts them back to the original picture."""
            faces = [f for f in app.get(img) if float(f.det_score) >= args.min_det_score]
            if faces:
                return img, faces, 0, 0
            h, w = img.shape[:2]
            ph, pw = h // 2, w // 2
            padded = np.zeros((h + 2 * ph, w + 2 * pw, 3), dtype=img.dtype)
            padded[ph:ph + h, pw:pw + w] = img
            faces = [f for f in app.get(padded) if float(f.det_score) >= args.min_det_score]
            return padded, faces, pw, ph

        def box(f, dx, dy):
            x1, y1, x2, y2 = (float(v) for v in f.bbox)
            return [x1 - dx, y1 - dy, x2 - dx, y2 - dy]

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
            img, faces, dx, dy = detect(img)
            rec["n_faces"] = len(faces)
            if faces and dx:
                rec["padded"] = True
            if faces:
                best = max(faces, key=prominence)
                emb = [float(x) for x in best.normed_embedding]
                dim = len(emb)
                rec.update(found=True, vector=emb,
                           bbox=box(best, dx, dy),
                           det_score=float(best.det_score))
                if second is not None:
                    rec["vector2"] = vec2(img, best)
                if args.all_faces:
                    # Deterministic order: most prominent first, ties broken on
                    # geometry so the same frame always yields the same sequence
                    # (becky's same-input-same-output rule). A tracker associates
                    # on its own; it just needs every face, not a chosen one.
                    ordered = sorted(
                        faces,
                        key=lambda f: (-prominence(f), float(f.bbox[0]), float(f.bbox[1])),
                    )
                    rec["all"] = []
                    for f in ordered:
                        one = {"vector": [float(x) for x in f.normed_embedding],
                               "bbox": box(f, dx, dy),
                               "det_score": float(f.det_score)}
                        if second is not None:
                            one["vector2"] = vec2(img, f)
                        rec["all"].append(one)
            elif args.all_faces:
                rec["all"] = []
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
