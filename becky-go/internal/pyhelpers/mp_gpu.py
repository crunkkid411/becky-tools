#!/usr/bin/env python3
"""MediaPipe pose, face mesh, gestures and drink boxes on the graphics card (Windows).

Why this exists: Jordan, 2026-10-19: "We NEED to set it up so it uses the GPU for
mediapipe". The Windows mediapipe wheel (1.0.1) is built with GPU switched off
("ImageCloneCalculator: GPU processing is disabled in build flags"), and upstream
turns its OpenGL path off on Windows. But the networks inside MediaPipe's .task
bundles run fully on the RTX 3070 through LiteRT (ai-edge-litert 2.3, WebGPU on
Direct3D 12; it needs Microsoft's dxcompiler.dll + dxil.dll next to libLiteRt.dll,
scripts/setup-picture-gpu.ps1 puts them there). Measured per network: about 14 ms
on the card against about 128 ms on the processor.

So this file runs MediaPipe's own networks and re-does MediaPipe's processing
around them, ported from the MediaPipe source (tasks/cc/vision/*_graph.cc,
calculators/util/*, calculators/tensor/*): letterboxed detector input, SSD anchors,
box decoding, weighted non-max suppression, rotated crops, heatmap refinement,
landmark projection, the next-frame tracking box and the one-euro + visibility
smoothing of VIDEO mode. picture.go's thresholds were calibrated on MediaPipe's
output, so the numbers must match it, not just look plausible
(mp_gpu_check.py compares the two on real footage).

Not ported (picture_signals.py does not use them): segmentation masks, world
landmarks of the pose, more than one pose or face, MediaPipe's hand de-duplication
(a simple box overlap check stands in).
"""
import io
import math
import os
import zipfile

import cv2
import numpy as np

# ---------------------------------------------------------------- LiteRT ----


class Net:
    """One network on the graphics card, or wholly on the processor when the card cannot
    run all of it: the pose detector (sparse weights, DENSIFY) split between the two gave
    wrong scores (best 0.11 where the processor says 0.85, 2026-10-19)."""

    def __init__(self, env, data, gpu=True):
        from ai_edge_litert.compiled_model import CompiledModel
        from ai_edge_litert.hardware_accelerator import HardwareAccelerator as HA
        self.m = CompiledModel.from_buffer(data, hardware_accel=(HA.GPU | HA.CPU) if gpu else HA.CPU, environment=env)
        if gpu and not self.m.is_fully_accelerated():
            self.m = CompiledModel.from_buffer(data, hardware_accel=HA.CPU, environment=env)
        self.sig = list(self.m.get_signature_list().keys())[0]
        self.ins = self.m.get_input_tensor_details(self.sig)
        self.outs = self.m.get_output_tensor_details(self.sig)
        self.ib = {n: self.m.create_input_buffer_by_name(self.sig, n) for n in self.ins}
        self.ob = {n: self.m.create_output_buffer_by_name(self.sig, n) for n in self.outs}

    def __call__(self, **inputs):
        for n, a in inputs.items():
            self.ib[n].write(np.ascontiguousarray(a, dtype=np.uint8 if self.ins[n]["dtype"] == "uint8" else np.float32))
        self.m.run_by_name(self.sig, self.ib, self.ob)
        return {n: self.ob[n].read(int(np.prod(d["shape"])), np.float32).reshape(d["shape"]) for n, d in self.outs.items()}

    @property
    def input(self):
        return next(iter(self.ins))


def environment():
    from ai_edge_litert.environment import Environment, EnvironmentOptions
    import ai_edge_litert
    return Environment.create(options=EnvironmentOptions(runtime_path=os.path.dirname(ai_edge_litert.__file__)))


def bundle(path):
    """{name: bytes} of every file inside a MediaPipe .task bundle (nested ones too)."""
    out = {}
    with zipfile.ZipFile(path) as z:
        for n in z.namelist():
            b = z.read(n)
            if n.endswith(".task"):
                out.update(bundle(io.BytesIO(b)))
            out[os.path.basename(n)] = b
    return out


def packed_labels(data):
    """labels.txt packed inside a .tflite (TFLite metadata appends a zip)."""
    with zipfile.ZipFile(io.BytesIO(data)) as z:
        return [l.strip() for l in z.read("labels.txt").decode("utf-8").splitlines()]


# ------------------------------------------------------------ geometry ------


def sigmoid(x):
    return 1.0 / (1.0 + np.exp(-x))


def norm_rad(a):
    return a - 2 * math.pi * math.floor((a + math.pi) / (2 * math.pi))


