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
import subprocess
import sys

# MediaPipe Pose landmark indices we use (BlazePose 33-point topology).
NOSE = 0
L_EYE, R_EYE = 2, 5
L_EAR, R_EAR = 7, 8
L_SHOULDER, R_SHOULDER = 11, 12
L_HIP, R_HIP = 23, 24


# FACE_BAND is how much of the crop width the face centre may roam across before
# the crop is pushed to follow. 0.34 keeps the face in the middle third - loose
# enough that small head movements do not drag the camera, tight enough that the
# subject is never pinned to an edge.
FACE_BAND = 0.34


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


def ema(vals, alpha):
    """One-pole exponential filter, forward in time. LAGS by construction."""
    out = []
    y = vals[0]
    for v in vals:
        y += (v - y) * alpha
        out.append(y)
    return out


def smooth_zero_phase(vals, alpha):
    """Smooth WITHOUT lag, by filtering forward and then backward.

    This is the whole reason the first version felt broken. A one-pole filter
    (held += (target - held) * ease) is CAUSAL: it only ever sees the past, so its
    output is mathematically guaranteed to trail the subject. No amount of tuning
    removes that - raising the gain just trades lag for jitter. It is the right
    tool for OBS, which is realtime and genuinely cannot see the future.

    We are not realtime. We have the entire file on disk, so we can run the filter
    forward and then run it BACKWARD over its own output. The backward pass applies
    exactly the opposite phase shift to the forward pass, and the two cancel: the
    result is zero-phase. The camera arrives WITH the subject, and because the
    curve is shaped by frames on both sides, it even leans into a move slightly
    before it happens - which is what a human operator does, because a human
    operator knows what is about to happen too.

    (Same trick as scipy's filtfilt; written out so this helper keeps no new
    dependency.)
    """
    if len(vals) < 3:
        return list(vals)
    fwd = ema(vals, alpha)
    back = ema(fwd[::-1], alpha)[::-1]
    return back


