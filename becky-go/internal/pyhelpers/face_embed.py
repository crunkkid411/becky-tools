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
    ap.add_argument("images", nargs="*", help="image paths (frames or enrollment faces)")
    ap.add_argument("--scrub-dir", default="",
                    help="SCRUB mode: a folder holding EVERY video frame of a short window, in order. "
                         "Starting at --scrub-from it steps ONE frame at a time outward in both directions, "
                         "follows the face at --target, and stops at the first clear shot (sharp, frontal, "
                         "big enough). Emits the clearest views it saw, best first")
    ap.add_argument("--scrub-from", type=int, default=0, help="index of the starting frame in --scrub-dir")
    ap.add_argument("--target", default="", help="x1,y1,x2,y2 of the face to follow in the starting frame")
    ap.add_argument("--keep", type=int, default=3, help="scrub mode: how many of the clearest views to emit")
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

        if args.scrub_dir:
            sys.stdout = real_stdout
            print(json.dumps(scrub(args, detect, box, read, vec2 if second is not None else None, face_align, np)))
            return

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


# A view is CLEAR when all of these hold (measured on Jordan's stream frames, 2026-10-10).
CLEAR_DET = 0.70    # detector is sure it is a face (green hair + makeup reads 0.72-0.79 straight on)
CLEAR_WIDTH = 80    # face box at least this many pixels wide
CLEAR_SHARP = 200   # Laplacian variance of the aligned 112 px crop: sharp 440-1240, 7 px blur ~210, 15 px 70-90
CLEAR_YAW = 0.25    # nose within this fraction of the eye span from the middle (0 = straight on)
SAME_TRACK = 0.25   # ArcFace between neighbouring frames: same person stays well above, a cut drops near 0


def view_quality(img, f, face_align, np):
    """How usable one face view is: detector confidence, size, sharpness, how straight on."""
    import cv2
    x1, y1, x2, y2 = (float(v) for v in f.bbox)
    width = x2 - x1
    crop = face_align.norm_crop(img, f.kps, 112)
    sharp = float(cv2.Laplacian(cv2.cvtColor(crop, cv2.COLOR_BGR2GRAY), cv2.CV_64F).var())
    le, re, nose = f.kps[0], f.kps[1], f.kps[2]
    span = float(re[0] - le[0]) or 1.0
    yaw = abs((float(nose[0]) - float(le[0])) / span - 0.5)
    det = float(f.det_score)
    clear = det >= CLEAR_DET and width >= CLEAR_WIDTH and sharp >= CLEAR_SHARP and yaw <= CLEAR_YAW
    score = det * min(1.0, width / 120) * min(1.0, sharp / 200) * max(0.0, 1 - 2 * yaw)
    return {"width": round(width, 1), "sharpness": round(sharp, 1), "yaw": round(yaw, 3),
            "quality": round(score, 4), "clear": clear}


def scrub(args, detect, box, read, vec2, face_align, np):
    """Step one frame at a time outward from --scrub-from (0, +1, -1, +2, -2, ...), follow the
    target face separately in each direction, and stop at the first clear view."""
    frames = sorted(p for p in os.listdir(args.scrub_dir) if p.lower().endswith(".jpg"))
    frames = [os.path.join(args.scrub_dir, p) for p in frames]
    if not frames:
        return {"skipped": True, "reason": "no frames in the scrub folder"}
    start = min(max(args.scrub_from, 0), len(frames) - 1)
    target = [float(v) for v in args.target.split(",")] if args.target else None
    track = {1: target, -1: target}  # last box seen walking forward / backward
    last = {1: None, -1: None}       # last face embedding seen in each direction
    stopped = {1: False, -1: False}  # a cut, or someone else took the spot: stop that way
    views, looked = [], 0
    order = [start]
    for k in range(1, len(frames)):
        order += [i for i in (start + k, start - k) if 0 <= i < len(frames)]
    for i in order:
        direction = 1 if i >= start else -1
        if stopped[direction] or (i == start and stopped[-1]):
            continue
        img = read(frames[i])
        if img is None:
            continue
        looked += 1
        img, faces, dx, dy = detect(img)  # same border retry as a still photo
        at = {id(f): box(f, dx, dy) for f in faces}  # boxes in the frame's own coordinates
        ref = track[direction]
        if ref is not None:
            cx, cy, w = (ref[0] + ref[2]) / 2, (ref[1] + ref[3]) / 2, max(ref[2] - ref[0], 1.0)
            def off(f):
                b = at[id(f)]
                return abs((b[0] + b[2]) / 2 - cx), abs((b[1] + b[3]) / 2 - cy)
            near = [f for f in faces if max(off(f)) < 1.5 * w]
            near.sort(key=lambda f: sum(off(f)))
            faces = near[:1]
        else:
            faces = sorted(faces, key=lambda f: -(f.bbox[2] - f.bbox[0]) * float(f.det_score))[:1]
        if not faces:
            continue
        f = faces[0]
        emb = np.asarray(f.normed_embedding, dtype=np.float32)
        prev = last[direction] if last[direction] is not None else last[-direction]
        if prev is not None and float(emb @ prev) < SAME_TRACK:
            stopped[direction] = True  # the face here is not the one we were following
            continue
        last[direction] = emb
        if i == start:
            last[-direction] = emb
        track[direction] = at[id(f)]
        q = view_quality(img, f, face_align, np)
        rec = {"path": frames[i], "index": i, "found": True, "n_faces": 1, "bbox": track[direction], "padded": bool(dx),
               "det_score": float(f.det_score), "vector": [float(x) for x in f.normed_embedding], **q}
        if vec2 is not None:
            rec["vector2"] = vec2(img, f)
        views.append(rec)
        if q["clear"]:
            break
    views.sort(key=lambda r: -r["quality"])
    return {"dim": 512, "frames_looked": looked, "frames_total": len(frames), "faces": views[:max(args.keep, 1)]}


if __name__ == "__main__":
    try:
        main()
    except Exception as e:  # noqa: BLE001 - report cleanly to the Go caller
        print(json.dumps({"skipped": True, "reason": f"{type(e).__name__}: {e}"}))
        sys.exit(0)