class Rect:
    """A NormalizedRect: centre, size as fractions of the image, rotation in radians."""
    __slots__ = ("cx", "cy", "w", "h", "rot")

    def __init__(self, cx, cy, w, h, rot=0.0):
        self.cx, self.cy, self.w, self.h, self.rot = cx, cy, w, h, rot

    def transformed(self, iw, ih, sx, sy, shift_x=0.0, shift_y=0.0, square_long=False):
        """RectTransformationCalculator::TransformNormalizedRect."""
        w, h, r = self.w, self.h, self.rot
        cx, cy = self.cx, self.cy
        if r == 0:
            cx, cy = cx + w * shift_x, cy + h * shift_y
        else:
            cx += (iw * w * shift_x * math.cos(r) - ih * h * shift_y * math.sin(r)) / iw
            cy += (iw * w * shift_x * math.sin(r) + ih * h * shift_y * math.cos(r)) / ih
        if square_long:
            s = max(w * iw, h * ih)
            w, h = s / iw, s / ih
        return Rect(cx, cy, w * sx, h * sy, r)

    def project(self, pts):
        """LandmarkProjectionCalculator: crop-space [0,1] points -> image fractions; z scales like x."""
        c, s = math.cos(self.rot), math.sin(self.rot)
        x, y = pts[:, 0] - 0.5, pts[:, 1] - 0.5
        out = pts.copy()
        out[:, 0] = (c * x - s * y) * self.w + self.cx
        out[:, 1] = (s * x + c * y) * self.h + self.cy
        if pts.shape[1] > 2:
            out[:, 2] = pts[:, 2] * self.w
        return out


def rotation(x0, y0, x1, y1, iw, ih, target):
    """DetectionsToRectsCalculator::ComputeRotation (points as image fractions)."""
    return norm_rad(target - math.atan2(-(y1 * ih - y0 * ih), x1 * iw - x0 * iw))


def crop(img, rect, iw, ih, size, zero_border):
    """ImageToTensorConverterOpenCv: the rotated rect warped to size x size."""
    rr = ((rect.cx * iw, rect.cy * ih), (rect.w * iw, rect.h * ih), rect.rot * 180.0 / math.pi)
    src = cv2.boxPoints(rr).astype(np.float32)
    dst = np.float32([[0, size], [0, 0], [size, 0], [size, size]])
    m = cv2.getPerspectiveTransform(src, dst)
    return cv2.warpPerspective(img, m, (size, size), flags=cv2.INTER_LINEAR,
                               borderMode=cv2.BORDER_CONSTANT if zero_border else cv2.BORDER_REPLICATE)


def letterbox(img, iw, ih, size):
    """The whole frame fitted into size x size with zero bars (keep_aspect_ratio): tensor, (pad_x, pad_y)."""
    w, h = float(iw), float(ih)
    if 1.0 > h / w:  # wider than tall: bars top and bottom
        pad = (1.0 - (h / w)) / 2.0
        r, pads = Rect(0.5, 0.5, 1.0, w / h), (0.0, pad)
    else:
        pad = (1.0 - (w / h)) / 2.0
        r, pads = Rect(0.5, 0.5, h / w, 1.0), (pad, 0.0)
    return crop(img, r, iw, ih, size, True), pads


def ssd_anchors(size, strides, num_layers, min_scale=0.1484375, max_scale=0.75):
    """SsdAnchorsCalculator with fixed_anchor_size, aspect ratio 1, interpolated scale 1:
    only the centres matter (w = h = 1)."""
    out, layer = [], 0
    while layer < num_layers:
        last, n = layer, 0
        while last < len(strides) and strides[last] == strides[layer]:
            n += 2  # aspect ratio 1.0 + the interpolated-scale anchor
            last += 1
        fm = int(math.ceil(size / strides[layer]))
        for y in range(fm):
            for x in range(fm):
                out.extend([((x + 0.5) / fm, (y + 0.5) / fm, 1.0, 1.0)] * n)
        layer = last
    return np.array(out, np.float32)


def decode(raw, scores, anchors, scale, num_kp, min_score):
    """TensorsToDetectionsCalculator (XYWH boxes, sigmoid scores clipped at 100) -> list of
    detections {score, box: [xmin, ymin, xmax, ymax], kp: (K, 2)} in tensor fractions."""
    s = sigmoid(np.clip(scores.reshape(-1), -100, 100))
    keep = np.nonzero(s >= min_score)[0]
    dets = []
    for i in keep:
        r, a = raw[i], anchors[i]
        cx = r[0] / scale * a[2] + a[0]
        cy = r[1] / scale * a[3] + a[1]
        w, h = r[2] / scale * a[2], r[3] / scale * a[3]
        if w < 0 or h < 0:
            continue
        kp = np.empty((num_kp, 2), np.float32)
        for k in range(num_kp):
            kp[k, 0] = r[4 + 2 * k] / scale * a[2] + a[0]
            kp[k, 1] = r[4 + 2 * k + 1] / scale * a[3] + a[1]
        dets.append({"score": float(s[i]), "box": [cx - w / 2, cy - h / 2, cx + w / 2, cy + h / 2], "kp": kp})
    return dets


