#!/usr/bin/env python3
"""becky-short helper: work out where the 9:16 crop should sit, frame by frame.

MediaPipe Pose Landmarker finds the subject; OpenCV reads the frames; the camera
path is smoothed here so the result looks EDITED rather than auto-generated.

Why pose and not a face box: a crop framed only on a face puts the head dead
centre and decapitates gestures. Framing on the shoulders/torso with headroom is
what a human operator does, and becky had no body-level signal at all before this.

Why this handles BOTH orientations with one path: most of Jordan's own streams are
already 1080x1920, so "reframe to 9:16" is really a push-in on the subject, while
ingested landscape footage needs a genuine pan-and-scan. Both are the same
operation - find the subject, choose a rect of the target aspect around them - so
there is one code path, not two.

Output (stdout, one JSON line):
  {"ok": true, "src_w":1080, "src_h":1920, "fps":29.97, "aspect":0.5625,
   "sampled":150, "found":148,
   "path":[{"t":0.0,"x":12.0,"y":300.0,"w":1056.0,"h":1877.0}, ...]}

x,y,w,h are a crop rect in SOURCE pixels, already clamped inside the frame, one
per sampled instant. The caller interpolates between them and hands ffmpeg a
crop+scale. Rects are whole pixels and even-sized, because odd dimensions break
yuv420p encoders.

On any failure prints {"ok": false, "reason": "..."} and exits 0, so the Go caller
surfaces a clean note instead of a stack trace (becky's degrade-never-crash rule).

Requires: mediapipe + opencv (cv2) + numpy. The Go caller sets PYTHONPATH.
"""
import argparse
import json
import os
import sys

# MediaPipe Pose landmark indices we use (BlazePose 33-point topology).
NOSE = 0
L_EYE, R_EYE = 2, 5
L_EAR, R_EAR = 7, 8
L_SHOULDER, R_SHOULDER = 11, 12
L_HIP, R_HIP = 23, 24


def parse_aspect(s):
    """'9:16' -> 0.5625 (width / height)."""
    if ":" in s:
        w, h = s.split(":", 1)
        return float(w) / float(h)
    return float(s)


