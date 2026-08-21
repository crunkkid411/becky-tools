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


def crop_width_for(head_w, shoulder_span, head_frac, shoulder_frac, src_w, min_crop_frac):
    """Crop WIDTH from the best available scale reference, or None.

    Pure on purpose: this is the one number that decides how tight the shot is,
    and the selftest asserts it without needing a video, a model or a GPU.

    The HEAD is the primary reference (see the long comment at the detection
    site: shoulders collapse in x the moment the subject leans or turns). The
    shoulder span is the fallback for a frame with no facial landmark at all.

    The head path does NOT consult shoulder_frac, and it never did - the old
    expression was `head_w * 3.4 * shoulder_frac / 0.46`, and since crop width
    was `shoulder_span / shoulder_frac`, the shoulder_frac cancelled clean out:
    0.30, 0.46, 0.58 and 0.70 all returned exactly 7.39 * head_w. A head is
    nearly always found, so --shoulder-frac did nothing on real footage. An
    earlier attempt to loosen framing with it moved the result one percentage
    point and was written off as "it is the footage".
    """
    if head_w and head_w > 1:
        w = head_w / max(head_frac, 0.02)
    elif shoulder_span and shoulder_span > 1:
        w = shoulder_span / max(shoulder_frac, 0.05)
    else:
        return None
    # Never tighter than min_crop_frac of the frame: past that it stops being a
    # shot of a person and becomes a texture close-up.
    return max(w, src_w * min_crop_frac)


def segment_bounds(times, cut_times):
    """[(lo,hi), ...] index ranges into times (and cxs/cys/widths, which are
    parallel arrays) that partition the window at each cut.

    cut_times are WINDOW-RELATIVE seconds, already filtered to strictly
    inside (0, times[-1]) by the caller. No cut_times at all returns one
    segment covering everything - IDENTICAL to the pre-Finding-2 behaviour,
    so a caller that doesn't know about shot boundaries sees no change.
    """
    if not cut_times:
        return [(0, len(times))]
    bounds = [0]
    for ct in cut_times:
        i = 0
        while i < len(times) and times[i] < ct:
            i += 1
        if 0 < i < len(times) and i != bounds[-1]:
            bounds.append(i)
    bounds.append(len(times))
    return [(bounds[j], bounds[j + 1]) for j in range(len(bounds) - 1) if bounds[j + 1] > bounds[j]]