def iou(a, b):
    ix = max(0.0, min(a[2], b[2]) - max(a[0], b[0]))
    iy = max(0.0, min(a[3], b[3]) - max(a[1], b[1]))
    inter = ix * iy
    union = (a[2] - a[0]) * (a[3] - a[1]) + (b[2] - b[0]) * (b[3] - b[1]) - inter
    return inter / union if union > 0 else 0.0


def weighted_nms(dets, thresh):
    """NonMaxSuppressionCalculator WEIGHTED: overlapping boxes are averaged by score."""
    rest = sorted(dets, key=lambda d: -d["score"])
    out = []
    while rest:
        top = rest[0]
        cand = [d for d in rest if iou(d["box"], top["box"]) > thresh]
        remain = [d for d in rest if iou(d["box"], top["box"]) <= thresh]
        tot = sum(d["score"] for d in cand)
        box = [sum(d["box"][j] * d["score"] for d in cand) / tot for j in range(4)]
        kp = sum(d["kp"] * d["score"] for d in cand) / tot
        out.append({"score": top["score"], "box": box, "kp": kp})
        if len(remain) == len(rest):
            break
        rest = remain
    return out


def hard_nms(dets, thresh, max_n):
    """NonMaxSuppressionCalculator DEFAULT: a box overlapping a kept, higher-scored one is dropped."""
    out = []
    for d in sorted(dets, key=lambda d: -d["score"]):
        if all(iou(d["box"], k["box"]) <= thresh for k in out):
            out.append(d)
            if len(out) == max_n:
                break
    return out


def unletterbox(d, pads):
    """DetectionLetterboxRemovalCalculator."""
    px, py = pads
    sx, sy = 1.0 - 2 * px, 1.0 - 2 * py
    b = d["box"]
    d["box"] = [(b[0] - px) / sx, (b[1] - py) / sy, (b[2] - px) / sx, (b[3] - py) / sy]
    d["kp"] = np.stack([(d["kp"][:, 0] - px) / sx, (d["kp"][:, 1] - py) / sy], 1)
    return d


def box_rect(box):
    return Rect((box[0] + box[2]) / 2, (box[1] + box[3]) / 2, box[2] - box[0], box[3] - box[1])


def to_tensor(img, lo, hi):
    """uint8 RGB -> float tensor in [lo, hi] (the model's NormalizationOptions)."""
    return (img.astype(np.float32) * ((hi - lo) / 255.0) + lo)[None]


# ------------------------------------------------------------ smoothing -----


class OneEuro:
    """util/filtering/one_euro_filter.cc (Create(): the first timestamp sets the frequency
    from -1 ns, as MediaPipe does)."""

    def __init__(self, min_cutoff, beta, dcut, freq=30.0):
        self.mc, self.beta, self.dc, self.freq = min_cutoff, beta, dcut, freq
        self.last_t = -1
        self.x = self.raw = self.dx = None

    def _alpha(self, cutoff):
        te, tau = 1.0 / self.freq, 1.0 / (2 * math.pi * cutoff)
        return 1.0 / (1.0 + tau / te)

    def __call__(self, t_ns, v, scale):
        if self.last_t >= t_ns:
            return v
        if self.last_t != 0 and t_ns != 0:
            self.freq = 1.0 / ((t_ns - self.last_t) * 1e-9)
        self.last_t = t_ns
        dv = 0.0 if self.raw is None else (v - self.raw) * scale * self.freq
        a = self._alpha(self.dc)
        self.dx = dv if self.dx is None else a * dv + (1 - a) * self.dx
        a = self._alpha(self.mc + self.beta * abs(self.dx))
        self.x = v if self.x is None else a * v + (1 - a) * self.x
        self.raw = v
        return self.x


class Smoother:
    """LandmarksSmoothingCalculator, one-euro (min_cutoff 0.05, beta 80, derivate cutoff 1),
    run in pixels, scaled by the object size (the tracking box, else the landmarks' box)."""

    def __init__(self):
        self.f = None

    def reset(self):
        self.f = None

    def __call__(self, lm, t_ms, iw, ih, roi=None):
        px = lm[:, :3] * np.float32([iw, ih, iw])
        if roi is not None:
            scale = (roi.w * iw + roi.h * ih) / 2.0
        else:
            scale = ((px[:, 0].max() - px[:, 0].min()) + (px[:, 1].max() - px[:, 1].min())) / 2.0
        if scale < 1e-6:
            return lm
        if self.f is None:
            self.f = [[OneEuro(0.05, 80.0, 1.0) for _ in range(3)] for _ in range(len(lm))]
        t = int(t_ms * 1e6)
        out = lm.copy()
        for i, fs in enumerate(self.f):
            for k in range(3):
                out[i, k] = fs[k](t, float(px[i, k]), 1.0 / scale) / (iw if k != 1 else ih)
        return out


