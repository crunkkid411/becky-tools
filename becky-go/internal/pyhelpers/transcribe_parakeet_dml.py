#!/usr/bin/env python3
"""becky-transcribe GPU helper: Parakeet-TDT via onnx-asr on DirectML.

Runs the SAME Parakeet model as transcribe_parakeet.py, but through onnx-asr +
onnxruntime-directml, which accelerates the int8 model on the GPU through
DirectX 12 with NO CUDA / cuDNN setup -- the exact approach the Rust app "Handy"
uses. Measured ~4-5x faster than the sherpa-onnx CPU path on an RTX 3070 (about
57x realtime), so a 2-hour stream transcribes in ~2 minutes instead of ~15. If
DirectML can't load it falls back to CPU, so it never hard-fails.

It emits the SAME one-line JSON contract as transcribe_parakeet.py so the Go
caller (cmd/transcribe) is unchanged:

  {"model","version","language","text",
   "words":[{"word","start","end","confidence"}],
   "device","fell_back"[,"fallback_reason"]}

Long files are decoded in fixed 30s windows (Parakeet's safe context) with a 2s
overlap; each window owns the words whose start falls in its non-overlap stride,
so there are no seam duplicates (the same scheme as the sherpa helper). On any
failure it prints {"skipped":true,"reason":...} and exits 0 so the Go caller
surfaces a clean error instead of a stack trace.

Setup (scripts/setup-asr-gpu.ps1): a venv with `onnx-asr`, `onnxruntime-directml`,
`soundfile`, `huggingface_hub`. The model is the int8 Parakeet (v3 by default),
the same family becky already uses.
"""
import argparse
import json
import math
import os
import sys


def _log(msg):
    sys.stderr.write(msg + "\n")


def read_audio(path):
    """Read a WAV as float32 mono + its sample rate (onnx-asr resamples to 16k
    internally if needed). The Go caller already writes 16 kHz mono."""
    import soundfile as sf

    audio, sr = sf.read(path, dtype="float32")
    if getattr(audio, "ndim", 1) > 1:
        audio = audio.mean(axis=1)
    return audio, sr


def providers_for(device):
    """Map --device to an onnxruntime provider list. 'auto' prefers DirectML
    (GPU) then CPU; 'dml' is GPU-only; 'cpu' is CPU-only."""
    d = (device or "auto").lower()
    if d == "cpu":
        return ["CPUExecutionProvider"], "cpu"
    if d in ("dml", "directml", "gpu"):
        return ["DmlExecutionProvider"], "dml"
    return ["DmlExecutionProvider", "CPUExecutionProvider"], "auto"


def merge_tokens_to_words(tokens, times, logprobs):
    """Merge onnx-asr BPE tokens into words. A token with a leading space (or the
    NeMo '_' marker) starts a new word; the rest continue it. Times are per-token
    START seconds (chunk-relative). Confidence = exp(mean token logprob), a real
    0..1 signal for downstream confidence checks. Returns chunk-relative words."""
    words = []
    cur, start, end, lps = "", None, None, []

    def flush():
        if cur.strip():
            conf = None
            if lps:
                conf = round(min(1.0, math.exp(sum(lps) / len(lps))), 4)
            words.append({
                "word": cur.strip(),
                "start": round(start or 0.0, 3),
                "end": round(end if end is not None else (start or 0.0), 3),
                "confidence": conf,
            })

    for i, tok in enumerate(tokens):
        t = float(times[i]) if i < len(times) else (end or 0.0)
        lp = float(logprobs[i]) if (logprobs is not None and i < len(logprobs)) else None
        if tok.startswith(" ") or tok.startswith("▁"):
            flush()
            cur, start, end, lps = tok.lstrip(" ▁"), t, t, ([lp] if lp is not None else [])
        else:
            if start is None:
                start = t
            cur += tok.lstrip(" ▁")
            end = t
            if lp is not None:
                lps.append(lp)
    flush()
    return words


