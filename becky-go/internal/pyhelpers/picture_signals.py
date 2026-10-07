#!/usr/bin/env python3
"""becky picture-signal helper: what the face and the body do inside short spans.

Why this exists: Jordan, 2026-10-06, on becky-livestream's breath check: "text
based editing is not enough" - a breath during a big movement, a held facial
expression after "Some of my videos got restored", a toast after "cheers" are
all visible, and a sound alone cannot tell them apart. The first breath check
measured whole-frame pixel change, which cannot tell a hand in the hair from a
still face. This samples each span at --fps and reports, per frame:

  face   [centre x, centre y, width] of the largest face, as fractions of the
         frame (insightface buffalo_l detection)
  mouth  inner-lip opening / outer-eye distance (insightface 68-point 3D
         landmarks 62/66 over 36/45); about 0.05 closed, 0.25+ wide open
  body   shoulders and wrists [x, y, visibility] (MediaPipe pose landmarker,
         VIDEO mode inside a span)
  jaw    MediaPipe Face Landmarker's jawOpen expression score (0-1): a second,
         independent face model for the held-open mouth
  objs   MediaPipe Object Detector (EfficientDet-Lite0) boxes for a bottle, cup
         or wine glass scoring 0.1+: {"what", "score", "area" (share of the
         frame), "cy" (box centre, 0 = top)} - the toast after "cheers"

No decisions here: becky-livestream's breath.go and picture.go judge the numbers.
Calibration on Jordan's footage: research/breath-vs-movement-sound-labels.md and
research/mediapipe-capabilities-2026-10.md.

Input:  --video <file> --spans <JSON file: [[a, b], ...] in seconds>
        --face-root <insightface root holding models/buffalo_l> --pose <pose_landmarker_*.task>
        --face-mesh <face_landmarker.task> --objects <efficientdet_lite0_int8.tflite>
Output: ONE JSON line on stdout:
  {"ok": true, "seconds": 12.3, "frames": [{"t": 12.3, "face": [0.5, 0.3, 0.2], "mouth": 0.06,
    "body": {"ls": [x, y, vis], "rs": [...], "lw": [...], "rw": [...]}, "jaw": 0.12,
    "objs": [{"what": "bottle", "score": 0.48, "area": 0.121, "cy": 0.33}]}, ...]}
  or {"ok": false, "reason": "..."}
A span that cannot be decoded is skipped (its frames are simply missing); Go
treats a span with too few frames as "the picture could not be checked".
"""
import argparse
import contextlib
import json
import os
import struct
import subprocess
import sys
import time

BODY = (("ls", 11), ("rs", 12), ("lw", 15), ("rw", 16))
LONG_SIDE = 640  # frames are scaled so the long side is 640 px; detection runs at 320
HELD = ("bottle", "cup", "wine glass")  # COCO names a raised drink can come back as


def fail(reason):
    print(json.dumps({"ok": False, "reason": reason}))
    sys.exit(0)


def bmp_frames(raw):
    """Split an ffmpeg image2pipe BMP stream into single BMP files."""
    i = 0
    while i + 6 <= len(raw) and raw[i:i + 2] == b"BM":
        n = struct.unpack_from("<I", raw, i + 2)[0]
        if n <= 0 or i + n > len(raw):
            break
        yield raw[i:i + n]
        i += n