# ---------------------------------------------------------------- pose ------

POSE_ANCHORS = None


class Pose:
    """PoseLandmarker, VIDEO mode, one pose: [33, 5] x, y, z, visibility, presence or None."""

    def __init__(self, env, task):
        global POSE_ANCHORS
        b = bundle(task)
        self.det = Net(env, b["pose_detector.tflite"])
        self.lm = Net(env, b["pose_landmarks_detector.tflite"])
        if POSE_ANCHORS is None:
            POSE_ANCHORS = ssd_anchors(224, [8, 16, 32, 32, 32], 5)
        self.rect = None
        self.smooth = Smoother()
        self.vis = None

    def _detect(self, rgb, iw, ih):
        t, pads = letterbox(rgb, iw, ih, 224)
        o = self.det(**{self.det.input: to_tensor(t, -1.0, 1.0)})
        dets = decode(o["Identity"][0], o["Identity_1"][0], POSE_ANCHORS, 224.0, 4, 0.5)
        dets = weighted_nms(dets, 0.5)
        if not dets:
            return None
        d = unletterbox(dets[0], pads)
        return self._align(d["kp"][0], d["kp"][1], iw, ih)

    @staticmethod
    def _align(c, e, iw, ih):
        """AlignmentPointsRectsCalculator (centre c, scale point e, target 90 deg) + 1.25 square."""
        size = math.hypot((e[0] - c[0]) * iw, (e[1] - c[1]) * ih) * 2.0
        r = Rect(float(c[0]), float(c[1]), size / iw, size / ih,
                 rotation(c[0], c[1], e[0], e[1], iw, ih, math.pi / 2))
        return r.transformed(iw, ih, 1.25, 1.25, square_long=True)

    def __call__(self, rgb, t_ms):
        ih, iw = rgb.shape[:2]
        rect = self.rect or self._detect(rgb, iw, ih)
        self.rect = None
        if rect is None:
            return self._lost()
        t = crop(rgb, rect, iw, ih, 256, False)
        o = self.lm(**{self.lm.input: to_tensor(t, 0.0, 1.0)})
        if float(o["Identity_1"].reshape(-1)[0]) < 0.5:
            return self._lost()
        raw = o["Identity"].reshape(39, 5)
        lm = np.empty((39, 5), np.float32)
        lm[:, 0], lm[:, 1] = raw[:, 0] / 256.0, raw[:, 1] / 256.0
        lm[:, 2] = raw[:, 2] / 256.0 / 0.4
        lm[:, 3], lm[:, 4] = sigmoid(raw[:, 3]), sigmoid(raw[:, 4])
        refine(lm, o["Identity_3"][0], 7, 0.5)
        lm = rect.project(lm)
        self.rect = self._align(lm[33], lm[34], iw, ih)
        body = lm[:33]
        if self.vis is None:
            self.vis = body[:, 3].copy()
        else:
            self.vis = 0.1 * body[:, 3] + 0.9 * self.vis
        body[:, 3] = self.vis
        return self.smooth(body, t_ms, iw, ih, self.rect)

    def _lost(self):
        self.smooth.reset()
        self.vis = None
        return None


def refine(lm, hm, kernel, min_conf):
    """RefineLandmarksFromHeatmapCalculator, on crop-space landmarks."""
    hh, hw, _ = hm.shape
    off = (kernel - 1) // 2
    for i in range(lm.shape[0]):
        col, row = int(lm[i, 0] * hw), int(lm[i, 1] * hh)
        if col < 0 or col >= hw or row < 0 or row >= hh:
            continue
        r0, r1 = max(0, row - off), min(hh, row + off + 1)
        c0, c1 = max(0, col - off), min(hw, col + off + 1)
        conf = sigmoid(hm[r0:r1, c0:c1, i])
        s = conf.sum()
        if conf.max() >= min_conf and s > 0:
            rows, cols = np.mgrid[r0:r1, c0:c1]
            lm[i, 0] = (cols * conf).sum() / hw / s
            lm[i, 1] = (rows * conf).sum() / hh / s


# ---------------------------------------------------------------- face ------

BLEND_IDX = [0, 1, 4, 5, 6, 7, 8, 10, 13, 14, 17, 21, 33, 37, 39, 40, 46, 52, 53, 54, 55, 58, 61, 63, 65, 66, 67, 70,
             78, 80, 81, 82, 84, 87, 88, 91, 93, 95, 103, 105, 107, 109, 127, 132, 133, 136, 144, 145, 146, 148, 149,
             150, 152, 153, 154, 155, 157, 158, 159, 160, 161, 162, 163, 168, 172, 173, 176, 178, 181, 185, 191, 195,
             197, 234, 246, 249, 251, 263, 267, 269, 270, 276, 282, 283, 284, 285, 288, 291, 293, 295, 296, 297, 300,
             308, 310, 311, 312, 314, 317, 318, 321, 323, 324, 332, 334, 336, 338, 356, 361, 362, 365, 373, 374, 375,
             377, 378, 379, 380, 381, 382, 384, 385, 386, 387, 388, 389, 390, 397, 398, 400, 402, 405, 409, 415, 454,
             466, 468, 469, 470, 471, 472, 473, 474, 475, 476, 477]