def own_window_words(words, offset, win_lo, win_hi, is_last):
    """Shift chunk-relative words to absolute time and keep only those this
    window owns: start in [win_lo, win_hi). The LAST window keeps everything to
    the end. This dedups the overlap tail (the next window owns it)."""
    out = []
    for w in words:
        start = round(w["start"] + offset, 3)
        if start < win_lo:
            continue
        if not is_last and start >= win_hi:
            continue
        out.append({
            "word": w["word"],
            "start": start,
            "end": round(w["end"] + offset, 3),
            "confidence": w.get("confidence"),
        })
    return out


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("audio")
    ap.add_argument("--model-name", default="nemo-parakeet-tdt-0.6b-v3",
                    help="onnx-asr model id (downloaded/cached) when --model-dir is empty")
    ap.add_argument("--model-dir", default="",
                    help="local onnx-asr model dir (encoder/decoder_joint/nemo128); "
                         "overrides --model-name")
    ap.add_argument("--quantization", default="int8")
    ap.add_argument("--device", default="auto")  # auto | dml | cpu
    ap.add_argument("--lang", default="en")
    ap.add_argument("--chunk-seconds", type=float, default=30.0)
    ap.add_argument("--chunk-overlap", type=float, default=2.0)
    args = ap.parse_args()

    try:
        import onnxruntime as ort

        ort.set_default_logger_severity(3)  # silence DML "node not assigned" spam
    except Exception:  # noqa: BLE001 - logging tweak is best-effort
        pass

    try:
        import onnx_asr
    except Exception as e:  # noqa: BLE001
        print(json.dumps({"skipped": True, "reason": f"onnx-asr not available: {e}"}))
        sys.exit(0)

    audio, sr = read_audio(args.audio)
    providers, want = providers_for(args.device)

    # Load the model; on a DirectML failure, fall back to CPU once.
    fell_back = False
    fallback_reason = None
    load_kwargs = dict(quantization=args.quantization)
    name_or_path = (args.model_name, args.model_dir or None)
    try:
        model = onnx_asr.load_model(*name_or_path, providers=providers, **load_kwargs)
        device = "dml" if providers and providers[0].startswith("Dml") else "cpu"
    except Exception as e:  # noqa: BLE001 - DML init can fail; retry on CPU
        if any(p.startswith("Dml") for p in providers):
            fell_back = True
            fallback_reason = f"{type(e).__name__}: {e}"[:300]
            _log(f"[transcribe-dml] DirectML load failed ({type(e).__name__}); falling back to CPU")
            model = onnx_asr.load_model(*name_or_path, providers=["CPUExecutionProvider"], **load_kwargs)
            device = "cpu"
        else:
            print(json.dumps({"skipped": True, "reason": f"model load failed: {e}"}))
            sys.exit(0)

    ts_model = model.with_timestamps()

    step = max(0.1, args.chunk_seconds)
    overlap = max(0.0, args.chunk_overlap)
    step_n = int(round(step * sr))
    win_n = step_n + int(round(overlap * sr))
    total = len(audio)
    if step_n >= total:
        num_windows = 1
    else:
        num_windows = (total + step_n - 1) // step_n

    all_words = []
    for i in range(num_windows):
        s = i * step_n
        is_last = i == num_windows - 1
        seg = audio[s:] if is_last else audio[s:s + win_n]
        if len(seg) == 0:
            continue
        try:
            r = ts_model.recognize(seg, sample_rate=sr)
        except Exception as e:  # noqa: BLE001 - a bad window must not kill the file
            _log(f"[transcribe-dml] window {i + 1}/{num_windows} failed: {type(e).__name__}: {e}")
            continue
        rel = merge_tokens_to_words(list(r.tokens), list(r.timestamps),
                                    list(r.logprobs) if r.logprobs is not None else None)
        offset = round(s / sr, 3)
        win_lo = offset
        win_hi = round((s + step_n) / sr, 3)
        all_words.extend(own_window_words(rel, offset, win_lo, win_hi, is_last))

    text = " ".join(w["word"] for w in all_words).strip()
    out = {
        "model": f"onnx-asr-{args.model_name}",
        "version": "v3" if "v3" in args.model_name else ("v2" if "v2" in args.model_name else ""),
        "language": args.lang,
        "text": text,
        "words": all_words,
        "device": device,
        "fell_back": fell_back,
    }
    if fell_back and fallback_reason:
        out["fallback_reason"] = fallback_reason
    print(json.dumps(out))


if __name__ == "__main__":
    try:
        main()
    except Exception as e:  # noqa: BLE001 - report cleanly to the Go caller
        print(json.dumps({"skipped": True, "reason": f"{type(e).__name__}: {e}"}))
        sys.exit(0)