def decode(ffmpeg, video, a, b, fps):
    """BGR frames of [a, b) at fps. ffmpeg applies the file's rotation, so a
    phone video coded sideways comes out the way it is shown."""
    scale = "scale='if(gt(iw,ih),%d,-2)':'if(gt(iw,ih),-2,%d)'" % (LONG_SIDE, LONG_SIDE)
    cmd = [ffmpeg, "-v", "error", "-ss", "%.3f" % a, "-t", "%.3f" % max(b - a, 0.05), "-i", video,
           "-vf", "fps=%g,%s" % (fps, scale), "-f", "image2pipe", "-vcodec", "bmp", "-"]
    raw = subprocess.run(cmd, capture_output=True, timeout=600).stdout
    return list(bmp_frames(raw))


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--video", required=True)
    ap.add_argument("--spans", required=True)
    ap.add_argument("--face-root", required=True)
    ap.add_argument("--pose", required=True)
    ap.add_argument("--face-mesh", required=True)
    ap.add_argument("--objects", required=True)
    ap.add_argument("--ffmpeg", default="ffmpeg")
    ap.add_argument("--fps", type=float, default=10.0)
    a = ap.parse_args()

    for p in (a.video, a.pose, a.face_mesh, a.objects, os.path.join(a.face_root, "models", "buffalo_l")):
        if not os.path.exists(p):
            fail("missing: " + p)
    try:
        spans = json.load(open(a.spans, encoding="utf-8"))
        import cv2
        import numpy as np
        with contextlib.redirect_stdout(sys.stderr):  # insightface prints its model list
            from insightface.app import FaceAnalysis
            app = FaceAnalysis(name="buffalo_l", root=a.face_root, allowed_modules=["detection", "landmark_3d_68"],
                               providers=["CPUExecutionProvider"])
            app.prepare(ctx_id=-1, det_size=(320, 320))
        import mediapipe as mp
        from mediapipe.tasks import python as mpt
        from mediapipe.tasks.python import vision as mpv
        video = mpv.RunningMode.VIDEO
        pose = mpv.PoseLandmarker.create_from_options(mpv.PoseLandmarkerOptions(
            base_options=mpt.BaseOptions(model_asset_path=a.pose), running_mode=video, num_poses=1))
        mesh = mpv.FaceLandmarker.create_from_options(mpv.FaceLandmarkerOptions(
            base_options=mpt.BaseOptions(model_asset_path=a.face_mesh), running_mode=video, num_faces=1,
            output_face_blendshapes=True))
        objects = mpv.ObjectDetector.create_from_options(mpv.ObjectDetectorOptions(
            base_options=mpt.BaseOptions(model_asset_path=a.objects), running_mode=video,
            score_threshold=0.1, max_results=15))
    except Exception as e:  # noqa: BLE001 - any setup failure is one typed answer
        fail("the picture models could not start: %s" % e)

    t0 = time.time()
    rows = []
    ts_ms = 0
    step = int(round(1000 / a.fps))
    with pose, mesh, objects:
        for s0, s1 in spans:
            try:
                frames = decode(a.ffmpeg, a.video, s0, s1, a.fps)
            except Exception as e:  # noqa: BLE001 - one bad span must not cost the rest
                print("span %.2f-%.2f: %s" % (s0, s1, e), file=sys.stderr)
                continue
            for i, buf in enumerate(frames):
                img = cv2.imdecode(np.frombuffer(buf, np.uint8), cv2.IMREAD_COLOR)
                if img is None:
                    continue
                h, w = img.shape[:2]
                r = {"t": round(s0 + i / a.fps, 3)}
                faces = app.get(img)
                if faces:
                    f = max(faces, key=lambda f: (f.bbox[2] - f.bbox[0]) * (f.bbox[3] - f.bbox[1]))
                    x1, y1, x2, y2 = (float(v) for v in f.bbox)
                    lm = f.landmark_3d_68
                    eye = float(np.linalg.norm(lm[36, :2] - lm[45, :2])) + 1e-6
                    r["face"] = [round((x1 + x2) / 2 / w, 4), round((y1 + y2) / 2 / h, 4), round((x2 - x1) / w, 4)]
                    r["mouth"] = round(float(np.linalg.norm(lm[62, :2] - lm[66, :2])) / eye, 3)
                ts_ms += step
                rgb = mp.Image(image_format=mp.ImageFormat.SRGB, data=np.ascontiguousarray(img[:, :, ::-1]))
                res = pose.detect_for_video(rgb, ts_ms)
                if res.pose_landmarks:
                    lms = res.pose_landmarks[0]
                    r["body"] = {k: [round(lms[j].x, 4), round(lms[j].y, 4), round(lms[j].visibility, 2)] for k, j in BODY}
                fm = mesh.detect_for_video(rgb, ts_ms)
                if fm.face_blendshapes:
                    jaw = [c.score for c in fm.face_blendshapes[0] if c.category_name == "jawOpen"]
                    if jaw:
                        r["jaw"] = round(float(jaw[0]), 3)
                objs = []
                for d in objects.detect_for_video(rgb, ts_ms).detections:
                    c, bb = d.categories[0], d.bounding_box
                    if c.category_name in HELD:
                        objs.append({"what": c.category_name, "score": round(float(c.score), 3),
                                     "area": round(bb.width * bb.height / float(w * h), 4),
                                     "cy": round((bb.origin_y + bb.height / 2) / float(h), 3)})
                if objs:
                    r["objs"] = objs
                rows.append(r)
            ts_ms += 1000  # a gap between spans, so the tracker does not smooth across them
    print(json.dumps({"ok": True, "seconds": round(time.time() - t0, 1), "frames": rows}))


if __name__ == "__main__":
    main()
