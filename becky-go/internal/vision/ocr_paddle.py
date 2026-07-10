#!/usr/bin/env python3
"""becky-ocr helper: offline scene/document OCR via RapidOCR (PaddleOCR PP-OCR
models on ONNX Runtime).

This is the ONNX-Runtime path the spec calls for: RapidOCR ships the PaddleOCR
detection + angle-classifier + recognition pipeline as ONNX models and runs them
on onnxruntime (the same engine becky already uses for insightface). The package
BUNDLES PP-OCRv4 (det+rec+cls) ONNX models, so the default path is fully offline
with no download. PP-OCRv5 (newer, +accuracy) is requested when available and
falls back to the bundled v4 if the v5 weights can't be fetched offline.

For each image (a becky-osint frame, or a single becky-vision corroboration
still), it returns the recognized text lines with per-line recognition
confidence and bounding box. To survive sideways phone footage (TEST-FEEDBACK
F1: exported frames can be 90deg-rotated), it can try the frame at 0/90/180/270
and keep the orientation with the best mean confidence.

Output (stdout, one JSON line; non-ASCII is \\uXXXX-escaped so stdout stays clean
on a cp1252 console):
  {"engine": "ppocr-v5-onnx", "results": [
     {"path": "...", "found": true, "rotation_applied": 90,
      "lines": [{"text": "...", "confidence": 0.93, "bbox": [x1,y1,x2,y2]}]}]}

On any failure prints {"skipped": true, "reason": "..."} and exits 0 so the Go
caller surfaces a clean note instead of a stack trace (graceful degrade), exactly
like face_embed.py.

Requires: rapidocr (3.x) + onnxruntime + opencv + numpy. The Go caller sets
PYTHONPATH so these resolve (the deps live in a --target site-packages dir, the
same one face_embed.py uses).
"""
import argparse
import json
import sys


def _engine_for_version(version_enum, label):
    """Build a RapidOCR engine pinned to one PP-OCR version (onnxruntime, mobile,
    CH detector + EN recognizer). Raises if that version's weights can't be built
    (e.g. offline and not cached) so the caller can fall back."""
    from rapidocr import RapidOCR, EngineType, OCRVersion, LangRec, LangDet, ModelType  # noqa: F401

    params = {
        "Det.engine_type": EngineType.ONNXRUNTIME,
        "Det.lang_type": LangDet.CH,   # v5/v6 detector is multilingual; detects Latin fine
        "Det.model_type": ModelType.MOBILE,
        "Det.ocr_version": version_enum,
        "Rec.engine_type": EngineType.ONNXRUNTIME,
        "Rec.lang_type": LangRec.EN,   # recognition is the language-specific half
        "Rec.model_type": ModelType.MOBILE,
        "Rec.ocr_version": version_enum,
    }
    return RapidOCR(params=params), label


def build_engine(engine_pref):
    """Pick the NEWEST PP-OCR version the installed rapidocr supports, so becky-ocr
    rides upstream improvements automatically. engine_pref:
      "ppocr" (default) -> try PP-OCRv6, then v5, then bundled v4
      "ppocr-v6"/"ppocr-v5" -> start at that version, then degrade
      "ppocr-v4" -> the bundled, fully-offline default only
    Each step is best-effort: a version whose weights can't be fetched offline (or
    that this rapidocr build doesn't know) is skipped, never fatal. Returns
    (engine, engine_label)."""
    from rapidocr import RapidOCR

    chain = []
    try:
        from rapidocr import OCRVersion
        want = (engine_pref or "ppocr").lower()
        # Newest-first. hasattr guards a rapidocr too old to know a version enum.
        if want in ("ppocr", "ppocr-v6") and hasattr(OCRVersion, "PPOCRV6"):
            chain.append((OCRVersion.PPOCRV6, "ppocr-v6-onnx"))
        if want in ("ppocr", "ppocr-v6", "ppocr-v5") and hasattr(OCRVersion, "PPOCRV5"):
            chain.append((OCRVersion.PPOCRV5, "ppocr-v5-onnx"))
    except Exception:  # noqa: BLE001 - rapidocr too old to select versions; use bundled
        chain = []

    for version_enum, label in chain:
        try:
            return _engine_for_version(version_enum, label)
        except Exception as e:  # noqa: BLE001 - offline / weights missing -> next
            print(f"{label} unavailable ({type(e).__name__}: {e}); trying next",
                  file=sys.stderr)

    # Bundled, fully-offline default (PP-OCRv4 det+rec+cls ONNX shipped in the wheel).
    return RapidOCR(), "ppocr-v4-onnx"