BLEND_NAMES = ["_neutral", "browDownLeft", "browDownRight", "browInnerUp", "browOuterUpLeft", "browOuterUpRight",
               "cheekPuff", "cheekSquintLeft", "cheekSquintRight", "eyeBlinkLeft", "eyeBlinkRight", "eyeLookDownLeft",
               "eyeLookDownRight", "eyeLookInLeft", "eyeLookInRight", "eyeLookOutLeft", "eyeLookOutRight",
               "eyeLookUpLeft", "eyeLookUpRight", "eyeSquintLeft", "eyeSquintRight", "eyeWideLeft", "eyeWideRight",
               "jawForward", "jawLeft", "jawOpen", "jawRight", "mouthClose", "mouthDimpleLeft", "mouthDimpleRight",
               "mouthFrownLeft", "mouthFrownRight", "mouthFunnel", "mouthLeft", "mouthLowerDownLeft",
               "mouthLowerDownRight", "mouthPressLeft", "mouthPressRight", "mouthPucker", "mouthRight",
               "mouthRollLower", "mouthRollUpper", "mouthShrugLower", "mouthShrugUpper", "mouthSmileLeft",
               "mouthSmileRight", "mouthStretchLeft", "mouthStretchRight", "mouthUpperUpLeft", "mouthUpperUpRight",
               "noseSneerLeft", "noseSneerRight"]
FACE_ANCHORS = None


class Face:
    """FaceLandmarker, VIDEO mode, one face: (blendshapes {name: score}, 4x4 face matrix) or None."""

    def __init__(self, env, task):
        global FACE_ANCHORS
        b = bundle(task)
        self.det = Net(env, b["face_detector.tflite"])
        self.lm = Net(env, b["face_landmarks_detector.tflite"])
        self.bs = Net(env, b["face_blendshapes.tflite"])
        if FACE_ANCHORS is None:
            FACE_ANCHORS = ssd_anchors(128, [8, 16, 16, 16], 4)
        self.canon, self.weights = geometry_metadata(b["geometry_pipeline_metadata_landmarks.binarypb"])
        self.rect = None
        self.smooth = Smoother()

    def _detect(self, rgb, iw, ih):
        t, pads = letterbox(rgb, iw, ih, 128)
        o = self.det(**{self.det.input: to_tensor(t, -1.0, 1.0)})
        dets = decode(o["regressors"][0], o["classificators"][0], FACE_ANCHORS, 128.0, 6, 0.5)
        dets = weighted_nms(dets, 0.5)
        if not dets:
            return None
        d = unletterbox(dets[0], pads)
        r = box_rect(d["box"])
        r.rot = rotation(d["kp"][0, 0], d["kp"][0, 1], d["kp"][1, 0], d["kp"][1, 1], iw, ih, 0.0)
        return r.transformed(iw, ih, 1.5, 1.5)

    def __call__(self, rgb, t_ms):
        ih, iw = rgb.shape[:2]
        rect = self.rect or self._detect(rgb, iw, ih)
        self.rect = None
        if rect is None:
            self.smooth.reset()
            return None
        t = crop(rgb, rect, iw, ih, 256, False)
        o = self.lm(**{self.lm.input: to_tensor(t, 0.0, 1.0)})
        if float(sigmoid(o["Identity_1"].reshape(-1)[0])) < 0.5:
            self.smooth.reset()
            return None
        lm = o["Identity"].reshape(478, 3) / 256.0
        lm = rect.project(lm)
        # next frame: LandmarksToDetection (box of all points) + rotation 33 -> 263, 1.5 square
        box = [lm[:, 0].min(), lm[:, 1].min(), lm[:, 0].max(), lm[:, 1].max()]
        nr = box_rect(box)
        nr.rot = rotation(lm[33, 0], lm[33, 1], lm[263, 0], lm[263, 1], iw, ih, 0.0)
        self.rect = nr.transformed(iw, ih, 1.5, 1.5, square_long=True)
        lm = self.smooth(lm, t_ms, iw, ih)
        pts = lm[BLEND_IDX, :2] * np.float32([iw, ih])
        scores = self.bs(**{self.bs.input: pts[None]})
        shapes = dict(zip(BLEND_NAMES, next(iter(scores.values())).reshape(-1).tolist()))
        return shapes, face_matrix(lm[:468], iw, ih, self.canon, self.weights)