def smooth_by_segments(vals, seg_bounds, k, alpha, deadband):
    """despike -> smooth_zero_phase -> hold_and_move, INDEPENDENTLY per
    segment of seg_bounds.

    This is the fix for Finding 2 (research/jordan-edit-reverse-engineered.md):
    smooth_zero_phase filters forward AND backward, so run across a whole clip
    it smears shot A's ending framing into shot B's start (and leans shot B's
    beginning backward into shot A) across every hard cut in between - the
    camera visibly drifts into position over the first half second of a cut
    instead of snapping, because the filter that removes lag ALSO has no
    concept of "this is a different shot now". Slicing vals at each cut and
    running the whole despike/smooth/hold pipeline independently per slice
    keeps every shot's filter blind to every other shot's frames, which is
    what a real operator's frame does too - a hard cut is not something a
    camera operator's hand smooths through.

    median_filter is included in the per-segment pipeline, not just
    smooth_zero_phase: a despike window straddling a cut boundary would leak
    the same way on a smaller scale.
    """
    out = [0.0] * len(vals)
    for lo, hi in seg_bounds:
        seg = vals[lo:hi]
        if not seg:
            continue
        out[lo:hi] = hold_and_move(smooth_zero_phase(median_filter(seg, k), alpha), deadband)
    return out


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
                    help="fraction of crop WIDTH the shoulders should span. FALLBACK ONLY: "
                         "used when no facial landmark is visible, so on talking-head "
                         "footage it almost never applies (see --head-frac)")
    # 0.212, MEASURED. The primary scale control, because the head is what the
    # crop actually tracks (a leaning subject's shoulders collapse in x; the head
    # does not).
    #
    # The number is Jordan's own, read off his vertical short with the SAME
    # head_w definition this file uses (ear-to-ear, else eye-to-eye * 2.2):
    # median 0.212 of frame width, p25 0.145, p75 0.290 over 55 frames.
    #
    # What it replaced asked for 0.135 (crop_w = head_w * 3.4 / 0.46 = 7.39 *
    # head_w) - a 36% wider shot than he frames. On 1920x1080 that request always
    # exceeded the 607px full-height width and got clamped there, so every crop
    # came back 606x1080 at y=0: a pure horizontal pan, no zoom, and - because
    # crop height was pinned to the source height - no vertical freedom for
    # --eye-line either. All 128 rects over a 32-second window, identical.
    ap.add_argument("--head-frac", type=float, default=0.212,
                    help="fraction of crop WIDTH the head should span (0.212 measured off "
                         "Jordan's own edit); LOWER is a wider shot")
    # 0.27, not 0.38, MEASURED off Jordan's own vertical edit with InsightFace
    # over all 915 frames of it (research/jordan-edit-reverse-engineered.md):
    # his face centre sits at 29.9% of frame height (p25 26.9, p75 35.5) and 90%
    # of his frames keep it in the upper 40%. The bottom half of his frame is
    # reserved for the caption block, the hands, and what is on the table.
    #
    # CAVEAT, and it is why --shoulder-frac was left alone: this only bites when
    # the crop is SMALLER than the source height. A 9:16 crop of 1920x1080 is at
    # most 608x1080 - the full height - so on footage where the subject is close
    # to camera there is no spare source above or below and NO vertical freedom
    # at all. On test-for-clips.mp4 his face is already 37.8% of the source
    # height, so it can never be smaller than that in the output and cannot be
    # moved up or down. becky's framing there is constrained, not wrong.
    ap.add_argument("--eye-line", type=float, default=0.27,
                    help="where the eyes sit down the crop HEIGHT (0.27 measured "
                         "off his own edit; 0.38 sat the subject too low)")
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
    # 0.23, not 0.34, and this one number was silently disabling ZOOM ENTIRELY on
    # every 16:9 source. The arithmetic, on 1920x1080:
    #
    #   a full-height 9:16 crop is  1080 * 9/16 = 607.5 px wide
    #   the 0.34 floor raises any narrower crop to 1920 * 0.34 = 652.8 px
    #   652.8 px at 9:16 is 1160.5 px tall, taller than the 1080 source,
    #   so the next clamp puts it straight back to 607.5 x 1080.
    #
    # The floor sat ABOVE the full-height width, so every punch-in the shoulder
    # rule asked for was rounded up and then clamped back out of existence.
    # Measured over a whole window of the BLINDFOLD master: all 128 crop rects
    # came back w=606 h=1080 y=0 - a pure horizontal pan, never a zoom, and with
    # h pinned to the source height there was no vertical freedom either, so
    # --eye-line could not do anything at all.
    #
    # Jordan does punch in. Recovered from his own vertical by SIFT+RANSAC
    # against the master and confirmed by rendering the recovered rect back out:
    # at his t=3.0s the visible master region is 446x792 - 0.232 of the source
    # width - and it matches his frame. becky's full-height crop of the same
    # instant is visibly wider, with the subject smaller and lower.
    #
    # So the floor is set just under his tightest measured shot. It still stops a
    # true texture close-up, and the composition guard below (the face centre must
    # stay in the middle band) is untouched and does the real work.
    ap.add_argument("--min-crop-frac", type=float, default=0.23,
                    help="crop width may never fall below this fraction of the source width "
                         "(0.23 = Jordan's tightest measured punch-in; above 0.316 no zoom "
                         "is possible at all on 16:9 source)")
    # Finding 2, research/jordan-edit-reverse-engineered.md: the source can
    # already carry hard cuts inside [start,end] (an already-edited window,
    # not one continuous take). becky-short's shot detector (internal/shotcut)
    # knows them already, so they are PASSED IN here rather than re-detected -
    # this helper has no scene detector of its own and should not grow one.
    ap.add_argument("--cut-times", default="",
                    help="comma-separated ABSOLUTE cut times (source seconds, same domain as "
                         "--start/--end) inside the window; the zero-phase smoother resets at "
                         "each one instead of blending shot A's framing into shot B across it")
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
    # seens[i] is whether THIS sample had a REAL detection, as opposed to
    # carrying the last good framing forward. The caller needs it to use the path
    # where it is real instead of throwing the whole path away because of one
    # dead stretch - see cmd/becky-short's spliceTracked.
    seens = []
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

                if ls and rs and abs(ls[0] - rs[0]) > 1:
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

                crop_w = crop_width_for(head_w, shoulder_span, args.head_frac,
                                        args.shoulder_frac, src_w, args.min_crop_frac)
                if cx is not None and crop_w:
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
            seen = not (cx is None or cy is None or crop_w is None)
            if not seen:
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
            seens.append(seen)
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

    # Cut times arrive ABSOLUTE (source seconds); times[] is WINDOW-relative
    # (see the `round(t - args.start, 4)` above), so shift into that domain
    # and drop anything outside the window - a cut passed in from a wider
    # detection window than this call's [start,end] must not split it.
    cut_times = []
    if args.cut_times.strip():
        for tok in args.cut_times.split(","):
            tok = tok.strip()
            if not tok:
                continue
            ct = float(tok) - args.start
            if 0 < ct < times[-1]:
                cut_times.append(ct)
    cut_times.sort()
    seg_bounds = segment_bounds(times, cut_times)

    cxs = smooth_by_segments(cxs, seg_bounds, k, args.smooth, dead)
    cys = smooth_by_segments(cys, seg_bounds, k, args.smooth, dead)
    # Width changes read as a zoom, which is far more noticeable than a pan, so it
    # is smoothed harder and held wider.
    widths = smooth_by_segments(widths, seg_bounds, k, 0.06, 0.12 * median(widths))

    path = []
    for t, sn, cx, cy, cw, fl, fr in zip(times, seens, cxs, cys, widths, faces_l, faces_r):
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
        path.append({"t": t, "x": ix, "y": iy, "w": iw, "h": ih, "seen": bool(sn)})

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
        # The gap AT THE VERY END - miss_run as the loop left it. A short must
        # not END on footage with nothing to look at, and the whole-span gates
        # cannot see this: a span that opens on the subject and closes on a
        # blocked lens passes both coverage and longest-gap while still leaving
        # the viewer on two seconds of somebody's shirt. Measured on the
        # BLINDFOLD master, 553-555s. deadtail.go trims the final span by this.
        "trailing_gap_s": round(miss_run / max(sample_fps, 1.0), 3),
        "path": path,
    }))