def hold_and_move(vals, deadband):
    """Give the lag-free path an operator's INTENT: hold, then commit to a move.

    Zero-phase smoothing alone tracks every real wobble, so the frame drifts
    constantly and reads as machine-made. This walks the already-smoothed, already
    lag-free path and holds position until the subject has genuinely left the
    deadband, then re-centres on them. Because it runs on a signal that has no lag,
    holding costs nothing: when it does move, it moves to where the subject IS, not
    to where they were.
    """
    if not vals:
        return []
    out = []
    held = vals[0]
    for v in vals:
        if abs(v - held) > deadband:
            # Re-centre, leaving the subject just inside the deadband so the next
            # frame does not immediately trip it again (no chatter).
            held = v - deadband if v > held else v + deadband
        out.append(held)
    return out


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--video", required=True)
    ap.add_argument("--model", required=True, help="pose_landmarker_*.task")
    ap.add_argument("--start", type=float, default=0.0)
    ap.add_argument("--end", type=float, default=0.0, help="0 = to end of file")
    ap.add_argument("--aspect", default="9:16", help="target width:height")
    ap.add_argument("--fps", type=float, default=0.0,
                    help="samples per second; 0 = EVERY FRAME (the default - this is an "
                         "offline editor, not a realtime preview, so there is no reason "
                         "to look at the subject less often than the footage shows him)")
    # Framing craft. Defaults chosen to look like a shoulders-up interview frame.
    ap.add_argument("--shoulder-frac", type=float, default=0.46,
                    help="fraction of crop WIDTH the shoulders should span")
    ap.add_argument("--eye-line", type=float, default=0.38,
                    help="where the eyes sit down the crop HEIGHT (0.38 ~ upper third)")
    ap.add_argument("--deadband", type=float, default=0.045,
                    help="subject drift, as a fraction of crop width, before the camera moves")
    ap.add_argument("--smooth", type=float, default=0.18,
                    help="0..1 zero-phase smoothing strength; LOWER is smoother. Applied "
                         "forward AND backward, so it adds no lag at any setting")
    ap.add_argument("--ffmpeg", default="ffmpeg")
    ap.add_argument("--min-head-frac", type=float, default=0.045,
                    help="reject a 'person' whose head is smaller than this fraction of "
                         "the frame width - at talking-head scale that is a false positive")
    ap.add_argument("--no-second-pass", action="store_true",
                    help="skip the fresh-eyes retry on frames the tracker lost")
    ap.add_argument("--retry-confidence", type=float, default=0.3,
                    help="detection confidence for the retry pass; lower than the "
                         "tracked pass because a frame it already failed is worth "
                         "a harder look")
    ap.add_argument("--min-visibility", type=float, default=0.5)
    ap.add_argument("--min-crop-frac", type=float, default=0.34,
                    help="crop width may never fall below this fraction of the source width")
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
    # SECOND PASS, per frame. The VIDEO-mode tracker warm-starts from the previous
    # frame, which is fast and stable but loses the subject after a hard turn or a
    # brief occlusion - and once lost it stays lost, because it keeps warm-starting
    # from a wrong pose. This IMAGE-mode landmarker has no temporal prior: it looks
    # at the frame cold. It runs ONLY on frames the tracked pass failed, so it costs
    # nothing on a clean shot and rescues the exact frames that would otherwise be
    # filled in by carrying a stale box forward.
    #
    # This is the offline licence the reference implementations take too - the
    # non-realtime example in ofxFaceTracker raises iterations 10x and attempts 4x
    # purely because it is not running live. We are not running live either.
    retry_opts = mp_vision.PoseLandmarkerOptions(
        base_options=mp_python.BaseOptions(model_asset_path=args.model),
        running_mode=mp_vision.RunningMode.IMAGE,
        num_poses=1,
        min_pose_detection_confidence=args.retry_confidence,
    )
    rescued = 0

    times, cxs, cys, widths = [], [], [], []
    miss_run, longest_miss = 0, 0
    faces_l, faces_r = [], []
    found = 0
    # fps 0 means "every frame": track at exactly the rate the footage was shot.
    sample_fps = args.fps if args.fps > 0 else src_fps
    step = 1.0 / max(sample_fps, 0.5)

    # ONE sequential decode of the window, at the sample rate.
    #
    # The first version seeked per sample with cv2.CAP_PROP_POS_MSEC. On a long
    # source that is slow AND does not reliably return the frame you asked for,
    # which is fatal once you are tracking every frame: mis-seeks read as the
    # subject teleporting. ffmpeg decoding the window once, in order, is both
    # exact and far faster.
    n_expected = int(round((end - args.start) * sample_fps))
    frame_bytes = src_w * src_h * 3
    dec = subprocess.Popen(
        [args.ffmpeg, "-v", "error", "-ss", f"{args.start:.6f}",
         "-t", f"{end - args.start:.6f}", "-i", args.video,
         "-vf", f"fps={sample_fps}", "-pix_fmt", "bgr24", "-f", "rawvideo", "-"],
        stdout=subprocess.PIPE, stderr=subprocess.PIPE)

    with mp_vision.PoseLandmarker.create_from_options(opts) as landmarker,             mp_vision.PoseLandmarker.create_from_options(retry_opts) as rescuer:
        t = args.start
        frame_i = 0
        while frame_i < n_expected:
            buf = dec.stdout.read(frame_bytes)
            if len(buf) < frame_bytes:
                break
            frame = np.frombuffer(buf, dtype=np.uint8).reshape(src_h, src_w, 3)
            t = args.start + frame_i / sample_fps
            frame_i += 1
            rgb = cv2.cvtColor(frame, cv2.COLOR_BGR2RGB)
            mp_img = mp.Image(image_format=mp.ImageFormat.SRGB, data=rgb)
            res = landmarker.detect_for_video(mp_img, int(t * 1000))
            if not res.pose_landmarks and not args.no_second_pass:
                # Tracked pass lost him: look again with fresh eyes.
                res = rescuer.detect(mp_img)
                if res.pose_landmarks:
                    rescued += 1

            cx = cy = None
            crop_w = None
            face_l = face_r = face_t = None
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

                # SCALE comes from the shoulders; POSITION comes from the FACE.
                #
                # Framing on the shoulder midpoint looks right only when someone
                # faces the camera squarely. Lean over - cutting your own hair,
                # reaching for something - and the torso centroid is nowhere near
                # your head, so the crop centres on your chest and shears the face
                # off the edge. That is what a first pass on Jordan's own footage
                # did: half a frame of empty wall, his head clipped at the right.
                # An operator puts the FACE where it belongs and uses the body only
                # to decide how wide to be.
                head_pts = [q for q in (nose, le, re, lear, rear) if q]
                if head_pts:
                    cx = sum(q[0] for q in head_pts) / len(head_pts)
                # NO fallback to the shoulder midpoint. If not one facial landmark
                # is visible, the subject is not presentable in a talking-head
                # frame - he is bent over, turned away, or out of shot - and a
                # position guessed from the torso is worse than no position at all.
                # On real footage that guess put the crop on a lamp while he was
                # leaning down out of view, and because it counted as a successful
                # detection nothing downstream could tell. A frame with no face is
                # now a MISS: it carries the last good framing forward and counts
                # toward the gap gate, so a sustained stretch of them is refused
                # rather than rendered.

                # SCALE comes from the HEAD, not the shoulders.
                #
                # Shoulder width looks like the natural scale reference until the
                # subject leans over or turns: the two shoulders collapse together
                # in x, the "span" goes tiny, and the crop zooms into a close-up of
                # the top of someone's head. That happened on Jordan's own footage
                # the moment he bent forward. Head width barely changes with pose,
                # so it is the stable ruler; shoulders are only a sanity check.
                head_w = None
                if lear and rear:
                    head_w = abs(lear[0] - rear[0])
                elif le and re:
                    head_w = abs(le[0] - re[0]) * 2.2  # eyes span ~45% of head width
                elif head_pts and len(head_pts) > 1:
                    head_w = max(q[0] for q in head_pts) - min(q[0] for q in head_pts)

                # SANITY: is this detection even at talking-head scale?
                #
                # MediaPipe will report a full skeleton with visibility 1.00 and
                # presence 1.00 for something that is not a person at all - on this
                # footage it confidently found a face inside a clear plastic object
                # while Jordan was bent out of shot, and pointed the crop at a lamp.
                # No confidence threshold catches that, because the confidence is
                # maximal. The geometry does: that phantom spanned 40 pixels of a
                # 1920-wide frame, where a real subject in this kind of shot spans
                # hundreds. A head smaller than min-head-frac of the frame is not
                # the person this crop is meant to follow.
                if head_w and head_w < args.min_head_frac * src_w:
                    head_w = None
                    cx = None

                if head_w and head_w > 1:
                    # A head-and-shoulders frame is roughly 3.4 head widths across.
                    shoulder_span = head_w * 3.4 * args.shoulder_frac / 0.46
                elif ls and rs and abs(ls[0] - rs[0]) > 1:
                    shoulder_span = abs(ls[0] - rs[0])
                else:
                    shoulder_span = None

                # Keep the whole head, with margin, as a hard constraint later.
                if head_pts:
                    face_l = min(q[0] for q in head_pts)
                    face_r = max(q[0] for q in head_pts)
                    face_t = min(q[1] for q in head_pts)
                else:
                    face_l = face_r = face_t = None

                if cx is not None and shoulder_span and shoulder_span > 1:
                    crop_w = shoulder_span / max(args.shoulder_frac, 0.05)
                    # Never tighter than a third of the frame: past that it stops
                    # being a shot of a person and becomes a texture close-up.
                    crop_w = max(crop_w, src_w * args.min_crop_frac)
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
                    miss_run = 0

            # cx can be known (from the face) while the SCALE is not (shoulders
            # out of frame, which happens constantly when someone leans over).
            # Guard on every value the path needs, not just cx: a None reaching
            # the median filter sorts against floats and kills the whole run.
            if cx is None or cy is None or crop_w is None:
                miss_run += 1
                if miss_run > longest_miss:
                    longest_miss = miss_run
                # Carry the last good framing rather than snapping to centre: a
                # missed detection is usually a blink of occlusion, not the subject
                # teleporting. Only fall back to a centre crop if nothing was ever
                # found.
                if cxs:
                    cx, cy, crop_w = cxs[-1], cys[-1], widths[-1]
                    face_l, face_r = faces_l[-1], faces_r[-1]
                else:
                    crop_w = min(src_w, src_h * aspect)
                    cx, cy = src_w / 2.0, src_h / 2.0

            times.append(round(t - args.start, 4))
            cxs.append(cx)
            cys.append(cy)
            widths.append(crop_w)
            faces_l.append(face_l if face_l is not None else cx)
            faces_r.append(face_r if face_r is not None else cx)

    try:
        dec.stdout.close()
    except OSError:
        pass
    dec.wait(timeout=30)
    cap.release()

    if not times:
        print(json.dumps({"ok": False, "reason": "no frames could be read in that window"}))
        return

    # Smooth: median filter kills jitter, then hold-and-glide gives it intent.
    # Width is smoothed hardest - a breathing zoom is far more noticeable than a
    # slow pan, so the crop size should change rarely.
    # At 30 fps a 5-frame window is only 1/6 s; scale the despike window with the
    # sample rate so it removes the same amount of TIME regardless of fps.
    k = int(max(3, round(sample_fps / 6.0)))
    if k % 2 == 0:
        k += 1
    if len(times) < k:
        k = 1
    # Despike -> LAG-FREE smooth -> operator intent. Order matters: the median
    # filter removes single-frame landmark noise, the zero-phase pass removes the
    # jitter without introducing the trailing that made the first version
    # unusable, and only then is the hold applied - on a signal that is already
    # aligned in time, so a hold never means "stale".
    dead = args.deadband * median(widths)
    cxs = hold_and_move(smooth_zero_phase(median_filter(cxs, k), args.smooth), dead)
    cys = hold_and_move(smooth_zero_phase(median_filter(cys, k), args.smooth), dead)
    # Width changes read as a zoom, which is far more noticeable than a pan, so it
    # is smoothed harder and held wider.
    widths = hold_and_move(smooth_zero_phase(median_filter(widths, k), 0.06),
                           0.12 * median(widths))

    path = []
    for t, cx, cy, cw, fl, fr in zip(times, cxs, cys, widths, faces_l, faces_r):
        ch = cw / aspect
        # Never ask for more than the source has; scale the rect down to fit.
        if cw > src_w:
            cw, ch = float(src_w), src_w / aspect
        if ch > src_h:
            ch, cw = float(src_h), src_h * aspect
        x = cx - cw / 2.0
        y = cy - ch / 2.0
        # HARD CONSTRAINT: the face must be COMPOSED, not merely present.
        #
        # "Inside the frame" is not good enough. Smoothing lags a moving subject
        # by design, so a face can sit legally inside the crop while jammed
        # against its edge - which is exactly what a first pass on Jordan's
        # footage produced: him pinned right, half the frame empty wall. Requiring
        # the face CENTRE to stay within the middle band forces the composition an
        # operator would hold, and the smoother still does the work of getting
        # there gracefully.
        fc = 0.5 * (fl + fr)
        lo = x + (0.5 - FACE_BAND / 2) * cw
        hi = x + (0.5 + FACE_BAND / 2) * cw
        if fc > hi:
            x += fc - hi
        elif fc < lo:
            x -= lo - fc

        # And the whole head stays in, with margin, whatever the band says.
        margin = 0.05 * cw
        if fr + margin > x + cw:
            x = fr + margin - cw
        if fl - margin < x:
            x = fl - margin

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
        "sampled": len(path), "found": found, "rescued": rescued,
        # The LONGEST unbroken stretch with no detection, in seconds. Average
        # coverage hides clustered misses: a clip can be 92% covered and still have
        # a dead patch where the subject simply is not on screen, and every frame in
        # that patch renders a stale box - which is how a "short" ends up framing a
        # lamp. The caller gates on this, not on the average.
        "longest_gap_s": round(longest_miss / max(sample_fps, 1.0), 3),
        "path": path,
    }))


if __name__ == "__main__":
    try:
        main()
    except Exception as e:  # noqa: BLE001 - report cleanly to the Go caller
        print(json.dumps({"ok": False, "reason": f"{type(e).__name__}: {e}"}))
        sys.exit(0)