def geometry_metadata(data):
    """geometry_pipeline_metadata.proto: canonical mesh vertices (3, 468) + Procrustes weights."""
    def fields(b):
        i = 0
        while i < len(b):
            key, i = varint(b, i)
            num, wt = key >> 3, key & 7
            if wt == 0:
                v, i = varint(b, i)
            elif wt == 2:
                n, i = varint(b, i)
                v, i = b[i:i + n], i + n
            elif wt == 5:
                v, i = b[i:i + 4], i + 4
            elif wt == 1:
                v, i = b[i:i + 8], i + 8
            else:
                raise ValueError("bad wire type")
            yield num, wt, v

    verts, basis = [], []
    for num, wt, v in fields(data):
        if num == 1:  # canonical_mesh
            for n2, w2, v2 in fields(v):
                if n2 == 3:
                    verts.extend(np.frombuffer(v2, "<f4").tolist() if w2 == 2 else [np.frombuffer(v2, "<f4")[0]])
        elif num == 2:  # procrustes_landmark_basis
            lid, wgt = 0, 0.0
            for n2, w2, v2 in fields(v):
                if n2 == 1:
                    lid = v2
                elif n2 == 2:
                    wgt = float(np.frombuffer(v2, "<f4")[0])
            basis.append((lid, wgt))
    canon = np.array(verts, np.float32).reshape(-1, 5)[:, :3].T.copy()
    weights = np.zeros(canon.shape[1], np.float32)
    for lid, wgt in basis:
        weights[lid] = wgt
    return canon, weights


def varint(b, i):
    v = s = 0
    while True:
        c = b[i]
        i += 1
        v |= (c & 0x7F) << s
        s += 7
        if c < 0x80:
            return v, i


def procrustes(src, dst, w):
    """procrustes_solver.cc: weighted orthogonal problem, 4x4 (scale * rotation, translation)."""
    sw = np.sqrt(w)
    ws, wt = src * sw, dst * sw
    total = float((sw * sw).sum())
    com = (ws * sw).sum(1) / total
    cws = ws - np.outer(com, sw)
    u, _, vt = np.linalg.svd(wt @ cws.T)
    if np.linalg.det(u) * np.linalg.det(vt) < 0:
        u[:, 2] *= -1
    rot = u @ vt
    scale = float(((rot @ cws) * wt).sum() / (cws * ws).sum())
    rs = scale * rot
    trans = ((wt - rs @ ws) * sw).sum(1) / total
    m = np.eye(4, dtype=np.float64)
    m[:3, :3], m[:3, 3] = rs, trans
    return m


def face_matrix(lm, iw, ih, canon, w):
    """geometry_pipeline.cc ScreenToMetricSpaceConverter, FACE_LANDMARK_PIPELINE, top-left
    origin, perspective camera 63 deg, near 1."""
    near = 1.0
    hn = 2.0 * near * math.tan(0.5 * math.radians(63.0))
    wn = iw * hn / ih
    left, bottom = -0.5 * wn, -0.5 * hn
    s = lm.T.astype(np.float64).copy()  # (3, 468)
    s[1] = 1.0 - s[1]
    s *= np.array([[wn], [hn], [wn]])
    s[0] += left
    s[1] += bottom
    depth = s[2].mean()
    canon = canon.astype(np.float64)

    def est_scale(p):
        return np.linalg.norm(procrustes(canon, p, w)[:, 0])

    def move_unproject(p, sc):
        p = p.copy()
        p[2] = (p[2] - depth + near) / sc
        p[0] = p[0] * p[2] / near
        p[1] = p[1] * p[2] / near
        p[2] *= -1
        return p

    inter = s.copy()
    inter[2] *= -1
    first = est_scale(inter)
    second = est_scale(move_unproject(s, first))
    metric = move_unproject(s, first * second)
    return procrustes(canon, metric, w)


# --------------------------------------------------------------- hands ------

HAND_ANCHORS = None