def median(vals):
    v = sorted(vals)
    n = len(v)
    if n == 0:
        return 0.0
    return v[n // 2] if n % 2 else 0.5 * (v[n // 2 - 1] + v[n // 2])


def median_filter(xs, k):
    """Odd-width median filter: kills single-frame landmark jitter without the
    lag a mean introduces. Edges clamp rather than shrink the signal."""
    if k <= 1 or len(xs) < 3:
        return list(xs)
    half = k // 2
    out = []
    for i in range(len(xs)):
        lo, hi = max(0, i - half), min(len(xs), i + half + 1)
        out.append(median(xs[lo:hi]))
    return out


def smooth_path(vals, deadband, ease):
    """Hold still, then glide. This is the whole difference between a crop that
    reads as a camera operator and one that reads as a script.

    A plain moving average tracks every wobble, so the frame floats constantly and
    the eye reads it as machine-made. Instead the camera HOLDS its position until
    the subject has drifted past `deadband`, then eases toward the new target
    (critically-damped-ish, no overshoot). The result is long still holds broken by
    deliberate moves, which is what an operator actually does.
    """
    if not vals:
        return []
    held = vals[0]
    target = vals[0]
    out = []
    for v in vals:
        if abs(v - target) > deadband:
            target = v
        held += (target - held) * ease
        out.append(held)
    return out


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--video", required=True)
    ap.add_argument("--model", required=True, help="pose_landmarker_*.task")
    ap.add_argument("--start", type=float, default=0.0)
    ap.add_argument("--end", type=float, default=0.0, help="0 = to end of file")
    ap.add_argument("--aspect", default="9:16", help="target width:height")
    ap.add_argument("--fps", type=float, default=8.0, help="samples per second")
    # Framing craft. Defaults chosen to look like a shoulders-up interview frame.
    ap.add_argument("--shoulder-frac", type=float, default=0.46,
                    help="fraction of crop WIDTH the shoulders should span")
    ap.add_argument("--eye-line", type=float, default=0.38,
                    help="where the eyes sit down the crop HEIGHT (0.38 ~ upper third)")
    ap.add_argument("--deadband", type=float, default=0.045,
                    help="subject drift, as a fraction of crop width, before the camera moves")
    ap.add_argument("--ease", type=float, default=0.14, help="0..1 glide rate once moving")
    ap.add_argument("--min-visibility", type=float, default=0.5)
    args = ap.parse_args()

    import cv2
    import numpy as np
    import mediapipe as mp
    from mediapipe.tasks import python as mp_python
    from mediapipe.tasks.python import vision as mp_vision

    if not os.path.exists(args.video):
        raise FileNotFoundError(args.video)
    if not os.path.exists(args.model):
        raise FileNotFoundError(
            f"pose model not found: {args.model} (get it with scripts/get-mediapipe-models.ps1)")

    aspect = parse_aspect(args.aspect)

    cap = cv2.VideoCapture(args.video)
    if not cap.isOpened():
        raise RuntimeError(f"OpenCV could not open {args.video}")
    src_fps = cap.get(cv2.CAP_PROP_FPS) or 30.0
    src_w = int(cap.get(cv2.CAP_PROP_FRAME_WIDTH))
    src_h = int(cap.get(cv2.CAP_PROP_FRAME_HEIGHT))
    dur = (cap.get(cv2.CAP_PROP_FRAME_COUNT) or 0) / src_fps if src_fps else 0.0
    end = args.end if args.end > 0 else (args.start + dur)

    opts = mp_vision.PoseLandmarkerOptions(
        base_options=mp_python.BaseOptions(model_asset_path=args.model),
        running_mode=mp_vision.RunningMode.VIDEO,
        num_poses=1,
        min_pose_detection_confidence=0.5,
        min_tracking_confidence=0.5,
    )

    times, cxs, cys, widths = [], [], [], []
    found = 0
    step = 1.0 / max(args.fps, 0.5)

    with mp_vision.PoseLandmarker.create_from_options(opts) as landmarker:
        t = args.start
        while t < end:
            cap.set(cv2.CAP_PROP_POS_MSEC, t * 1000.0)
            ok, frame = cap.read()
            if not ok or frame is None:
                break
            rgb = cv2.cvtColor(frame, cv2.COLOR_BGR2RGB)
            mp_img = mp.Image(image_format=mp.ImageFormat.SRGB, data=rgb)
            res = landmarker.detect_for_video(mp_img, int(t * 1000))

            cx = cy = None
            crop_w = None
            if res.pose_landmarks:
                lm = res.pose_landmarks[0]

                def pt(i):
                    p = lm[i]
                    vis = getattr(p, "visibility", 1.0)
                    if vis is not None and vis < args.min_visibility:
                        return None
                    return (p.x * src_w, p.y * src_h)

                ls, rs = pt(L_SHOULDER), pt(R_SHOULDER)
                le, re = pt(L_EYE), pt(R_EYE)
                nose = pt(NOSE)
                lear, rear = pt(L_EAR), pt(R_EAR)

                # Horizontal centre: shoulder midpoint is the most stable anchor a
                # human operator would use. Fall back to the head if the shoulders
                # are out of frame or occluded.
                if ls and rs:
                    cx = 0.5 * (ls[0] + rs[0])
                    shoulder_span = abs(ls[0] - rs[0])
                elif nose:
                    cx = nose[0]
                    # No shoulders: infer scale from the head. Ear-to-ear is roughly
                    # a third of shoulder width on an adult.
                    if lear and rear:
                        shoulder_span = abs(lear[0] - rear[0]) * 3.0
                    else:
                        shoulder_span = src_w * 0.25
                else:
                    shoulder_span = None

                if cx is not None and shoulder_span and shoulder_span > 1:
                    crop_w = shoulder_span / max(args.shoulder_frac, 0.05)
                    crop_h = crop_w / aspect
                    # Vertical: put the eyes on the eye-line. Without eyes, use the
                    # nose, and without that sit the shoulders low in frame.
                    if le and re:
                        eye_y = 0.5 * (le[1] + re[1])
                    elif nose:
                        eye_y = nose[1]
                    elif ls and rs:
                        eye_y = 0.5 * (ls[1] + rs[1]) - 0.35 * crop_h
                    else:
                        eye_y = src_h * 0.4
                    cy = eye_y + (0.5 - args.eye_line) * crop_h
                    found += 1

            if cx is None:
                # Carry the last good framing rather than snapping to centre: a
                # missed detection is usually a blink of occlusion, not the subject
                # teleporting. Only fall back to a centre crop if nothing was ever
                # found.
                if cxs:
                    cx, cy, crop_w = cxs[-1], cys[-1], widths[-1]
                else:
                    crop_w = min(src_w, src_h * aspect)
                    cx, cy = src_w / 2.0, src_h / 2.0

            times.append(round(t - args.start, 4))
            cxs.append(cx)
            cys.append(cy)
            widths.append(crop_w)
            t += step

    cap.release()

    if not times:
        print(json.dumps({"ok": False, "reason": "no frames could be read in that window"}))
        return

    # Smooth: median filter kills jitter, then hold-and-glide gives it intent.
    # Width is smoothed hardest - a breathing zoom is far more noticeable than a
    # slow pan, so the crop size should change rarely.
    k = 5 if len(times) >= 5 else 1
    cxs = smooth_path(median_filter(cxs, k), deadband=args.deadband * median(widths), ease=args.ease)
    cys = smooth_path(median_filter(cys, k), deadband=args.deadband * median(widths), ease=args.ease)
    widths = smooth_path(median_filter(widths, k), deadband=0.10 * median(widths), ease=0.06)

    path = []
    for t, cx, cy, cw in zip(times, cxs, cys, widths):
        ch = cw / aspect
        # Never ask for more than the source has; scale the rect down to fit.
        if cw > src_w:
            cw, ch = float(src_w), src_w / aspect
        if ch > src_h:
            ch, cw = float(src_h), src_h * aspect
        x = cx - cw / 2.0
        y = cy - ch / 2.0
        # Clamp inside the frame: a crop rect that runs off the edge gives ffmpeg
        # black bars, which reads instantly as a broken auto-crop.
        x = max(0.0, min(x, src_w - cw))
        y = max(0.0, min(y, src_h - ch))
        # Even, whole pixels - yuv420p needs even dimensions.
        iw, ih = int(cw) & ~1, int(ch) & ~1
        ix, iy = int(x) & ~1, int(y) & ~1
        ix = min(ix, src_w - iw)
        iy = min(iy, src_h - ih)
        path.append({"t": t, "x": ix, "y": iy, "w": iw, "h": ih})

    print(json.dumps({
        "ok": True,
        "src_w": src_w, "src_h": src_h, "fps": round(src_fps, 4),
        "aspect": round(aspect, 6),
        "sampled": len(path), "found": found,
        "path": path,
    }))


if __name__ == "__main__":
    try:
        main()
    except Exception as e:  # noqa: BLE001 - report cleanly to the Go caller
        print(json.dumps({"ok": False, "reason": f"{type(e).__name__}: {e}"}))
        sys.exit(0)