def _selftest():
    """Offline proof for the Finding-2 fix: no video, no model, no network.

    Asserts VALUES, not truthiness. The first check is a REGRESSION GUARD -
    it proves the un-segmented smoother actually DOES bleed across a hard
    cut, so the rest of this test is checking a real bug, not a strawman.
    """
    ok = True

    def check(name, cond, detail=""):
        nonlocal ok
        print(f"  {'PASS' if cond else 'FAIL'}  {name}" + (f" - {detail}" if detail and not cond else ""))
        if not cond:
            ok = False

    # Two shots, hard cut at frame 20: shot A holds 0.0, shot B holds 100.0.
    # A real camera does not drift toward the next shot's framing before the
    # cut happens.
    vals = [0.0] * 20 + [100.0] * 20

    whole = smooth_zero_phase(median_filter(vals, 5), 0.18)
    check("regression guard: the WHOLE-ARRAY smoother DOES bleed across a hard cut "
          "(if this ever fails, --cut-times has nothing left to fix)",
          abs(whole[19]) > 5.0 or abs(whole[20] - 100.0) > 5.0,
          f"frame19={whole[19]:.2f} frame20={whole[20]:.2f}")

    bounds = segment_bounds(list(range(40)), [20])
    check("segment_bounds splits exactly at the cut frame", bounds == [(0, 20), (20, 40)], str(bounds))

    fixed = smooth_by_segments(vals, bounds, 5, 0.18, 1.0)
    check("segmented smoothing holds shot A flat at its own value up to the cut",
          abs(fixed[19] - 0.0) < 0.01, f"frame19={fixed[19]:.4f}")
    check("segmented smoothing holds shot B flat at its own value from the cut",
          abs(fixed[20] - 100.0) < 0.01, f"frame20={fixed[20]:.4f}")
    check("shot B's value never bleeds backward into shot A",
          max(fixed[:20]) < 1.0, f"max(shotA)={max(fixed[:20]):.4f}")
    check("shot A's value never bleeds forward into shot B",
          min(fixed[20:]) > 99.0, f"min(shotB)={min(fixed[20:]):.4f}")

    check("no cut times = one segment covering everything (unchanged old behaviour)",
          segment_bounds(list(range(40)), []) == [(0, 40)], "")

    # Three shots: two cuts must produce three independent segments, not two.
    three = segment_bounds(list(range(30)), [10, 20])
    check("two cuts split into three segments", three == [(0, 10), (10, 20), (20, 30)], str(three))

    # ---- framing scale: the two bugs that between them disabled ZOOM entirely ----
    SRC_W, SRC_H, ASPECT = 1920, 1080, 9 / 16
    FULL_H_W = SRC_H * ASPECT          # 607.5 - the widest 9:16 crop a 16:9 source allows

    # Bug 1: --head-frac was not connected. It has to move the shot.
    tight = crop_width_for(180, None, 0.29, 0.46, SRC_W, 0.23)
    wide = crop_width_for(180, None, 0.145, 0.46, SRC_W, 0.23)
    check("a tighter --head-frac gives a narrower crop",
          tight < wide - 100, f"head_frac 0.29 -> {tight:.0f}px, 0.145 -> {wide:.0f}px")
    check("the head path ignores --shoulder-frac (it is the fallback reference, by design)",
          crop_width_for(180, None, 0.212, 0.30, SRC_W, 0.23)
          == crop_width_for(180, None, 0.212, 0.70, SRC_W, 0.23), "")
    check("the shoulder fallback still responds to --shoulder-frac",
          crop_width_for(None, 400, 0.212, 0.70, SRC_W, 0.23)
          < crop_width_for(None, 400, 0.212, 0.30, SRC_W, 0.23), "")

    # Bug 2, and it is pure arithmetic: a --min-crop-frac floor ABOVE the
    # full-height width makes a punch-in impossible on ANY 16:9 source. The floor
    # raises the crop past what fits, the aspect clamp puts it straight back to
    # full height, and the result is a pure horizontal pan forever - which also
    # pins crop height to the source height, leaving --eye-line nothing to move.
    check("REGRESSION GUARD: the old 0.34 floor really did make a punch-in impossible",
          crop_width_for(180, None, 0.212, 0.46, SRC_W, 0.34) > FULL_H_W,
          f"floor 0.34 -> {crop_width_for(180, None, 0.212, 0.46, SRC_W, 0.34):.0f}px "
          f"vs full-height {FULL_H_W:.0f}px")
    check("the shipped --min-crop-frac leaves room to punch in on 16:9 source",
          SRC_W * 0.23 < FULL_H_W, f"{SRC_W * 0.23:.0f}px vs {FULL_H_W:.0f}px")
    check("at the shipped defaults a close subject DOES produce a punch-in",
          crop_width_for(120, None, 0.212, 0.46, SRC_W, 0.23) < FULL_H_W,
          f"head_w=120 -> {crop_width_for(120, None, 0.212, 0.46, SRC_W, 0.23):.0f}px")
    check("no scale reference at all returns None rather than guessing",
          crop_width_for(None, None, 0.212, 0.46, SRC_W, 0.23) is None, "")

    print(f"\n{'ALL PASS' if ok else 'SOME FAILED'}")
    return 0 if ok else 1


if __name__ == "__main__":
    if "--selftest" in sys.argv:
        sys.exit(_selftest())
    try:
        main()
    except Exception as e:  # noqa: BLE001 - report cleanly to the Go caller
        print(json.dumps({"ok": False, "reason": f"{type(e).__name__}: {e}"}))
        sys.exit(0)