class Hands:
    """GestureRecognizer, VIDEO mode, two hands: [[gesture, score], ...] for the hands seen."""

    def __init__(self, env, task):
        global HAND_ANCHORS
        b = bundle(task)
        self.det = Net(env, b["hand_detector.tflite"])
        self.lm = Net(env, b["hand_landmarks_detector.tflite"])
        self.emb = Net(env, b["gesture_embedder.tflite"])
        cls = b["canned_gesture_classifier.tflite"]
        self.cls = Net(env, cls)
        self.labels = packed_labels(cls)
        if HAND_ANCHORS is None:
            HAND_ANCHORS = ssd_anchors(192, [8, 16, 16, 16], 4)
        self.rects = []

    def _detect(self, rgb, iw, ih):
        t, pads = letterbox(rgb, iw, ih, 192)
        o = self.det(**{self.det.input: to_tensor(t, 0.0, 1.0)})
        dets = decode(o["Identity"][0], o["Identity_1"][0], HAND_ANCHORS, 192.0, 7, 0.5)
        out = []
        for d in weighted_nms(dets, 0.3)[:2]:
            d = unletterbox(d, pads)
            r = box_rect(d["box"])
            r.rot = rotation(d["kp"][0, 0], d["kp"][0, 1], d["kp"][2, 0], d["kp"][2, 1], iw, ih, math.pi / 2)
            out.append(r.transformed(iw, ih, 2.6, 2.6, shift_y=-0.5, square_long=True))
        return out

    def __call__(self, rgb, t_ms):
        ih, iw = rgb.shape[:2]
        rects = list(self.rects)
        if len(rects) < 2:
            for r in self._detect(rgb, iw, ih):  # AssociationNormRectCalculator: the newer box wins
                rects = [p for p in rects if rect_iou(p, r) <= 0.5] + [r]
        rects = rects[-2:] if len(rects) > 2 else rects
        self.rects, seen, gestures = [], [], []
        for rect in rects:
            t = crop(rgb, rect, iw, ih, 224, False)
            o = self.lm(**{self.lm.input: to_tensor(t, 0.0, 1.0)})
            if float(o["Identity_1"].reshape(-1)[0]) < 0.5:
                continue
            raw = o["Identity"].reshape(21, 3)
            lm = np.stack([raw[:, 0] / 224.0, raw[:, 1] / 224.0, raw[:, 2] / 224.0 / 0.4], 1)
            lm = rect.project(lm)
            box = [lm[:, 0].min(), lm[:, 1].min(), lm[:, 0].max(), lm[:, 1].max()]
            if any(iou(box, b) > 0.5 for b in seen):  # ponytail: stands in for HandLandmarksDeduplication
                continue
            seen.append(box)
            self.rects.append(hand_rect(lm, iw, ih).transformed(iw, ih, 2.0, 2.0, shift_y=-0.1, square_long=True))
            world = o["Identity_3"].reshape(21, 3).copy()
            c, s = math.cos(rect.rot), math.sin(rect.rot)
            world[:, 0], world[:, 1] = c * world[:, 0] - s * world[:, 1], s * world[:, 0] + c * world[:, 1]
            g = self._gesture(lm, world, float(o["Identity_2"].reshape(-1)[0]), iw, ih)
            if g:
                gestures.append(g)
        return gestures

    def _gesture(self, lm, world, right, iw, ih):
        m = float(max(iw, ih))
        a = lm.copy()
        a[:, 0] = (a[:, 0] - 0.5) * (iw / m) + 0.5
        a[:, 1] = (a[:, 1] - 0.5) * (ih / m) + 0.5
        e = self.emb(hand=object_norm(a)[None], handedness=np.float32([[right]]), world_hand=object_norm(world)[None])
        p = self.cls(**{self.cls.input: next(iter(e.values()))})
        p = next(iter(p.values())).reshape(-1)
        i = int(np.argmax(p))
        return None if self.labels[i] == "None" else [self.labels[i], round(float(p[i]), 3)]


def object_norm(p):
    """LandmarksToMatrixCalculator object normalization, origin = the wrist."""
    q = p - p[0]
    scale = max(q[:, 0].max() - q[:, 0].min(), q[:, 1].max() - q[:, 1].min()) + 1e-5
    return (q / scale).astype(np.float32)


def rect_iou(a, b):
    def box(r):
        return [r.cx - r.w / 2, r.cy - r.h / 2, r.cx + r.w / 2, r.cy + r.h / 2]
    return iou(box(a), box(b))


def hand_rect(lm, iw, ih):
    """HandLandmarksToRectCalculator."""
    x0, y0 = lm[0, 0] * iw, lm[0, 1] * ih
    x1 = ((lm[4, 0] + lm[8, 0]) / 2 + lm[6, 0]) / 2 * iw
    y1 = ((lm[4, 1] + lm[8, 1]) / 2 + lm[6, 1]) / 2 * ih
    rot = norm_rad(math.pi / 2 - math.atan2(-(y1 - y0), x1 - x0))
    rev = norm_rad(-rot)
    acx, acy = (lm[:, 0].max() + lm[:, 0].min()) / 2, (lm[:, 1].max() + lm[:, 1].min()) / 2
    ox, oy = (lm[:, 0] - acx) * iw, (lm[:, 1] - acy) * ih
    px = ox * math.cos(rev) - oy * math.sin(rev)
    py = ox * math.sin(rev) + oy * math.cos(rev)
    pcx, pcy = (px.max() + px.min()) / 2, (py.max() + py.min()) / 2
    cx = pcx * math.cos(rot) - pcy * math.sin(rot) + iw * acx
    cy = pcx * math.sin(rot) + pcy * math.cos(rot) + ih * acy
    return Rect(cx / iw, cy / ih, (px.max() - px.min()) / iw, (py.max() - py.min()) / ih, rot)