def read_image(path, cv2, np):
    """Unicode-safe image read. cv2.imread fails SILENTLY (returns None) on
    non-ASCII paths on Windows, so read the bytes via numpy and decode -- the same
    pattern face_embed.py uses."""
    data = np.fromfile(path, dtype=np.uint8)
    if data.size == 0:
        return None
    return cv2.imdecode(data, cv2.IMREAD_COLOR)


def rotate(img, deg, cv2):
    """Rotate an image clockwise by a quadrant angle (0/90/180/270)."""
    if deg == 90:
        return cv2.rotate(img, cv2.ROTATE_90_CLOCKWISE)
    if deg == 180:
        return cv2.rotate(img, cv2.ROTATE_180)
    if deg == 270:
        return cv2.rotate(img, cv2.ROTATE_90_COUNTERCLOCKWISE)
    return img


def extract_lines(result):
    """Normalize a RapidOCR result into a list of {text, confidence, bbox} dicts.
    RapidOCR 3.x returns an object exposing .txts / .scores / .boxes (boxes are
    4-point polygons); older shapes return a list of [box, text, score]. Both are
    handled so the helper is robust across versions."""
    lines = []
    txts = getattr(result, "txts", None)
    if txts is not None:
        scores = getattr(result, "scores", None) or []
        boxes = getattr(result, "boxes", None)
        for i, text in enumerate(txts):
            conf = float(scores[i]) if i < len(scores) else 0.0
            bbox = poly_to_bbox(boxes[i]) if boxes is not None and i < len(boxes) else None
            lines.append({"text": text, "confidence": conf, "bbox": bbox})
        return lines
    # Legacy list-of-triples shape.
    if isinstance(result, (list, tuple)):
        for item in result:
            try:
                box, text, score = item[0], item[1], item[2]
            except Exception:  # noqa: BLE001
                continue
            lines.append({"text": text, "confidence": float(score),
                          "bbox": poly_to_bbox(box)})
    return lines


def poly_to_bbox(poly):
    """Convert a 4-point polygon to an axis-aligned [x1,y1,x2,y2] int box."""
    if poly is None:
        return None
    xs, ys = [], []
    for pt in poly:
        try:
            xs.append(float(pt[0]))
            ys.append(float(pt[1]))
        except Exception:  # noqa: BLE001
            return None
    if not xs or not ys:
        return None
    return [int(min(xs)), int(min(ys)), int(max(xs)), int(max(ys))]


def mean_conf(lines):
    if not lines:
        return 0.0
    return sum(l["confidence"] for l in lines) / len(lines)


def ocr_image(engine, img, try_rotations, cv2):
    """OCR one image. When try_rotations is set, try 0/90/180/270 and keep the
    orientation with the highest mean recognition confidence (a full-frame 90deg
    rotation is beyond the per-box angle classifier, so we brute-force it)."""
    angles = [0, 90, 180, 270] if try_rotations else [0]
    best_lines, best_rot, best_score = [], 0, -1.0
    for deg in angles:
        view = rotate(img, deg, cv2)
        try:
            result = engine(view)
        except Exception:  # noqa: BLE001 - a single orientation failing is non-fatal
            continue
        lines = extract_lines(result)
        score = mean_conf(lines) * (1.0 + 0.01 * len(lines))  # tie-break toward more text
        if score > best_score:
            best_lines, best_rot, best_score = lines, deg, score
    return best_lines, best_rot


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("images", nargs="+", help="frame image paths to OCR")
    ap.add_argument("--engine", default="ppocr",
                    help="ppocr (auto: PP-OCRv6->v5->v4 ONNX) | ppocr-v6 | ppocr-v5 | ppocr-v4")
    ap.add_argument("--try-rotations", action="store_true",
                    help="try 0/90/180/270 and keep the best (sideways-frame fallback)")
    args = ap.parse_args()

    import numpy as np
    import cv2

    engine, label = build_engine(args.engine)

    out = []
    for path in args.images:
        rec = {"path": path, "found": False, "rotation_applied": 0, "lines": []}
        img = read_image(path, cv2, np)
        if img is None:
            rec["error"] = "unreadable image"
            out.append(rec)
            continue
        lines, rot = ocr_image(engine, img, args.try_rotations, cv2)
        rec["lines"] = lines
        rec["rotation_applied"] = rot
        rec["found"] = len(lines) > 0
        out.append(rec)

    # ensure_ascii=True keeps stdout safe on a cp1252 Windows console; the Go side
    # decodes the JSON back to proper unicode.
    print(json.dumps({"engine": label, "results": out}, ensure_ascii=True))


if __name__ == "__main__":
    try:
        main()
    except Exception as e:  # noqa: BLE001 - report cleanly to the Go caller
        print(json.dumps({"skipped": True, "reason": f"{type(e).__name__}: {e}"}))
        sys.exit(0)