# ------------------------------------------------------------- objects ------


class Objects:
    """ObjectDetector (EfficientDet-Lite0, no NMS inside the model): [{what, score, box}]."""

    def __init__(self, env, path, score=0.1, max_results=15):
        from ai_edge_litert import schema_py_generated as S
        from mediapipe.tasks.metadata import metadata_schema_py_generated as M
        from mediapipe.tasks.metadata import object_detector_metadata_schema_py_generated as O
        data = open(path, "rb").read()
        self.net = Net(env, data)
        self.labels = packed_labels(data)
        self.score, self.max = score, max_results
        model = S.Model.GetRootAsModel(data, 0)
        md = next(model.Metadata(i) for i in range(model.MetadataLength()) if model.Metadata(i).Name() == b"TFLITE_METADATA")
        mm = M.ModelMetadata.GetRootAsModelMetadata(model.Buffers(md.Buffer()).DataAsNumpy().tobytes(), 0)
        cm = next(mm.SubgraphMetadata(0).CustomMetadata(i) for i in range(mm.SubgraphMetadata(0).CustomMetadataLength())
                  if mm.SubgraphMetadata(0).CustomMetadata(i).Name() == b"DETECTOR_METADATA")
        opts = O.ObjectDetectorOptions.GetRootAs(cm.DataAsNumpy().tobytes(), 0)
        t = opts.TensorsDecodingOptions()
        self.scale = (t.XScale(), t.YScale(), t.WScale(), t.HScale())
        self.exp, self.sig = bool(t.ApplyExponentialOnBoxSize()), bool(t.SigmoidScore())
        sch = opts.SsdAnchorsOptions().FixedAnchorsSchema()
        self.anchors = np.array([[sch.Anchors(i).XCenter(), sch.Anchors(i).YCenter(), sch.Anchors(i).Width(),
                                  sch.Anchors(i).Height()] for i in range(sch.AnchorsLength())], np.float32)

    def __call__(self, rgb):
        ih, iw = rgb.shape[:2]
        t = crop(rgb, Rect(0.5, 0.5, 1.0, 1.0), iw, ih, 320, False)
        o = self.net(images=t[None])
        sc = o["output_1"][0]
        if self.sig:
            sc = sigmoid(sc)
        cls = sc.argmax(1)
        best = sc[np.arange(len(sc)), cls]
        keep = np.nonzero(best >= self.score)[0]
        raw, a = o["output_0"][0][keep], self.anchors[keep]
        xs, ys, ws, hs = self.scale
        cy = raw[:, 0] / ys * a[:, 3] + a[:, 1]
        cx = raw[:, 1] / xs * a[:, 2] + a[:, 0]
        if self.exp:
            h, w = np.exp(raw[:, 2] / hs) * a[:, 3], np.exp(raw[:, 3] / ws) * a[:, 2]
        else:
            h, w = raw[:, 2] / hs * a[:, 3], raw[:, 3] / ws * a[:, 2]
        dets = [{"score": float(best[k]), "what": self.labels[int(cls[k])],
                 "box": [float(cx[j] - w[j] / 2), float(cy[j] - h[j] / 2), float(cx[j] + w[j] / 2), float(cy[j] + h[j] / 2)]}
                for j, k in enumerate(keep) if w[j] >= 0 and h[j] >= 0]
        return hard_nms(dets, 0.3, self.max)


if __name__ == "__main__":  # self-check: python mp_gpu.py
    assert len(ssd_anchors(224, [8, 16, 32, 32, 32], 5)) == 2254  # pose_detector outputs [1, 2254, 12]
    assert len(ssd_anchors(128, [8, 16, 16, 16], 4)) == 896       # face_detector [1, 896, 16]
    assert len(ssd_anchors(192, [8, 16, 16, 16], 4)) == 2016      # hand_detector [1, 2016, 18]
    pts = np.random.default_rng(0).normal(size=(3, 50))
    a = math.radians(20)
    rot = np.array([[math.cos(a), 0, math.sin(a)], [0, 1, 0], [-math.sin(a), 0, math.cos(a)]])
    m = procrustes(pts, 2.0 * rot @ pts + np.array([[1.0], [2.0], [3.0]]), np.ones(50))
    assert np.allclose(m[:3, :3], 2.0 * rot, atol=1e-6) and np.allclose(m[:3, 3], [1, 2, 3], atol=1e-6)
    r = Rect(0.5, 0.5, 0.2, 0.4, 0.0).transformed(640, 360, 1.25, 1.25, square_long=True)
    assert abs(r.w * 640 - r.h * 360) < 1e-6 and abs(r.h * 360 - 0.4 * 360 * 1.25) < 1e-6
    print("mp_gpu self-check ok")
